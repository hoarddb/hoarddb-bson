// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import "testing"

func TestFuzzSeedsLoad(t *testing.T) {
	if len(fuzzTargets) != 4 {
		t.Fatalf("%d fuzz targets are declared, want 4", len(fuzzTargets))
	}
	for _, target := range fuzzTargets {
		t.Run(target.name, func(t *testing.T) {
			seedBytes, err := target.seeds()
			if err != nil {
				t.Fatalf("loading the seeds of %s: %v", target.name, err)
			}
			t.Logf("%s: %d seeds", target.name, len(seedBytes))
			if len(seedBytes) == 0 {
				t.Fatalf("%s has no seeds", target.name)
			}
		})
	}
}
