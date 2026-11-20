// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson_test

import "encoding/binary"

// The byte builders of this package's tests. They write BSON by hand, so a
// test states the exact bytes it feeds Validate and owes nothing to an
// encoder that does not exist yet.

// nestingShape is how each level below the root opens.
type nestingShape int

const (
	nestedDocuments nestingShape = iota
	nestedArrays
	nestedScopes // the scope of a code-with-scope value
	nestedMixed  // documents, arrays and scopes in turn
)

func (shape nestingShape) String() string {
	return [...]string{"documents", "arrays", "scopes", "mixed"}[shape]
}

// documentOf puts a little-endian int32 of the total length, its own
// four bytes included, in front of body and terminator.
func documentOf(elements ...[]byte) []byte {
	documentBytes := []byte{0, 0, 0, 0}
	for _, element := range elements {
		documentBytes = append(documentBytes, element...)
	}
	documentBytes = append(documentBytes, 0)
	binary.LittleEndian.PutUint32(documentBytes, uint32(len(documentBytes)))
	return documentBytes
}

// elementOf is a type byte, a key and the value's bytes.
func elementOf(typeByte byte, key string, valueBytes []byte) []byte {
	element := append([]byte{typeByte}, key...)
	element = append(element, 0)
	return append(element, valueBytes...)
}

// stringValueOf is a BSON string value: length (terminator counted), bytes, 0.
func stringValueOf(text string) []byte {
	valueBytes := binary.LittleEndian.AppendUint32(nil, uint32(len(text)+1))
	valueBytes = append(valueBytes, text...)
	return append(valueBytes, 0)
}

// codeWithScopeOf is a code-with-scope value: total length, code, scope.
func codeWithScopeOf(code string, scope []byte) []byte {
	codeBytes := stringValueOf(code)
	valueBytes := binary.LittleEndian.AppendUint32(nil, uint32(4+len(codeBytes)+len(scope)))
	valueBytes = append(valueBytes, codeBytes...)
	return append(valueBytes, scope...)
}

// nestedDocumentOf is a document depth levels deep, the root counted as 1:
// each level holds the next under key "a" (or "0" in an array), and the
// deepest is empty.
func nestedDocumentOf(depth int, shape nestingShape) []byte {
	levelBytes := documentOf()
	for level := depth - 1; level >= 1; level-- {
		childShape := shape
		if shape == nestedMixed {
			childShape = nestingShape(level % 3)
		}
		switch childShape {
		case nestedArrays:
			levelBytes = documentOf(elementOf(0x04, "0", levelBytes))
		case nestedScopes:
			levelBytes = documentOf(elementOf(0x0F, "a", codeWithScopeOf("f", levelBytes)))
		default:
			levelBytes = documentOf(elementOf(0x03, "a", levelBytes))
		}
	}
	return levelBytes
}
