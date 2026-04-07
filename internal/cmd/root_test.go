// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAsciiArt(t *testing.T) {
	var buf bytes.Buffer
	AsciiArt(&buf)

	out := buf.String()

	if !strings.Contains(out, "____") {
		t.Fatalf("expected ascii art, got %q", out)
	}

	if !strings.Contains(out, "v"+Version) {
		t.Fatalf("expected version in output, got %q", out)
	}
}

func TestParseFlags_Defaults(t *testing.T) {
	got, err := ParseFlags(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.ConfigPath != "" {
		t.Fatalf("expected empty config path when no flag given, got %q", got.ConfigPath)
	}

	if got.REPL {
		t.Fatalf("expected REPL false by default")
	}

	if got.ShowHelp {
		t.Fatalf("expected help false by default")
	}

	if got.ShowVersion {
		t.Fatalf("expected version false by default")
	}
}

func TestParseFlags_Config(t *testing.T) {
	got, err := ParseFlags([]string{"-config", "/tmp/test.json"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.ConfigPath != "/tmp/test.json" {
		t.Fatalf("expected /tmp/test.json, got %q", got.ConfigPath)
	}
}

func TestParseFlags_Console(t *testing.T) {
	got, err := ParseFlags([]string{"-console"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !got.REPL {
		t.Fatalf("expected REPL true")
	}
}

func TestParseFlags_Help(t *testing.T) {
	got, err := ParseFlags([]string{"-help"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !got.ShowHelp {
		t.Fatalf("expected help true")
	}
}

func TestParseFlags_HelpShort(t *testing.T) {
	got, err := ParseFlags([]string{"-h"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !got.ShowHelp {
		t.Fatalf("expected help true")
	}
}

func TestParseFlags_Version(t *testing.T) {
	got, err := ParseFlags([]string{"-version"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !got.ShowVersion {
		t.Fatalf("expected version true")
	}
}

func TestParseFlags_VersionShort(t *testing.T) {
	got, err := ParseFlags([]string{"-v"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !got.ShowVersion {
		t.Fatalf("expected version true")
	}
}

func TestResolveConfigPath_Explicit(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(cfg, []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveConfigPath(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != cfg {
		t.Fatalf("expected %q, got %q", cfg, got)
	}
}

func TestResolveConfigPath_ExplicitMissing(t *testing.T) {
	_, err := ResolveConfigPath("/nonexistent/path/config.json")
	if err == nil {
		t.Fatal("expected error for missing explicit config, got nil")
	}
}

func TestResolveConfigPath_NoArgs_NoneFound(t *testing.T) {
	// Run from a temp dir so neither ./config.json nor the system path exist.
	tmp := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	_, err = ResolveConfigPath("")
	if err == nil {
		t.Fatal("expected error when no config found, got nil")
	}
}

func TestResolveConfigPath_NoArgs_LocalFound(t *testing.T) {
	tmp := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	if err := os.WriteFile("config.json", []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveConfigPath("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != DefaultConfigLocal {
		t.Fatalf("expected %q, got %q", DefaultConfigLocal, got)
	}
}

func TestParseFlags_InvalidFlag(t *testing.T) {
	_, err := ParseFlags([]string{"-wat"})
	if err == nil {
		t.Fatalf("expected error for invalid flag")
	}
}
