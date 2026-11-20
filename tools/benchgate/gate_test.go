// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"golang.org/x/perf/benchmath"
)

// shifted is ten timings from base, base+step, ... so two samples overlap
// by as much as their bases differ.
func shifted(base, step float64) []float64 {
	timings := make([]float64, 10)
	for position := range timings {
		timings[position] = base + float64(position)*step
	}
	return timings
}

// rawP is the uncorrected two-sided Mann-Whitney p-value of two samples.
func rawP(vendorTimings, libraryTimings []float64) float64 {
	thresholds := &benchmath.DefaultThresholds
	return benchmath.AssumeNothing.Compare(
		benchmath.NewSample(append([]float64(nil), vendorTimings...), thresholds),
		benchmath.NewSample(append([]float64(nil), libraryTimings...), thresholds)).P
}

func TestHolmFlagsOnlyCorrectedSignificantRows(t *testing.T) {
	// 18 rows where the library is a little slower: p near 0.02, significant
	// alone, not after the correction across 20 rows.
	mildVendor, mildLibrary := shifted(100, 1), shifted(104, 1)
	if p := rawP(mildVendor, mildLibrary); p >= 0.05 || p < 0.0025 {
		t.Fatalf("the mild rows' raw p is %v; the test needs it significant alone and not after Holm", p)
	}
	var timeRows []timeRow
	for rowIndex := 0; rowIndex < 18; rowIndex++ {
		timeRows = append(timeRows, timeRow{name: fmt.Sprintf("BenchmarkMild/row=%d", rowIndex), vendor: mildVendor, library: mildLibrary})
	}
	// One row far slower, and one far faster: both significant after the
	// correction, and only the slower one is a candidate.
	timeRows = append(timeRows,
		timeRow{name: "BenchmarkSlow", vendor: shifted(100, 1), library: shifted(200, 1)},
		timeRow{name: "BenchmarkFast", vendor: shifted(200, 1), library: shifted(100, 1)})

	verdicts := holmCandidates(timeRows, 0.05)
	var candidates []string
	for _, verdict := range verdicts {
		if verdict.candidate {
			candidates = append(candidates, verdict.name)
		}
	}
	if strings.Join(candidates, ",") != "BenchmarkSlow" {
		t.Fatalf("candidates are %v, want only BenchmarkSlow", candidates)
	}
	for _, verdict := range verdicts {
		if verdict.name == "BenchmarkFast" && !(verdict.adjustedP < 0.05) {
			t.Fatalf("the faster row's corrected p is %v; it must be significant and still not a candidate", verdict.adjustedP)
		}
	}

	// A candidate is a regression only when its re-run alone is
	// significant again.
	confirmed, unchecked := confirmCandidates(verdicts, func(rowName string) (timeRow, error) {
		return timeRow{name: rowName, vendor: shifted(100, 1), library: shifted(200, 1)}, nil
	}, 0.05)
	if len(confirmed) != 1 || confirmed[0] != "BenchmarkSlow" || len(unchecked) != 0 {
		t.Fatalf("confirmed regressions %v and unchecked %v, want [BenchmarkSlow] and none", confirmed, unchecked)
	}
	notConfirmed, _ := confirmCandidates(verdicts, func(rowName string) (timeRow, error) {
		return timeRow{name: rowName, vendor: shifted(100, 1), library: shifted(100, 1)}, nil
	}, 0.05)
	if len(notConfirmed) != 0 {
		t.Fatalf("a re-run with no difference still confirmed %v", notConfirmed)
	}
	_, failedRerun := confirmCandidates(verdicts, func(string) (timeRow, error) {
		return timeRow{}, errNoRerunDir
	}, 0.05)
	if len(failedRerun) != 1 {
		t.Fatalf("a failed re-run left %v unchecked, want the candidate kept as a regression", failedRerun)
	}
}

func TestAllocationsCompareExactly(t *testing.T) {
	for _, row := range []struct {
		unit            string
		vendor, library float64
		regression      bool
	}{
		{"allocs/op", 2, 3, true},
		{"allocs/op", 2, 2, false},
		{"allocs/op", 2, 1, false},
		{"B/op", 100, 101, true},
		{"B/op", 101, 100, false},
	} {
		samples := allocationRow{name: "BenchmarkX", unit: row.unit,
			vendor: []float64{row.vendor, row.vendor}, library: []float64{row.library, row.library}}
		if got := samples.regression(); got != row.regression {
			t.Errorf("%s vendor %v library %v: regression %v, want %v", row.unit, row.vendor, row.library, got, row.regression)
		}
	}
}

func TestParsePairsLibraryAndVendorRows(t *testing.T) {
	benchmarkText := `goos: linux
pkg: example
BenchmarkLookup/size=ref450/lib=hoarddb-16    1000    50 ns/op    0 B/op    0 allocs/op
BenchmarkLookup/size=ref450/lib=vendor-16     1000    70 ns/op   16 B/op    1 allocs/op
BenchmarkLookup/size=ref450/lib=hoarddb-16    1000    52 ns/op    0 B/op    0 allocs/op
BenchmarkLookup/size=ref450/lib=vendor-16     1000    71 ns/op   16 B/op    1 allocs/op
BenchmarkScan/check=Validate-16               1000    90 ns/op    0 B/op    0 allocs/op
`
	parsed, err := parseBenchmarks(strings.NewReader(benchmarkText), "inline")
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.timeRows) != 1 || parsed.timeRows[0].name != "BenchmarkLookup/size=ref450-16" {
		t.Fatalf("time rows %+v, want one, BenchmarkLookup/size=ref450-16", parsed.timeRows)
	}
	// benchfmt tidies ns/op into sec/op, which is not exact in binary.
	if got := parsed.timeRows[0]; len(got.library) != 2 || math.Abs(got.library[0]-50e-9) > 1e-15 || math.Abs(got.vendor[1]-71e-9) > 1e-15 {
		t.Fatalf("time samples %+v", got)
	}
	if len(parsed.allocationRows) != 2 {
		t.Fatalf("allocation rows %+v, want B/op and allocs/op", parsed.allocationRows)
	}
	if strings.Join(parsed.unpaired, ",") != "BenchmarkScan/check=Validate-16" {
		t.Fatalf("unpaired %v", parsed.unpaired)
	}
	if got, want := parsed.timeRows[0].rerunPattern, `^BenchmarkLookup$/^size=ref450$/^lib=(hoarddb|vendor)$`; got != want {
		t.Fatalf("re-run pattern %q, want %q", got, want)
	}
}
