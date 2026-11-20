// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package encode_test

import (
	"bytes"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/internal/encode"
	"github.com/hoarddb/hoarddb-bson/internal/plan"
)

type hinted struct {
	Name  string
	Items []int
}

// TestSizeHintIsTheLastEncodedSize holds the per-plan size hint (spec 5.5)
// to its definition: after a top-level encode, the plan of the value's
// type holds that encoding's length.
func TestSizeHintIsTheLastEncodedSize(t *testing.T) {
	cache := plan.NewCache(plan.BSONTypes{Raw: reflect.TypeOf(bson.Raw(nil)), RawValue: reflect.TypeOf(bson.RawValue{})})
	encoder := encode.New(cache)
	typePlan, err := cache.Lookup(reflect.TypeOf(hinted{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []hinted{{Name: "short"}, {Name: "a longer name", Items: []int{1, 2, 3, 4, 5}}, {}} {
		encoded, err := encoder.Document(value)
		if err != nil {
			t.Fatal(err)
		}
		if hint := typePlan.SizeHint.Load(); int(hint) != len(encoded) {
			t.Errorf("after encoding %+v (%d bytes) the size hint is %d", value, len(encoded), hint)
		}
	}
}

type concurrentShape struct {
	ID     int
	Labels map[string]string
	Nested []map[string]int
	Extra  map[string]interface{} `bson:",inline"`
}

func concurrentValue(goroutine, run int) concurrentShape {
	return concurrentShape{
		ID:     goroutine*1000 + run,
		Labels: map[string]string{"g": fmt.Sprint(goroutine), "r": fmt.Sprint(run), "pad": fmt.Sprintf("%0*d", run%40, run)},
		Nested: []map[string]int{{"z": run, "a": goroutine}, {"m": run % 7}},
		Extra:  map[string]interface{}{"x": run, "w": []int{goroutine}},
	}
}

// TestConcurrentEncodesAreIndependent encodes from 32 goroutines at once
// through one encoder, so the pooled writers and the map entry scratch are
// shared across goroutines (run under -race in the gates), and requires
// every result to equal the same value encoded alone.
func TestConcurrentEncodesAreIndependent(t *testing.T) {
	cache := plan.NewCache(plan.BSONTypes{Raw: reflect.TypeOf(bson.Raw(nil)), RawValue: reflect.TypeOf(bson.RawValue{})})
	encoder := encode.New(cache)
	const goroutines, runs = 32, 100
	want := make([][][]byte, goroutines)
	for goroutine := range want {
		want[goroutine] = make([][]byte, runs)
		for run := range want[goroutine] {
			encoded, err := encoder.Document(concurrentValue(goroutine, run))
			if err != nil {
				t.Fatal(err)
			}
			want[goroutine][run] = encoded
		}
	}
	var wait sync.WaitGroup
	failures := make(chan string, goroutines)
	for goroutine := 0; goroutine < goroutines; goroutine++ {
		wait.Add(1)
		go func(goroutine int) {
			defer wait.Done()
			for run := 0; run < runs; run++ {
				encoded, err := encoder.Document(concurrentValue(goroutine, run))
				if err != nil || !bytes.Equal(encoded, want[goroutine][run]) {
					failures <- fmt.Sprintf("goroutine %d run %d: err %v, bytes % x, want % x", goroutine, run, err, encoded, want[goroutine][run])
					return
				}
			}
		}(goroutine)
	}
	wait.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
}
