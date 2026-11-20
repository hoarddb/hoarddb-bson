// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: RawElement's methods on the library's bsoncore; DebugString renders the ErrNestingTooDeep refusal past MaxNestingCeiling; String renders through the library's Extended JSON writer (build step 5a) and the nesting refusal past MaxNestingCeiling

package bson

import (
	"errors"

	"github.com/hoarddb/hoarddb-bson/internal/extjson"
	"github.com/hoarddb/hoarddb-bson/x/bsonx/bsoncore"
)

// RawElement is a raw encoded BSON document or array element.
type RawElement []byte

// Key returns the key for this element. If the element is not valid, this method returns an empty
// string. If knowing if the element is valid is important, use KeyErr.
func (re RawElement) Key() string { return bsoncore.Element(re).Key() }

// KeyErr returns the key for this element, returning an error if the element is not valid.
func (re RawElement) KeyErr() (string, error) { return bsoncore.Element(re).KeyErr() }

// Value returns the value of this element. If the element is not valid, this method returns an
// empty Value. If knowing if the element is valid is important, use ValueErr.
func (re RawElement) Value() RawValue { return convertFromCoreValue(bsoncore.Element(re).Value()) }

// ValueErr returns the value for this element, returning an error if the element is not valid.
func (re RawElement) ValueErr() (RawValue, error) {
	val, err := bsoncore.Element(re).ValueErr()
	return convertFromCoreValue(val), err
}

// Validate ensures re is a valid BSON element.
func (re RawElement) Validate() error { return bsoncore.Element(re).Validate() }

// String returns the BSON element encoded as Extended JSON. An element
// whose value opens a level past MaxNestingCeiling (the value being level
// 1, as in DebugString) renders as a refusal naming ErrNestingTooDeep.
func (re RawElement) String() string {
	text, err := extjson.AppendElementText(nil, re, true, false)
	switch {
	case err == nil:
		return string(text)
	case errors.Is(err, extjson.ErrNestingTooDeep):
		return nestingRefusal
	}
	return "<malformed>"
}

// DebugString outputs a human readable version of RawElement. It will attempt to stringify the
// valid components of the element even if the entire element is not valid. An element nested
// deeper than MaxNestingCeiling renders as a refusal naming ErrNestingTooDeep.
func (re RawElement) DebugString() string {
	element := bsoncore.Element(re)
	return refuseValuePastCeiling(element.DebugString(), "<malformed>", element.Value())
}
