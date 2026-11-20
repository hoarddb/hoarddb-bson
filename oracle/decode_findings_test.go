// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorbsontype "go.mongodb.org/mongo-driver/bson/bsontype"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// The vendor behaviours the step 4c plan named and 4e's decoder runs:
// decode hooks, map keys, the inline quirks and non-empty interface fields.

// decRecordedHook records what UnmarshalBSON received, so the decoded value
// shows which hook ran and with which bytes.
type decRecordedHook struct {
	Via   string
	Bytes []byte
}

func (hook *decRecordedHook) UnmarshalBSON(data []byte) error {
	hook.Via, hook.Bytes = "UnmarshalBSON", append([]byte(nil), data...)
	if len(data) == 4 && string(data) == "\x07\x00\x00\x00" {
		return errors.New("refused by the hook")
	}
	return nil
}

// decVendorValueHook and decLibraryValueHook have both decode hooks on the
// pointer: the vendor calls UnmarshalBSONValue first (registration order).
type decVendorValueHook struct {
	Via   string
	Type  byte
	Bytes []byte
}

func (hook *decVendorValueHook) UnmarshalBSONValue(valueType vendorbsontype.Type, data []byte) error {
	hook.Via, hook.Type, hook.Bytes = "UnmarshalBSONValue", byte(valueType), append([]byte(nil), data...)
	if valueType == vendorbson.TypeBoolean {
		return errors.New("refused by the value hook")
	}
	return nil
}

func (hook *decVendorValueHook) UnmarshalBSON(data []byte) error {
	hook.Via = "UnmarshalBSON"
	return nil
}

type decLibraryValueHook struct {
	Via   string
	Type  byte
	Bytes []byte
}

func (hook *decLibraryValueHook) UnmarshalBSONValue(valueType bsontype.Type, data []byte) error {
	hook.Via, hook.Type, hook.Bytes = "UnmarshalBSONValue", byte(valueType), append([]byte(nil), data...)
	if valueType == bsontype.Boolean {
		return errors.New("refused by the value hook")
	}
	return nil
}

func (hook *decLibraryValueHook) UnmarshalBSON(data []byte) error {
	hook.Via = "UnmarshalBSON"
	return nil
}

// decValueReceiverHook has UnmarshalBSON on the value receiver, which can
// set nothing; the vendor still calls it.
type decValueReceiverHook struct{ N int32 }

func (decValueReceiverHook) UnmarshalBSON([]byte) error { return nil }

type decHookFields struct {
	H     decRecordedHook
	P     *decRecordedHook
	L     []decRecordedHook
	M     map[string]decRecordedHook
	R     decValueReceiverHook
	Value decVendorValueHook
	VP    *decVendorValueHook
}

type decLibHookFields struct {
	H     decRecordedHook
	P     *decRecordedHook
	L     []decRecordedHook
	M     map[string]decRecordedHook
	R     decValueReceiverHook
	Value decLibraryValueHook
	VP    *decLibraryValueHook
}

func TestDecodeHooksMatchVendor(t *testing.T) {
	target := decTarget{"*decHookFields", func() any { return &decHookFields{} }, func() any { return &decLibHookFields{} }}
	prefilled := decTarget{"*decHookFields prefilled",
		func() any {
			return &decHookFields{P: &decRecordedHook{Via: "old"}, VP: &decVendorValueHook{Via: "old"}}
		},
		func() any {
			return &decLibHookFields{P: &decRecordedHook{Via: "old"}, VP: &decLibraryValueHook{Via: "old"}}
		}}
	sub := vendorbson.D{{Key: "x", Value: int32(1)}}
	documents := [][]byte{
		decMarshal(t, vendorbson.D{{Key: "h", Value: sub}, {Key: "p", Value: sub}, {Key: "l", Value: vendorbson.A{sub, "s"}},
			{Key: "m", Value: vendorbson.D{{Key: "k", Value: int64(3)}}}, {Key: "r", Value: sub}, {Key: "value", Value: "text"}, {Key: "vp", Value: 2.5}}),
		decMarshal(t, vendorbson.D{{Key: "p", Value: nil}, {Key: "vp", Value: nil}, {Key: "h", Value: nil}, {Key: "value", Value: nil}}),
		documentOf(elementOf(0x06, "p", nil), elementOf(0x7F, "vp", nil), elementOf(0xFF, "p", nil)),
		decMarshal(t, vendorbson.D{{Key: "value", Value: true}}),
		decMarshal(t, vendorbson.D{{Key: "h", Value: int32(7)}}),
		decMarshal(t, vendorbson.D{{Key: "vp", Value: false}}),
	}
	for _, document := range documents {
		decCheck(t, "hooks", document, target, prefilled)
	}
	// A top-level target with UnmarshalBSON gets the whole document's bytes
	// (Decoder.Decode's first branch).
	whole := decSame("*decRecordedHook", decZero[decRecordedHook])
	for _, document := range append(documents, []byte{0xFF, 0, 0, 0, 0}, []byte{3, 0}, documentOf()) {
		decCheck(t, "top-level hook", document, whole)
	}
}

type (
	decIntKeyed      map[int]string
	decUint8Keyed    map[uint8]string
	decFloatKeyed    map[float64]string
	decBoolKeyed     map[bool]string
	decAnyKeyed      map[any]string
	decNamedKey      string
	decNamedKeyed    map[decNamedKey]string
	decTextKey       string
	decTextKeyed     map[decTextKey]string
	decHookKey       struct{ Text string }
	decHookKeyed     map[decHookKey]string
	decUintptrKeyed  map[uintptr]string
	decFailingKey    struct{ Text string }
	decFailingKeyed  map[decFailingKey]string
	decInt8Keyed     map[int8]string
	decTextPtrKey    struct{ Text string }
	decTextPtrKeyed  map[decTextPtrKey]string
	decNamedIntKey   int16
	decNamedIntKeyed map[decNamedIntKey]string
)

func (key *decTextKey) UnmarshalText(text []byte) error {
	*key = decTextKey("text:" + string(text))
	return nil
}

func (key *decHookKey) UnmarshalKey(text string) error {
	key.Text = "key:" + text
	return nil
}

// UnmarshalText is ignored: UnmarshalKey comes first.
func (key *decHookKey) UnmarshalText(text []byte) error {
	key.Text = "text:" + string(text)
	return nil
}

func (key *decFailingKey) UnmarshalKey(text string) error {
	return fmt.Errorf("the key %q is refused", text)
}

func (key *decTextPtrKey) UnmarshalText(text []byte) error {
	key.Text = string(text)
	return nil
}

func TestDecodeMapKeysMatchVendor(t *testing.T) {
	targets := []decTarget{
		decSame("*decIntKeyed", decZero[decIntKeyed]), decSame("*decUint8Keyed", decZero[decUint8Keyed]),
		decSame("*decFloatKeyed", decZero[decFloatKeyed]), decSame("*decBoolKeyed", decZero[decBoolKeyed]),
		decSame("*decAnyKeyed", decZero[decAnyKeyed]), decSame("*decNamedKeyed", decZero[decNamedKeyed]),
		decSame("*decTextKeyed", decZero[decTextKeyed]), decSame("*decHookKeyed", decZero[decHookKeyed]),
		decSame("*decUintptrKeyed", decZero[decUintptrKeyed]), decSame("*decFailingKeyed", decZero[decFailingKeyed]),
		decSame("*decInt8Keyed", decZero[decInt8Keyed]), decSame("*decTextPtrKeyed", decZero[decTextPtrKeyed]),
		decSame("*decNamedIntKeyed", decZero[decNamedIntKeyed]),
		decSame("*struct{M decIntKeyed}", decZero[struct{ M decIntKeyed }]),
	}
	keySets := [][]string{
		{}, {"7"}, {"-7"}, {"0", "1"}, {"128"}, {"256"}, {"-129"}, {"x"}, {"1.5"}, {"true"}, {""}, {"99999999999999999999"},
		{"7", "x"}, {"40000"}, {"0x10"}, {" 7"},
	}
	for _, keys := range keySets {
		document := vendorbson.D{}
		for position, key := range keys {
			document = append(document, vendorbson.E{Key: key, Value: "v" + strconv.Itoa(position)})
		}
		encoded := decMarshal(t, document)
		for _, target := range targets {
			if divergence := decDivergence(encoded, target); divergence != "" {
				t.Errorf("keys %q: %s", keys, divergence)
			}
		}
	}
}

type decInlinedWithMap struct {
	A string
	M map[string]string `bson:",inline"`
}

type decOuterOfInlinedMap struct {
	In decInlinedWithMap `bson:",inline"`
	C  string
}

type decHiddenEmbed struct{ X string }

type decOuterOfHidden struct {
	Before         string
	decHiddenEmbed `bson:",inline"`
	After          string
}

type decPointerInlined struct {
	X string
	Y string
}

type decOuterOfPointer struct {
	Before string
	B      *decPointerInlined `bson:",inline"`
	After  string
}

type decTwoLevelPointers struct {
	P *decOuterOfPointer `bson:",inline"`
	Z string
}

type decDuplicateKeys struct {
	X1 string `bson:"k"`
	X2 string `bson:"k"`
}

type decHoldsDuplicate struct {
	Ok  string
	Bad *decDuplicateKeys
}

func TestDecodeInlineQuirksMatchVendor(t *testing.T) {
	targets := []decTarget{
		decSame("*decOuterOfInlinedMap", decZero[decOuterOfInlinedMap]),
		decSame("*decOuterOfHidden", decZero[decOuterOfHidden]),
		decSame("*decOuterOfPointer", decZero[decOuterOfPointer]),
		decSame("*decOuterOfPointer prefilled", func() decOuterOfPointer { return decOuterOfPointer{B: &decPointerInlined{Y: "kept"}} }),
		decSame("*decTwoLevelPointers", decZero[decTwoLevelPointers]),
		decSame("*decDuplicateKeys", decZero[decDuplicateKeys]),
		decSame("*decHoldsDuplicate", decZero[decHoldsDuplicate]),
		decSame("*struct{Inline map[string]int32 inline}", decZero[struct {
			Inline map[string]int32 `bson:",inline"`
		}]),
	}
	for _, keys := range [][]string{{"a"}, {"m"}, {"zz"}, {"x"}, {"X"}, {"y"}, {"before", "after"}, {"c", "a", "q"}, {"z", "x"}, {"k"}, {"ok", "bad"}} {
		document := vendorbson.D{}
		for _, key := range keys {
			if key == "bad" {
				document = append(document, vendorbson.E{Key: key, Value: vendorbson.D{{Key: "k", Value: "v"}}})
				continue
			}
			document = append(document, vendorbson.E{Key: key, Value: "v-" + key})
		}
		decCheck(t, fmt.Sprintf("inline keys %q", keys), decMarshal(t, document), targets...)
	}
	decCheck(t, "inline map with a wrong value type", decMarshal(t, vendorbson.D{{Key: "free", Value: "text"}}), targets[len(targets)-1])
}

type decReaderField struct{ R io.Reader }

type decStringerField struct {
	S fmt.Stringer
}

type decSliceOfReaders struct{ L []io.Reader }

type decMapOfReaders struct{ M map[string]io.Reader }

type decStringer struct{}

func (decStringer) String() string { return "s" }

func TestDecodeNonEmptyInterfaceIsAnError(t *testing.T) {
	document := decMarshal(t, vendorbson.D{{Key: "r", Value: "x"}, {Key: "s", Value: "y"}, {Key: "l", Value: vendorbson.A{"z"}}, {Key: "m", Value: vendorbson.D{{Key: "k", Value: "v"}}}})
	targets := []decTarget{
		decSame("*decReaderField", decZero[decReaderField]),
		decSame("*decStringerField prefilled", func() decStringerField { return decStringerField{S: decStringer{}} }),
		decSame("*decSliceOfReaders", decZero[decSliceOfReaders]),
		decSame("*decMapOfReaders", decZero[decMapOfReaders]),
		decSame("*map[string]io.Reader", decZero[map[string]io.Reader]),
		decSame("*io.Reader", decZero[io.Reader]),
	}
	for _, target := range targets {
		decMustRefuse(t, "non-empty interface", document, target)
	}
	decCheck(t, "non-empty interface", document, targets...)
	decCheck(t, "non-empty interface, key absent", decMarshal(t, vendorbson.D{{Key: "other", Value: int32(1)}}), targets[:4]...)
}
