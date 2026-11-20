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
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	vendorbsontype "go.mongodb.org/mongo-driver/bson/bsontype"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// typeConstants pairs every element-type constant of the two packages by
// name. TestBsontypeExportsTheVendorSurface proves the list is complete. The
// vendor marks its constants deprecated in favour of bson.Type*, and they
// are still what it ships, so they are the oracle.
//
//nolint:staticcheck // SA1019: the deprecated vendor constants are the oracle.
var typeConstants = []struct {
	name    string
	library bsontype.Type
	vendor  vendorbsontype.Type
}{
	{"Double", bsontype.Double, vendorbsontype.Double},
	{"String", bsontype.String, vendorbsontype.String},
	{"EmbeddedDocument", bsontype.EmbeddedDocument, vendorbsontype.EmbeddedDocument},
	{"Array", bsontype.Array, vendorbsontype.Array},
	{"Binary", bsontype.Binary, vendorbsontype.Binary},
	{"Undefined", bsontype.Undefined, vendorbsontype.Undefined},
	{"ObjectID", bsontype.ObjectID, vendorbsontype.ObjectID},
	{"Boolean", bsontype.Boolean, vendorbsontype.Boolean},
	{"DateTime", bsontype.DateTime, vendorbsontype.DateTime},
	{"Null", bsontype.Null, vendorbsontype.Null},
	{"Regex", bsontype.Regex, vendorbsontype.Regex},
	{"DBPointer", bsontype.DBPointer, vendorbsontype.DBPointer},
	{"JavaScript", bsontype.JavaScript, vendorbsontype.JavaScript},
	{"Symbol", bsontype.Symbol, vendorbsontype.Symbol},
	{"CodeWithScope", bsontype.CodeWithScope, vendorbsontype.CodeWithScope},
	{"Int32", bsontype.Int32, vendorbsontype.Int32},
	{"Timestamp", bsontype.Timestamp, vendorbsontype.Timestamp},
	{"Int64", bsontype.Int64, vendorbsontype.Int64},
	{"Decimal128", bsontype.Decimal128, vendorbsontype.Decimal128},
	{"MinKey", bsontype.MinKey, vendorbsontype.MinKey},
	{"MaxKey", bsontype.MaxKey, vendorbsontype.MaxKey},
}

// subtypeConstants pairs every binary-subtype constant by name.
//
//nolint:staticcheck // SA1019: the deprecated vendor constants are the oracle.
var subtypeConstants = []struct {
	name    string
	library byte
	vendor  byte
}{
	{"BinaryGeneric", bsontype.BinaryGeneric, vendorbsontype.BinaryGeneric},
	{"BinaryFunction", bsontype.BinaryFunction, vendorbsontype.BinaryFunction},
	{"BinaryBinaryOld", bsontype.BinaryBinaryOld, vendorbsontype.BinaryBinaryOld},
	{"BinaryUUIDOld", bsontype.BinaryUUIDOld, vendorbsontype.BinaryUUIDOld},
	{"BinaryUUID", bsontype.BinaryUUID, vendorbsontype.BinaryUUID},
	{"BinaryMD5", bsontype.BinaryMD5, vendorbsontype.BinaryMD5},
	{"BinaryEncrypted", bsontype.BinaryEncrypted, vendorbsontype.BinaryEncrypted},
	{"BinaryColumn", bsontype.BinaryColumn, vendorbsontype.BinaryColumn},
	{"BinarySensitive", bsontype.BinarySensitive, vendorbsontype.BinarySensitive},
	{"BinaryUserDefined", bsontype.BinaryUserDefined, vendorbsontype.BinaryUserDefined},
}

func TestBsontypeMatchesVendor(t *testing.T) {
	for _, constant := range typeConstants {
		if byte(constant.library) != byte(constant.vendor) {
			t.Errorf("%s is 0x%02x, the vendor's is 0x%02x", constant.name, byte(constant.library), byte(constant.vendor))
		}
	}
	for _, constant := range subtypeConstants {
		if constant.library != constant.vendor {
			t.Errorf("%s is 0x%02x, the vendor's is 0x%02x", constant.name, constant.library, constant.vendor)
		}
	}
	// Every byte, not only the named ones: String and IsValid are total.
	for typeByte := 0; typeByte < 256; typeByte++ {
		libraryType, vendorType := bsontype.Type(typeByte), vendorbsontype.Type(typeByte)
		if libraryType.String() != vendorType.String() {
			t.Errorf("Type(0x%02x).String() is %q, the vendor's is %q", typeByte, libraryType.String(), vendorType.String())
		}
		if libraryType.IsValid() != vendorType.IsValid() {
			t.Errorf("Type(0x%02x).IsValid() is %v, the vendor's is %v", typeByte, libraryType.IsValid(), vendorType.IsValid())
		}
	}
}

func TestBsontypeMethodSetMatchesVendor(t *testing.T) {
	libraryType, vendorType := reflect.TypeOf(bsontype.Type(0)), reflect.TypeOf(vendorbsontype.Type(0))
	if libraryType.Kind() != vendorType.Kind() {
		t.Fatalf("Type is a %s, the vendor's is a %s", libraryType.Kind(), vendorType.Kind())
	}
	for _, typeOf := range []reflect.Type{libraryType, reflect.PointerTo(libraryType)} {
		vendorOf := vendorType
		if typeOf.Kind() == reflect.Pointer {
			vendorOf = reflect.PointerTo(vendorType)
		}
		if typeOf.NumMethod() != vendorOf.NumMethod() {
			t.Errorf("%s has %d methods, the vendor's %s has %d", typeOf, typeOf.NumMethod(), vendorOf, vendorOf.NumMethod())
		}
		for methodIndex := 0; methodIndex < vendorOf.NumMethod(); methodIndex++ {
			vendorMethod := vendorOf.Method(methodIndex)
			libraryMethod, found := typeOf.MethodByName(vendorMethod.Name)
			if !found {
				t.Errorf("%s has no method %s", typeOf, vendorMethod.Name)
				continue
			}
			// Both receivers differ by package, so compare the signature
			// with the receiver dropped.
			if got, want := signatureOf(libraryMethod.Type), signatureOf(vendorMethod.Type); got != want {
				t.Errorf("%s.%s is %s, the vendor's is %s", typeOf, vendorMethod.Name, got, want)
			}
		}
	}
}

// signatureOf writes a method's parameter and result kinds without its
// receiver.
func signatureOf(methodType reflect.Type) string {
	var signature strings.Builder
	signature.WriteString("(")
	for parameterIndex := 1; parameterIndex < methodType.NumIn(); parameterIndex++ {
		signature.WriteString(methodType.In(parameterIndex).String() + ",")
	}
	signature.WriteString(")(")
	for resultIndex := 0; resultIndex < methodType.NumOut(); resultIndex++ {
		signature.WriteString(methodType.Out(resultIndex).String() + ",")
	}
	signature.WriteString(")")
	return signature.String()
}

// TestBsontypeExportsTheVendorSurface reads both packages' sources and
// compares their exported top-level names, so a constant the vendor adds,
// or one this package invents, fails here instead of going unpaired.
func TestBsontypeExportsTheVendorSurface(t *testing.T) {
	libraryNames := exportedNamesOf(t, "github.com/hoarddb/hoarddb-bson/bson/bsontype")
	vendorNames := exportedNamesOf(t, "go.mongodb.org/mongo-driver/bson/bsontype")
	if strings.Join(libraryNames, " ") != strings.Join(vendorNames, " ") {
		t.Fatalf("exported names differ\n library: %v\n  vendor: %v", libraryNames, vendorNames)
	}
	paired := map[string]bool{"Type": true}
	for _, constant := range typeConstants {
		paired[constant.name] = true
	}
	for _, constant := range subtypeConstants {
		paired[constant.name] = true
	}
	for _, exportedName := range vendorNames {
		if !paired[exportedName] {
			t.Errorf("the vendor exports %s and the differential does not pair it", exportedName)
		}
	}
}

// exportedNamesOf lists, sorted, the exported top-level declarations of the
// non-test files of an importable package.
func exportedNamesOf(t *testing.T, importPath string) []string {
	t.Helper()
	listCommand := exec.Command("go", "list", "-f", "{{.Dir}}", importPath)
	listCommand.Env = append(os.Environ(), "GOWORK=off")
	listOutput, err := listCommand.Output()
	if err != nil {
		t.Fatalf("finding %s with go list: %v", importPath, err)
	}
	packageDir := strings.TrimSpace(string(listOutput))
	fileSet := token.NewFileSet()
	sourcePaths, err := filepath.Glob(filepath.Join(packageDir, "*.go"))
	if err != nil {
		t.Fatalf("listing %s: %v", packageDir, err)
	}
	var exportedNames []string
	for _, sourcePath := range sourcePaths {
		if strings.HasSuffix(sourcePath, "_test.go") {
			continue
		}
		parsedFile, err := parser.ParseFile(fileSet, sourcePath, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", sourcePath, err)
		}
		for _, declaration := range parsedFile.Decls {
			switch typed := declaration.(type) {
			case *ast.GenDecl:
				for _, spec := range typed.Specs {
					switch specOf := spec.(type) {
					case *ast.ValueSpec:
						for _, valueName := range specOf.Names {
							if valueName.IsExported() {
								exportedNames = append(exportedNames, valueName.Name)
							}
						}
					case *ast.TypeSpec:
						if specOf.Name.IsExported() {
							exportedNames = append(exportedNames, specOf.Name.Name)
						}
					}
				}
			case *ast.FuncDecl:
				if typed.Recv == nil && typed.Name.IsExported() {
					exportedNames = append(exportedNames, typed.Name.Name)
				}
			}
		}
	}
	sort.Strings(exportedNames)
	return exportedNames
}
