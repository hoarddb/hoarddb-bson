// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package extjson

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// The scanner's contract is the vendor's, held by the oracle module's
// differentials. These cases pin the quirks that a rewrite toward RFC 8259
// would quietly lose; each expected text is the vendor's.

func scanAll(text string) ([]token, error) {
	tokenScanner := scanner{text: []byte(text)}
	var tokens []token
	for {
		nextToken, err := tokenScanner.next()
		if err != nil {
			return tokens, err
		}
		tokens = append(tokens, nextToken)
		if nextToken.kind == tokenEndOfInput {
			return tokens, nil
		}
	}
}

func TestScannerErrorText(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"bad character", `{"a": @}`, "invalid JSON input. Position: 6. Character: @"},
		{"bad character past 512 bytes", strings.Repeat(" ", 600) + "@", "invalid JSON input. Position: 88. Character: @"},
		{"literal short at end", `tr`, "EOF"},
		{"literal wrong", `nulL`, "invalid JSON literal. Position: 0, literal: nulL"},
		{"false then letter", `falsey`, "invalid JSON literal. Position: 0, literal: fals"},
		{"number leading zero", `01`, "invalid JSON number. Position: 0"},
		{"number out of range", `1e400`, `strconv.ParseFloat: parsing "1e400": value out of range`},
		{"string at end", `"abc`, "end of input in JSON string"},
		{"bad escape", `"\x"`, `invalid escape sequence in JSON string '\x'`},
		{"short escape at end", `"\u12`, "invalid unicode sequence in JSON string: 12\x00\x00"},
		{"short second half keeps the first", `"\ud834\u12`, "invalid unicode sequence in JSON string: 12\x004"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := scanAll(testCase.text)
			if err == nil || err.Error() != testCase.want {
				t.Fatalf("error %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestScannerLiteralShortAtEndIsBareEOF(t *testing.T) {
	_, err := scanAll(`[tru`)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("error %v, want io.EOF", err)
	}
}

func TestScannerTokens(t *testing.T) {
	cases := []struct {
		name string
		text string
		want token
	}{
		{"int32", `-2147483648`, token{kind: tokenInt32, int32Number: -2147483648}},
		{"minus zero is an int32", `-0`, token{kind: tokenInt32}},
		{"int64", `2147483648`, token{kind: tokenInt64, int64Number: 2147483648}},
		{"above int64 is a double", `99999999999999999999`, token{kind: tokenDouble, double: 1e20}},
		{"exponent is a double", `1E3`, token{kind: tokenDouble, double: 1000}},
		{"true", `true`, token{kind: tokenBoolean, flag: true}},
		{"false", `false`, token{kind: tokenBoolean}},
		{"null", `null`, token{kind: tokenNull}},
		{"surrogate pair", `"𝄞"`, token{kind: tokenString, text: "\U0001D11E"}},
		{"lone surrogate then text", `"\ud834x"`, token{kind: tokenString, text: "�x"}},
		{"reversed surrogates", `"\udd1e\ud834"`, token{kind: tokenString, text: "��"}},
		{"bad hex", `"\uZZZZ"`, token{kind: tokenString, text: "�"}},
		{"surrogate then other escape", `"\ud834\n"`, token{kind: tokenString, text: "�\n"}},
		{"raw control byte and invalid UTF-8", "\"\t\xff\"", token{kind: tokenString, text: "\t\xff"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			tokens, err := scanAll(testCase.text)
			if err != nil {
				t.Fatal(err)
			}
			if len(tokens) != 2 || tokens[0] != testCase.want {
				t.Fatalf("tokens %+v, want %+v then the end", tokens, testCase.want)
			}
		})
	}
}

func TestScannerGivesBackTheByteAfterANumber(t *testing.T) {
	tokens, err := scanAll(`[1,true]`)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []tokenKind{tokenBeginArray, tokenInt32, tokenComma, tokenBoolean, tokenEndArray, tokenEndOfInput}
	if len(tokens) != len(kinds) {
		t.Fatalf("%d tokens, want %d: %+v", len(tokens), len(kinds), tokens)
	}
	for index, kind := range kinds {
		if tokens[index].kind != kind {
			t.Fatalf("token %d is kind %d, want %d", index, tokens[index].kind, kind)
		}
	}
}
