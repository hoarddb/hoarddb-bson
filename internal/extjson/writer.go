// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: derived from bson/bsonrw/extjson_writer.go (the text of every value, formatDouble, the date and regex rules, the trailing-comma close): rewritten as an append-only text writer without a mode stack, which refuses a level past MaxNestingDepth; the string escaping is not ported (escape.go, written fresh under build doc S-3); the refusal's sentinel is bson.ErrNestingTooDeep's (step 5's Go-value half).

package extjson

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/internal/nesting"
)

// ErrNestingTooDeep is under the writer's refusal of a document that opens
// a level past MaxNestingDepth (spec 5.2), and under the parser's refusal
// of a text nesting objects past it. The vendor has no limit on the writer;
// the sentinel is bson.ErrNestingTooDeep, so every refusal matches it.
var ErrNestingTooDeep = nesting.ErrTooDeep

// rfc3339Milli is the vendor's layout for a relaxed date.
const rfc3339Milli = "2006-01-02T15:04:05.999Z07:00"

// textWriter appends Extended JSON. Every value is followed by a comma, and
// closing a container replaces the comma after its last value (or appends
// the closer after an empty one), as the vendor's writer does. The method
// set is the vendor's value writer's, so a Go-value encoder can drive it
// as the vendor's encoder drives its writer (docs/core-api.md).
type textWriter struct {
	buffer     []byte
	canonical  bool
	escapeHTML bool
	// level is the number of documents, arrays and scopes open.
	level int
}

func (writer *textWriter) open(opener byte) error {
	writer.level++
	if writer.level > MaxNestingDepth {
		return fmt.Errorf("%w: more than %d levels", ErrNestingTooDeep, MaxNestingDepth)
	}
	writer.buffer = append(writer.buffer, opener)
	return nil
}

func (writer *textWriter) close(closer byte) {
	if last := len(writer.buffer) - 1; writer.buffer[last] == ',' {
		writer.buffer[last] = closer
	} else {
		writer.buffer = append(writer.buffer, closer)
	}
	writer.level--
}

// endValue ends a value inside a container.
func (writer *textWriter) endValue() {
	writer.buffer = append(writer.buffer, ',')
}

func (writer *textWriter) openDocument() error { return writer.open('{') }
func (writer *textWriter) openArray() error    { return writer.open('[') }

// openScope opens a code with scope, whose scope is the new level.
func (writer *textWriter) openScope(code string) error {
	if err := writer.open('{'); err != nil {
		return err
	}
	writer.buffer = append(writer.buffer, `"$code":`...)
	writer.buffer = appendQuoted(writer.buffer, code, writer.escapeHTML)
	writer.buffer = append(writer.buffer, `,"$scope":{`...)
	return nil
}

// closeDocument closes a document below the root, or the root when root.
func (writer *textWriter) closeDocument(root bool) {
	writer.close('}')
	if !root {
		writer.endValue()
	}
}

func (writer *textWriter) closeArray() {
	writer.close(']')
	writer.endValue()
}

func (writer *textWriter) closeScope() {
	writer.close('}')
	writer.buffer = append(writer.buffer, '}')
	writer.endValue()
}

func (writer *textWriter) key(name string) {
	writer.buffer = appendQuoted(writer.buffer, name, writer.escapeHTML)
	writer.buffer = append(writer.buffer, ':')
}

// wrappedQuoted appends {"$<wrapper>":"<text>"} with the text as it is.
func (writer *textWriter) wrappedQuoted(wrapper, text string) {
	writer.buffer = append(writer.buffer, `{"$`...)
	writer.buffer = append(writer.buffer, wrapper...)
	writer.buffer = append(writer.buffer, `":"`...)
	writer.buffer = append(writer.buffer, text...)
	writer.buffer = append(writer.buffer, `"},`...)
}

func (writer *textWriter) writeDouble(number float64) {
	text := formatDouble(number)
	// Relaxed JSON has no number for the infinities and NaN.
	if writer.canonical || math.IsInf(number, 0) || math.IsNaN(number) {
		writer.wrappedQuoted("numberDouble", text)
		return
	}
	writer.buffer = append(writer.buffer, text...)
	writer.endValue()
}

// formatDouble is the vendor's: the shortest text that reads back as the
// same double, in the exponent form strconv's 'G' picks, with ".0" added
// to an integral value written without an exponent.
func formatDouble(number float64) string {
	switch {
	case math.IsInf(number, 1):
		return "Infinity"
	case math.IsInf(number, -1):
		return "-Infinity"
	case math.IsNaN(number):
		return "NaN"
	}
	text := strconv.FormatFloat(number, 'G', -1, 64)
	if !strings.ContainsAny(text, "E.") {
		text += ".0"
	}
	return text
}

func (writer *textWriter) writeString(text string) {
	writer.buffer = appendQuoted(writer.buffer, text, writer.escapeHTML)
	writer.endValue()
}

func (writer *textWriter) writeBinary(subtype byte, payload []byte) {
	writer.buffer = append(writer.buffer, `{"$binary":{"base64":"`...)
	writer.buffer = base64.StdEncoding.AppendEncode(writer.buffer, payload)
	writer.buffer = append(writer.buffer, `","subType":"`...)
	writer.buffer = append(writer.buffer, lowerHexDigits[subtype>>4], lowerHexDigits[subtype&0xF])
	writer.buffer = append(writer.buffer, `"}},`...)
}

func (writer *textWriter) writeLiteral(literal string) {
	writer.buffer = append(writer.buffer, literal...)
	writer.endValue()
}

func (writer *textWriter) writeObjectID(objectID primitive.ObjectID) {
	writer.wrappedQuoted("oid", objectID.Hex())
}

func (writer *textWriter) writeBoolean(flag bool) {
	writer.writeLiteral(strconv.FormatBool(flag))
}

// writeDateTime writes milliseconds since the epoch. The relaxed form is a
// date string only for years 1970 to 9999.
func (writer *textWriter) writeDateTime(milliseconds int64) {
	instant := time.Unix(milliseconds/1e3, milliseconds%1e3*1e6).UTC()
	writer.buffer = append(writer.buffer, `{"$date":`...)
	if writer.canonical || instant.Year() < 1970 || instant.Year() > 9999 {
		writer.buffer = append(writer.buffer, `{"$numberLong":"`...)
		writer.buffer = strconv.AppendInt(writer.buffer, milliseconds, 10)
		writer.buffer = append(writer.buffer, `"}},`...)
		return
	}
	writer.buffer = append(writer.buffer, '"')
	writer.buffer = instant.AppendFormat(writer.buffer, rfc3339Milli)
	writer.buffer = append(writer.buffer, `"},`...)
}

// writeRegex writes the options sorted by rune, which turns each byte that
// is not UTF-8 into U+FFFD first, as the vendor's []rune conversion does.
func (writer *textWriter) writeRegex(pattern, options string) {
	optionRunes := []rune(options)
	slices.Sort(optionRunes)
	writer.buffer = append(writer.buffer, `{"$regularExpression":{"pattern":`...)
	writer.buffer = appendQuoted(writer.buffer, pattern, writer.escapeHTML)
	writer.buffer = append(writer.buffer, `,"options":`...)
	writer.buffer = appendQuoted(writer.buffer, string(optionRunes), writer.escapeHTML)
	writer.buffer = append(writer.buffer, `}},`...)
}

// writeDBPointer writes the namespace without escaping, as the vendor
// does: a quote or a control byte in it is copied into the text.
func (writer *textWriter) writeDBPointer(namespace string, objectID primitive.ObjectID) {
	writer.buffer = append(writer.buffer, `{"$dbPointer":{"$ref":"`...)
	writer.buffer = append(writer.buffer, namespace...)
	writer.buffer = append(writer.buffer, `","$id":{"$oid":"`...)
	writer.buffer = hex.AppendEncode(writer.buffer, objectID[:])
	writer.buffer = append(writer.buffer, `"}}},`...)
}

// writeWrappedString writes {"$<wrapper>":"<escaped text>"}.
func (writer *textWriter) writeWrappedString(wrapper, text string) {
	writer.buffer = append(writer.buffer, `{"$`...)
	writer.buffer = append(writer.buffer, wrapper...)
	writer.buffer = append(writer.buffer, `":`...)
	writer.buffer = appendQuoted(writer.buffer, text, writer.escapeHTML)
	writer.buffer = append(writer.buffer, `},`...)
}

func (writer *textWriter) writeJavaScript(code string) { writer.writeWrappedString("code", code) }
func (writer *textWriter) writeSymbol(symbol string)   { writer.writeWrappedString("symbol", symbol) }

func (writer *textWriter) writeInt32(number int32) {
	writer.writeInteger("numberInt", int64(number))
}

func (writer *textWriter) writeInt64(number int64) {
	writer.writeInteger("numberLong", number)
}

func (writer *textWriter) writeInteger(wrapper string, number int64) {
	if writer.canonical {
		writer.wrappedQuoted(wrapper, strconv.FormatInt(number, 10))
		return
	}
	writer.buffer = strconv.AppendInt(writer.buffer, number, 10)
	writer.endValue()
}

func (writer *textWriter) writeTimestamp(seconds, increment uint32) {
	writer.buffer = append(writer.buffer, `{"$timestamp":{"t":`...)
	writer.buffer = strconv.AppendUint(writer.buffer, uint64(seconds), 10)
	writer.buffer = append(writer.buffer, `,"i":`...)
	writer.buffer = strconv.AppendUint(writer.buffer, uint64(increment), 10)
	writer.buffer = append(writer.buffer, `}},`...)
}

func (writer *textWriter) writeDecimal128(decimal primitive.Decimal128) {
	writer.wrappedQuoted("numberDecimal", decimal.String())
}

func (writer *textWriter) writeUndefined() { writer.writeLiteral(`{"$undefined":true}`) }
func (writer *textWriter) writeNull()      { writer.writeLiteral("null") }
func (writer *textWriter) writeMinKey()    { writer.writeLiteral(`{"$minKey":1}`) }
func (writer *textWriter) writeMaxKey()    { writer.writeLiteral(`{"$maxKey":1}`) }
