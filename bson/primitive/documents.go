// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package primitive

// D is an ordered BSON document: its elements encode in slice order. Use it
// where the order of the keys matters, as in a command or a sort key.
//
//	bson.D{{Key: "name", Value: "Ada"}, {Key: "age", Value: 36}}
type D []E

// Map returns the elements of d as an M. A key that appears more than once
// keeps its last value.
//
// Deprecated: an M loses the order D keeps. Marshal the D and unmarshal the
// bytes into an M instead.
func (d D) Map() M {
	elementsByKey := make(M, len(d))
	for _, element := range d {
		elementsByKey[element.Key] = element.Value
	}
	return elementsByKey
}

// E is one element of a D: a key and its value.
type E struct {
	Key   string
	Value interface{}
}

// M is an unordered BSON document. Its keys encode in no promised order;
// use a D where the order matters.
//
//	bson.M{"name": "Ada", "age": 36}
type M map[string]interface{}

// A is an ordered BSON array.
//
//	bson.A{"Ada", 36, bson.D{{Key: "city", Value: "London"}}}
type A []interface{}
