// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/packages"
)

// referenceCount is the running count of one vendor name in one tree.
type referenceCount struct {
	kind  string
	count int
}

// callerKey identifies an enclosing function in a file.
type callerKey struct {
	file string
	fn   string
}

// treeResult is everything one tree contributes before merging.
type treeResult struct {
	vendorVersion string
	refs          map[string]*referenceCount
	nameRefs      map[string]int
	aliases       map[string]map[string]bool
	viaExpression map[string]bool
	named         map[string]*types.Named
	loadedFiles   map[string]bool
	loadedImports map[string]int
	calls         []CallSite
	mapSites      []MapSite
	errorSites    []ErrorSite
	renderings    []RenderingSite
	boundaryDefs  map[string][]string
	boundaryCalls map[string]map[callerKey]int
}

// treeAnalyzer holds what the file walk shares across one tree.
type treeAnalyzer struct {
	tree       Tree
	absDir     string
	result     *treeResult
	boundaries map[string]bool
	fieldOwner map[*types.Var]*types.TypeName
	seenTypes  map[types.Type]bool
}

// analyzeTree loads a tree and walks each of its files once.
func analyzeTree(tree Tree, boundaryNames []string) (*treeResult, error) {
	absDir, err := filepath.Abs(tree.Dir)
	if err != nil {
		return nil, fmt.Errorf("resolving %s tree directory %q: %w", tree.Label, tree.Dir, err)
	}
	absTree := tree
	absTree.Dir = absDir
	loaded, err := loadTree(absTree)
	if err != nil {
		return nil, err
	}
	analyzer := &treeAnalyzer{
		tree:   tree,
		absDir: absDir,
		result: &treeResult{
			refs:          map[string]*referenceCount{},
			nameRefs:      map[string]int{},
			aliases:       map[string]map[string]bool{},
			viaExpression: map[string]bool{},
			named:         map[string]*types.Named{},
			loadedFiles:   map[string]bool{},
			loadedImports: map[string]int{},
			boundaryDefs:  map[string][]string{},
			boundaryCalls: map[string]map[callerKey]int{},
		},
		boundaries: map[string]bool{},
		fieldOwner: map[*types.Var]*types.TypeName{},
		seenTypes:  map[types.Type]bool{},
	}
	for _, name := range boundaryNames {
		analyzer.boundaries[name] = true
	}
	analyzer.indexVendorPackages(loaded)
	for _, loadedPackage := range loaded {
		for _, file := range loadedPackage.Syntax {
			analyzer.analyzeFile(loadedPackage, file)
		}
	}
	return analyzer.result, nil
}

// indexVendorPackages records which struct owns each vendor field and which
// vendor module version the tree resolved.
func (analyzer *treeAnalyzer) indexVendorPackages(roots []*packages.Package) {
	visited := map[*packages.Package]bool{}
	var visit func(*packages.Package)
	visit = func(candidate *packages.Package) {
		if visited[candidate] {
			return
		}
		visited[candidate] = true
		if _, mirrored := mirroredPackages[candidate.PkgPath]; mirrored && candidate.Types != nil {
			if candidate.Module != nil && analyzer.result.vendorVersion == "" {
				analyzer.result.vendorVersion = candidate.Module.Version
			}
			analyzer.indexFieldOwners(candidate.Types)
		}
		for _, imported := range candidate.Imports {
			visit(imported)
		}
	}
	for _, root := range roots {
		visit(root)
	}
}

func (analyzer *treeAnalyzer) indexFieldOwners(vendorPackage *types.Package) {
	scope := vendorPackage.Scope()
	for _, name := range scope.Names() {
		typeName, isType := scope.Lookup(name).(*types.TypeName)
		if !isType || typeName.IsAlias() {
			continue
		}
		structType, isStruct := typeName.Type().Underlying().(*types.Struct)
		if !isStruct {
			continue
		}
		for index := 0; index < structType.NumFields(); index++ {
			analyzer.fieldOwner[structType.Field(index)] = typeName
		}
	}
}

// relativePath returns the tree-relative slash path of a file, or false for a
// file outside the tree (a generated test main, a dependency).
func (analyzer *treeAnalyzer) relativePath(filename string) (string, bool) {
	relative, err := filepath.Rel(analyzer.absDir, filename)
	if err != nil || strings.HasPrefix(relative, "..") {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

// fileContext is the state of the walk over one file.
type fileContext struct {
	*treeAnalyzer
	relative  string
	info      *types.Info
	pkg       *packages.Package
	file      *ast.File
	codecErrs map[types.Object][]errorAssignment
}

func (analyzer *treeAnalyzer) analyzeFile(loadedPackage *packages.Package, file *ast.File) {
	position := loadedPackage.Fset.File(file.Pos())
	if position == nil {
		return
	}
	relative, inTree := analyzer.relativePath(position.Name())
	if !inTree || analyzer.result.loadedFiles[relative] {
		return
	}
	analyzer.result.loadedFiles[relative] = true
	for _, importSpec := range file.Imports {
		importPath := strings.Trim(importSpec.Path.Value, "\"`")
		if importPath == vendorModule || strings.HasPrefix(importPath, vendorModule+"/") {
			analyzer.result.loadedImports[importPath]++
		}
	}
	context := &fileContext{
		treeAnalyzer: analyzer,
		relative:     relative,
		info:         loadedPackage.TypesInfo,
		pkg:          loadedPackage,
		file:         file,
	}
	context.codecErrs = context.findCodecErrors()
	inspector.New([]*ast.File{file}).WithStack(nil, func(node ast.Node, push bool, stack []ast.Node) bool {
		if push {
			context.visit(node, stack)
		}
		return true
	})
}

func (context *fileContext) visit(node ast.Node, stack []ast.Node) {
	switch typed := node.(type) {
	case *ast.Ident:
		context.visitIdent(typed, stack)
	case *ast.SelectorExpr:
		context.visitSentinel(typed, stack)
	case *ast.CallExpr:
		context.visitCall(typed, stack)
	case *ast.FuncDecl:
		context.visitFuncDecl(typed)
	case *ast.Field:
		context.visitTaggedField(typed, stack)
	}
	if expression, isExpression := node.(ast.Expr); isExpression {
		if typeAndValue, recorded := context.info.Types[expression]; recorded {
			context.noteType(typeAndValue.Type)
		}
	}
}

// site builds the position of a node. The enclosing declaration comes from the stack.
func (context *fileContext) site(node ast.Node, stack []ast.Node) Site {
	position := context.pkg.Fset.Position(node.Pos())
	return Site{
		Tree: context.tree.Label,
		File: context.relative,
		Line: position.Line,
		Func: enclosingName(stack),
		col:  position.Column,
	}
}

func enclosingName(stack []ast.Node) string {
	for index := len(stack) - 1; index >= 0; index-- {
		if declaration, isFunc := stack[index].(*ast.FuncDecl); isFunc {
			return declarationName(declaration)
		}
	}
	return "(package scope)"
}

func declarationName(declaration *ast.FuncDecl) string {
	if declaration.Recv == nil || len(declaration.Recv.List) == 0 {
		return declaration.Name.Name
	}
	receiver := ast.Unparen(declaration.Recv.List[0].Type)
	pointer := false
	if star, isStar := receiver.(*ast.StarExpr); isStar {
		pointer = true
		receiver = ast.Unparen(star.X)
	}
	switch generic := receiver.(type) {
	case *ast.IndexExpr:
		receiver = generic.X
	case *ast.IndexListExpr:
		receiver = generic.X
	}
	receiverName := "?"
	if ident, isIdent := receiver.(*ast.Ident); isIdent {
		receiverName = ident.Name
	}
	if pointer {
		return "(*" + receiverName + ")." + declaration.Name.Name
	}
	return "(" + receiverName + ")." + declaration.Name.Name
}

// visitFuncDecl records where a boundary function is defined.
func (context *fileContext) visitFuncDecl(declaration *ast.FuncDecl) {
	if !context.boundaries[declaration.Name.Name] {
		return
	}
	line := context.pkg.Fset.Position(declaration.Pos()).Line
	definition := fmt.Sprintf("%s:%s:%d", context.tree.Label, context.relative, line)
	context.result.boundaryDefs[declaration.Name.Name] = append(context.result.boundaryDefs[declaration.Name.Name], definition)
}

// visitIdent counts a reference to a vendor name and checks for a use of a
// codec error value.
func (context *fileContext) visitIdent(ident *ast.Ident, stack []ast.Node) {
	object := context.info.Uses[ident]
	if object == nil {
		return
	}
	if name, kind, isVendor := context.vendorObjectName(object); isVendor {
		counter := context.result.refs[name]
		if counter == nil {
			counter = &referenceCount{kind: kind}
			context.result.refs[name] = counter
		}
		counter.count++
		if typeName, isType := object.(*types.TypeName); isType {
			context.noteTypeNameReference(typeName, name)
		}
	}
	if isSelectorName(stack, ident) {
		return
	}
	if origin, isCodecError := context.codecErrorOrigin(object, ident.Pos()); isCodecError {
		context.recordErrorUse(ident, stack, origin)
	}
}

func isSelectorName(stack []ast.Node, ident *ast.Ident) bool {
	if len(stack) < 2 {
		return false
	}
	selector, isSelector := stack[len(stack)-2].(*ast.SelectorExpr)
	return isSelector && selector.Sel == ident
}

// noteTypeNameReference counts a by-name reference on the type it resolves to,
// so a use of the bson.D alias counts for primitive.D with the alias noted.
func (context *fileContext) noteTypeNameReference(typeName *types.TypeName, referenced string) {
	resolved, isNamed := types.Unalias(typeName.Type()).(*types.Named)
	if !isNamed || !resolved.Obj().Exported() || !isVendorPackage(resolved.Obj().Pkg()) {
		return
	}
	key := qualifiedName(resolved.Obj())
	context.result.nameRefs[key]++
	context.result.named[key] = resolved
	if referenced != key {
		if context.result.aliases[key] == nil {
			context.result.aliases[key] = map[string]bool{}
		}
		context.result.aliases[key][referenced] = true
	}
}

// noteType records every vendor named type an expression's type holds.
func (context *fileContext) noteType(candidate types.Type) {
	if candidate == nil || context.seenTypes[candidate] {
		return
	}
	context.seenTypes[candidate] = true
	switch typed := types.Unalias(candidate).(type) {
	case *types.Pointer:
		context.noteType(typed.Elem())
	case *types.Slice:
		context.noteType(typed.Elem())
	case *types.Array:
		context.noteType(typed.Elem())
	case *types.Chan:
		context.noteType(typed.Elem())
	case *types.Map:
		context.noteType(typed.Key())
		context.noteType(typed.Elem())
	case *types.Tuple:
		for index := 0; index < typed.Len(); index++ {
			context.noteType(typed.At(index).Type())
		}
	case *types.Named:
		if typed.Obj().Exported() && isVendorPackage(typed.Obj().Pkg()) {
			key := qualifiedName(typed.Obj())
			context.result.viaExpression[key] = true
			context.result.named[key] = typed
		}
	}
}

// vendorObjectName names an exported vendor object the way the inventory
// lists it: package.Name, package.Type.Method or package.Type.Field.
func (context *fileContext) vendorObjectName(object types.Object) (name, kind string, isVendor bool) {
	if !object.Exported() || !isVendorPackage(object.Pkg()) {
		return "", "", false
	}
	packageName := object.Pkg().Name()
	switch typed := object.(type) {
	case *types.TypeName:
		if typed.Parent() != typed.Pkg().Scope() {
			return "", "", false
		}
		if typed.IsAlias() {
			return packageName + "." + typed.Name(), "alias", true
		}
		return packageName + "." + typed.Name(), "type", true
	case *types.Func:
		function := typed.Origin()
		signature := function.Type().(*types.Signature)
		if signature.Recv() == nil {
			return packageName + "." + function.Name(), "func", true
		}
		owner := receiverTypeName(signature.Recv().Type())
		if owner == "" {
			return "", "", false
		}
		return packageName + "." + owner + "." + function.Name(), "method", true
	case *types.Var:
		if typed.IsField() {
			owner, known := context.fieldOwner[typed.Origin()]
			if !known {
				return "", "", false
			}
			return packageName + "." + owner.Name() + "." + typed.Name(), "field", true
		}
		return packageName + "." + typed.Name(), "var", true
	case *types.Const:
		return packageName + "." + typed.Name(), "const", true
	}
	return "", "", false
}

// receiverTypeName returns the named type behind a method receiver, or "".
func receiverTypeName(receiver types.Type) string {
	receiver = types.Unalias(receiver)
	if pointer, isPointer := receiver.(*types.Pointer); isPointer {
		receiver = types.Unalias(pointer.Elem())
	}
	if named, isNamed := receiver.(*types.Named); isNamed {
		return named.Obj().Name()
	}
	return ""
}

func isVendorPackage(pkg *types.Package) bool {
	if pkg == nil {
		return false
	}
	_, mirrored := mirroredPackages[pkg.Path()]
	return mirrored
}

func qualifiedName(typeName *types.TypeName) string {
	return typeName.Pkg().Name() + "." + typeName.Name()
}

// qualifier prints a package by its name, which is unambiguous among the six
// vendor packages and the standard library.
func qualifier(pkg *types.Package) string { return pkg.Name() }

func typeText(candidate types.Type) string { return types.TypeString(candidate, qualifier) }
