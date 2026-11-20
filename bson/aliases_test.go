// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson_test

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
)

// Each assignment compiles only if the bson name and the primitive name are
// one type: two defined types with the same underlying type are not
// assignable to each other (spec 5.1, aliases).
var (
	_ bson.D      = primitive.D(nil)
	_ primitive.D = bson.D(nil)
	_ bson.E      = primitive.E{}
	_ primitive.E = bson.E{}
	_ bson.M      = primitive.M(nil)
	_ primitive.M = bson.M(nil)
	_ bson.A      = primitive.A(nil)
	_ primitive.A = bson.A(nil)
)

// typeCheckSnippet type-checks a function body in a file that imports bson
// and primitive.
func typeCheckSnippet(t *testing.T, body string) error {
	t.Helper()
	t.Setenv("GOWORK", "off")
	fileSet := token.NewFileSet()
	sourceText := "package snippet\n\nimport (\n\t\"github.com/hoarddb/hoarddb-bson/bson\"\n\t\"github.com/hoarddb/hoarddb-bson/bson/primitive\"\n)\n\n" +
		"var _ = primitive.D{}\nvar _ = bson.D{}\n\nfunc snippet(holder any) {\n" + body + "\n}\n"
	parsedFile, err := parser.ParseFile(fileSet, "snippet.go", sourceText, 0)
	if err != nil {
		t.Fatalf("parsing the snippet: %v", err)
	}
	config := types.Config{Importer: importer.ForCompiler(fileSet, "source", nil)}
	_, err = config.Check("snippet", fileSet, []*ast.File{parsedFile}, nil)
	return err
}

// TestAliasesAreAliases pins bson.D, E, M and A as aliases of the primitive
// types, as the vendor declares them, because the tree mixes both spellings
// in one type switch (server/sort_order.go:sortKeyOf). A type switch with
// a case for each spelling is a duplicate case only if they are one type,
// and a value of one spelling matches a case of the other.
func TestAliasesAreAliases(t *testing.T) {
	for _, pair := range [][2]string{{"bson.D", "primitive.D"}, {"bson.E", "primitive.E"}, {"bson.M", "primitive.M"}, {"bson.A", "primitive.A"}} {
		err := typeCheckSnippet(t, "switch holder.(type) {\ncase "+pair[0]+":\ncase "+pair[1]+":\n}")
		if err == nil || !strings.Contains(err.Error(), "duplicate case") {
			t.Errorf("a type switch on %s and %s type-checked with %v, want a duplicate case", pair[0], pair[1], err)
		}
	}
	if err := typeCheckSnippet(t, "switch holder.(type) {\ncase bson.D:\ncase bson.M:\ncase bson.A:\ncase bson.E:\n}"); err != nil {
		t.Fatalf("the canary switch over four distinct types failed to type-check: %v", err)
	}
	for name, holder := range map[string]any{"D": primitive.D{{Key: "a", Value: 1}}, "E": primitive.E{Key: "a"}, "M": primitive.M{"a": 1}, "A": primitive.A{1}} {
		matched := ""
		switch holder.(type) {
		case bson.D:
			matched = "D"
		case bson.E:
			matched = "E"
		case bson.M:
			matched = "M"
		case bson.A:
			matched = "A"
		}
		if matched != name {
			t.Errorf("a primitive.%s matched the bson case %q", name, matched)
		}
	}
}

// TestDMapKeepsTheLastDuplicate pins D.Map: every element becomes a map
// entry, and a repeated key keeps its last value, as the vendor's loop does.
func TestDMapKeepsTheLastDuplicate(t *testing.T) {
	ordered := bson.D{{Key: "a", Value: 1}, {Key: "b", Value: "two"}, {Key: "a", Value: 3}}
	mapped := ordered.Map() //nolint:staticcheck // mirrored deprecated name
	if len(mapped) != 2 || mapped["a"] != 3 || mapped["b"] != "two" {
		t.Fatalf("Map = %v, want map[a:3 b:two]", mapped)
	}
	if empty := bson.D(nil).Map(); empty == nil || len(empty) != 0 { //nolint:staticcheck // mirrored deprecated name
		t.Fatalf("Map of a nil D = %#v, want an empty non-nil map", empty)
	}
}
