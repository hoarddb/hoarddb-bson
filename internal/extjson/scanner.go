// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package extjson

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// This scanner is written fresh (build doc S-3): it splits text into JSON
// tokens with the vendor's grammar and error text, which differ from
// RFC 8259 in places the CLI shows, so each difference is kept on purpose.

// readChunk is the size of the vendor scanner's reads. Its error positions
// count from the start of the read that holds the byte, not from the start
// of the text, so a position past the first 512 bytes starts again from 0.
// The error text is the contract (spec 9.1.5), so positions are reported
// the same way.
const readChunk = 512

type tokenKind byte

const (
	tokenBeginObject tokenKind = iota
	tokenEndObject
	tokenBeginArray
	tokenEndArray
	tokenColon
	tokenComma
	tokenInt32
	tokenInt64
	tokenDouble
	tokenString
	tokenBoolean
	tokenNull
	tokenEndOfInput
)

// token is one JSON token. Only the field its kind names is set.
type token struct {
	kind        tokenKind
	position    int
	delimiter   byte
	text        string
	int32Number int32
	int64Number int64
	double      float64
	flag        bool
}

// errEndOfInputInString is the vendor's text for a string the input ends in.
var errEndOfInputInString = errors.New("end of input in JSON string")

// scanner reads tokens from text. It never reads backwards more than the
// one byte that ends a number or a literal.
type scanner struct {
	text   []byte
	offset int
}

// reportedPosition is how the vendor numbers the byte at offset.
func reportedPosition(offset int) int {
	return offset % readChunk
}

func (tokenScanner *scanner) readByte() (byte, bool) {
	if tokenScanner.offset >= len(tokenScanner.text) {
		return 0, false
	}
	nextByte := tokenScanner.text[tokenScanner.offset]
	tokenScanner.offset++
	return nextByte, true
}

// unread gives back the byte that ended a number or a literal. At the end
// of the input there is none to give back.
func (tokenScanner *scanner) unread(readOne bool) {
	if readOne {
		tokenScanner.offset--
	}
}

func isJSONSpace(character byte) bool {
	return character == ' ' || character == '\t' || character == '\r' || character == '\n'
}

func isASCIIDigit(character byte) bool {
	return '0' <= character && character <= '9'
}

// endsValue reports whether character may follow a literal.
func endsValue(character byte) bool {
	return character == ',' || character == '}' || character == ']' || isJSONSpace(character)
}

func delimiterKind(character byte) (tokenKind, bool) {
	switch character {
	case '{':
		return tokenBeginObject, true
	case '}':
		return tokenEndObject, true
	case '[':
		return tokenBeginArray, true
	case ']':
		return tokenEndArray, true
	case ':':
		return tokenColon, true
	case ',':
		return tokenComma, true
	}
	return 0, false
}

// next returns the next token, or a token of kind tokenEndOfInput.
func (tokenScanner *scanner) next() (token, error) {
	character, ok := tokenScanner.readByte()
	for ok && isJSONSpace(character) {
		character, ok = tokenScanner.readByte()
	}
	if !ok {
		return token{kind: tokenEndOfInput}, nil
	}
	start := tokenScanner.offset - 1
	if kind, isDelimiter := delimiterKind(character); isDelimiter {
		return token{kind: kind, delimiter: character, position: reportedPosition(start)}, nil
	}
	switch {
	case character == '"':
		return tokenScanner.scanString(start)
	case character == '-' || isASCIIDigit(character):
		return tokenScanner.scanNumber(start)
	case character == 't' || character == 'f' || character == 'n':
		return tokenScanner.scanLiteral(start)
	}
	return token{}, fmt.Errorf("invalid JSON input. Position: %d. Character: %c", reportedPosition(start), character)
}

// scanLiteral reads true, false or null. The vendor reads four bytes before
// it looks at them, so input that ends inside them is a bare io.EOF.
func (tokenScanner *scanner) scanLiteral(start int) (token, error) {
	if len(tokenScanner.text)-start < 4 {
		tokenScanner.offset = len(tokenScanner.text)
		return token{}, io.EOF
	}
	literal := string(tokenScanner.text[start : start+4])
	tokenScanner.offset = start + 4
	position := reportedPosition(start)
	following, readOne := tokenScanner.readByte()
	ended := !readOne || endsValue(following)
	switch {
	case literal == "true" && ended:
		tokenScanner.unread(readOne)
		return token{kind: tokenBoolean, flag: true, position: position}, nil
	case literal == "null" && ended:
		tokenScanner.unread(readOne)
		return token{kind: tokenNull, position: position}, nil
	case literal == "fals" && following == 'e':
		afterE, readAfterE := tokenScanner.readByte()
		if !readAfterE || endsValue(afterE) {
			tokenScanner.unread(readAfterE)
			return token{kind: tokenBoolean, flag: false, position: position}, nil
		}
	}
	return token{}, fmt.Errorf("invalid JSON literal. Position: %d, literal: %s", position, literal)
}

type numberPart byte

const (
	partMinus numberPart = iota
	partLeadingZero
	partInteger
	partPoint
	partFraction
	partExponentLetter
	partExponentSign
	partExponent
)

// numberCanEnd reports whether a number may end in part: the input ends,
// or a delimiter or space follows.
func numberCanEnd(part numberPart) bool {
	return part == partLeadingZero || part == partInteger || part == partFraction || part == partExponent
}

// nextNumberPart moves the number grammar on by one character, or reports
// that the character cannot come next.
func nextNumberPart(part numberPart, character byte) (numberPart, bool) {
	switch {
	case part == partMinus && character == '0':
		return partLeadingZero, true
	case part == partMinus && isASCIIDigit(character):
		return partInteger, true
	case (part == partLeadingZero || part == partInteger) && character == '.':
		return partPoint, true
	case (part == partLeadingZero || part == partInteger || part == partFraction) && (character == 'e' || character == 'E'):
		return partExponentLetter, true
	case part == partInteger && isASCIIDigit(character):
		return partInteger, true
	case (part == partPoint || part == partFraction) && isASCIIDigit(character):
		return partFraction, true
	case part == partExponentLetter && (character == '+' || character == '-'):
		return partExponentSign, true
	case (part == partExponentLetter || part == partExponentSign || part == partExponent) && isASCIIDigit(character):
		return partExponent, true
	}
	return part, false
}

// scanNumber reads a number. One without a point or an exponent that fits
// an int64 is an int32 or an int64. Anything else, including an integer
// too large for an int64, is a double.
func (tokenScanner *scanner) scanNumber(start int) (token, error) {
	first := tokenScanner.text[start]
	part := partInteger
	switch first {
	case '-':
		part = partMinus
	case '0':
		part = partLeadingZero
	}
	isDouble := false
	for {
		character, readOne := tokenScanner.readByte()
		ends := !readOne || character == '}' || character == ']' || character == ',' || isJSONSpace(character)
		if ends && numberCanEnd(part) {
			tokenScanner.unread(readOne)
			break
		}
		nextPart, valid := nextNumberPart(part, character)
		if !readOne || !valid {
			return token{}, fmt.Errorf("invalid JSON number. Position: %d", reportedPosition(start))
		}
		if nextPart == partPoint || nextPart == partExponentLetter {
			isDouble = true
		}
		part = nextPart
	}
	numberText := string(tokenScanner.text[start:tokenScanner.offset])
	position := reportedPosition(start)
	if !isDouble {
		if integer, err := strconv.ParseInt(numberText, 10, 64); err == nil {
			if integer < math.MinInt32 || integer > math.MaxInt32 {
				return token{kind: tokenInt64, int64Number: integer, position: position}, nil
			}
			return token{kind: tokenInt32, int32Number: int32(integer), position: position}, nil
		}
	}
	double, err := strconv.ParseFloat(numberText, 64)
	if err != nil {
		// The vendor returns strconv's error as it is, and the text is the contract.
		return token{}, err
	}
	return token{kind: tokenDouble, double: double, position: position}, nil
}

// stringStep is what the string scanner does with the byte it holds.
type stringStep byte

const (
	stepRead       stringStep = iota // read the next byte
	stepCharacter                    // the held byte is a character of the string
	stepEscapeChar                   // the held byte follows a backslash
)

// scanString reads a string from its opening quote. Control characters and
// invalid UTF-8 pass through unchecked, as in the vendor.
func (tokenScanner *scanner) scanString(start int) (token, error) {
	var decoded []byte
	step := stepRead
	var held byte
	for {
		if step == stepRead {
			nextByte, ok := tokenScanner.readByte()
			if !ok {
				return token{}, errEndOfInputInString
			}
			held, step = nextByte, stepCharacter
		}
		if step == stepCharacter {
			switch held {
			case '"':
				return token{kind: tokenString, text: string(decoded), position: reportedPosition(start)}, nil
			case '\\':
				escapeChar, ok := tokenScanner.readByte()
				if !ok {
					return token{}, errEndOfInputInString
				}
				held = escapeChar // an escape letter, decoded below
			default:
				decoded = append(decoded, held)
				step = stepRead
				continue
			}
		}
		var err error
		decoded, held, step, err = tokenScanner.escape(decoded, held)
		if err != nil {
			return token{}, err
		}
	}
}

// simpleEscape returns the byte a one-letter escape stands for.
func simpleEscape(escapeChar byte) (byte, bool) {
	switch escapeChar {
	case '"', '\\', '/':
		return escapeChar, true
	case 'b':
		return '\b', true
	case 'f':
		return '\f', true
	case 'n':
		return '\n', true
	case 'r':
		return '\r', true
	case 't':
		return '\t', true
	}
	return 0, false
}

// escape decodes the escape whose letter is escapeChar and says what the
// string scanner does next.
func (tokenScanner *scanner) escape(decoded []byte, escapeChar byte) ([]byte, byte, stringStep, error) {
	if replacement, isSimple := simpleEscape(escapeChar); isSimple {
		return append(decoded, replacement), 0, stepRead, nil
	}
	if escapeChar != 'u' {
		return decoded, 0, stepRead, fmt.Errorf("invalid escape sequence in JSON string '\\%c'", escapeChar)
	}
	return tokenScanner.unicodeEscape(decoded)
}

// unicodeEscape decodes \uXXXX, and a second one after it when the first is
// half of a surrogate pair. A hex group that is not four hex digits decodes
// to U+FFFD, and so does a surrogate that is not half of a pair, after
// which the byte that followed it is read again as itself.
func (tokenScanner *scanner) unicodeEscape(decoded []byte) ([]byte, byte, stringStep, error) {
	// One buffer serves both halves of a pair, as in the vendor, whose error
	// for a short second half prints what is left of the first.
	var hexDigits [4]byte
	if err := tokenScanner.readHexDigits(&hexDigits); err != nil {
		return decoded, 0, stepRead, err
	}
	codePoint := hexValue(hexDigits)
	if !utf16.IsSurrogate(codePoint) {
		return utf8.AppendRune(decoded, codePoint), 0, stepRead, nil
	}
	afterSurrogate, ok := tokenScanner.readByte()
	if !ok {
		return decoded, 0, stepRead, errEndOfInputInString
	}
	if afterSurrogate != '\\' {
		return utf8.AppendRune(decoded, utf8.RuneError), afterSurrogate, stepCharacter, nil
	}
	escapeChar, ok := tokenScanner.readByte()
	if !ok {
		return decoded, 0, stepRead, errEndOfInputInString
	}
	if escapeChar != 'u' {
		return utf8.AppendRune(decoded, utf8.RuneError), escapeChar, stepEscapeChar, nil
	}
	if err := tokenScanner.readHexDigits(&hexDigits); err != nil {
		return decoded, 0, stepRead, err
	}
	secondHalf := hexValue(hexDigits)
	if paired := utf16.DecodeRune(codePoint, secondHalf); paired != utf8.RuneError {
		return utf8.AppendRune(decoded, paired), 0, stepRead, nil
	}
	decoded = utf8.AppendRune(decoded, utf8.RuneError)
	return utf8.AppendRune(decoded, secondHalf), 0, stepRead, nil
}

// readHexDigits fills hexDigits with the next four bytes. When the input
// ends first, the byte it could not read is zero and the ones after it keep
// what they held, and the error prints all four.
func (tokenScanner *scanner) readHexDigits(hexDigits *[4]byte) error {
	for index := range hexDigits {
		nextByte, ok := tokenScanner.readByte()
		if !ok {
			hexDigits[index] = 0
			return fmt.Errorf("invalid unicode sequence in JSON string: %s", hexDigits[:])
		}
		hexDigits[index] = nextByte
	}
	return nil
}

// hexValue reads four hex digits as a code point, or returns -1, which
// encodes as U+FFFD.
func hexValue(hexDigits [4]byte) rune {
	var codePoint rune
	for _, digit := range hexDigits {
		var digitValue byte
		switch {
		case '0' <= digit && digit <= '9':
			digitValue = digit - '0'
		case 'a' <= digit && digit <= 'f':
			digitValue = digit - 'a' + 10
		case 'A' <= digit && digit <= 'F':
			digitValue = digit - 'A' + 10
		default:
			return -1
		}
		codePoint = codePoint<<4 | rune(digitValue)
	}
	return codePoint
}
