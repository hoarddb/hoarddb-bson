// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: derived from bson/bsoncodec/struct_codec.go (DecodeValue, getInlineField), map_codec.go (DecodeValue, decodeKey), slice_codec.go, pointer_codec.go, empty_interface_codec.go, array_codec.go, default_value_decoders.go (DDecodeValue, ArrayDecodeValue, decodeD, decodeDefault, decodeElemsFromDocumentReader, readCodeWithScope, the two hook decoders, CoreDocumentDecodeValue) and bson/primitive_codecs.go (RawDecodeValue): the decode order, the ancestor rule, what a non-empty target keeps and every error text kept; a struct's layout and its fields' codecs come from the step 4c plan; an inlined value struct reached through a nil pointer is an error where the vendor panics (coordinator's ruling, 2026-10-24); a pointer to a type with no decoder gives the vendor's first-call error on every call; in step 5's Go-value half, bson.Raw and the bsoncore types read their bytes through the reader's document or array copy, which differ for Extended JSON, and a null into a map passed by value is an error where the vendor panics.

package decode

import (
	"encoding"
	"errors"
	"fmt"
	"reflect"
	"strconv"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/internal/plan"
)

var (
	elementType          = reflect.TypeOf(primitive.E{})
	byteType             = reflect.TypeOf(byte(0))
	valueUnmarshalerType = reflect.TypeOf((*plan.ValueUnmarshaler)(nil)).Elem()
	unmarshalerType      = reflect.TypeOf((*plan.Unmarshaler)(nil)).Elem()
)

func isDocumentType(valueType bsontype.Type) bool {
	return valueType == bsontype.Type(0) || valueType == bsontype.EmbeddedDocument
}

// decodeInterface is what an interface{} receives: the type map's Go type
// for the value, an embedded document as the ancestor's type when there is
// one (rows D1 to D7), and nil for a null.
func (decoder *Decoder) decodeInterface(context decodeContext, valueReader *reader) (reflect.Value, error) {
	valueType := valueReader.Type()
	heldPlan := decoder.typeMap[valueType]
	if isDocumentType(valueType) && context.ancestor != nil {
		heldPlan = context.ancestor
	}
	if heldPlan == nil {
		if valueType == bsontype.Null {
			return reflect.Zero(emptyInterfaceType), valueReader.ReadNull()
		}
		return reflect.Value{}, noTypeMapEntryError{valueType: valueType}
	}
	return decoder.decodeElement(context, valueReader, heldPlan, true)
}

// decodeD is primitive.D's decoder. Its elements decode with primitive.D
// as the ancestor, and an error inside one is returned without its key.
func (decoder *Decoder) decodeD(context decodeContext, valueReader *reader, target reflect.Value) error {
	if !target.IsValid() || !target.CanSet() {
		return valueDecoderError{name: "DDecodeValue", kinds: []reflect.Kind{reflect.Slice}, received: target}
	}
	switch valueType := valueReader.Type(); {
	case isDocumentType(valueType):
		context.ancestor = decoder.documentPlan
	case valueType == bsontype.Null:
		target.Set(reflect.Zero(target.Type()))
		return valueReader.ReadNull()
	default:
		return fmt.Errorf("cannot decode %v into a primitive.D", valueType)
	}
	if err := valueReader.ReadDocument(); err != nil {
		return err
	}
	var elements primitive.D
	if !target.IsNil() {
		// The prefilled slice's array is written over: the vendor appends
		// into it from length 0.
		target.SetLen(0)
		elements = target.Interface().(primitive.D)
	} else {
		elements = make(primitive.D, 0)
	}
	for {
		key, err := valueReader.ReadElement()
		if errors.Is(err, errEndOfDocument) {
			break
		}
		if err != nil {
			return err
		}
		held, err := decoder.decodeElement(context, valueReader, decoder.interfacePlan, false)
		if err != nil {
			return err
		}
		elements = append(elements, primitive.E{Key: key, Value: held.Interface()})
	}
	target.Set(reflect.ValueOf(elements))
	return nil
}

// decodeDocumentElements reads the elements of the document the reader is
// inside as interface{} values, wrapping an error with its key.
func (decoder *Decoder) decodeDocumentElements(context decodeContext, valueReader *reader) ([]primitive.E, error) {
	elements := make([]primitive.E, 0)
	for {
		key, err := valueReader.ReadElement()
		if errors.Is(err, errEndOfDocument) {
			return elements, nil
		}
		if err != nil {
			return nil, err
		}
		held, err := decoder.decodeInterface(context, valueReader)
		if err != nil {
			return nil, newDecodeError(key, err)
		}
		elements = append(elements, primitive.E{Key: key, Value: held.Interface()})
	}
}

// readDocumentAsElements is a slice or array of primitive.E's document read.
func (decoder *Decoder) readDocumentAsElements(context decodeContext, valueReader *reader) ([]primitive.E, error) {
	if valueType := valueReader.Type(); !isDocumentType(valueType) {
		return nil, fmt.Errorf("cannot decode %v into a D", valueType)
	}
	if err := valueReader.ReadDocument(); err != nil {
		return nil, err
	}
	return decoder.decodeDocumentElements(context, valueReader)
}

// readCodeWithScope reads a code with scope. Its scope's elements decode
// with the context it was reached with: the vendor sets no ancestor here.
func (decoder *Decoder) readCodeWithScope(context decodeContext, valueReader *reader) (primitive.CodeWithScope, error) {
	if valueType := valueReader.Type(); valueType != bsontype.CodeWithScope {
		return primitive.CodeWithScope{}, readNullOrUndefined(valueReader, valueType, "a primitive.CodeWithScope")
	}
	code, err := valueReader.ReadCodeWithScope()
	if err != nil {
		return primitive.CodeWithScope{}, err
	}
	elements, err := decoder.decodeDocumentElements(context, valueReader)
	if err != nil {
		return primitive.CodeWithScope{}, err
	}
	scope := make(primitive.D, 0, len(elements))
	return primitive.CodeWithScope{Code: primitive.JavaScript(code), Scope: append(scope, elements...)}, nil
}

// decodeArrayValues reads an array's values into a new slice of valuesType,
// whose elements are the sequence plan's element type, wrapping an error
// with its position. Each value is decoded in place in a zero element, which
// gives what the vendor's new value per element gives, without boxing it.
func (decoder *Decoder) decodeArrayValues(context decodeContext, valueReader *reader, sequencePlan *plan.Type, valuesType reflect.Type) (reflect.Value, error) {
	if err := valueReader.ReadArray(); err != nil {
		return reflect.Value{}, err
	}
	elementPlan := sequencePlan.Elem
	if elementPlan.Decode.Codec == plan.NoCodec {
		return reflect.Value{}, noDecoderError{goType: elementPlan.GoType}
	}
	values := reflect.Zero(valuesType)
	for count := 0; ; count++ {
		err := valueReader.ReadValue()
		if errors.Is(err, errEndOfArray) {
			return values.Slice(0, count), nil
		}
		if err != nil {
			return reflect.Value{}, err
		}
		if count == values.Len() {
			grown := reflect.MakeSlice(valuesType, max(4, 2*count), max(4, 2*count))
			reflect.Copy(grown, values)
			values = grown
		}
		if err := decoder.decodeValue(context, valueReader, elementPlan, values.Index(count)); err != nil {
			return reflect.Value{}, newDecodeError(strconv.Itoa(count), err)
		}
	}
}

// decodeSlice is the slice codec: an array, null or undefined, a document
// for a slice of primitive.E (which becomes the ancestor), a binary or a
// string for a slice of bytes. A prefilled slice is reset to length 0 and
// appended to (row R1).
func (decoder *Decoder) decodeSlice(context decodeContext, valueReader *reader, slicePlan *plan.Type, target reflect.Value) error {
	if !target.CanSet() || target.Kind() != reflect.Slice {
		return valueDecoderError{name: "SliceDecodeValue", kinds: []reflect.Kind{reflect.Slice}, received: target}
	}
	elementIsE := slicePlan.GoType.Elem() == elementType
	switch valueType := valueReader.Type(); {
	case valueType == bsontype.Array:
	case valueType == bsontype.Null:
		target.Set(reflect.Zero(target.Type()))
		return valueReader.ReadNull()
	case valueType == bsontype.Undefined:
		target.Set(reflect.Zero(target.Type()))
		return valueReader.ReadUndefined()
	case isDocumentType(valueType):
		if !elementIsE {
			return fmt.Errorf("cannot decode document into %s", target.Type())
		}
	case valueType == bsontype.Binary:
		if slicePlan.GoType.Elem() != byteType {
			return fmt.Errorf("SliceDecodeValue can only decode a binary into a byte array, got %v", valueType)
		}
		payload, subtype, err := valueReader.ReadBinary()
		if err != nil {
			return err
		}
		if subtype != bsontype.BinaryGeneric && subtype != bsontype.BinaryBinaryOld {
			return fmt.Errorf("SliceDecodeValue can only be used to decode subtype 0x00 or 0x02 for %s, got %v", bsontype.Binary, subtype)
		}
		setBytesAppended(target, payload)
		return nil
	case valueType == bsontype.String:
		if elementGoType := slicePlan.GoType.Elem(); elementGoType != byteType {
			return fmt.Errorf("SliceDecodeValue can only decode a string into a byte array, got %v", elementGoType)
		}
		text, err := valueReader.ReadString()
		if err != nil {
			return err
		}
		setBytesAppended(target, []byte(text))
		return nil
	default:
		return fmt.Errorf("cannot decode %v into a slice", valueType)
	}
	if elementIsE {
		context.ancestor = slicePlan
		elements, err := decoder.readDocumentAsElements(context, valueReader)
		if err != nil {
			return err
		}
		if target.IsNil() {
			target.Set(reflect.MakeSlice(target.Type(), 0, len(elements)))
		}
		target.SetLen(0)
		target.Set(reflect.AppendSlice(target, reflect.ValueOf(elements)))
		return nil
	}
	values, err := decoder.decodeArrayValues(context, valueReader, slicePlan, target.Type())
	if err != nil {
		return err
	}
	if target.IsNil() {
		// The new slice's own array, cut to its length, is the fresh array
		// of exactly that capacity the vendor makes; an empty array still
		// gives a non-nil empty slice.
		target.Set(values.Slice3(0, values.Len(), values.Len()))
		if values.Len() == 0 {
			target.Set(reflect.MakeSlice(target.Type(), 0, 0))
		}
		return nil
	}
	target.SetLen(0)
	target.Set(reflect.AppendSlice(target, values))
	return nil
}

func setBytesAppended(target reflect.Value, payload []byte) {
	if target.IsNil() {
		target.Set(reflect.MakeSlice(target.Type(), 0, len(payload)))
	}
	target.SetLen(0)
	target.Set(reflect.AppendSlice(target, reflect.ValueOf(payload)))
}

// decodeArray is the array decoder. Elements past the decoded ones keep
// their values.
func (decoder *Decoder) decodeArray(context decodeContext, valueReader *reader, arrayPlan *plan.Type, target reflect.Value) error {
	if !target.IsValid() || target.Kind() != reflect.Array {
		return valueDecoderError{name: "ArrayDecodeValue", kinds: []reflect.Kind{reflect.Array}, received: target}
	}
	elementGoType := arrayPlan.GoType.Elem()
	switch valueType := valueReader.Type(); {
	case valueType == bsontype.Array:
	case isDocumentType(valueType):
		if elementGoType != elementType {
			return fmt.Errorf("cannot decode document into %s", target.Type())
		}
	case valueType == bsontype.Binary:
		if elementGoType != byteType {
			return fmt.Errorf("ArrayDecodeValue can only be used to decode binary into a byte array, got %v", valueType)
		}
		payload, subtype, err := valueReader.ReadBinary()
		if err != nil {
			return err
		}
		if subtype != bsontype.BinaryGeneric && subtype != bsontype.BinaryBinaryOld {
			return fmt.Errorf("ArrayDecodeValue can only be used to decode subtype 0x00 or 0x02 for %s, got %v", bsontype.Binary, subtype)
		}
		if len(payload) > target.Len() {
			return fmt.Errorf("more elements returned in array than can fit inside %s", target.Type())
		}
		for position, payloadByte := range payload {
			target.Index(position).Set(reflect.ValueOf(payloadByte))
		}
		return nil
	case valueType == bsontype.Null:
		target.Set(reflect.Zero(target.Type()))
		return valueReader.ReadNull()
	case valueType == bsontype.Undefined:
		target.Set(reflect.Zero(target.Type()))
		return valueReader.ReadUndefined()
	default:
		return fmt.Errorf("cannot decode %v into an array", valueType)
	}
	var values reflect.Value
	if elementGoType == elementType {
		documentElements, err := decoder.readDocumentAsElements(context, valueReader)
		if err != nil {
			return err
		}
		values = reflect.ValueOf(documentElements)
	} else {
		var err error
		if values, err = decoder.decodeArrayValues(context, valueReader, arrayPlan, reflect.SliceOf(elementGoType)); err != nil {
			return err
		}
	}
	if values.Len() > target.Len() {
		return fmt.Errorf("more elements returned in array than can fit inside %s, got %v elements", target.Type(), values.Len())
	}
	reflect.Copy(target, values)
	return nil
}

// decodeMap is the map codec. A prefilled map is extended, not cleared (row
// R1); a map of interface{} is the ancestor of its values (row D2).
func (decoder *Decoder) decodeMap(context decodeContext, valueReader *reader, mapPlan *plan.Type, target reflect.Value) error {
	if target.Kind() != reflect.Map || (!target.CanSet() && target.IsNil()) {
		return valueDecoderError{name: "MapDecodeValue", kinds: []reflect.Kind{reflect.Map}, received: target}
	}
	switch valueType := valueReader.Type(); {
	case isDocumentType(valueType):
	case valueType == bsontype.Null:
		if err := refuseNilMapByValue(valueType, target); err != nil {
			return err
		}
		target.Set(reflect.Zero(target.Type()))
		return valueReader.ReadNull()
	case valueType == bsontype.Undefined:
		if err := refuseNilMapByValue(valueType, target); err != nil {
			return err
		}
		target.Set(reflect.Zero(target.Type()))
		return valueReader.ReadUndefined()
	default:
		return fmt.Errorf("cannot decode %v into a %s", valueType, target.Type())
	}
	if err := valueReader.ReadDocument(); err != nil {
		return err
	}
	if target.IsNil() {
		target.Set(reflect.MakeMap(target.Type()))
	}
	elementPlan := mapPlan.Elem
	if elementPlan.Decode.Codec == plan.NoCodec {
		return noDecoderError{goType: elementPlan.GoType}
	}
	if elementPlan.GoType == emptyInterfaceType {
		context.ancestor = mapPlan
	}
	for {
		key, err := valueReader.ReadElement()
		if errors.Is(err, errEndOfDocument) {
			return nil
		}
		if err != nil {
			return err
		}
		mapKey, err := decodeKey(key, mapPlan)
		if err != nil {
			return err
		}
		element, err := decoder.decodeElement(context, valueReader, elementPlan, true)
		if err != nil {
			return newDecodeError(key, err)
		}
		target.SetMapIndex(mapKey, element)
	}
}

// decodeKey is the map codec's key rule, from the plan: the UnmarshalKey
// and UnmarshalText hooks before the string kind, integers parsed and
// range-checked, any other key type an error for every key.
// refuseNilMapByValue refuses a null or undefined decoded into a map that
// cannot be set: one passed by value, which only a text root can meet
// (bson.UnmarshalExtJSON of "null" into a map). The vendor sets the map to
// nil anyway and panics.
func refuseNilMapByValue(valueType bsontype.Type, target reflect.Value) error {
	if target.CanSet() {
		return nil
	}
	return fmt.Errorf("cannot decode %v into a %s passed by value: only a map behind a pointer can be set to nil; pass a pointer to the map", valueType, target.Type())
}

func decodeKey(key string, mapPlan *plan.Type) (reflect.Value, error) {
	keyType := mapPlan.GoType.Key()
	switch mapPlan.KeyDecode {
	case plan.KeyHookKey:
		keyPointer := reflect.New(keyType)
		err := keyPointer.Interface().(plan.KeyUnmarshaler).UnmarshalKey(key)
		return keyPointer.Elem(), err
	case plan.TextHookKey:
		keyPointer := reflect.New(keyType)
		err := keyPointer.Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(key))
		return keyPointer.Elem(), err
	case plan.StringKey:
		return reflect.ValueOf(key).Convert(keyType), nil
	case plan.IntKey:
		number, parseErr := strconv.ParseInt(key, 10, 64)
		if parseErr != nil || reflect.Zero(keyType).OverflowInt(number) {
			return reflect.Value{}, fmt.Errorf("failed to unmarshal number key %v", key)
		}
		return reflect.ValueOf(number).Convert(keyType), nil
	case plan.UintKey:
		number, parseErr := strconv.ParseUint(key, 10, 64)
		if parseErr != nil || reflect.Zero(keyType).OverflowUint(number) {
			return reflect.Value{}, fmt.Errorf("failed to unmarshal number key %v", key)
		}
		return reflect.ValueOf(number).Convert(keyType), nil
	}
	return reflect.Value{}, fmt.Errorf("unsupported key type: %v", keyType)
}

// decodePointer is the pointer codec: null and undefined set nil, anything
// else is decoded through the pointer, which is allocated when nil (row
// P1).
func (decoder *Decoder) decodePointer(context decodeContext, valueReader *reader, pointerPlan *plan.Type, target reflect.Value) error {
	if !target.CanSet() || target.Kind() != reflect.Pointer {
		return valueDecoderError{name: "PointerCodec.DecodeValue", kinds: []reflect.Kind{reflect.Pointer}, received: target}
	}
	switch valueReader.Type() {
	case bsontype.Null:
		target.Set(reflect.Zero(target.Type()))
		return valueReader.ReadNull()
	case bsontype.Undefined:
		target.Set(reflect.Zero(target.Type()))
		return valueReader.ReadUndefined()
	}
	if target.IsNil() {
		target.Set(reflect.New(target.Type().Elem()))
	}
	if pointerPlan.Elem.Decode.Codec == plan.NoCodec {
		// The vendor's first decode of such a pointer in a process gives
		// this error; every later one panics on its cached nil decoder.
		return noDecoderError{goType: pointerPlan.Elem.GoType}
	}
	return decoder.decodeValue(context, valueReader, pointerPlan.Elem, target.Elem())
}

// decodeStruct is the struct codec. Each field decodes with a fresh context,
// so the ancestor is reset (row D4); an unknown key is skipped unread (row
// N5) or goes to the inline map; fields the document does not mention keep
// their values (row R1).
func (decoder *Decoder) decodeStruct(context decodeContext, valueReader *reader, structPlan *plan.Type, target reflect.Value) error {
	if !target.CanSet() || target.Kind() != reflect.Struct {
		return valueDecoderError{name: "StructCodec.DecodeValue", kinds: []reflect.Kind{reflect.Struct}, received: target}
	}
	switch valueType := valueReader.Type(); {
	case isDocumentType(valueType):
	case valueType == bsontype.Null:
		if err := valueReader.ReadNull(); err != nil {
			return err
		}
		target.Set(reflect.Zero(target.Type()))
		return nil
	case valueType == bsontype.Undefined:
		if err := valueReader.ReadUndefined(); err != nil {
			return err
		}
		target.Set(reflect.Zero(target.Type()))
		return nil
	default:
		return fmt.Errorf("cannot decode %v into a %s", valueType, target.Type())
	}
	if structPlan.Err != nil {
		return structPlan.Err
	}
	layout := structPlan.Struct
	var inlineMap reflect.Value
	var inlineElementPlan *plan.Type
	if layout.InlineMap != nil {
		inlineMap = target.Field(layout.InlineMapIndex)
		inlineElementPlan = layout.InlineMap.Elem
		if inlineElementPlan.Decode.Codec == plan.NoCodec {
			return noDecoderError{goType: inlineElementPlan.GoType}
		}
	}
	if err := valueReader.ReadDocument(); err != nil {
		return err
	}
	// revisited holds the element offsets read after a skip moved the read
	// backwards; reading one again means the vendor would loop for ever.
	var revisited map[int64]bool
	for {
		elementStart := valueReader.offset
		if revisited != nil {
			if revisited[elementStart] {
				return fmt.Errorf("document is invalid: a negative value length returns the read to byte %d again, so the document never ends", elementStart)
			}
			revisited[elementStart] = true
		}
		keyBytes, err := valueReader.ReadElementKey()
		if errors.Is(err, errEndOfDocument) {
			return nil
		}
		if err != nil {
			return err
		}
		field, found := layout.FieldByKeyBytes(keyBytes)
		if !found {
			if layout.InlineMap == nil {
				movedBack, err := valueReader.Skip()
				if err != nil {
					return err
				}
				if movedBack && revisited == nil {
					revisited = map[int64]bool{elementStart: true}
				}
				continue
			}
			if inlineMap.IsNil() {
				inlineMap.Set(reflect.MakeMap(inlineMap.Type()))
			}
			element := reflect.New(inlineElementPlan.GoType).Elem()
			context.ancestor = layout.InlineMap
			if err := decoder.decodeValue(context, valueReader, inlineElementPlan, element); err != nil {
				return err
			}
			inlineMap.SetMapIndex(reflect.ValueOf(string(keyBytes)), element)
			continue
		}
		if err := decoder.decodeField(context, valueReader, target, field, keyBytes); err != nil {
			return err
		}
	}
}

func (decoder *Decoder) decodeField(context decodeContext, valueReader *reader, target reflect.Value, field *plan.Field, keyBytes []byte) error {
	fieldValue := target.Field(field.Index[0])
	if len(field.Index) > 1 {
		var err error
		if fieldValue, err = inlineField(target, field.Index, keyBytes); err != nil {
			return err
		}
	}
	if fieldValue.Kind() == reflect.Pointer && fieldValue.IsNil() {
		fieldValue.Set(reflect.New(fieldValue.Type().Elem()))
	}
	if field.Type.Decode.Codec == plan.NoCodec {
		return newDecodeError(field.Key, noDecoderError{goType: fieldValue.Type()})
	}
	fieldContext := decodeContext{truncate: field.Truncate || context.truncate}
	if err := decoder.decodeValue(fieldContext, valueReader, field.Type, fieldValue); err != nil {
		return newDecodeError(field.Key, err)
	}
	return nil
}

// fieldByIndex is reflect.Value.FieldByIndex, reporting a nil struct
// pointer on the path instead of panicking.
func fieldByIndex(structValue reflect.Value, index []int) (reflect.Value, bool) {
	fieldValue := structValue
	for depth, position := range index {
		if depth > 0 && fieldValue.Kind() == reflect.Pointer && fieldValue.Type().Elem().Kind() == reflect.Struct {
			if fieldValue.IsNil() {
				return reflect.Value{}, false
			}
			fieldValue = fieldValue.Elem()
		}
		fieldValue = fieldValue.Field(position)
	}
	return fieldValue, true
}

// inlineField is the vendor's getInlineField: the field at index, after
// allocating the nil struct pointer above it. The vendor allocates the
// parent of the index unconditionally once it has allocated one further up,
// and when that parent is a struct value it panics; that case is an error
// (coordinator's ruling, 2026-10-24).
func inlineField(structValue reflect.Value, index []int, keyBytes []byte) (reflect.Value, error) {
	if fieldValue, reached := fieldByIndex(structValue, index); reached {
		return fieldValue, nil
	}
	parentIndex := index[:len(index)-1]
	parent, reached := fieldByIndex(structValue, parentIndex)
	if !reached {
		var err error
		if parent, err = inlineField(structValue, parentIndex, keyBytes); err != nil {
			return parent, err
		}
		if parent.Kind() != reflect.Pointer {
			return reflect.Value{}, fmt.Errorf("decoding key %q: the inlined struct %s in %s is reached through a nil pointer and cannot be allocated; allocate the pointer before decoding", keyBytes, parent.Type(), structValue.Type())
		}
	}
	parent.Set(reflect.New(parent.Type().Elem()))
	fieldValue, _ := fieldByIndex(structValue, index)
	return fieldValue, nil
}

// decodeValueBytes is the decoder of bson.Raw, bsoncore.Document and
// bsoncore.Array: the value's bytes appended to the target from length 0,
// whatever the value's type. On an error the target holds what the read
// returned, as the vendor's does.
func decodeValueBytes(valueReader *reader, codec plan.Codec, target reflect.Value) error {
	if !target.CanSet() {
		name := map[plan.Codec]string{plan.RawCodec: "RawDecodeValue", plan.CoreDocumentCodec: "CoreDocumentDecodeValue", plan.CoreArrayCodec: "CoreArrayDecodeValue"}[codec]
		return valueDecoderError{name: name, typeNames: []string{target.Type().String()}, received: target}
	}
	if target.IsNil() {
		target.Set(reflect.MakeSlice(target.Type(), 0, 0))
	}
	target.SetLen(0)
	readBytes := valueReader.ReadDocumentBytes
	if codec == plan.CoreArrayCodec {
		readBytes = valueReader.ReadArrayBytes
	}
	valueBytes, err := readBytes(target.Bytes())
	target.SetBytes(valueBytes)
	return err
}

// decodeValueHook calls UnmarshalBSONValue with the value's type and a copy
// of its bytes. A null sets a pointer target to nil instead.
func decodeValueHook(valueReader *reader, target reflect.Value) error {
	refusal := valueDecoderError{name: "ValueUnmarshalerDecodeValue", typeNames: []string{"bsoncodec.ValueUnmarshaler"}, received: target}
	if valueReader.Type() == bsontype.Null && target.Kind() == reflect.Pointer {
		target.Set(reflect.Zero(target.Type()))
		return valueReader.ReadNull()
	}
	if target.Kind() == reflect.Pointer && target.IsNil() {
		if !target.CanSet() {
			return refusal
		}
		target.Set(reflect.New(target.Type().Elem()))
	}
	if !target.Type().Implements(valueUnmarshalerType) {
		if !target.CanAddr() {
			return refusal
		}
		target = target.Addr()
	}
	valueType, valueBytes, err := valueReader.ReadValueBytes(nil)
	if err != nil {
		return err
	}
	hook, implements := target.Interface().(plan.ValueUnmarshaler)
	if !implements {
		refusal.received = target
		return refusal
	}
	return hook.UnmarshalBSONValue(valueType, valueBytes)
}

// decodeBytesHook calls UnmarshalBSON with a copy of the value's bytes. A
// value with no bytes (null, undefined, MinKey, MaxKey) sets a pointer
// target to nil instead.
func decodeBytesHook(valueReader *reader, target reflect.Value) error {
	refusal := valueDecoderError{name: "UnmarshalerDecodeValue", typeNames: []string{"bsoncodec.Unmarshaler"}, received: target}
	if target.Kind() == reflect.Pointer && target.IsNil() {
		if !target.CanSet() {
			return refusal
		}
		target.Set(reflect.New(target.Type().Elem()))
	}
	_, valueBytes, err := valueReader.ReadValueBytes(nil)
	if err != nil {
		return err
	}
	if target.Kind() == reflect.Pointer && len(valueBytes) == 0 {
		target.Set(reflect.Zero(target.Type()))
		return nil
	}
	if !target.Type().Implements(unmarshalerType) {
		if !target.CanAddr() {
			return refusal
		}
		target = target.Addr()
	}
	hook, implements := target.Interface().(plan.Unmarshaler)
	if !implements {
		refusal.received = target
		return refusal
	}
	return hook.UnmarshalBSON(valueBytes)
}
