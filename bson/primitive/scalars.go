// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package primitive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// DateTime is the BSON datetime: milliseconds since the Unix epoch, negative
// before it.
type DateTime int64

var (
	_ json.Marshaler   = DateTime(0)
	_ json.Unmarshaler = (*DateTime)(nil)
)

// MarshalJSON marshals the DateTime as an RFC 3339 string in UTC. Years
// outside 0 to 9999 are an error, as for time.Time.
func (dateTime DateTime) MarshalJSON() ([]byte, error) {
	return json.Marshal(dateTime.Time().UTC())
}

// UnmarshalJSON sets the DateTime from an RFC 3339 JSON string, dropping
// anything finer than a millisecond. JSON null leaves the receiver unchanged,
// as it does for time.Time.
func (dateTime *DateTime) UnmarshalJSON(jsonBytes []byte) error {
	if string(jsonBytes) == "null" {
		return nil
	}
	var parsed time.Time
	if err := json.Unmarshal(jsonBytes, &parsed); err != nil {
		return err
	}
	*dateTime = NewDateTimeFromTime(parsed)
	return nil
}

// Time returns the DateTime as a time.Time in the local zone.
func (dateTime DateTime) Time() time.Time {
	return time.Unix(int64(dateTime)/1000, int64(dateTime)%1000*1_000_000)
}

// NewDateTimeFromTime converts a time.Time, dropping anything finer than a
// millisecond. The floor is toward the earlier millisecond for times before
// the epoch too, because Unix() floors and Nanosecond() is never negative.
func NewDateTimeFromTime(instant time.Time) DateTime {
	return DateTime(instant.Unix()*1e3 + int64(instant.Nanosecond())/1e6)
}

// Timestamp is the BSON timestamp: seconds and an ordinal within the second,
// meant for ordering events such as replication log entries.
type Timestamp struct {
	T uint32
	I uint32
}

// After reports whether tp is after other.
func (tp Timestamp) After(other Timestamp) bool {
	return tp.T > other.T || (tp.T == other.T && tp.I > other.I)
}

// Before reports whether tp is before other.
func (tp Timestamp) Before(other Timestamp) bool {
	return tp.T < other.T || (tp.T == other.T && tp.I < other.I)
}

// Equal reports whether tp and other are the same.
func (tp Timestamp) Equal(other Timestamp) bool {
	return tp.T == other.T && tp.I == other.I
}

// IsZero reports whether tp is the zero Timestamp.
func (tp Timestamp) IsZero() bool {
	return tp.T == 0 && tp.I == 0
}

// Compare returns -1 if tp is before other, +1 if it is after and 0 if they
// are the same.
func (tp Timestamp) Compare(other Timestamp) int {
	switch {
	case tp.Equal(other):
		return 0
	case tp.Before(other):
		return -1
	default:
		return +1
	}
}

// CompareTimestamp compares two Timestamps like Timestamp.Compare.
//
// Deprecated: Use Timestamp.Compare instead.
func CompareTimestamp(left, right Timestamp) int {
	return left.Compare(right)
}

// Binary is the BSON binary: a subtype byte and the data.
type Binary struct {
	Subtype byte
	Data    []byte
}

// Equal reports whether bp and other have the same subtype and data.
func (bp Binary) Equal(other Binary) bool {
	return bp.Subtype == other.Subtype && bytes.Equal(bp.Data, other.Data)
}

// IsZero reports whether bp is the empty Binary of subtype 0.
func (bp Binary) IsZero() bool {
	return bp.Subtype == 0 && len(bp.Data) == 0
}

// Regex is the BSON regular expression: a pattern and its option letters.
type Regex struct {
	Pattern string
	Options string
}

// String renders the Regex as a JSON-like object. It does not escape the
// pattern, which is what call sites that print it already see.
func (rp Regex) String() string {
	return fmt.Sprintf(`{"pattern": "%s", "options": "%s"}`, rp.Pattern, rp.Options)
}

// Equal reports whether rp and other have the same pattern and options.
func (rp Regex) Equal(other Regex) bool {
	return rp.Pattern == other.Pattern && rp.Options == other.Options
}

// IsZero reports whether rp is the empty Regex.
func (rp Regex) IsZero() bool {
	return rp.Pattern == "" && rp.Options == ""
}
