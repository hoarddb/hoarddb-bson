// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hoarddb/hoarddb-bson/bson"
)

// The bson package's names against the two lists that allow them: the
// inventory (docs/api-inventory.md), for mirrored names, and the new core's
// record (docs/core-api.md), for the names the spec adds (5.2). Both ways:
// a name off both lists fails, and a listed name the package lacks fails
// unless bsonNamesLater says which step builds it.

// bsonNamesLater are the inventoried bson names the package does not
// build, each with the reason: a later step builds it, or it is kept out.
var bsonNamesLater = map[string]string{
	// Step 4f's finding: the inventory reaches these six only from
	// server/range_raw_test.go (the registry-hook test spec call C15
	// rewrites before the swap) and the test-only vendor shim. Their
	// signatures name bsoncodec.Registry, DecodeContext and EncodeContext
	// and bsonrw's reader and writer, which spec call C7 leaves out of the
	// library, so they cannot be mirrored with the vendor's signatures.
	"Decoder":         "kept out (step 4f): C15 test and shim only; needs bsoncodec.Registry and bsonrw",
	"Encoder":         "kept out (step 4f): C15 test and shim only; needs bsoncodec.Registry and bsonrw",
	"NewDecoder":      "kept out (step 4f): C15 test and shim only; takes a bsonrw.ValueReader",
	"NewEncoder":      "kept out (step 4f): C15 test and shim only; takes a bsonrw.ValueWriter",
	"NewRegistry":     "kept out (step 4f): C15 test and shim only; returns a bsoncodec.Registry",
	"DefaultRegistry": "kept out (step 4f): C15 test and shim only; is a bsoncodec.Registry",
}

// inventoryBsonName matches a bson row of the inventory's name table, and
// inventoryBsonSection the heading of a bson type's whole method set.
var (
	inventoryBsonName    = regexp.MustCompile("^\\| `bson\\.([A-Za-z0-9]+)` \\| ")
	inventoryBsonSection = regexp.MustCompile("^### `bson\\.([A-Za-z0-9]+)`$")
	inventoryBsonMethod  = regexp.MustCompile("^- `func \\(\\*?bson\\.([A-Za-z0-9]+)\\) ([A-Za-z0-9]+)\\(")
	coreAPIDeclaration   = regexp.MustCompile(`^(?:func|type|var|const) ([A-Z][A-Za-z0-9]*)\b`)
)

func readDoc(t *testing.T, path string) []string {
	t.Helper()
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return strings.Split(string(text), "\n")
}

// inventoriedBsonNames are the bson package-level names the inventory
// lists, from its name table and its method-set headings.
func inventoriedBsonNames(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, line := range readDoc(t, "../docs/api-inventory.md") {
		for _, pattern := range []*regexp.Regexp{inventoryBsonName, inventoryBsonSection} {
			if match := pattern.FindStringSubmatch(line); match != nil {
				seen[match[1]] = true
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("the inventory lists no bson name; the row patterns no longer match it")
	}
	var names []string
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// coreAPIPackage matches the line that names the package of the Go blocks
// after it.
var coreAPIPackage = regexp.MustCompile("^Package `([^`]+)`")

// coreAPINames are the package-level names of docs/core-api.md's Go blocks
// that follow a "Package `.../bson`" line. Other packages' blocks (step 5b's
// internal/extjson) are not package bson's.
func coreAPINames(t *testing.T) []string {
	t.Helper()
	var names []string
	inBlock, inBson := false, false
	for _, line := range readDoc(t, "../docs/core-api.md") {
		switch {
		case coreAPIPackage.MatchString(line):
			inBson = coreAPIPackage.FindStringSubmatch(line)[1] == "github.com/hoarddb/hoarddb-bson/bson"
		case strings.HasPrefix(line, "```go"):
			inBlock = true
		case strings.HasPrefix(line, "```"):
			inBlock = false
		case inBlock && inBson:
			if match := coreAPIDeclaration.FindStringSubmatch(line); match != nil {
				names = append(names, match[1])
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("core-api.md declares no name; the block pattern no longer matches it")
	}
	return names
}

func laterStepOf(name string) (string, bool) {
	step, later := bsonNamesLater[name]
	return step, later
}

// TestBsonExportsOnlyInventoriedNames holds package bson to the inventory
// and the new core's record, in both directions.
func TestBsonExportsOnlyInventoriedNames(t *testing.T) {
	inventoried, coreNames := inventoriedBsonNames(t), coreAPINames(t)
	libraryNames := exportedNamesOf(t, "github.com/hoarddb/hoarddb-bson/bson")
	for _, name := range libraryNames {
		if !containsName(inventoried, name) && !containsName(coreNames, name) {
			t.Errorf("bson exports %s, which neither the inventory nor core-api.md lists", name)
		}
		if step, later := laterStepOf(name); later && containsName(inventoried, name) {
			t.Errorf("bson exports %s, listed as arriving later (%s); delete the allowance", name, step)
		}
	}
	var missing []string
	for _, name := range inventoried {
		if _, later := laterStepOf(name); !later && !containsName(libraryNames, name) {
			missing = append(missing, name)
		}
	}
	for _, name := range coreNames {
		if !containsName(libraryNames, name) {
			missing = append(missing, name+" (core-api.md)")
		}
	}
	if len(missing) > 0 {
		t.Errorf("listed bson names not built: %v", missing)
	}
	for name, step := range bsonNamesLater {
		if !containsName(inventoried, name) {
			t.Errorf("%s is allowed as arriving in %s but the inventory does not list it; delete the allowance", name, step)
		}
	}
}

// bsonMethodsLater are the inventoried methods of the built bson types that
// a later step adds (see raw_test.go).
var bsonMethodsLater = map[string]map[string]string{
	"RawValue":   rawValueMethodsLater,
	"RawElement": rawElementMethodsLater,
}

// TestBsonMethodSetsAreTheInventoried holds each built bson type's exported
// methods to the inventory's whole method set, both ways, less the methods
// a later step adds.
func TestBsonMethodSetsAreTheInventoried(t *testing.T) {
	inventoried := map[string][]string{}
	for _, line := range readDoc(t, "../docs/api-inventory.md") {
		if match := inventoryBsonMethod.FindStringSubmatch(line); match != nil {
			if _, later := bsonMethodsLater[match[1]][match[2]]; !later {
				inventoried[match[1]] = append(inventoried[match[1]], match[2])
			}
		}
	}
	libraryTypes := map[string]reflect.Type{
		"Raw": reflect.TypeOf(bson.Raw{}), "RawElement": reflect.TypeOf(bson.RawElement{}), "RawValue": reflect.TypeOf(bson.RawValue{}),
	}
	for typeName, libraryType := range libraryTypes {
		var libraryMethods []string
		for methodIndex := range reflect.PointerTo(libraryType).NumMethod() {
			libraryMethods = append(libraryMethods, reflect.PointerTo(libraryType).Method(methodIndex).Name)
		}
		listed := append([]string{}, inventoried[typeName]...)
		sort.Strings(listed)
		sort.Strings(libraryMethods)
		if len(listed) == 0 {
			t.Fatalf("the inventory lists no methods of bson.%s; the row pattern no longer matches it", typeName)
		}
		if strings.Join(listed, " ") != strings.Join(libraryMethods, " ") {
			t.Errorf("bson.%s methods:\n library %v\n inventory %v", typeName, libraryMethods, listed)
		}
	}
}
