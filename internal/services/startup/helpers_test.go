// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package startup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteJailLogRotationConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "newsyslog.conf.d", "sylve.conf")
	jailsPath := "/var/db/sylve/jails"

	if err := writeJailLogRotationConfig(configPath, jailsPath); err != nil {
		t.Fatalf("writeJailLogRotationConfig: %v", err)
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}

	want := "# Managed by Sylve; changes will be overwritten.\n" +
		"/var/db/sylve/jails/*/*.log\t0644\t5\t1M\t*\tBEGNZ\n"
	if string(content) != want {
		t.Fatalf("generated config = %q, want %q", content, want)
	}

	if err := writeJailLogRotationConfig(configPath, jailsPath); err != nil {
		t.Fatalf("idempotent writeJailLogRotationConfig: %v", err)
	}
}

func TestWriteJailLogRotationConfigRejectsUnsafePath(t *testing.T) {
	err := writeJailLogRotationConfig(
		filepath.Join(t.TempDir(), "sylve.conf"),
		"/var/db/sylve jails",
	)
	if err == nil {
		t.Fatal("expected an unsafe jails path to be rejected")
	}
}

func TestSysctlSyncRaisesNetFIBsWhenBelowMinimum(t *testing.T) {
	previousGet := startupGetSysctlInt64
	previousSet := startupSetSysctlInt32
	t.Cleanup(func() {
		startupGetSysctlInt64 = previousGet
		startupSetSysctlInt32 = previousSet
	})

	getValues := map[string]int64{
		"net.fibs": 1,
	}
	setValues := map[string]int32{}

	startupGetSysctlInt64 = func(name string) (int64, error) {
		if value, ok := getValues[name]; ok {
			return value, nil
		}
		return 0, nil
	}

	startupSetSysctlInt32 = func(name string, value int32) error {
		setValues[name] = value
		return nil
	}

	svc := &Service{}
	if err := svc.SysctlSync(); err != nil {
		t.Fatalf("expected sysctl sync to succeed, got: %v", err)
	}

	if value, ok := setValues["net.fibs"]; !ok {
		t.Fatal("expected net.fibs to be set when current value is below minimum")
	} else if value != 8 {
		t.Fatalf("expected net.fibs to be set to 8, got %d", value)
	}
}

func TestSysctlSyncKeepsNetFIBsWhenAlreadyHighEnough(t *testing.T) {
	previousGet := startupGetSysctlInt64
	previousSet := startupSetSysctlInt32
	t.Cleanup(func() {
		startupGetSysctlInt64 = previousGet
		startupSetSysctlInt32 = previousSet
	})

	getValues := map[string]int64{
		"net.fibs": 12,
	}
	setValues := map[string]int32{}

	startupGetSysctlInt64 = func(name string) (int64, error) {
		if value, ok := getValues[name]; ok {
			return value, nil
		}
		return 0, nil
	}

	startupSetSysctlInt32 = func(name string, value int32) error {
		setValues[name] = value
		return nil
	}

	svc := &Service{}
	if err := svc.SysctlSync(); err != nil {
		t.Fatalf("expected sysctl sync to succeed, got: %v", err)
	}

	if _, ok := setValues["net.fibs"]; ok {
		t.Fatal("expected net.fibs not to be changed when current value is already >= 8")
	}
}
