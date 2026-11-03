// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.
//go:build freebsd

package sysctl

import (
	"strings"
	"testing"
)

func TestGetString(t *testing.T) {
	val, err := GetString("kern.ostype")
	if err != nil {
		t.Fatalf("GetString failed: %v", err)
	}

	if !strings.HasPrefix(val, "FreeBSD") {
		t.Errorf("unexpected kern.ostype value: %q", val)
	}
}

func TestGetInt64(t *testing.T) {
	key := "vm.kmem_zmax"
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
