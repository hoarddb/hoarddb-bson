// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

// Command benchgate is the benchmark gate of spec 9.2. It reads go test
// -bench output holding lib=hoarddb and lib=vendor sub-benchmarks, tests
// every time row with a Mann–Whitney U test, corrects across all of them
// with Holm–Bonferroni, re-runs each candidate alone at -count=20 and
// reports it only if it is significant again. Allocations compare exactly.
// It exits 1 on any regression.
//
// Usage:
//
//	benchgate -dir <package dir> <bench file>
//
// -dir is where a candidate is re-run (go test -run '^$' -bench <row>
// -benchmem -count=20 .); it is needed only when there is a candidate.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"text/tabwriter"
)

const alpha = 0.05

// rerunCount is the re-run's -count (spec 9.2).
const rerunCount = 20

func main() {
	packageDir := flag.String("dir", "", "the package directory to re-run a candidate row in")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: benchgate -dir <package dir> <bench file>")
		os.Exit(2)
	}
	regressions, err := runGate(flag.Arg(0), *packageDir, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "benchgate:", err)
		os.Exit(2)
	}
	if regressions > 0 {
		fmt.Fprintf(os.Stderr, "benchgate: %d regression(s); review each one with its numbers\n", regressions)
		os.Exit(1)
	}
}

// runGate gates one benchmark file and prints its table, returning the
// number of regressions.
func runGate(benchPath, packageDir string, output io.Writer) (int, error) {
	benchBytes, err := os.ReadFile(benchPath)
	if err != nil {
		return 0, fmt.Errorf("reading the benchmark file: %w", err)
	}
	parsed, err := parseBenchmarks(bytes.NewReader(benchBytes), benchPath)
	if err != nil {
		return 0, err
	}
	verdicts := holmCandidates(parsed.timeRows, alpha)
	confirmed, unchecked := confirmCandidates(verdicts, func(rerunPattern string) (timeRow, error) {
		rerun, err := rerunRow(packageDir, rerunPattern)
		if err != nil {
			// The row stays a regression; say why it could not be cleared.
			fmt.Fprintln(os.Stderr, "benchgate:", err)
		}
		return rerun, err
	}, alpha)
	confirmedSet, uncheckedSet := map[string]bool{}, map[string]bool{}
	for _, rowName := range confirmed {
		confirmedSet[rowName] = true
	}
	for _, rowName := range unchecked {
		uncheckedSet[rowName] = true
	}

	table := &gateTable{writer: tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)}
	table.row("row\tunit\tvendor\thoarddb\tp\tp (Holm)\tverdict\n")
	for _, verdict := range verdicts {
		verdictText := "ok"
		switch {
		case confirmedSet[verdict.name]:
			verdictText = "REGRESSION (confirmed by re-run)"
		case uncheckedSet[verdict.name]:
			verdictText = "REGRESSION (candidate; the re-run failed)"
		case verdict.candidate:
			verdictText = "candidate, not confirmed by re-run"
		}
		table.row("%s\tns/op\t%.1f\t%.1f\t%.4f\t%.4f\t%s\n", verdict.name,
			verdict.vendorMedian*1e9, verdict.libMedian*1e9, verdict.rawP, verdict.adjustedP, verdictText)
	}
	allocationRegressions := 0
	for _, row := range parsed.allocationRows {
		verdictText := "ok"
		if row.regression() {
			verdictText = "REGRESSION (more than the vendor)"
			allocationRegressions++
		}
		table.row("%s\t%s\t%g\t%g\t\t\t%s\n", row.name, row.unit, median(row.vendor), median(row.library), verdictText)
	}
	for _, unpairedName := range parsed.unpaired {
		table.row("%s\t\t\t\t\t\tunpaired, informational\n", unpairedName)
	}
	if err := table.flush(); err != nil {
		return 0, fmt.Errorf("writing the table: %w", err)
	}
	return len(confirmed) + len(unchecked) + allocationRegressions, nil
}

// gateTable writes the table's rows and keeps the first write error, which
// flush returns.
type gateTable struct {
	writer   *tabwriter.Writer
	writeErr error
}

func (table *gateTable) row(format string, arguments ...any) {
	if table.writeErr == nil {
		_, table.writeErr = fmt.Fprintf(table.writer, format, arguments...)
	}
}

func (table *gateTable) flush() error {
	if table.writeErr != nil {
		return table.writeErr
	}
	return table.writer.Flush()
}

// errNoRerunDir is the refusal to re-run without a package directory.
var errNoRerunDir = errors.New("a row is a candidate regression and must be re-run alone; pass -dir <the benchmarked package's directory>")

// rerunRow runs one row alone at -count=20 and parses its samples.
func rerunRow(packageDir, rerunPattern string) (timeRow, error) {
	if packageDir == "" {
		return timeRow{}, errNoRerunDir
	}
	rerunCommand := exec.Command("go", "test", "-run", "^$", "-bench", rerunPattern, "-benchmem",
		fmt.Sprintf("-count=%d", rerunCount), ".")
	rerunCommand.Dir = packageDir
	rerunCommand.Env = append(os.Environ(), "GOWORK=off")
	var rerunOutput bytes.Buffer
	rerunCommand.Stdout = &rerunOutput
	rerunCommand.Stderr = os.Stderr
	if err := rerunCommand.Run(); err != nil {
		return timeRow{}, fmt.Errorf("re-running %s in %s: %w", rerunPattern, packageDir, err)
	}
	parsed, err := parseBenchmarks(&rerunOutput, "re-run of "+rerunPattern)
	if err != nil {
		return timeRow{}, err
	}
	if len(parsed.timeRows) != 1 {
		return timeRow{}, fmt.Errorf("re-running %s gave %d time rows, want 1", rerunPattern, len(parsed.timeRows))
	}
	return parsed.timeRows[0], nil
}
