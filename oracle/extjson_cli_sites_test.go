// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"testing"

	"github.com/hoarddb/hoarddb-bson/bson"
)

// The hoardDB CLI sites that print, truncate or branch on an Extended JSON
// error (inventory item 5, spec 9.1.5 and call C18). Each decodes in relaxed
// mode into its own target, and each shows the error its own way. The
// expected text at each site is the vendor's, from the vendor-generated
// error table (its map or bson.D column), put through the site's own
// rendering; the library's error is put through the same rendering.
type extJSONCLISite struct {
	name string
	// ordered is the bson.D target; otherwise the site decodes into a
	// *map[string]interface{}.
	ordered bool
	// render is what the site shows of the outcome: the text, the text cut
	// to 100 bytes, or only whether there was an error.
	render func(outcome string) string
}

func showWhole(outcome string) string { return outcome }

// truncateForMessage is hoardDB's cli/ingest_read.go:truncateForMessage.
func truncateForMessage(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}

var extJSONCLISites = []extJSONCLISite{
	// usagef("could not read document %s: %v", original, err)
	{name: "cli/extjson_input.go:decodeDataDocument", render: showWhole},
	// if bson.UnmarshalExtJSON(encoded, false, &decoded) == nil { return nil }
	{name: "cli/extjson_input.go:refusedWrapper", render: func(outcome string) string {
		if outcome == "ok" {
			return "accepted"
		}
		return "refused"
	}},
	// usagef("parsing date literal %q: %v", literal, err)
	{name: "cli/date_functions.go:parseDateLiteral", render: showWhole},
	// usagef("date.parse(%q): %v", argStr, err), after a first refusal
	{name: "cli/date_functions.go:evaluateDateParse", render: showWhole},
	// "the line is not valid Extended JSON (" + truncateForMessage(err.Error(), 100) + ")"
	{name: "cli/ingest_read.go:decodeJSONLine", ordered: true, render: func(outcome string) string {
		return truncateForMessage(outcome, 100)
	}},
	// fmt.Errorf("cannot be read as Extended JSON: %w", err)
	{name: "cli/ingest_metadata.go:parseMetadata", ordered: true, render: showWhole},
}

// TestExtJSONErrorTextAtCLISites runs every row of the vendor-generated
// error table through the library at each CLI site, with the site's target
// and mode, and requires that the site shows what it shows with the vendor:
// the vendor's text from the table's column for that target, rendered by
// the site. The table is first checked to be what the vendor generates now.
func TestExtJSONErrorTextAtCLISites(t *testing.T) {
	rows := loadExtJSONErrorTable(t)
	for _, site := range extJSONCLISites {
		t.Run(site.name, func(t *testing.T) {
			var refusals int
			for _, row := range rows {
				vendorOutcomeText := string(row.Relaxed.Map)
				var libraryTarget any = &map[string]any{}
				if site.ordered {
					vendorOutcomeText = string(row.Relaxed.Ordered)
					libraryTarget = &bson.D{}
				}
				libraryOutcomeText, _ := decLibraryOutcome(func() error { return bson.UnmarshalExtJSON([]byte(row.Input), false, libraryTarget) })
				if vendorOutcomeText != "ok" {
					refusals++
				}
				if got, want := site.render(libraryOutcomeText), site.render(vendorOutcomeText); got != want {
					t.Errorf("%s: the site shows %q with the library, %q with the vendor\n  input %q", row.Name, got, want, clipped(string(row.Input)))
				}
			}
			t.Logf("%d rows, %d refused by the vendor at this site's target", len(rows), refusals)
		})
	}
}
