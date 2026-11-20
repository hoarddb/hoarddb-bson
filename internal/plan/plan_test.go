// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package plan_test

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/internal/plan"
)

// newTestCache returns an empty cache, so every test's first lookup is a
// real first use.
func newTestCache() *plan.Cache {
	return plan.NewCache(plan.BSONTypes{
		Raw:      reflect.TypeOf(bson.Raw(nil)),
		RawValue: reflect.TypeOf(bson.RawValue{}),
	})
}

func lookup(t *testing.T, cache *plan.Cache, goType reflect.Type) *plan.Type {
	t.Helper()
	typePlan, err := cache.Lookup(goType)
	if err != nil {
		t.Fatalf("Lookup(%v): %v", goType, err)
	}
	return typePlan
}

// The recursive shapes of the server's catalog (catalogType.Elem,
// FieldDeclaration.Elem), a mutually recursive pair, and recursion through a
// slice and a map.
type catalogType struct {
	Kind string
	Elem *catalogType
}

type leftNode struct {
	Right *rightNode
}

type rightNode struct {
	Left   *leftNode
	Lefts  []leftNode
	ByName map[string]rightNode
}

type treeNode struct {
	Label    string
	Children []treeNode
}

// SelfInline inlines itself through a pointer. The vendor recurses without
// end on it; the plan reports an error instead.
type SelfInline struct {
	X           int
	*SelfInline `bson:",inline"`
}

// InlineCycleA and InlineCycleB inline each other.
type InlineCycleA struct {
	A             string
	*InlineCycleB `bson:",inline"`
}

type InlineCycleB struct {
	B             string
	*InlineCycleA `bson:",inline"`
}

// pointsAtInlineCycle refers to a bad type without inlining it, so only the
// bad type carries the error.
type pointsAtInlineCycle struct {
	Bad *SelfInline
}

func fieldNamed(t *testing.T, structPlan *plan.Type, key string) plan.Field {
	t.Helper()
	if structPlan.Struct == nil {
		t.Fatalf("%v has no struct layout (Err: %v)", structPlan.GoType, structPlan.Err)
	}
	for _, field := range structPlan.Struct.Fields {
		if field.Key == key {
			return field
		}
	}
	t.Fatalf("%v has no field %q", structPlan.GoType, key)
	return plan.Field{}
}

// checkComplete fails on every unfilled plan reachable from root.
func checkComplete(t *testing.T, root *plan.Type) {
	t.Helper()
	for _, problem := range incompletePlans(root) {
		t.Error(problem)
	}
}

// incompletePlans walks every plan reachable from root and reports a
// placeholder that was never filled: a struct plan with neither a layout nor
// an error, or a container plan without its element plan. It only reads, so
// a goroutine can run it on a plan it was just handed, and the race detector
// sees any plan still being written.
func incompletePlans(root *plan.Type) []string {
	var problems []string
	seen := map[*plan.Type]bool{}
	var walk func(*plan.Type)
	walk = func(typePlan *plan.Type) {
		if typePlan == nil || seen[typePlan] {
			return
		}
		seen[typePlan] = true
		switch typePlan.GoType.Kind() {
		case reflect.Struct:
			if typePlan.Struct == nil && typePlan.Err == nil {
				problems = append(problems, fmt.Sprintf("struct plan of %v was handed out unfilled", typePlan.GoType))
				return
			}
			if typePlan.Struct != nil {
				for _, field := range typePlan.Struct.Fields {
					walk(field.Type)
				}
				walk(typePlan.Struct.InlineMap)
			}
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			if typePlan.Elem == nil {
				problems = append(problems, fmt.Sprintf("container plan of %v has no element plan", typePlan.GoType))
				return
			}
			if typePlan.Elem.GoType != typePlan.GoType.Elem() {
				problems = append(problems, fmt.Sprintf("element plan of %v is for %v", typePlan.GoType, typePlan.Elem.GoType))
			}
			walk(typePlan.Elem)
		}
	}
	walk(root)
	return problems
}

func TestPlanCompilesRecursiveTypes(t *testing.T) {
	t.Run("self through a pointer", func(t *testing.T) {
		cache := newTestCache()
		catalogPlan := lookup(t, cache, reflect.TypeOf(catalogType{}))
		checkComplete(t, catalogPlan)
		elemField := fieldNamed(t, catalogPlan, "elem")
		if elemField.Type.GoType != reflect.TypeOf(&catalogType{}) {
			t.Fatalf("elem field plan is for %v", elemField.Type.GoType)
		}
		if elemField.Type.Elem != catalogPlan {
			t.Errorf("the cycle is not closed: *catalogType's element plan is not catalogType's plan")
		}
		if pointerPlan := lookup(t, cache, reflect.TypeOf(&catalogType{})); pointerPlan != elemField.Type {
			t.Errorf("a later lookup of *catalogType returned a different plan from the one the cycle uses")
		}
	})
	t.Run("mutually recursive pair", func(t *testing.T) {
		cache := newTestCache()
		leftPlan := lookup(t, cache, reflect.TypeOf(leftNode{}))
		checkComplete(t, leftPlan)
		rightPlan := lookup(t, cache, reflect.TypeOf(rightNode{}))
		if fieldNamed(t, leftPlan, "right").Type.Elem != rightPlan {
			t.Errorf("leftNode.Right does not lead to rightNode's cached plan")
		}
		if fieldNamed(t, rightPlan, "left").Type.Elem != leftPlan {
			t.Errorf("rightNode.Left does not lead back to leftNode's plan")
		}
		if fieldNamed(t, rightPlan, "lefts").Type.Elem != leftPlan {
			t.Errorf("rightNode.Lefts does not lead back to leftNode's plan")
		}
		if fieldNamed(t, rightPlan, "byname").Type.Elem != rightPlan {
			t.Errorf("rightNode.ByName does not lead back to rightNode's plan")
		}
	})
	t.Run("self through a slice", func(t *testing.T) {
		cache := newTestCache()
		treePlan := lookup(t, cache, reflect.TypeOf(treeNode{}))
		checkComplete(t, treePlan)
		if fieldNamed(t, treePlan, "children").Type.Elem != treePlan {
			t.Errorf("treeNode.Children does not lead back to treeNode's plan")
		}
	})
	t.Run("inline recursion is an error, not a stack overflow", func(t *testing.T) {
		cache := newTestCache()
		for _, goType := range []reflect.Type{reflect.TypeOf(SelfInline{}), reflect.TypeOf(InlineCycleA{}), reflect.TypeOf(InlineCycleB{})} {
			typePlan := lookup(t, cache, goType)
			if typePlan.Err == nil {
				t.Errorf("%v inlines itself, and its plan has no error", goType)
			}
			if typePlan.Struct != nil {
				t.Errorf("%v has an error and a layout", goType)
			}
		}
		holderPlan := lookup(t, cache, reflect.TypeOf(pointsAtInlineCycle{}))
		checkComplete(t, holderPlan)
		if holderPlan.Err != nil {
			t.Errorf("a struct that only points at a bad type got its error: %v", holderPlan.Err)
		}
		if fieldNamed(t, holderPlan, "bad").Type.Elem.Err == nil {
			t.Errorf("the pointed-at bad type lost its error")
		}
	})
}

// TestConcurrentFirstUseOfARecursiveType runs under -race in the gates. Every
// goroutine starts at once on an empty cache, so they all compile; the first
// published plan must be the one every goroutine gets back.
func TestConcurrentFirstUseOfARecursiveType(t *testing.T) {
	const goroutines = 64
	t.Run("one recursive type", func(t *testing.T) {
		cache := newTestCache()
		plans := lookUpTogether(t, goroutines, func(int) []reflect.Type {
			return []reflect.Type{reflect.TypeOf(catalogType{})}
		}, cache)
		first := plans[0][0]
		for goroutine, got := range plans {
			if got[0] != first {
				t.Errorf("goroutine %d got a different plan from goroutine 0", goroutine)
			}
		}
		checkComplete(t, first)
		if again := lookup(t, cache, reflect.TypeOf(catalogType{})); again != first {
			t.Errorf("the cache holds a different plan from the one every goroutine got")
		}
		if fieldNamed(t, first, "elem").Type.Elem.Struct == nil {
			t.Errorf("the plan reached through the cycle has no layout")
		}
	})
	t.Run("a mutually recursive pair entered from both ends", func(t *testing.T) {
		cache := newTestCache()
		pair := []reflect.Type{reflect.TypeOf(leftNode{}), reflect.TypeOf(rightNode{})}
		plans := lookUpTogether(t, goroutines, func(goroutine int) []reflect.Type {
			if goroutine%2 == 0 {
				return pair
			}
			return []reflect.Type{pair[1], pair[0]}
		}, cache)
		for _, goType := range pair {
			cached := lookup(t, cache, goType)
			checkComplete(t, cached)
			for goroutine, got := range plans {
				for _, typePlan := range got {
					if typePlan.GoType == goType && typePlan != cached {
						t.Errorf("goroutine %d got a plan of %v that is not the cached one", goroutine, goType)
					}
				}
			}
		}
	})
	t.Run("many distinct types", func(t *testing.T) {
		cache := newTestCache()
		distinct := distinctStructTypes(200)
		plans := lookUpTogether(t, goroutines, func(goroutine int) []reflect.Type {
			rotated := make([]reflect.Type, 0, len(distinct))
			start := goroutine * 7 % len(distinct)
			rotated = append(rotated, distinct[start:]...)
			return append(rotated, distinct[:start]...)
		}, cache)
		for _, goType := range distinct {
			cached := lookup(t, cache, goType)
			if len(cached.Struct.Fields) != 2 {
				t.Fatalf("%v: %d fields, want 2", goType, len(cached.Struct.Fields))
			}
			for goroutine, got := range plans {
				for _, typePlan := range got {
					if typePlan.GoType == goType && typePlan != cached {
						t.Errorf("goroutine %d got a plan of %v that is not the cached one", goroutine, goType)
					}
				}
			}
		}
	})
}

// lookUpTogether releases every goroutine at the same moment and returns the
// plans each one got, in its own lookup order. Each goroutine walks the plan
// it got at once, before any other goroutine is known to be done.
func lookUpTogether(t *testing.T, goroutines int, typesFor func(goroutine int) []reflect.Type, cache *plan.Cache) [][]*plan.Type {
	t.Helper()
	start := make(chan struct{})
	plans := make([][]*plan.Type, goroutines)
	lookupErrors := make([]error, goroutines)
	var finished sync.WaitGroup
	for goroutine := 0; goroutine < goroutines; goroutine++ {
		finished.Add(1)
		go func(goroutine int) {
			defer finished.Done()
			<-start
			for _, goType := range typesFor(goroutine) {
				typePlan, err := cache.Lookup(goType)
				if err != nil {
					lookupErrors[goroutine] = err
					return
				}
				if problems := incompletePlans(typePlan); len(problems) > 0 {
					lookupErrors[goroutine] = fmt.Errorf("plan of %v: %s", goType, strings.Join(problems, "; "))
					return
				}
				plans[goroutine] = append(plans[goroutine], typePlan)
			}
		}(goroutine)
	}
	close(start)
	finished.Wait()
	for goroutine, err := range lookupErrors {
		if err != nil {
			t.Fatalf("goroutine %d: %v", goroutine, err)
		}
	}
	return plans
}

// distinctStructTypes builds struct types no other test compiled, each with
// a field that points back at a shared recursive type.
func distinctStructTypes(count int) []reflect.Type {
	distinct := make([]reflect.Type, 0, count)
	for position := 0; position < count; position++ {
		distinct = append(distinct, reflect.StructOf([]reflect.StructField{
			{Name: fmt.Sprintf("Field%d", position), Type: reflect.TypeOf("")},
			{Name: "Catalog", Type: reflect.TypeOf(&catalogType{})},
		}))
	}
	return distinct
}

type allocProbe struct {
	Name    string `bson:"name"`
	Count   int
	Tags    []string
	Parents map[string]*allocProbe
}

func TestPlanCacheHitAllocatesNothing(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector changes allocation counts")
	}
	cache := newTestCache()
	for _, goType := range []reflect.Type{
		reflect.TypeOf(allocProbe{}),
		reflect.TypeOf(&allocProbe{}),
		reflect.TypeOf(map[string]interface{}{}),
		reflect.TypeOf((*interface{})(nil)).Elem(),
		reflect.TypeOf(bson.D{}),
		reflect.TypeOf(catalogType{}),
	} {
		lookup(t, cache, goType)
		allocations := testing.AllocsPerRun(1000, func() {
			if _, err := cache.Lookup(goType); err != nil {
				t.Fatalf("Lookup(%v): %v", goType, err)
			}
		})
		if allocations != 0 {
			t.Errorf("a cache hit for %v allocates %.1f times per lookup, want 0", goType, allocations)
		}
	}
	probePlan := lookup(t, cache, reflect.TypeOf(allocProbe{}))
	// A key with an upper-case letter is lower-cased for the second lookup,
	// which allocates, as the vendor's strings.ToLower does; these do not.
	for _, key := range []string{"name", "count", "missing"} {
		allocations := testing.AllocsPerRun(1000, func() {
			probePlan.Struct.FieldByKey(key)
		})
		if allocations != 0 {
			t.Errorf("FieldByKey(%q) allocates %.1f times, want 0", key, allocations)
		}
	}
}

func TestLookupRefusesANilType(t *testing.T) {
	typePlan, err := newTestCache().Lookup(nil)
	if err == nil || typePlan != nil {
		t.Fatalf("Lookup(nil) = %v, %v; want nil and an error", typePlan, err)
	}
}
