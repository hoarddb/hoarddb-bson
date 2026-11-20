// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson_test

import (
	"bytes"
	"errors"
	"math"
	"testing"

	"github.com/hoarddb/hoarddb-bson/bson"
)

func TestBuilderRefusesMisuse(t *testing.T) {
	misuses := []struct {
		name  string
		build func(builder *bson.Builder)
		want  error
	}{
		{"key with a zero byte", func(builder *bson.Builder) { builder.AppendInt32("a\x00b", 1) }, bson.ErrInvalidKey},
		{"StartDocument key with a zero byte", func(builder *bson.Builder) { builder.StartDocument("\x00") }, bson.ErrInvalidKey},
		{"a key inside an array", func(builder *bson.Builder) {
			builder.StartArray("list")
			builder.AppendString("0", "x")
			builder.End()
		}, bson.ErrInvalidKey},
		{"End with no level open", func(builder *bson.Builder) { builder.End() }, bson.ErrUnbalancedBuilder},
		{"Document with a level open", func(builder *bson.Builder) { builder.StartDocument("open") }, bson.ErrUnbalancedBuilder},
		{"a zero Document appended", func(builder *bson.Builder) { builder.AppendDocument("empty", bson.Document{}) },
			bson.ErrMalformedDocument},
	}
	for _, misuse := range misuses {
		t.Run(misuse.name, func(t *testing.T) {
			var builder bson.Builder
			misuse.build(&builder)
			document, err := builder.Document()
			if !errors.Is(err, misuse.want) {
				t.Fatalf("Document gave %v, want an error matching %v", err, misuse.want)
			}
			if document.Bytes() != nil {
				t.Fatalf("a refused build returned bytes % x", document.Bytes())
			}
			// Document resets the builder, the error included.
			if next, err := builder.Document(); err != nil || !bytes.Equal(next.Bytes(), []byte{5, 0, 0, 0, 0}) {
				t.Fatalf("after a refusal the builder gave % x, %v, want an empty document", next.Bytes(), err)
			}
		})
	}
}

// TestBuilderKeepsTheFirstError checks that a later call neither clears the
// first error nor writes past it.
func TestBuilderKeepsTheFirstError(t *testing.T) {
	var builder bson.Builder
	builder.End()
	builder.AppendInt32("a\x00", 1)
	builder.AppendString("fine", "value")
	_, err := builder.Document()
	if !errors.Is(err, bson.ErrUnbalancedBuilder) || errors.Is(err, bson.ErrInvalidKey) {
		t.Fatalf("got %v, want only the first error, ErrUnbalancedBuilder", err)
	}
}

func TestBuilderRefusesALengthPastInt32(t *testing.T) {
	if err := bson.CheckBuiltLength(math.MaxInt32); err != nil {
		t.Fatalf("the largest int32 length was refused: %v", err)
	}
	if err := bson.CheckBuiltLength(int64(math.MaxInt32) + 1); !errors.Is(err, bson.ErrDocumentTooLarge) {
		t.Fatalf("a length past int32 gave %v, want ErrDocumentTooLarge", err)
	}
}

// TestBuilderDocumentDoesNotAliasTheNextBuild holds Document's promise that
// the returned bytes are never written again.
func TestBuilderDocumentDoesNotAliasTheNextBuild(t *testing.T) {
	var builder bson.Builder
	builder.AppendInt32("first", 1)
	first, err := builder.Document()
	if err != nil {
		t.Fatal(err)
	}
	firstBytes := append([]byte(nil), first.Bytes()...)
	builder.AppendInt64("second, longer", 2)
	if _, err := builder.Document(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), firstBytes) {
		t.Fatalf("the second build wrote into the first document: % x", first.Bytes())
	}
}
