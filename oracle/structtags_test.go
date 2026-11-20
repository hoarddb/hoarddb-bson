// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	vendorbsoncodec "go.mongodb.org/mongo-driver/bson/bsoncodec"

	"github.com/hoarddb/hoarddb-bson/bson/bsoncodec"
)

// tagFormCases covers every tag form the vendor's DefaultStructTagParser
// distinguishes: the bson key, the bare legacy form, other keys, and flags.
var tagFormCases = []struct {
	form      string
	fieldName string
	tag       reflect.StructTag
}{
	{"no tag", "Plain", ``},
	{"empty bson tag", "EmptyBSON", `bson:""`},
	{"name only", "Named", `bson:"custom"`},
	{"skip key", "Skipped", `bson:"-"`},
	{"skip bare", "SkippedBare", `-`},
	{"dash with trailing comma is a name", "DashComma", `bson:"-,"`},
	{"dash with flag is a name", "DashFlag", `bson:"-,omitempty"`},
	{"omitempty only", "OmitOnly", `bson:",omitempty"`},
	{"inline only", "InlineOnly", `bson:",inline"`},
	{"minsize only", "MinOnly", `bson:",minsize"`},
	{"truncate only", "TruncOnly", `bson:",truncate"`},
	{"name and omitempty", "NamedOmit", `bson:"n,omitempty"`},
	{"all flags", "AllFlags", `bson:"all,omitempty,minsize,truncate,inline"`},
	{"flags in another order", "Reordered", `bson:",truncate,inline,minsize,omitempty"`},
	{"empty name then empty flag", "EmptyFlag", `bson:",,omitempty"`},
	{"name with commas", "Commas", `bson:"a,b,c"`},
	{"unknown flag", "UnknownFlag", `bson:"x,unknown"`},
	{"flags are case sensitive", "CaseFlag", `bson:"x,OmitEmpty"`},
	{"flag spelled as the name", "FlagAsName", `bson:"omitempty"`},
	{"repeated flag", "Repeated", `bson:"x,omitempty,omitempty"`},
	{"json only", "JSONOnly", `json:"jsonkey"`},
	{"json with options", "JSONOpts", `json:"jsonkey,omitempty"`},
	{"json dash", "JSONDash", `json:"-"`},
	{"bson and json", "BothTags", `bson:"b" json:"j"`},
	{"empty bson and json", "EmptyBothTags", `bson:"" json:"j,omitempty"`},
	{"bson skip and json name", "SkipJSON", `json:"j" bson:"-"`},
	{"unrelated key", "OtherKey", `yaml:"y"`},
	{"bare name", "BareName", `myb`},
	{"bare name with flag", "BareFlag", `myc,omitempty`},
	{"bare flag only", "BareFlagOnly", `,minsize`},
	{"mixed case Go name", "MixedCASEName", `bson:",omitempty"`},
	{"unicode Go name", "Ünï", ``},
	{"space inside the key", "Spaced", `bson:"a b,omitempty"`},
}

func TestStructTagsMatchDefaultStructTagParser(t *testing.T) {
	for _, tagForm := range tagFormCases {
		t.Run(tagForm.form, func(t *testing.T) {
			field := reflect.StructField{Name: tagForm.fieldName, Tag: tagForm.tag}

			vendorTags, vendorErr := vendorbsoncodec.DefaultStructTagParser.ParseStructTags(field) //nolint:staticcheck // mirrored deprecated name
			libraryTags, libraryErr := bsoncodec.DefaultStructTagParser.ParseStructTags(field)
			if errorText(libraryErr) != errorText(vendorErr) {
				t.Fatalf("error: library %v, vendor %v", libraryErr, vendorErr)
			}

			vendorValue := reflect.ValueOf(vendorTags)
			libraryValue := reflect.ValueOf(libraryTags)
			for fieldIndex := 0; fieldIndex < vendorValue.NumField(); fieldIndex++ {
				name := vendorValue.Type().Field(fieldIndex).Name
				if got, want := libraryValue.FieldByName(name).Interface(), vendorValue.Field(fieldIndex).Interface(); got != want {
					t.Errorf("StructTags.%s: library %v, vendor %v", name, got, want)
				}
			}
		})
	}
}

func TestStructTagParserFuncAdaptsAFunction(t *testing.T) {
	want := bsoncodec.StructTags{Name: "adapted", Truncate: true}
	var parser bsoncodec.StructTagParserFunc = func(reflect.StructField) (bsoncodec.StructTags, error) {
		return want, nil
	}
	got, err := parser.ParseStructTags(reflect.StructField{Name: "Ignored"})
	if err != nil || got != want {
		t.Fatalf("ParseStructTags = %+v, %v; want %+v, nil", got, err, want)
	}
}

// describeTagType lists a type's fields (name, type, tag) and the methods of
// both T and *T, so two packages' types compare by shape.
func describeTagType(typ reflect.Type) []string {
	var lines []string
	for _, methodHolder := range []reflect.Type{typ, reflect.PointerTo(typ)} {
		for methodIndex := 0; methodIndex < methodHolder.NumMethod(); methodIndex++ {
			method := methodHolder.Method(methodIndex)
			lines = append(lines, "method "+method.Name+" "+method.Type.String())
		}
	}
	if typ.Kind() == reflect.Struct {
		for fieldIndex := 0; fieldIndex < typ.NumField(); fieldIndex++ {
			field := typ.Field(fieldIndex)
			lines = append(lines, "field "+field.Name+" "+field.Type.String()+" "+string(field.Tag))
		}
	}
	return append(lines, "kind "+typ.Kind().String())
}

// TestMirroredTagNamesKeepTheVendorsShape checks the names the spec mirrors
// (spec 5.1): the same fields in the same order, the same method sets with
// the same signatures, and the same type of the exported variable.
func TestMirroredTagNamesKeepTheVendorsShape(t *testing.T) {
	typePairs := []struct {
		name          string
		library, vend reflect.Type
	}{
		{"StructTags", reflect.TypeOf(bsoncodec.StructTags{}), reflect.TypeOf(vendorbsoncodec.StructTags{})},                                  //nolint:staticcheck // mirrored deprecated name
		{"StructTagParserFunc", reflect.TypeOf(bsoncodec.StructTagParserFunc(nil)), reflect.TypeOf(vendorbsoncodec.StructTagParserFunc(nil))}, //nolint:staticcheck // mirrored deprecated name
	}
	for _, pair := range typePairs {
		t.Run(pair.name, func(t *testing.T) {
			// Type strings carry the package name; both packages are named bsoncodec.
			if got, want := describeTagType(pair.library), describeTagType(pair.vend); !reflect.DeepEqual(got, want) {
				t.Errorf("shape differs:\nlibrary %q\nvendor  %q", got, want)
			}
		})
	}
	if got, want := reflect.TypeOf(bsoncodec.DefaultStructTagParser).String(), reflect.TypeOf(vendorbsoncodec.DefaultStructTagParser).String(); got != want { //nolint:staticcheck // mirrored deprecated name
		t.Errorf("DefaultStructTagParser type: library %s, vendor %s", got, want)
	}
}

// TestBsoncodecExportsOnlyTheMirroredTagNames pins spec 5.1: the library's
// bsoncodec holds StructTags, StructTagParserFunc and DefaultStructTagParser
// and nothing else, and each exists in the vendor.
func TestBsoncodecExportsOnlyTheMirroredTagNames(t *testing.T) {
	sourceFiles, err := os.ReadDir("../bson/bsoncodec")
	if err != nil {
		t.Fatalf("reading ../bson/bsoncodec: %v", err)
	}
	var exported []string
	for _, sourceFile := range sourceFiles {
		if !strings.HasSuffix(sourceFile.Name(), ".go") || strings.HasSuffix(sourceFile.Name(), "_test.go") {
			continue
		}
		parsedFile, err := parser.ParseFile(token.NewFileSet(), filepath.Join("../bson/bsoncodec", sourceFile.Name()), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", sourceFile.Name(), err)
		}
		for declName := range parsedFile.Scope.Objects {
			if ast.IsExported(declName) {
				exported = append(exported, declName)
			}
		}
	}
	sort.Strings(exported)
	want := []string{"DefaultStructTagParser", "StructTagParserFunc", "StructTags"}
	if !reflect.DeepEqual(exported, want) {
		t.Fatalf("exported names = %v, want %v", exported, want)
	}
}
