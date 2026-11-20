// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"cmp"
	"fmt"
	"go/types"
	"maps"
	"slices"
	"strings"
)

// implicitMethods are the methods something calls without a call site in the
// tree: fmt, encoding/json, encoding, and the codec itself.
var implicitMethods = []string{
	"Error", "Format", "GoString", "IsZero", "MarshalBSON", "MarshalBSONValue", "MarshalJSON", "MarshalText",
	"String", "UnmarshalBSON", "UnmarshalBSONValue", "UnmarshalJSON", "UnmarshalText",
}

// typeAccumulator merges the vendor types found in each tree.
type typeAccumulator struct {
	byName map[string]*TypeEntry
}

func newTypeAccumulator() *typeAccumulator {
	return &typeAccumulator{byName: map[string]*TypeEntry{}}
}

func (accumulator *typeAccumulator) merge(result *treeResult) {
	for key, named := range result.named {
		entry, exists := accumulator.byName[key]
		if !exists {
			entry = buildTypeEntry(key, named)
			accumulator.byName[key] = entry
		}
		entry.NameRefs += result.nameRefs[key]
		entry.ViaExpression = entry.ViaExpression || result.viaExpression[key]
		for alias := range result.aliases[key] {
			if !slices.Contains(entry.Aliases, alias) {
				entry.Aliases = append(entry.Aliases, alias)
			}
		}
		slices.Sort(entry.Aliases)
	}
}

func (accumulator *typeAccumulator) entries() []TypeEntry {
	listed := make([]TypeEntry, 0, len(accumulator.byName))
	for _, name := range slices.Sorted(maps.Keys(accumulator.byName)) {
		listed = append(listed, *accumulator.byName[name])
	}
	return listed
}

// buildTypeEntry lists the whole exported surface of a vendor type: spec 5.1
// item 2 mirrors a listed type whole because interface satisfaction is invisible
// to references.
func buildTypeEntry(name string, named *types.Named) *TypeEntry {
	entry := &TypeEntry{Name: name, Kind: kindOf(named)}
	if structType, isStruct := named.Underlying().(*types.Struct); isStruct {
		for index := 0; index < structType.NumFields(); index++ {
			field := structType.Field(index)
			if field.Exported() {
				entry.Fields = append(entry.Fields, field.Name())
				entry.FieldTypes = append(entry.FieldTypes, typeText(field.Type()))
			} else {
				entry.UnexportedFields = append(entry.UnexportedFields, field.Name())
			}
		}
	}
	var methodSet *types.MethodSet
	if _, isInterface := named.Underlying().(*types.Interface); isInterface {
		methodSet = types.NewMethodSet(named)
	} else {
		methodSet = types.NewMethodSet(types.NewPointer(named))
	}
	type method struct{ name, signature string }
	var methods []method
	for index := 0; index < methodSet.Len(); index++ {
		function, isFunc := methodSet.At(index).Obj().(*types.Func)
		if !isFunc || !function.Exported() {
			continue
		}
		methods = append(methods, method{function.Name(), methodSignature(function)})
	}
	slices.SortFunc(methods, func(left, right method) int { return cmp.Compare(left.name, right.name) })
	for _, listed := range methods {
		entry.Methods = append(entry.Methods, listed.name)
		entry.Signatures = append(entry.Signatures, listed.signature)
		if slices.Contains(implicitMethods, listed.name) {
			entry.Implicit = append(entry.Implicit, listed.name)
		}
	}
	return entry
}

func kindOf(named *types.Named) string {
	switch underlying := named.Underlying().(type) {
	case *types.Struct:
		return "struct"
	case *types.Interface:
		return "interface"
	case *types.Basic:
		return underlying.Name()
	case *types.Map:
		return "map"
	case *types.Slice:
		return "slice of " + typeText(underlying.Elem())
	case *types.Array:
		return fmt.Sprintf("array of %d %s", underlying.Len(), typeText(underlying.Elem()))
	}
	return "other"
}

// methodSignature prints a method the way Go declares it.
func methodSignature(function *types.Func) string {
	signature := function.Type().(*types.Signature)
	rest := strings.TrimPrefix(types.TypeString(signature, qualifier), "func")
	if signature.Recv() == nil {
		return "func " + function.Name() + rest
	}
	return "func (" + typeText(signature.Recv().Type()) + ") " + function.Name() + rest
}

// boundaryAccumulator merges boundary definitions and callers across trees.
type boundaryAccumulator struct {
	names   []string
	defs    map[string][]string
	callers map[string]map[BoundaryCaller]int
}

func newBoundaryAccumulator(names []string) *boundaryAccumulator {
	return &boundaryAccumulator{
		names:   slices.Compact(slices.Sorted(slices.Values(names))),
		defs:    map[string][]string{},
		callers: map[string]map[BoundaryCaller]int{},
	}
}

func (accumulator *boundaryAccumulator) merge(label string, result *treeResult) {
	for name, definitions := range result.boundaryDefs {
		accumulator.defs[name] = append(accumulator.defs[name], definitions...)
	}
	for name, callers := range result.boundaryCalls {
		if accumulator.callers[name] == nil {
			accumulator.callers[name] = map[BoundaryCaller]int{}
		}
		for key, calls := range callers {
			caller := BoundaryCaller{Tree: label, File: key.file, Func: key.fn, Test: strings.HasSuffix(key.file, "_test.go")}
			accumulator.callers[name][caller] += calls
		}
	}
}

func (accumulator *boundaryAccumulator) entries(treeOrder map[string]int) []BoundaryEntry {
	listed := make([]BoundaryEntry, 0, len(accumulator.names))
	for _, name := range accumulator.names {
		entry := BoundaryEntry{Name: name, Defined: slices.Clone(accumulator.defs[name])}
		slices.Sort(entry.Defined)
		for caller, calls := range accumulator.callers[name] {
			caller.Calls = calls
			entry.Callers = append(entry.Callers, caller)
		}
		slices.SortFunc(entry.Callers, func(left, right BoundaryCaller) int {
			return cmp.Or(
				cmp.Compare(treeOrder[left.Tree], treeOrder[right.Tree]),
				cmp.Compare(left.File, right.File),
				cmp.Compare(left.Func, right.Func),
			)
		})
		listed = append(listed, entry)
	}
	return listed
}
