// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

// Package repocheck holds the repository-level checks for licence, notices,
// headers and marks (spec §4.2). It has no non-test code on purpose: the
// checks read the tree, they do not import it.
package repocheck

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// apache2SHA256 is the SHA-256 of the canonical Apache License 2.0 text,
// including the appendix, as shipped in /usr/share/common-licenses/Apache-2.0.
const apache2SHA256 = "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30"

// noticeText is spec §4.2 item 4, verbatim, as amended by ruling O16: the
// origin line for the vendor and mgo, then the one for the server's depth
// scan that bson/validate.go derives from.
const noticeText = "Portions Copyright (C) MongoDB, Inc. 2017-present, Apache-2.0. " +
	"Portions based on gopkg.in/mgo.v2/bson, Copyright (c) 2010-2013 Gustavo Niemeyer, BSD-2-Clause.\n" +
	"Portions derived from hoardDB's server/nesting_depth.go, licensed to this library under Apache-2.0 by its copyright holder."

// readmeSentence is spec §4.2 item 7.
const readmeSentence = "A Go library implementing BSON as specified at bsonspec.org."

// hoarddbHeader is the header every hoardDB-written .go file starts with.
const hoarddbHeader = `// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
`

// vendorHeader is the header a ported file keeps, line for line.
const vendorHeader = `// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
//
// Based on gopkg.in/mgo.v2/bson by Gustavo Niemeyer
// See THIRD-PARTY-NOTICES for original license terms.
`

// vendorHeaderOriginal is the header of a vendor file that is MongoDB's own
// work, with no "Based on gopkg.in/mgo.v2/bson" lines (the bsoncore files:
// build doc step 4b, "the mgo line does not apply"). A ported file keeps
// whichever of the two its source starts with.
const vendorHeaderOriginal = `// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
`

// headerFormProblem checks one ported file's text against the header form
// docs/ported-files.md names for it ("mgo" or "apache") and returns what is
// wrong, or "". The form is per file: the apache header is a prefix of the mgo
// one, so accepting either would let a file drop its mgo lines.
func headerFormProblem(sourceText, form string) string {
	var wantHeader string
	switch form {
	case "mgo":
		wantHeader = vendorHeader
	case "apache":
		wantHeader = vendorHeaderOriginal
	default:
		return fmt.Sprintf("header form %q is neither mgo nor apache", form)
	}
	if !strings.HasPrefix(sourceText, wantHeader) {
		return fmt.Sprintf("does not begin with the %s vendor header lines unchanged", form)
	}
	afterHeader, _, _ := strings.Cut(strings.TrimPrefix(sourceText, wantHeader), "\n")
	if !modifiedLine.MatchString(afterHeader) {
		return fmt.Sprintf("line after the %s vendor header is %q, want a %q line", form, afterHeader, "// Modified by hoardDB, <date>: <summary>")
	}
	return ""
}

// headerFormProblemAt reads a file and applies headerFormProblem.
func headerFormProblemAt(t *testing.T, absolutePath, form string) string {
	t.Helper()
	fileBytes, err := os.ReadFile(absolutePath)
	if err != nil {
		t.Fatalf("reading %s: %v", absolutePath, err)
	}
	return headerFormProblem(string(fileBytes), form)
}

// skippedDirs are never part of the repository's own tree.
var skippedDirs = map[string]bool{".git": true, ".worktrees": true, ".ci-tools": true}

// repoRoot is two levels above this package's directory, where go test runs.
func repoRoot(t *testing.T) string {
	t.Helper()
	rootPath, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}
	return rootPath
}

func readRepoFile(t *testing.T, relativePath string) string {
	t.Helper()
	fileBytes, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(relativePath)))
	if err != nil {
		t.Fatalf("reading %s: %v", relativePath, err)
	}
	return string(fileBytes)
}

// walkRepo calls visit for every file and directory of the repository's own
// tree, with a slash-separated path relative to the root.
func walkRepo(t *testing.T, visit func(relativePath string, entry fs.DirEntry)) {
	t.Helper()
	rootPath := repoRoot(t)
	walkErr := filepath.WalkDir(rootPath, func(entryPath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && skippedDirs[entry.Name()] {
			return filepath.SkipDir
		}
		relativePath, relErr := filepath.Rel(rootPath, entryPath)
		if relErr != nil {
			return relErr
		}
		if relativePath != "." {
			visit(filepath.ToSlash(relativePath), entry)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking the repository: %v", walkErr)
	}
}

func goFiles(t *testing.T) []string {
	t.Helper()
	var goFilePaths []string
	walkRepo(t, func(relativePath string, entry fs.DirEntry) {
		if !entry.IsDir() && strings.HasSuffix(relativePath, ".go") {
			goFilePaths = append(goFilePaths, relativePath)
		}
	})
	return goFilePaths
}

func TestLicenceIsApache2(t *testing.T) {
	licenceText := readRepoFile(t, "LICENSE")
	licenceHash := sha256.Sum256([]byte(licenceText))
	if gotHash := hex.EncodeToString(licenceHash[:]); gotHash != apache2SHA256 {
		t.Fatalf("LICENSE SHA-256 = %s, want %s (the canonical Apache-2.0 text)", gotHash, apache2SHA256)
	}
}

func TestNoticeStatesOriginOnly(t *testing.T) {
	noticeFile := readRepoFile(t, "NOTICE")
	if strings.TrimSuffix(noticeFile, "\n") != noticeText {
		t.Fatalf("NOTICE = %q, want exactly %q (one optional trailing newline)", noticeFile, noticeText)
	}
}

func TestThirdPartyNoticesCarryMgo(t *testing.T) {
	notices := readRepoFile(t, "THIRD-PARTY-NOTICES")
	for _, wantFragment := range []string{
		"License notice for gopkg.in/mgo.v2/bson",
		"Copyright (c) 2010-2013 - Gustavo Niemeyer",
		"Redistributions in binary form must reproduce the above copyright notice",
	} {
		if !strings.Contains(notices, wantFragment) {
			t.Errorf("THIRD-PARTY-NOTICES lacks %q", wantFragment)
		}
	}
}

func TestGoDirectiveIs123(t *testing.T) {
	moduleFile := readRepoFile(t, "go.mod")
	var goDirectives []string
	scanner := bufio.NewScanner(strings.NewReader(moduleFile))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "require") {
			t.Errorf("go.mod has a require directive (%q); the library has no dependencies", line)
		}
		if strings.HasPrefix(line, "go ") {
			goDirectives = append(goDirectives, line)
		}
	}
	if len(goDirectives) != 1 || goDirectives[0] != "go 1.23" {
		t.Errorf("go.mod go directives = %q, want exactly [\"go 1.23\"]", goDirectives)
	}
}

// portedFiles returns the library paths listed in docs/ported-files.md, taken
// from the first backticked cell of each table row, with the header form
// named in the row's last cell.
func portedFiles(t *testing.T) map[string]string {
	t.Helper()
	listing := readRepoFile(t, "docs/ported-files.md")
	firstCell := regexp.MustCompile("^\\|\\s*`([^`]+)`")
	listed := map[string]string{}
	for _, line := range strings.Split(listing, "\n") {
		match := firstCell.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		cells := strings.Split(strings.TrimSuffix(strings.TrimSpace(line), "|"), "|")
		listed[match[1]] = strings.TrimSpace(cells[len(cells)-1])
	}
	return listed
}

func TestEveryGoFileCarriesAHeader(t *testing.T) {
	listed := portedFiles(t)
	for _, relativePath := range goFiles(t) {
		sourceText := readRepoFile(t, relativePath)
		form, isPorted := listed[relativePath]
		if !isPorted {
			if !strings.HasPrefix(sourceText, hoarddbHeader) {
				t.Errorf("%s does not start with the hoardDB Apache-2.0 header", relativePath)
			}
			continue
		}
		if problem := headerFormProblem(sourceText, form); problem != "" {
			t.Errorf("ported file %s %s", relativePath, problem)
		}
	}
	for listedPath := range listed {
		if _, err := os.Stat(filepath.Join(repoRoot(t), filepath.FromSlash(listedPath))); err != nil {
			t.Errorf("docs/ported-files.md lists %s, which does not exist: %v", listedPath, err)
		}
	}
}

func hasMark(text string) bool {
	return strings.Contains(strings.ToLower(text), "mongo")
}

func TestNoMarkInAnyNameOrPath(t *testing.T) {
	// The tree must exist for the check to mean anything.
	if _, err := os.Stat(filepath.Join(repoRoot(t), "go.mod")); err != nil {
		t.Fatalf("no go.mod, so there is no module path to check: %v", err)
	}
	walkRepo(t, func(relativePath string, entry fs.DirEntry) {
		if hasMark(relativePath) {
			t.Errorf("path %s contains a mark", relativePath)
		}
		if entry.Name() == "go.mod" {
			for _, line := range strings.Split(readRepoFile(t, relativePath), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "module ") && hasMark(line) {
					t.Errorf("%s: module path contains a mark: %q", relativePath, line)
				}
			}
		}
	})
	for _, relativePath := range goFiles(t) {
		checkGoFileForMarks(t, relativePath)
	}
}

// checkGoFileForMarks flags a mark in a package name, an exported identifier
// or (outside oracle/, which imports the vendor as its oracle, and the
// inventory tool's fixture modules, which are vendor client code by design)
// an import path.
func checkGoFileForMarks(t *testing.T, relativePath string) {
	t.Helper()
	fileSet := token.NewFileSet()
	parsedFile, err := parser.ParseFile(fileSet, filepath.Join(repoRoot(t), filepath.FromSlash(relativePath)), nil, parser.SkipObjectResolution)
	if err != nil {
		t.Errorf("parsing %s: %v", relativePath, err)
		return
	}
	if hasMark(parsedFile.Name.Name) {
		t.Errorf("%s: package name %q contains a mark", relativePath, parsedFile.Name.Name)
	}
	ast.Inspect(parsedFile, func(node ast.Node) bool {
		if identifier, ok := node.(*ast.Ident); ok && identifier.IsExported() && hasMark(identifier.Name) {
			t.Errorf("%s: exported identifier %q contains a mark", relativePath, identifier.Name)
		}
		return true
	})
	if strings.HasPrefix(relativePath, "oracle/") || strings.HasPrefix(relativePath, "tools/inventory/testdata/") {
		return
	}
	for _, importSpec := range parsedFile.Imports {
		importPath, unquoteErr := strconv.Unquote(importSpec.Path.Value)
		if unquoteErr == nil && hasMark(importPath) {
			t.Errorf("%s: import path %q contains a mark", relativePath, importPath)
		}
	}
}

func TestReadmeMakesNoCompatibilityClaim(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	if !strings.Contains(readme, readmeSentence) {
		t.Errorf("README.md lacks the sentence %q", readmeSentence)
	}
	lowerReadme := strings.ToLower(readme)
	for _, banned := range []string{"compatible with", "drop-in", "replacement for", "®", "™"} {
		if strings.Contains(lowerReadme, banned) {
			t.Errorf("README.md contains %q, which reads as a compatibility claim or a mark", banned)
		}
	}
}
