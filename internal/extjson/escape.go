// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package extjson

import "unicode/utf8"

// The string escaping of the Extended JSON writer, written for this
// library from the rules below (build doc S-3: the vendor's escape tables
// derive from Go's encoding/json and are not ported). The rules are the
// vendor writer's observable behaviour, and the oracle's differentials
// check each one:
//
//   - '"' and '\' are escaped with a backslash; newline, carriage return,
//     tab, backspace and form feed take their short escapes (\n \r \t \b
//     \f); every other byte below 0x20 is written \u00XX.
//   - With escapeHTML, '<', '>' and '&' are written \u003c, \u003e and
//     \u0026; without it they are written as they are.
//   - Every other ASCII byte, DEL included, is written as it is.
//   - A byte that does not start a valid UTF-8 sequence is written \ufffd,
//     one escape per byte.
//   - U+2028 and U+2029 are always written \u2028 and \u2029, because a
//     JavaScript parser reads them as line ends.
//   - Every other valid UTF-8 sequence, a written-out U+FFFD included, is
//     copied as it is.
//
// Hexadecimal digits are lower case.

const lowerHexDigits = "0123456789abcdef"

// appendQuoted appends text as a quoted JSON string under those rules.
func appendQuoted(dst []byte, text string, escapeHTML bool) []byte {
	dst = append(dst, '"')
	for position := 0; position < len(text); {
		octet := text[position]
		if octet < utf8.RuneSelf {
			dst = appendASCII(dst, octet, escapeHTML)
			position++
			continue
		}
		character, width := utf8.DecodeRuneInString(text[position:])
		switch {
		case character == utf8.RuneError && width == 1:
			dst = appendCodeUnitEscape(dst, utf8.RuneError)
		case character == '\u2028' || character == '\u2029':
			dst = appendCodeUnitEscape(dst, character)
		default:
			dst = append(dst, text[position:position+width]...)
		}
		position += width
	}
	return append(dst, '"')
}

// appendASCII appends one byte below 0x80.
func appendASCII(dst []byte, octet byte, escapeHTML bool) []byte {
	switch octet {
	case '"', '\\':
		return append(dst, '\\', octet)
	case '\n':
		return append(dst, '\\', 'n')
	case '\r':
		return append(dst, '\\', 'r')
	case '\t':
		return append(dst, '\\', 't')
	case '\b':
		return append(dst, '\\', 'b')
	case '\f':
		return append(dst, '\\', 'f')
	case '<', '>', '&':
		if escapeHTML {
			return appendCodeUnitEscape(dst, rune(octet))
		}
		return append(dst, octet)
	}
	if octet < 0x20 {
		return appendCodeUnitEscape(dst, rune(octet))
	}
	return append(dst, octet)
}

// appendCodeUnitEscape appends \uXXXX for a character of the Basic
// Multilingual Plane, the only ones the rules escape.
func appendCodeUnitEscape(dst []byte, character rune) []byte {
	return append(dst, '\\', 'u',
		lowerHexDigits[character>>12&0xF], lowerHexDigits[character>>8&0xF],
		lowerHexDigits[character>>4&0xF], lowerHexDigits[character&0xF])
}
