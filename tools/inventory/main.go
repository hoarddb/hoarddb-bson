// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
)

func main() {
	if err := runCommand(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "inventory:", err)
		os.Exit(1)
	}
}

func runCommand(arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("inventory", flag.ContinueOnError)
	serverDir := flags.String("server", "", "the hoardDB module directory (the one holding go.mod)")
	serverCommit := flags.String("server-commit", "", "the commit of the server tree, recorded in the header")
	driverDir := flags.String("driver", "", "the hoarddb-go module directory")
	driverCommit := flags.String("driver-commit", "", "the commit of the driver tree, recorded in the header")
	boundaries := flags.String("boundaries", strings.Join(DefaultBoundaries, ","), "comma-separated boundary function names (item 6)")
	toolCommit := flags.String("tool-commit", buildRevision(), "the commit of this tool, recorded in the header")
	outputPath := flags.String("o", "", "write the inventory to this file instead of standard output")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("reading flags: %w", err)
	}
	config := Config{ToolCommit: *toolCommit, Boundaries: splitList(*boundaries)}
	if *serverDir != "" {
		config.Trees = append(config.Trees, Tree{Label: "server", Dir: *serverDir, Commit: *serverCommit})
	}
	if *driverDir != "" {
		config.Trees = append(config.Trees, Tree{Label: "driver", Dir: *driverDir, Commit: *driverCommit})
	}
	if len(config.Trees) == 0 {
		return fmt.Errorf("no tree given. Run it as 'go run . -server <hoardDB dir> -server-commit <hash> -driver <hoarddb-go dir> -driver-commit <hash> -o ../../docs/api-inventory.md'")
	}
	rendered, err := Run(config)
	if err != nil {
		return err
	}
	if *outputPath == "" {
		if _, err := stdout.Write(rendered); err != nil {
			return fmt.Errorf("writing the inventory: %w", err)
		}
		return nil
	}
	if err := os.WriteFile(*outputPath, rendered, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", *outputPath, err)
	}
	return nil
}

func splitList(list string) []string {
	var names []string
	for _, name := range strings.Split(list, ",") {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	return names
}

// buildRevision is the VCS revision the binary was built from. A binary built
// by 'go run' carries none, so the flag is how a generated file records it.
func buildRevision() string {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, setting := range buildInfo.Settings {
		if setting.Key == "vcs.revision" {
			return setting.Value
		}
	}
	return "unknown"
}
