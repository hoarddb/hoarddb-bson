// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: derived from bson/bsonrw/extjson_wrappers.go: the wrapper checks and their error text are kept, rewritten as methods returning typed values.

package extjson

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
)

// These turn the value a wrapper holds into the BSON value it stands for,
// with the vendor's error text, misspellings included ("balue", the "$" in
// "$invalid $numberDecimal string"), because the CLI prints it. Several
// errors come from strconv and encoding/hex unwrapped, as in the vendor.

func (wrapped jsonValue) binaryValue() ([]byte, byte, error) {
	if wrapped.kind != bsontype.EmbeddedDocument {
		return nil, 0, fmt.Errorf("$binary value should be object, but instead is %s", wrapped.kind)
	}
	var payload []byte
	var subtype byte
	payloadFound, subtypeFound := false, false
	for index, key := range wrapped.object.keys {
		member := wrapped.object.values[index]
		switch key {
		case "base64":
			if payloadFound {
				return nil, 0, errors.New("duplicate base64 key in $binary")
			}
			if member.kind != bsontype.String {
				return nil, 0, fmt.Errorf("$binary base64 value should be string, but instead is %s", member.kind)
			}
			decoded, err := base64.StdEncoding.DecodeString(member.text)
			if err != nil {
				return nil, 0, fmt.Errorf("invalid $binary base64 string: %s", member.text)
			}
			payload, payloadFound = decoded, true
		case "subType":
			if subtypeFound {
				return nil, 0, errors.New("duplicate subType key in $binary")
			}
			if member.kind != bsontype.String {
				return nil, 0, fmt.Errorf("$binary subType value should be string, but instead is %s", member.kind)
			}
			parsed, err := strconv.ParseUint(member.text, 16, 8)
			if err != nil {
				return nil, 0, fmt.Errorf("invalid $binary subType string: %q: %w", member.text, err)
			}
			subtype, subtypeFound = byte(parsed), true
		default:
			return nil, 0, fmt.Errorf("invalid key in $binary object: %s", key)
		}
	}
	if !payloadFound {
		return nil, 0, errors.New("missing base64 field in $binary object")
	}
	if !subtypeFound {
		return nil, 0, errors.New("missing subType field in $binary object")
	}
	return payload, subtype, nil
}

func (wrapped jsonValue) dbPointerValue() (string, primitive.ObjectID, error) {
	if wrapped.kind != bsontype.EmbeddedDocument {
		return "", primitive.NilObjectID, fmt.Errorf("$dbPointer value should be object, but instead is %s", wrapped.kind)
	}
	var namespace string
	var id primitive.ObjectID
	namespaceFound, idFound := false, false
	for index, key := range wrapped.object.keys {
		member := wrapped.object.values[index]
		switch key {
		case "$ref":
			if namespaceFound {
				return "", primitive.NilObjectID, errors.New("duplicate $ref key in $dbPointer")
			}
			if member.kind != bsontype.String {
				return "", primitive.NilObjectID, fmt.Errorf("$dbPointer $ref value should be string, but instead is %s", member.kind)
			}
			namespace, namespaceFound = member.text, true
		case "$id":
			if idFound {
				return "", primitive.NilObjectID, errors.New("duplicate $id key in $dbPointer")
			}
			if member.kind != bsontype.String {
				return "", primitive.NilObjectID, fmt.Errorf("$dbPointer $id value should be string, but instead is %s", member.kind)
			}
			parsed, err := primitive.ObjectIDFromHex(member.text)
			if err != nil {
				return "", primitive.NilObjectID, err
			}
			id, idFound = parsed, true
		default:
			return "", primitive.NilObjectID, fmt.Errorf("invalid key in $dbPointer object: %s", key)
		}
	}
	if !namespaceFound {
		return "", id, errors.New("missing $ref field in $dbPointer object")
	}
	if !idFound {
		return "", id, errors.New("missing $id field in $dbPointer object")
	}
	return namespace, id, nil
}

// dateLayouts are the two forms a relaxed $date string takes, tried in order.
var dateLayouts = []string{"2006-01-02T15:04:05.999Z07:00", "2006-01-02T15:04:05.999Z0700"}

func (wrapped jsonValue) dateTimeValue() (int64, error) {
	switch wrapped.kind {
	case bsontype.Int32:
		return int64(wrapped.int32Number), nil
	case bsontype.Int64:
		return wrapped.int64Number, nil
	case bsontype.String:
		return dateTimeFromText(wrapped.text)
	case bsontype.EmbeddedDocument:
		return dateTimeFromObject(wrapped.object)
	}
	return 0, fmt.Errorf("$date value should be string or object, but instead is %s", wrapped.kind)
}

func dateTimeFromText(dateText string) (int64, error) {
	var instant time.Time
	var err error
	for _, layout := range dateLayouts {
		instant, err = time.Parse(layout, dateText)
		if err == nil {
			break
		}
	}
	if err != nil {
		return 0, fmt.Errorf("invalid $date value string: %s", dateText)
	}
	return int64(primitive.NewDateTimeFromTime(instant)), nil
}

func dateTimeFromObject(dateObject *wrapperObject) (int64, error) {
	var milliseconds int64
	found := false
	for index, key := range dateObject.keys {
		member := dateObject.values[index]
		if key != "$numberLong" {
			return 0, fmt.Errorf("invalid key in $date object: %s", key)
		}
		if found {
			return 0, errors.New("duplicate $numberLong key in $date")
		}
		if member.kind != bsontype.String {
			return 0, fmt.Errorf("$date $numberLong field should be string, but instead is %s", member.kind)
		}
		parsed, err := member.int64Value()
		if err != nil {
			return 0, err
		}
		milliseconds, found = parsed, true
	}
	if !found {
		return 0, errors.New("missing $numberLong field in $date object")
	}
	return milliseconds, nil
}

func (wrapped jsonValue) decimal128Value() (primitive.Decimal128, error) {
	if wrapped.kind != bsontype.String {
		return primitive.Decimal128{}, fmt.Errorf("$numberDecimal value should be string, but instead is %s", wrapped.kind)
	}
	parsed, err := primitive.ParseDecimal128(wrapped.text)
	if err != nil {
		return primitive.Decimal128{}, fmt.Errorf("$invalid $numberDecimal string: %s", wrapped.text)
	}
	return parsed, nil
}

func (wrapped jsonValue) doubleValue() (float64, error) {
	if wrapped.kind == bsontype.Double {
		return wrapped.double, nil
	}
	if wrapped.kind != bsontype.String {
		return 0, fmt.Errorf("$numberDouble value should be string, but instead is %s", wrapped.kind)
	}
	switch wrapped.text {
	case "Infinity":
		return math.Inf(1), nil
	case "-Infinity":
		return math.Inf(-1), nil
	case "NaN":
		return math.NaN(), nil
	}
	return strconv.ParseFloat(wrapped.text, 64)
}

func (wrapped jsonValue) int32Value() (int32, error) {
	if wrapped.kind == bsontype.Int32 {
		return wrapped.int32Number, nil
	}
	if wrapped.kind != bsontype.String {
		return 0, fmt.Errorf("$numberInt value should be string, but instead is %s", wrapped.kind)
	}
	parsed, err := strconv.ParseInt(wrapped.text, 10, 64)
	if err != nil {
		return 0, err
	}
	if parsed < math.MinInt32 || parsed > math.MaxInt32 {
		return 0, fmt.Errorf("$numberInt value should be int32 but instead is int64: %d", parsed)
	}
	return int32(parsed), nil
}

func (wrapped jsonValue) int64Value() (int64, error) {
	if wrapped.kind == bsontype.Int64 {
		return wrapped.int64Number, nil
	}
	if wrapped.kind != bsontype.String {
		return 0, fmt.Errorf("$numberLong value should be string, but instead is %s", wrapped.kind)
	}
	return strconv.ParseInt(wrapped.text, 10, 64)
}

func (wrapped jsonValue) codeText() (string, error) {
	if wrapped.kind != bsontype.String {
		return "", fmt.Errorf("$code value should be string, but instead is %s", wrapped.kind)
	}
	return wrapped.text, nil
}

// checkKeyFlag checks a $minKey or $maxKey value; name is "min" or "max".
func (wrapped jsonValue) checkKeyFlag(name string) error {
	if wrapped.kind != bsontype.Int32 {
		return fmt.Errorf("$%sKey value should be int32, but instead is %s", name, wrapped.kind)
	}
	if wrapped.int32Number != 1 {
		return fmt.Errorf("$%sKey value must be 1, but instead is %d", name, wrapped.int32Number)
	}
	return nil
}

func (wrapped jsonValue) objectIDValue() (primitive.ObjectID, error) {
	if wrapped.kind != bsontype.String {
		return primitive.NilObjectID, fmt.Errorf("$oid value should be string, but instead is %s", wrapped.kind)
	}
	return primitive.ObjectIDFromHex(wrapped.text)
}

func (wrapped jsonValue) regexValue() (string, string, error) {
	if wrapped.kind != bsontype.EmbeddedDocument {
		return "", "", fmt.Errorf("$regularExpression value should be object, but instead is %s", wrapped.kind)
	}
	var pattern, options string
	patternFound, optionsFound := false, false
	for index, key := range wrapped.object.keys {
		member := wrapped.object.values[index]
		switch key {
		case "pattern":
			if patternFound {
				return "", "", errors.New("duplicate pattern key in $regularExpression")
			}
			if member.kind != bsontype.String {
				return "", "", fmt.Errorf("$regularExpression pattern value should be string, but instead is %s", member.kind)
			}
			pattern, patternFound = member.text, true
		case "options":
			if optionsFound {
				return "", "", errors.New("duplicate options key in $regularExpression")
			}
			if member.kind != bsontype.String {
				return "", "", fmt.Errorf("$regularExpression options value should be string, but instead is %s", member.kind)
			}
			options, optionsFound = member.text, true
		default:
			return "", "", fmt.Errorf("invalid key in $regularExpression object: %s", key)
		}
	}
	if !patternFound {
		return "", "", errors.New("missing pattern field in $regularExpression object")
	}
	if !optionsFound {
		return "", "", errors.New("missing options field in $regularExpression object")
	}
	return pattern, options, nil
}

func (wrapped jsonValue) symbolText() (string, error) {
	if wrapped.kind != bsontype.String {
		return "", fmt.Errorf("$symbol value should be string, but instead is %s", wrapped.kind)
	}
	return wrapped.text, nil
}

// timestampPart reads the t or i of a $timestamp.
func timestampPart(key string, member jsonValue, alreadyFound bool) (uint32, error) {
	if alreadyFound {
		return 0, fmt.Errorf("duplicate %s key in $timestamp", key)
	}
	switch member.kind {
	case bsontype.Int32:
		if member.int32Number < 0 {
			return 0, fmt.Errorf("$timestamp %s number should be uint32: %d", key, member.int32Number)
		}
		return uint32(member.int32Number), nil
	case bsontype.Int64:
		if member.int64Number < 0 || member.int64Number > int64(math.MaxUint32) {
			return 0, fmt.Errorf("$timestamp %s number should be uint32: %d", key, member.int64Number)
		}
		return uint32(member.int64Number), nil
	}
	return 0, fmt.Errorf("$timestamp %s value should be uint32, but instead is %s", key, member.kind)
}

func (wrapped jsonValue) timestampValue() (uint32, uint32, error) {
	if wrapped.kind != bsontype.EmbeddedDocument {
		return 0, 0, fmt.Errorf("$timestamp value should be object, but instead is %s", wrapped.kind)
	}
	var seconds, ordinal uint32
	secondsFound, ordinalFound := false, false
	for index, key := range wrapped.object.keys {
		member := wrapped.object.values[index]
		var err error
		switch key {
		case "t":
			seconds, err = timestampPart(key, member, secondsFound)
			secondsFound = true
		case "i":
			ordinal, err = timestampPart(key, member, ordinalFound)
			ordinalFound = true
		default:
			return 0, 0, fmt.Errorf("invalid key in $timestamp object: %s", key)
		}
		if err != nil {
			return 0, 0, err
		}
	}
	if !secondsFound {
		return 0, 0, errors.New("missing t field in $timestamp object")
	}
	if !ordinalFound {
		return 0, 0, errors.New("missing i field in $timestamp object")
	}
	return seconds, ordinal, nil
}

func (wrapped jsonValue) checkUndefined() error {
	if wrapped.kind != bsontype.Boolean {
		return fmt.Errorf("undefined value should be boolean, but instead is %s", wrapped.kind)
	}
	if !wrapped.flag {
		return fmt.Errorf("$undefined balue boolean should be true, but instead is %v", wrapped.flag)
	}
	return nil
}
