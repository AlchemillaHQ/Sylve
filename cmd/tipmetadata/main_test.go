// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

func TestMetadataForBuild(t *testing.T) {
	info := fixtureBuildInfo()
	builtAt := time.Date(2026, 10, 6, 10, 30, 0, 0, time.FixedZone("offset", 3600))
	metadata, err := metadataForBuild(info, "0.3.1", "amd64", "15.1-RELEASE", testCommit, builtAt)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Commit != testCommit || metadata.Version != "0.3.1" || metadata.Go != "go1.26.8" || metadata.FreeBSD != "15.1-RELEASE" || metadata.BuiltAt != "2026-10-06T09:30:00Z" {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
}

func TestMetadataRejectsWrongBuild(t *testing.T) {
	for _, name := range []string{"not sylve", "wrong OS", "wrong arch", "wrong source", "short commit", "overridden commit"} {
		t.Run(name, func(t *testing.T) {
			info := fixtureBuildInfo()
			switch name {
			case "not sylve":
				info.Path = "another/program"
			case "wrong OS":
				info.Settings[0].Value = "linux"
			case "wrong arch":
				info.Settings[1].Value = "arm64"
			case "wrong source":
				info.Settings[2].Value = strings.Repeat("a", 40)
			case "short commit":
				info.Settings[3].Value = strings.ReplaceAll(info.Settings[3].Value, testCommit, testCommit[:7])
			case "overridden commit":
				info.Settings[3].Value += " -X github.com/alchemillahq/sylve/internal/cmd.Commit=wrong"
			}
			if _, err := metadataForBuild(info, "0.3.1", "amd64", "15.1-RELEASE", testCommit, time.Now()); err == nil {
				t.Fatal("accepted mismatched binary build information")
			}
		})
	}
}

func TestGenerate(t *testing.T) {
	dir := fixtureRelease(t)
	if err := os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("another published asset"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := generate(dir, testCommit); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "sylve-version.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]string
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 5 || metadata["version"] != "0.3.1" || metadata["commit"] != testCommit || metadata["builtAt"] != "2026-10-06T10:31:00Z" || metadata["go"] != "go1.26.8" || metadata["freebsd"] != "15.1-RELEASE" {
		t.Fatalf("unexpected flat manifest: %s", data)
	}
	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(sums)), "\n")
	if len(lines) != len(payloadNames)+2 {
		t.Fatalf("unexpected checksum entries: %s", sums)
	}
	for _, line := range lines {
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 || len(parts[0]) != 64 {
			t.Fatalf("invalid GNU checksum line: %q", line)
		}
		if strings.HasPrefix(parts[1], "build-") || parts[1] == "SHA256SUMS" {
			t.Fatalf("included private build record or recursive checksum: %q", line)
		}
		asset, err := os.ReadFile(filepath.Join(dir, parts[1]))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(asset)
		if parts[0] != hex.EncodeToString(digest[:]) {
			t.Fatalf("checksum mismatch for %s", parts[1])
		}
	}
	if err := generate(dir, testCommit); err != nil {
		t.Fatalf("regeneration: %v", err)
	}
	again, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil || string(again) != string(sums) {
		t.Fatalf("metadata generation must be deterministic: %v", err)
	}
}

func TestGenerateRejectsMixedOrIncompleteAssets(t *testing.T) {
	for _, name := range []string{"missing binary", "changed binary", "missing record", "wrong commit", "wrong arch", "wrong version", "wrong Go", "wrong FreeBSD", "invalid time", "missing web assets"} {
		t.Run(name, func(t *testing.T) {
			dir := fixtureRelease(t)
			recordPath := filepath.Join(dir, "build-arm64.json")
			data, err := os.ReadFile(recordPath)
			if err != nil {
				t.Fatal(err)
			}
			var record buildRecord
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "missing binary":
				err = os.Remove(filepath.Join(dir, "sylve-arm64"))
			case "changed binary":
				err = os.WriteFile(filepath.Join(dir, "sylve-arm64"), []byte("stale binary"), 0o644)
			case "missing record":
				err = os.Remove(recordPath)
			case "wrong commit":
				record.Commit = strings.Repeat("a", 40)
			case "wrong arch":
				record.Arch = "amd64"
			case "wrong version":
				record.Version = "0.3.0"
			case "wrong Go":
				record.Go = "go1.26.9"
			case "wrong FreeBSD":
				record.FreeBSD = "15.0-RELEASE"
			case "invalid time":
				record.BuiltAt = "not-a-timestamp"
			case "missing web assets":
				err = os.Remove(filepath.Join(dir, "sylve-web-assets.tar.gz"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if name != "missing record" {
				if err := writeJSON(recordPath, record); err != nil {
					t.Fatal(err)
				}
			}
			if err := generate(dir, testCommit); err == nil {
				t.Fatal("generated metadata for mixed or incomplete assets")
			}
			if _, err := os.Stat(filepath.Join(dir, "SHA256SUMS")); !os.IsNotExist(err) {
				t.Fatal("published a checksum manifest despite failed validation")
			}
		})
	}
}

func TestRunRequiresFullCommit(t *testing.T) {
	for _, commit := range []string{"", "unknown", testCommit[:7]} {
		if err := run([]string{"generate", "-commit", commit, "-dir", t.TempDir()}); err == nil {
			t.Fatalf("accepted non-full commit %q", commit)
		}
	}
}

func fixtureBuildInfo() *debug.BuildInfo {
	return &debug.BuildInfo{
		Path: "github.com/alchemillahq/sylve/cmd/sylve", GoVersion: "go1.26.8",
		Settings: []debug.BuildSetting{
			{Key: "GOOS", Value: "freebsd"},
			{Key: "GOARCH", Value: "amd64"},
			{Key: "vcs.revision", Value: testCommit},
			{Key: "-ldflags", Value: "-s -w -X github.com/alchemillahq/sylve/internal/cmd.Commit=" + testCommit},
		},
	}
}

func fixtureRelease(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range payloadNames {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+" contents"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i, arch := range []string{"amd64", "arm64"} {
		asset, err := os.ReadFile(filepath.Join(dir, "sylve-"+arch))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(asset)
		record := buildRecord{
			versionInfo: versionInfo{
				Version: "0.3.1", Commit: testCommit, BuiltAt: time.Date(2026, 10, 6, 10, 30+i, 0, 0, time.UTC).Format(time.RFC3339),
				Go: "go1.26.8", FreeBSD: "15.1-RELEASE",
			},
			Arch: arch, SHA256: hex.EncodeToString(digest[:]),
		}
		if err := writeJSON(filepath.Join(dir, "build-"+arch+".json"), record); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
