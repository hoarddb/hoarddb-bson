// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson

// MarshalExtJSON returns the Extended JSON text of val, canonical or
// relaxed, with '<', '>' and '&' escaped when escapeHTML is set. val must
// be a document, as for Marshal: a struct, a map with string keys, a D, a
// Raw, or a value whose MarshalBSON returns one. The text is written from
// the Go value itself, not from its BSON, so a key or a regex holding a
// zero byte is written escaped where Marshal refuses it, and the bytes of a
// Raw, a RawValue or a hook are read from their own start. A map's entries
// are written in ascending byte order of their keys. A value nested deeper
// than MaxNestingCeiling, or one that contains itself, is refused with an
// error matching ErrNestingTooDeep.
func MarshalExtJSON(val interface{}, canonical, escapeHTML bool) ([]byte, error) {
	if !oracleTapCompiled {
		return libraryEncoder.Text(val, canonical, escapeHTML)
	}
	after := oracleBeforeText("MarshalExtJSON", val, nil, canonical, escapeHTML)
	text, err := libraryEncoder.Text(val, canonical, escapeHTML)
	if tapErr := after(val, text, 0, err); tapErr != nil {
		return nil, tapErr
	}
	return text, err
}

// UnmarshalExtJSON decodes the Extended JSON text in data, canonical or
// relaxed, into val, which must be a non-nil pointer or a non-nil map, with
// Unmarshal's rules for each target. Only the first JSON value is read, and
// a repeated key is decoded each time it appears. A map, a D or a struct
// receives what the text holds, a key with a zero byte or a regex's options
// in the order written included; a Raw receives BSON, which refuses both.
// A text that opens a level past MaxNestingCeiling where it is decoded is
// refused with an error matching ErrNestingTooDeep.
func UnmarshalExtJSON(data []byte, canonical bool, val interface{}) error {
	if !oracleTapCompiled {
		return libraryDecoder.UnmarshalText(data, canonical, val)
	}
	after := oracleBeforeText("UnmarshalExtJSON", val, data, canonical, false)
	err := libraryDecoder.UnmarshalText(data, canonical, val)
	if tapErr := after(val, data, 0, err); tapErr != nil {
		return tapErr
	}
	return err
}
