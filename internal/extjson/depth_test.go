// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package extjson

import (
	"runtime/debug"
	"strings"
	"testing"
)

// Arrays do not count toward the ceiling, as in the vendor, so their depth
// is bounded only by the text. The byte writer keeps its open containers in
// a slice: with the goroutine stack capped at 256 KiB, 100,000 nested
// arrays are read, where a writer that recursed per level would overflow
// the stack and crash the test binary.
func TestDeepArraysDoNotRecurse(t *testing.T) {
	const depth = 100_000
	text := []byte(`{"a":` + strings.Repeat("[", depth) + strings.Repeat("]", depth) + "}")
	previousLimit := debug.SetMaxStack(256 << 10)
	defer debug.SetMaxStack(previousLimit)
	documentBytes, err := AppendDocument(nil, text, false)
	if err != nil {
		t.Fatal(err)
	}
	// The root's length and terminator, the element "a" (type, key and
	// terminator), each array's length and terminator, and the header of
	// every array but the outermost (type, key "0" and terminator).
	if want := 5 + 3 + depth*5 + (depth-1)*3; len(documentBytes) != want {
		t.Fatalf("%d bytes, want %d", len(documentBytes), want)
	}
}
