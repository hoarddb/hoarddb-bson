// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package primitive

import "fmt"

// The legacy types are kept for round-trip fidelity: a document that holds
// one decodes and encodes back to the same bytes. New documents do not need
// them.

// Undefined is the deprecated BSON undefined value.
type Undefined struct{}

// Null is the BSON null value.
type Null struct{}

// DBPointer is the deprecated BSON DBPointer: a namespace string and an
// ObjectID.
type DBPointer struct {
	DB      string
	Pointer ObjectID
}

// String renders the DBPointer as a JSON-like object. The pointer prints
// through ObjectID.String, wrapper included.
func (pointer DBPointer) String() string {
	return fmt.Sprintf(`{"db": "%s", "pointer": "%s"}`, pointer.DB, pointer.Pointer)
}

// Equal reports whether pointer and other are the same.
func (pointer DBPointer) Equal(other DBPointer) bool {
	return pointer == other
}

// IsZero reports whether pointer is the empty DBPointer.
func (pointer DBPointer) IsZero() bool {
	return pointer.DB == "" && pointer.Pointer.IsZero()
}

// JavaScript is the BSON JavaScript code value.
type JavaScript string

// Symbol is the deprecated BSON symbol value.
type Symbol string

// CodeWithScope is the deprecated BSON JavaScript code with scope.
type CodeWithScope struct {
	Code  JavaScript
	Scope interface{}
}

// String renders the code and its scope as a JSON-like object. The scope
// prints with %v and is not escaped.
func (withScope CodeWithScope) String() string {
	return fmt.Sprintf(`{"code": "%s", "scope": %v}`, withScope.Code, withScope.Scope)
}

// MinKey is the BSON value that sorts before every other.
type MinKey struct{}

// MaxKey is the BSON value that sorts after every other.
type MaxKey struct{}
