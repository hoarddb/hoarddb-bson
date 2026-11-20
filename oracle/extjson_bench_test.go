// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"

	"github.com/hoarddb/hoarddb-bson/internal/extjson"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/benchdata"
)

// BenchmarkExtJSONDecode reads Ref450 written as relaxed Extended JSON, the
// mode the CLI reads, into BSON bytes: lib=hoarddb with the core's
// AppendDocument, lib=vendor with UnmarshalExtJSON into a bson.Raw. Both
// produce the same bytes (TestUnmarshalExtJSONMatchesVendorOnTheCorpus
// covers the forms; this input is checked below before timing).
func BenchmarkExtJSONDecode(b *testing.B) {
	ref450, err := benchdata.Ref450Bytes()
	if err != nil {
		b.Fatal(err)
	}
	relaxedText, err := vendorbson.MarshalExtJSON(vendorbson.Raw(ref450), false, false)
	if err != nil {
		b.Fatal(err)
	}
	if divergence := extJSONDivergence(relaxedText, false); divergence != "" {
		b.Fatalf("the benchmark input diverges: %s", divergence)
	}
	b.Run("lib=hoarddb", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(relaxedText)))
		for range b.N {
			if _, err := extjson.AppendDocument(nil, relaxedText, false); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("lib=vendor", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(relaxedText)))
		for range b.N {
			var rawTarget vendorbson.Raw
			if err := vendorbson.UnmarshalExtJSON(relaxedText, false, &rawTarget); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkExtJSONEncode writes Ref450 as Extended JSON from its BSON
// bytes, in both modes: lib=hoarddb with the core's AppendText, lib=vendor
// with MarshalExtJSON of a bson.Raw. The text is checked to be the same
// before timing.
func BenchmarkExtJSONEncode(b *testing.B) {
	ref450, err := benchdata.Ref450Bytes()
	if err != nil {
		b.Fatal(err)
	}
	for _, mode := range []struct {
		name      string
		canonical bool
	}{{"mode=relaxed", false}, {"mode=canonical", true}} {
		if divergence := writeDivergence(ref450, mode.canonical, false); divergence != "" {
			b.Fatalf("the benchmark input diverges, %s: %s", mode.name, divergence)
		}
		b.Run(mode.name+"/lib=hoarddb", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(ref450)))
			for range b.N {
				if _, err := extjson.AppendText(nil, ref450, mode.canonical, false); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(mode.name+"/lib=vendor", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(ref450)))
			for range b.N {
				if _, err := vendorbson.MarshalExtJSON(vendorbson.Raw(ref450), mode.canonical, false); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
