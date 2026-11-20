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

// scanDocuments is N of spec 9.2's Validate-against-TrustStored row.
const scanDocuments = 1000

// BenchmarkValidateVsTrustStoredScan is the informational row of spec 9.2
// for call C1 (review finding B1): what a scan of 1,000 stored Ref450
// documents costs when each is validated again, against TrustStored's
// framing check. It has no lib=vendor side, so benchgate lists it as
// unpaired; the vendor baselines of Validate (the server's depth scan) live
// in hoardDB, because the scan is BSL (build doc SE-5).
func BenchmarkValidateVsTrustStoredScan(b *testing.B) {
	ref450, err := benchdata.Ref450Bytes()
	if err != nil {
		b.Fatal(err)
	}
	storedDocuments := make([][]byte, scanDocuments)
	for position := range storedDocuments {
		storedDocuments[position] = append([]byte(nil), ref450...)
	}
	b.Run("check=Validate", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(scanDocuments * len(ref450)))
		for range b.N {
			for _, storedDocument := range storedDocuments {
				if _, err := bson.Validate(storedDocument, bson.Limits{Depth: 100}); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
	b.Run("check=TrustStored", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(scanDocuments * len(ref450)))
		for range b.N {
			for _, storedDocument := range storedDocuments {
				if _, err := bson.TrustStored(storedDocument); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}

// BenchmarkBuilder builds Ref450 element by element: lib=hoarddb with
// bson.Builder, lib=vendor with bsoncore's Append*Element functions and
// BuildDocument, the way the tree builds documents today. Both produce the
// same bytes (TestBuilderMatchesBuildDocument).
func BenchmarkBuilder(b *testing.B) {
	b.Run("lib=hoarddb", func(b *testing.B) {
		b.ReportAllocs()
		var builder bson.Builder
		for range b.N {
			if _, err := buildRef450(&builder); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("lib=vendor", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			vendorBuildRef450()
		}
	})
}

// BenchmarkLookup is spec 9.2's lookup pair on Ref450: Document.Lookup
// against vendorbson.Raw.LookupErr, for a top-level member and a nested
// one. "address" is Ref450's last field, so the top-level row walks every
// element before it; "address.country" is the embedded document's last.
func BenchmarkLookup(b *testing.B) {
	ref450, err := benchdata.Ref450Bytes()
	if err != nil {
		b.Fatal(err)
	}
	document, err := bson.Validate(ref450, bson.Limits{Depth: 3})
	if err != nil {
		b.Fatal(err)
	}
	for _, row := range []struct {
		name string
		path bson.Path
		keys []string
	}{
		{"top", bson.Path{bson.Name("address")}, []string{"address"}},
		{"nested", bson.Path{bson.Name("address"), bson.Name("country")}, []string{"address", "country"}},
	} {
		b.Run(row.name+"/lib=hoarddb", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, found, err := document.Lookup(row.path); !found || err != nil {
					b.Fatal("not found")
				}
			}
		})
		b.Run(row.name+"/lib=vendor", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := vendorbson.Raw(ref450).LookupErr(row.keys...); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkStructMarshal is spec 9.2's struct-marshal row and the hard one:
// Ref450 as a struct passed by value, lib=hoarddb through the encoder's
// internal entry (bson.Marshal wraps it in step 4f), lib=vendor through
// vendorbson.Marshal. The form=pointer, form=D and form=map rows are
// informational: the same document from a struct pointer, a bson.D, and a
// map of the same fields.
func BenchmarkStructMarshal(b *testing.B) {
	libRecord, vendorRecord := benchdata.Ref450LibRecord(), benchdata.Ref450Record()
	vendorDocument := benchdata.Ref450D()
	libDocument := libraryTwin(b, vendorDocument)
	vendorMap := vendorbson.M{}
	for _, documentElement := range vendorDocument {
		vendorMap[documentElement.Key] = documentElement.Value
	}
	libMap := libraryTwin(b, vendorMap)
	for _, row := range []struct {
		form        string
		lib, vendor any
	}{
		{"struct", libRecord, vendorRecord},
		{"pointer", &libRecord, &vendorRecord},
		{"D", libDocument, vendorDocument},
		{"map", libMap, vendorMap},
	} {
		b.Run("form="+row.form+"/lib=hoarddb", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := bson.Marshal(row.lib); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("form="+row.form+"/lib=vendor", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := vendorbson.Marshal(row.vendor); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
