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
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"
const testBuiltAt = "2026-10-06T10:30:00Z"

func TestMetadataForBuild(t *testing.T) {
	info := fixtureBuildInfo()
	metadata, err := metadataForBuild(info, "0.3.1", "amd64", "15.1-RELEASE", testCommit)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Commit != testCommit || metadata.Version != "0.3.1" || metadata.Go != "go1.26.8" || metadata.FreeBSD != "15.1-RELEASE" || metadata.BuiltAt != testBuiltAt {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
	info.Settings[3].Value = strings.ReplaceAll(info.Settings[3].Value, testBuiltAt, "2026-10-06T12:30:00+02:00")
	metadata, err = metadataForBuild(info, "0.3.1", "amd64", "15.1-RELEASE", testCommit)
	if err != nil || metadata.BuiltAt != testBuiltAt {
		t.Fatalf("embedded build timestamp was not normalized: %+v %v", metadata, err)
	}
}

func TestMetadataRejectsWrongBuild(t *testing.T) {
	for _, name := range []string{"not sylve", "wrong OS", "wrong arch", "wrong source", "short commit", "overridden commit", "missing time", "invalid time", "zero time", "overridden time"} {
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
			case "missing time":
				info.Settings[3].Value = strings.Split(info.Settings[3].Value, " -X github.com/alchemillahq/sylve/internal/cmd.BuiltAt=")[0]
			case "invalid time":
				info.Settings[3].Value = strings.ReplaceAll(info.Settings[3].Value, testBuiltAt, "unknown")
			case "zero time":
				info.Settings[3].Value = strings.ReplaceAll(info.Settings[3].Value, testBuiltAt, "0001-01-01T00:00:00Z")
			case "overridden time":
				info.Settings[3].Value += " -X github.com/alchemillahq/sylve/internal/cmd.BuiltAt=unknown"
			}
			if _, err := metadataForBuild(info, "0.3.1", "amd64", "15.1-RELEASE", testCommit); err == nil {
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
	if len(metadata) != 5 || metadata["version"] != "0.3.1" || metadata["commit"] != testCommit || metadata["builtAt"] != testBuiltAt || metadata["go"] != "go1.26.8" || metadata["freebsd"] != "15.1-RELEASE" {
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

func TestGenerateAcceptsEquivalentArchitectureTimestamps(t *testing.T) {
	dir := fixtureRelease(t)
	path := filepath.Join(dir, "build-arm64.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record buildRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	record.BuiltAt = "2026-10-06T12:30:00+02:00"
	if err := writeJSON(path, record); err != nil {
		t.Fatal(err)
	}
	if err := generate(dir, testCommit); err != nil {
		t.Fatalf("equivalent timestamps disagreed: %v", err)
	}
}

func TestGenerateRejectsMixedOrIncompleteAssets(t *testing.T) {
	for _, name := range []string{"missing binary", "changed binary", "missing record", "wrong commit", "wrong arch", "wrong version", "wrong Go", "wrong FreeBSD", "invalid time", "zero time", "different time", "missing web assets", "missing helper", "missing compressed helper", "missing helper manifest", "changed helper", "wrong helper digest"} {
		t.Run(name, func(t *testing.T) {
			dir := fixtureRelease(t)
			recordPath := filepath.Join(dir, "build-arm64.json")
			if name == "wrong helper digest" {
				recordPath = filepath.Join(dir, "build-amd64.json")
			}
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
			case "zero time":
				record.BuiltAt = "0001-01-01T00:00:00Z"
			case "different time":
				record.BuiltAt = "2026-10-06T10:31:00Z"
			case "missing web assets":
				err = os.Remove(filepath.Join(dir, "sylve-web-assets.tar.gz"))
			case "missing helper":
				err = os.Remove(filepath.Join(dir, "CSMWrap.iso"))
			case "missing compressed helper":
				err = os.Remove(filepath.Join(dir, "CSMWrap.iso.gz"))
			case "missing helper manifest":
				err = os.Remove(filepath.Join(dir, "CSMWrap-build-manifest.json"))
			case "changed helper":
				err = os.WriteFile(filepath.Join(dir, "CSMWrap.iso"), []byte("stale helper"), 0o644)
			case "wrong helper digest":
				record.CSMWrapSHA256 = strings.Repeat("a", 64)
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
			{Key: "-ldflags", Value: "-s -w -X github.com/alchemillahq/sylve/internal/cmd.Commit=" + testCommit + " -X github.com/alchemillahq/sylve/internal/cmd.BuiltAt=" + testBuiltAt},
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
	helperDigest, err := checksum(filepath.Join(dir, "CSMWrap.iso"))
	if err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"amd64", "arm64"} {
		asset, err := os.ReadFile(filepath.Join(dir, "sylve-"+arch))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(asset)
		record := buildRecord{
			versionInfo: versionInfo{
				Version: "0.3.1", Commit: testCommit, BuiltAt: testBuiltAt,
				Go: "go1.26.8", FreeBSD: "15.1-RELEASE",
			},
			Arch: arch, SHA256: hex.EncodeToString(digest[:]),
		}
		if arch == "amd64" {
			record.CSMWrapSHA256 = helperDigest
		}
		if err := writeJSON(filepath.Join(dir, "build-"+arch+".json"), record); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
