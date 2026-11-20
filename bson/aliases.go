// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson

import "github.com/hoarddb/hoarddb-bson/bson/primitive"

// D, E, M and A are aliases, not new types, so a bson.D and a primitive.D
// are one type: code that names both in a type switch, as callers of the
// mirrored API do, needs them to be the same case.

// D is an ordered BSON document; see primitive.D.
type D = primitive.D

// E is one element of a D; see primitive.E.
type E = primitive.E

// M is an unordered BSON document; see primitive.M.
type M = primitive.M

// A is an ordered BSON array; see primitive.A.
type A = primitive.A
