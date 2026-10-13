// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package main

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"time"
)

var (
	commitPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	versionPattern = regexp.MustCompile(`(?m)^const Version = "([^"]+)"`)
	payloadNames   = []string{"sylve-amd64", "sylve-arm64", "sylve-web-assets.tar.gz", "sylve-web-demo-assets.tar.gz",
		"CSMWrap.iso"}
)

type versionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	BuiltAt string `json:"builtAt"`
	Go      string `json:"go"`
	FreeBSD string `json:"freebsd"`
}

type buildRecord struct {
	versionInfo
	Arch          string `json:"arch"`
	SHA256        string `json:"sha256"`
	CSMWrapSHA256 string `json:"csmwrapSHA256,omitempty"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "downloads" {
		return recordDownloads(args[1:])
	}
	if len(args) == 0 || (args[0] != "record" && args[0] != "generate") {
		return fmt.Errorf("usage: tipmetadata <record|generate|downloads> [flags]")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	commit := flags.String("commit", "", "full source commit SHA")
	binary := flags.String("binary", "", "FreeBSD binary to record")
	arch := flags.String("arch", "", "binary architecture")
	freebsd := flags.String("freebsd", "", "resolved FreeBSD target release")
	dir := flags.String("dir", ".", "directory containing release assets and build records")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if !commitPattern.MatchString(*commit) {
		return fmt.Errorf("-commit must be a full 40-character git SHA")
	}
	if args[0] == "generate" {
		return generate(*dir, *commit)
	}
	return record(*binary, *arch, *freebsd, *commit)
}

func record(binary, arch, freebsd, commit string) error {
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		return fmt.Errorf("read binary build information: %w", err)
	}
	source, err := os.ReadFile("internal/cmd/root.go")
	if err != nil {
		return err
	}
	match := versionPattern.FindSubmatch(source)
	if len(match) != 2 {
		return fmt.Errorf("cannot read Sylve version from internal/cmd/root.go")
	}
	metadata, err := metadataForBuild(info, string(match[1]), arch, freebsd, commit, time.Now())
	if err != nil {
		return err
	}
	digest, err := checksum(binary)
	if err != nil {
		return err
	}
	record := buildRecord{
		versionInfo: metadata, Arch: arch, SHA256: digest,
	}
	if arch == "amd64" {
		manifestData, err := os.ReadFile("internal/assets/csmwrap/CSMWrap-build-manifest.json")
		if err != nil {
			return err
		}
		var manifest struct {
			Architecture string            `json:"architecture"`
			SHA256       string            `json:"sha256"`
			Size         int64             `json:"size"`
			Files        map[string]string `json:"files"`
		}
		if err := json.Unmarshal(manifestData, &manifest); err != nil {
			return err
		}
		if manifest.Architecture != "x86_64" || manifest.Size <= 0 || manifest.Size > 64<<20 || len(manifest.SHA256) != 64 {
			return fmt.Errorf("invalid CSMWrap build manifest")
		}
		compressed, err := os.ReadFile("internal/assets/csmwrap/CSMWrap.iso.gz")
		if err != nil {
			return err
		}
		compressedHash := sha256.Sum256(compressed)
		if hex.EncodeToString(compressedHash[:]) != manifest.Files["CSMWrap.iso.gz"] {
			return fmt.Errorf("CSMWrap embedded asset checksum mismatch")
		}
		binaryData, err := os.ReadFile(binary)
		if err != nil {
			return err
		}
		if len(compressed) == 0 || !bytes.Contains(binaryData, compressed) || !bytes.Contains(binaryData, manifestData) {
			return fmt.Errorf("amd64 binary does not embed the staged CSMWrap bundle")
		}
		record.CSMWrapSHA256 = manifest.SHA256
	}
	return writeJSON(filepath.Join(filepath.Dir(binary), "build-"+arch+".json"), record)
}

func metadataForBuild(info *debug.BuildInfo, version, arch, freebsd, commit string, builtAt time.Time) (versionInfo, error) {
	if info.Path != "github.com/alchemillahq/sylve/cmd/sylve" {
		return versionInfo{}, fmt.Errorf("binary is not Sylve")
	}
	settings := make(map[string]string)
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if (arch != "amd64" && arch != "arm64") || settings["GOARCH"] != arch || settings["GOOS"] != "freebsd" {
		return versionInfo{}, fmt.Errorf("binary does not target FreeBSD/%s", arch)
	}
	if settings["vcs.revision"] != commit {
		return versionInfo{}, fmt.Errorf("binary source commit does not match %s", commit)
	}
	flags := strings.Fields(settings["-ldflags"])
	commitFlag := "github.com/alchemillahq/sylve/internal/cmd.Commit="
	var bakedCommit string
	for i, value := range flags {
		if strings.HasPrefix(value, commitFlag) && i > 0 && flags[i-1] == "-X" {
			bakedCommit = strings.TrimPrefix(value, commitFlag)
		} else if strings.HasPrefix(value, "-X="+commitFlag) {
			bakedCommit = strings.TrimPrefix(value, "-X="+commitFlag)
		}
	}
	if bakedCommit != commit {
		return versionInfo{}, fmt.Errorf("binary does not embed the full commit %s", commit)
	}
	if version == "" || info.GoVersion == "" || freebsd == "" {
		return versionInfo{}, fmt.Errorf("missing build version, Go version, or FreeBSD release")
	}
	return versionInfo{
		Version: version, Commit: commit, BuiltAt: builtAt.UTC().Format(time.RFC3339),
		Go: info.GoVersion, FreeBSD: freebsd,
	}, nil
}

func generate(dir, commit string) error {
	helperDigest, err := checksum(filepath.Join(dir, "CSMWrap.iso"))
	if err != nil {
		return err
	}
	var metadata versionInfo
	var latest time.Time
	for _, arch := range []string{"amd64", "arm64"} {
		data, err := os.ReadFile(filepath.Join(dir, "build-"+arch+".json"))
		if err != nil {
			return err
		}
		var record buildRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		if record.Arch != arch || record.Commit != commit || record.Version == "" || record.Go == "" || record.FreeBSD == "" {
			return fmt.Errorf("invalid or stale build record for %s", arch)
		}
		builtAt, err := time.Parse(time.RFC3339, record.BuiltAt)
		if err != nil {
			return fmt.Errorf("invalid build time for %s: %w", arch, err)
		}
		digest, err := checksum(filepath.Join(dir, "sylve-"+arch))
		if err != nil {
			return err
		}
		if digest != record.SHA256 {
			return fmt.Errorf("sylve-%s checksum does not match its build record", arch)
		}
		if arch == "amd64" {
			if record.CSMWrapSHA256 != helperDigest {
				return fmt.Errorf("amd64 embedded CSMWrap differs from release bundle")
			}
			metadata = record.versionInfo
		} else if metadata.Version != record.Version || metadata.Go != record.Go || metadata.FreeBSD != record.FreeBSD {
			return fmt.Errorf("architecture builds disagree on version, Go version, or FreeBSD release")
		}
		if builtAt.After(latest) {
			latest = builtAt
		}
	}
	metadata.BuiltAt = latest.UTC().Format(time.RFC3339)
	if err := writeJSON(filepath.Join(dir, "sylve-version.json"), metadata); err != nil {
		return err
	}

	for _, name := range payloadNames {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var sums strings.Builder
	for _, entry := range entries {
		name := entry.Name()
		if name == "SHA256SUMS" || name == "build-amd64.json" || name == "build-arm64.json" {
			continue
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("release asset %s is not a regular file", name)
		}
		digest, err := checksum(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		fmt.Fprintf(&sums, "%s  %s\n", digest, name)
	}
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0o644)
}

func checksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
