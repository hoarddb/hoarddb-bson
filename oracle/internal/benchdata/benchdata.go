// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

// Package benchdata defines the benchmark inputs of build doc section 4.0
// once, so every benchmark and allocation test reads the same bytes.
package benchdata

import (
	"fmt"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorprimitive "go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/hoarddb/hoarddb-bson/bson/primitive"
)

// Ref450Size is the encoded size of Ref450, inside the 450 ± 8 bytes the
// build doc sets.
const Ref450Size = 450

// Address is Ref450's embedded document.
type Address struct {
	Street  string `bson:"street"`
	City    string `bson:"city"`
	Zip     string `bson:"zip"`
	Country string `bson:"country"`
}

// Record is the shape of Ref450: ten top-level fields of the build doc's
// listed types, one of them an embedded document and one an array.
type Record struct {
	ID      vendorprimitive.ObjectID `bson:"_id"`
	Name    string                   `bson:"name"`
	Email   string                   `bson:"email"`
	Notes   string                   `bson:"notes"`
	Age     int32                    `bson:"age"`
	Score   float64                  `bson:"score"`
	Active  bool                     `bson:"active"`
	Created vendorprimitive.DateTime `bson:"created"`
	Tags    []string                 `bson:"tags"`
	Address Address                  `bson:"address"`
}

// Ref450Record is the Go struct form of Ref450. Every value is fixed, so
// the bytes are the same on every run.
func Ref450Record() Record {
	return Record{
		ID:      vendorprimitive.ObjectID{0x65, 0x3a, 0x1f, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88},
		Name:    "Ada Example",
		Email:   "ada.example@example.com",
		Notes:   ref450Notes,
		Age:     36,
		Score:   87.25,
		Active:  true,
		Created: vendorprimitive.DateTime(1793836800000),
		Tags:    []string{"reader", "writer", "admin"},
		Address: Address{Street: "12 Sample Street", City: "Exampleton", Zip: "00000", Country: "Nowhere"},
	}
}

// LibRecord is Record in the library's primitive types, for the library's
// side of an encode benchmark or allocation test.
type LibRecord struct {
	ID      primitive.ObjectID `bson:"_id"`
	Name    string             `bson:"name"`
	Email   string             `bson:"email"`
	Notes   string             `bson:"notes"`
	Age     int32              `bson:"age"`
	Score   float64            `bson:"score"`
	Active  bool               `bson:"active"`
	Created primitive.DateTime `bson:"created"`
	Tags    []string           `bson:"tags"`
	Address Address            `bson:"address"`
}

// Ref450LibRecord is Ref450Record with the library's types: the same values,
// so the two encode to the same bytes.
func Ref450LibRecord() LibRecord {
	record := Ref450Record()
	return LibRecord{
		ID: primitive.ObjectID(record.ID), Name: record.Name, Email: record.Email, Notes: record.Notes,
		Age: record.Age, Score: record.Score, Active: record.Active, Created: primitive.DateTime(record.Created),
		Tags: record.Tags, Address: record.Address,
	}
}

// ref450Notes pads the record to Ref450Size; TestRef450IsItsSize holds it there.
const ref450Notes = "Synthetic benchmark record. It carries no real person, place or value. " +
	"The text fills the document to the size the build doc fixes for its reference input..."

// Ref450D is the ordered-document form of Ref450, in the vendor's types.
func Ref450D() vendorbson.D {
	record := Ref450Record()
	return vendorbson.D{
		{Key: "_id", Value: record.ID},
		{Key: "name", Value: record.Name},
		{Key: "email", Value: record.Email},
		{Key: "notes", Value: record.Notes},
		{Key: "age", Value: record.Age},
		{Key: "score", Value: record.Score},
		{Key: "active", Value: record.Active},
		{Key: "created", Value: record.Created},
		{Key: "tags", Value: vendorbson.A{record.Tags[0], record.Tags[1], record.Tags[2]}},
		{Key: "address", Value: vendorbson.D{
			{Key: "street", Value: record.Address.Street},
			{Key: "city", Value: record.Address.City},
			{Key: "zip", Value: record.Address.Zip},
			{Key: "country", Value: record.Address.Country},
		}},
	}
}

// Ref450Bytes is the raw form of Ref450, as the vendor encodes the struct.
func Ref450Bytes() ([]byte, error) {
	encoded, err := vendorbson.Marshal(Ref450Record())
	if err != nil {
		return nil, fmt.Errorf("encoding Ref450: %w", err)
	}
	return encoded, nil
}
