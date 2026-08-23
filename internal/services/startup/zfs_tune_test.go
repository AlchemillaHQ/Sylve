// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package startup

import (
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
)

const giB = int64(1024) * 1024 * 1024

func TestComputeARCMax(t *testing.T) {
	cases := []struct {
		name string
		mem  int64
		want int64
	}{
		{"64GiB is 10 percent", 64 * giB, 64 * giB / 10},
		{"160GiB hits the 16GiB cap exactly", 160 * giB, 16 * giB},
		{"256GiB is capped at 16GiB", 256 * giB, 16 * giB},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeARCMax(tc.mem); got != tc.want {
				t.Fatalf("computeARCMax(%d) = %d, want %d", tc.mem, got, tc.want)
			}
		})
	}
}

func swapZFSTuneMocks(t *testing.T, tune bool, mem int64) *bool {
	t.Helper()

	prevConfig := config.ParsedConfig
	prevSet := startupSetSysctlInt64
	prevMem := startupGetSystemMemoryBytes

	t.Cleanup(func() {
		config.ParsedConfig = prevConfig
		startupSetSysctlInt64 = prevSet
		startupGetSystemMemoryBytes = prevMem
	})

	config.ParsedConfig = &internal.SylveConfig{
		ZFS: internal.ZFSConfig{Tune: tune},
	}

	called := false
	startupSetSysctlInt64 = func(name string, value int64) error {
		if name == arcMaxOID {
			called = true
		}
		return nil
	}
	startupGetSystemMemoryBytes = func() (int64, error) {
		return mem, nil
	}

	return &called
}

func TestZFSTuneDisabledIsNoOp(t *testing.T) {
	called := swapZFSTuneMocks(t, false, 64*giB)

	svc := &Service{}
	if err := svc.ZFSTune(); err != nil {
		t.Fatalf("expected ZFSTune to succeed, got: %v", err)
	}

	if *called {
		t.Fatal("expected arc.max not to be set when zfs.tune is disabled")
	}
}

func TestZFSTuneAppliesARCMax(t *testing.T) {
	called := swapZFSTuneMocks(t, true, 64*giB)

	var setValue int64
	startupSetSysctlInt64 = func(name string, value int64) error {
		if name == arcMaxOID {
			*called = true
			setValue = value
		}
		return nil
	}

	svc := &Service{}
	if err := svc.ZFSTune(); err != nil {
		t.Fatalf("expected ZFSTune to succeed, got: %v", err)
	}

	if !*called {
		t.Fatal("expected arc.max to be set when zfs.tune is enabled and no override exists")
	}
	if want := computeARCMax(64 * giB); setValue != want {
		t.Fatalf("expected arc.max to be set to %d, got %d", want, setValue)
	}
}

func TestZFSTuneStoredOverrideSkipsAutoTune(t *testing.T) {
	called := swapZFSTuneMocks(t, true, 64*giB)

	db := testutil.NewSQLiteTestDB(t, &models.SystemTunable{})
	if err := db.Create(&models.SystemTunable{Name: arcMaxOID, Value: "12345"}).Error; err != nil {
		t.Fatalf("failed to seed stored tunable: %v", err)
	}

	svc := &Service{DB: db}
	if err := svc.ZFSTune(); err != nil {
		t.Fatalf("expected ZFSTune to succeed, got: %v", err)
	}

	if *called {
		t.Fatal("expected auto-tune to be skipped when a stored arc.max override exists")
	}
}
