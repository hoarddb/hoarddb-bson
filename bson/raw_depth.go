// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson

import (
	"fmt"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/x/bsonx/bsoncore"
)

// The renderings of Raw, RawValue and RawElement recurse, so they hold the
// MaxNestingCeiling (spec 5.2). bsoncore's renderer already stops at the
// 201st level, but bsoncore cannot import this package, so it gives the
// refusal it uses for anything it cannot render ("" or "<malformed>"),
// which a reader cannot tell from a malformed document. When the renderer
// gives that refusal, these functions measure the depth themselves and, if
// the rendering would have opened a level past the ceiling, render the
// ErrNestingTooDeep refusal instead.

// nestingRefusal is the rendering of input nested past the ceiling.
var nestingRefusal = fmt.Sprintf("<%v: more than %d levels>", ErrNestingTooDeep, MaxNestingCeiling)

// minRenderedDocument is the shortest document the renderer opens: a length
// prefix and a terminator.
const minRenderedDocument = 5

// refuseDocumentPastCeiling returns rendered, or the nesting refusal when the
// renderer gave its refusal because document opens a level past the ceiling.
func refuseDocumentPastCeiling(rendered, rendererRefusal string, document []byte) string {
	if rendered == rendererRefusal && documentOpensPastCeiling(document, 1) {
		return nestingRefusal
	}
	return rendered
}

// refuseValuePastCeiling is refuseDocumentPastCeiling for one value, whose
// document, array or scope is level 1.
func refuseValuePastCeiling(rendered, rendererRefusal string, value bsoncore.Value) string {
	if rendered == rendererRefusal && valueOpensPastCeiling(value, 1) {
		return nestingRefusal
	}
	return rendered
}

// documentOpensPastCeiling reports whether rendering document as level
// opens a level past MaxNestingCeiling. It reads the way bsoncore's
// renderer reads, element by element up to the first it cannot read, and
// stops descending at the first level past the ceiling, so it recurses at
// most MaxNestingCeiling+1 frames however deep the bytes go.
func documentOpensPastCeiling(document []byte, level int) bool {
	if len(document) < minRenderedDocument {
		return false
	}
	if level > MaxNestingCeiling {
		return true
	}
	remainingLength, rest, _ := bsoncore.ReadLength(document) // five bytes hold a length
	remainingLength -= 4
	for remainingLength > 1 {
		element, afterElement, ok := bsoncore.ReadElement(rest)
		remainingLength -= int32(len(element))
		if !ok {
			return false
		}
		rest = afterElement
		if valueOpensPastCeiling(element.Value(), level+1) {
			return true
		}
	}
	return false
}

// valueOpensPastCeiling reports whether rendering value, whose document,
// array or scope would be level, opens a level past the ceiling.
func valueOpensPastCeiling(value bsoncore.Value, level int) bool {
	switch value.Type {
	case bsontype.EmbeddedDocument:
		if document, ok := value.DocumentOK(); ok {
			return documentOpensPastCeiling(document, level)
		}
	case bsontype.Array:
		if array, ok := value.ArrayOK(); ok {
			return documentOpensPastCeiling(array, level)
		}
	case bsontype.CodeWithScope:
		if _, scope, ok := value.CodeWithScopeOK(); ok {
			return documentOpensPastCeiling(scope, level)
		}
	}
	return false
}
