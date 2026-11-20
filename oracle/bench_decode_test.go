// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"

	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/benchdata"
)

// Step 4e's benchmark rows (spec 9.2): struct unmarshal and unmarshal to a
// map, on Ref450, through bson.Unmarshal (decLibraryDecoder, step 4f)
// against the vendor's bson.Unmarshal.

// decBenchRecord is benchdata.Record in the library's types.
type decBenchRecord struct {
	ID      primitive.ObjectID `bson:"_id"`
	Name    string             `bson:"name"`
	Email   string             `bson:"email"`
	Notes   string             `bson:"notes"`
	Age     int32              `bson:"age"`
	Score   float64            `bson:"score"`
	Active  bool               `bson:"active"`
	Created primitive.DateTime `bson:"created"`
	Tags    []string           `bson:"tags"`
	Address benchdata.Address  `bson:"address"`
}

func decRef450(tb testing.TB) []byte {
	tb.Helper()
	ref450, err := benchdata.Ref450Bytes()
	if err != nil {
		tb.Fatal(err)
	}
	return ref450
}

func BenchmarkStructUnmarshal(b *testing.B) {
	ref450 := decRef450(b)
	b.Run("lib=hoarddb", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(ref450)))
		for range b.N {
			var record decBenchRecord
			if err := decLibraryDecoder.Unmarshal(ref450, &record); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("lib=vendor", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(ref450)))
		for range b.N {
			var record benchdata.Record
			if err := vendorbson.Unmarshal(ref450, &record); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkUnmarshalToMap(b *testing.B) {
	ref450 := decRef450(b)
	b.Run("lib=hoarddb", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(ref450)))
		for range b.N {
			var document map[string]any
			if err := decLibraryDecoder.Unmarshal(ref450, &document); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("lib=vendor", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(ref450)))
		for range b.N {
			var document map[string]any
			if err := vendorbson.Unmarshal(ref450, &document); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestDecodeAllocsAtMostVendor is spec 9.2's allocation gate for the decode
// rows: AllocsPerRun of the library's decode of Ref450, into the struct and
// into a map, is at most the vendor's on the same input, measured here.
func TestDecodeAllocsAtMostVendor(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector changes allocation counts")
	}
	ref450 := decRef450(t)
	rows := []struct {
		name    string
		library func() error
		vendor  func() error
	}{
		{"struct", func() error { var record decBenchRecord; return decLibraryDecoder.Unmarshal(ref450, &record) },
			func() error { var record benchdata.Record; return vendorbson.Unmarshal(ref450, &record) }},
		{"map", func() error { var document map[string]any; return decLibraryDecoder.Unmarshal(ref450, &document) },
			func() error { var document map[string]any; return vendorbson.Unmarshal(ref450, &document) }},
		{"bson.D", func() error { var document primitive.D; return decLibraryDecoder.Unmarshal(ref450, &document) },
			func() error { var document vendorbson.D; return vendorbson.Unmarshal(ref450, &document) }},
	}
	for _, row := range rows {
		for _, call := range []func() error{row.library, row.vendor} {
			if err := call(); err != nil {
				t.Fatalf("%s: %v", row.name, err)
			}
		}
		libraryAllocs := testing.AllocsPerRun(200, func() { _ = row.library() })
		vendorAllocs := testing.AllocsPerRun(200, func() { _ = row.vendor() })
		t.Logf("%s: library %.0f allocations, vendor %.0f", row.name, libraryAllocs, vendorAllocs)
		if libraryAllocs > vendorAllocs {
			t.Errorf("%s: the library allocates %.0f times per decode of Ref450, the vendor %.0f", row.name, libraryAllocs, vendorAllocs)
		}
	}
}
