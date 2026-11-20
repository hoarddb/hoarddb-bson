// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

//go:build !bsonoracle

package bson_test

import (
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"testing"
)

// packageScope type-checks package bson's non-test files as a build with
// buildTags selects them, and returns the package's scope.
func packageScope(t *testing.T, buildTags []string) *types.Scope {
	t.Helper()
	t.Setenv("GOWORK", "off")
	buildContext := build.Default
	buildContext.BuildTags = buildTags
	packageDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := buildContext.ImportDir(packageDir, 0)
	if err != nil {
		t.Fatalf("listing package bson's files with tags %v: %v", buildTags, err)
	}
	fileSet := token.NewFileSet()
	var parsedFiles []*ast.File
	for _, fileName := range listed.GoFiles {
		parsedFile, err := parser.ParseFile(fileSet, filepath.Join(packageDir, fileName), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", fileName, err)
		}
		parsedFiles = append(parsedFiles, parsedFile)
	}
	config := types.Config{Importer: importer.ForCompiler(fileSet, "source", nil)}
	checked, err := config.Check("github.com/hoarddb/hoarddb-bson/bson", fileSet, parsedFiles, nil)
	if err != nil {
		t.Fatalf("type-checking package bson with tags %v: %v", buildTags, err)
	}
	return checked.Scope()
}

// TestOracleTapIsAbsentWithoutTheTag holds spec 9.1.7: a normal build
// declares no tap, so it carries no tap state or cost. The same check with
// the bsonoracle tag must find SetOracleTap, so the test cannot pass
// because the loader saw nothing.
func TestOracleTapIsAbsentWithoutTheTag(t *testing.T) {
	untagged := packageScope(t, nil)
	if untagged.Lookup("Marshal") == nil {
		t.Fatal("the untagged package declares no Marshal; the loader is broken")
	}
	for _, tapName := range []string{"SetOracleTap", "OracleTap", "OracleEvent"} {
		if untagged.Lookup(tapName) != nil {
			t.Errorf("the untagged package declares %s", tapName)
		}
	}
	if packageScope(t, []string{"bsonoracle"}).Lookup("SetOracleTap") == nil {
		t.Error("the package built with the bsonoracle tag declares no SetOracleTap")
	}
}
