// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: derived from bson/bsoncodec/registry.go (the LookupEncoder and LookupDecoder order: type codec, hook on the value or the pointer, kind codec), the registration tables of default_value_encoders.go, default_value_decoders.go and bson/primitive_codecs.go, and map_codec.go's key rules; rewritten to name a codec per type instead of returning one.

package plan

import (
	"encoding"
	"encoding/json"
	"net/url"
	"reflect"
	"time"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/x/bsonx/bsoncore"
)

// The hook interfaces, by method set, as the vendor declares them.

// ValueMarshaler is a type that writes itself as one BSON value.
type ValueMarshaler interface {
	MarshalBSONValue() (bsontype.Type, []byte, error)
}

// Marshaler is a type that writes itself as a BSON document.
type Marshaler interface {
	MarshalBSON() ([]byte, error)
}

// Proxy is a type that is encoded as the value it returns.
type Proxy interface {
	ProxyBSON() (interface{}, error)
}

// ValueUnmarshaler is a type that reads itself from one BSON value.
type ValueUnmarshaler interface {
	UnmarshalBSONValue(bsontype.Type, []byte) error
}

// Unmarshaler is a type that reads itself from a BSON value's bytes.
type Unmarshaler interface {
	UnmarshalBSON([]byte) error
}

// Zeroer is a type that says whether omitempty drops it.
type Zeroer interface {
	IsZero() bool
}

// KeyMarshaler is a map key type that writes its own key.
type KeyMarshaler interface {
	MarshalKey() (key string, err error)
}

// KeyUnmarshaler is a map key type that reads its own key.
type KeyUnmarshaler interface {
	UnmarshalKey(key string) error
}

var (
	zeroerType           = reflect.TypeOf((*Zeroer)(nil)).Elem()
	keyMarshalerType     = reflect.TypeOf((*KeyMarshaler)(nil)).Elem()
	keyUnmarshalerType   = reflect.TypeOf((*KeyUnmarshaler)(nil)).Elem()
	textMarshalerType    = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	textUnmarshalerType  = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
	byteType             = reflect.TypeOf(byte(0))
	stringType           = reflect.TypeOf("")
	elementType          = reflect.TypeOf(primitive.E{})
	documentType         = reflect.TypeOf(primitive.D{})
	emptyInterfaceType   = reflect.TypeOf((*interface{})(nil)).Elem()
	valueMarshalerType   = reflect.TypeOf((*ValueMarshaler)(nil)).Elem()
	marshalerType        = reflect.TypeOf((*Marshaler)(nil)).Elem()
	proxyType            = reflect.TypeOf((*Proxy)(nil)).Elem()
	valueUnmarshalerType = reflect.TypeOf((*ValueUnmarshaler)(nil)).Elem()
	unmarshalerType      = reflect.TypeOf((*Unmarshaler)(nil)).Elem()
)

// hook is one registered hook interface and the codec it selects.
type hook struct {
	iface reflect.Type
	codec Codec
}

// The hooks in the vendor's registration order, which decides which hook a
// type implementing two of them gets.
var (
	encodeHooks = []hook{{valueMarshalerType, ValueMarshalerHook}, {marshalerType, MarshalerHook}, {proxyType, ProxyHook}}
	decodeHooks = []hook{{valueUnmarshalerType, ValueUnmarshalerHook}, {unmarshalerType, UnmarshalerHook}}
)

// typeCodecs returns the type codecs of the vendor's default registry, the
// ones bson.NewRegistry adds included. primitive.D has a decode codec only.
func typeCodecs(bsonTypes BSONTypes) (encodeTypes, decodeTypes map[reflect.Type]Codec) {
	encodeTypes = map[reflect.Type]Codec{
		reflect.TypeOf([]byte(nil)):               ByteSliceCodec,
		reflect.TypeOf(time.Time{}):               TimeCodec,
		emptyInterfaceType:                        EmptyInterfaceCodec,
		reflect.TypeOf(bsoncore.Array(nil)):       CoreArrayCodec,
		reflect.TypeOf(primitive.ObjectID{}):      ObjectIDCodec,
		reflect.TypeOf(primitive.Decimal128{}):    Decimal128Codec,
		reflect.TypeOf(json.Number("")):           JSONNumberCodec,
		reflect.TypeOf(url.URL{}):                 URLCodec,
		reflect.TypeOf(primitive.JavaScript("")):  JavaScriptCodec,
		reflect.TypeOf(primitive.Symbol("")):      SymbolCodec,
		reflect.TypeOf(primitive.Binary{}):        BinaryCodec,
		reflect.TypeOf(primitive.Undefined{}):     UndefinedCodec,
		reflect.TypeOf(primitive.DateTime(0)):     DateTimeCodec,
		reflect.TypeOf(primitive.Null{}):          NullCodec,
		reflect.TypeOf(primitive.Regex{}):         RegexCodec,
		reflect.TypeOf(primitive.DBPointer{}):     DBPointerCodec,
		reflect.TypeOf(primitive.Timestamp{}):     TimestampCodec,
		reflect.TypeOf(primitive.MinKey{}):        MinKeyCodec,
		reflect.TypeOf(primitive.MaxKey{}):        MaxKeyCodec,
		reflect.TypeOf(bsoncore.Document(nil)):    CoreDocumentCodec,
		reflect.TypeOf(primitive.CodeWithScope{}): CodeWithScopeCodec,
		bsonTypes.Raw:      RawCodec,
		bsonTypes.RawValue: RawValueCodec,
	}
	decodeTypes = make(map[reflect.Type]Codec, len(encodeTypes)+1)
	for goType, codec := range encodeTypes {
		decodeTypes[goType] = codec
	}
	decodeTypes[documentType] = DCodec
	return encodeTypes, decodeTypes
}

// kindCodec is the vendor's kind codec for kind, NoCodec when it registers
// none (uintptr, complex, chan, func, interface, unsafe.Pointer).
func kindCodec(kind reflect.Kind) Codec {
	switch kind {
	case reflect.Bool:
		return BooleanCodec
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return IntCodec
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return UintCodec
	case reflect.Float32, reflect.Float64:
		return FloatCodec
	case reflect.Array:
		return ArrayCodec
	case reflect.Map:
		return MapCodec
	case reflect.Slice:
		return SliceCodec
	case reflect.String:
		return StringCodec
	case reflect.Struct:
		return StructCodec
	case reflect.Pointer:
		return PointerCodec
	}
	return NoCodec
}

// choose is the vendor's LookupEncoder or LookupDecoder: the type codec,
// else the first hook in registration order that the type or its pointer
// implements, else the kind codec.
func choose(goType reflect.Type, types map[reflect.Type]Codec, hooks []hook) Choice {
	if codec, found := types[goType]; found {
		return Choice{Codec: codec}
	}
	for _, candidate := range hooks {
		if goType.Implements(candidate.iface) {
			return Choice{Codec: candidate.codec}
		}
		if goType.Kind() != reflect.Pointer && reflect.PointerTo(goType).Implements(candidate.iface) {
			// The vendor's fallback for a value that is not addressable is
			// the first hook the type itself implements, from the start of
			// the list, else the kind codec; type codecs are not consulted.
			otherwise := kindCodec(goType.Kind())
			for _, valueHook := range hooks {
				if goType.Implements(valueHook.iface) {
					otherwise = valueHook.codec
					break
				}
			}
			return Choice{Codec: candidate.codec, AddressedOnly: true, Otherwise: otherwise}
		}
	}
	return Choice{Codec: kindCodec(goType.Kind())}
}

func isIntKind(kind reflect.Kind) bool {
	return kind >= reflect.Int && kind <= reflect.Int64
}

func isUintKind(kind reflect.Kind) bool {
	return kind >= reflect.Uint && kind <= reflect.Uintptr
}

// keyEncodeCodec is map_codec.go's encodeKey order for a key type.
func keyEncodeCodec(keyType reflect.Type) KeyCodec {
	switch {
	case keyType.Kind() == reflect.String:
		return StringKey
	case keyType.Kind() == reflect.Interface:
		return DynamicKey
	case keyType.Implements(keyMarshalerType):
		return KeyHookKey
	case keyType.Implements(textMarshalerType):
		return TextHookKey
	case isIntKind(keyType.Kind()):
		return IntKey
	case isUintKind(keyType.Kind()):
		return UintKey
	}
	return UnsupportedKey
}

// keyDecodeCodec is map_codec.go's decodeKey order for a key type. A float
// key is unsupported, because the vendor parses one only when keys are
// encoded with fmt, which the default codec does not do.
func keyDecodeCodec(keyType reflect.Type) KeyCodec {
	pointerType := reflect.PointerTo(keyType)
	switch {
	case pointerType.Implements(keyUnmarshalerType):
		return KeyHookKey
	case pointerType.Implements(textUnmarshalerType):
		return TextHookKey
	case keyType.Kind() == reflect.String:
		return StringKey
	case isIntKind(keyType.Kind()):
		return IntKey
	case isUintKind(keyType.Kind()):
		return UintKey
	}
	return UnsupportedKey
}
