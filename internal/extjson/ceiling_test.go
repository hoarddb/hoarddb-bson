// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package extjson_test

import (
	"testing"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/internal/extjson"
)

// The Extended JSON ceiling is the library's one nesting ceiling (spec 5.2).
// They are two constants because bson will import this package.
func TestMaxNestingDepthIsTheCeiling(t *testing.T) {
	if extjson.MaxNestingDepth != bson.MaxNestingCeiling {
		t.Fatalf("extjson.MaxNestingDepth is %d, bson.MaxNestingCeiling %d", extjson.MaxNestingDepth, bson.MaxNestingCeiling)
	}
}
