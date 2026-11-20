// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

// Package fixture is the module the inventory tests run over. Every call in
// it is placed so that the tests can name the enclosing function and the
// expected reference counts. It is never built into anything.
package fixture

import (
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Account is a struct target with no map inside.
type Account struct {
	ID   primitive.ObjectID `bson:"_id"`
	Name string
}

// Settings is a struct target with a map field.
type Settings struct {
	Labels map[string]string
	Owner  Account
}

func encodeAccount(account Account) ([]byte, error) { return bson.Marshal(account) }

func encodeOrdered(ordered bson.D) ([]byte, error) { return bson.Marshal(ordered) }

func encodePlainMap(plain map[string]interface{}) ([]byte, error) { return bson.Marshal(plain) }

func encodeBSONM() ([]byte, error) { return bson.Marshal(bson.M{"a": 1}) }

func encodeAnything(anything interface{}) ([]byte, error) { return bson.Marshal(anything) }

func encodeSettings(settings Settings) ([]byte, error) { return bson.Marshal(&settings) }

func decodeAccount(encoded []byte) (Account, error) {
	var account Account
	err := bson.Unmarshal(encoded, &account)
	return account, err
}

func encodeScalar(scalar int32) (byte, error) {
	valueType, _, err := bson.MarshalValue(scalar)
	return byte(valueType), err
}

func decodeRawValue(encoded bson.RawValue) (string, error) {
	var text string
	err := encoded.Unmarshal(&text)
	return text, err
}

func typeOfRawValue(encoded bson.RawValue) byte { return byte(encoded.Type) }

func parseExtJSON(text []byte) (bson.D, error) {
	var parsed bson.D
	err := bson.UnmarshalExtJSON(text, true, &parsed)
	return parsed, err
}

func renderExtJSON(ordered bson.D) ([]byte, error) { return bson.MarshalExtJSON(ordered, true, false) }

func mustBytes(encoded []byte, err error) []byte {
	if err != nil {
		return nil
	}
	return encoded
}

// marshalPair puts two codec calls on one line.
func marshalPair(first, second bson.D) [][]byte {
	return [][]byte{mustBytes(bson.Marshal(first)), mustBytes(bson.Marshal(second))}
}
