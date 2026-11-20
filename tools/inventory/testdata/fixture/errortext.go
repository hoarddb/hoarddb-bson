// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package fixture

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func truncatedText(anything interface{}) string {
	_, err := bson.Marshal(anything)
	if err != nil {
		return err.Error()[:20]
	}
	return ""
}

func comparedText(encoded []byte) bool {
	var parsed bson.D
	err := bson.Unmarshal(encoded, &parsed)
	return err != nil && err.Error() == "unexpected EOF"
}

func branchedText(encoded []byte) bool {
	var parsed bson.D
	err := bson.Unmarshal(encoded, &parsed)
	return err != nil && strings.Contains(err.Error(), "invalid")
}

func printedError(encoded []byte) error {
	var parsed bson.D
	if err := bson.Unmarshal(encoded, &parsed); err != nil {
		return fmt.Errorf("decoding request: %v", err)
	}
	return nil
}

func wrappedError(encoded []byte) error {
	var parsed bson.D
	if err := bson.Unmarshal(encoded, &parsed); err != nil {
		return fmt.Errorf("decoding request: %w", err)
	}
	return nil
}

func unrelatedErrorText() string {
	notCodec := errors.New("not from the codec")
	return notCodec.Error()
}

func renderings(identifier primitive.ObjectID, amount primitive.Decimal128, document bson.Raw) string {
	return identifier.Hex() + amount.String() + document.String() + fmt.Sprintf("%v", identifier)
}

func notCodec() error { return errors.New("not from the codec") }

func dial() (string, error) { return "", notCodec() }

// reassignedError reuses err after a codec call: only the use before the
// reassignment is a codec error use.
func reassignedError(document bson.D) string {
	_, err := bson.Marshal(document)
	if err != nil {
		return fmt.Sprintf("encoding: %v", err)
	}
	err = notCodec()
	return err.Error()[:5]
}

// redeclaredError reuses err through a := that declares only dialed.
func redeclaredError(document bson.D) string {
	_, err := bson.Marshal(document)
	if err != nil {
		return ""
	}
	dialed, err := dial()
	return dialed + err.Error()[:5]
}

func loggedCodecError(logger *slog.Logger, encoded []byte) {
	var parsed bson.D
	if err := bson.Unmarshal(encoded, &parsed); err != nil {
		logger.Error("decoding request", "error", err)
	}
}

func sentinelCompared(err error) bool { return err == bson.ErrDecodeToNil }

func sentinelIs(err error) bool { return errors.Is(err, bson.ErrDecodeToNil) }

// conditionalReassign is shaped like the TTL sweeper: the codec error survives
// to the log call because the reassignments sit in blocks that do not enclose it.
func conditionalReassign(document bson.D, logger *slog.Logger) {
	_, err := bson.Marshal(document)
	if err == nil {
		if _, dialErr := dial(); dialErr != nil {
			err = dialErr
		} else {
			err = notCodec()
		}
	}
	if err != nil {
		logger.Warn("expiring", "error", err)
	}
}

// closureLocalError declares a different err inside the closure.
func closureLocalError(document bson.D) string {
	if _, err := bson.Marshal(document); err != nil {
		return ""
	}
	run := func() string {
		_, err := dial()
		return err.Error()[:5]
	}
	return run()
}
