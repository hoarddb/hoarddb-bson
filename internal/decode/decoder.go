// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: derived from bson/decoder.go (Decoder.Decode), bson/unmarshal.go, bson/raw_value.go (UnmarshalWithRegistry) and bson/bsoncodec/bsoncodec.go (decodeTypeOrValueWithInfo, DecodeContext): the argument checks and their text kept; the registry lookup replaced by the step 4c plan; the decode context holds only the ancestor and Truncate, the two fields the default registry reads; a nil pointer to RawValue's Unmarshal is an error where the vendor panics.

// Package decode is the reflection decoder: BSON bytes into Go values,
// with the vendor's (v1.17.10) default type mapping, ancestor rule, hooks,
// quirks and error text, driven by the per-type plans of internal/plan.
// Package bson's Unmarshal, RawValue.Unmarshal and Document.Decode (step
// 4f) call it.
package decode

import (
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/internal/plan"
)

// Decoder decodes with the plans of one cache. Its tables are built once,
// on first use; after that it is read only, so one Decoder serves
// concurrent calls.
type Decoder struct {
	cache *plan.Cache

	tablesOnce sync.Once
	tablesErr  error
	// interfacePlan is interface{}'s plan, documentPlan primitive.D's.
	interfacePlan *plan.Type
	documentPlan  *plan.Type
	// typeMap is the Go type an interface{} receives for each BSON type,
	// the vendor's RegisterTypeMapEntry table; nil where it has no entry.
	typeMap [256]*plan.Type
}

// typeMapEntries is the vendor's default type map. A null has no entry: an
// interface{} receives nil for it.
var typeMapEntries = map[bsontype.Type]reflect.Type{
	bsontype.Double:           reflect.TypeOf(float64(0)),
	bsontype.String:           reflect.TypeOf(""),
	bsontype.Array:            reflect.TypeOf(primitive.A{}),
	bsontype.Binary:           reflect.TypeOf(primitive.Binary{}),
	bsontype.Undefined:        reflect.TypeOf(primitive.Undefined{}),
	bsontype.ObjectID:         reflect.TypeOf(primitive.ObjectID{}),
	bsontype.Boolean:          reflect.TypeOf(false),
	bsontype.DateTime:         reflect.TypeOf(primitive.DateTime(0)),
	bsontype.Regex:            reflect.TypeOf(primitive.Regex{}),
	bsontype.DBPointer:        reflect.TypeOf(primitive.DBPointer{}),
	bsontype.JavaScript:       reflect.TypeOf(primitive.JavaScript("")),
	bsontype.Symbol:           reflect.TypeOf(primitive.Symbol("")),
	bsontype.CodeWithScope:    reflect.TypeOf(primitive.CodeWithScope{}),
	bsontype.Int32:            reflect.TypeOf(int32(0)),
	bsontype.Int64:            reflect.TypeOf(int64(0)),
	bsontype.Timestamp:        reflect.TypeOf(primitive.Timestamp{}),
	bsontype.Decimal128:       reflect.TypeOf(primitive.Decimal128{}),
	bsontype.MinKey:           reflect.TypeOf(primitive.MinKey{}),
	bsontype.MaxKey:           reflect.TypeOf(primitive.MaxKey{}),
	bsontype.Type(0):          reflect.TypeOf(primitive.D{}),
	bsontype.EmbeddedDocument: reflect.TypeOf(primitive.D{}),
}

var emptyInterfaceType = reflect.TypeOf((*any)(nil)).Elem()

// New returns a Decoder that takes its plans from cache, whose BSON types
// must be package bson's Raw and RawValue: the cache step 4f declares and
// also hands to encode.New.
func New(cache *plan.Cache) *Decoder {
	return &Decoder{cache: cache}
}

// Cache returns the plan cache the Decoder takes its plans from, so that
// package bson can test that its encoder and decoder share one.
func (decoder *Decoder) Cache() *plan.Cache {
	return decoder.cache
}

// buildTables plans interface{} and the type map's Go types, once.
func (decoder *Decoder) buildTables() {
	interfacePlan, err := decoder.cache.Lookup(emptyInterfaceType)
	if err != nil {
		decoder.tablesErr = fmt.Errorf("planning interface{}: %w", err)
		return
	}
	decoder.interfacePlan = interfacePlan
	for valueType, goType := range typeMapEntries {
		typePlan, err := decoder.cache.Lookup(goType)
		if err != nil {
			decoder.tablesErr = fmt.Errorf("planning %s: %w", goType, err)
			return
		}
		decoder.typeMap[valueType] = typePlan
	}
	decoder.documentPlan = decoder.typeMap[bsontype.EmbeddedDocument]
}

// decodeContext is what a decoder passes to the decoders of the values
// inside the one it decodes: the ancestor an interface{} decodes an
// embedded document as (rows D1 to D7), and whether a double may be
// truncated, from a truncate tag. A struct field starts a fresh one.
type decodeContext struct {
	ancestor *plan.Type
	truncate bool
}

// Unmarshal decodes the document in documentBytes into target, a non-nil pointer or
// map, as the vendor's bson.Unmarshal does. A target implementing
// UnmarshalBSON gets the document's bytes.
func (decoder *Decoder) Unmarshal(documentBytes []byte, target any) error {
	var valueReader reader
	newDocumentReader(documentBytes, &valueReader)
	if hook, isHook := target.(plan.Unmarshaler); isHook {
		_, documentBytes, err := valueReader.ReadValueBytes(nil)
		if err != nil {
			return err
		}
		return hook.UnmarshalBSON(documentBytes)
	}
	targetValue := reflect.ValueOf(target)
	switch targetValue.Kind() {
	case reflect.Pointer:
		if targetValue.IsNil() {
			return errDecodeToNil
		}
		targetValue = targetValue.Elem()
	case reflect.Map:
		if targetValue.IsNil() {
			return errDecodeToNil
		}
	default:
		return fmt.Errorf("argument to Decode must be a pointer or a map, but got %v", targetValue)
	}
	return decoder.decodeRoot(&valueReader, targetValue)
}

// UnmarshalValue decodes one value of valueType, held in valueBytes, into target,
// as the vendor's RawValue.Unmarshal does.
func (decoder *Decoder) UnmarshalValue(valueType bsontype.Type, valueBytes []byte, target any) error {
	var valueReader reader
	newValueReader(valueType, valueBytes, &valueReader)
	targetValue := reflect.ValueOf(target)
	if targetValue.Kind() != reflect.Pointer {
		return fmt.Errorf("argument to Unmarshal* must be a pointer to a type, but got %v", targetValue)
	}
	if targetValue.IsNil() {
		// The vendor panics here, asking a nil pointer's element its type.
		return errDecodeToNil
	}
	return decoder.decodeRoot(&valueReader, targetValue.Elem())
}

func (decoder *Decoder) decodeRoot(valueReader *reader, target reflect.Value) error {
	decoder.tablesOnce.Do(decoder.buildTables)
	if decoder.tablesErr != nil {
		return decoder.tablesErr
	}
	targetPlan, err := decoder.cache.Lookup(target.Type())
	if err != nil {
		return fmt.Errorf("planning %s: %w", target.Type(), err)
	}
	if targetPlan.Decode.Codec == plan.NoCodec {
		return noDecoderError{goType: target.Type()}
	}
	return decoder.decodeValue(decodeContext{}, valueReader, targetPlan, target)
}

// decodeValue decodes the value valueReader is on into target with the
// codec the plan names: the vendor's ValueDecoder.DecodeValue.
func (decoder *Decoder) decodeValue(context decodeContext, valueReader *reader, typePlan *plan.Type, target reflect.Value) error {
	codec := typePlan.Decode.Codec
	if typePlan.Decode.AddressedOnly && !target.CanAddr() {
		// The vendor's condAddrDecoder: the hook needs the address.
		codec = typePlan.Decode.Otherwise
	}
	switch codec {
	case plan.BooleanCodec:
		if !target.IsValid() || !target.CanSet() || target.Kind() != reflect.Bool {
			return valueDecoderError{name: "BooleanDecodeValue", kinds: []reflect.Kind{reflect.Bool}, received: target}
		}
		flag, err := readBoolean(valueReader)
		if err != nil {
			return err
		}
		target.SetBool(flag)
		return nil
	case plan.IntCodec:
		if !target.CanSet() {
			return valueDecoderError{name: "IntDecodeValue", kinds: intKinds, received: target}
		}
		number, err := readInt(context, valueReader, target.Kind())
		if err != nil {
			return err
		}
		target.SetInt(number)
		return nil
	case plan.UintCodec:
		if !target.CanSet() {
			return valueDecoderError{name: "UintDecodeValue", kinds: uintKinds, received: target}
		}
		number, err := readUint(context, valueReader, target.Kind())
		if err != nil {
			return err
		}
		target.SetUint(number)
		return nil
	case plan.FloatCodec:
		if !target.CanSet() {
			return valueDecoderError{name: "FloatDecodeValue", kinds: []reflect.Kind{reflect.Float32, reflect.Float64}, received: target}
		}
		number, err := readFloat(context, valueReader, target.Kind())
		if err != nil {
			return err
		}
		target.SetFloat(number)
		return nil
	case plan.StringCodec:
		if !target.CanSet() || target.Kind() != reflect.String {
			return valueDecoderError{name: "StringDecodeValue", kinds: []reflect.Kind{reflect.String}, received: target}
		}
		text, err := readStringValue(valueReader)
		if err != nil {
			return err
		}
		target.SetString(text)
		return nil
	case plan.EmptyInterfaceCodec:
		if !target.CanSet() {
			return valueDecoderError{name: "EmptyInterfaceDecodeValue", typeNames: []string{"interface {}"}, received: target}
		}
		held, err := decoder.decodeInterface(context, valueReader)
		if err != nil {
			return err
		}
		target.Set(held)
		return nil
	case plan.ArrayCodec:
		return decoder.decodeArray(context, valueReader, typePlan, target)
	case plan.SliceCodec:
		return decoder.decodeSlice(context, valueReader, typePlan, target)
	case plan.MapCodec:
		return decoder.decodeMap(context, valueReader, typePlan, target)
	case plan.StructCodec:
		return decoder.decodeStruct(context, valueReader, typePlan, target)
	case plan.PointerCodec:
		return decoder.decodePointer(context, valueReader, typePlan, target)
	case plan.DCodec:
		return decoder.decodeD(context, valueReader, target)
	case plan.RawCodec, plan.CoreDocumentCodec, plan.CoreArrayCodec:
		return decodeValueBytes(valueReader, codec, target)
	case plan.RawValueCodec:
		if !target.CanSet() {
			return valueDecoderError{name: "RawValueDecodeValue", typeNames: []string{target.Type().String()}, received: target}
		}
		valueType, valueBytes, err := valueReader.ReadValueBytes(nil)
		if err != nil {
			return err
		}
		target.Set(reflect.Zero(target.Type()))
		target.Field(0).SetUint(uint64(valueType))
		target.Field(1).SetBytes(valueBytes)
		return nil
	case plan.ValueUnmarshalerHook:
		return decodeValueHook(valueReader, target)
	case plan.UnmarshalerHook:
		return decodeBytesHook(valueReader, target)
	case plan.NoCodec:
		return noDecoderError{goType: target.Type()}
	}
	return decoder.decodeTypeCodecInto(context, valueReader, codec, target)
}

// typeCodecNames are the vendor's decoder names and the type name its
// ValueDecoderError shows, for each type codec.
var typeCodecNames = map[plan.Codec][2]string{
	plan.ByteSliceCodec:     {"ByteSliceDecodeValue", "[]uint8"},
	plan.TimeCodec:          {"TimeDecodeValue", "time.Time"},
	plan.ObjectIDCodec:      {"ObjectIDDecodeValue", "primitive.ObjectID"},
	plan.Decimal128Codec:    {"Decimal128DecodeValue", "primitive.Decimal128"},
	plan.JSONNumberCodec:    {"JSONNumberDecodeValue", "json.Number"},
	plan.URLCodec:           {"URLDecodeValue", "url.URL"},
	plan.JavaScriptCodec:    {"JavaScriptDecodeValue", "primitive.JavaScript"},
	plan.SymbolCodec:        {"SymbolDecodeValue", "primitive.Symbol"},
	plan.BinaryCodec:        {"BinaryDecodeValue", "primitive.Binary"},
	plan.UndefinedCodec:     {"UndefinedDecodeValue", "primitive.Undefined"},
	plan.DateTimeCodec:      {"DateTimeDecodeValue", "primitive.DateTime"},
	plan.NullCodec:          {"NullDecodeValue", "primitive.Null"},
	plan.RegexCodec:         {"RegexDecodeValue", "primitive.Regex"},
	plan.DBPointerCodec:     {"DBPointerDecodeValue", "primitive.DBPointer"},
	plan.TimestampCodec:     {"TimestampDecodeValue", "primitive.Timestamp"},
	plan.MinKeyCodec:        {"MinKeyDecodeValue", "primitive.MinKey"},
	plan.MaxKeyCodec:        {"MaxKeyDecodeValue", "primitive.MaxKey"},
	plan.CodeWithScopeCodec: {"CodeWithScopeDecodeValue", "primitive.CodeWithScope"},
}

// decodeTypeCodecInto is a type codec's DecodeValue: the settable check,
// then the decoded value set whole.
func (decoder *Decoder) decodeTypeCodecInto(context decodeContext, valueReader *reader, codec plan.Codec, target reflect.Value) error {
	names, isTypeCodec := typeCodecNames[codec]
	if !isTypeCodec {
		return noDecoderError{goType: target.Type()}
	}
	if !target.CanSet() {
		return valueDecoderError{name: names[0], typeNames: []string{names[1]}, received: target}
	}
	// The frequent ones are set in place: boxing them in an interface{} for
	// reflect.Value.Set would allocate once per value.
	switch codec {
	case plan.DateTimeCodec:
		milliseconds, err := readDateTime(valueReader)
		if err != nil {
			return err
		}
		target.SetInt(int64(milliseconds))
		return nil
	case plan.ObjectIDCodec:
		id, err := readObjectID(valueReader)
		if err != nil {
			return err
		}
		*target.Addr().Interface().(*primitive.ObjectID) = id
		return nil
	case plan.TimeCodec:
		decodedTime, err := readTime(valueReader)
		if err != nil {
			return err
		}
		*target.Addr().Interface().(*time.Time) = decodedTime
		return nil
	}
	decoded, err := decoder.decodeTypeCodec(context, valueReader, codec)
	if err != nil {
		return err
	}
	target.Set(decoded)
	return nil
}

// decodeTypeCodec reads a type codec's value as a new reflect.Value of the
// codec's type: the vendor's typeDecoder.decodeType.
func (decoder *Decoder) decodeTypeCodec(context decodeContext, valueReader *reader, codec plan.Codec) (reflect.Value, error) {
	var decoded any
	var err error
	switch codec {
	case plan.ByteSliceCodec:
		decoded, err = readByteSlice(valueReader)
	case plan.TimeCodec:
		decoded, err = readTime(valueReader)
	case plan.ObjectIDCodec:
		decoded, err = readObjectID(valueReader)
	case plan.Decimal128Codec:
		decoded, err = readDecimal128(valueReader)
	case plan.JSONNumberCodec:
		decoded, err = readJSONNumber(valueReader)
	case plan.URLCodec:
		decoded, err = readURL(valueReader)
	case plan.JavaScriptCodec:
		decoded, err = readJavaScript(valueReader)
	case plan.SymbolCodec:
		decoded, err = readSymbol(valueReader)
	case plan.BinaryCodec:
		decoded, err = readBinaryValue(valueReader)
	case plan.UndefinedCodec:
		decoded, err = primitive.Undefined{}, readUndefined(valueReader)
	case plan.DateTimeCodec:
		decoded, err = readDateTime(valueReader)
	case plan.NullCodec:
		decoded, err = primitive.Null{}, readNull(valueReader)
	case plan.RegexCodec:
		decoded, err = readRegex(valueReader)
	case plan.DBPointerCodec:
		decoded, err = readDBPointer(valueReader)
	case plan.TimestampCodec:
		decoded, err = readTimestamp(valueReader)
	case plan.MinKeyCodec:
		decoded, err = primitive.MinKey{}, readMinKey(valueReader)
	case plan.MaxKeyCodec:
		decoded, err = primitive.MaxKey{}, readMaxKey(valueReader)
	case plan.CodeWithScopeCodec:
		decoded, err = decoder.readCodeWithScope(context, valueReader)
	}
	if err != nil {
		return reflect.Value{}, err
	}
	return reflect.ValueOf(decoded), nil
}

// The codecs the vendor implements as type decoders as well as value
// decoders. A slice, map or interface{} element of one of these types is
// decoded as a new value and converted, rather than into a new variable.
func isTypeDecoder(codec plan.Codec) bool {
	switch codec {
	case plan.BooleanCodec, plan.IntCodec, plan.UintCodec, plan.FloatCodec, plan.StringCodec, plan.EmptyInterfaceCodec:
		return true
	}
	_, isTypeCodec := typeCodecNames[codec]
	return isTypeCodec
}

// decodeElement decodes a container's element of elementPlan's type: the
// vendor's decodeTypeOrValueWithInfo. convert says whether a type
// decoder's result is converted to the element type.
func (decoder *Decoder) decodeElement(context decodeContext, valueReader *reader, elementPlan *plan.Type, convert bool) (reflect.Value, error) {
	codec := elementPlan.Decode.Codec
	if elementPlan.Decode.AddressedOnly || !isTypeDecoder(codec) {
		element := reflect.New(elementPlan.GoType).Elem()
		err := decoder.decodeValue(context, valueReader, elementPlan, element)
		return element, err
	}
	decoded, err := decoder.decodeTyped(context, valueReader, elementPlan)
	if err == nil && convert && decoded.Type() != elementPlan.GoType {
		decoded = decoded.Convert(elementPlan.GoType)
	}
	return decoded, err
}

// decodeTyped is a type decoder's decodeType: a new value of the kind's
// base type, or of the type codec's type.
func (decoder *Decoder) decodeTyped(context decodeContext, valueReader *reader, elementPlan *plan.Type) (reflect.Value, error) {
	kind := elementPlan.GoType.Kind()
	switch elementPlan.Decode.Codec {
	case plan.BooleanCodec:
		flag, err := readBoolean(valueReader)
		return reflect.ValueOf(flag), err
	case plan.IntCodec:
		number, err := readInt(context, valueReader, kind)
		if err != nil {
			return reflect.Value{}, err
		}
		return intValueOf(number, kind), nil
	case plan.UintCodec:
		number, err := readUint(context, valueReader, kind)
		if err != nil {
			return reflect.Value{}, err
		}
		return uintValueOf(number, kind), nil
	case plan.FloatCodec:
		number, err := readFloat(context, valueReader, kind)
		if err != nil {
			return reflect.Value{}, err
		}
		if kind == reflect.Float32 {
			return reflect.ValueOf(float32(number)), nil
		}
		return reflect.ValueOf(number), nil
	case plan.StringCodec:
		text, err := readStringValue(valueReader)
		return reflect.ValueOf(text), err
	case plan.EmptyInterfaceCodec:
		return decoder.decodeInterface(context, valueReader)
	}
	return decoder.decodeTypeCodec(context, valueReader, elementPlan.Decode.Codec)
}

func intValueOf(number int64, kind reflect.Kind) reflect.Value {
	switch kind {
	case reflect.Int8:
		return reflect.ValueOf(int8(number))
	case reflect.Int16:
		return reflect.ValueOf(int16(number))
	case reflect.Int32:
		return reflect.ValueOf(int32(number))
	case reflect.Int:
		return reflect.ValueOf(int(number))
	}
	return reflect.ValueOf(number)
}

func uintValueOf(number uint64, kind reflect.Kind) reflect.Value {
	switch kind {
	case reflect.Uint8:
		return reflect.ValueOf(uint8(number))
	case reflect.Uint16:
		return reflect.ValueOf(uint16(number))
	case reflect.Uint32:
		return reflect.ValueOf(uint32(number))
	case reflect.Uint:
		return reflect.ValueOf(uint(number))
	}
	return reflect.ValueOf(number)
}
