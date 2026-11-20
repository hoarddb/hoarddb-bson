// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

// Package oracle holds the module-level checks of the oracle module. The
// differential helpers live in its subpackages.
package oracle

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	// The blank import makes the module build against the library through
	// the replace directive, so a broken replace fails here first.
	_ "github.com/hoarddb/hoarddb-bson/bson"
)

const vendorModulePath = "go.mongodb.org/mongo-driver"

const mgoNoticeTitle = "License notice for gopkg.in/mgo.v2/bson"

// goListModule runs `go list -m -f format module` from the oracle module.
func goListModule(t *testing.T, format, modulePath string) string {
	t.Helper()
	listCommand := exec.Command("go", "list", "-m", "-f", format, modulePath)
	listCommand.Env = append(os.Environ(), "GOWORK=off")
	listOutput, err := listCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -m -f %q %s: %v\n%s", format, modulePath, err, listOutput)
	}
	return strings.TrimSpace(string(listOutput))
}

// isSeparator reports whether line is one of the dashed rules that
// THIRD-PARTY-NOTICES puts around each notice title.
func isSeparator(line []byte) bool {
	return len(line) > 0 && len(bytes.Trim(line, "-")) == 0
}

// mgoNoticeBlock returns the mgo notice of a THIRD-PARTY-NOTICES file: from
// the rule above its title to its last non-blank line before the next rule
// (or the end of the file). The blank lines before the next rule are padding
// between notices, not part of the notice.
func mgoNoticeBlock(t *testing.T, noticesPath string) []byte {
	t.Helper()
	noticesText, err := os.ReadFile(noticesPath)
	if err != nil {
		t.Fatalf("reading %s: %v", noticesPath, err)
	}
	lines := bytes.SplitAfter(noticesText, []byte("\n"))
	titleIndex := -1
	for lineIndex, line := range lines {
		if string(bytes.TrimRight(line, "\n")) == mgoNoticeTitle {
			titleIndex = lineIndex
			break
		}
	}
	if titleIndex < 1 || !isSeparator(bytes.TrimRight(lines[titleIndex-1], "\n")) {
		t.Fatalf("%s has no %q title under a dashed rule", noticesPath, mgoNoticeTitle)
	}
	// Skip the rule under the title, then stop at the next rule.
	endIndex := len(lines)
	for lineIndex := titleIndex + 2; lineIndex < len(lines); lineIndex++ {
		if isSeparator(bytes.TrimRight(lines[lineIndex], "\n")) {
			endIndex = lineIndex
			break
		}
	}
	block := bytes.Join(lines[titleIndex-1:endIndex], nil)
	return append(bytes.TrimRight(block, "\n"), '\n')
}

func TestThirdPartyNoticesMatchTheVendorVerbatim(t *testing.T) {
	vendorDir := goListModule(t, "{{.Dir}}", vendorModulePath)
	if vendorDir == "" {
		t.Fatalf("go list -m found no source directory for %s", vendorModulePath)
	}
	vendorBlock := mgoNoticeBlock(t, filepath.Join(vendorDir, "THIRD-PARTY-NOTICES"))
	libraryBlock := mgoNoticeBlock(t, filepath.Join("..", "THIRD-PARTY-NOTICES"))
	if !bytes.Equal(libraryBlock, vendorBlock) {
		t.Fatalf("the library's mgo notice differs from the vendor's (%s).\nlibrary:\n%s\nvendor:\n%s",
			vendorDir, libraryBlock, vendorBlock)
	}
}

// TestOracleUsesTheLocalLibrary guards spec §9.1: the oracle module tests the
// library in the same checkout, through `replace ../`, never a fetched copy.
func TestOracleUsesTheLocalLibrary(t *testing.T) {
	if replacement := goListModule(t, "{{with .Replace}}{{.Path}}{{end}}", "github.com/hoarddb/hoarddb-bson"); replacement != "../" {
		t.Fatalf("github.com/hoarddb/hoarddb-bson is replaced by %q, want \"../\"", replacement)
	}
}
