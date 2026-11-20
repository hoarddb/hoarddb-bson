// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// portedRow is one row of docs/ported-files.md: the library path and the
// vendor path, both backticked.
var portedRow = regexp.MustCompile("^\\|\\s*`([^`]+)`\\s*\\|\\s*`([^`]+)`")

// leadingComment returns the lines of sourceText before its first line that
// is not a // comment: the file header.
func leadingComment(sourceText string) []string {
	var headerLines []string
	for _, line := range strings.Split(sourceText, "\n") {
		if !strings.HasPrefix(line, "//") {
			break
		}
		headerLines = append(headerLines, line)
	}
	return headerLines
}

// TestPortedFilesStartWithTheVendorFilesHeader compares each ported file's
// header with the header of the vendor file it names, read from the module
// cache, so the header is checked against its source and not against a copy.
func TestPortedFilesStartWithTheVendorFilesHeader(t *testing.T) {
	listing, err := os.ReadFile(filepath.Join("..", "docs", "ported-files.md"))
	if err != nil {
		t.Fatalf("reading docs/ported-files.md: %v", err)
	}
	vendorDir := goListModule(t, "{{.Dir}}", vendorModulePath)
	rowCount := 0
	for _, line := range strings.Split(string(listing), "\n") {
		match := portedRow.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		rowCount++
		libraryPath, vendorPath := match[1], match[2]
		cells := strings.Split(strings.TrimSuffix(strings.TrimSpace(line), "|"), "|")
		listedForm := strings.TrimSpace(cells[len(cells)-1])
		vendorSource, err := os.ReadFile(filepath.Join(vendorDir, filepath.FromSlash(vendorPath)))
		if err != nil {
			t.Errorf("%s names vendor file %s, which cannot be read: %v", libraryPath, vendorPath, err)
			continue
		}
		librarySource, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(libraryPath)))
		if err != nil {
			t.Errorf("reading ported file %s: %v", libraryPath, err)
			continue
		}
		vendorHeader := leadingComment(string(vendorSource))
		libraryHeader := leadingComment(string(librarySource))
		if len(vendorHeader) == 0 {
			t.Errorf("vendor file %s has no header comment", vendorPath)
			continue
		}
		// The column names the form of the vendor source itself, so a wrong
		// entry cannot make repocheck demand the wrong lines.
		sourceForm := "apache"
		if strings.Contains(strings.Join(vendorHeader, "\n"), "Based on gopkg.in/mgo.v2/bson") {
			sourceForm = "mgo"
		}
		if listedForm != sourceForm {
			t.Errorf("%s: docs/ported-files.md names header form %q, the vendor file %s has the %q form", libraryPath, listedForm, vendorPath, sourceForm)
		}
		if len(libraryHeader) != len(vendorHeader)+1 ||
			strings.Join(libraryHeader[:len(vendorHeader)], "\n") != strings.Join(vendorHeader, "\n") {
			t.Errorf("%s header:\n%s\nwant the header of %s unchanged, then one Modified line:\n%s",
				libraryPath, strings.Join(libraryHeader, "\n"), vendorPath, strings.Join(vendorHeader, "\n"))
			continue
		}
		if !strings.HasPrefix(libraryHeader[len(vendorHeader)], "// Modified by hoardDB, ") {
			t.Errorf("%s: the line after the vendor header is %q, want a Modified by hoardDB line",
				libraryPath, libraryHeader[len(vendorHeader)])
		}
	}
	if rowCount == 0 {
		t.Fatalf("docs/ported-files.md has no ported-file row")
	}
}
