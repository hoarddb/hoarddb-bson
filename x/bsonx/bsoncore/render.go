// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: the String and DebugString renderings of value.go, document.go, element.go and array.go, gathered into one renderer that counts nesting and refuses past the ceiling; the string escaping is written without the encoding/json tables the vendor's tables.go derives from

package bsoncore

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// maxRenderDepth is the library's nesting ceiling (spec 5.2, bson.MaxNestingCeiling, which this
// package cannot import): a document, array or code-with-scope scope nested deeper than this
// many levels is not rendered. The root is level 1.
const maxRenderDepth = 200

// malformedText is what DebugString writes for input it cannot render.
const malformedText = "<malformed>"

// renderer writes the String and DebugString renderings of one value, counting the levels the
// rendering opens. It is one use, not shared: the zero value is ready.
type renderer struct {
	depth    int
	exceeded bool
}

// enter opens one level. It reports false, and remembers that the ceiling was passed, when the
// level would be the 201st.
func (r *renderer) enter() bool {
	if r.exceeded || r.depth >= maxRenderDepth {
		r.exceeded = true
		return false
	}
	r.depth++
	return true
}

func (r *renderer) leave() { r.depth-- }

// refuseIfTooDeep returns refusal when the rendering passed the ceiling, and rendered otherwise.
// The partial text is dropped whole: half a document is worse than none.
func (r *renderer) refuseIfTooDeep(rendered, refusal string) string {
	if r.exceeded {
		return refusal
	}
	return rendered
}

// String implements the fmt.String interface. This method will return values in extended JSON
// format. If the value is not valid, this returns an empty string. A value nested deeper than the
// library ceiling renders as the empty string as well.
func (v Value) String() string {
	var rendering renderer
	return rendering.refuseIfTooDeep(rendering.value(v), "")
}

// DebugString outputs a human readable version of Value. It will attempt to stringify the
// valid components of the value even if the entire value is not valid. A value nested deeper
// than the library ceiling renders as "<malformed>".
func (v Value) DebugString() string {
	var rendering renderer
	return rendering.refuseIfTooDeep(rendering.valueDebug(v), malformedText)
}

func (r *renderer) value(v Value) string {
	switch v.Type {
	case bsontype.Double:
		f64, ok := v.DoubleOK()
		if !ok {
			return ""
		}
		return fmt.Sprintf(`{"$numberDouble":"%s"}`, formatDouble(f64))
	case bsontype.String:
		str, ok := v.StringValueOK()
		if !ok {
			return ""
		}
		return escapeString(str)
	case bsontype.EmbeddedDocument:
		doc, ok := v.DocumentOK()
		if !ok {
			return ""
		}
		return r.document(doc)
	case bsontype.Array:
		arr, ok := v.ArrayOK()
		if !ok {
			return ""
		}
		return r.array(arr)
	case bsontype.Binary:
		subtype, data, ok := v.BinaryOK()
		if !ok {
			return ""
		}
		return fmt.Sprintf(`{"$binary":{"base64":"%s","subType":"%02x"}}`, base64.StdEncoding.EncodeToString(data), subtype)
	case bsontype.Undefined:
		return `{"$undefined":true}`
	case bsontype.ObjectID:
		oid, ok := v.ObjectIDOK()
		if !ok {
			return ""
		}
		return fmt.Sprintf(`{"$oid":"%s"}`, oid.Hex())
	case bsontype.Boolean:
		b, ok := v.BooleanOK()
		if !ok {
			return ""
		}
		return strconv.FormatBool(b)
	case bsontype.DateTime:
		dt, ok := v.DateTimeOK()
		if !ok {
			return ""
		}
		return fmt.Sprintf(`{"$date":{"$numberLong":"%d"}}`, dt)
	case bsontype.Null:
		return "null"
	case bsontype.Regex:
		pattern, options, ok := v.RegexOK()
		if !ok {
			return ""
		}
		return fmt.Sprintf(
			`{"$regularExpression":{"pattern":%s,"options":"%s"}}`,
			escapeString(pattern), sortStringAlphebeticAscending(options),
		)
	case bsontype.DBPointer:
		ns, pointer, ok := v.DBPointerOK()
		if !ok {
			return ""
		}
		return fmt.Sprintf(`{"$dbPointer":{"$ref":%s,"$id":{"$oid":"%s"}}}`, escapeString(ns), pointer.Hex())
	case bsontype.JavaScript:
		js, ok := v.JavaScriptOK()
		if !ok {
			return ""
		}
		return fmt.Sprintf(`{"$code":%s}`, escapeString(js))
	case bsontype.Symbol:
		symbol, ok := v.SymbolOK()
		if !ok {
			return ""
		}
		return fmt.Sprintf(`{"$symbol":%s}`, escapeString(symbol))
	case bsontype.CodeWithScope:
		code, scope, ok := v.CodeWithScopeOK()
		if !ok {
			return ""
		}
		// The code is written as it is, unquoted: the vendor's rendering does the same.
		return fmt.Sprintf(`{"$code":%s,"$scope":%s}`, code, r.document(scope))
	case bsontype.Int32:
		i32, ok := v.Int32OK()
		if !ok {
			return ""
		}
		return fmt.Sprintf(`{"$numberInt":"%d"}`, i32)
	case bsontype.Timestamp:
		t, i, ok := v.TimestampOK()
		if !ok {
			return ""
		}
		return fmt.Sprintf(`{"$timestamp":{"t":%v,"i":%v}}`, t, i)
	case bsontype.Int64:
		i64, ok := v.Int64OK()
		if !ok {
			return ""
		}
		return fmt.Sprintf(`{"$numberLong":"%d"}`, i64)
	case bsontype.Decimal128:
		d128, ok := v.Decimal128OK()
		if !ok {
			return ""
		}
		return fmt.Sprintf(`{"$numberDecimal":"%s"}`, d128.String())
	case bsontype.MinKey:
		return `{"$minKey":1}`
	case bsontype.MaxKey:
		return `{"$maxKey":1}`
	default:
		return ""
	}
}

func (r *renderer) valueDebug(v Value) string {
	switch v.Type {
	case bsontype.String:
		str, ok := v.StringValueOK()
		if !ok {
			return malformedText
		}
		return escapeString(str)
	case bsontype.EmbeddedDocument:
		doc, ok := v.DocumentOK()
		if !ok {
			return malformedText
		}
		return r.documentDebug(doc)
	case bsontype.Array:
		arr, ok := v.ArrayOK()
		if !ok {
			return malformedText
		}
		return r.arrayDebug(arr)
	case bsontype.CodeWithScope:
		code, scope, ok := v.CodeWithScopeOK()
		if !ok {
			return ""
		}
		return fmt.Sprintf(`{"$code":%s,"$scope":%s}`, code, r.documentDebug(scope))
	default:
		str := r.value(v)
		if str == "" {
			return malformedText
		}
		return str
	}
}

func (r *renderer) element(e Element) string {
	if len(e) == 0 {
		return ""
	}
	t := bsontype.Type(e[0])
	idx := bytes.IndexByte(e[1:], 0x00)
	if idx == -1 {
		return ""
	}
	key, valBytes := []byte(e[1:idx+1]), []byte(e[idx+2:])
	val, _, valid := readTypedValue(valBytes, t)
	if !valid {
		return ""
	}
	return "\"" + string(key) + "\": " + r.value(val)
}

func (r *renderer) elementDebug(e Element) string {
	if len(e) == 0 {
		return malformedText
	}
	t := bsontype.Type(e[0])
	idx := bytes.IndexByte(e[1:], 0x00)
	if idx == -1 {
		return fmt.Sprintf(`bson.Element{[%s]<malformed>}`, t)
	}
	key, valBytes := []byte(e[1:idx+1]), []byte(e[idx+2:])
	val, _, valid := readTypedValue(valBytes, t)
	if !valid {
		return fmt.Sprintf(`bson.Element{[%s]"%s": <malformed>}`, t, key)
	}
	// The vendor formats the value with %v, which writes its String, not its DebugString.
	return fmt.Sprintf(`bson.Element{[%s]"%s": %s}`, t, key, r.value(val))
}

func (r *renderer) document(d Document) string {
	if len(d) < 5 {
		return ""
	}
	if !r.enter() {
		return ""
	}
	defer r.leave()
	var buf strings.Builder
	buf.WriteByte('{')

	length, rem, _ := ReadLength(d) // We know we have enough bytes to read the length

	length -= 4

	var elem Element
	var ok bool
	first := true
	for length > 1 {
		if !first {
			buf.WriteByte(',')
		}
		elem, rem, ok = ReadElement(rem)
		length -= int32(len(elem))
		if !ok {
			return ""
		}
		buf.WriteString(r.element(elem))
		first = false
	}
	buf.WriteByte('}')

	return buf.String()
}

func (r *renderer) documentDebug(d Document) string {
	if len(d) < 5 {
		return malformedText
	}
	if !r.enter() {
		return malformedText
	}
	defer r.leave()
	var buf strings.Builder
	buf.WriteString("Document")
	length, rem, _ := ReadLength(d) // We know we have enough bytes to read the length
	buf.WriteByte('(')
	buf.WriteString(strconv.Itoa(int(length)))
	length -= 4
	buf.WriteString("){")
	var elem Element
	var ok bool
	for length > 1 {
		elem, rem, ok = ReadElement(rem)
		length -= int32(len(elem))
		if !ok {
			fmt.Fprintf(&buf, "<malformed (%d)>", length)
			break
		}
		buf.WriteString(r.elementDebug(elem))
	}
	buf.WriteByte('}')

	return buf.String()
}

func (r *renderer) array(a Array) string {
	if len(a) < 5 {
		return ""
	}
	if !r.enter() {
		return ""
	}
	defer r.leave()
	var buf strings.Builder
	buf.WriteByte('[')

	length, rem, _ := ReadLength(a) // We know we have enough bytes to read the length

	length -= 4

	var elem Element
	var ok bool
	for length > 1 {
		elem, rem, ok = ReadElement(rem)
		length -= int32(len(elem))
		if !ok {
			return ""
		}
		buf.WriteString(r.value(elem.Value()))
		if length > 1 {
			buf.WriteByte(',')
		}
	}
	if length != 1 { // Missing final null byte or inaccurate length
		return ""
	}

	buf.WriteByte(']')
	return buf.String()
}

func (r *renderer) arrayDebug(a Array) string {
	if len(a) < 5 {
		return malformedText
	}
	if !r.enter() {
		return malformedText
	}
	defer r.leave()
	var buf strings.Builder
	buf.WriteString("Array")
	length, rem, _ := ReadLength(a) // We know we have enough bytes to read the length
	buf.WriteByte('(')
	buf.WriteString(strconv.Itoa(int(length)))
	length -= 4
	buf.WriteString(")[")
	var elem Element
	var ok bool
	for length > 1 {
		elem, rem, ok = ReadElement(rem)
		length -= int32(len(elem))
		if !ok {
			fmt.Fprintf(&buf, "<malformed (%d)>", length)
			break
		}
		buf.WriteString(r.valueDebug(elem.Value()))
		if length != 1 {
			buf.WriteByte(',')
		}
	}
	buf.WriteByte(']')

	return buf.String()
}

const lowerHex = "0123456789abcdef"

// escapeString quotes s as a JSON string with the vendor's escapes: the quote, the backslash and
// the control characters as \n \r \t \b \f or \u00XX, "<", ">" and "&" as \u00XX, an invalid UTF-8
// byte as the text \ufffd, and U+2028 and U+2029 as \u2028 and \u2029. DEL passes through.
func escapeString(s string) string {
	var buf strings.Builder
	buf.WriteByte('"')
	for position := 0; position < len(s); {
		b := s[position]
		if b < utf8.RuneSelf {
			position++
			switch b {
			case '\\', '"':
				buf.WriteByte('\\')
				buf.WriteByte(b)
			case '\n':
				buf.WriteString(`\n`)
			case '\r':
				buf.WriteString(`\r`)
			case '\t':
				buf.WriteString(`\t`)
			case '\b':
				buf.WriteString(`\b`)
			case '\f':
				buf.WriteString(`\f`)
			case '<', '>', '&':
				buf.WriteString(`\u00`)
				buf.WriteByte(lowerHex[b>>4])
				buf.WriteByte(lowerHex[b&0xF])
			default:
				if b < 0x20 {
					buf.WriteString(`\u00`)
					buf.WriteByte(lowerHex[b>>4])
					buf.WriteByte(lowerHex[b&0xF])
				} else {
					buf.WriteByte(b)
				}
			}
			continue
		}
		c, size := utf8.DecodeRuneInString(s[position:])
		switch {
		case c == utf8.RuneError && size == 1:
			buf.WriteString(`\ufffd`)
		case c == '\u2028' || c == '\u2029':
			buf.WriteString(`\u202`)
			buf.WriteByte(lowerHex[c&0xF])
		default:
			buf.WriteString(s[position : position+size])
		}
		position += size
	}
	buf.WriteByte('"')
	return buf.String()
}

func formatDouble(f float64) string {
	var s string
	switch {
	case math.IsInf(f, 1):
		s = "Infinity"
	case math.IsInf(f, -1):
		s = "-Infinity"
	case math.IsNaN(f):
		s = "NaN"
	default:
		// Print exactly one decimalType place for integers; otherwise, print as many are necessary to
		// perfectly represent it.
		s = strconv.FormatFloat(f, 'G', -1, 64)
		if !strings.ContainsRune(s, '.') {
			s += ".0"
		}
	}

	return s
}

func sortStringAlphebeticAscending(s string) string {
	runes := []rune(s)
	slices.Sort(runes)
	return string(runes)
}
