// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorbsoncore "go.mongodb.org/mongo-driver/x/bsonx/bsoncore"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/x/bsonx/bsoncore"
)

// Inputs the vendor's decoder panics on or never returns from. The library
// must not panic (AGENTS.md slop rule 1) and must return, so each is an error
// in the library. The first is the coordinator's sanctioned divergence; the
// others are reported in docs/build-reports/step-4e.md for a ruling, with
// the library's texts recorded in docs/core-api.md.

type decPanicDeep3 struct{ Z string }

type decPanicDeep2 struct {
	D3 decPanicDeep3 `bson:",inline"`
	Y  string
}

// decPanicShape inlines a struct through a pointer, and that struct inlines
// another by value: the vendor's getInlineField allocates the pointer, then
// calls reflect.Type.Elem on the value struct and panics.
type decPanicShape struct {
	Q *decPanicDeep2 `bson:",inline"`
}

// decInlinePanicText is the library's refusal of decPanicShape's key "z",
// recorded in docs/core-api.md.
const decInlinePanicText = `decoding key "z": the inlined struct oracle.decPanicDeep3 in oracle.decPanicShape is reached through a nil pointer and cannot be allocated; allocate the pointer before decoding`

// decVendorPanicOf runs the vendor's decode and returns its panic, or fails
// t when it does not panic.
func decVendorPanicOf(t *testing.T, label string, vendorCall func() error) string {
	t.Helper()
	outcome := vendorOutcome(vendorCall)
	if !strings.HasPrefix(outcome, "panic: ") {
		t.Fatalf("%s: the vendor no longer panics (%s); this divergence is gone and the library must match it", label, outcome)
	}
	return outcome
}

func TestDecodeInlineThroughNilPointerIsAnErrorNotAPanic(t *testing.T) {
	document := decMarshal(t, vendorbson.D{{Key: "z", Value: "v"}})
	vendorPanic := decVendorPanicOf(t, "inline through a nil pointer", func() error {
		var target decPanicShape
		return vendorbson.Unmarshal(document, &target)
	})
	if !strings.Contains(vendorPanic, "reflect: Elem of invalid type oracle.decPanicDeep3") {
		t.Errorf("the vendor's panic is %q, want the reflect.Type.Elem panic the ruling names", vendorPanic)
	}
	var libraryTarget decPanicShape
	libraryText, libraryErr := decLibraryOutcome(func() error { return decLibraryDecoder.Unmarshal(document, &libraryTarget) })
	if libraryErr == nil || libraryText != decInlinePanicText {
		t.Errorf("the library's outcome is %q, want the error %q", libraryText, decInlinePanicText)
	}
	// Keys the vendor reaches without the panic decode the same on both sides.
	decCheck(t, "the panic shape's other keys", decMarshal(t, vendorbson.D{{Key: "y", Value: "v"}, {Key: "q", Value: "w"}}), decSame("*decPanicShape", decZero[decPanicShape]))
	decCheck(t, "the panic shape prefilled", document, decSame("*decPanicShape prefilled", func() decPanicShape { return decPanicShape{Q: &decPanicDeep2{}} }))
}

type decRawFields struct {
	Raw      vendorbson.Raw
	RawValue vendorbson.RawValue
}

type decLibRawFields struct {
	Raw      bson.Raw
	RawValue bson.RawValue
}

type decCoreArrayField struct{ Arr vendorbsoncore.Array }

type decLibCoreArrayField struct{ Arr bsoncore.Array }

type decBytesHookField struct{ H decRecordedHook }

type decValueHookField struct{ V decVendorValueHook }

type decLibValueHookField struct{ V decLibraryValueHook }

type decPointerToReader struct{ R *io.Reader }

// The documents the child processes decode: a negative skip that lands
// back on its own element, and a pointer to a type with no decoder.
var (
	decSelfLoopDocument = documentOf(elementOf(0x02, "b", int32Of(-7)))
	decPointerDocument  = documentOf(elementOf(0x02, "r", stringValueOf("x")))
)

// decChildEnvironment names the vendor call a re-executed test binary makes
// in TestDecodeVendorChild, so it runs in a process of its own.
const decChildEnvironment = "HOARDDB_DECODE_VENDOR_CHILD"

// decChildMarker starts the line the child prints with its vendor outcome.
const decChildMarker = "VENDOR OUTCOME: "

// TestDecodeVendorChild is the child half of the process tests below. It
// does nothing in a normal run.
func TestDecodeVendorChild(t *testing.T) {
	var outcome string
	switch os.Getenv(decChildEnvironment) {
	case "":
		t.Skip("run by TestDecodeVendorPanicsAreErrors in a child process")
	case "self-loop":
		outcome = vendorOutcome(func() error { return vendorbson.Unmarshal(decSelfLoopDocument, &decDeclaresA{}) })
	case "pointer-first-call":
		outcome = vendorOutcome(func() error { return vendorbson.Unmarshal(decPointerDocument, &decPointerToReader{}) })
	default:
		t.Fatalf("unknown child mode %q", os.Getenv(decChildEnvironment))
	}
	fmt.Println(decChildMarker + outcome)
}

// decRunVendorChild re-executes the test binary to make one vendor call in
// a fresh process, kills it after timeout, and returns its outcome line, or
// whether it was still running when killed.
func decRunVendorChild(t *testing.T, mode string, timeout time.Duration) (outcome string, killed bool) {
	t.Helper()
	childContext, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	child := exec.CommandContext(childContext, os.Args[0], "-test.run=^TestDecodeVendorChild$", "-test.count=1")
	child.Env = append(os.Environ(), decChildEnvironment+"="+mode)
	var stdout bytes.Buffer
	child.Stdout = &stdout
	err := child.Run()
	if childContext.Err() != nil {
		return "", true
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("starting the %s child: %v", mode, err)
	}
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.HasPrefix(line, decChildMarker) {
			return strings.TrimPrefix(line, decChildMarker), false
		}
	}
	t.Fatalf("the %s child printed no outcome (%v): %s", mode, err, stdout.String())
	return "", false
}

// TestDecodeVendorPanicsAreErrors holds the unsanctioned cases, each a vendor
// panic or hang found while building step 4e: the vendor's behaviour is
// asserted, and the library returns the error recorded here instead.
func TestDecodeVendorPanicsAreErrors(t *testing.T) {
	negativeRoot := []byte{0xF0, 0xFF, 0xFF, 0xFF, 0}
	cases := []struct {
		name        string
		document    []byte
		vendor      func() any
		library     func() any
		vendorPanic string
		libraryText string
	}{
		{"a negative root length into bson.Raw", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0},
			func() any { return &vendorbson.Raw{} }, func() any { return &bson.Raw{} },
			"panic: runtime error: slice bounds out of range [:-1]", "invalid length: -1"},
		{"a negative string length into a bson.Raw field", documentOf(elementOf(0x02, "raw", int32Of(-10))),
			func() any { return &decRawFields{} }, func() any { return &decLibRawFields{} },
			"panic: runtime error: slice bounds out of range [9:3]", "error decoding key raw: invalid length: -6"},
		{"a negative string length into a bson.RawValue field", documentOf(elementOf(0x02, "rawvalue", int32Of(-10))),
			func() any { return &decRawFields{} }, func() any { return &decLibRawFields{} },
			"panic: runtime error: slice bounds out of range [14:8]", "error decoding key rawvalue: invalid length: -6"},
		{"a negative array length into a bsoncore.Array field", documentOf(elementOf(0x04, "arr", int32Of(-3))),
			func() any { return &decCoreArrayField{} }, func() any { return &decLibCoreArrayField{} },
			"panic: runtime error: slice bounds out of range [9:6]", "error decoding key arr: invalid length: -3"},
		{"a negative string length into an UnmarshalBSON hook field", documentOf(elementOf(0x02, "h", int32Of(-9))),
			func() any { return &decBytesHookField{} }, func() any { return &decBytesHookField{} },
			"panic: runtime error: slice bounds out of range [7:2]", "error decoding key h: invalid length: -5"},
		{"a negative string length into an UnmarshalBSONValue hook field", documentOf(elementOf(0x02, "v", int32Of(-9))),
			func() any { return &decValueHookField{} }, func() any { return &decLibValueHookField{} },
			"panic: runtime error: slice bounds out of range [7:2]", "error decoding key v: invalid length: -5"},
		{"a negative root length into a top-level UnmarshalBSON hook", negativeRoot,
			func() any { return &decRecordedHook{} }, func() any { return &decRecordedHook{} },
			"panic: runtime error: slice bounds out of range [:-16]", "invalid length: -16"},
		{"a negative root length into a top-level bsoncore.Document", negativeRoot,
			func() any { return &vendorbsoncore.Document{} }, func() any { return &bsoncore.Document{} },
			"panic: runtime error: slice bounds out of range [:-16]", "invalid length: -16"},
		{"a negative skip past the start of the bytes", documentOf(elementOf(0x02, "b", int32Of(-20))),
			func() any { return &decDeclaresA{} }, func() any { return &decDeclaresA{} },
			"panic: runtime error: index out of range [-9]", "document is invalid: a value length moves the read to byte -9, before the start of the bytes"},
	}
	for _, testCase := range cases {
		vendorPanic := decVendorPanicOf(t, testCase.name, func() error { return vendorbson.Unmarshal(testCase.document, testCase.vendor()) })
		if vendorPanic != testCase.vendorPanic {
			t.Errorf("%s: the vendor's panic is %q, want %q", testCase.name, vendorPanic, testCase.vendorPanic)
		}
		libraryText, _ := decLibraryOutcome(func() error { return decLibraryDecoder.Unmarshal(testCase.document, testCase.library()) })
		if libraryText != testCase.libraryText {
			t.Errorf("%s: the library's outcome is %q, want %q", testCase.name, libraryText, testCase.libraryText)
		}
	}
	// A negative skip that lands back on its own element: the vendor reads
	// it again for ever, so a child process making that call is still running
	// when it is killed; the library stops at the repeat.
	if outcome, killed := decRunVendorChild(t, "self-loop", 5*time.Second); !killed {
		t.Errorf("self-loop: the vendor returned %q within 5 seconds; it used to loop for ever, and the library must now match it", outcome)
	}
	libraryDone := make(chan string, 1)
	go func() {
		libraryText, _ := decLibraryOutcome(func() error { return decLibraryDecoder.Unmarshal(decSelfLoopDocument, &decDeclaresA{}) })
		libraryDone <- libraryText
	}()
	select {
	case libraryText := <-libraryDone:
		if want := "document is invalid: a negative value length returns the read to byte 4 again, so the document never ends"; libraryText != want {
			t.Errorf("self-loop: the library's outcome is %q, want %q", libraryText, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("self-loop: the library has not returned after 10 seconds")
	}
	// A pointer to a type with no decoder: the vendor's first decode in a
	// process gives an error, and every later one panics on its cached nil
	// decoder. The library gives the first-call error every time.
	const pointerText = "error decoding key r: no decoder found for io.Reader"
	firstCall, killed := decRunVendorChild(t, "pointer-first-call", 30*time.Second)
	if killed || firstCall != pointerText {
		t.Errorf("pointer to io.Reader: the vendor's first call in a fresh process gave %q (killed: %t), want %q", firstCall, killed, pointerText)
	}
	_ = vendorOutcome(func() error { return vendorbson.Unmarshal(decPointerDocument, &decPointerToReader{}) })
	laterCall := decVendorPanicOf(t, "a pointer to io.Reader, after the first decode", func() error {
		return vendorbson.Unmarshal(decPointerDocument, &decPointerToReader{})
	})
	if want := "panic: interface conversion: interface is nil, not bsoncodec.ValueDecoder"; laterCall != want {
		t.Errorf("pointer to io.Reader, a later call: the vendor's panic is %q, want %q", laterCall, want)
	}
	for attempt := 0; attempt < 3; attempt++ {
		libraryText, _ := decLibraryOutcome(func() error { return decLibraryDecoder.Unmarshal(decPointerDocument, &decPointerToReader{}) })
		if libraryText != pointerText {
			t.Errorf("pointer to io.Reader, attempt %d: the library's outcome is %q, want %q", attempt, libraryText, pointerText)
		}
	}
	// RawValue.Unmarshal into a nil pointer.
	nilPointerPanic := decVendorPanicOf(t, "RawValue.Unmarshal into a nil pointer", func() error {
		return vendorbson.RawValue{Type: 0x02, Value: stringValueOf("s")}.Unmarshal((*string)(nil))
	})
	if want := "panic: reflect: call of reflect.Value.Type on zero Value"; nilPointerPanic != want {
		t.Errorf("RawValue.Unmarshal into a nil pointer: the vendor's panic is %q, want %q", nilPointerPanic, want)
	}
	libraryText, _ := decLibraryOutcome(func() error { return decLibraryDecoder.UnmarshalValue(0x02, stringValueOf("s"), (*string)(nil)) })
	if want := "cannot Decode to nil value"; libraryText != want {
		t.Errorf("UnmarshalValue into a nil pointer: the library's outcome is %q, want %q", libraryText, want)
	}
}

// TestDecodeKnownVendorPanicsAreTheClasses: the harness accepts a vendor
// panic only of a class the library refuses on purpose.
func TestDecodeKnownVendorPanicsAreTheClasses(t *testing.T) {
	for vendorText, want := range map[string]bool{
		"panic: runtime error: slice bounds out of range [9:3]":                     true,
		"panic: runtime error: slice bounds out of range [:-16]":                    true,
		"panic: runtime error: index out of range [-9]":                             true,
		"panic: reflect: Elem of invalid type oracle.decPanicDeep3":                 true,
		"panic: interface conversion: interface is nil, not bsoncodec.ValueDecoder": true,
		"panic: reflect: call of reflect.Value.Type on zero Value":                  true,
		"panic: runtime error: slice bounds out of range [3:9]":                     false,
		"panic: runtime error: slice bounds out of range [:16]":                     false,
		"panic: runtime error: index out of range [5] with length 3":                false,
		"panic: runtime error: invalid memory address or nil pointer dereference":   false,
		"panic: reflect: call of reflect.Value.SetInt on string Value":              false,
	} {
		if got := decIsKnownVendorPanic(vendorText); got != want {
			t.Errorf("%q: known %t, want %t", vendorText, got, want)
		}
	}
}
