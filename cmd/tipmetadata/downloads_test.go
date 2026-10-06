// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadNotes(t *testing.T) {
	release := releaseDownloads{Body: "Rolling prerelease for Sylve tip."}
	for _, step := range []struct {
		name   string
		assets []downloadAsset
		amd64  uint64
		arm64  uint64
	}{
		{"initial", []downloadAsset{{1, "sylve-amd64", 100}, {2, "sylve-arm64", 20}, {3, "SHA256SUMS", 50}}, 100, 20},
		{"retry before replacement", []downloadAsset{{1, "sylve-amd64", 100}, {2, "sylve-arm64", 20}}, 100, 20},
		{"more downloads before replacement", []downloadAsset{{1, "sylve-amd64", 110}, {2, "sylve-arm64", 25}}, 110, 25},
		{"partially replaced", []downloadAsset{{4, "sylve-amd64", 5}, {2, "sylve-arm64", 25}}, 115, 25},
		{"retry after partial replacement", []downloadAsset{{4, "sylve-amd64", 5}, {2, "sylve-arm64", 25}}, 115, 25},
		{"asset removed before upload", []downloadAsset{{4, "sylve-amd64", 5}}, 115, 25},
		{"both replaced", []downloadAsset{{4, "sylve-amd64", 7}, {5, "sylve-arm64", 3}}, 117, 28},
		{"assets cleared", nil, 117, 28},
	} {
		t.Run(step.name, func(t *testing.T) {
			release.Assets = step.assets
			notes, err := downloadNotes(release)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(notes, "Rolling prerelease for Sylve tip.\n\n") || strings.Count(notes, "### Recorded downloads\n") != 1 || strings.Contains(notes, "updated") || strings.Contains(notes, "| Architecture |") {
				t.Fatalf("unexpected release description: %s", notes)
			}
			for arch, total := range map[string]uint64{"amd64": step.amd64, "arm64": step.arm64} {
				badge := fmt.Sprintf("![%s downloads: %d](https://img.shields.io/badge/%s-%d-blue)", arch, total, arch, total)
				if !strings.Contains(notes, badge) {
					t.Fatalf("incorrect recorded downloads for %s: %s", arch, notes)
				}
			}
			state := readDownloadCheckpoint(t, notes)
			if len(state) != 2 || state["sylve-amd64"].Total != step.amd64 || state["sylve-arm64"].Total != step.arm64 {
				t.Fatalf("unexpected checkpoints: %+v", state)
			}
			if strings.Contains(step.name, "retry") && notes != release.Body {
				t.Fatal("a retry with unchanged counts must not change the notes")
			}
			release.Body = notes
		})
	}
	state := readDownloadCheckpoint(t, release.Body)
	if state["sylve-amd64"].AssetID != 4 || state["sylve-amd64"].Count != 7 || state["sylve-arm64"].AssetID != 5 || state["sylve-arm64"].Count != 3 {
		t.Fatalf("lost current asset checkpoints: %+v", state)
	}
}

func TestDownloadNotesPreservesSurroundingNotes(t *testing.T) {
	release := releaseDownloads{Body: "Custom introduction.\n\n## Changes\n\nRelease details."}
	notes, err := downloadNotes(release)
	if err != nil {
		t.Fatal(err)
	}
	release.Body = notes + "\nA manually added footer.\n"
	release.Assets = []downloadAsset{{1, "sylve-amd64", 10}}
	updated, err := downloadNotes(release)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(updated, "Custom introduction.\n\n## Changes\n\nRelease details.\n\n") || !strings.HasSuffix(updated, "\nA manually added footer.\n") {
		t.Fatalf("overwrote unrelated release notes: %s", updated)
	}
}

func TestDownloadNotesBadgesShareLine(t *testing.T) {
	notes, err := downloadNotes(releaseDownloads{})
	if err != nil {
		t.Fatal(err)
	}
	row := "![amd64 downloads: 0](https://img.shields.io/badge/amd64-0-blue) " +
		"![arm64 downloads: 0](https://img.shields.io/badge/arm64-0-blue)"
	if !strings.Contains(notes, "\n"+row+"\n\n") {
		t.Fatalf("badges must share one Markdown line: %s", notes)
	}
}

func TestDownloadNotesReplacesTableWithBadges(t *testing.T) {
	body := downloadsStart + "\n### Recorded downloads\n\n" +
		"| Architecture | Downloads |\n| --- | ---: |\n| amd64 | 100 |\n| arm64 | 20 |\n" +
		`<!-- sylve-tip-downloads-state: {"sylve-amd64":{"total":100,"assetId":1,"count":100},"sylve-arm64":{"total":20,"assetId":2,"count":20}} -->` +
		"\n" + downloadsEnd
	notes, err := downloadNotes(releaseDownloads{Body: body, Assets: []downloadAsset{{3, "sylve-amd64", 5}, {2, "sylve-arm64", 20}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(notes, "| Architecture |") || !strings.Contains(notes, "https://img.shields.io/badge/amd64-105-blue") || !strings.Contains(notes, "https://img.shields.io/badge/arm64-20-blue") {
		t.Fatalf("did not replace the table with badges: %s", notes)
	}
	state := readDownloadCheckpoint(t, notes)
	if state["sylve-amd64"] != (downloadCheckpoint{Total: 105, AssetID: 3, Count: 5}) || state["sylve-arm64"] != (downloadCheckpoint{Total: 20, AssetID: 2, Count: 20}) {
		t.Fatalf("lost download history when replacing the table: %+v", state)
	}
}

func TestDownloadNotesRejectsInvalidCheckpoints(t *testing.T) {
	notes, err := downloadNotes(releaseDownloads{Assets: []downloadAsset{{1, "sylve-amd64", 10}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		body string
	}{
		{"missing start", strings.Replace(notes, downloadsStart, "", 1)},
		{"missing end", strings.Replace(notes, downloadsEnd, "", 1)},
		{"duplicate section", notes + notes},
		{"missing state", downloadsStart + "\n### Recorded downloads\n" + downloadsEnd},
		{"invalid JSON", downloadsStart + "\n<!-- sylve-tip-downloads-state: broken -->\n" + downloadsEnd},
		{"null state", downloadsStart + "\n<!-- sylve-tip-downloads-state: null -->\n" + downloadsEnd},
		{"empty state", downloadsStart + "\n<!-- sylve-tip-downloads-state: {} -->\n" + downloadsEnd},
		{"inconsistent total", strings.Replace(notes, `"total":10`, `"total":9`, 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := downloadNotes(releaseDownloads{Body: test.body}); err == nil {
				t.Fatal("discarded invalid saved download history")
			}
		})
	}
	for _, asset := range []downloadAsset{{0, "sylve-amd64", 10}, {1, "sylve-amd64", 9}} {
		if _, err := downloadNotes(releaseDownloads{Body: notes, Assets: []downloadAsset{asset}}); err == nil {
			t.Fatalf("accepted invalid download snapshot: %+v", asset)
		}
	}
}

func TestRunDownloads(t *testing.T) {
	dir := t.TempDir()
	releasePath := filepath.Join(dir, "release.json")
	notesPath := filepath.Join(dir, "notes.md")
	data := `{"body":"Existing notes.","assets":[{"id":123,"name":"sylve-amd64","download_count":42}]}`
	if err := os.WriteFile(releasePath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"downloads", "-release", releasePath, "-out", notesPath}); err != nil {
		t.Fatal(err)
	}
	notes, err := os.ReadFile(notesPath)
	if err != nil {
		t.Fatal(err)
	}
	state := readDownloadCheckpoint(t, string(notes))
	if state["sylve-amd64"] != (downloadCheckpoint{Total: 42, AssetID: 123, Count: 42}) || state["sylve-arm64"] != (downloadCheckpoint{}) {
		t.Fatalf("incorrect GitHub API snapshot: %+v", state)
	}
	if err := run([]string{"downloads"}); err == nil {
		t.Fatal("accepted missing release and output paths")
	}
}

func readDownloadCheckpoint(t *testing.T, notes string) map[string]downloadCheckpoint {
	t.Helper()
	match := downloadsStatePattern.FindStringSubmatch(notes)
	if len(match) != 2 {
		t.Fatalf("missing hidden checkpoint: %s", notes)
	}
	var state map[string]downloadCheckpoint
	if err := json.Unmarshal([]byte(match[1]), &state); err != nil {
		t.Fatal(err)
	}
	return state
}
