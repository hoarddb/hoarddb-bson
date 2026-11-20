// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/perf/benchfmt"
	"golang.org/x/perf/benchmath"
)

// timeRow is one benchmark's timings (sec/op) on both libraries.
type timeRow struct {
	name            string // the benchmark name without its lib= part
	rerunPattern    string // the -bench pattern that runs just this row
	vendor, library []float64
}

// allocationRow is one benchmark's B/op or allocs/op on both libraries.
type allocationRow struct {
	name, unit      string
	vendor, library []float64
}

// regression is spec 9.2's exact rule: the library allocates more than the
// vendor. Allocation counts are deterministic, so the medians compare.
func (row allocationRow) regression() bool {
	return median(row.library) > median(row.vendor)
}

// parsedBenchmarks is a benchmark file split into the rows the gate reads.
type parsedBenchmarks struct {
	timeRows       []timeRow
	allocationRows []allocationRow
	unpaired       []string // benchmarks without both lib=hoarddb and lib=vendor
}

// benchmarkPrefix is what benchfmt drops from every name; the gate puts
// it back, so its rows and re-run patterns read as go test prints them.
const benchmarkPrefix = "Benchmark"

const (
	libraryLabel = "lib=hoarddb"
	vendorLabel  = "lib=vendor"
)

// pairedSamples collects both libraries' values of one row and unit.
type pairedSamples struct {
	rerunPattern    string
	vendor, library []float64
}

// parseBenchmarks reads go test -bench output. A row is a benchmark name
// with its /lib= part removed; its library and vendor results pair up.
func parseBenchmarks(input io.Reader, fileName string) (parsedBenchmarks, error) {
	reader := benchfmt.NewReader(input, fileName)
	samplesByRow := map[string]map[string]*pairedSamples{}
	seenNames := map[string]bool{}
	for reader.Scan() {
		switch record := reader.Result().(type) {
		case *benchfmt.SyntaxError:
			return parsedBenchmarks{}, fmt.Errorf("reading %s: %w", fileName, record)
		case *benchfmt.Result:
			rowName, rerunPattern, side := splitLibrary(record.Name)
			if side == "" {
				seenNames[benchmarkPrefix+record.Name.String()] = true
				continue
			}
			for _, measured := range record.Values {
				if samplesByRow[rowName] == nil {
					samplesByRow[rowName] = map[string]*pairedSamples{}
				}
				samples := samplesByRow[rowName][measured.Unit]
				if samples == nil {
					samples = &pairedSamples{rerunPattern: rerunPattern}
					samplesByRow[rowName][measured.Unit] = samples
				}
				if side == libraryLabel {
					samples.library = append(samples.library, measured.Value)
				} else {
					samples.vendor = append(samples.vendor, measured.Value)
				}
			}
		}
	}
	if err := reader.Err(); err != nil {
		return parsedBenchmarks{}, fmt.Errorf("reading %s: %w", fileName, err)
	}
	var parsed parsedBenchmarks
	for unpairedName := range seenNames {
		parsed.unpaired = append(parsed.unpaired, unpairedName)
	}
	for rowName, samplesByUnit := range samplesByRow {
		for unit, samples := range samplesByUnit {
			if len(samples.library) == 0 || len(samples.vendor) == 0 {
				parsed.unpaired = append(parsed.unpaired, rowName+" ("+unit+")")
				continue
			}
			switch unit {
			case "sec/op":
				parsed.timeRows = append(parsed.timeRows, timeRow{name: rowName, rerunPattern: samples.rerunPattern,
					vendor: samples.vendor, library: samples.library})
			case "B/op", "allocs/op":
				parsed.allocationRows = append(parsed.allocationRows, allocationRow{name: rowName, unit: unit,
					vendor: samples.vendor, library: samples.library})
			}
		}
	}
	sort.Strings(parsed.unpaired)
	sort.Slice(parsed.timeRows, func(left, right int) bool { return parsed.timeRows[left].name < parsed.timeRows[right].name })
	sort.Slice(parsed.allocationRows, func(left, right int) bool {
		leftRow, rightRow := parsed.allocationRows[left], parsed.allocationRows[right]
		return leftRow.name+leftRow.unit < rightRow.name+rightRow.unit
	})
	return parsed, nil
}

// splitLibrary removes the /lib= part of a benchmark name. It returns the
// row name, a -bench pattern that runs both sides of just that row, and
// which side the result is, or "" when the name has no lib= part.
func splitLibrary(fullName benchfmt.Name) (rowName, rerunPattern, side string) {
	baseName, nameParts := fullName.Parts()
	rowParts := []string{benchmarkPrefix + string(baseName)}
	patternParts := []string{"^" + regexp.QuoteMeta(benchmarkPrefix+string(baseName)) + "$"}
	for _, namePart := range nameParts {
		if !bytes.HasPrefix(namePart, []byte("/")) {
			rowParts = append(rowParts, string(namePart)) // the -GOMAXPROCS suffix
			continue
		}
		subName := string(namePart[1:])
		if subName == libraryLabel || subName == vendorLabel {
			side = subName
			patternParts = append(patternParts, "^lib=(hoarddb|vendor)$")
			continue
		}
		rowParts = append(rowParts, string(namePart))
		patternParts = append(patternParts, "^"+regexp.QuoteMeta(subName)+"$")
	}
	return strings.Join(rowParts, ""), strings.Join(patternParts, "/"), side
}

// timeVerdict is one time row after the Holm–Bonferroni correction.
type timeVerdict struct {
	name, rerunPattern      string
	vendorMedian, libMedian float64
	rawP, adjustedP         float64
	// candidate is a row significant after the correction with the
	// library slower; it is a regression only if its re-run confirms it.
	candidate bool
}

// holmCandidates applies the Holm–Bonferroni step-down correction across
// every time row (spec 9.2): rows sorted by p ascending, the k-th (from 0)
// compared with alpha/(m-k), stopping at the first that fails. The adjusted
// p-value is the running maximum of (m-k)·p, capped at 1.
func holmCandidates(timeRows []timeRow, alpha float64) []timeVerdict {
	verdicts := make([]timeVerdict, len(timeRows))
	for rowIndex, row := range timeRows {
		verdicts[rowIndex] = timeVerdict{name: row.name, rerunPattern: row.rerunPattern,
			vendorMedian: median(row.vendor), libMedian: median(row.library),
			rawP: uTestP(row.vendor, row.library)}
	}
	order := make([]int, len(verdicts))
	for position := range order {
		order[position] = position
	}
	sort.SliceStable(order, func(left, right int) bool { return verdicts[order[left]].rawP < verdicts[order[right]].rawP })
	runningMax := 0.0
	for rank, rowIndex := range order {
		runningMax = max(runningMax, min(1, float64(len(order)-rank)*verdicts[rowIndex].rawP))
		verdicts[rowIndex].adjustedP = runningMax
	}
	for rowIndex := range verdicts {
		verdict := &verdicts[rowIndex]
		verdict.candidate = verdict.adjustedP < alpha && verdict.libMedian > verdict.vendorMedian
	}
	return verdicts
}

// rerunFunc re-runs one row alone and returns its new samples.
type rerunFunc func(rerunPattern string) (timeRow, error)

// confirmCandidates re-runs each candidate alone and returns those
// significant again with the library still slower (confirmed), and those
// whose re-run failed (unchecked). Both count as regressions, so a broken
// re-run can never pass the gate.
func confirmCandidates(verdicts []timeVerdict, rerun rerunFunc, alpha float64) (confirmed, unchecked []string) {
	for _, verdict := range verdicts {
		if !verdict.candidate {
			continue
		}
		rerunRow, err := rerun(verdict.rerunPattern)
		if err != nil || len(rerunRow.vendor) == 0 || len(rerunRow.library) == 0 {
			unchecked = append(unchecked, verdict.name)
			continue
		}
		if uTestP(rerunRow.vendor, rerunRow.library) < alpha && median(rerunRow.library) > median(rerunRow.vendor) {
			confirmed = append(confirmed, verdict.name)
		}
	}
	return confirmed, unchecked
}

// uTestP is the two-sided Mann–Whitney U p-value of two samples.
func uTestP(vendorValues, libraryValues []float64) float64 {
	thresholds := &benchmath.DefaultThresholds
	return benchmath.AssumeNothing.Compare(
		benchmath.NewSample(append([]float64(nil), vendorValues...), thresholds),
		benchmath.NewSample(append([]float64(nil), libraryValues...), thresholds)).P
}

// median of values, which it does not reorder.
func median(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}
