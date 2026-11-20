// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

var fixtureConfig = Config{
	Trees:      []Tree{{Label: "server", Dir: "testdata/fixture", Commit: "fixture-commit"}},
	Boundaries: []string{"checkDocumentDepth", "refuseDeepRequest", "missingBoundary"},
	ToolCommit: "tool-commit",
}

var (
	fixtureOnce      sync.Once
	fixtureInventory *Inventory
	fixtureErr       error
)

func collectFixture(t *testing.T) *Inventory {
	t.Helper()
	fixtureOnce.Do(func() { fixtureInventory, fixtureErr = Collect(fixtureConfig) })
	if fixtureErr != nil {
		t.Fatalf("collecting the fixture inventory: %v", fixtureErr)
	}
	return fixtureInventory
}

func findReference(inventory *Inventory, name string) (Reference, bool) {
	for _, reference := range inventory.References {
		if reference.Name == name {
			return reference, true
		}
	}
	return Reference{}, false
}

func TestInventoryCountsReferencesInTheFixture(t *testing.T) {
	inventory := collectFixture(t)
	wantCounts := map[string]int{
		"bson.Marshal":                13,
		"bson.Unmarshal":              6,
		"bson.MarshalValue":           1,
		"bson.UnmarshalExtJSON":       1,
		"bson.MarshalExtJSON":         1,
		"bson.D":                      14,
		"bson.M":                      1,
		"bson.Raw":                    1,
		"bson.RawValue":               2,
		"bson.RawValue.Unmarshal":     1,
		"bson.RawValue.Type":          1,
		"primitive.ObjectID":          2,
		"primitive.Decimal128":        1,
		"primitive.DateTime":          1,
		"primitive.ObjectID.Hex":      1,
		"bson.Raw.String":             1,
		"primitive.Decimal128.String": 1,
	}
	for name, wantCount := range wantCounts {
		reference, found := findReference(inventory, name)
		if !found {
			t.Errorf("reference %q is missing from the inventory", name)
			continue
		}
		if gotCount := reference.Counts["server"]; gotCount != wantCount {
			t.Errorf("%s: %d references, want %d", name, gotCount, wantCount)
		}
	}
	for _, unused := range []string{"bson.NewDecoder", "bson.Unmarshal.x", "primitive.NewObjectID"} {
		if _, found := findReference(inventory, unused); found {
			t.Errorf("reference %q is listed but the fixture never uses it", unused)
		}
	}
}

func TestInventoryCountsImportingFiles(t *testing.T) {
	inventory := collectFixture(t)
	wantFiles := map[string]int{
		"go.mongodb.org/mongo-driver/bson":           2,
		"go.mongodb.org/mongo-driver/bson/primitive": 3,
	}
	gotFiles := map[string]int{}
	for _, importCount := range inventory.Imports {
		gotFiles[importCount.Path] = importCount.Files["server"]
	}
	for importPath, wantCount := range wantFiles {
		if gotFiles[importPath] != wantCount {
			t.Errorf("%s: imported by %d files, want %d", importPath, gotFiles[importPath], wantCount)
		}
	}
}

func findType(inventory *Inventory, name string) (TypeEntry, bool) {
	for _, entry := range inventory.Types {
		if entry.Name == name {
			return entry, true
		}
	}
	return TypeEntry{}, false
}

func TestInventoryListsWholeMethodSets(t *testing.T) {
	inventory := collectFixture(t)

	// bsontype.Type is reached only through the RawValue.Type field: no name
	// reference and no method call anywhere in the fixture.
	reachedOnly, found := findType(inventory, "bsontype.Type")
	if !found {
		t.Fatal("bsontype.Type, reached only through a struct field, is not listed")
	}
	if reachedOnly.NameRefs != 0 || !reachedOnly.ViaExpression {
		t.Errorf("bsontype.Type: NameRefs=%d ViaExpression=%v, want 0 and true", reachedOnly.NameRefs, reachedOnly.ViaExpression)
	}
	if want := []string{"IsValid", "String"}; !slices.Equal(reachedOnly.Methods, want) {
		t.Errorf("bsontype.Type methods = %v, want the whole set %v", reachedOnly.Methods, want)
	}
	if !slices.Equal(reachedOnly.Implicit, []string{"String"}) {
		t.Errorf("bsontype.Type implicit-call methods = %v, want [String]", reachedOnly.Implicit)
	}

	rawValue, found := findType(inventory, "bson.RawValue")
	if !found {
		t.Fatal("bson.RawValue is not listed")
	}
	for _, wantMethod := range []string{"Unmarshal", "UnmarshalWithRegistry", "Validate", "String", "IsZero", "Int32", "Document"} {
		if !slices.Contains(rawValue.Methods, wantMethod) {
			t.Errorf("bson.RawValue lacks method %q (has %d methods)", wantMethod, len(rawValue.Methods))
		}
	}
	if !slices.Equal(rawValue.UnexportedFields, []string{"r"}) {
		t.Errorf("bson.RawValue unexported fields = %v, want [r]", rawValue.UnexportedFields)
	}
	if !slices.Equal(rawValue.Fields, []string{"Type", "Value"}) {
		t.Errorf("bson.RawValue exported fields = %v, want [Type Value]", rawValue.Fields)
	}

	// UnmarshalJSON has a pointer receiver: the set of a value type alone would miss it.
	objectID, found := findType(inventory, "primitive.ObjectID")
	if !found {
		t.Fatal("primitive.ObjectID is not listed")
	}
	for _, wantMethod := range []string{"Hex", "IsZero", "MarshalJSON", "UnmarshalJSON", "MarshalText", "UnmarshalText", "String", "Timestamp"} {
		if !slices.Contains(objectID.Methods, wantMethod) {
			t.Errorf("primitive.ObjectID lacks method %q, has %v", wantMethod, objectID.Methods)
		}
	}

	ordered, found := findType(inventory, "primitive.D")
	if !found {
		t.Fatal("primitive.D, used only through the bson.D alias, is not listed")
	}
	if ordered.NameRefs != 14 || !slices.Equal(ordered.Aliases, []string{"bson.D"}) {
		t.Errorf("primitive.D: NameRefs=%d Aliases=%v, want 14 and [bson.D]", ordered.NameRefs, ordered.Aliases)
	}
}

func callsByFunction(inventory *Inventory) map[string]CallSite {
	byFunction := map[string]CallSite{}
	for _, call := range inventory.Calls {
		byFunction[call.Func] = call
	}
	return byFunction
}

func TestInventoryClassifiesStaticTargets(t *testing.T) {
	inventory := collectFixture(t)
	if len(inventory.Calls) != 23 {
		t.Errorf("%d codec call sites, want 23", len(inventory.Calls))
	}
	byFunction := callsByFunction(inventory)
	wantCalls := []struct {
		function, callee, kind, class string
		pendingTap                    bool
	}{
		{"encodeAccount", "bson.Marshal", "Marshal", "struct", false},
		{"encodeOrdered", "bson.Marshal", "Marshal", "ordered-document", false},
		{"encodePlainMap", "bson.Marshal", "Marshal", "map", false},
		{"encodeBSONM", "bson.Marshal", "Marshal", "map", false},
		{"encodeAnything", "bson.Marshal", "Marshal", "interface{}", true},
		{"encodeSettings", "bson.Marshal", "Marshal", "struct", false},
		{"decodeAccount", "bson.Unmarshal", "Unmarshal", "struct", false},
		{"encodeScalar", "bson.MarshalValue", "MarshalValue", "scalar", false},
		{"decodeRawValue", "bson.RawValue.Unmarshal", "RawValue.Unmarshal", "scalar", false},
		{"parseExtJSON", "bson.UnmarshalExtJSON", "ExtJSON", "ordered-document", false},
		{"renderExtJSON", "bson.MarshalExtJSON", "ExtJSON", "ordered-document", false},
	}
	for _, want := range wantCalls {
		got, found := byFunction[want.function]
		if !found {
			t.Errorf("%s: no codec call site", want.function)
			continue
		}
		if got.Callee != want.callee || got.Kind != want.kind || got.Class != want.class || got.PendingTap != want.pendingTap {
			t.Errorf("%s: got callee=%s kind=%s class=%s pending=%v, want %s %s %s %v",
				want.function, got.Callee, got.Kind, got.Class, got.PendingTap, want.callee, want.kind, want.class, want.pendingTap)
		}
		if got.Tree != "server" || got.File == "" || got.Line == 0 {
			t.Errorf("%s: site position is incomplete: %+v", want.function, got)
		}
	}
}

func TestInventoryFindsMapMarshalSites(t *testing.T) {
	inventory := collectFixture(t)
	gotVia := map[string]string{}
	for _, site := range inventory.MapSites {
		gotVia[site.Call.Func] = site.Via
	}
	if len(gotVia) != 5 {
		t.Errorf("map-marshal sites in %v, want exactly encodePlainMap, encodeBSONM, encodeSettings, encodeAnything and truncatedText (both interface{} targets)", gotVia)
	}
	if !strings.Contains(gotVia["encodeAnything"], "may hold a map (pending tap)") {
		t.Errorf("encodeAnything via %q, want \"may hold a map (pending tap)\": an interface{} target can hold a map", gotVia["encodeAnything"])
	}
	if !strings.Contains(gotVia["encodePlainMap"], "map[string]interface{}") {
		t.Errorf("encodePlainMap via %q, want the map type", gotVia["encodePlainMap"])
	}
	if !strings.Contains(gotVia["encodeBSONM"], "bson.M") {
		t.Errorf("encodeBSONM via %q, want bson.M", gotVia["encodeBSONM"])
	}
	if !strings.Contains(gotVia["encodeSettings"], "Labels") {
		t.Errorf("encodeSettings via %q, want the Labels field path", gotVia["encodeSettings"])
	}
	for _, notAMapSite := range []string{"encodeAccount", "encodeOrdered"} {
		if _, found := gotVia[notAMapSite]; found {
			t.Errorf("%s is listed as a map-marshal site", notAMapSite)
		}
	}
}

func TestInventoryFindsErrorTextAndRenderingSites(t *testing.T) {
	inventory := collectFixture(t)
	gotActions := map[string]string{}
	for _, site := range inventory.ErrorSites {
		gotActions[site.Func] = site.Action
	}
	wantActions := map[string]string{
		"truncatedText": "Error() truncated",
		"comparedText":  "Error() compared",
		"branchedText":  "Error() branched",
		"printedError":  "printed",
		"wrappedError":  "wrapped",

		"reassignedError":     "printed",
		"loggedCodecError":    "logged",
		"sentinelCompared":    "compared",
		"sentinelIs":          "compared (errors.Is)",
		"conditionalReassign": "logged",
	}
	for function, wantAction := range wantActions {
		if gotActions[function] != wantAction {
			t.Errorf("%s: error-text action %q, want %q", function, gotActions[function], wantAction)
		}
	}
	if len(inventory.ErrorSites) != len(wantActions) {
		t.Errorf("%d error-text sites, want %d (unrelatedErrorText, closureLocalError, redeclaredError and the use after the reassignment in reassignedError must not be listed): %v", len(inventory.ErrorSites), len(wantActions), gotActions)
	}

	type rendering struct{ function, form, name string }
	var gotRenderings []rendering
	for _, site := range inventory.Renderings {
		gotRenderings = append(gotRenderings, rendering{site.Func, site.Form, site.Name})
	}
	wantRenderings := []rendering{
		{"renderings", "explicit", "primitive.ObjectID.Hex"},
		{"renderings", "explicit", "primitive.Decimal128.String"},
		{"renderings", "explicit", "bson.Raw.String"},
		{"renderings", "implicit fmt", "primitive.ObjectID"},
		{"eventJSON", "implicit json", "primitive.DateTime"},
		{"type Event", "json tag", "primitive.DateTime"},
	}
	if len(gotRenderings) != len(wantRenderings) {
		t.Errorf("rendering sites = %v, want %v", gotRenderings, wantRenderings)
	}
	for _, want := range wantRenderings {
		if !slices.Contains(gotRenderings, want) {
			t.Errorf("rendering site %v is missing from %v", want, gotRenderings)
		}
	}
}

func TestInventoryListsBoundaryFunctions(t *testing.T) {
	inventory := collectFixture(t)
	byName := map[string]BoundaryEntry{}
	for _, entry := range inventory.Boundaries {
		byName[entry.Name] = entry
	}
	depth := byName["checkDocumentDepth"]
	if len(depth.Defined) != 1 {
		t.Errorf("checkDocumentDepth defined at %v, want one definition", depth.Defined)
	}
	var production, tests []string
	for _, caller := range depth.Callers {
		if caller.Test {
			tests = append(tests, caller.Func)
		} else {
			production = append(production, caller.Func)
		}
	}
	slices.Sort(production)
	if want := []string{"(*Server).restoreChunk", "refuseDeepRequest", "replayWAL"}; !slices.Equal(production, want) {
		t.Errorf("production callers = %v, want %v (a closure reports its enclosing declaration)", production, want)
	}
	if want := []string{"TestBoundaryFromATest"}; !slices.Equal(tests, want) {
		t.Errorf("test callers = %v, want %v", tests, want)
	}
	if got := byName["refuseDeepRequest"]; len(got.Defined) != 1 || len(got.Callers) != 0 {
		t.Errorf("refuseDeepRequest = %+v, want one definition and no callers", got)
	}
	if got, listed := byName["missingBoundary"]; !listed || len(got.Defined) != 0 {
		t.Errorf("missingBoundary = %+v (listed %v), want a listed entry with no definition so that a rename is visible", got, listed)
	}
}

func TestInventoryIsDeterministic(t *testing.T) {
	first, err := Run(fixtureConfig)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	second, err := Run(fixtureConfig)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two runs over the same tree give different bytes")
	}
	for _, wantFragment := range []string{
		"## 1. Referenced names", "## 2. Whole method sets", "## 3. Marshal and unmarshal call sites",
		"## 4. Map-marshal sites", "## 5. Error-text and rendering sites", "## 6. Boundary functions",
		"fixture-commit", "tool-commit", "pending tap",
	} {
		if !strings.Contains(string(first), wantFragment) {
			t.Errorf("rendered inventory lacks %q", wantFragment)
		}
	}
}

func TestInventoryTellsTwoCallsOnOneLineApart(t *testing.T) {
	inventory := collectFixture(t)
	var pair []CallSite
	for _, call := range inventory.Calls {
		if call.Func == "marshalPair" {
			pair = append(pair, call)
		}
	}
	if len(pair) != 2 || pair[0].Line != pair[1].Line || pair[0].col == pair[1].col {
		t.Fatalf("marshalPair calls = %+v, want two on one line in different columns", pair)
	}
	rendered := string(inventory.Render())
	for _, call := range pair {
		want := fmt.Sprintf("| server:%s:%d:%d |", call.File, call.Line, call.col)
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered inventory lacks the site %q, so two calls on one line look alike", want)
		}
	}
}
