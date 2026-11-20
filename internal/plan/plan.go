// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

// Package plan compiles, once per Go type, every decision the reflection
// encoder and decoder make that depends only on the type: the codec the
// vendor's default registry would pick (v1.17.10), the hook interfaces on the
// value and the pointer receiver, the Zeroer check, the map key rules, and a
// struct's fields with their keys, flags, inline expansion and dominance.
// It encodes and decodes nothing itself.
//
// Plans are published through a sync.Map keyed by reflect.Type and are
// immutable once published (but for the encoder's atomic size hint), so a
// lookup takes no lock and a hit allocates
// nothing (spec 5.3). Two goroutines may compile the same type at once; the
// first to publish wins and the other's plan is discarded. A recursive type
// compiles through its own plan, created empty and filled before anything is
// published.
package plan

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
)

// Codec names the codec the vendor's default registry picks for a type.
// The encoders and decoders of steps 4d and 4e implement one behaviour per
// codec.
type Codec uint8

// The codecs of the vendor's default registry. A kind codec serves every type
// of its reflect.Kind; a type codec serves exactly one type; a hook serves a
// type that implements the hook's interface.
const (
	// NoCodec: no codec is registered, so encoding or decoding a value of the
	// type is an error. A field of an interface type other than interface{}
	// has NoCodec and is dispatched on its dynamic type.
	NoCodec Codec = iota

	BooleanCodec
	IntCodec
	UintCodec
	FloatCodec
	StringCodec
	ArrayCodec
	SliceCodec
	MapCodec
	StructCodec
	PointerCodec

	EmptyInterfaceCodec
	ByteSliceCodec
	TimeCodec
	CoreArrayCodec
	CoreDocumentCodec
	ObjectIDCodec
	Decimal128Codec
	JSONNumberCodec
	URLCodec
	JavaScriptCodec
	SymbolCodec
	BinaryCodec
	UndefinedCodec
	DateTimeCodec
	NullCodec
	RegexCodec
	DBPointerCodec
	TimestampCodec
	MinKeyCodec
	MaxKeyCodec
	CodeWithScopeCodec
	RawCodec
	RawValueCodec
	// DCodec is primitive.D's decode-only type codec; primitive.D encodes
	// through SliceCodec.
	DCodec

	ValueMarshalerHook
	MarshalerHook
	ProxyHook
	ValueUnmarshalerHook
	UnmarshalerHook
)

// Choice is the codec for one direction of one type.
type Choice struct {
	Codec Codec
	// AddressedOnly is set when Codec is a hook that only the pointer type
	// implements (the vendor's condAddrEncoder and condAddrDecoder): the
	// hook is called through the value's address when the value is
	// addressable, and Otherwise is used when it is not.
	AddressedOnly bool
	Otherwise     Codec
}

// KeyCodec names the vendor's rule for a map key type.
type KeyCodec uint8

// The map key rules. On encode the order of checks is string kind, the
// MarshalKey hook, the MarshalText hook, then integer kinds. On decode it is
// the UnmarshalKey hook on the pointer, the UnmarshalText hook on the
// pointer, then string and integer kinds.
const (
	// UnsupportedKey: an error ("unsupported key type: ...") for every entry.
	UnsupportedKey KeyCodec = iota
	StringKey
	IntKey
	UintKey
	// KeyHookKey: MarshalKey on encode (a nil pointer key writes ""),
	// UnmarshalKey through a new pointer on decode.
	KeyHookKey
	// TextHookKey: MarshalText on encode (a nil pointer key writes ""),
	// UnmarshalText through a new pointer on decode.
	TextHookKey
	// DynamicKey (encode only): an interface key type. The MarshalKey and
	// MarshalText hooks of the key's dynamic value apply, and any other key
	// is unsupported, a string included.
	DynamicKey
)

// Type is the plan of one Go type. It is immutable once published, except
// for SizeHint.
type Type struct {
	GoType reflect.Type
	Encode Choice
	Decode Choice
	// Zeroer: GoType implements IsZero() bool. omitempty asks it for any
	// value except a nil pointer. For a pointer type this includes the
	// element's value methods, so a non-nil pointer to a zero time.Time is
	// empty.
	Zeroer bool

	// Elem is the element plan of a pointer, slice, array or map type.
	Elem *Type
	// KeyEncode and KeyDecode are a map type's key rules.
	KeyEncode KeyCodec
	KeyDecode KeyCodec
	// ElemIsByte: a slice or array whose element type is exactly byte. The
	// slice and array codecs write Binary for it; the slice decoder accepts
	// Binary and String.
	ElemIsByte bool
	// ElemIsE: a slice or array whose element type is exactly primitive.E.
	// The array codec writes a document for it, and the slice decoder
	// accepts a document.
	ElemIsE bool
	// ConvertibleToD: a slice type convertible to primitive.D, which the
	// slice codec writes as a document.
	ConvertibleToD bool

	// Struct is a struct type's layout, nil when Err is set.
	Struct *Struct
	// Err is the vendor's refusal of a struct type's layout (a duplicated
	// key, a bad inline field). The vendor returns it when the struct codec
	// reaches a value of the type, on encode and on decode, and not before:
	// a struct that refers to the type without inlining it compiles.
	Err error

	// SizeHint is the one mutable field: the encoded size of the last
	// top-level value of this type, which the encoder uses to size its
	// buffer once (spec 5.5).
	SizeHint atomic.Int32
}

// Struct is a struct type's layout, as the vendor's describeStruct builds it.
type Struct struct {
	// Fields in encode order: by index path, an inlined struct's fields at
	// the inline field's position, after the dominance rule removed the
	// duplicates.
	Fields []Field
	// InlineMap is the plan of the inline map field, nil when the struct has
	// none. InlineMapIndex is that field's index in the struct. Its keys are
	// written after the fields, and a decoded key no field has goes into it.
	InlineMap      *Type
	InlineMapIndex int

	fieldsByKey map[string]int
}

// Field is one encoded field of a struct layout.
type Field struct {
	Key string
	// Index is the field's index path from the outer struct: one element for
	// a direct field, more for a field of an inlined struct.
	Index []int
	// ThroughPointer: the inline path crosses a struct pointer. The encoder
	// skips the field when a pointer on the path is nil, with or without
	// omitempty; the decoder allocates the nil pointers on the path.
	ThroughPointer bool
	OmitEmpty      bool
	MinSize        bool
	Truncate       bool
	Type           *Type
}

// FieldByKey finds the field a decoded key goes to: the exact key first,
// then the key lower-cased (row N4). A key no field has returns false, and
// the decoder skips it or hands it to the inline map (row N5).
func (layout *Struct) FieldByKey(key string) (*Field, bool) {
	if position, found := layout.fieldsByKey[key]; found {
		return &layout.Fields[position], true
	}
	if position, found := layout.fieldsByKey[strings.ToLower(key)]; found {
		return &layout.Fields[position], true
	}
	return nil, false
}

// FieldByKeyBytes is FieldByKey for a key still in the document's bytes.
// The exact lookup allocates nothing; the lower-cased one allocates as
// FieldByKey's does.
func (layout *Struct) FieldByKeyBytes(key []byte) (*Field, bool) {
	if position, found := layout.fieldsByKey[string(key)]; found {
		return &layout.Fields[position], true
	}
	if position, found := layout.fieldsByKey[strings.ToLower(string(key))]; found {
		return &layout.Fields[position], true
	}
	return nil, false
}

// BSONTypes names the two codec types package bson declares. This package
// cannot import them, because package bson imports it.
type BSONTypes struct {
	Raw      reflect.Type
	RawValue reflect.Type
}

// ErrNilType is Lookup's refusal of a nil reflect.Type, which the vendor's
// registry refuses too.
var ErrNilType = errors.New("plan: no plan for a nil type")

// Cache holds the published plans. The zero Cache is not usable; NewCache
// builds one.
type Cache struct {
	plans sync.Map // reflect.Type to *Type

	encodeTypes map[reflect.Type]Codec
	decodeTypes map[reflect.Type]Codec
}

// NewCache returns an empty cache whose type codecs include bsonTypes.
func NewCache(bsonTypes BSONTypes) *Cache {
	encodeTypes, decodeTypes := typeCodecs(bsonTypes)
	return &Cache{encodeTypes: encodeTypes, decodeTypes: decodeTypes}
}

// Lookup returns the plan of goType, compiling and publishing it on first
// use. A hit takes no lock and allocates nothing.
func (cache *Cache) Lookup(goType reflect.Type) (*Type, error) {
	if goType == nil {
		return nil, ErrNilType
	}
	if published, found := cache.plans.Load(goType); found {
		return published.(*Type), nil
	}
	return cache.compile(goType), nil
}
