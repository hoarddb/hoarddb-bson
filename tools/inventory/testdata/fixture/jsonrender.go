// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package fixture

import (
	"encoding/json"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Event carries a vendor type that encoding/json renders through MarshalJSON.
type Event struct {
	At primitive.DateTime `json:"at"`
}

func eventJSON(event Event) ([]byte, error) { return json.Marshal(event) }
