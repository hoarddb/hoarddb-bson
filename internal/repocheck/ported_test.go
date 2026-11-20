// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package repocheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// modifiedLine is the line a ported file adds right after the vendor header
// (Apache-2.0 section 4(b)): a date and a non-empty summary.
var modifiedLine = regexp.MustCompile(`^// Modified by hoardDB, \d{4}-\d{2}-\d{2}: \S.*$`)

func TestPortedFilesCarryVendorHeaderAndModifiedLine(t *testing.T) {
	listed := portedFiles(t)
	// Decimal128 is ported (spec section 4.2 item 2), so an empty listing
	// means the table or its parser is broken, not that nothing is ported.
	if _, ok := listed["bson/primitive/decimal.go"]; !ok {
		t.Fatalf("docs/ported-files.md does not list bson/primitive/decimal.go; listed: %v", listed)
	}
	if listed["bson/primitive/decimal.go"] != "mgo" {
		t.Errorf("bson/primitive/decimal.go is listed with header form %q, want mgo (its vendor file carries the mgo lines)", listed["bson/primitive/decimal.go"])
	}
	for relativePath, form := range listed {
		if problem := headerFormProblemAt(t, filepath.Join(repoRoot(t), filepath.FromSlash(relativePath)), form); problem != "" {
			t.Errorf("ported file %s %s", relativePath, problem)
		}
	}
	// The kept "See THIRD-PARTY-NOTICES" line names the file at this
	// repository's root, which must therefore exist and carry the mgo notice.
	notices, err := os.ReadFile(filepath.Join(repoRoot(t), "THIRD-PARTY-NOTICES"))
	if err != nil {
		t.Fatalf("ported files refer to this repository's THIRD-PARTY-NOTICES, which cannot be read: %v", err)
	}
	if !strings.Contains(string(notices), "License notice for gopkg.in/mgo.v2/bson") {
		t.Errorf("THIRD-PARTY-NOTICES, which the ported headers refer to, lacks the mgo notice")
	}
}

// TestHeaderFormIsPerFile builds scratch ported files in a temp directory and
// requires the check to report a file whose lines do not match the form its
// row names. The mgo-form file with its mgo lines dropped is the case the
// first version of the check let through.
func TestHeaderFormIsPerFile(t *testing.T) {
	const modified = "// Modified by hoardDB, 2026-10-24: scratch\n\npackage scratch\n"
	mgoLines := "//\n// Based on gopkg.in/mgo.v2/bson by Gustavo Niemeyer\n// See THIRD-PARTY-NOTICES for original license terms.\n"
	for _, caseOf := range []struct {
		name, form, text string
		wantProblem      bool
	}{
		{"mgo form complete", "mgo", vendorHeaderOriginal + mgoLines + modified, false},
		{"mgo form with its mgo lines dropped", "mgo", vendorHeaderOriginal + modified, true},
		{"mgo form with only the Based-on line dropped", "mgo", vendorHeaderOriginal + "//\n// See THIRD-PARTY-NOTICES for original license terms.\n" + modified, true},
		{"apache form complete", "apache", vendorHeaderOriginal + modified, false},
		{"apache form carrying mgo lines", "apache", vendorHeaderOriginal + mgoLines + modified, true},
		{"mgo form without the Modified line", "mgo", vendorHeaderOriginal + mgoLines + "\npackage scratch\n", true},
		{"unknown form", "either", vendorHeaderOriginal + modified, true},
	} {
		scratchPath := filepath.Join(t.TempDir(), "scratch.go")
		if err := os.WriteFile(scratchPath, []byte(caseOf.text), 0o600); err != nil {
			t.Fatalf("%s: writing the scratch file: %v", caseOf.name, err)
		}
		if problem := headerFormProblemAt(t, scratchPath, caseOf.form); (problem != "") != caseOf.wantProblem {
			t.Errorf("%s: problem = %q, want a problem: %t", caseOf.name, problem, caseOf.wantProblem)
		}
	}
}
