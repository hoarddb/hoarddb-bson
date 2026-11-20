// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/benchdata"
)

// The Go-value rows of the Extended JSON benchmarks (spec 9.2, "Extended
// JSON encode and decode"), informational for step 5: Ref450 as the struct,
// as a map (the CLI's documents.go writes a map) and as a bson.D, written
// in relaxed mode, the CLI's; and the same text read into a map (the CLI's
// extjson_input.go), a bson.D (its ingest) and the struct.

// BenchmarkMarshalExtJSONGoValues writes Ref450 from a Go value.
func BenchmarkMarshalExtJSONGoValues(b *testing.B) {
	record, libRecord := benchdata.Ref450Record(), benchdata.Ref450LibRecord()
	var vendorMap map[string]any
	ref450, err := benchdata.Ref450Bytes()
	if err != nil {
		b.Fatal(err)
	}
	if err := vendorbson.Unmarshal(ref450, &vendorMap); err != nil {
		b.Fatal(err)
	}
	vendorDocument := benchdata.Ref450D()
	for _, shape := range []struct {
		name        string
		lib, vendor any
	}{
		{"shape=struct", libRecord, record},
		{"shape=map", libraryTwin(b, vendorMap), vendorMap},
		{"shape=D", libraryTwin(b, vendorDocument), vendorDocument},
	} {
		if divergence := extJSONWriteDivergence(twinValues(shape.name, shape.lib, shape.vendor), extJSONMode{}); divergence != "" {
			b.Fatalf("the benchmark input diverges, %s: %s", shape.name, divergence)
		}
		b.Run(shape.name+"/lib=hoarddb", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := bson.MarshalExtJSON(shape.lib, false, false); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(shape.name+"/lib=vendor", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := vendorbson.MarshalExtJSON(shape.vendor, false, false); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkUnmarshalExtJSONGoValues reads Ref450's relaxed text into a Go
// value.
func BenchmarkUnmarshalExtJSONGoValues(b *testing.B) {
	ref450, err := benchdata.Ref450Bytes()
	if err != nil {
		b.Fatal(err)
	}
	relaxedText, err := vendorbson.MarshalExtJSON(vendorbson.Raw(ref450), false, false)
	if err != nil {
		b.Fatal(err)
	}
	for _, target := range []decTarget{
		decTargetMap, decTargetD,
		{"Ref450 struct", func() any { return &benchdata.Record{} }, func() any { return &benchdata.LibRecord{} }},
	} {
		if divergence := extJSONReadDivergence(relaxedText, false, target); divergence != "" {
			b.Fatalf("the benchmark input diverges into %s: %s", target.name, divergence)
		}
		b.Run("target="+target.name+"/lib=hoarddb", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(relaxedText)))
			for range b.N {
				if err := bson.UnmarshalExtJSON(relaxedText, false, target.library()); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("target="+target.name+"/lib=vendor", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(relaxedText)))
			for range b.N {
				if err := vendorbson.UnmarshalExtJSON(relaxedText, false, target.vendor()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
