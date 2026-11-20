// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"bytes"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorprimitive "go.mongodb.org/mongo-driver/bson/primitive"
	vendorbsoncore "go.mongodb.org/mongo-driver/x/bsonx/bsoncore"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/benchdata"
)

// buildRef450 builds Ref450 element by element with the library's builder.
func buildRef450(builder *bson.Builder) (bson.Document, error) {
	record := benchdata.Ref450Record()
	builder.AppendObjectID("_id", primitive.ObjectID(record.ID))
	builder.AppendString("name", record.Name)
	builder.AppendString("email", record.Email)
	builder.AppendString("notes", record.Notes)
	builder.AppendInt32("age", record.Age)
	builder.AppendDouble("score", record.Score)
	builder.AppendBoolean("active", record.Active)
	builder.AppendDateTime("created", int64(record.Created))
	builder.StartArray("tags")
	for _, tag := range record.Tags {
		builder.AppendString("", tag)
	}
	builder.End()
	builder.StartDocument("address")
	builder.AppendString("street", record.Address.Street)
	builder.AppendString("city", record.Address.City)
	builder.AppendString("zip", record.Address.Zip)
	builder.AppendString("country", record.Address.Country)
	builder.End()
	return builder.Document()
}

// vendorBuildRef450 is the same document through the vendor's
// BuildDocument and Append*Element functions.
func vendorBuildRef450() []byte {
	record := benchdata.Ref450Record()
	tags := vendorbsoncore.BuildArray(nil,
		vendorbsoncore.Value{Type: vendorbson.TypeString, Data: stringValueOf(record.Tags[0])},
		vendorbsoncore.Value{Type: vendorbson.TypeString, Data: stringValueOf(record.Tags[1])},
		vendorbsoncore.Value{Type: vendorbson.TypeString, Data: stringValueOf(record.Tags[2])})
	address := vendorbsoncore.BuildDocument(nil,
		vendorbsoncore.AppendStringElement(nil, "street", record.Address.Street),
		vendorbsoncore.AppendStringElement(nil, "city", record.Address.City),
		vendorbsoncore.AppendStringElement(nil, "zip", record.Address.Zip),
		vendorbsoncore.AppendStringElement(nil, "country", record.Address.Country))
	return vendorbsoncore.BuildDocument(nil,
		vendorbsoncore.AppendObjectIDElement(nil, "_id", record.ID),
		vendorbsoncore.AppendStringElement(nil, "name", record.Name),
		vendorbsoncore.AppendStringElement(nil, "email", record.Email),
		vendorbsoncore.AppendStringElement(nil, "notes", record.Notes),
		vendorbsoncore.AppendInt32Element(nil, "age", record.Age),
		vendorbsoncore.AppendDoubleElement(nil, "score", record.Score),
		vendorbsoncore.AppendBooleanElement(nil, "active", record.Active),
		vendorbsoncore.AppendDateTimeElement(nil, "created", int64(record.Created)),
		vendorbsoncore.AppendArrayElement(nil, "tags", tags),
		vendorbsoncore.AppendDocumentElement(nil, "address", address))
}

type builderCase struct {
	name    string
	library func(builder *bson.Builder) (bson.Document, error)
	vendor  func() []byte
}

func builderCases(t *testing.T) []builderCase {
	storedInner, err := bson.Validate(documentOf(elementOf(0x10, "inner", int32Of(9))), bson.Limits{Depth: 2})
	if err != nil {
		t.Fatalf("validating the appended document: %v", err)
	}
	longKey := strings.Repeat("k", 1<<16)
	return []builderCase{
		{"empty document",
			func(builder *bson.Builder) (bson.Document, error) { return builder.Document() },
			func() []byte { return vendorbsoncore.BuildDocument(nil) }},
		{"every element method, edge values",
			func(builder *bson.Builder) (bson.Document, error) {
				builder.AppendDouble("nan", math.NaN())
				builder.AppendDouble("negative zero", math.Copysign(0, -1))
				builder.AppendDouble("max", math.MaxFloat64)
				builder.AppendString("", "")
				builder.AppendString("zero inside", "a\x00b")
				builder.AppendString(longKey, strings.Repeat("v", 1<<16))
				builder.AppendBinary("generic", 0x00, nil)
				builder.AppendBinary("old", 0x02, []byte{1, 2, 3})
				builder.AppendObjectID("id", primitive.ObjectID{})
				builder.AppendBoolean("false", false)
				builder.AppendBoolean("true", true)
				builder.AppendDateTime("datetime min", math.MinInt64)
				builder.AppendNull("null")
				builder.AppendInt32("int32 min", math.MinInt32)
				builder.AppendInt32("int32 max", math.MaxInt32)
				builder.AppendInt64("int64 min", math.MinInt64)
				builder.AppendInt64("int64 max", math.MaxInt64)
				builder.AppendDocument("stored", storedInner)
				return builder.Document()
			},
			func() []byte {
				return vendorbsoncore.BuildDocument(nil,
					vendorbsoncore.AppendDoubleElement(nil, "nan", math.NaN()),
					vendorbsoncore.AppendDoubleElement(nil, "negative zero", math.Copysign(0, -1)),
					vendorbsoncore.AppendDoubleElement(nil, "max", math.MaxFloat64),
					vendorbsoncore.AppendStringElement(nil, "", ""),
					vendorbsoncore.AppendStringElement(nil, "zero inside", "a\x00b"),
					vendorbsoncore.AppendStringElement(nil, longKey, strings.Repeat("v", 1<<16)),
					vendorbsoncore.AppendBinaryElement(nil, "generic", 0x00, nil),
					vendorbsoncore.AppendBinaryElement(nil, "old", 0x02, []byte{1, 2, 3}),
					vendorbsoncore.AppendObjectIDElement(nil, "id", vendorprimitive.ObjectID{}),
					vendorbsoncore.AppendBooleanElement(nil, "false", false),
					vendorbsoncore.AppendBooleanElement(nil, "true", true),
					vendorbsoncore.AppendDateTimeElement(nil, "datetime min", math.MinInt64),
					vendorbsoncore.AppendNullElement(nil, "null"),
					vendorbsoncore.AppendInt32Element(nil, "int32 min", math.MinInt32),
					vendorbsoncore.AppendInt32Element(nil, "int32 max", math.MaxInt32),
					vendorbsoncore.AppendInt64Element(nil, "int64 min", math.MinInt64),
					vendorbsoncore.AppendInt64Element(nil, "int64 max", math.MaxInt64),
					vendorbsoncore.AppendDocumentElement(nil, "stored", storedInner.Bytes()))
			}},
		{"nested documents three levels deep",
			func(builder *bson.Builder) (bson.Document, error) {
				builder.AppendString("level", "one")
				builder.StartDocument("two")
				builder.AppendInt32("level", 2)
				builder.StartDocument("three")
				builder.AppendInt64("level", 3)
				builder.End()
				builder.StartDocument("empty")
				builder.End()
				builder.End()
				builder.AppendBoolean("after", true)
				return builder.Document()
			},
			func() []byte {
				three := vendorbsoncore.BuildDocument(nil, vendorbsoncore.AppendInt64Element(nil, "level", 3))
				two := vendorbsoncore.BuildDocument(nil, vendorbsoncore.AppendInt32Element(nil, "level", 2),
					vendorbsoncore.AppendDocumentElement(nil, "three", three),
					vendorbsoncore.AppendDocumentElement(nil, "empty", vendorbsoncore.BuildDocument(nil)))
				return vendorbsoncore.BuildDocument(nil, vendorbsoncore.AppendStringElement(nil, "level", "one"),
					vendorbsoncore.AppendDocumentElement(nil, "two", two),
					vendorbsoncore.AppendBooleanElement(nil, "after", true))
			}},
		{"arrays: twelve positions, a document and an array inside",
			func(builder *bson.Builder) (bson.Document, error) {
				builder.StartArray("list")
				for position := range 10 {
					builder.AppendInt32("", int32(position))
				}
				builder.StartDocument("")
				builder.AppendString("in", "document")
				builder.End()
				builder.StartArray("")
				builder.AppendNull("")
				builder.End()
				builder.End()
				builder.StartArray("empty")
				builder.End()
				return builder.Document()
			},
			func() []byte {
				var values []vendorbsoncore.Value
				for position := range 10 {
					values = append(values, vendorbsoncore.Value{Type: vendorbson.TypeInt32, Data: int32Of(int32(position))})
				}
				values = append(values,
					vendorbsoncore.Value{Type: vendorbson.TypeEmbeddedDocument,
						Data: vendorbsoncore.BuildDocument(nil, vendorbsoncore.AppendStringElement(nil, "in", "document"))},
					vendorbsoncore.Value{Type: vendorbson.TypeArray,
						Data: vendorbsoncore.BuildArray(nil, vendorbsoncore.Value{Type: vendorbson.TypeNull})})
				return vendorbsoncore.BuildDocument(nil,
					vendorbsoncore.AppendArrayElement(nil, "list", vendorbsoncore.BuildArray(nil, values...)),
					vendorbsoncore.AppendArrayElement(nil, "empty", vendorbsoncore.BuildArray(nil)))
			}},
		{"ref450", buildRef450, vendorBuildRef450},
	}
}

func TestBuilderMatchesBuildDocument(t *testing.T) {
	var reusedBuilder bson.Builder
	for _, buildCase := range builderCases(t) {
		t.Run(buildCase.name, func(t *testing.T) {
			var builder bson.Builder
			document, err := buildCase.library(&builder)
			if err != nil {
				t.Fatalf("Document: %v", err)
			}
			want := buildCase.vendor()
			if !bytes.Equal(document.Bytes(), want) {
				t.Fatalf("builder bytes differ from BuildDocument's\n builder % x\n  vendor % x", document.Bytes(), want)
			}
			if _, err := bson.Validate(document.Bytes(), bson.Limits{Depth: bson.MaxNestingCeiling}); err != nil {
				t.Fatalf("the builder's document does not pass Validate: %v", err)
			}
			// One Builder used for every case in turn: Document resets it,
			// and an earlier document's bytes must not change.
			reused, err := buildCase.library(&reusedBuilder)
			if err != nil || !bytes.Equal(reused.Bytes(), want) {
				t.Fatalf("a reused builder gave % x, %v", reused.Bytes(), err)
			}
			if !bytes.Equal(document.Bytes(), want) {
				t.Fatal("building again changed the first document's bytes")
			}
		})
	}
	ref450, err := benchdata.Ref450Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(vendorBuildRef450(), ref450) {
		t.Fatal("vendorBuildRef450 is not Ref450's encoding; the ref450 case does not test Ref450")
	}
}

// TestBuilderDocumentSkipsValidation proves Builder.Document makes no
// validation pass: a Document whose framing is right and whose interior is
// not (accepted by TrustStored, which does not walk) is appended verbatim,
// as the vendor's AppendDocumentElement would append it, and Document
// still succeeds although Validate refuses the result.
func TestBuilderDocumentSkipsValidation(t *testing.T) {
	malformedInterior := documentOf(elementOf(0x08, "flag", []byte{0x02}))
	stored, err := bson.TrustStored(malformedInterior)
	if err != nil {
		t.Fatalf("TrustStored refused the framing: %v", err)
	}
	var builder bson.Builder
	builder.AppendDocument("stored", stored)
	document, err := builder.Document()
	if err != nil {
		t.Fatalf("Document walked the appended document: %v", err)
	}
	want := vendorbsoncore.BuildDocument(nil, vendorbsoncore.AppendDocumentElement(nil, "stored", malformedInterior))
	if !bytes.Equal(document.Bytes(), want) {
		t.Fatalf("got % x, want % x", document.Bytes(), want)
	}
	if _, err := bson.Validate(document.Bytes(), bson.Limits{Depth: 3}); !errors.Is(err, bson.ErrMalformedDocument) {
		t.Fatalf("Validate gave %v, want the boolean refusal that shows the interior was not checked", err)
	}
}

// TestBuilderArrayKeysAreThePositions checks the positions past 9, where a
// one-digit shortcut would go wrong.
func TestBuilderArrayKeysAreThePositions(t *testing.T) {
	var builder bson.Builder
	builder.StartArray("a")
	var values []vendorbsoncore.Value
	for position := range 111 {
		builder.AppendInt64("", int64(position))
		values = append(values, vendorbsoncore.Value{Type: vendorbson.TypeInt64, Data: binaryLittleEndian64(uint64(position))})
	}
	builder.End()
	document, err := builder.Document()
	if err != nil {
		t.Fatal(err)
	}
	want := vendorbsoncore.BuildDocument(nil, vendorbsoncore.AppendArrayElement(nil, "a", vendorbsoncore.BuildArray(nil, values...)))
	if !bytes.Equal(document.Bytes(), want) {
		t.Fatalf("array of 111 values differs (last key %q)", strconv.Itoa(110))
	}
}
