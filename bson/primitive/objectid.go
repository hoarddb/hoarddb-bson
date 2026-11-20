// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package primitive

import (
	"encoding"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync/atomic"
	"time"
)

// ErrInvalidHex indicates that a hex string cannot be converted to an ObjectID.
var ErrInvalidHex = errors.New("the provided hex string is not a valid ObjectID")

// ObjectID is the BSON ObjectID type: 4 bytes of seconds since the Unix
// epoch, 5 bytes unique to the process and 3 bytes of a counter, all big
// endian where they hold a number (bsonspec.org).
type ObjectID [12]byte

// NilObjectID is the zero value for ObjectID.
var NilObjectID ObjectID

// The process-unique bytes and the counter's start come from the runtime's
// OS-seeded generator and not from crypto/rand: that generator has no error
// path, so package load cannot fail, and an ObjectID needs uniqueness, not
// secrecy.
var (
	objectIDCounter = rand.Uint32()
	processUnique   = processUniqueBytes()
)

var (
	_ encoding.TextMarshaler   = ObjectID{}
	_ encoding.TextUnmarshaler = &ObjectID{}
	_ json.Marshaler           = ObjectID{}
	_ json.Unmarshaler         = &ObjectID{}
)

func processUniqueBytes() [5]byte {
	var uniqueBytes [5]byte
	binary.BigEndian.PutUint32(uniqueBytes[0:4], rand.Uint32())
	uniqueBytes[4] = byte(rand.Uint32())
	return uniqueBytes
}

// NewObjectID generates a new ObjectID from the current time.
func NewObjectID() ObjectID {
	return NewObjectIDFromTimestamp(time.Now())
}

// NewObjectIDFromTimestamp generates a new ObjectID whose time part is the
// given time, in whole seconds.
func NewObjectIDFromTimestamp(timestamp time.Time) ObjectID {
	var newID ObjectID
	binary.BigEndian.PutUint32(newID[0:4], uint32(timestamp.Unix()))
	copy(newID[4:9], processUnique[:])
	counter := atomic.AddUint32(&objectIDCounter, 1)
	newID[9], newID[10], newID[11] = byte(counter>>16), byte(counter>>8), byte(counter)
	return newID
}

// Timestamp extracts the time part of the ObjectID, in UTC.
func (id ObjectID) Timestamp() time.Time {
	unixSeconds := binary.BigEndian.Uint32(id[0:4])
	return time.Unix(int64(unixSeconds), 0).UTC()
}

// Hex returns the hex encoding of the ObjectID as a string.
func (id ObjectID) Hex() string {
	var hexBytes [24]byte
	hex.Encode(hexBytes[:], id[:])
	return string(hexBytes[:])
}

func (id ObjectID) String() string {
	return fmt.Sprintf("ObjectID(%q)", id.Hex())
}

// IsZero returns true if id is the empty ObjectID.
func (id ObjectID) IsZero() bool {
	return id == NilObjectID
}

// ObjectIDFromHex creates an ObjectID from a hex string. It returns an error
// if the string is not 24 hex digits.
func ObjectIDFromHex(hexText string) (ObjectID, error) {
	if len(hexText) != 24 {
		return NilObjectID, ErrInvalidHex
	}
	var parsed ObjectID
	if _, err := hex.Decode(parsed[:], []byte(hexText)); err != nil {
		return NilObjectID, err
	}
	return parsed, nil
}

// IsValidObjectID returns true if the string is 24 hex digits.
//
// Deprecated: Use ObjectIDFromHex and check the error instead.
func IsValidObjectID(hexText string) bool {
	_, err := ObjectIDFromHex(hexText)
	return err == nil
}

// MarshalText returns the ObjectID as UTF-8 hex text, which lets an ObjectID
// be a map key when marshalling JSON.
func (id ObjectID) MarshalText() ([]byte, error) {
	return []byte(id.Hex()), nil
}

// UnmarshalText sets the ObjectID from hex text.
func (id *ObjectID) UnmarshalText(hexText []byte) error {
	parsed, err := ObjectIDFromHex(string(hexText))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// MarshalJSON returns the ObjectID as a JSON string of 24 hex digits.
func (id ObjectID) MarshalJSON() ([]byte, error) {
	return json.Marshal(id.Hex())
}

var errNotExtendedJSONObjectID = errors.New("not an extended JSON ObjectID")

// UnmarshalJSON sets the ObjectID from JSON. A 12-byte input is taken as the
// raw ObjectID bytes. Anything else must be a JSON string of 24 hex digits or
// an Extended JSON {"$oid": "..."} document. An empty string gives the nil
// ObjectID and JSON null leaves the receiver unchanged. A malformed hex
// string may leave the leading bytes it decoded in the receiver.
func (id *ObjectID) UnmarshalJSON(jsonBytes []byte) error {
	if string(jsonBytes) == "null" {
		return nil
	}
	if len(jsonBytes) == 12 {
		copy(id[:], jsonBytes)
		return nil
	}

	var decoded any
	if err := json.Unmarshal(jsonBytes, &decoded); err != nil {
		return err
	}
	hexText, isString := decoded.(string)
	if !isString {
		wrapper, isObject := decoded.(map[string]any)
		if !isObject {
			return errNotExtendedJSONObjectID
		}
		hexText, isString = wrapper["$oid"].(string)
		if !isString {
			return errNotExtendedJSONObjectID
		}
	}

	if len(hexText) == 0 {
		*id = NilObjectID
		return nil
	}
	if len(hexText) != 24 {
		return fmt.Errorf("cannot unmarshal into an ObjectID, the length must be 24 but it is %d", len(hexText))
	}
	_, err := hex.Decode(id[:], []byte(hexText))
	return err
}
