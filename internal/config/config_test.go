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

	"github.com/alchemillahq/sylve/internal"
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

func TestReadConfigDefaultsPAMDisabled(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := ReadConfig(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if cfg.Auth.EnablePAM {
		t.Fatal("expected PAM authentication to be disabled by default")
	}
}

func TestIsPAMEnabledRequiresExplicitOptIn(t *testing.T) {
	previous := ParsedConfig
	t.Cleanup(func() {
		ParsedConfig = previous
	})

	ParsedConfig = nil
	if IsPAMEnabled() {
		t.Fatal("expected PAM authentication to be disabled without a parsed config")
	}

	ParsedConfig = &internal.SylveConfig{
		Auth: internal.AuthConfig{EnablePAM: true},
	}
	if !IsPAMEnabled() {
		t.Fatal("expected explicit PAM enablement to be honored")
	}
}

func TestGetUploadsConfigUsesDefaults(t *testing.T) {
	previous := ParsedConfig
	ParsedConfig = nil
	t.Cleanup(func() {
		ParsedConfig = previous
	})

	got := GetUploadsConfig()
	if got.MaxFileBytes != DefaultUploadMaxFileBytes {
		t.Fatalf("max file bytes = %d, want %d", got.MaxFileBytes, DefaultUploadMaxFileBytes)
	}
	if got.MaxConcurrentTransfers != DefaultUploadMaxConcurrentTransfers {
		t.Fatalf(
			"max concurrent transfers = %d, want %d",
			got.MaxConcurrentTransfers,
			DefaultUploadMaxConcurrentTransfers,
		)
	}
}

func TestGetUploadsConfigHonorsPositiveOverrides(t *testing.T) {
	previous := ParsedConfig
	ParsedConfig = &internal.SylveConfig{
		Uploads: internal.UploadsConfig{
			MaxFileBytes:           1024,
			MaxConcurrentTransfers: 3,
		},
	}
	t.Cleanup(func() {
		ParsedConfig = previous
	})

	got := GetUploadsConfig()
	if got.MaxFileBytes != 1024 || got.MaxConcurrentTransfers != 3 {
		t.Fatalf("unexpected upload config: %+v", got)
	}
}

func TestSetupDataPathCreatesDownloaderUploadStagingDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", root)

	if err := SetupDataPath(); err != nil {
		t.Fatalf("setup data path: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "downloads", "uploads"))
	if err != nil {
		t.Fatalf("stat downloader upload staging directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("downloader upload staging path is not a directory")
	}
}
