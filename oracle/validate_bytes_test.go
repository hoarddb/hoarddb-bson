// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import "encoding/binary"

// Hand-written BSON for the Validate tests: each probe states its bytes,
// so a probe cannot be "fixed" by an encoder normalising it.

// documentOf frames elements as a document: total length, elements, 0.
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

// int32Of is a little-endian int32, as BSON writes every length.
func int32Of(number int32) []byte {
	return binary.LittleEndian.AppendUint32(nil, uint32(number))
}

// stringValueOf is a BSON string: length (terminator counted), bytes, 0.
func stringValueOf(text string) []byte {
	return append(append(int32Of(int32(len(text)+1)), text...), 0)
}

// codeWithScopeOf is a code-with-scope value: total length, code, scope.
func codeWithScopeOf(code string, scope []byte) []byte {
	codeBytes := stringValueOf(code)
	valueBytes := int32Of(int32(4 + len(codeBytes) + len(scope)))
	return append(append(valueBytes, codeBytes...), scope...)
}

// binaryValueOf is a binary value: length, subtype, bytes.
func binaryValueOf(subtype byte, payload []byte) []byte {
	return append(append(int32Of(int32(len(payload))), subtype), payload...)
}

// withInt32At returns a copy of documentBytes with an int32 written at offset.
func withInt32At(documentBytes []byte, offset int, number int32) []byte {
	edited := append([]byte(nil), documentBytes...)
	binary.LittleEndian.PutUint32(edited[offset:], uint32(number))
	return edited
}

// withByteAt returns a copy of documentBytes with one byte replaced.
func withByteAt(documentBytes []byte, offset int, replacement byte) []byte {
	edited := append([]byte(nil), documentBytes...)
	edited[offset] = replacement
	return edited
}

// nestedDocumentOf is a document depth levels deep, the root counted as 1,
// each level holding the next under key "a".
func nestedDocumentOf(depth int) []byte {
	levelBytes := documentOf()
	for level := depth - 1; level >= 1; level-- {
		levelBytes = documentOf(elementOf(0x03, "a", levelBytes))
	}
	return levelBytes
}

// framingSeeds are the root framings a reader must refuse without
// panicking: declared lengths of 0, negative, 1 to 4, and longer than the
// bytes. The vendor's Raw.Validate panics on the first two (step 1d).
func framingSeeds() [][]byte {
	emptyDocument := documentOf()
	return [][]byte{
		nil,
		{},
		{0},
		{5, 0, 0},
		withInt32At(emptyDocument, 0, 0),
		withInt32At(emptyDocument, 0, -1),
		withInt32At(emptyDocument, 0, -5),
		withInt32At(emptyDocument, 0, -0x80000000),
		withInt32At(emptyDocument, 0, 1),
		withInt32At(emptyDocument, 0, 2),
		withInt32At(emptyDocument, 0, 3),
		withInt32At(emptyDocument, 0, 4),
		{4, 0, 0, 0},
		{0, 0, 0, 0},
		{0xFF, 0xFF, 0xFF, 0xFF},
		withInt32At(emptyDocument, 0, 6),
		withInt32At(emptyDocument, 0, 0x7FFFFFFF),
		append(withInt32At(emptyDocument, 0, 6), 0),
	}
}
