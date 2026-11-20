// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

//go:build !bsonoracle

package bson

import "github.com/hoarddb/hoarddb-bson/bson/bsontype"

// A build without the bsonoracle tag has no oracle tap (spec 9.1.7): no
// SetOracleTap, no state and no cost. oracleTapCompiled is the constant
// false, so the entry points' tapped branches are compiled away, and
// oracleBefore and oracleBeforeText, which only those branches call, are left out of every
// linked binary.
const oracleTapCompiled = false

// oracleBefore lets the entry points' tapped branches compile in this
// build; no call of it is ever reached.
func oracleBefore(string, any, []byte, bsontype.Type) oracleAfter { return nil }

// oracleBeforeText is oracleBefore for the Extended JSON entry points.
func oracleBeforeText(string, any, []byte, bool, bool) oracleAfter { return nil }
