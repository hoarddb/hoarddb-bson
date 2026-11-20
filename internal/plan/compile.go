// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: derived from bson/bsoncodec/struct_codec.go (describeStructSlow, dominantField and byIndex): the field walk, the inline rules, the dominance sort and the error text are kept in rewritten code that builds an immutable plan per type through a private compile session, with an error instead of unbounded recursion for a struct that inlines itself.

package plan

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/hoarddb/hoarddb-bson/bson/bsoncodec"
)

// compileSession compiles the plans one Lookup miss needs. Its plans are
// private to the goroutine until compile publishes them, so a plan that a
// recursive type refers to while it is still being filled is never seen by
// another goroutine half-filled.
type compileSession struct {
	cache   *Cache
	pending map[reflect.Type]*Type
	// layouts memoizes struct layouts, which depend only on the inline
	// structure: a layout is built before any field's type plan is, so an
	// ordinary field never sees a half-built layout.
	layouts map[reflect.Type]structLayout
	// inlining holds the structs on the current chain of inline descents.
	// Inlining one of them again is a recursion the vendor never returns
	// from; reaching one through an ordinary field is not.
	inlining map[reflect.Type]bool
}

// structLayout is a struct's layout before its field types are planned.
type structLayout struct {
	fields         []Field // Type unset
	inlineMapType  reflect.Type
	inlineMapIndex int
	err            error
}

// errInlineRecursion marks a layout refused because its inline fields lead
// back to a struct on the inline chain.
var errInlineRecursion = errors.New("inlines a struct whose layout includes itself")

// compile builds the plan of root and of every type it reaches that is not
// yet published, then publishes them. When another goroutine published a
// type first, its plan is kept and this one's is dropped; the plans this
// session built still refer to each other, and are equal to the kept ones.
func (cache *Cache) compile(root reflect.Type) *Type {
	session := compileSession{
		cache:    cache,
		pending:  map[reflect.Type]*Type{},
		layouts:  map[reflect.Type]structLayout{},
		inlining: map[reflect.Type]bool{},
	}
	rootPlan := session.planFor(root)
	for goType, typePlan := range session.pending {
		published, _ := cache.plans.LoadOrStore(goType, typePlan)
		if goType == root {
			rootPlan = published.(*Type)
		}
	}
	return rootPlan
}

func (session *compileSession) planFor(goType reflect.Type) *Type {
	if published, found := session.cache.plans.Load(goType); found {
		return published.(*Type)
	}
	if pending, found := session.pending[goType]; found {
		return pending
	}
	typePlan := &Type{
		GoType: goType,
		Encode: choose(goType, session.cache.encodeTypes, encodeHooks),
		Decode: choose(goType, session.cache.decodeTypes, decodeHooks),
		Zeroer: goType.Implements(zeroerType),
	}
	// Registered before its element plans, so a type that reaches itself
	// gets this plan back.
	session.pending[goType] = typePlan
	switch goType.Kind() {
	case reflect.Pointer:
		typePlan.Elem = session.planFor(goType.Elem())
	case reflect.Slice:
		typePlan.Elem = session.planFor(goType.Elem())
		typePlan.ElemIsByte = goType.Elem() == byteType
		typePlan.ElemIsE = goType.Elem() == elementType
		typePlan.ConvertibleToD = goType.ConvertibleTo(documentType)
	case reflect.Array:
		typePlan.Elem = session.planFor(goType.Elem())
		typePlan.ElemIsByte = goType.Elem() == byteType
		typePlan.ElemIsE = goType.Elem() == elementType
	case reflect.Map:
		typePlan.Elem = session.planFor(goType.Elem())
		typePlan.KeyEncode = keyEncodeCodec(goType.Key())
		typePlan.KeyDecode = keyDecodeCodec(goType.Key())
	case reflect.Struct:
		typePlan.Struct, typePlan.Err = session.planStruct(goType)
	}
	return typePlan
}

// planStruct builds the layout first and only then plans the field types,
// which may lead back to this struct.
func (session *compileSession) planStruct(structType reflect.Type) (*Struct, error) {
	layout := session.layoutOf(structType)
	if errors.Is(layout.err, errInlineRecursion) {
		return nil, fmt.Errorf("(struct %s) %w", structType, errInlineRecursion)
	}
	if layout.err != nil {
		return nil, layout.err
	}
	planned := &Struct{
		Fields:         slices.Clone(layout.fields),
		InlineMapIndex: layout.inlineMapIndex,
		fieldsByKey:    make(map[string]int, len(layout.fields)),
	}
	for position := range planned.Fields {
		field := &planned.Fields[position]
		field.Type = session.planFor(fieldTypeAt(structType, field.Index))
		planned.fieldsByKey[field.Key] = position
	}
	if layout.inlineMapType != nil {
		planned.InlineMap = session.planFor(layout.inlineMapType)
	}
	return planned, nil
}

// fieldTypeAt walks an index path through struct and struct pointer types.
func fieldTypeAt(structType reflect.Type, index []int) reflect.Type {
	fieldType := structType
	for _, position := range index {
		if fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		fieldType = fieldType.Field(position).Type
	}
	return fieldType
}

// layoutOf returns a struct's layout, from a published plan, the session's
// memo, or by building it. A layout refused for inline recursion is not
// memoized, because whether it recurses depends on the chain that reached it.
func (session *compileSession) layoutOf(structType reflect.Type) structLayout {
	if published, found := session.cache.plans.Load(structType); found {
		typePlan := published.(*Type)
		if typePlan.Err != nil {
			if errors.Is(typePlan.Err, errInlineRecursion) {
				return structLayout{err: errInlineRecursion}
			}
			return structLayout{err: typePlan.Err}
		}
		layout := structLayout{fields: typePlan.Struct.Fields, inlineMapIndex: typePlan.Struct.InlineMapIndex}
		if typePlan.Struct.InlineMap != nil {
			layout.inlineMapType = typePlan.Struct.InlineMap.GoType
		}
		return layout
	}
	if memoized, found := session.layouts[structType]; found {
		return memoized
	}
	if session.inlining[structType] {
		return structLayout{err: errInlineRecursion}
	}
	session.inlining[structType] = true
	layout := session.buildLayout(structType)
	delete(session.inlining, structType)
	if !errors.Is(layout.err, errInlineRecursion) {
		session.layouts[structType] = layout
	}
	return layout
}

// buildLayout is the vendor's describeStructSlow with its default options:
// unexported fields skipped, duplicated inlined keys resolved by depth, and
// an error for two fields of the same key at the same depth.
func (session *compileSession) buildLayout(structType reflect.Type) structLayout {
	layout := structLayout{inlineMapIndex: -1}
	var candidates []Field
	for position := 0; position < structType.NumField(); position++ {
		structField := structType.Field(position)
		// AllowUnexportedFields is off, so an unexported embedded struct is
		// skipped too, inline tag or not (row N1).
		if structField.PkgPath != "" {
			continue
		}
		tags, err := bsoncodec.DefaultStructTagParser.ParseStructTags(structField)
		if err != nil {
			return structLayout{err: fmt.Errorf("parsing the tag of %s.%s: %w", structType, structField.Name, err)}
		}
		if tags.Skip {
			continue
		}
		if !tags.Inline {
			candidates = append(candidates, Field{
				Key:       tags.Name,
				Index:     []int{position},
				OmitEmpty: tags.OmitEmpty,
				MinSize:   tags.MinSize,
				Truncate:  tags.Truncate,
			})
			continue
		}
		inlined, err := session.inlineField(structType, structField, position, &layout)
		if err != nil {
			return structLayout{err: err}
		}
		candidates = append(candidates, inlined...)
	}
	fields, err := dominantFields(structType, candidates)
	if err != nil {
		return structLayout{err: err}
	}
	layout.fields = fields
	return layout
}

// inlineField expands one field tagged inline: a map becomes the layout's
// inline map, and a struct or struct pointer contributes its own layout's
// fields with their index paths prefixed by position. Only the inlined
// struct's fields are taken; an inline map inside it is dropped, as the
// vendor drops it.
func (session *compileSession) inlineField(structType reflect.Type, structField reflect.StructField, position int, layout *structLayout) ([]Field, error) {
	fieldType := structField.Type
	throughPointer := false
	switch fieldType.Kind() {
	case reflect.Map:
		if layout.inlineMapType != nil {
			return nil, errors.New("(struct " + structType.String() + ") multiple inline maps")
		}
		if fieldType.Key() != stringType {
			return nil, errors.New("(struct " + structType.String() + ") inline map must have a string keys")
		}
		layout.inlineMapType = fieldType
		layout.inlineMapIndex = position
		return nil, nil
	case reflect.Pointer:
		if fieldType.Elem().Kind() != reflect.Struct {
			return nil, fmt.Errorf("(struct %s) inline fields must be a struct, a struct pointer, or a map", structType.String())
		}
		fieldType = fieldType.Elem()
		throughPointer = true
	case reflect.Struct:
	default:
		return nil, fmt.Errorf("(struct %s) inline fields must be a struct, a struct pointer, or a map", structType.String())
	}
	innerLayout := session.layoutOf(fieldType)
	if innerLayout.err != nil {
		return nil, innerLayout.err
	}
	inlined := make([]Field, 0, len(innerLayout.fields))
	for _, innerField := range innerLayout.fields {
		innerField.Index = append([]int{position}, innerField.Index...)
		innerField.ThroughPointer = innerField.ThroughPointer || throughPointer
		innerField.Type = nil
		inlined = append(inlined, innerField)
	}
	return inlined, nil
}

// compareIndex is the vendor's byIndex order: index paths compared element
// by element, a path before the longer paths it begins.
func compareIndex(left, right []int) int {
	return slices.Compare(left, right)
}

// dominantFields keeps one field per key, as the vendor does: the shallowest
// (shortest index path) wins, two at the same depth are an error, and the
// survivors are put back in index order.
func dominantFields(structType reflect.Type, candidates []Field) ([]Field, error) {
	byKey := slices.Clone(candidates)
	slices.SortFunc(byKey, func(left, right Field) int {
		if order := strings.Compare(left.Key, right.Key); order != 0 {
			return order
		}
		if len(left.Index) != len(right.Index) {
			return len(left.Index) - len(right.Index)
		}
		return compareIndex(left.Index, right.Index)
	})
	dominant := make([]Field, 0, len(byKey))
	for start := 0; start < len(byKey); {
		end := start + 1
		for end < len(byKey) && byKey[end].Key == byKey[start].Key {
			end++
		}
		if end-start > 1 && len(byKey[start].Index) == len(byKey[start+1].Index) {
			return nil, fmt.Errorf("struct %s has duplicated key %s", structType.String(), byKey[start].Key)
		}
		dominant = append(dominant, byKey[start])
		start = end
	}
	slices.SortFunc(dominant, func(left, right Field) int {
		return compareIndex(left.Index, right.Index)
	})
	return dominant, nil
}
