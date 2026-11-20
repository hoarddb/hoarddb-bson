// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson

import "github.com/hoarddb/hoarddb-bson/bson/bsontype"

// Segment is one step of a Path: a member name, which looks inside a
// document, or a position, which counts the elements of an array.
type Segment struct {
	name       string
	position   int
	isPosition bool
}

// Name is the segment for the member of a document named name. The name is
// compared byte for byte; the empty name is a name like any other.
func Name(name string) Segment {
	return Segment{name: name}
}

// Position is the segment for the element at a 0-based position of an
// array. The resolver counts the array's elements in order to reach it; the
// array's own keys play no part, whatever they say. A negative position is
// never found.
func Position(position int) Segment {
	return Segment{position: position, isPosition: true}
}

// Path names a value inside a document, one segment per level:
// Path{Name("lines"), Position(3), Name("qty")} is lines[3].qty.
type Path []Segment

// Lookup returns the value at path, uncopied, and whether it is there. A
// name needs a document and a position needs an array; anything else on
// the way (a scalar, a name on an array, a position on a document, a
// position past the end) is absent, never an error. A name that repeats in
// one document finds its first element. The empty path finds nothing.
// Bytes that are not BSON, which only a TrustStored Document can hold, end
// the walk there: what lies past them is absent. The error is nil for every
// Document a constructor made; the zero Document is refused with an error
// matching ErrZeroDocument.
func (document Document) Lookup(path Path) (View, bool, error) {
	if document.b == nil {
		return View{}, false, ErrZeroDocument
	}
	if len(path) == 0 {
		return View{}, false, nil
	}
	reached := View{valueType: bsontype.EmbeddedDocument, valueBytes: document.b}
	for depth := range path {
		inner, ok := path[depth].within(reached)
		if !ok {
			return View{}, false, nil
		}
		reached = inner
	}
	return reached, true, nil
}

// within resolves one segment against the value the path has reached so
// far. Each kind of segment opens one kind of container, and on any other
// value it finds nothing.
func (segment Segment) within(container View) (View, bool) {
	if segment.isPosition {
		if container.valueType != bsontype.Array || segment.position < 0 {
			return View{}, false
		}
		return advanceTo(cursorOver(container.valueBytes), segment.position)
	}
	if container.valueType != bsontype.EmbeddedDocument {
		return View{}, false
	}
	return firstMemberNamed(cursorOver(container.valueBytes), segment.name)
}

// cursorOver is a cursor over the elements of a document's bytes: the
// root's, framed by its constructor, or an embedded document's or an
// array's, measured by the cursor that read it as a value.
func cursorOver(containerBytes []byte) Cursor {
	elements, _ := elementsOf(containerBytes)
	return Cursor{elements: elements, offset: 4}
}

// firstMemberNamed reads elements until a key equals name, so of several
// elements under one key, the earliest is the answer.
func firstMemberNamed(cursor Cursor, name string) (View, bool) {
	for cursor.Next() {
		if cursor.KeyEquals(name) {
			return cursor.Value(), true
		}
	}
	return View{}, false
}

// advanceTo steps the cursor over position elements and returns the one
// after them. The cursor running out, or meeting bytes that are not an
// element, before then is a miss.
func advanceTo(cursor Cursor, position int) (View, bool) {
	for ; position > 0; position-- {
		if !cursor.Next() {
			return View{}, false
		}
	}
	if !cursor.Next() {
		return View{}, false
	}
	return cursor.Value(), true
}
