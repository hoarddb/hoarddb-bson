// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package compare

import (
	"reflect"
	"strings"
)

// structField is the Go value behind one element name of a struct level.
type structField struct {
	goValue reflect.Value
	depth   int
	// ambiguous marks a name two fields at the same inline depth claim; its
	// element compares raw rather than against a guessed field.
	ambiguous bool
}

// structShape compares a struct level: its fields in order, then the entries
// of its inline map, if it has one, as a set. The vendor writes a struct's
// fields first and its inline map after them (bsoncodec StructCodec.EncodeValue).
func structShape(goStruct reflect.Value) documentShape {
	fields := map[string]structField{}
	inlineMap := collectStructFields(goStruct, 0, fields)
	childValue := func(key string, _ int) reflect.Value {
		if field, found := fields[key]; found {
			if field.ambiguous {
				return reflect.Value{}
			}
			return field.goValue
		}
		if inlineMap.IsValid() {
			return inlineMap.MapIndex(reflect.ValueOf(key).Convert(inlineMap.Type().Key()))
		}
		return reflect.Value{}
	}
	if !inlineMap.IsValid() {
		return documentShape{orderedPrefix: allInOrder, childValue: childValue}
	}
	// The ordered part ends at the first element that is not a field. A
	// library that interleaved an inline entry among the fields shortens its
	// ordered part, and the in-order comparison reports it.
	orderedPrefix := func(keys []string) int {
		for position, key := range keys {
			if _, isField := fields[key]; !isField {
				return position
			}
		}
		return len(keys)
	}
	return documentShape{orderedPrefix: orderedPrefix, childValue: childValue}
}

// collectStructFields records the element name of every encoded field of
// goStruct, following inline structs, with the vendor's dominance rule: a
// shallower field wins its name. It returns the struct's own inline map; the
// vendor ignores an inline map of an inlined struct.
func collectStructFields(goStruct reflect.Value, depth int, fields map[string]structField) reflect.Value {
	var inlineMap reflect.Value
	var inlineStructs []reflect.Value
	structType := goStruct.Type()
	for fieldIndex := 0; fieldIndex < structType.NumField(); fieldIndex++ {
		fieldType := structType.Field(fieldIndex)
		if fieldType.PkgPath != "" {
			continue // unexported: the vendor's default codec skips it
		}
		key, skip, inline := parseBSONTag(fieldType)
		fieldValue := goStruct.Field(fieldIndex)
		switch {
		case skip:
		case inline && fieldType.Type.Kind() == reflect.Map:
			inlineMap = fieldValue
		case inline:
			if inlined := concreteValue(fieldValue); inlined.IsValid() && inlined.Kind() == reflect.Struct {
				inlineStructs = append(inlineStructs, inlined)
			}
		default:
			recordStructField(fields, key, structField{goValue: fieldValue, depth: depth})
		}
	}
	for _, inlined := range inlineStructs {
		collectStructFields(inlined, depth+1, fields)
	}
	return inlineMap
}

func recordStructField(fields map[string]structField, key string, candidate structField) {
	existing, found := fields[key]
	switch {
	case !found || candidate.depth < existing.depth:
		fields[key] = candidate
	case candidate.depth == existing.depth:
		existing.ambiguous = true
		fields[key] = existing
	}
}

// parseBSONTag mirrors the vendor's bsoncodec.DefaultStructTagParser: the
// element name is the tag's first item, or the lower-cased field name, and a
// tag with no key:"value" pairs is read as a bson tag.
func parseBSONTag(fieldType reflect.StructField) (key string, skip, inline bool) {
	tag, found := fieldType.Tag.Lookup("bson")
	if !found && !strings.Contains(string(fieldType.Tag), ":") && len(fieldType.Tag) > 0 {
		tag = string(fieldType.Tag)
	}
	if tag == "-" {
		return "", true, false
	}
	key = strings.ToLower(fieldType.Name)
	for itemIndex, item := range strings.Split(tag, ",") {
		if itemIndex == 0 && item != "" {
			key = item
		}
		if item == "inline" {
			inline = true
		}
	}
	return key, false, inline
}
