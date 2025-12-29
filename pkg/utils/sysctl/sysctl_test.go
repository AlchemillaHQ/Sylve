// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.
//go:build freebsd || darwin

package sysctl

import (
	"runtime"
	"strings"
	"testing"
)

func TestGetString(t *testing.T) {
	val, err := GetString("kern.ostype")
	if err != nil {
		t.Fatalf("GetString failed: %v", err)
	}

	switch runtime.GOOS {
	case "freebsd":
		if !strings.HasPrefix(val, "FreeBSD") {
			t.Errorf("unexpected kern.ostype value: %q", val)
		}
	case "darwin":
		if val != "Darwin" {
			t.Errorf("unexpected kern.ostype value: %q", val)
		}
	}
}

func TestGetInt64(t *testing.T) {
	var key string

	// vm.swap_idle_enabled does NOT exist on macOS
	switch runtime.GOOS {
	case "freebsd":
		key = "vm.kmem_zmax"
	case "darwin":
		key = "kern.maxfiles"
	}

	val, err := GetInt64(key)
	if err != nil {
		t.Fatalf("GetInt64(%s) failed: %v", key, err)
	}

	if val <= 0 {
		t.Errorf("unexpected value for %s: %d", key, val)
	}
}

func TestGetBytes(t *testing.T) {
	bytes, err := GetBytes("kern.hostname")
	if err != nil {
		t.Fatalf("GetBytes failed: %v", err)
	}
	if len(bytes) == 0 {
		t.Errorf("GetBytes returned empty data")
	}
}

func TestGetStringNullTermination(t *testing.T) {
	val, err := GetString("kern.hostname")
	if err != nil {
		t.Fatalf("GetString failed: %v", err)
	}
	if strings.ContainsRune(val, '\x00') {
		t.Errorf("value contains unexpected null byte: %q", val)
	}
}
