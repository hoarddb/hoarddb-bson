// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

// Package bsoncodec holds the struct-tag types the plan compiler and the
// tools that name struct fields share. It mirrors only StructTags,
// StructTagParserFunc and DefaultStructTagParser, with the vendor's
// signatures, so callers port by changing the import path.
package bsoncodec

import (
	"reflect"
	"strings"
)

// StructTagParserFunc adapts a function to the struct-tag parsing contract.
type StructTagParserFunc func(reflect.StructField) (StructTags, error)

// ParseStructTags calls the adapted function.
func (parseFunc StructTagParserFunc) ParseStructTags(field reflect.StructField) (StructTags, error) {
	return parseFunc(field)
}

// StructTags is what the struct codec reads from a field's tag.
//
// The key of an exported field is its lower-cased Go name unless the tag
// names one. OmitEmpty drops a zero value, an empty slice or an empty map.
// MinSize writes an integer wider than 32 bits as an int32 when the value
// fits. Truncate lets a BSON double lose precision to fit a float32. Inline
// processes a struct or map field's fields or keys as if they belonged to
// the outer struct. Skip marks a field the codec ignores.
type StructTags struct {
	Name      string
	OmitEmpty bool
	MinSize   bool
	Truncate  bool
	Inline    bool
	Skip      bool
}

// DefaultStructTagParser reads the bson tag, or the whole tag when it holds
// no key:"value" pair. The format is "[<key>][,<flag1>[,<flag2>]]". A field
// with no key gets its lower-cased Go name. A tag that is exactly "-" sets
// Skip and leaves every other field at its zero value. A json tag is never
// read.
var DefaultStructTagParser StructTagParserFunc = func(field reflect.StructField) (StructTags, error) {
	tagText, hasBSONKey := field.Tag.Lookup("bson")
	// A bare tag such as `myb,omitempty` is accepted only when the tag holds
	// no key:"value" pair at all.
	if !hasBSONKey && len(field.Tag) > 0 && !strings.Contains(string(field.Tag), ":") {
		tagText = string(field.Tag)
	}
	return parseTagText(strings.ToLower(field.Name), tagText), nil
}

func parseTagText(defaultName, tagText string) StructTags {
	if tagText == "-" {
		return StructTags{Skip: true}
	}
	parsed := StructTags{Name: defaultName}
	for position, part := range strings.Split(tagText, ",") {
		if position == 0 && part != "" {
			parsed.Name = part
		}
		switch part {
		case "omitempty":
			parsed.OmitEmpty = true
		case "minsize":
			parsed.MinSize = true
		case "truncate":
			parsed.Truncate = true
		case "inline":
			parsed.Inline = true
		}
	}
	return parsed
}
