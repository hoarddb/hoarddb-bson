// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"go/types"
	"reflect"
	"strings"
)

// dereference strips aliases and pointers.
func dereference(candidate types.Type) types.Type {
	for candidate != nil {
		candidate = types.Unalias(candidate)
		pointer, isPointer := candidate.(*types.Pointer)
		if !isPointer {
			return candidate
		}
		candidate = pointer.Elem()
	}
	return nil
}

// hasMethod reports whether a named type, or a pointer to it, has an exported method.
func hasMethod(named *types.Named, method string) bool {
	return types.NewMethodSet(types.NewPointer(named)).Lookup(nil, method) != nil ||
		types.NewMethodSet(named).Lookup(nil, method) != nil
}

// classifyTarget gives the static class of a codec target and says whether the
// static type fails to decide it, which the runtime tap completes (spec 9.1.7).
func classifyTarget(target types.Type) (class string, pendingTap bool) {
	base := dereference(target)
	switch typed := base.(type) {
	case nil:
		return "unknown", true
	case *types.TypeParam:
		return "type-parameter", true
	case *types.Basic:
		if typed.Kind() == types.UntypedNil {
			return "nil", false
		}
	case *types.Named:
		if class, isVendor := vendorClass(typed); isVendor {
			return class, false
		}
	}
	switch underlying := base.Underlying().(type) {
	case *types.Interface:
		if underlying.Empty() {
			return "interface{}", true
		}
		return "interface", true
	case *types.Struct:
		return "struct", false
	case *types.Map:
		return "map", false
	case *types.Slice:
		if basic, isBasic := underlying.Elem().Underlying().(*types.Basic); isBasic && basic.Kind() == types.Byte {
			return "bytes", false
		}
		return "slice", false
	case *types.Array:
		return "array", false
	case *types.Basic:
		return "scalar", false
	}
	return "other", false
}

// vendorClass names the classes the vendor's own document types fall in.
func vendorClass(named *types.Named) (string, bool) {
	if !isVendorPackage(named.Obj().Pkg()) {
		return "", false
	}
	switch qualifiedName(named.Obj()) {
	case "primitive.D":
		return "ordered-document", true
	case "primitive.M":
		return "map", true
	case "primitive.A":
		return "array-value", true
	case "bson.Raw":
		return "raw-document", true
	case "bson.RawValue":
		return "raw-value", true
	}
	if named.Obj().Pkg().Name() == "primitive" {
		return "bson-scalar", true
	}
	return "", false
}

var customCodecMethods = []string{"MarshalBSON", "UnmarshalBSON", "MarshalBSONValue", "UnmarshalBSONValue"}

// codecMethodsNote names the custom codec methods a target type carries, which
// make the static class a lower bound.
func codecMethodsNote(target types.Type) string {
	named, isNamed := dereference(target).(*types.Named)
	if !isNamed {
		return ""
	}
	var found []string
	for _, method := range customCodecMethods {
		if hasMethod(named, method) {
			found = append(found, method)
		}
	}
	if len(found) == 0 {
		return ""
	}
	return "has " + strings.Join(found, ", ")
}

// typeSearch walks a type's reachable fields looking for a hit, the way the
// codec or encoding/json would reach them.
type typeSearch struct {
	tagKey   string
	hitMap   bool
	hitNamed func(*types.Named) bool
}

const maxSearchDepth = 8

// find returns the field path to the first hit and the hit's type text.
func (search typeSearch) find(root types.Type) (path, hit string, found bool) {
	return search.walk(root, "", 0, map[*types.Named]bool{})
}

func (search typeSearch) walk(shown types.Type, path string, depth int, seen map[*types.Named]bool) (string, string, bool) {
	if depth > maxSearchDepth || shown == nil {
		return "", "", false
	}
	switch typed := types.Unalias(shown).(type) {
	case *types.Pointer:
		return search.walk(typed.Elem(), path, depth+1, seen)
	case *types.Slice:
		return search.walk(typed.Elem(), path+"[]", depth+1, seen)
	case *types.Array:
		return search.walk(typed.Elem(), path+"[]", depth+1, seen)
	case *types.Map:
		if search.hitMap {
			return path, typeText(shown), true
		}
		return search.walk(typed.Elem(), path+"{}", depth+1, seen)
	case *types.Named:
		if seen[typed] {
			return "", "", false
		}
		seen[typed] = true
		if search.hitNamed != nil && search.hitNamed(typed) {
			return path, typeText(shown), true
		}
		if _, isMap := typed.Underlying().(*types.Map); isMap && search.hitMap {
			return path, typeText(shown), true
		}
		return search.walk(typed.Underlying(), path, depth+1, seen)
	case *types.Struct:
		return search.walkFields(typed, path, depth, seen)
	}
	return "", "", false
}

func (search typeSearch) walkFields(structType *types.Struct, path string, depth int, seen map[*types.Named]bool) (string, string, bool) {
	for index := 0; index < structType.NumFields(); index++ {
		field := structType.Field(index)
		if !field.Exported() && !field.Embedded() {
			continue
		}
		if reflect.StructTag(structType.Tag(index)).Get(search.tagKey) == "-" {
			continue
		}
		fieldPath := field.Name()
		if path != "" {
			fieldPath = path + "." + field.Name()
		}
		if foundPath, hit, found := search.walk(field.Type(), fieldPath, depth+1, seen); found {
			return foundPath, hit, true
		}
	}
	return "", "", false
}

// findMapIn reports how a marshal target holds a map: it is one, or a struct
// field reaches one. The vendor encodes maps in random key order (spec 6.1).
func findMapIn(target types.Type) (string, bool) {
	path, hit, found := typeSearch{tagKey: "bson", hitMap: true}.find(target)
	if !found {
		return "", false
	}
	if path == "" {
		return hit, true
	}
	return path + " (" + hit + ")", true
}

// findJSONVendorValue finds a vendor value that encoding/json reaches and renders
// through the value's own MarshalJSON or UnmarshalJSON.
func findJSONVendorValue(target types.Type) (path, hit string, found bool) {
	return typeSearch{tagKey: "json", hitNamed: func(named *types.Named) bool {
		return isVendorPackage(named.Obj().Pkg()) && (hasMethod(named, "MarshalJSON") || hasMethod(named, "UnmarshalJSON"))
	}}.find(target)
}
