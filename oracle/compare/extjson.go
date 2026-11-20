// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package compare

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// ExtJSON reports to tb the first difference between the vendor's and the
// library's Extended JSON text of goValue, a document.
//
// It is Encoded's rule (spec 6.1) applied to text: it walks goValue beside
// the two texts, and a level that comes from a Go map (or a struct's inline
// map, after the struct's own fields) compares as a set of members keyed by
// name, because the vendor's map order is random; every other level
// compares member by member in order. Everything that is not a document or
// array level of the Go value (a scalar, a wrapper such as
// {"$numberInt":"1"}, a level behind a MarshalBSON method or inside a
// bson.Raw) compares byte for byte, so a changed number form, escape or
// wrapper is a difference. Keys compare as written, escapes included.
//
// Comparing by reading both texts back is not an option: the reader would
// make 1.0 and 1.00, or \u003c and <, the same.
func ExtJSON(tb testing.TB, goValue any, vendorText, libText []byte) {
	tb.Helper()
	if err := extJSONDifference(goValue, vendorText, libText); err != nil {
		tb.Errorf("compare.ExtJSON(%T): %v\n vendor:  %s\n library: %s", goValue, err, vendorText, libText)
	}
}

func extJSONDifference(goValue any, vendorText, libText []byte) error {
	vendorNode, vendorErr := parseJSONText(quotedDBPointerNamespaces(vendorText))
	libNode, libErr := parseJSONText(quotedDBPointerNamespaces(libText))
	if vendorErr != nil || libErr != nil {
		// A text that is still not JSON compares raw.
		if !bytes.Equal(vendorText, libText) {
			return fmt.Errorf("$: the texts differ and one is not JSON (vendor: %v, library: %v)", vendorErr, libErr)
		}
		return nil
	}
	return compareJSONDocuments("$", documentShapeOf(reflect.ValueOf(goValue), false), vendorNode, libNode)
}

// jsonNode is one JSON value: an object or an array with its members, or a
// leaf. raw is the value's text as written.
type jsonNode struct {
	kind     byte // '{', '[' or 0 for a leaf
	raw      []byte
	rawKeys  [][]byte
	keys     []string
	children []jsonNode
}

func parseJSONText(text []byte) (jsonNode, error) {
	textParser := jsonTextParser{text: text}
	node, err := textParser.value()
	if err != nil {
		return jsonNode{}, err
	}
	textParser.skipSpace()
	if textParser.position != len(text) {
		return jsonNode{}, fmt.Errorf("text after the value at byte %d", textParser.position)
	}
	return node, nil
}

// jsonTextParser splits JSON text into nodes. It checks only what it needs
// to find each value's end; the vendor's writer is the source of the text.
type jsonTextParser struct {
	text     []byte
	position int
}

var errJSONEnd = errors.New("the text ends inside a value")

func (textParser *jsonTextParser) skipSpace() {
	for textParser.position < len(textParser.text) && strings.IndexByte(" \t\r\n", textParser.text[textParser.position]) >= 0 {
		textParser.position++
	}
}

func (textParser *jsonTextParser) value() (jsonNode, error) {
	textParser.skipSpace()
	if textParser.position >= len(textParser.text) {
		return jsonNode{}, errJSONEnd
	}
	start := textParser.position
	switch textParser.text[start] {
	case '{':
		return textParser.container('{', '}', start)
	case '[':
		return textParser.container('[', ']', start)
	case '"':
		if err := textParser.skipString(); err != nil {
			return jsonNode{}, err
		}
	default:
		for textParser.position < len(textParser.text) && strings.IndexByte(",}] \t\r\n", textParser.text[textParser.position]) < 0 {
			textParser.position++
		}
		if textParser.position == start {
			return jsonNode{}, fmt.Errorf("no value at byte %d", start)
		}
	}
	return jsonNode{raw: textParser.text[start:textParser.position]}, nil
}

func (textParser *jsonTextParser) skipString() error {
	textParser.position++
	for textParser.position < len(textParser.text) {
		switch textParser.text[textParser.position] {
		case '\\':
			textParser.position += 2
			continue
		case '"':
			textParser.position++
			return nil
		}
		textParser.position++
	}
	return errJSONEnd
}

func (textParser *jsonTextParser) container(opener, closer byte, start int) (jsonNode, error) {
	node := jsonNode{kind: opener}
	textParser.position++
	textParser.skipSpace()
	if textParser.position < len(textParser.text) && textParser.text[textParser.position] == closer {
		textParser.position++
		node.raw = textParser.text[start:textParser.position]
		return node, nil
	}
	for {
		if opener == '{' {
			textParser.skipSpace()
			keyStart := textParser.position
			if keyStart >= len(textParser.text) || textParser.text[keyStart] != '"' {
				return jsonNode{}, fmt.Errorf("no key at byte %d", keyStart)
			}
			if err := textParser.skipString(); err != nil {
				return jsonNode{}, err
			}
			rawKey := textParser.text[keyStart:textParser.position]
			var key string
			// json.Unmarshal may unescape in its argument's own bytes.
			if err := json.Unmarshal(bytes.Clone(rawKey), &key); err != nil {
				return jsonNode{}, fmt.Errorf("key at byte %d: %w", keyStart, err)
			}
			node.rawKeys = append(node.rawKeys, rawKey)
			node.keys = append(node.keys, key)
			textParser.skipSpace()
			if textParser.position >= len(textParser.text) || textParser.text[textParser.position] != ':' {
				return jsonNode{}, fmt.Errorf("no colon at byte %d", textParser.position)
			}
			textParser.position++
		} else {
			node.keys = append(node.keys, strconv.Itoa(len(node.children)))
			node.rawKeys = append(node.rawKeys, nil)
		}
		child, err := textParser.value()
		if err != nil {
			return jsonNode{}, err
		}
		node.children = append(node.children, child)
		textParser.skipSpace()
		if textParser.position >= len(textParser.text) {
			return jsonNode{}, errJSONEnd
		}
		switch textParser.text[textParser.position] {
		case ',':
			textParser.position++
		case closer:
			textParser.position++
			node.raw = textParser.text[start:textParser.position]
			return node, nil
		default:
			return jsonNode{}, fmt.Errorf("unexpected %q at byte %d", textParser.text[textParser.position], textParser.position)
		}
	}
}

func compareJSONDocuments(path string, shape documentShape, vendorNode, libNode jsonNode) error {
	if vendorNode.kind != libNode.kind || vendorNode.kind == 0 {
		return compareJSONRaw(path, vendorNode, libNode)
	}
	vendorPrefix := shape.orderedPrefix(vendorNode.keys)
	libPrefix := shape.orderedPrefix(libNode.keys)
	if vendorPrefix != libPrefix {
		return fmt.Errorf("%s: %d members in order (vendor) vs %d (library): vendor keys %q, library keys %q",
			path, vendorPrefix, libPrefix, vendorNode.keys, libNode.keys)
	}
	for position := 0; position < vendorPrefix; position++ {
		if err := compareJSONMembers(path, shape, position, vendorNode, position, libNode, position); err != nil {
			return err
		}
	}
	if len(vendorNode.keys) != len(libNode.keys) {
		return fmt.Errorf("%s: %d map entries (vendor) vs %d (library): vendor keys %q, library keys %q",
			path, len(vendorNode.keys)-vendorPrefix, len(libNode.keys)-libPrefix, vendorNode.keys, libNode.keys)
	}
	// Members are matched by the key as the text spells it. Two keys can
	// share a spelling (each byte that is not UTF-8 is written as U+FFFD), so
	// a spelling holds a list of members, and each vendor member takes any
	// unused library member with its spelling that compares equal.
	libByKey := map[string][]int{}
	for position := libPrefix; position < len(libNode.keys); position++ {
		libByKey[libNode.keys[position]] = append(libByKey[libNode.keys[position]], position)
	}
	used := map[int]bool{}
	for position := vendorPrefix; position < len(vendorNode.keys); position++ {
		candidates, found := libByKey[vendorNode.keys[position]]
		if !found {
			return fmt.Errorf("%s: map key %q is in the vendor's text but not the library's (library keys %q)",
				path, vendorNode.keys[position], libNode.keys)
		}
		var firstErr error
		matched := false
		for _, libPosition := range candidates {
			if used[libPosition] {
				continue
			}
			if err := compareJSONMembers(path, shape, position, vendorNode, position, libNode, libPosition); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			used[libPosition] = true
			matched = true
			break
		}
		if !matched {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: no unused library member for key %q", path, vendorNode.keys[position])
			}
			return firstErr
		}
	}
	return nil
}

// compareJSONMembers compares the vendor's member vendorPosition with the
// library's member libPosition; goPosition is the member's position in the
// Go value, for a sequence.
func compareJSONMembers(path string, shape documentShape, goPosition int, vendorNode jsonNode, vendorPosition int, libNode jsonNode, libPosition int) error {
	key := vendorNode.keys[vendorPosition]
	if !bytes.Equal(vendorNode.rawKeys[vendorPosition], libNode.rawKeys[libPosition]) {
		return fmt.Errorf("%s: member %d is %s (vendor) vs %s (library)", path, vendorPosition, vendorNode.rawKeys[vendorPosition], libNode.rawKeys[libPosition])
	}
	childPath := shape.childPath(path, key)
	return compareJSONValues(childPath, shape.childValue(key, goPosition), vendorNode.children[vendorPosition], libNode.children[libPosition])
}

func compareJSONValues(path string, goValue reflect.Value, vendorNode, libNode jsonNode) error {
	switch {
	case vendorNode.kind == '{' && libNode.kind == '{' && isDocumentLevel(goValue):
		return compareJSONDocuments(path, documentShapeOf(goValue, false), vendorNode, libNode)
	case vendorNode.kind == '[' && libNode.kind == '[' && isArrayLevel(goValue):
		return compareJSONDocuments(path, documentShapeOf(goValue, true), vendorNode, libNode)
	}
	return compareJSONRaw(path, vendorNode, libNode)
}

func compareJSONRaw(path string, vendorNode, libNode jsonNode) error {
	if !bytes.Equal(vendorNode.raw, libNode.raw) {
		return fmt.Errorf("%s: %s (vendor) vs %s (library)", path, vendorNode.raw, libNode.raw)
	}
	return nil
}

// scalarPackageSuffixes name the packages of the struct types that encode as one BSON value, not
// as a document: time.Time, url.URL, and the two libraries' primitive and
// bson types (RawValue, Binary, Regex, ...).
var scalarPackageSuffixes = []string{"time", "net/url", "/bson", "/bson/primitive"}

func isScalarStruct(goType reflect.Type) bool {
	for _, suffix := range scalarPackageSuffixes {
		if goType.PkgPath() == suffix || strings.HasSuffix(goType.PkgPath(), suffix) {
			return true
		}
	}
	return false
}

// isDocumentLevel reports whether goValue encodes as a document whose
// members its Go value describes: a map, a struct of its own, or a bson.D.
func isDocumentLevel(goValue reflect.Value) bool {
	goValue = concreteValue(goValue)
	if !goValue.IsValid() || hasCustomEncoding(goValue.Type()) {
		return false
	}
	switch goValue.Kind() {
	case reflect.Map:
		return true
	case reflect.Struct:
		return !isScalarStruct(goValue.Type())
	case reflect.Slice:
		return isKeyValueStruct(goValue.Type().Elem())
	}
	return false
}

// isArrayLevel reports whether goValue encodes as an array whose values its
// Go value describes: a slice or an array that is not bytes or a bson.D.
func isArrayLevel(goValue reflect.Value) bool {
	goValue = concreteValue(goValue)
	if !goValue.IsValid() || hasCustomEncoding(goValue.Type()) {
		return false
	}
	switch goValue.Kind() {
	case reflect.Slice, reflect.Array:
		elemType := goValue.Type().Elem()
		return elemType.Kind() != reflect.Uint8 && !isKeyValueStruct(elemType)
	}
	return false
}

var (
	dbPointerStart = []byte(`{"$dbPointer":{"$ref":"`)
	dbPointerEnd   = []byte(`","$id":{"$oid":"`)
)

// quotedDBPointerNamespaces escapes every byte of a DBPointer's namespace
// as \u00XX, so a text whose namespace the vendor wrote unescaped (a quote
// or a control byte in it) can be parsed. Both texts are rewritten the same
// way, so two texts that differ still differ.
func quotedDBPointerNamespaces(text []byte) []byte {
	if !bytes.Contains(text, dbPointerStart) {
		return text
	}
	var rewritten []byte
	rest := text
	for {
		start := bytes.Index(rest, dbPointerStart)
		if start < 0 {
			return append(rewritten, rest...)
		}
		start += len(dbPointerStart)
		end := bytes.Index(rest[start:], dbPointerEnd)
		if end < 0 {
			return append(rewritten, rest...)
		}
		rewritten = append(rewritten, rest[:start]...)
		for _, octet := range rest[start : start+end] {
			rewritten = append(rewritten, fmt.Sprintf("\\u%04x", octet)...)
		}
		rest = rest[start+end:]
	}
}
