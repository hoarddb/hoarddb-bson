// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"math"
	"math/rand/v2"
	"strings"

	vendorbsontype "go.mongodb.org/mongo-driver/bson/bsontype"
	vendorbsoncore "go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
)

// The inputs of the bsoncore type differentials: documents, the elements
// and values cut out of them, and damaged copies of both. Every document is
// stated byte by byte (validate_bytes_test.go), so a probe cannot be
// normalised by an encoder, and the vendor's ReadElement does the cutting,
// so a cut never depends on the code under test.

// stringProbes are the strings every string-bearing type is rendered with:
// each escape class of the JSON text the String methods write.
var stringProbes = []string{
	"", "abc", "a\"b\\c", "tab\tnl\ncr\rbs\bff\f", "\x00\x01\x1f", "<script>&</script>", "\x7f",
	"café 世界", "  ", "�", "\xff\xfe", "\xc3\x28", "\xed\xa0\x80", "\xf0\x9f\x98\x80",
	strings.Repeat("x", 300),
}

func doubleBytes(number float64) []byte {
	return binary.LittleEndian.AppendUint64(nil, math.Float64bits(number))
}

func int64Bytes(number int64) []byte {
	return binary.LittleEndian.AppendUint64(nil, uint64(number))
}

// scalarElements are single elements covering every type byte the vendor
// names, with edge values, plus the bytes that are no type at all.
func scalarElements() [][]byte {
	objectID := []byte{0x50, 0x7f, 0x1f, 0x77, 0xbc, 0xf8, 0x6c, 0xd7, 0x99, 0x43, 0x90, 0x11}
	var elements [][]byte
	for _, number := range []float64{0, math.Copysign(0, -1), 1.5, 5, -2, 1e21, 1e-7, math.Inf(1), math.Inf(-1), math.NaN(), math.MaxFloat64} {
		elements = append(elements, elementOf(0x01, "d", doubleBytes(number)))
	}
	for _, text := range stringProbes {
		elements = append(elements,
			elementOf(0x02, "s", stringValueOf(text)),
			elementOf(0x0D, "j", stringValueOf(text)),
			elementOf(0x0E, "y", stringValueOf(text)),
			elementOf(0x0B, "r", []byte(text+"\x00im\x00")),
			elementOf(0x0C, "p", append(stringValueOf(text), objectID...)),
			elementOf(0x0F, "w", codeWithScopeOf(text, documentOf(elementOf(0x10, "x", int32Of(1))))),
			elementOf(0x03, text, documentOf()),
		)
	}
	for _, options := range []string{"", "i", "msix", "xsmi", "\xff", "ii"} {
		elements = append(elements, elementOf(0x0B, "r", []byte("^a.*\x00"+options+"\x00")))
	}
	for _, subtype := range []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x80, 0xfe, 0xff} {
		elements = append(elements, elementOf(0x05, "b", binaryValueOf(subtype, nil)), elementOf(0x05, "b", binaryValueOf(subtype, []byte{1, 2, 3})))
	}
	elements = append(elements,
		elementOf(0x05, "b", binaryValueOf(0x02, append(int32Of(2), 1, 2))),
		elementOf(0x05, "b", binaryValueOf(0x02, append(int32Of(9), 1, 2))),
		elementOf(0x05, "b", binaryValueOf(0x02, []byte{1, 2, 3})),
		elementOf(0x06, "u", nil), elementOf(0x07, "o", objectID),
		elementOf(0x08, "t", []byte{1}), elementOf(0x08, "f", []byte{0}), elementOf(0x08, "x", []byte{2}),
		elementOf(0x0A, "n", nil), elementOf(0x7F, "max", nil), elementOf(0xFF, "min", nil),
		elementOf(0x04, "a", documentOf()),
		elementOf(0x04, "a", documentOf(elementOf(0x10, "0", int32Of(1)), elementOf(0x02, "1", stringValueOf("v")))),
		elementOf(0x04, "a", documentOf(elementOf(0x10, "1", int32Of(1)))),
		elementOf(0x04, "a", documentOf(elementOf(0x10, "0", int32Of(1)), elementOf(0x10, "0", int32Of(2)))),
		elementOf(0x03, "d", documentOf(elementOf(0x03, "in", documentOf(elementOf(0x10, "deep", int32Of(7)))))),
		elementOf(0x03, "d", documentOf(elementOf(0x04, "in", documentOf(elementOf(0x10, "0", int32Of(7)))))),
		elementOf(0x0F, "w", codeWithScopeOf("", documentOf())),
		elementOf(0x0F, "w", codeWithScopeOf("f(x)", documentOf(elementOf(0x03, "n", documentOf(elementOf(0x08, "b", []byte{1})))))),
	)
	for _, number := range []int32{0, 1, -1, math.MaxInt32, math.MinInt32} {
		elements = append(elements, elementOf(0x10, "i", int32Of(number)))
	}
	for _, number := range []int64{0, 1, -1, math.MaxInt64, math.MinInt64, 1<<31 + 5, -(1 << 31) - 5} {
		elements = append(elements, elementOf(0x12, "l", int64Bytes(number)))
	}
	// DateTime: negative milliseconds take the remainder path of Time.
	for _, milliseconds := range []int64{0, 1, -1, -1500, 1_700_000_000_123, math.MaxInt64, math.MinInt64, 253402300800000} {
		elements = append(elements, elementOf(0x09, "t", int64Bytes(milliseconds)))
	}
	for _, pair := range [][2]uint32{{0, 0}, {1, 2}, {math.MaxUint32, math.MaxUint32}, {7, 0}} {
		elements = append(elements, elementOf(0x11, "ts", binary.LittleEndian.AppendUint32(binary.LittleEndian.AppendUint32(nil, pair[0]), pair[1])))
	}
	for _, decimalHex := range []string{
		"00000000000000000000000000000000", "0100000000000000000000000000403000",
		"00000000000000000000000000007c00", "0000000000000000000000000000f800", "000000000000000000000000000078ff",
		"ffffffffffffffffffffffffffff3f30", "2b00000000000000000000000000403000", "ffffffffffffffffffffffffffffffff",
	} {
		decimalBytes, _ := hex.DecodeString(decimalHex)
		elements = append(elements, elementOf(0x13, "m", decimalBytes[:16]))
	}
	// Type bytes the vendor does not name: valueLength refuses each.
	for _, unnamed := range []byte{0x14, 0x20, 0x80, 0xFE} {
		elements = append(elements, elementOf(unnamed, "z", []byte{1, 2, 3, 4}))
	}
	return elements
}

// baseDocuments are the hand-stated documents: one per scalar element, one
// holding them all, and the framing edges.
func baseDocuments() [][]byte {
	scalars := scalarElements()
	documents := [][]byte{documentOf(), documentOf(scalars...)}
	for _, scalar := range scalars {
		documents = append(documents, documentOf(scalar))
	}
	documents = append(documents,
		documentOf(scalars[0], scalars[1], scalars[2]),
		documentOf(elementOf(0x10, "dup", int32Of(1)), elementOf(0x10, "dup", int32Of(2))),
		documentOf(elementOf(0x03, "a", documentOf(elementOf(0x10, "x", int32Of(1)))), elementOf(0x03, "a", documentOf(elementOf(0x10, "y", int32Of(2))))),
		documentOf(elementOf(0x10, "", int32Of(1))),
		[]byte{5, 0, 0, 0, 0}, []byte{4, 0, 0, 0}, []byte{0, 0, 0, 0}, []byte{0xff, 0xff, 0xff, 0xff, 0}, []byte{1}, nil, []byte{},
	)
	return documents
}

// damagedDocuments are seeded copies of docs with a truncation at every
// length up to 64 bytes plus a few cuts beyond, a length prefix edited, and
// random bytes flipped. The seed is fixed so a failure reproduces.
func damagedDocuments(documents [][]byte, perDocument int) [][]byte {
	generator := rand.New(rand.NewPCG(2026, 4242))
	var damaged [][]byte
	for _, document := range documents {
		for cut := 0; cut < len(document) && cut < 64; cut++ {
			damaged = append(damaged, append([]byte(nil), document[:cut]...))
		}
		if len(document) >= 5 {
			for _, delta := range []int32{-1, 1, -4, 100} {
				edited := append([]byte(nil), document...)
				binary.LittleEndian.PutUint32(edited, uint32(int32(binary.LittleEndian.Uint32(edited))+delta))
				damaged = append(damaged, edited)
			}
			edited := append([]byte(nil), document...)
			edited[len(edited)-1] = 7
			damaged = append(damaged, edited)
		}
		for range perDocument {
			if len(document) == 0 {
				break
			}
			edited := append([]byte(nil), document...)
			for range 1 + generator.IntN(3) {
				edited[generator.IntN(len(edited))] = byte(generator.IntN(256))
			}
			damaged = append(damaged, edited)
		}
	}
	return damaged
}

// typeBytes are the type bytes values are tried under: the 21 named ones,
// zero and bytes no type uses.
var typeBytes = []byte{
	0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F,
	0x10, 0x11, 0x12, 0x13, 0x14, 0x7F, 0x80, 0xFE, 0xFF,
}

// bsoncoreInputs is everything the type differentials iterate.
type bsoncoreInputs struct {
	documents [][]byte
	elements  [][]byte
	values    []vendorbsoncore.Value
}

// buildBsoncoreInputs is the corpus: hand-stated documents, the v1.0
// corpus and vectors (allSeeds), damaged copies of the hand-stated ones and
// a sample of the seeds, the elements cut from all of them, and values made
// of every element's bytes under every type byte.
func buildBsoncoreInputs() (*bsoncoreInputs, error) {
	seeds, err := allSeeds()
	if err != nil {
		return nil, err
	}
	base := baseDocuments()
	documents := append(append([][]byte{}, base...), seeds...)
	documents = append(documents, damagedDocuments(base, 6)...)
	// A sample of the seeds is damaged too; every seed would multiply the
	// run for little new coverage.
	var seedSample [][]byte
	for position, seed := range seeds {
		if position%7 == 0 {
			seedSample = append(seedSample, seed)
		}
	}
	documents = append(documents, damagedDocuments(seedSample, 2)...)

	seenElements := map[string]bool{}
	var elements [][]byte
	addElement := func(candidate []byte) {
		key := hex.EncodeToString(candidate)
		if !seenElements[key] {
			seenElements[key] = true
			elements = append(elements, candidate)
		}
	}
	for _, scalar := range scalarElements() {
		addElement(scalar)
		for cut := 0; cut < len(scalar) && cut < 24; cut++ {
			addElement(scalar[:cut])
		}
	}
	for _, document := range documents {
		if len(document) < 5 {
			continue
		}
		rest := document[4:]
		for range 64 {
			element, remaining, ok := vendorbsoncore.ReadElement(rest)
			if !ok {
				break
			}
			addElement(append([]byte(nil), element...))
			rest = remaining
		}
	}

	seenData := map[string]bool{}
	var dataSamples [][]byte
	for _, element := range elements {
		if len(element) == 0 {
			continue
		}
		keyEnd := bytes.IndexByte(element[1:], 0)
		if keyEnd < 0 {
			continue
		}
		data := element[keyEnd+2:]
		for _, sample := range [][]byte{data, data[:len(data)/2], append(append([]byte(nil), data...), 0xAB, 0xCD)} {
			if key := hex.EncodeToString(sample); !seenData[key] && len(dataSamples) < 700 {
				seenData[key] = true
				dataSamples = append(dataSamples, append([]byte(nil), sample...))
			}
		}
	}
	dataSamples = append(dataSamples, nil, []byte{})
	var values []vendorbsoncore.Value
	for _, data := range dataSamples {
		for _, typeByte := range typeBytes {
			values = append(values, vendorbsoncore.Value{Type: vendorbsontype.Type(typeByte), Data: data})
		}
	}
	return &bsoncoreInputs{documents: documents, elements: elements, values: values}, nil
}
