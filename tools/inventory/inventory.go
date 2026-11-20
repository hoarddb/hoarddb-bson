// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

// Command inventory lists the vendor BSON API that a tree uses (spec 5.1).
//
// It type-checks a hoardDB tree and the hoardDB Go driver with go/packages,
// so every number comes from resolved objects and not from text matching. The
// six sections of its Markdown output are the six items of spec 5.1.
package main

import (
	"cmp"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

// vendorModule is the module the library replaces. It is a string here and
// never an import, so the library module keeps no dependency on it.
const vendorModule = "go.mongodb.org/mongo-driver"

// mirroredPackages maps each vendor import path of spec 5.1 to the library
// path that replaces it, or to the reason there is none.
var mirroredPackages = map[string]string{
	vendorModule + "/bson":             "github.com/hoarddb/hoarddb-bson/bson",
	vendorModule + "/bson/primitive":   "github.com/hoarddb/hoarddb-bson/bson/primitive",
	vendorModule + "/x/bsonx/bsoncore": "github.com/hoarddb/hoarddb-bson/x/bsonx/bsoncore",
	vendorModule + "/bson/bsontype":    "github.com/hoarddb/hoarddb-bson/bson/bsontype",
	vendorModule + "/bson/bsoncodec":   "github.com/hoarddb/hoarddb-bson/bson/bsoncodec (struct tags only)",
	vendorModule + "/bson/bsonrw":      "not provided",
}

// DefaultBoundaries is the boundary list of build step 1b, item 6 of spec 5.1.
var DefaultBoundaries = []string{
	"checkDocumentDepth", "CheckDocumentDepth", "refuseDeepRequest", "decodePeerAnswer",
	"refuseDeepRelayedAnswers", "refuseDeepUpdateResult", "updateMayDeepen", "hasWireLayoutPayload",
}

// Tree is one source tree to inventory.
type Tree struct {
	Label  string // "server" or "driver"; the column and prefix in every table
	Dir    string // the directory holding the tree's go.mod
	Commit string // recorded in the output header
}

// Config is everything a run depends on.
type Config struct {
	Trees      []Tree
	Boundaries []string
	ToolCommit string
}

// Inventory is the collected result, before rendering.
type Inventory struct {
	Config         Config
	VendorVersions map[string]string // tree label -> vendor module version
	ImportingFiles map[string]int    // tree label -> files importing any vendor path
	Imports        []ImportCount
	NotLoaded      []string // "label:path" of files importing a mirrored path that the loader did not see
	References     []Reference
	Types          []TypeEntry
	Calls          []CallSite
	MapSites       []MapSite
	ErrorSites     []ErrorSite
	Renderings     []RenderingSite
	Boundaries     []BoundaryEntry
}

// ImportCount is how many files import one vendor path.
type ImportCount struct {
	Path   string
	Mirror string
	Files  map[string]int // by tree label, every .go file under the tree (what grep -rl counts)
	Loaded map[string]int // by tree label, files the type checker saw
}

// Reference is one exported vendor name and how often each tree uses it.
type Reference struct {
	Name   string // package.Name, package.Type.Method or package.Type.Field
	Kind   string
	Counts map[string]int
}

// TypeEntry is one vendor type with its whole exported surface.
type TypeEntry struct {
	Name             string
	Kind             string
	NameRefs         int  // references by name, aliases included
	ViaExpression    bool // some expression in the tree has this type or contains it
	Aliases          []string
	Fields           []string
	FieldTypes       []string // parallel to Fields
	UnexportedFields []string
	Methods          []string // sorted names
	Signatures       []string // parallel to Methods
	Implicit         []string // methods that fmt, encoding/json or the codec call without a visible call site
}

// Site is a position in a tree.
type Site struct {
	Tree string
	File string
	Line int
	Func string // the enclosing top-level declaration
	col  int
}

// CallSite is one marshal, unmarshal or Extended JSON call.
type CallSite struct {
	Site
	Callee     string
	Kind       string
	TargetType string
	Class      string
	PendingTap bool // the static type does not decide the class; the runtime tap does
	Note       string
}

// MapSite is a marshal call whose target is, or contains, a map.
type MapSite struct {
	Call    CallSite
	Via     string
	Pending bool // the target is an interface: it may hold a map, and the runtime tap decides
}

// ErrorSite is a place that prints, truncates, compares or branches on the
// text or the identity of a codec error.
type ErrorSite struct {
	Site
	Action string
	Origin string
}

// RenderingSite is a place whose output shows a string rendering of a vendor value.
type RenderingSite struct {
	Site
	Form   string
	Name   string
	Detail string
}

// BoundaryEntry is one configured boundary function and who calls it.
type BoundaryEntry struct {
	Name    string
	Defined []string
	Callers []BoundaryCaller
}

// BoundaryCaller is an enclosing function holding calls to a boundary function.
type BoundaryCaller struct {
	Tree  string
	File  string
	Func  string
	Test  bool
	Calls int
}

// Run collects the inventory and renders it.
func Run(cfg Config) ([]byte, error) {
	inventory, err := Collect(cfg)
	if err != nil {
		return nil, err
	}
	return inventory.Render(), nil
}

// Collect type-checks every tree and gathers the six sections.
func Collect(cfg Config) (*Inventory, error) {
	inventory := &Inventory{Config: cfg, VendorVersions: map[string]string{}, ImportingFiles: map[string]int{}}
	importsByPath := map[string]*ImportCount{}
	references := map[string]*Reference{}
	typeAccumulator := newTypeAccumulator()
	boundaryAccumulator := newBoundaryAccumulator(cfg.Boundaries)

	for _, tree := range cfg.Trees {
		syntactic, err := scanImports(tree)
		if err != nil {
			return nil, err
		}
		result, err := analyzeTree(tree, cfg.Boundaries)
		if err != nil {
			return nil, err
		}
		inventory.VendorVersions[tree.Label] = result.vendorVersion
		inventory.ImportingFiles[tree.Label] = len(syntactic)
		mergeImports(importsByPath, tree.Label, syntactic, result.loadedImports)
		inventory.NotLoaded = append(inventory.NotLoaded, notLoaded(tree.Label, syntactic, result.loadedFiles)...)
		mergeReferences(references, tree.Label, result.refs)
		typeAccumulator.merge(result)
		boundaryAccumulator.merge(tree.Label, result)
		inventory.Calls = append(inventory.Calls, result.calls...)
		inventory.MapSites = append(inventory.MapSites, result.mapSites...)
		inventory.ErrorSites = append(inventory.ErrorSites, result.errorSites...)
		inventory.Renderings = append(inventory.Renderings, result.renderings...)
	}

	treeOrder := map[string]int{}
	for index, tree := range cfg.Trees {
		treeOrder[tree.Label] = index
	}
	compareSites := func(left, right Site) int {
		return cmp.Or(
			cmp.Compare(treeOrder[left.Tree], treeOrder[right.Tree]),
			cmp.Compare(left.File, right.File),
			cmp.Compare(left.Line, right.Line),
			cmp.Compare(left.col, right.col),
		)
	}
	slices.SortFunc(inventory.Calls, func(left, right CallSite) int {
		return cmp.Or(compareSites(left.Site, right.Site), cmp.Compare(left.Callee, right.Callee))
	})
	slices.SortFunc(inventory.MapSites, func(left, right MapSite) int { return compareSites(left.Call.Site, right.Call.Site) })
	slices.SortFunc(inventory.ErrorSites, func(left, right ErrorSite) int {
		return cmp.Or(compareSites(left.Site, right.Site), cmp.Compare(left.Action, right.Action))
	})
	slices.SortFunc(inventory.Renderings, func(left, right RenderingSite) int {
		return cmp.Or(compareSites(left.Site, right.Site), cmp.Compare(left.Name, right.Name))
	})
	slices.Sort(inventory.NotLoaded)

	for _, importCount := range importsByPath {
		inventory.Imports = append(inventory.Imports, *importCount)
	}
	slices.SortFunc(inventory.Imports, func(left, right ImportCount) int { return cmp.Compare(left.Path, right.Path) })
	for _, reference := range references {
		inventory.References = append(inventory.References, *reference)
	}
	slices.SortFunc(inventory.References, func(left, right Reference) int { return cmp.Compare(left.Name, right.Name) })
	inventory.Types = typeAccumulator.entries()
	inventory.Boundaries = boundaryAccumulator.entries(treeOrder)
	return inventory, nil
}

// fileImports maps a tree-relative file path to the vendor paths it imports.
type fileImports map[string][]string

// scanImports reads every .go file under the tree, built or not, because the
// acceptance check counts files with grep and the loader skips build-tagged
// and nested-module files.
func scanImports(tree Tree) (fileImports, error) {
	found := fileImports{}
	walkErr := filepath.WalkDir(tree.Dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return fmt.Errorf("reading the imports of %s: %w", path, parseErr)
		}
		relative, relErr := filepath.Rel(tree.Dir, path)
		if relErr != nil {
			return fmt.Errorf("making %s relative to %s: %w", path, tree.Dir, relErr)
		}
		for _, importSpec := range parsed.Imports {
			importPath := strings.Trim(importSpec.Path.Value, "\"`")
			if importPath == vendorModule || strings.HasPrefix(importPath, vendorModule+"/") {
				relative = filepath.ToSlash(relative)
				found[relative] = append(found[relative], importPath)
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("scanning imports under %s tree %s: %w", tree.Label, tree.Dir, walkErr)
	}
	return found, nil
}

func mergeImports(into map[string]*ImportCount, label string, syntactic fileImports, loaded map[string]int) {
	entryFor := func(path string) *ImportCount {
		entry, exists := into[path]
		if !exists {
			mirror, mirrored := mirroredPackages[path]
			if !mirrored {
				mirror = "not mirrored (outside spec 5.1)"
			}
			entry = &ImportCount{Path: path, Mirror: mirror, Files: map[string]int{}, Loaded: map[string]int{}}
			into[path] = entry
		}
		return entry
	}
	for _, importedPaths := range syntactic {
		for _, path := range slices.Compact(slices.Sorted(slices.Values(importedPaths))) {
			entryFor(path).Files[label]++
		}
	}
	for path, fileCount := range loaded {
		entryFor(path).Loaded[label] = fileCount
	}
}

func notLoaded(label string, syntactic fileImports, loadedFiles map[string]bool) []string {
	var missing []string
	for relative, importedPaths := range syntactic {
		if loadedFiles[relative] {
			continue
		}
		for _, path := range importedPaths {
			if _, mirrored := mirroredPackages[path]; mirrored {
				missing = append(missing, label+":"+relative)
				break
			}
		}
	}
	return missing
}

func mergeReferences(into map[string]*Reference, label string, counted map[string]*referenceCount) {
	for name, counter := range counted {
		reference, exists := into[name]
		if !exists {
			reference = &Reference{Name: name, Kind: counter.kind, Counts: map[string]int{}}
			into[name] = reference
		}
		reference.Counts[label] += counter.count
	}
}

// loadTree type-checks the tree's packages, tests included, and everything
// they import from source, so a vendor type is the same object everywhere.
func loadTree(tree Tree) ([]*packages.Package, error) {
	config := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports |
			packages.NeedDeps | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedSyntax | packages.NeedModule,
		Dir:   tree.Dir,
		Tests: true,
		Env:   append(os.Environ(), "GOWORK=off"),
	}
	loaded, err := packages.Load(config, "./...")
	if err != nil {
		return nil, fmt.Errorf("loading the %s tree at %s: %w", tree.Label, tree.Dir, err)
	}
	var problems []string
	for _, loadedPackage := range loaded {
		for _, packageError := range loadedPackage.Errors {
			problems = append(problems, packageError.Error())
		}
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		problems = slices.Compact(problems)
		const shown = 8
		if len(problems) > shown {
			problems = append(problems[:shown], fmt.Sprintf("... and %d more", len(problems)-shown))
		}
		return nil, fmt.Errorf("the %s tree at %s does not type-check, so its inventory would be incomplete:\n  %s",
			tree.Label, tree.Dir, strings.Join(problems, "\n  "))
	}
	slices.SortFunc(loaded, func(left, right *packages.Package) int { return cmp.Compare(left.ID, right.ID) })
	return loaded, nil
}
