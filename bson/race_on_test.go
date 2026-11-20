// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

//go:build race

package bson_test

// raceEnabled is true under -race, which changes allocation counts, so
// every AllocsPerRun test skips (build doc section 2, further traps).
const raceEnabled = true
