// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: Raw's methods on the library's bsoncore; String renders the ErrNestingTooDeep refusal past MaxNestingCeiling; ErrNilReader, ReadDocument and NewFromIOReader left out (not in the inventory); Validate notes that it is shallow

package bson

import (
	"github.com/hoarddb/hoarddb-bson/x/bsonx/bsoncore"
)

// Raw is a raw encoded BSON document. It can be used to delay BSON document decoding or precompute
// a BSON encoded document.
//
// A Raw must be a full BSON document. Use the RawValue type for individual BSON values.
type Raw []byte

// Validate validates the document. This method only validates the first document in
// the slice, to validate other documents, the slice must be resliced.
//
// It is shallow: it reads each element's length, not inside embedded documents. It is not the
// check a document from outside the process needs; that is the package function Validate.
func (r Raw) Validate() (err error) { return bsoncore.Document(r).Validate() }

// Lookup search the document, potentially recursively, for the given key. If
// there are multiple keys provided, this method will recurse down, as long as
// the top and intermediate nodes are either documents or arrays.If an error
// occurs or if the value doesn't exist, an empty RawValue is returned.
func (r Raw) Lookup(key ...string) RawValue {
	return convertFromCoreValue(bsoncore.Document(r).Lookup(key...))
}

// LookupErr searches the document and potentially subdocuments or arrays for the
// provided key. Each key provided to this method represents a layer of depth.
func (r Raw) LookupErr(key ...string) (RawValue, error) {
	val, err := bsoncore.Document(r).LookupErr(key...)
	return convertFromCoreValue(val), err
}

// Elements returns this document as a slice of elements. The returned slice will contain valid
// elements. If the document is not valid, the elements up to the invalid point will be returned
// along with an error.
func (r Raw) Elements() ([]RawElement, error) {
	doc := bsoncore.Document(r)
	if len(doc) == 0 {
		return nil, nil
	}
	elems, err := doc.Elements()
	if err != nil {
		return nil, err
	}
	relems := make([]RawElement, 0, len(elems))
	for _, elem := range elems {
		relems = append(relems, RawElement(elem))
	}
	return relems, nil
}

// Values returns this document as a slice of values. The returned slice will contain valid values.
// If the document is not valid, the values up to the invalid point will be returned along with an
// error.
func (r Raw) Values() ([]RawValue, error) {
	vals, err := bsoncore.Document(r).Values()
	rvals := make([]RawValue, 0, len(vals))
	for _, val := range vals {
		rvals = append(rvals, convertFromCoreValue(val))
	}
	return rvals, err
}

// Index searches for and retrieves the element at the given index. This method will panic if
// the document is invalid or if the index is out of bounds.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Document.Index panics where the
// vendor's does. IndexErr is the form that returns an error instead.
func (r Raw) Index(index uint) RawElement { return RawElement(bsoncore.Document(r).Index(index)) }

// IndexErr searches for and retrieves the element at the given index.
func (r Raw) IndexErr(index uint) (RawElement, error) {
	elem, err := bsoncore.Document(r).IndexErr(index)
	return RawElement(elem), err
}

// String returns the BSON document encoded as Extended JSON. A document nested deeper than
// MaxNestingCeiling renders as a refusal naming ErrNestingTooDeep, not as JSON.
func (r Raw) String() string {
	return refuseDocumentPastCeiling(bsoncore.Document(r).String(), "", r)
}
