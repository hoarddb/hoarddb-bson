// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

//go:build bsonoracle

package bson

import (
	"sync/atomic"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// The runtime oracle tap of spec 9.1.7, compiled only with the bsonoracle
// tag. hoardDB's test-only internal/bsonoracle/tap installs one that runs
// the vendor beside every call, fails on a divergence, and records the
// (Go type, bytes) pairs for the fuzz seeds and the inventory.

// OracleEvent is one call of an entry point as a tap sees it, before the
// call runs or after it returns.
type OracleEvent struct {
	// Function is the entry point: "Marshal", "MarshalValue", "Unmarshal",
	// "RawValue.Unmarshal", "Document.Decode", "MarshalExtJSON" or
	// "UnmarshalExtJSON".
	Function string
	// Value is the Go value encoded, or the decode's target. Before a
	// decode it is the target as the caller passed it, not yet written.
	Value any
	// Bytes is the BSON: the input of a decode, before and after; the
	// result of an encode, after only. For MarshalValue and
	// RawValue.Unmarshal it is the value's bytes.
	Bytes []byte
	// Type is the value's BSON type, for MarshalValue (after) and
	// RawValue.Unmarshal; zero otherwise.
	Type bsontype.Type
	// Err is the call's error, after only.
	Err error
	// Canonical and EscapeHTML are an Extended JSON call's mode arguments
	// (EscapeHTML for MarshalExtJSON only). For those calls Bytes is the
	// text: UnmarshalExtJSON's input, before and after; MarshalExtJSON's
	// result, after only.
	Canonical  bool
	EscapeHTML bool
}

// OracleTap observes one call: it receives the call's inputs before the
// call runs, and returns the function that receives its results. When that
// function returns an error, the call returns it instead of its own result,
// which fails the test that made the call. The tap runs on the caller's
// goroutine, from every goroutine that calls an entry point, so it must be
// safe for concurrent use. A call the tap makes itself is tapped too.
type OracleTap func(before OracleEvent) (after func(OracleEvent) error)

// installedTap is the tap every entry point calls, or nil for none.
var installedTap atomic.Pointer[OracleTap]

// SetOracleTap installs tap for every later call of Marshal, MarshalValue,
// Unmarshal, RawValue.Unmarshal, Document.Decode, MarshalExtJSON and
// UnmarshalExtJSON, in place of any tap
// installed before. SetOracleTap(nil) removes it.
func SetOracleTap(tap OracleTap) {
	if tap == nil {
		installedTap.Store(nil)
		return
	}
	installedTap.Store(&tap)
}

// oracleTapCompiled is true: the entry points call oracleBefore.
const oracleTapCompiled = true

// oracleBefore hands the installed tap a call's inputs, and returns what
// hands it the results. With no tap installed it returns untapped, so an
// untapped call allocates nothing more than in a build without the tag.
func oracleBefore(function string, value any, valueBytes []byte, valueType bsontype.Type) oracleAfter {
	tap := installedTap.Load()
	if tap == nil {
		return untapped
	}
	after := (*tap)(OracleEvent{Function: function, Value: value, Bytes: valueBytes, Type: valueType})
	if after == nil {
		return untapped
	}
	return func(value any, valueBytes []byte, valueType bsontype.Type, err error) error {
		return after(OracleEvent{Function: function, Value: value, Bytes: valueBytes, Type: valueType, Err: err})
	}
}

// oracleBeforeText is oracleBefore for an Extended JSON call, whose events
// also carry its mode arguments; text is UnmarshalExtJSON's input.
func oracleBeforeText(function string, value any, text []byte, canonical, escapeHTML bool) oracleAfter {
	tap := installedTap.Load()
	if tap == nil {
		return untapped
	}
	after := (*tap)(OracleEvent{Function: function, Value: value, Bytes: text, Canonical: canonical, EscapeHTML: escapeHTML})
	if after == nil {
		return untapped
	}
	return func(value any, text []byte, _ bsontype.Type, err error) error {
		return after(OracleEvent{Function: function, Value: value, Bytes: text, Err: err, Canonical: canonical, EscapeHTML: escapeHTML})
	}
}

func untapped(any, []byte, bsontype.Type, error) error { return nil }
