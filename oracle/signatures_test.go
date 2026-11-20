// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorbsontype "go.mongodb.org/mongo-driver/bson/bsontype"
	vendorprimitive "go.mongodb.org/mongo-driver/bson/primitive"
	vendorbsoncore "go.mongodb.org/mongo-driver/x/bsonx/bsoncore"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/x/bsonx/bsoncore"
)

// vendorToLibraryPath maps a vendor package path to the library's, so a
// vendor signature can be compared with the library's as text.
var vendorToLibraryPath = strings.NewReplacer(
	"go.mongodb.org/mongo-driver/", "github.com/hoarddb/hoarddb-bson/",
)

// qualifiedType writes a type with full package paths, mapped from the
// vendor's to the library's, and walks unnamed types, so two signatures
// that print the same through reflect.Type.String (which uses the short
// package name) still differ when one names a type of the wrong package.
func qualifiedType(goType reflect.Type) string {
	if goType.Name() != "" {
		if goType.PkgPath() == "" {
			return goType.Name()
		}
		return vendorToLibraryPath.Replace(goType.PkgPath()) + "." + goType.Name()
	}
	switch goType.Kind() {
	case reflect.Slice:
		return "[]" + qualifiedType(goType.Elem())
	case reflect.Array:
		return fmt.Sprintf("[%d]%s", goType.Len(), qualifiedType(goType.Elem()))
	case reflect.Pointer:
		return "*" + qualifiedType(goType.Elem())
	case reflect.Map:
		return "map[" + qualifiedType(goType.Key()) + "]" + qualifiedType(goType.Elem())
	case reflect.Func:
		return qualifiedSignature(goType, 0)
	default:
		return goType.String()
	}
}

// qualifiedSignature writes a function type's parameters (from firstIn, so
// a method's receiver can be skipped), results and variadic flag.
func qualifiedSignature(functionType reflect.Type, firstIn int) string {
	var parameters, results []string
	for parameterIndex := firstIn; parameterIndex < functionType.NumIn(); parameterIndex++ {
		parameters = append(parameters, qualifiedType(functionType.In(parameterIndex)))
	}
	for resultIndex := range functionType.NumOut() {
		results = append(results, qualifiedType(functionType.Out(resultIndex)))
	}
	return fmt.Sprintf("func(%s) (%s) variadic=%t", strings.Join(parameters, ", "), strings.Join(results, ", "),
		functionType.IsVariadic())
}

// qualifiedStruct writes a struct's fields in order, with their types and tags.
func qualifiedStruct(structType reflect.Type) string {
	var fields []string
	for fieldIndex := range structType.NumField() {
		field := structType.Field(fieldIndex)
		fields = append(fields, fmt.Sprintf("%s %s %q", field.Name, qualifiedType(field.Type), field.Tag))
	}
	return "struct{" + strings.Join(fields, "; ") + "}"
}

// mirroredFunctions pairs every function the library's bsoncore exports with
// the vendor's. TestBsoncoreFunctionsAreAllPaired proves the list covers
// the package, so a function added to the library without a row fails.
var mirroredFunctions = []struct {
	name    string
	library any
	vendor  any
}{
	{"AppendArrayElement", bsoncore.AppendArrayElement, vendorbsoncore.AppendArrayElement},
	{"AppendArrayEnd", bsoncore.AppendArrayEnd, vendorbsoncore.AppendArrayEnd},
	{"AppendArrayStart", bsoncore.AppendArrayStart, vendorbsoncore.AppendArrayStart},
	{"AppendBinaryElement", bsoncore.AppendBinaryElement, vendorbsoncore.AppendBinaryElement},
	{"AppendBooleanElement", bsoncore.AppendBooleanElement, vendorbsoncore.AppendBooleanElement},
	{"AppendDateTimeElement", bsoncore.AppendDateTimeElement, vendorbsoncore.AppendDateTimeElement},
	{"AppendDocumentElement", bsoncore.AppendDocumentElement, vendorbsoncore.AppendDocumentElement},
	{"AppendDocumentEnd", bsoncore.AppendDocumentEnd, vendorbsoncore.AppendDocumentEnd},
	{"AppendDocumentStart", bsoncore.AppendDocumentStart, vendorbsoncore.AppendDocumentStart},
	{"AppendDoubleElement", bsoncore.AppendDoubleElement, vendorbsoncore.AppendDoubleElement},
	{"AppendHeader", bsoncore.AppendHeader, vendorbsoncore.AppendHeader},
	{"AppendInt32Element", bsoncore.AppendInt32Element, vendorbsoncore.AppendInt32Element},
	{"AppendInt64Element", bsoncore.AppendInt64Element, vendorbsoncore.AppendInt64Element},
	{"AppendObjectIDElement", bsoncore.AppendObjectIDElement, vendorbsoncore.AppendObjectIDElement},
	{"AppendStringElement", bsoncore.AppendStringElement, vendorbsoncore.AppendStringElement},
	{"BuildArray", bsoncore.BuildArray, vendorbsoncore.BuildArray},
	{"BuildDocument", bsoncore.BuildDocument, vendorbsoncore.BuildDocument},
	{"ReadBinary", bsoncore.ReadBinary, vendorbsoncore.ReadBinary},
	{"ReadBoolean", bsoncore.ReadBoolean, vendorbsoncore.ReadBoolean},
	{"ReadDocument", bsoncore.ReadDocument, vendorbsoncore.ReadDocument},
	{"ReadElement", bsoncore.ReadElement, vendorbsoncore.ReadElement},
	{"ReadInt32", bsoncore.ReadInt32, vendorbsoncore.ReadInt32},
	{"ReadInt64", bsoncore.ReadInt64, vendorbsoncore.ReadInt64},
	{"ReadLength", bsoncore.ReadLength, vendorbsoncore.ReadLength},
}

// mirroredTypes pairs every type of bsoncore and bsontype the library has
// built so far. A struct compares field by field; every type compares its
// exported method set, receivers dropped.
var mirroredTypes = []struct {
	name    string
	library reflect.Type
	vendor  reflect.Type
}{
	{"bsoncore.Value", reflect.TypeOf(bsoncore.Value{}), reflect.TypeOf(vendorbsoncore.Value{})},
	{"bsoncore.Document", reflect.TypeOf(bsoncore.Document{}), reflect.TypeOf(vendorbsoncore.Document{})},
	{"bsoncore.Element", reflect.TypeOf(bsoncore.Element{}), reflect.TypeOf(vendorbsoncore.Element{})},
	{"bsoncore.Array", reflect.TypeOf(bsoncore.Array{}), reflect.TypeOf(vendorbsoncore.Array{})},
	{"bsontype.Type", reflect.TypeOf(bsontype.Type(0)), reflect.TypeOf(vendorbsontype.Type(0))},
}

func TestFunctionSignaturesMatchVendor(t *testing.T) {
	for _, function := range mirroredFunctions {
		librarySignature := qualifiedType(reflect.TypeOf(function.library))
		vendorSignature := qualifiedType(reflect.TypeOf(function.vendor))
		if librarySignature != vendorSignature {
			t.Errorf("%s:\n library %s\n  vendor %s", function.name, librarySignature, vendorSignature)
		}
	}
	for _, typePair := range mirroredTypes {
		if typePair.library.Kind() != typePair.vendor.Kind() {
			t.Errorf("%s: kind %s, the vendor's is %s", typePair.name, typePair.library.Kind(), typePair.vendor.Kind())
			continue
		}
		if typePair.library.Kind() == reflect.Struct {
			if libraryFields, vendorFields := qualifiedStruct(typePair.library), qualifiedStruct(typePair.vendor); libraryFields != vendorFields {
				t.Errorf("%s fields:\n library %s\n  vendor %s", typePair.name, libraryFields, vendorFields)
			}
		}
		libraryMethods, vendorMethods := qualifiedMethods(typePair.library), qualifiedMethods(typePair.vendor)
		if strings.Join(libraryMethods, "\n") != strings.Join(vendorMethods, "\n") {
			t.Errorf("%s methods:\n library %v\n  vendor %v", typePair.name, libraryMethods, vendorMethods)
		}
	}
}

func qualifiedMethods(goType reflect.Type) []string {
	var methods []string
	for methodIndex := range goType.NumMethod() {
		method := goType.Method(methodIndex)
		methods = append(methods, method.Name+" "+qualifiedSignature(method.Type, 1))
	}
	return methods
}

// TestBsoncoreFunctionsAreAllPaired fails when the library's bsoncore
// exports a function or a type mirroredFunctions or mirroredTypes does not
// pair, so the signature test grows with the package.
func TestBsoncoreFunctionsAreAllPaired(t *testing.T) {
	paired := map[string]bool{}
	for _, function := range mirroredFunctions {
		paired[function.name] = true
	}
	for _, typePair := range mirroredTypes {
		paired[strings.TrimPrefix(typePair.name, "bsoncore.")] = true
	}
	for _, name := range exportedNamesOf(t, "github.com/hoarddb/hoarddb-bson/x/bsonx/bsoncore") {
		if !paired[name] {
			t.Errorf("bsoncore exports %s, which TestFunctionSignaturesMatchVendor does not compare", name)
		}
	}
}

// TestSignatureCompareSeesPackagePaths is the compare's own canary: two
// types with the same short name in different packages must not compare
// equal, and a vendor path must map onto the library's.
func TestSignatureCompareSeesPackagePaths(t *testing.T) {
	if qualifiedType(reflect.TypeOf(bsontype.Type(0))) != qualifiedType(reflect.TypeOf(vendorbsontype.Type(0))) {
		t.Fatal("the vendor's bsontype.Type does not map onto the library's")
	}
	type Type byte
	if qualifiedType(reflect.TypeOf(Type(0))) == qualifiedType(reflect.TypeOf(bsontype.Type(0))) {
		t.Fatal("a Type of another package compares equal to bsontype.Type")
	}
	if reflect.TypeOf(Type(0)).Kind() != reflect.TypeOf(bsontype.Type(0)).Kind() {
		t.Fatal("canary types differ in kind, so the canary proves nothing")
	}
}

// rawTypePairs are the bson types of step 4a and the primitive types the
// aliases name, each with the vendor's.
var rawTypePairs = []struct {
	name    string
	library reflect.Type
	vendor  reflect.Type
	later   map[string]string
}{
	{"bson.Raw", reflect.TypeOf(bson.Raw{}), reflect.TypeOf(vendorbson.Raw{}), nil},
	{"bson.RawElement", reflect.TypeOf(bson.RawElement{}), reflect.TypeOf(vendorbson.RawElement{}), rawElementMethodsLater},
	{"bson.RawValue", reflect.TypeOf(bson.RawValue{}), reflect.TypeOf(vendorbson.RawValue{}), rawValueMethodsLater},
	{"primitive.D", reflect.TypeOf(primitive.D{}), reflect.TypeOf(vendorprimitive.D{}), nil},
	{"primitive.E", reflect.TypeOf(primitive.E{}), reflect.TypeOf(vendorprimitive.E{}), nil},
	{"primitive.M", reflect.TypeOf(primitive.M{}), reflect.TypeOf(vendorprimitive.M{}), nil},
	{"primitive.A", reflect.TypeOf(primitive.A{}), reflect.TypeOf(vendorprimitive.A{}), nil},
}

// qualifiedExportedFields writes a struct's exported fields in order, and
// returns the names of its unexported ones.
func qualifiedExportedFields(structType reflect.Type) (exported string, unexported []string) {
	var fields []string
	for fieldIndex := range structType.NumField() {
		field := structType.Field(fieldIndex)
		if !field.IsExported() {
			unexported = append(unexported, field.Name)
			continue
		}
		fields = append(fields, fmt.Sprintf("%s %s %q", field.Name, qualifiedType(field.Type), field.Tag))
	}
	return "struct{" + strings.Join(fields, "; ") + "}", unexported
}

// TestRawMethodSetsMatchVendor compares the kinds, the exported fields and
// the method sets (value and pointer receivers, signatures with package
// paths) of Raw, RawElement, RawValue and the primitive D, E, M and A with
// the vendor's, less the methods a later step adds. RawValue's one
// unexported field, the vendor's registry, is not mirrored: it is set only
// by the vendor's decoder, and step 4f decides what RawValue.Unmarshal needs.
func TestRawMethodSetsMatchVendor(t *testing.T) {
	for _, typePair := range rawTypePairs {
		if typePair.library.Kind() != typePair.vendor.Kind() {
			t.Errorf("%s: kind %s, the vendor's is %s", typePair.name, typePair.library.Kind(), typePair.vendor.Kind())
			continue
		}
		if qualifiedType(typePair.library) != qualifiedType(typePair.vendor) {
			t.Errorf("%s: the library's type is %s, the vendor's maps to %s", typePair.name, qualifiedType(typePair.library), qualifiedType(typePair.vendor))
		}
		if typePair.library.Kind() == reflect.Slice || typePair.library.Kind() == reflect.Map {
			if qualifiedType(typePair.library.Elem()) != qualifiedType(typePair.vendor.Elem()) {
				t.Errorf("%s: element type %s, the vendor's maps to %s", typePair.name, qualifiedType(typePair.library.Elem()), qualifiedType(typePair.vendor.Elem()))
			}
		}
		if typePair.library.Kind() == reflect.Struct {
			libraryFields, libraryUnexported := qualifiedExportedFields(typePair.library)
			vendorFields, vendorUnexported := qualifiedExportedFields(typePair.vendor)
			if libraryFields != vendorFields {
				t.Errorf("%s fields:\n library %s\n  vendor %s", typePair.name, libraryFields, vendorFields)
			}
			wantUnexported := map[string]string{"bson.RawValue": "r"}[typePair.name]
			if strings.Join(vendorUnexported, " ") != wantUnexported {
				t.Errorf("%s: the vendor's unexported fields are %v, the test expects %q; look at what the new one does", typePair.name, vendorUnexported, wantUnexported)
			}
			if len(libraryUnexported) > 0 {
				t.Errorf("%s: the library has unexported fields %v that the mirror does not account for", typePair.name, libraryUnexported)
			}
		}
		for _, receiver := range []struct {
			label           string
			library, vendor reflect.Type
		}{{"value", typePair.library, typePair.vendor}, {"pointer", reflect.PointerTo(typePair.library), reflect.PointerTo(typePair.vendor)}} {
			var vendorMethods []string
			for _, method := range qualifiedMethods(receiver.vendor) {
				if _, later := typePair.later[strings.Fields(method)[0]]; !later {
					vendorMethods = append(vendorMethods, method)
				}
			}
			if libraryMethods := qualifiedMethods(receiver.library); strings.Join(libraryMethods, "\n") != strings.Join(vendorMethods, "\n") {
				t.Errorf("%s %s methods:\n library %v\n  vendor %v", typePair.name, receiver.label, libraryMethods, vendorMethods)
			}
		}
	}
}
