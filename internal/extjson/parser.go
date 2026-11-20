// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: derived from bson/bsonrw/extjson_parser.go (and wrapperKeyBSONType from extjson_wrappers.go): its parse states, read-ahead order and error text are kept in rewritten code with new names; the token transitions are a switch instead of a map; the depth limit is exported as MaxNestingDepth, and its refusal matches ErrNestingTooDeep (step 5's Go-value half).

package extjson

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// The parser turns tokens into Extended JSON values one step at a time.
// What it accepts and the text of every error are the vendor's (spec
// 9.1.5), so it is derived from the vendor's parser: it keeps its states
// and the order in which it reads ahead. Every branch the vendor has is kept, including ones a global
// argument says the reader never reaches: the equivalence is then argued
// one branch at a time, which a reviewer can check, and the differential
// fuzz checks the rest.

// MaxNestingDepth is the deepest an Extended JSON text may nest objects,
// the root counting as 1. Arrays do not count, as in the vendor. It equals
// bson.MaxNestingCeiling (TestMaxNestingDepthIsTheCeiling).
const MaxNestingDepth = 200

// ErrInvalidJSON is the vendor's error for a text whose first value cannot
// be read at all.
var ErrInvalidJSON = errors.New("invalid JSON input")

// ErrEndOfDocument ends a document's elements. Its text is the vendor's,
// because a wrapper that ends too early returns it to the caller.
var ErrEndOfDocument = errors.New("end of document")

// ErrEndOfArray ends an array's values.
var ErrEndOfArray = errors.New("end of array")

type parseState byte

const (
	stateStart parseState = iota
	stateBeginObject
	stateEndObject
	stateBeginArray
	stateEndArray
	stateColon
	stateComma
	stateKey
	stateValue
	stateDone
	stateInvalid
)

type containerMode byte

const (
	modeNone containerMode = iota
	modeObject
	modeArray
)

// jsonValue is one JSON scalar, or the object a wrapper such as $binary
// holds. kind is the BSON type of a scalar token (Int32, Int64, Double,
// String, Boolean, Null), or EmbeddedDocument for an object.
type jsonValue struct {
	kind        bsontype.Type
	text        string
	int32Number int32
	int64Number int64
	double      float64
	flag        bool
	object      *wrapperObject
}

// wrapperObject is the inside of a multi-key wrapper, in input order.
type wrapperObject struct {
	keys   []string
	values []jsonValue
}

func objectValue(keys []string, values []jsonValue) jsonValue {
	return jsonValue{kind: bsontype.EmbeddedDocument, object: &wrapperObject{keys: keys, values: values}}
}

func scalarValue(scalarToken token) jsonValue {
	switch scalarToken.kind {
	case tokenInt32:
		return jsonValue{kind: bsontype.Int32, int32Number: scalarToken.int32Number}
	case tokenInt64:
		return jsonValue{kind: bsontype.Int64, int64Number: scalarToken.int64Number}
	case tokenDouble:
		return jsonValue{kind: bsontype.Double, double: scalarToken.double}
	case tokenString:
		return jsonValue{kind: bsontype.String, text: scalarToken.text}
	case tokenBoolean:
		return jsonValue{kind: bsontype.Boolean, flag: scalarToken.flag}
	}
	return jsonValue{kind: bsontype.Null}
}

type parser struct {
	scanner   scanner
	state     parseState
	modes     []containerMode
	key       string
	value     jsonValue
	err       error
	canonical bool
	depth     int

	// emptyObject records that peekType read a whole {}: the next readKey
	// reports the end of that document without reading.
	emptyObject bool
	// relaxedUUID records that peekType saw $uuid. Only the string form
	// clears it, so after {"$uuid": {...}} it stays set and the next
	// legacy {"$binary": "...", "$type": ...} is read as a $uuid, as in the
	// vendor.
	relaxedUUID bool
}

func newParser(text []byte, canonical bool) *parser {
	return &parser{scanner: scanner{text: text}, canonical: canonical}
}

// advance reads one token and moves to the state it leads to, or to
// stateInvalid with err set. In stateDone and stateInvalid it does nothing.
func (jsonParser *parser) advance() {
	if jsonParser.state == stateDone || jsonParser.state == stateInvalid {
		return
	}
	nextToken, err := jsonParser.scanner.next()
	if err != nil {
		jsonParser.fail(err)
		return
	}
	if !jsonParser.accepts(nextToken.kind) {
		jsonParser.fail(unexpectedTokenError(nextToken))
		return
	}
	switch nextToken.kind {
	case tokenBeginObject:
		jsonParser.state = stateBeginObject
		jsonParser.modes = append(jsonParser.modes, modeObject)
		jsonParser.depth++
		if jsonParser.depth > MaxNestingDepth {
			jsonParser.fail(depthError{text: fmt.Sprintf("invalid JSON input; nesting too deep (%d levels) at position %d",
				jsonParser.depth, nextToken.position)})
		}
	case tokenEndObject:
		jsonParser.state = stateEndObject
		jsonParser.depth--
		if jsonParser.popMode() != modeObject {
			jsonParser.fail(unexpectedTokenError(nextToken))
		}
	case tokenBeginArray:
		jsonParser.state = stateBeginArray
		jsonParser.modes = append(jsonParser.modes, modeArray)
	case tokenEndArray:
		jsonParser.state = stateEndArray
		if jsonParser.popMode() != modeArray {
			jsonParser.fail(unexpectedTokenError(nextToken))
		}
	case tokenColon:
		jsonParser.state = stateColon
	case tokenComma:
		jsonParser.state = stateComma
	case tokenEndOfInput:
		jsonParser.state = stateDone
		if len(jsonParser.modes) != 0 {
			jsonParser.fail(unexpectedTokenError(nextToken))
		}
	case tokenString:
		// A string after { or after a comma in an object is a key.
		isKey := jsonParser.state == stateBeginObject ||
			(jsonParser.state == stateComma && jsonParser.peekMode() != modeArray)
		if isKey {
			jsonParser.state = stateKey
			jsonParser.key = nextToken.text
			return
		}
		jsonParser.state = stateValue
		jsonParser.value = scalarValue(nextToken)
	default:
		jsonParser.state = stateValue
		jsonParser.value = scalarValue(nextToken)
	}
}

// depthError is the parser's refusal of a text nesting objects past
// MaxNestingDepth: the vendor's text, matching ErrNestingTooDeep, so that
// bson.UnmarshalExtJSON's refusal past the ceiling matches
// bson.ErrNestingTooDeep wherever it comes from (spec 5.2).
type depthError struct{ text string }

func (refusal depthError) Error() string { return refusal.text }
func (depthError) Unwrap() error         { return ErrNestingTooDeep }

func (jsonParser *parser) fail(err error) {
	jsonParser.err = err
	jsonParser.state = stateInvalid
}

// valueTokens are the tokens that may start a value.
func startsValue(kind tokenKind) bool {
	switch kind {
	case tokenBeginObject, tokenBeginArray, tokenInt32, tokenInt64, tokenDouble, tokenString, tokenBoolean, tokenNull:
		return true
	}
	return false
}

// followsValue reports whether kind may come after a complete value.
func followsValue(kind tokenKind) bool {
	return kind == tokenEndObject || kind == tokenEndArray || kind == tokenComma || kind == tokenEndOfInput
}

// accepts reports whether a token of kind may come next.
func (jsonParser *parser) accepts(kind tokenKind) bool {
	switch jsonParser.state {
	case stateStart:
		return startsValue(kind) || kind == tokenEndOfInput
	case stateBeginObject:
		return kind == tokenEndObject || kind == tokenString
	case stateEndObject:
		// A second root document may follow the first one, outside an array.
		if kind == tokenBeginObject && jsonParser.depth == 0 {
			return jsonParser.peekMode() != modeArray
		}
		return followsValue(kind)
	case stateBeginArray:
		return startsValue(kind) || kind == tokenEndArray
	case stateEndArray, stateValue:
		return followsValue(kind)
	case stateColon:
		return startsValue(kind)
	case stateComma:
		switch jsonParser.peekMode() {
		case modeObject:
			return kind == tokenString
		case modeNone:
			return false
		}
		return startsValue(kind)
	case stateKey:
		return kind == tokenColon
	}
	return false
}

func (jsonParser *parser) popMode() containerMode {
	if len(jsonParser.modes) == 0 {
		return modeNone
	}
	innermost := jsonParser.modes[len(jsonParser.modes)-1]
	jsonParser.modes = jsonParser.modes[:len(jsonParser.modes)-1]
	return innermost
}

func (jsonParser *parser) peekMode() containerMode {
	if len(jsonParser.modes) == 0 {
		return modeNone
	}
	return jsonParser.modes[len(jsonParser.modes)-1]
}

// wrapperKeyType returns the BSON type a wrapper key stands for, or
// EmbeddedDocument for a plain key.
func wrapperKeyType(key string) bsontype.Type {
	switch key {
	case "$numberInt":
		return bsontype.Int32
	case "$numberLong":
		return bsontype.Int64
	case "$oid":
		return bsontype.ObjectID
	case "$symbol":
		return bsontype.Symbol
	case "$numberDouble":
		return bsontype.Double
	case "$numberDecimal":
		return bsontype.Decimal128
	case "$binary":
		return bsontype.Binary
	case "$code":
		return bsontype.JavaScript
	case "$scope":
		return bsontype.CodeWithScope
	case "$timestamp":
		return bsontype.Timestamp
	case "$regularExpression":
		return bsontype.Regex
	case "$dbPointer":
		return bsontype.DBPointer
	case "$date":
		return bsontype.DateTime
	case "$minKey":
		return bsontype.MinKey
	case "$maxKey":
		return bsontype.MaxKey
	case "$undefined":
		return bsontype.Undefined
	}
	return bsontype.EmbeddedDocument
}

// peekType reads up to the next value and returns its BSON type. For an
// object, it reads the first key: a wrapper key decides the type, except at
// the root, which is always a document.
func (jsonParser *parser) peekType() (bsontype.Type, error) {
	initialState := jsonParser.state
	jsonParser.advance()
	switch jsonParser.state {
	case stateValue:
		return jsonParser.value.kind, nil
	case stateBeginArray:
		return bsontype.Array, nil
	case stateInvalid:
		return 0, jsonParser.err
	case stateComma:
		if jsonParser.peekMode() == modeArray {
			return jsonParser.peekType()
		}
	case stateEndArray:
		return 0, ErrEndOfArray
	case stateBeginObject:
		return jsonParser.peekObjectType(initialState)
	}
	return 0, nil
}

func (jsonParser *parser) peekObjectType(initialState parseState) (bsontype.Type, error) {
	jsonParser.advance()
	switch jsonParser.state {
	case stateEndObject:
		jsonParser.emptyObject = true
		return bsontype.EmbeddedDocument, nil
	case stateInvalid:
		return 0, jsonParser.err
	case stateKey:
		if initialState == stateStart {
			return bsontype.EmbeddedDocument, nil
		}
		valueType := wrapperKeyType(jsonParser.key)
		if jsonParser.key == "$uuid" {
			jsonParser.relaxedUUID = true
			valueType = bsontype.Binary
		}
		switch valueType {
		case bsontype.JavaScript:
			return jsonParser.peekCodeType()
		case bsontype.CodeWithScope:
			return 0, errors.New("invalid extended JSON: code with $scope must contain $code before $scope")
		}
		return valueType, nil
	}
	return 0, nil
}

// peekCodeType reads a $code value and the key after it, if any, to tell
// JavaScript from code with scope.
func (jsonParser *parser) peekCodeType() (bsontype.Type, error) {
	if _, err := jsonParser.readValue(bsontype.JavaScript); err != nil {
		return 0, err
	}
	switch jsonParser.state {
	case stateEndObject:
		return bsontype.JavaScript, nil
	case stateComma:
		jsonParser.advance()
		if jsonParser.state == stateKey && jsonParser.key == "$scope" {
			return bsontype.CodeWithScope, nil
		}
		return 0, fmt.Errorf("invalid extended JSON: unexpected key %s in CodeWithScope object", jsonParser.key)
	case stateInvalid:
		return 0, jsonParser.err
	}
	return 0, ErrInvalidJSON
}

// readKey reads the next key of the open document and peeks its value's
// type. At the end of the document it returns ErrEndOfDocument.
func (jsonParser *parser) readKey() (string, bsontype.Type, error) {
	if jsonParser.emptyObject {
		jsonParser.emptyObject = false
		return "", 0, ErrEndOfDocument
	}
	switch jsonParser.state {
	case stateStart:
		jsonParser.advance()
		if jsonParser.state == stateBeginObject {
			jsonParser.advance()
		}
	case stateBeginObject:
		jsonParser.advance()
	case stateValue, stateEndObject, stateEndArray:
		jsonParser.advance()
		switch jsonParser.state {
		case stateBeginObject, stateComma:
			jsonParser.advance()
		case stateEndObject:
			return "", 0, ErrEndOfDocument
		case stateDone:
			return "", 0, io.EOF
		case stateInvalid:
			return "", 0, jsonParser.err
		default:
			return "", 0, ErrInvalidJSON
		}
	case stateKey:
		// peekType read the key already.
	default:
		return "", 0, invalidRequestError("key")
	}
	var key string
	switch jsonParser.state {
	case stateKey:
		key = jsonParser.key
	case stateEndObject:
		return "", 0, ErrEndOfDocument
	case stateInvalid:
		return "", 0, jsonParser.err
	default:
		return "", 0, invalidRequestError("key")
	}
	jsonParser.advance()
	if err := requireColon(jsonParser.state, key); err != nil {
		return "", 0, err
	}
	valueType, err := jsonParser.peekType()
	if err != nil {
		return "", 0, err
	}
	return key, valueType, nil
}

// requireColon checks only the state: a key followed by a token that is
// not a colon is reported as a missing colon, whatever the token was.
func requireColon(state parseState, key string) error {
	if state != stateColon {
		return fmt.Errorf("invalid JSON input: missing colon after key \"%s\"", key)
	}
	return nil
}

// readValue reads the value peekType announced as valueType: a scalar, a
// single-key wrapper's value, or a multi-key wrapper's object.
func (jsonParser *parser) readValue(valueType bsontype.Type) (jsonValue, error) {
	if jsonParser.state == stateInvalid {
		return jsonValue{}, jsonParser.err
	}
	switch valueType {
	case bsontype.Null, bsontype.Boolean, bsontype.String:
		if jsonParser.state != stateValue {
			return jsonValue{}, invalidRequestError(valueType.String())
		}
		return jsonParser.value, nil
	case bsontype.Int32, bsontype.Int64, bsontype.Double:
		// A number may be written bare, in either mode.
		if jsonParser.state == stateValue {
			return jsonParser.value, nil
		}
		return jsonParser.readSingleKeyValue(valueType)
	case bsontype.Decimal128, bsontype.Symbol, bsontype.ObjectID, bsontype.MinKey, bsontype.MaxKey, bsontype.Undefined:
		return jsonParser.readSingleKeyValue(valueType)
	case bsontype.Binary, bsontype.Regex, bsontype.Timestamp, bsontype.DBPointer:
		return jsonParser.readObjectWrapper(valueType)
	case bsontype.DateTime:
		return jsonParser.readDateValue()
	case bsontype.JavaScript:
		return jsonParser.readCodeValue()
	case bsontype.CodeWithScope:
		return jsonParser.readScopeStart()
	case bsontype.EmbeddedDocument, bsontype.Array:
		return jsonValue{}, invalidRequestError(valueType.String())
	}
	return jsonValue{}, nil
}

// readSingleKeyValue reads {"$wrapper": value}, with the key already read.
func (jsonParser *parser) readSingleKeyValue(valueType bsontype.Type) (jsonValue, error) {
	if jsonParser.state != stateKey {
		return jsonValue{}, invalidRequestError(valueType.String())
	}
	jsonParser.advance()
	if err := requireColon(jsonParser.state, jsonParser.key); err != nil {
		return jsonValue{}, err
	}
	jsonParser.advance()
	if jsonParser.state != stateValue || !wrapperValueHasKind(valueType, jsonParser.value.kind) {
		return jsonValue{}, invalidJSONErrorForType("value", valueType)
	}
	wrapped := jsonParser.value
	jsonParser.advance()
	if jsonParser.state != stateEndObject {
		return jsonValue{}, invalidJSONErrorForType("} after value", valueType)
	}
	return wrapped, nil
}

// wrapperValueHasKind reports whether a single-key wrapper's value is of the
// JSON kind it takes: {"$numberInt": v} needs a string v.
func wrapperValueHasKind(valueType, kind bsontype.Type) bool {
	switch valueType {
	case bsontype.MinKey, bsontype.MaxKey:
		return kind == bsontype.Int32
	case bsontype.Undefined:
		return kind == bsontype.Boolean
	case bsontype.Int32, bsontype.Int64, bsontype.Double, bsontype.Decimal128, bsontype.Symbol, bsontype.ObjectID:
		return kind == bsontype.String
	}
	return false
}

// readObjectWrapper reads {"$binary": {...}}, {"$regularExpression": {...}},
// {"$timestamp": {...}} or {"$dbPointer": {...}}, and the $uuid and legacy
// $binary forms, into the two-key object the wrapper parsers take.
func (jsonParser *parser) readObjectWrapper(valueType bsontype.Type) (jsonValue, error) {
	if jsonParser.state != stateKey {
		return jsonValue{}, invalidRequestError(valueType.String())
	}
	jsonParser.advance()
	if err := requireColon(jsonParser.state, jsonParser.key); err != nil {
		return jsonValue{}, err
	}
	jsonParser.advance()
	if valueType == bsontype.Binary && jsonParser.state == stateValue {
		if jsonParser.relaxedUUID {
			return jsonParser.readUUID()
		}
		return jsonParser.readLegacyBinary()
	}
	if jsonParser.state != stateBeginObject {
		return jsonValue{}, invalidJSONErrorForType("{", valueType)
	}
	keys, values, err := jsonParser.readObject(2)
	if err != nil {
		return jsonValue{}, err
	}
	jsonParser.advance()
	if jsonParser.state != stateEndObject {
		return jsonValue{}, invalidJSONErrorForType("2 key-value pairs and then }", valueType)
	}
	return objectValue(keys, values), nil
}

// uuidFormatError is the vendor's text for a $uuid of the wrong shape.
const uuidFormatError = "$uuid value does not follow RFC 4122 format regarding length and hyphens"

// readUUID reads the string of {"$uuid": "..."} as binary subtype 4.
func (jsonParser *parser) readUUID() (jsonValue, error) {
	defer func() { jsonParser.relaxedUUID = false }()
	uuidText, err := jsonParser.value.symbolText()
	if err != nil {
		return jsonValue{}, err
	}
	// RFC 4122: 36 characters, with hyphens at 8, 13, 18 and 23.
	shaped := len(uuidText) == 36 && uuidText[8] == '-' && uuidText[13] == '-' && uuidText[18] == '-' && uuidText[23] == '-'
	if !shaped {
		return jsonValue{}, errors.New(uuidFormatError)
	}
	hexDigits := strings.ReplaceAll(uuidText, "-", "")
	if len(hexDigits) != 32 {
		return jsonValue{}, errors.New(uuidFormatError)
	}
	uuidBytes, err := hex.DecodeString(hexDigits)
	if err != nil {
		return jsonValue{}, fmt.Errorf("$uuid value does not follow RFC 4122 format regarding hex bytes: %w", err)
	}
	jsonParser.advance()
	if jsonParser.state != stateEndObject {
		return jsonValue{}, invalidJSONErrorForType("$uuid and value and then }", bsontype.Binary)
	}
	// The vendor hands the bytes on as base64 text, so they take the same
	// path as {"$binary": {...}} from here on.
	return objectValue([]string{"base64", "subType"}, []jsonValue{
		{kind: bsontype.String, text: base64.StdEncoding.EncodeToString(uuidBytes)},
		{kind: bsontype.String, text: "04"},
	}), nil
}

// readLegacyBinary reads {"$binary": "<base64>", "$type": "<hex>"}.
func (jsonParser *parser) readLegacyBinary() (jsonValue, error) {
	encoded := jsonParser.value
	jsonParser.advance()
	if jsonParser.state != stateComma {
		return jsonValue{}, invalidJSONErrorForType(",", bsontype.Binary)
	}
	jsonParser.advance()
	key, subtypeType, err := jsonParser.readKey()
	if err != nil {
		return jsonValue{}, err
	}
	if key != "$type" {
		return jsonValue{}, invalidJSONErrorForType("$type", bsontype.Binary)
	}
	subtype, err := jsonParser.readValue(subtypeType)
	if err != nil {
		return jsonValue{}, err
	}
	jsonParser.advance()
	if jsonParser.state != stateEndObject {
		return jsonValue{}, invalidJSONErrorForType("2 key-value pairs and then }", bsontype.Binary)
	}
	return objectValue([]string{"base64", "subType"}, []jsonValue{encoded, subtype}), nil
}

// readDateValue reads {"$date": ...}. Only relaxed mode takes a bare value.
func (jsonParser *parser) readDateValue() (jsonValue, error) {
	switch jsonParser.state {
	case stateValue:
		return jsonParser.value, nil
	case stateKey:
	default:
		return jsonValue{}, invalidRequestError(bsontype.DateTime.String())
	}
	jsonParser.advance()
	if err := requireColon(jsonParser.state, jsonParser.key); err != nil {
		return jsonValue{}, err
	}
	jsonParser.advance()
	var dateValue jsonValue
	switch jsonParser.state {
	case stateBeginObject:
		keys, values, err := jsonParser.readObject(1)
		if err != nil {
			return jsonValue{}, err
		}
		dateValue = objectValue(keys, values)
	case stateValue:
		if jsonParser.canonical {
			return jsonValue{}, invalidJSONError("{")
		}
		dateValue = jsonParser.value
	default:
		if jsonParser.canonical {
			return jsonValue{}, invalidJSONErrorForType("object", bsontype.DateTime)
		}
		return jsonValue{}, invalidJSONErrorForType("ISO-8601 Internet Date/Time Format as described in RFC-3339", bsontype.DateTime)
	}
	jsonParser.advance()
	if jsonParser.state != stateEndObject {
		return jsonValue{}, invalidJSONErrorForType("value and then }", bsontype.DateTime)
	}
	return dateValue, nil
}

// readCodeValue reads the value of $code and the token after it, which
// tells JavaScript ("}") from code with scope (",").
func (jsonParser *parser) readCodeValue() (jsonValue, error) {
	switch jsonParser.state {
	case stateKey:
		jsonParser.advance()
		if err := requireColon(jsonParser.state, jsonParser.key); err != nil {
			return jsonValue{}, err
		}
		jsonParser.advance()
		if jsonParser.state != stateValue {
			return jsonValue{}, invalidJSONErrorForType("value", bsontype.JavaScript)
		}
		code := jsonParser.value
		jsonParser.advance()
		return code, nil
	case stateEndObject:
		// peekType read {"$code": ...} whole; the code is the last value.
		return jsonParser.value, nil
	}
	return jsonValue{}, invalidRequestError(bsontype.JavaScript.String())
}

// readScopeStart reads `"$scope": {` after a $code, and returns the code
// peekType read before it.
func (jsonParser *parser) readScopeStart() (jsonValue, error) {
	if jsonParser.state != stateKey || jsonParser.key != "$scope" {
		return jsonValue{}, invalidRequestError(bsontype.CodeWithScope.String())
	}
	code := jsonParser.value
	jsonParser.advance()
	if err := requireColon(jsonParser.state, jsonParser.key); err != nil {
		return jsonValue{}, err
	}
	jsonParser.advance()
	if jsonParser.state != stateBeginObject {
		return jsonValue{}, invalidJSONError("$scope to be embedded document")
	}
	return code, nil
}

// readObject reads the keys and values of an object of known size whose
// { the parser has read, and its closing }.
func (jsonParser *parser) readObject(keyCount int) ([]string, []jsonValue, error) {
	keys := make([]string, keyCount)
	values := make([]jsonValue, keyCount)
	for index := range keyCount {
		key, valueType, err := jsonParser.readKey()
		if err != nil {
			return nil, nil, err
		}
		switch jsonParser.state {
		case stateKey:
			nested, err := jsonParser.readValue(valueType)
			if err != nil {
				return nil, nil, err
			}
			keys[index], values[index] = key, nested
		case stateValue:
			keys[index], values[index] = key, jsonParser.value
		default:
			return nil, nil, invalidJSONError("value")
		}
	}
	jsonParser.advance()
	if jsonParser.state != stateEndObject {
		return nil, nil, invalidJSONError("}")
	}
	return keys, values, nil
}

func invalidRequestError(what string) error {
	return fmt.Errorf("invalid request to read %s", what)
}

func invalidJSONError(expected string) error {
	return fmt.Errorf("invalid JSON input; expected %s", expected)
}

func invalidJSONErrorForType(expected string, valueType bsontype.Type) error {
	return fmt.Errorf("invalid JSON input; expected %s for %s", expected, valueType)
}

func unexpectedTokenError(unexpected token) error {
	switch unexpected.kind {
	case tokenInt32:
		return fmt.Errorf("invalid JSON input; unexpected number (%v) at position %d", unexpected.int32Number, unexpected.position)
	case tokenInt64:
		return fmt.Errorf("invalid JSON input; unexpected number (%v) at position %d", unexpected.int64Number, unexpected.position)
	case tokenDouble:
		return fmt.Errorf("invalid JSON input; unexpected number (%v) at position %d", unexpected.double, unexpected.position)
	case tokenString:
		return fmt.Errorf("invalid JSON input; unexpected string (\"%v\") at position %d", unexpected.text, unexpected.position)
	case tokenBoolean:
		return fmt.Errorf("invalid JSON input; unexpected boolean literal (%v) at position %d", unexpected.flag, unexpected.position)
	case tokenNull:
		return fmt.Errorf("invalid JSON input; unexpected null literal at position %d", unexpected.position)
	case tokenEndOfInput:
		return fmt.Errorf("invalid JSON input; unexpected end of input at position %d", unexpected.position)
	}
	return fmt.Errorf("invalid JSON input; unexpected %c at position %d", unexpected.delimiter, unexpected.position)
}
