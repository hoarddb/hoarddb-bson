// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: derived from bson/bsoncodec/default_value_encoders.go (the kind and type encoders, the three hook encoders and isImplementationNil), with bson/bsoncodec/uint_codec.go, time_codec.go, string_codec.go, byte_slice_codec.go, pointer_codec.go, empty_interface_codec.go, cond_addr_codec.go and bson/primitive_codecs.go; rewritten as one switch over the plan's codec, writing bytes directly, with the vendor's integer widths, error text and write order; in step 5's Go-value half, each write also goes to the Extended JSON writer in text mode, where a regex's zero byte is not refused and a hook's bytes are read by a fresh value reader.

package encode

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"time"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/internal/plan"
	"github.com/hoarddb/hoarddb-bson/internal/wire"
)

var (
	valueMarshalerType = reflect.TypeOf((*plan.ValueMarshaler)(nil)).Elem()
	marshalerType      = reflect.TypeOf((*plan.Marshaler)(nil)).Elem()
	proxyType          = reflect.TypeOf((*plan.Proxy)(nil)).Elem()
)

// errNullByteRegex is the vendor's refusal of a regex that cannot be two
// cstrings.
var errNullByteRegex = errors.New("BSON regex values cannot contain null bytes")

// encodeValue writes val, whose plan is typePlan, at slot. minSize is the
// vendor's EncodeContext.MinSize: set by a minsize field tag, it holds for
// everything encoded inside that field.
func (encoding *writer) encodeValue(typePlan *plan.Type, val reflect.Value, at slot, minSize bool) error {
	choice := typePlan.Encode
	if choice.AddressedOnly {
		if val.CanAddr() {
			return encoding.encodeHook(choice.Codec, val.Addr(), at, minSize)
		}
		// The vendor's condAddrEncoder: a value that is not addressable takes
		// the codec the type itself has, and none is an error.
		if choice.Otherwise == plan.NoCodec {
			return noEncoderError{goType: val.Type()}
		}
		return encoding.encodeWith(choice.Otherwise, typePlan, val, at, minSize)
	}
	return encoding.encodeWith(choice.Codec, typePlan, val, at, minSize)
}

func (encoding *writer) encodeWith(codec plan.Codec, typePlan *plan.Type, val reflect.Value, at slot, minSize bool) error {
	switch codec {
	case plan.ValueMarshalerHook, plan.MarshalerHook, plan.ProxyHook:
		return encoding.encodeHook(codec, val, at, minSize)
	case plan.StructCodec:
		return encoding.encodeStruct(typePlan, val, at, minSize)
	case plan.MapCodec:
		return encoding.encodeMap(typePlan, val, at, minSize)
	case plan.SliceCodec:
		return encoding.encodeSlice(typePlan, val, at, minSize)
	case plan.ArrayCodec:
		return encoding.encodeArray(typePlan, val, at, minSize)
	case plan.PointerCodec:
		return encoding.encodePointer(typePlan, val, at, minSize)
	case plan.EmptyInterfaceCodec:
		if val.IsNil() {
			return encoding.writeEmpty(at, bsontype.Null, "WriteNull")
		}
		return encoding.encodeDynamic(val.Elem(), at, minSize)
	case plan.NoCodec:
		return noEncoderError{goType: val.Type()}
	}
	return encoding.encodeScalar(codec, val, at, minSize)
}

// encodeDynamic writes the value an interface holds, through its dynamic
// type's plan.
func (encoding *writer) encodeDynamic(dynamic reflect.Value, at slot, minSize bool) error {
	dynamicPlan, err := encoding.cache.Lookup(dynamic.Type())
	if err != nil {
		return err
	}
	return encoding.encodeValue(dynamicPlan, dynamic, at, minSize)
}

// encodePointer is the vendor's PointerCodec: null for nil, else the
// element through its own plan. An element type with no codec, a non-empty
// interface included, is an error: the pointer codec does not look at the
// dynamic type.
func (encoding *writer) encodePointer(typePlan *plan.Type, val reflect.Value, at slot, minSize bool) error {
	if val.IsNil() {
		return encoding.writeEmpty(at, bsontype.Null, "WriteNull")
	}
	elemPlan := typePlan.Elem
	if elemPlan.Encode.Codec == plan.NoCodec {
		// The vendor names the element type the first time a pointer type
		// fails and the pointer type after that (PointerCodec.ecache); the
		// first is what a fresh process says.
		return noEncoderError{goType: elemPlan.GoType}
	}
	return encoding.encodeValue(elemPlan, val.Elem(), at, minSize)
}

// encodeScalar writes the kind codecs and type codecs that are one value.
func (encoding *writer) encodeScalar(codec plan.Codec, val reflect.Value, at slot, minSize bool) error {
	switch codec {
	case plan.BooleanCodec:
		return encoding.writeBoolean(at, val.Bool())
	case plan.IntCodec:
		return encoding.writeInt(at, val.Kind(), val.Int(), minSize)
	case plan.UintCodec:
		return encoding.writeUint(at, val.Kind(), val.Uint(), minSize)
	case plan.FloatCodec:
		return encoding.writeDouble(at, val.Float())
	case plan.StringCodec:
		return encoding.writeString(at, bsontype.String, "WriteString", val.String())
	case plan.JavaScriptCodec:
		return encoding.writeString(at, bsontype.JavaScript, "WriteJavascript", val.String())
	case plan.SymbolCodec:
		return encoding.writeString(at, bsontype.Symbol, "WriteSymbol", val.String())
	case plan.ByteSliceCodec:
		if val.IsNil() {
			return encoding.writeEmpty(at, bsontype.Null, "WriteNull")
		}
		return encoding.writeBinary(at, 0x00, val.Bytes())
	case plan.TimeCodec:
		instant := valueAs[time.Time](val)
		return encoding.writeDateTime(at, int64(primitive.NewDateTimeFromTime(instant)))
	case plan.DateTimeCodec:
		return encoding.writeDateTime(at, val.Int())
	case plan.ObjectIDCodec:
		if err := encoding.scalarHeader(at, bsontype.ObjectID, "WriteObjectID"); err != nil {
			return err
		}
		if encoding.textMode {
			encoding.text.WriteObjectID(objectIDOf(val))
			return nil
		}
		encoding.appendObjectID(val)
		return nil
	case plan.JSONNumberCodec:
		return encoding.writeJSONNumber(at, json.Number(val.String()), minSize)
	case plan.URLCodec:
		location := valueAs[url.URL](val)
		return encoding.writeString(at, bsontype.String, "WriteString", location.String())
	case plan.Decimal128Codec:
		return encoding.writeDecimal128(at, valueAs[primitive.Decimal128](val))
	case plan.BinaryCodec:
		binary := valueAs[primitive.Binary](val)
		return encoding.writeBinary(at, binary.Subtype, binary.Data)
	case plan.RegexCodec:
		regex := valueAs[primitive.Regex](val)
		return encoding.writeRegex(at, regex.Pattern, regex.Options)
	case plan.DBPointerCodec:
		return encoding.writeDBPointer(at, val)
	case plan.TimestampCodec:
		timestamp := valueAs[primitive.Timestamp](val)
		return encoding.writeTimestamp(at, timestamp.T, timestamp.I)
	case plan.NullCodec:
		return encoding.writeEmpty(at, bsontype.Null, "WriteNull")
	case plan.UndefinedCodec:
		return encoding.writeEmpty(at, bsontype.Undefined, "WriteUndefined")
	case plan.MinKeyCodec:
		return encoding.writeEmpty(at, bsontype.MinKey, "WriteMinKey")
	case plan.MaxKeyCodec:
		return encoding.writeEmpty(at, bsontype.MaxKey, "WriteMaxKey")
	case plan.CodeWithScopeCodec:
		return encoding.writeCodeWithScope(at, val, minSize)
	case plan.CoreDocumentCodec, plan.RawCodec:
		return encoding.copyDocument(at, val.Bytes())
	case plan.CoreArrayCodec:
		return encoding.copyArray(at, val.Bytes())
	case plan.RawValueCodec:
		return encoding.writeRawValue(at, bsontype.Type(val.Field(0).Uint()), val.Field(1).Bytes())
	}
	// Every Codec the plan names is a case above or in encodeWith; this is
	// the decode-only DCodec and the decode hooks, which no Encode choice
	// holds.
	return noEncoderError{goType: val.Type()}
}

// valueAs reads a value of a type codec's own type. An addressable value is
// read through its address, which boxes a pointer instead of copying the
// value onto the heap.
func valueAs[T any](val reflect.Value) T {
	if val.CanAddr() {
		return *val.Addr().Interface().(*T)
	}
	return val.Interface().(T)
}

func (encoding *writer) writeBoolean(at slot, flag bool) error {
	if err := encoding.scalarHeader(at, bsontype.Boolean, "WriteBoolean"); err != nil {
		return err
	}
	if encoding.textMode {
		encoding.text.WriteBoolean(flag)
		return nil
	}
	if flag {
		encoding.buf = append(encoding.buf, 0x01)
	} else {
		encoding.buf = append(encoding.buf, 0x00)
	}
	return nil
}

func fitsInt32(number int64) bool {
	return number >= math.MinInt32 && number <= math.MaxInt32
}

// writeInt is the vendor's IntEncodeValue: the narrow kinds are int32, int
// is int32 when it fits, and int64 is int32 only under minsize.
func (encoding *writer) writeInt(at slot, kind reflect.Kind, number int64, minSize bool) error {
	switch kind {
	case reflect.Int8, reflect.Int16, reflect.Int32:
		return encoding.writeInt32(at, int32(number))
	case reflect.Int:
		if fitsInt32(number) {
			return encoding.writeInt32(at, int32(number))
		}
	default:
		if minSize && fitsInt32(number) {
			return encoding.writeInt32(at, int32(number))
		}
	}
	return encoding.writeInt64(at, number)
}

// writeUint is the vendor's UIntCodec: uint8 and uint16 are int32, the wider
// kinds int64 (int32 under minsize when it fits), and above MaxInt64 an
// error, before anything is written.
func (encoding *writer) writeUint(at slot, kind reflect.Kind, number uint64, minSize bool) error {
	if kind == reflect.Uint8 || kind == reflect.Uint16 {
		return encoding.writeInt32(at, int32(number))
	}
	if minSize && number <= math.MaxInt32 {
		return encoding.writeInt32(at, int32(number))
	}
	if number > math.MaxInt64 {
		return fmt.Errorf("%d overflows int64", number)
	}
	return encoding.writeInt64(at, int64(number))
}

func (encoding *writer) writeInt32(at slot, number int32) error {
	if err := encoding.scalarHeader(at, bsontype.Int32, "WriteInt32"); err != nil {
		return err
	}
	if encoding.textMode {
		encoding.text.WriteInt32(number)
		return nil
	}
	encoding.buf = wire.AppendInt32(encoding.buf, number)
	return nil
}

func (encoding *writer) writeInt64(at slot, number int64) error {
	if err := encoding.scalarHeader(at, bsontype.Int64, "WriteInt64"); err != nil {
		return err
	}
	if encoding.textMode {
		encoding.text.WriteInt64(number)
		return nil
	}
	encoding.buf = wire.AppendInt64(encoding.buf, number)
	return nil
}

func (encoding *writer) writeDouble(at slot, number float64) error {
	if err := encoding.scalarHeader(at, bsontype.Double, "WriteDouble"); err != nil {
		return err
	}
	if encoding.textMode {
		encoding.text.WriteDouble(number)
		return nil
	}
	encoding.buf = wire.AppendDouble(encoding.buf, number)
	return nil
}

// writeString writes the three string-shaped types; method is the vendor's
// writer call for the type, for the text of the top-level refusal.
func (encoding *writer) writeString(at slot, stringType bsontype.Type, method, text string) error {
	if err := encoding.scalarHeader(at, stringType, method); err != nil {
		return err
	}
	if encoding.textMode {
		encoding.writeTextString(stringType, text)
		return nil
	}
	encoding.buf = wire.AppendString(encoding.buf, text)
	return nil
}

// writeBinary names WriteBinaryWithSubtype in its refusal, because the
// vendor's WriteBinary delegates to it.
func (encoding *writer) writeBinary(at slot, subtype byte, payload []byte) error {
	if err := encoding.scalarHeader(at, bsontype.Binary, "WriteBinaryWithSubtype"); err != nil {
		return err
	}
	if encoding.textMode {
		encoding.text.WriteBinary(subtype, payload)
		return nil
	}
	encoding.buf = wire.AppendBinary(encoding.buf, subtype, payload)
	return nil
}

func (encoding *writer) writeDateTime(at slot, milliseconds int64) error {
	if err := encoding.scalarHeader(at, bsontype.DateTime, "WriteDateTime"); err != nil {
		return err
	}
	if encoding.textMode {
		encoding.text.WriteDateTime(milliseconds)
		return nil
	}
	encoding.buf = wire.AppendInt64(encoding.buf, milliseconds)
	return nil
}

// appendObjectID copies the 12 bytes element by element, which reads a
// value that is not addressable without boxing it.
func (encoding *writer) appendObjectID(val reflect.Value) {
	for position := 0; position < val.Len(); position++ {
		encoding.buf = append(encoding.buf, byte(val.Index(position).Uint()))
	}
}

// writeJSONNumber is the vendor's JSONNumberEncodeValue: an integer as the
// int64 kind, else a double, else strconv's error.
func (encoding *writer) writeJSONNumber(at slot, number json.Number, minSize bool) error {
	if integer, err := number.Int64(); err == nil {
		return encoding.writeInt(at, reflect.Int64, integer, minSize)
	}
	float, err := number.Float64()
	if err != nil {
		return err
	}
	return encoding.writeDouble(at, float)
}

func (encoding *writer) writeDecimal128(at slot, decimal primitive.Decimal128) error {
	if err := encoding.scalarHeader(at, bsontype.Decimal128, "WriteDecimal128"); err != nil {
		return err
	}
	if encoding.textMode {
		encoding.text.WriteDecimal128(decimal)
		return nil
	}
	high, low := decimal.GetBytes()
	encoding.buf = wire.AppendInt64(encoding.buf, int64(low))
	encoding.buf = wire.AppendInt64(encoding.buf, int64(high))
	return nil
}

// writeRegex refuses a zero byte before it looks at the slot, as the vendor's
// BSON writer does (its Extended JSON writer does not), and writes the
// options sorted.
func (encoding *writer) writeRegex(at slot, pattern, options string) error {
	if !encoding.textMode && (containsZero(pattern) || containsZero(options)) {
		return errNullByteRegex
	}
	if err := encoding.scalarHeader(at, bsontype.Regex, "WriteRegex"); err != nil {
		return err
	}
	if encoding.textMode {
		encoding.text.WriteRegex(pattern, options)
		return nil
	}
	encoding.buf = append(encoding.buf, pattern...)
	encoding.buf = append(encoding.buf, 0)
	encoding.buf = appendSortedOptions(encoding.buf, options)
	encoding.buf = append(encoding.buf, 0)
	return nil
}

func containsZero(text string) bool {
	for position := 0; position < len(text); position++ {
		if text[position] == 0 {
			return true
		}
	}
	return false
}

// appendSortedOptions appends the regex options in ascending rune order,
// the vendor's sortStringAlphebeticAscending.
func appendSortedOptions(dst []byte, options string) []byte {
	letters := []rune(options)
	for unsorted := 1; unsorted < len(letters); unsorted++ {
		for position := unsorted; position > 0 && letters[position] < letters[position-1]; position-- {
			letters[position], letters[position-1] = letters[position-1], letters[position]
		}
	}
	return append(dst, string(letters)...)
}

func (encoding *writer) writeDBPointer(at slot, val reflect.Value) error {
	if err := encoding.scalarHeader(at, bsontype.DBPointer, "WriteDBPointer"); err != nil {
		return err
	}
	if encoding.textMode {
		encoding.text.WriteDBPointer(val.Field(0).String(), objectIDOf(val.Field(1)))
		return nil
	}
	encoding.buf = wire.AppendString(encoding.buf, val.Field(0).String())
	encoding.appendObjectID(val.Field(1))
	return nil
}

// writeTimestamp writes the increment first: it is the low half.
func (encoding *writer) writeTimestamp(at slot, seconds, increment uint32) error {
	if err := encoding.scalarHeader(at, bsontype.Timestamp, "WriteTimestamp"); err != nil {
		return err
	}
	if encoding.textMode {
		encoding.text.WriteTimestamp(seconds, increment)
		return nil
	}
	encoding.buf = wire.AppendInt32(encoding.buf, int32(increment))
	encoding.buf = wire.AppendInt32(encoding.buf, int32(seconds))
	return nil
}

// writeCodeWithScope is the vendor's CodeWithScopeEncodeValue. The scope is
// encoded as if at the top level, because the vendor encodes it into a
// fresh value writer: a nil scope has no encoder, and a scope that is not a
// document is refused.
func (encoding *writer) writeCodeWithScope(at slot, val reflect.Value, minSize bool) error {
	if err := encoding.header(at, bsontype.CodeWithScope, "WriteCodeWithScope", "CodeWithScope"); err != nil {
		return err
	}
	if encoding.textMode {
		return encoding.writeTextCodeWithScope(val, minSize)
	}
	start := encoding.reserveLength()
	encoding.buf = wire.AppendString(encoding.buf, val.Field(0).String())
	scope := val.Field(1)
	if scope.IsNil() {
		return noEncoderError{}
	}
	if err := encoding.encodeDynamic(scope.Elem(), topLevel(), minSize); err != nil {
		return err
	}
	return encoding.putLength(start)
}

// writeRawValue is the vendor's RawValueEncodeValue: the type is checked,
// the bytes are not.
func (encoding *writer) writeRawValue(at slot, valueType bsontype.Type, valueBytes []byte) error {
	if !valueType.IsValid() {
		return fmt.Errorf("the RawValue Type specifies an invalid BSON type: %#x", byte(valueType))
	}
	return encoding.writeValueBytes(at, valueType, valueBytes)
}

// writeValueBytes is the vendor's WriteValueBytes: a header and the bytes
// as given, unchecked.
func (encoding *writer) writeValueBytes(at slot, valueType bsontype.Type, valueBytes []byte) error {
	if encoding.textMode {
		return encoding.copyTextValue(at, valueType, valueBytes)
	}
	if err := encoding.scalarHeader(at, valueType, "WriteValueBytes"); err != nil {
		return err
	}
	encoding.buf = append(encoding.buf, valueBytes...)
	return nil
}

// encodeHook calls one of the three encode hooks on val, which implements
// it (when the plan chose the hook only for the pointer, val is the address
// already). The codec, not the receiver's method set, selects the call: a
// hook interface may hold a value that has more than one hook.
func (encoding *writer) encodeHook(codec plan.Codec, val reflect.Value, at slot, minSize bool) error {
	switch codec {
	case plan.ValueMarshalerHook:
		receiver, err := hookReceiver(val, valueMarshalerType)
		if err != nil || receiver == nil {
			return encoding.nullOr(err, at)
		}
		valueType, valueBytes, err := receiver.(plan.ValueMarshaler).MarshalBSONValue()
		if err != nil {
			return err
		}
		return encoding.writeValueBytes(at, valueType, valueBytes)
	case plan.MarshalerHook:
		receiver, err := hookReceiver(val, marshalerType)
		if err != nil || receiver == nil {
			return encoding.nullOr(err, at)
		}
		documentBytes, err := receiver.(plan.Marshaler).MarshalBSON()
		if err != nil {
			return err
		}
		return encoding.writeValueBytes(at, bsontype.EmbeddedDocument, documentBytes)
	}
	receiver, err := hookReceiver(val, proxyType)
	if err != nil || receiver == nil {
		return encoding.nullOr(err, at)
	}
	proxied, err := receiver.(plan.Proxy).ProxyBSON()
	if err != nil {
		return err
	}
	return encoding.encodeProxied(proxied, at, minSize)
}

// nullOr returns err, or writes the null the vendor writes for a hook with
// no receiver.
func (encoding *writer) nullOr(err error, at slot) error {
	if err != nil {
		return err
	}
	return encoding.writeEmpty(at, bsontype.Null, "WriteNull")
}

// hookReceiver returns the value whose hook method is called, nil when the
// vendor writes null instead (a nil pointer to a type that has the method
// on its value, or a nil interface), and an error for a nil pointer held in
// an interface whose method is on the value, on which the vendor's call
// panics.
func hookReceiver(val reflect.Value, hookType reflect.Type) (any, error) {
	if val.Kind() == reflect.Pointer && val.IsNil() && implementedBeneathPointers(val.Type(), hookType) {
		return nil, nil
	}
	receiver := val.Interface()
	if val.Kind() == reflect.Interface && receiver != nil {
		if err := refuseNilValueReceiver(reflect.ValueOf(receiver), hookType); err != nil {
			return nil, err
		}
	}
	return receiver, nil
}

// refuseNilValueReceiver is the error for a nil pointer whose type has the
// hook only through its element's value methods: Go cannot call such a
// method, and the vendor panics where this returns.
func refuseNilValueReceiver(receiver reflect.Value, hookType reflect.Type) error {
	if receiver.Kind() == reflect.Pointer && receiver.IsNil() && implementedBeneathPointers(receiver.Type(), hookType) {
		return fmt.Errorf("cannot call %s on a nil %s: the method has a value receiver", hookType.Method(0).Name, receiver.Type())
	}
	return nil
}

// implementedBeneathPointers is the type half of the vendor's
// isImplementationNil: the type with every pointer level removed has the
// hook.
func implementedBeneathPointers(goType, hookType reflect.Type) bool {
	for goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}
	return goType.Implements(hookType)
}

// encodeProxied writes what ProxyBSON returned. A nil has no encoder, and a
// pointer is followed one level.
func (encoding *writer) encodeProxied(proxied any, at slot, minSize bool) error {
	if proxied == nil {
		return noEncoderError{}
	}
	proxiedValue := reflect.ValueOf(proxied)
	if proxiedValue.Kind() == reflect.Pointer {
		if proxiedValue.IsNil() {
			// The vendor takes the type of the invalid element and panics.
			return fmt.Errorf("ProxyBSON returned a nil %s", proxiedValue.Type())
		}
		proxiedValue = proxiedValue.Elem()
	}
	return encoding.encodeDynamic(proxiedValue, at, minSize)
}
