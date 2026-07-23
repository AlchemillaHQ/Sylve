// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDataPathFromConfigUsesRelativeConfigPathWithoutCreatingIt(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", "")
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"dataPath":"state"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got, err := DataPathFromConfig(configPath)
	if err != nil {
		t.Fatalf("resolve data path: %v", err)
	}
	want := filepath.Join(configDir, "state")
	if got != want {
		t.Fatalf("data path = %q, want %q", got, want)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Fatalf("expected data path not to be created, got err=%v", err)
	}
}

func TestDataPathFromConfigHonorsEnvironmentOverride(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"dataPath":"ignored"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	override := filepath.Join(t.TempDir(), "override")
	t.Setenv("SYLVE_DATA_PATH", override)
	got, err := DataPathFromConfig(configPath)
	if err != nil {
		t.Fatalf("resolve data path: %v", err)
	}
	if got != override {
		t.Fatalf("data path = %q, want %q", got, override)
	}
	if _, err := os.Stat(override); !os.IsNotExist(err) {
		t.Fatalf("expected override path not to be created, got err=%v", err)
	}
}

func TestReadConfigReturnsDecodeErrors(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := ReadConfig(configPath); err == nil {
		t.Fatal("expected config decode error")
	} else if !strings.HasPrefix(err.Error(), "decode config") {
		t.Fatalf("unexpected error: %v", err)
	}
}
