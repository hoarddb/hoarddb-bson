// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"cmp"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/types/typeutil"
)

// calleeFunction returns the function or method a call statically names.
func calleeFunction(info *types.Info, call *ast.CallExpr) *types.Func {
	function, _ := typeutil.Callee(info, call).(*types.Func)
	return function
}

func (context *fileContext) visitCall(call *ast.CallExpr, stack []ast.Node) {
	function := calleeFunction(context.info, call)
	if function == nil {
		return
	}
	function = function.Origin()
	context.recordBoundaryCall(function, stack)
	if isVendorPackage(function.Pkg()) {
		context.recordCodecCall(function, call, stack)
		context.recordExplicitRendering(function, call, stack)
	}
	context.recordImplicitRenderings(function, call, stack)
}

// recordBoundaryCall notes a call to a configured boundary function declared in this tree.
func (context *fileContext) recordBoundaryCall(function *types.Func, stack []ast.Node) {
	if !context.boundaries[function.Name()] {
		return
	}
	declared := context.pkg.Fset.Position(function.Pos()).Filename
	if _, inTree := context.relativePath(declared); !inTree {
		return
	}
	callers := context.result.boundaryCalls[function.Name()]
	if callers == nil {
		callers = map[callerKey]int{}
		context.result.boundaryCalls[function.Name()] = callers
	}
	callers[callerKey{file: context.relative, fn: enclosingName(stack)}]++
}

// codecKind classifies a vendor function as one of the call kinds of spec 5.1 item 3.
func codecKind(function *types.Func) (kind string, isCodec bool) {
	name := function.Name()
	receiver := ""
	if signature := function.Type().(*types.Signature); signature.Recv() != nil {
		receiver = receiverTypeName(signature.Recv().Type())
	}
	inBSON := function.Pkg().Path() == vendorModule+"/bson"
	switch {
	case strings.Contains(name, "ExtJSON"):
		return "ExtJSON", true
	case receiver == "" && inBSON && strings.HasPrefix(name, "MarshalValue"):
		return "MarshalValue", true
	case receiver == "" && inBSON && strings.HasPrefix(name, "Marshal"):
		return "Marshal", true
	case receiver == "" && inBSON && strings.HasPrefix(name, "Unmarshal"):
		return "Unmarshal", true
	case receiver == "RawValue" && strings.HasPrefix(name, "Unmarshal"):
		return "RawValue.Unmarshal", true
	case inBSON && receiver == "Encoder" && name == "Encode":
		return "Encoder.Encode", true
	case inBSON && receiver == "Decoder" && name == "Decode":
		return "Decoder.Decode", true
	}
	return "", false
}

// marshals reports whether a codec call writes bytes (as opposed to reading them).
func marshals(function *types.Func, kind string) bool {
	if kind == "Encoder.Encode" {
		return true
	}
	return strings.Contains(function.Name(), "Marshal") && !strings.Contains(function.Name(), "Unmarshal")
}

// targetParameter returns the index of the first empty-interface parameter,
// which in every codec entry point is the value being encoded or decoded into.
func targetParameter(signature *types.Signature) int {
	for index := 0; index < signature.Params().Len(); index++ {
		if iface, isInterface := signature.Params().At(index).Type().Underlying().(*types.Interface); isInterface && iface.Empty() {
			return index
		}
	}
	return -1
}

func (context *fileContext) recordCodecCall(function *types.Func, call *ast.CallExpr, stack []ast.Node) {
	kind, isCodec := codecKind(function)
	if !isCodec {
		return
	}
	calleeName, _, _ := context.vendorObjectName(function)
	site := CallSite{Site: context.site(call, stack), Callee: calleeName, Kind: kind, TargetType: "n/a", Class: "n/a"}
	targetIndex := targetParameter(function.Type().(*types.Signature))
	var target types.Type
	if targetIndex >= 0 && targetIndex < len(call.Args) {
		target = context.info.TypeOf(call.Args[targetIndex])
		site.TargetType = typeText(target)
		site.Class, site.PendingTap = classifyTarget(target)
		site.Note = codecMethodsNote(target)
	}
	context.result.calls = append(context.result.calls, site)
	if target != nil && marshals(function, kind) {
		if via, found := findMapIn(target); found {
			context.result.mapSites = append(context.result.mapSites, MapSite{Call: site, Via: via})
		} else if site.PendingTap {
			// An interface-typed target can hold a map at run time; the runtime tap decides (spec 9.1.7).
			context.result.mapSites = append(context.result.mapSites, MapSite{Call: site, Via: mayHoldMap, Pending: true})
		}
	}
}

// mayHoldMap is the section 4 text for a marshal target the static type cannot decide.
const mayHoldMap = "may hold a map (pending tap)"

var explicitRenderings = []string{"Hex", "String", "GoString"}

func (context *fileContext) recordExplicitRendering(function *types.Func, call *ast.CallExpr, stack []ast.Node) {
	if !slices.Contains(explicitRenderings, function.Name()) || function.Type().(*types.Signature).Recv() == nil {
		return
	}
	name, _, isVendor := context.vendorObjectName(function)
	if !isVendor {
		return
	}
	context.result.renderings = append(context.result.renderings,
		RenderingSite{Site: context.site(call, stack), Form: "explicit", Name: name})
}

func isPrintFunction(packagePath, name string) bool {
	switch packagePath {
	case "fmt":
		for _, prefix := range []string{"Sprint", "Print", "Fprint", "Errorf", "Append"} {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
	case "log":
		for _, prefix := range []string{"Print", "Fatal", "Panic"} {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
	}
	return false
}

func isJSONEntryPoint(function *types.Func) bool {
	if function.Pkg().Path() != "encoding/json" {
		return false
	}
	switch function.Name() {
	case "Marshal", "MarshalIndent", "Unmarshal", "Encode", "Decode":
		return true
	}
	return false
}

// recordImplicitRenderings finds vendor values that fmt or encoding/json will
// render through String, MarshalJSON or UnmarshalJSON.
func (context *fileContext) recordImplicitRenderings(function *types.Func, call *ast.CallExpr, stack []ast.Node) {
	if function.Pkg() == nil {
		return
	}
	switch {
	case isPrintFunction(function.Pkg().Path(), function.Name()):
		for _, argument := range call.Args {
			named, isNamed := dereference(context.info.TypeOf(argument)).(*types.Named)
			if isNamed && isVendorPackage(named.Obj().Pkg()) && hasMethod(named, "String") {
				context.result.renderings = append(context.result.renderings, RenderingSite{
					Site: context.site(argument, stack), Form: "implicit fmt", Name: qualifiedName(named.Obj()),
				})
			}
		}
	case isJSONEntryPoint(function):
		targetIndex := targetParameter(function.Type().(*types.Signature))
		if targetIndex < 0 || targetIndex >= len(call.Args) {
			return
		}
		if path, hit, found := findJSONVendorValue(context.info.TypeOf(call.Args[targetIndex])); found {
			context.result.renderings = append(context.result.renderings, RenderingSite{
				Site: context.site(call, stack), Form: "implicit json", Name: hit, Detail: path,
			})
		}
	}
}

// errorAssignment is one assignment to an error variable. Origin is the vendor
// function the error came from, or "" when the assigned value is not a codec error.
type errorAssignment struct {
	end    token.Pos
	origin string
	scope  ast.Node // the innermost block-like node holding the assignment
}

// isScopeNode reports whether a node bounds where an assignment is certain to
// have run: a use outside the node cannot assume the assignment happened.
func isScopeNode(node ast.Node) bool {
	switch node.(type) {
	case *ast.BlockStmt, *ast.CaseClause, *ast.CommClause, *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt,
		*ast.SwitchStmt, *ast.TypeSwitchStmt:
		return true
	}
	return false
}

// findCodecErrors records every assignment to an error variable with the block
// that holds it. A use is attributed to the nearest preceding assignment whose
// block also holds the use (that one certainly ran), together with any nearer
// assignment in a block that does not (it may have run).
func (context *fileContext) findCodecErrors() map[types.Object][]errorAssignment {
	found := map[types.Object][]errorAssignment{}
	record := func(statementEnd token.Pos, scope ast.Node, targets []ast.Expr, values []ast.Expr) {
		var function *types.Func
		var origin string
		if len(values) == 1 {
			if call, isCall := ast.Unparen(values[0]).(*ast.CallExpr); isCall {
				function = calleeFunction(context.info, call)
				if function != nil && function.Exported() && isVendorPackage(function.Pkg()) {
					origin, _, _ = context.vendorObjectName(function.Origin())
				} else {
					function = nil
				}
			}
		}
		for index, target := range targets {
			ident, isIdent := target.(*ast.Ident)
			if !isIdent || ident.Name == "_" {
				continue
			}
			object := context.info.Defs[ident]
			if object == nil {
				object = context.info.Uses[ident]
			}
			if object == nil || !isErrorType(object.Type()) {
				continue
			}
			assigned := errorAssignment{end: statementEnd, scope: scope}
			if function != nil {
				results := function.Type().(*types.Signature).Results()
				if index < results.Len() && isErrorType(results.At(index).Type()) {
					assigned.origin = origin
				}
			}
			found[object] = append(found[object], assigned)
		}
	}
	var scopes []ast.Node
	ast.Inspect(context.file, func(node ast.Node) bool {
		if node == nil {
			scopes = scopes[:len(scopes)-1]
			return true
		}
		if isScopeNode(node) {
			scopes = append(scopes, node)
		} else {
			scopes = append(scopes, nil) // keeps the pop above balanced
		}
		var scope ast.Node = context.file
		for index := len(scopes) - 1; index >= 0; index-- {
			if scopes[index] != nil {
				scope = scopes[index]
				break
			}
		}
		switch typed := node.(type) {
		case *ast.AssignStmt:
			record(typed.End(), scope, typed.Lhs, typed.Rhs)
		case *ast.ValueSpec:
			names := make([]ast.Expr, len(typed.Names))
			for index, name := range typed.Names {
				names[index] = name
			}
			record(typed.End(), scope, names, typed.Values)
		}
		return true
	})
	for _, assignments := range found {
		slices.SortFunc(assignments, func(left, right errorAssignment) int { return cmp.Compare(left.end, right.end) })
	}
	return found
}

func isErrorType(candidate types.Type) bool {
	return types.Identical(candidate, types.Universe.Lookup("error").Type())
}

// codecErrorOrigin says whether an object holds a codec error at the position
// of a use: a variable that a vendor call's error may have reached, or a value
// of a vendor error type.
func (context *fileContext) codecErrorOrigin(object types.Object, use token.Pos) (string, bool) {
	assignments := context.codecErrs[object]
	index, _ := slices.BinarySearchFunc(assignments, use, func(assigned errorAssignment, position token.Pos) int {
		return cmp.Compare(assigned.end, position)
	})
	origin := ""
	for ; index > 0; index-- {
		assigned := assignments[index-1]
		if origin == "" {
			origin = assigned.origin
		}
		if assigned.scope.Pos() <= use && use < assigned.scope.End() {
			break // this assignment certainly ran before the use; earlier ones are overwritten
		}
	}
	if origin != "" {
		return origin, true
	}
	variable, isVariable := object.(*types.Var)
	if !isVariable {
		return "", false
	}
	named, isNamed := dereference(variable.Type()).(*types.Named)
	if isNamed && isVendorPackage(named.Obj().Pkg()) && hasMethod(named, "Error") {
		return "vendor error type " + qualifiedName(named.Obj()), true
	}
	return "", false
}

// visitSentinel handles a reference to an exported vendor error variable.
func (context *fileContext) visitSentinel(selector *ast.SelectorExpr, stack []ast.Node) {
	variable, isVariable := context.info.Uses[selector.Sel].(*types.Var)
	if !isVariable || variable.IsField() || !variable.Exported() || !isVendorPackage(variable.Pkg()) || !isErrorType(variable.Type()) {
		return
	}
	context.recordErrorUse(selector, stack, "sentinel "+variable.Pkg().Name()+"."+variable.Name())
}

func (context *fileContext) recordErrorUse(node ast.Node, stack []ast.Node, origin string) {
	action := context.errorUseAction(stack)
	if action == "" {
		return
	}
	context.result.errorSites = append(context.result.errorSites,
		ErrorSite{Site: context.site(node, stack), Action: action, Origin: origin})
}

// errorUseAction says what a use of an error expression does with its text or
// identity. It returns "" for uses that carry the error on unchanged.
func (context *fileContext) errorUseAction(stack []ast.Node) string {
	last := len(stack) - 1
	if last < 1 {
		return ""
	}
	errorExpr := stack[last]
	switch parent := stack[last-1].(type) {
	case *ast.SelectorExpr:
		if parent.X == errorExpr && parent.Sel.Name == "Error" && last >= 2 {
			if call, isCall := stack[last-2].(*ast.CallExpr); isCall && call.Fun == parent {
				return context.textUseAction(stack[:last-1])
			}
		}
	case *ast.CallExpr:
		return context.errorArgumentAction(parent, errorExpr)
	case *ast.BinaryExpr:
		other := parent.Y
		if other == errorExpr {
			other = parent.X
		}
		if (parent.Op == token.EQL || parent.Op == token.NEQ) && !context.info.Types[other].IsNil() {
			return "compared"
		}
	case *ast.SwitchStmt:
		if parent.Tag == errorExpr {
			return "switched"
		}
	}
	return ""
}

// textUseAction classifies what happens to the result of an Error() call. The
// stack ends at the call.
func (context *fileContext) textUseAction(stack []ast.Node) string {
	callIndex := len(stack) - 1
	if callIndex == 0 {
		return "Error() used"
	}
	call := stack[callIndex]
	switch parent := stack[callIndex-1].(type) {
	case *ast.SliceExpr:
		if parent.X == call {
			return "Error() truncated"
		}
	case *ast.BinaryExpr:
		switch parent.Op {
		case token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ:
			return "Error() compared"
		}
	case *ast.SwitchStmt:
		if parent.Tag == call {
			return "Error() branched"
		}
	case *ast.CallExpr:
		if callee := calleeFunction(context.info, parent); callee != nil && callee.Pkg() != nil {
			switch callee.Pkg().Path() {
			case "strings", "bytes":
				return "Error() branched"
			case "fmt", "log":
				return "Error() printed"
			}
		}
	}
	return "Error() used"
}

// errorArgumentAction classifies an error passed straight to a call.
func (context *fileContext) errorArgumentAction(call *ast.CallExpr, errorExpr ast.Node) string {
	if !slices.ContainsFunc(call.Args, func(argument ast.Expr) bool { return argument == errorExpr }) {
		return ""
	}
	callee := calleeFunction(context.info, call)
	if callee == nil || callee.Pkg() == nil {
		return ""
	}
	path := callee.Pkg().Path()
	switch {
	case path == "errors" && callee.Name() == "Is":
		// errors.Is compares identity, the "compares" of spec 5.1 item 5; errors.As matches by type and is not listed.
		return "compared (errors.Is)"
	case path == "fmt" && callee.Name() == "Errorf":
		if len(call.Args) > 0 && strings.Contains(context.constantString(call.Args[0]), "%w") {
			return "wrapped"
		}
		return "printed"
	case isPrintFunction(path, callee.Name()):
		return "printed"
	case path == "log/slog" || strings.HasSuffix(path, "/logging"):
		return "logged"
	}
	return ""
}

func (context *fileContext) constantString(expression ast.Expr) string {
	value := context.info.Types[expression].Value
	if value == nil || value.Kind() != constant.String {
		return ""
	}
	return constant.StringVal(value)
}

// visitTaggedField records a struct field that carries a json tag and holds a
// vendor value: encoding/json renders it through MarshalJSON wherever the
// struct reaches json, which a static call search can miss (spec 5.1 item 2).
func (context *fileContext) visitTaggedField(field *ast.Field, stack []ast.Node) {
	if field.Tag == nil || len(stack) < 4 {
		return
	}
	tag, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return
	}
	jsonTag := reflect.StructTag(tag).Get("json")
	if jsonTag == "" || jsonTag == "-" {
		return
	}
	path, hit, found := findJSONVendorValue(context.info.TypeOf(field.Type))
	if !found {
		return
	}
	typeSpec, isTypeSpec := stack[len(stack)-4].(*ast.TypeSpec)
	if !isTypeSpec {
		return
	}
	fieldName := typeText(context.info.TypeOf(field.Type))
	if len(field.Names) > 0 {
		fieldName = field.Names[0].Name
	}
	detail := typeSpec.Name.Name + "." + fieldName
	if path != "" {
		detail += "." + path
	}
	site := context.site(field, stack)
	site.Func = "type " + typeSpec.Name.Name
	context.result.renderings = append(context.result.renderings,
		RenderingSite{Site: site, Form: "json tag", Name: hit, Detail: detail})
}
