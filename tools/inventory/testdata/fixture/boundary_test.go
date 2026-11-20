// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package fixture

import "testing"

func TestBoundaryFromATest(t *testing.T) {
	if err := checkDocumentDepth(nil, 1); err != nil {
		t.Fatal(err)
	}
}
