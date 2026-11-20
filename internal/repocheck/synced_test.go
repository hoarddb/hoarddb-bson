// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package repocheck

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const syncedVectorsDir = "testdata/vectors"

// sumLine is one line of sha256sum's output: 64 hex digits, two spaces, then
// the path relative to testdata/vectors. The synced script writes the files
// outside the directory (the lint script, the Go version) as ../../ paths.
var sumLine = regexp.MustCompile(`^([0-9a-f]{64})  (\S.*)$`)

// syncedSums reads testdata/vectors/SHA256SUMS into path -> hash, with the
// path as written (relative to testdata/vectors).
func syncedSums(t *testing.T) map[string]string {
	t.Helper()
	sumsText := readRepoFile(t, syncedVectorsDir+"/SHA256SUMS")
	sums := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(sumsText))
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := scanner.Text()
		match := sumLine.FindStringSubmatch(line)
		if match == nil {
			t.Fatalf("SHA256SUMS line %d is not '<sha256>  <path>': %q", lineNumber, line)
		}
		if _, repeated := sums[match[2]]; repeated {
			t.Errorf("SHA256SUMS lists %s twice", match[2])
		}
		sums[match[2]] = match[1]
	}
	return sums
}

func fileSHA256(t *testing.T, absolutePath string) string {
	t.Helper()
	fileBytes, err := os.ReadFile(absolutePath)
	if err != nil {
		t.Fatalf("reading %s: %v", absolutePath, err)
	}
	fileHash := sha256.Sum256(fileBytes)
	return hex.EncodeToString(fileHash[:])
}

func TestVectorsMatchTheirHashes(t *testing.T) {
	rootPath := repoRoot(t)
	vectorsPath := filepath.Join(rootPath, filepath.FromSlash(syncedVectorsDir))
	sums := syncedSums(t)

	listedPaths := make([]string, 0, len(sums))
	for listedPath := range sums {
		listedPaths = append(listedPaths, listedPath)
	}
	sort.Strings(listedPaths)
	for _, listedPath := range listedPaths {
		absolutePath := filepath.Join(vectorsPath, filepath.FromSlash(listedPath))
		relativeToRoot, err := filepath.Rel(rootPath, absolutePath)
		if err != nil || relativeToRoot == ".." || strings.HasPrefix(relativeToRoot, ".."+string(filepath.Separator)) {
			t.Errorf("SHA256SUMS lists %s, which is outside the repository", listedPath)
			continue
		}
		if gotHash := fileSHA256(t, absolutePath); gotHash != sums[listedPath] {
			t.Errorf("%s: SHA-256 = %s, SHA256SUMS lists %s. a synced file is never edited here (rule 10): run the sync script in a hoardDB checkout, or restore the file with git", listedPath, gotHash, sums[listedPath])
		}
	}

	// A file nobody hashed would be trusted silently.
	walkErr := filepath.WalkDir(vectorsPath, func(entryPath string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() == "SHA256SUMS" {
			return err
		}
		relativePath, relErr := filepath.Rel(vectorsPath, entryPath)
		if relErr != nil {
			return relErr
		}
		if _, listed := sums[filepath.ToSlash(relativePath)]; !listed {
			t.Errorf("%s/%s is not listed in SHA256SUMS", syncedVectorsDir, filepath.ToSlash(relativePath))
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking %s: %v", syncedVectorsDir, walkErr)
	}

	// The two files synced from outside the vectors tree must be listed, or
	// the pin on the linter and the Go version would be unchecked.
	for _, mustList := range []string{"../../scripts/lint-tool.sh", "../../.server-go-version"} {
		if _, listed := sums[mustList]; !listed {
			t.Errorf("SHA256SUMS does not list %s", mustList)
		}
	}
	for _, mustList := range []string{"bson-vectors/v1.json", "driver-vectors/v1.json"} {
		if _, listed := sums[mustList]; !listed {
			t.Errorf("SHA256SUMS does not list %s", mustList)
		}
	}
}
