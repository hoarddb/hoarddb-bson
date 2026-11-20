// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

// Package nesting holds the library's depth ceiling (spec 5.2), so that
// package bson, which exports it, and the encoder and decoder, which bson
// imports and which enforce it, share one constant and one sentinel.
package nesting

import (
	"errors"
	"fmt"
)

// Ceiling is the deepest any document may nest in this library, the root
// counted as level 1 and each embedded document, array and code-with-scope
// scope adding one. bson.MaxNestingCeiling is this constant.
const Ceiling = 200

// ErrTooDeep is bson.ErrNestingTooDeep.
var ErrTooDeep = errors.New("bson: document nested too deep")

// ErrPastCeiling is the refusal of a level past Ceiling by the encoder and
// the decoder. One value serves every refusal, so refusing allocates
// nothing.
var ErrPastCeiling = fmt.Errorf("%w: more than %d levels", ErrTooDeep, Ceiling)
