// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: derived from bson/bsoncodec/default_value_decoders.go (the scalar and primitive decoders and their decodeType halves), uint_codec.go, string_codec.go, byte_slice_codec.go and time_codec.go with their default options: each source type, conversion, width check and error text kept; a decoder reads into a Go value and sets the target once, chosen by the step 4c plan's Codec instead of a registry.

package decode

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"strconv"
	"time"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
)

// timeFormat is the layout TimeCodec parses a string with.
const timeFormat = "2006-01-02T15:04:05.999Z07:00"

var (
	intKinds  = []reflect.Kind{reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int}
	uintKinds = []reflect.Kind{reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uint}
)

// maxInt and maxUint are the widths of int and uint on this platform, kept
// as variables so the overflow checks read the same on 32 and 64 bits.
var (
	maxInt  = int64(math.MaxInt)
	maxUint = uint64(math.MaxUint)
)

func readBoolean(valueReader *reader) (bool, error) {
	switch valueType := valueReader.Type(); valueType {
	case bsontype.Int32:
		number, err := valueReader.ReadInt32()
		return number != 0, err
	case bsontype.Int64:
		number, err := valueReader.ReadInt64()
		return number != 0, err
	case bsontype.Double:
		number, err := valueReader.ReadDouble()
		return number != 0, err
	case bsontype.Boolean:
		return valueReader.ReadBoolean()
	case bsontype.Null:
		return false, valueReader.ReadNull()
	case bsontype.Undefined:
		return false, valueReader.ReadUndefined()
	default:
		return false, fmt.Errorf("cannot decode %v into a boolean", valueType)
	}
}

// readWideInteger reads any integer source as int64, before the width check
// of the target kind.
func readWideInteger(context decodeContext, valueReader *reader) (int64, error) {
	switch valueType := valueReader.Type(); valueType {
	case bsontype.Int32:
		number, err := valueReader.ReadInt32()
		return int64(number), err
	case bsontype.Int64:
		return valueReader.ReadInt64()
	case bsontype.Double:
		number, err := valueReader.ReadDouble()
		if err != nil {
			return 0, err
		}
		if !context.truncate && math.Floor(number) != number {
			return 0, errCannotTruncate
		}
		if number > float64(math.MaxInt64) {
			return 0, fmt.Errorf("%g overflows int64", number)
		}
		return int64(number), nil
	case bsontype.Boolean:
		flag, err := valueReader.ReadBoolean()
		if flag {
			return 1, err
		}
		return 0, err
	case bsontype.Null:
		return 0, valueReader.ReadNull()
	case bsontype.Undefined:
		return 0, valueReader.ReadUndefined()
	default:
		return 0, fmt.Errorf("cannot decode %v into an integer type", valueType)
	}
}

// readInt reads an integer for a Go value of kind, refusing one that does
// not fit, with the vendor's text.
func readInt(context decodeContext, valueReader *reader, kind reflect.Kind) (int64, error) {
	number, err := readWideInteger(context, valueReader)
	if err != nil {
		return 0, err
	}
	switch kind {
	case reflect.Int8:
		if number < math.MinInt8 || number > math.MaxInt8 {
			return 0, fmt.Errorf("%d overflows int8", number)
		}
	case reflect.Int16:
		if number < math.MinInt16 || number > math.MaxInt16 {
			return 0, fmt.Errorf("%d overflows int16", number)
		}
	case reflect.Int32:
		if number < math.MinInt32 || number > math.MaxInt32 {
			return 0, fmt.Errorf("%d overflows int32", number)
		}
	case reflect.Int:
		// The vendor checks only the top of int's range.
		if number > maxInt {
			return 0, fmt.Errorf("%d overflows int", number)
		}
	}
	return number, nil
}

// readUint is the UIntCodec's decode: the integer sources with null and
// undefined, then a width check per kind.
func readUint(context decodeContext, valueReader *reader, kind reflect.Kind) (uint64, error) {
	number, err := readWideInteger(context, valueReader)
	if err != nil {
		return 0, err
	}
	switch kind {
	case reflect.Uint8:
		if number < 0 || number > math.MaxUint8 {
			return 0, fmt.Errorf("%d overflows uint8", number)
		}
	case reflect.Uint16:
		if number < 0 || number > math.MaxUint16 {
			return 0, fmt.Errorf("%d overflows uint16", number)
		}
	case reflect.Uint32:
		if number < 0 || number > math.MaxUint32 {
			return 0, fmt.Errorf("%d overflows uint32", number)
		}
	case reflect.Uint64:
		if number < 0 {
			return 0, fmt.Errorf("%d overflows uint64", number)
		}
	case reflect.Uint:
		if number < 0 || uint64(number) > maxUint {
			return 0, fmt.Errorf("%d overflows uint", number)
		}
	}
	return uint64(number), nil
}

// readFloat reads a float for a float32 or float64, refusing a double a
// float32 cannot hold exactly unless the field is tagged truncate.
func readFloat(context decodeContext, valueReader *reader, kind reflect.Kind) (float64, error) {
	var number float64
	var err error
	switch valueType := valueReader.Type(); valueType {
	case bsontype.Int32:
		var integer int32
		integer, err = valueReader.ReadInt32()
		number = float64(integer)
	case bsontype.Int64:
		var integer int64
		integer, err = valueReader.ReadInt64()
		number = float64(integer)
	case bsontype.Double:
		number, err = valueReader.ReadDouble()
	case bsontype.Boolean:
		var flag bool
		flag, err = valueReader.ReadBoolean()
		if flag {
			number = 1
		}
	case bsontype.Null:
		err = valueReader.ReadNull()
	case bsontype.Undefined:
		err = valueReader.ReadUndefined()
	default:
		return 0, fmt.Errorf("cannot decode %v into a float32 or float64 type", valueType)
	}
	if err != nil {
		return 0, err
	}
	if kind == reflect.Float32 && !context.truncate && float64(float32(number)) != number {
		return 0, errCannotTruncate
	}
	return number, nil
}

// readStringValue is the StringCodec's decode, with ObjectIDs as hex (the
// vendor's default option).
func readStringValue(valueReader *reader) (string, error) {
	switch valueType := valueReader.Type(); valueType {
	case bsontype.String:
		return valueReader.ReadString()
	case bsontype.ObjectID:
		id, err := valueReader.ReadObjectID()
		if err != nil {
			return "", err
		}
		return id.Hex(), nil
	case bsontype.Symbol:
		return valueReader.ReadSymbol()
	case bsontype.Binary:
		payload, subtype, err := valueReader.ReadBinary()
		if err != nil {
			return "", err
		}
		if subtype != bsontype.BinaryGeneric && subtype != bsontype.BinaryBinaryOld {
			return "", decodeBinaryError{subtype: subtype, typeName: "string"}
		}
		return string(payload), nil
	case bsontype.Null:
		return "", valueReader.ReadNull()
	case bsontype.Undefined:
		return "", valueReader.ReadUndefined()
	default:
		return "", fmt.Errorf("cannot decode %v into a string type", valueType)
	}
}

// readByteSlice is the ByteSliceCodec's decode. A null or undefined value
// gives a nil slice.
func readByteSlice(valueReader *reader) ([]byte, error) {
	switch valueType := valueReader.Type(); valueType {
	case bsontype.String:
		text, err := valueReader.ReadString()
		if err != nil {
			return nil, err
		}
		return []byte(text), nil
	case bsontype.Symbol:
		symbol, err := valueReader.ReadSymbol()
		if err != nil {
			return nil, err
		}
		return []byte(symbol), nil
	case bsontype.Binary:
		payload, subtype, err := valueReader.ReadBinary()
		if err != nil {
			return nil, err
		}
		if subtype != bsontype.BinaryGeneric && subtype != bsontype.BinaryBinaryOld {
			return nil, decodeBinaryError{subtype: subtype, typeName: "[]byte"}
		}
		return payload, nil
	case bsontype.Null:
		return nil, valueReader.ReadNull()
	case bsontype.Undefined:
		return nil, valueReader.ReadUndefined()
	default:
		return nil, fmt.Errorf("cannot decode %v into a []byte", valueType)
	}
}

// readTime is the TimeCodec's decode, in UTC (row T2).
func readTime(valueReader *reader) (time.Time, error) {
	var decoded time.Time
	switch valueType := valueReader.Type(); valueType {
	case bsontype.DateTime:
		milliseconds, err := valueReader.ReadDateTime()
		if err != nil {
			return time.Time{}, err
		}
		decoded = time.Unix(milliseconds/1000, milliseconds%1000*1000000)
	case bsontype.String:
		text, err := valueReader.ReadString()
		if err != nil {
			return time.Time{}, err
		}
		decoded, err = time.Parse(timeFormat, text)
		if err != nil {
			return time.Time{}, err
		}
	case bsontype.Int64:
		milliseconds, err := valueReader.ReadInt64()
		if err != nil {
			return time.Time{}, err
		}
		decoded = time.Unix(milliseconds/1000, milliseconds%1000*1000000)
	case bsontype.Timestamp:
		seconds, _, err := valueReader.ReadTimestamp()
		if err != nil {
			return time.Time{}, err
		}
		decoded = time.Unix(int64(seconds), 0)
	case bsontype.Null:
		if err := valueReader.ReadNull(); err != nil {
			return time.Time{}, err
		}
	case bsontype.Undefined:
		if err := valueReader.ReadUndefined(); err != nil {
			return time.Time{}, err
		}
	default:
		return time.Time{}, fmt.Errorf("cannot decode %v into a time.Time", valueType)
	}
	return decoded.UTC(), nil
}

// readNullOrUndefined reads a null or an undefined value for a decoder that
// accepts both and gives its zero value, and refuses any other type with
// "cannot decode <type> into <description>".
func readNullOrUndefined(valueReader *reader, valueType bsontype.Type, description string) error {
	switch valueType {
	case bsontype.Null:
		return valueReader.ReadNull()
	case bsontype.Undefined:
		return valueReader.ReadUndefined()
	}
	return fmt.Errorf("cannot decode %v into %s", valueType, description)
}

func readJavaScript(valueReader *reader) (primitive.JavaScript, error) {
	if valueType := valueReader.Type(); valueType != bsontype.JavaScript {
		return "", readNullOrUndefined(valueReader, valueType, "a primitive.JavaScript")
	}
	code, err := valueReader.ReadJavascript()
	return primitive.JavaScript(code), err
}

func readSymbol(valueReader *reader) (primitive.Symbol, error) {
	switch valueType := valueReader.Type(); valueType {
	case bsontype.String:
		symbol, err := valueReader.ReadString()
		return primitive.Symbol(symbol), err
	case bsontype.Symbol:
		symbol, err := valueReader.ReadSymbol()
		return primitive.Symbol(symbol), err
	case bsontype.Binary:
		payload, subtype, err := valueReader.ReadBinary()
		if err != nil {
			return "", err
		}
		if subtype != bsontype.BinaryGeneric && subtype != bsontype.BinaryBinaryOld {
			return "", decodeBinaryError{subtype: subtype, typeName: "primitive.Symbol"}
		}
		return primitive.Symbol(payload), nil
	default:
		return "", readNullOrUndefined(valueReader, valueType, "a primitive.Symbol")
	}
}

func readBinaryValue(valueReader *reader) (primitive.Binary, error) {
	if valueType := valueReader.Type(); valueType != bsontype.Binary {
		return primitive.Binary{}, readNullOrUndefined(valueReader, valueType, "a Binary")
	}
	payload, subtype, err := valueReader.ReadBinary()
	return primitive.Binary{Subtype: subtype, Data: payload}, err
}

func readUndefined(valueReader *reader) error {
	switch valueType := valueReader.Type(); valueType {
	case bsontype.Undefined:
		return valueReader.ReadUndefined()
	case bsontype.Null:
		return valueReader.ReadNull()
	default:
		return fmt.Errorf("cannot decode %v into an Undefined", valueType)
	}
}

func readObjectID(valueReader *reader) (primitive.ObjectID, error) {
	switch valueType := valueReader.Type(); valueType {
	case bsontype.ObjectID:
		return valueReader.ReadObjectID()
	case bsontype.String:
		text, err := valueReader.ReadString()
		if err != nil {
			return primitive.ObjectID{}, err
		}
		if id, err := primitive.ObjectIDFromHex(text); err == nil {
			return id, nil
		}
		if len(text) != 12 {
			return primitive.ObjectID{}, fmt.Errorf("an ObjectID string must be exactly 12 bytes long (got %v)", len(text))
		}
		var id primitive.ObjectID
		copy(id[:], text)
		return id, nil
	default:
		return primitive.ObjectID{}, readNullOrUndefined(valueReader, valueType, "an ObjectID")
	}
}

func readDateTime(valueReader *reader) (primitive.DateTime, error) {
	if valueType := valueReader.Type(); valueType != bsontype.DateTime {
		return 0, readNullOrUndefined(valueReader, valueType, "a DateTime")
	}
	milliseconds, err := valueReader.ReadDateTime()
	return primitive.DateTime(milliseconds), err
}

func readNull(valueReader *reader) error {
	switch valueType := valueReader.Type(); valueType {
	case bsontype.Undefined:
		return valueReader.ReadUndefined()
	case bsontype.Null:
		return valueReader.ReadNull()
	default:
		return fmt.Errorf("cannot decode %v into a Null", valueType)
	}
}

func readRegex(valueReader *reader) (primitive.Regex, error) {
	if valueType := valueReader.Type(); valueType != bsontype.Regex {
		return primitive.Regex{}, readNullOrUndefined(valueReader, valueType, "a Regex")
	}
	pattern, options, err := valueReader.ReadRegex()
	return primitive.Regex{Pattern: pattern, Options: options}, err
}

func readDBPointer(valueReader *reader) (primitive.DBPointer, error) {
	if valueType := valueReader.Type(); valueType != bsontype.DBPointer {
		return primitive.DBPointer{}, readNullOrUndefined(valueReader, valueType, "a DBPointer")
	}
	namespace, pointer, err := valueReader.ReadDBPointer()
	return primitive.DBPointer{DB: namespace, Pointer: pointer}, err
}

func readTimestamp(valueReader *reader) (primitive.Timestamp, error) {
	if valueType := valueReader.Type(); valueType != bsontype.Timestamp {
		return primitive.Timestamp{}, readNullOrUndefined(valueReader, valueType, "a Timestamp")
	}
	seconds, increment, err := valueReader.ReadTimestamp()
	return primitive.Timestamp{T: seconds, I: increment}, err
}

func readMinKey(valueReader *reader) error {
	if valueType := valueReader.Type(); valueType != bsontype.MinKey {
		return readNullOrUndefined(valueReader, valueType, "a MinKey")
	}
	return valueReader.ReadMinKey()
}

func readMaxKey(valueReader *reader) error {
	if valueType := valueReader.Type(); valueType != bsontype.MaxKey {
		return readNullOrUndefined(valueReader, valueType, "a MaxKey")
	}
	return valueReader.ReadMaxKey()
}

func readDecimal128(valueReader *reader) (primitive.Decimal128, error) {
	if valueType := valueReader.Type(); valueType != bsontype.Decimal128 {
		return primitive.Decimal128{}, readNullOrUndefined(valueReader, valueType, "a primitive.Decimal128")
	}
	return valueReader.ReadDecimal128()
}

func readJSONNumber(valueReader *reader) (json.Number, error) {
	switch valueType := valueReader.Type(); valueType {
	case bsontype.Double:
		number, err := valueReader.ReadDouble()
		if err != nil {
			return "", err
		}
		return json.Number(strconv.FormatFloat(number, 'f', -1, 64)), nil
	case bsontype.Int32:
		number, err := valueReader.ReadInt32()
		if err != nil {
			return "", err
		}
		return json.Number(strconv.FormatInt(int64(number), 10)), nil
	case bsontype.Int64:
		number, err := valueReader.ReadInt64()
		if err != nil {
			return "", err
		}
		return json.Number(strconv.FormatInt(number, 10)), nil
	default:
		return "", readNullOrUndefined(valueReader, valueType, "a json.Number")
	}
}

func readURL(valueReader *reader) (url.URL, error) {
	if valueType := valueReader.Type(); valueType != bsontype.String {
		return url.URL{}, readNullOrUndefined(valueReader, valueType, "a *url.URL")
	}
	text, err := valueReader.ReadString()
	if err != nil {
		return url.URL{}, err
	}
	parsed, err := url.Parse(text)
	if err != nil {
		return url.URL{}, err
	}
	return *parsed, nil
}
