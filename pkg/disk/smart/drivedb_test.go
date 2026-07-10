// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package smart

import "testing"

func TestDriveDBUsesFullStringMatches(t *testing.T) {
	db := compileDriveDB([]DriveModelEntry{{
		ModelPattern:  "MODEL-[0-9]+",
		FirmwareRegex: "FW[0-9]+",
		AttrOverrides: map[uint32]AttrDef{1: {Name: "matched"}},
	}})

	if got := lookupModelAttrs(db, "MODEL-123", "FW7"); got[1].Name != "matched" {
		t.Fatalf("valid full match was missed: %#v", got)
	}
	if got := lookupModelAttrs(db, "prefix MODEL-123 suffix", "FW7"); len(got) != 0 {
		t.Fatalf("substring model unexpectedly matched: %#v", got)
	}
	if got := lookupModelAttrs(db, "MODEL-123", "FW7-extra"); len(got) != 0 {
		t.Fatalf("substring firmware unexpectedly matched: %#v", got)
	}
}

func TestDriveDBFirstMatchWins(t *testing.T) {
	db := compileDriveDB([]DriveModelEntry{
		{Family: "first-family", ModelPattern: "MODEL.*", Warning: "warning", FirmwareBugs: FirmwareBugSamsung, AttrOverrides: map[uint32]AttrDef{1: {Name: "first"}}},
		{ModelPattern: "MODEL-1", AttrOverrides: map[uint32]AttrDef{1: {Name: "second"}, 2: {Name: "merged"}}},
	})
	got := lookupModelAttrs(db, "MODEL-1", "")
	if len(got) != 1 || got[1].Name != "first" {
		t.Fatalf("entries were merged or precedence changed: %#v", got)
	}
	match, ok := lookupDrive(db, "MODEL-1", "")
	if !ok || match.Family != "first-family" || match.Warning != "warning" || match.FirmwareBugs != FirmwareBugSamsung {
		t.Fatalf("drive metadata was lost: %#v, %v", match, ok)
	}
}

func TestGeneratedDriveDBCompleteness(t *testing.T) {
	if len(modelDB) < 800 {
		t.Fatalf("generated only %d drive records", len(modelDB))
	}
	if len(compiledDB) != len(modelDB) {
		t.Fatalf("%d of %d generated model regexes did not compile", len(modelDB)-len(compiledDB), len(modelDB))
	}
	var firmwareBugEntries int
	for _, entry := range modelDB {
		if entry.FirmwareBugs != 0 {
			firmwareBugEntries++
		}
	}
	if firmwareBugEntries < 20 {
		t.Fatalf("only %d firmware-bug records were retained", firmwareBugEntries)
	}
}

func BenchmarkLookupDrive(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = lookupDrive(compiledDB, "CONSISTENT SSD S7 256GB", "ACEC29B5")
	}
}

func TestDriveDBSkipsInvalidRegexes(t *testing.T) {
	db := compileDriveDB([]DriveModelEntry{
		{ModelPattern: "[", AttrOverrides: map[uint32]AttrDef{1: {Name: "bad-model"}}},
		{ModelPattern: "GOOD", FirmwareRegex: "[", AttrOverrides: map[uint32]AttrDef{1: {Name: "bad-fw"}}},
		{ModelPattern: "GOOD", AttrOverrides: map[uint32]AttrDef{1: {Name: "good"}}},
	})
	if len(db) != 1 {
		t.Fatalf("invalid entries were retained: %d", len(db))
	}
	if got := lookupModelAttrs(db, "GOOD", ""); got[1].Name != "good" {
		t.Fatalf("valid fallback did not match: %#v", got)
	}
}
