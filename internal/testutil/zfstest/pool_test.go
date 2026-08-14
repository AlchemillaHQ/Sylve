// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfstest

import (
	"path/filepath"
	"testing"
)

func TestValidatePoolName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "/", "zroot", "tank", "sylve-test-a/b", "sylve-test-a@b", "sylve-test-a#b"} {
		if err := validatePoolName(name); err == nil {
			t.Fatalf("validatePoolName(%q) unexpectedly succeeded", name)
		}
	}
	if err := validatePoolName("sylve-test-1234-abcdef"); err != nil {
		t.Fatalf("valid pool name rejected: %v", err)
	}
}

func TestCleanupTargetsRejectsNonDirectResources(t *testing.T) {
	t.Parallel()

	const pool = "sylve-test-1234-abcdef"
	tests := []struct {
		resource string
		want     string
		wantErr  bool
	}{
		{resource: pool + "/source", want: pool + "/source"},
		{resource: pool + "@point", want: pool + "@point"},
		{resource: pool + "/source/child", wantErr: true},
		{resource: pool + "/source@point", wantErr: true},
		{resource: "zroot/source", wantErr: true},
		{resource: pool + "/", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.resource, func(t *testing.T) {
			t.Parallel()
			got, err := cleanupTargets(pool, []string{pool, test.resource})
			if test.wantErr {
				if err == nil {
					t.Fatalf("cleanupTargets(%q) unexpectedly succeeded with %q", test.resource, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("cleanupTargets(%q): %v", test.resource, err)
			}
			if len(got) != 1 || got[0] != test.want {
				t.Fatalf("cleanupTargets(%q) = %q, want %q", test.resource, got, test.want)
			}
		})
	}
}

func TestCleanupTargetsKeepsDirectOwnedResources(t *testing.T) {
	t.Parallel()

	const pool = "sylve-test-1234-abcdef"
	got, err := cleanupTargets(pool, []string{
		pool,
		pool + "@root",
		pool + "/source",
		pool + "/target",
	})
	if err != nil {
		t.Fatalf("cleanupTargets: %v", err)
	}
	want := []string{pool + "@root", pool + "/source", pool + "/target"}
	if len(got) != len(want) {
		t.Fatalf("cleanup targets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cleanup targets = %v, want %v", got, want)
		}
	}
}

func TestOwnedPoolValidateRejectsStateDirectoriesOutsideTmp(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(filepath.Clean(t.TempDir()), stateDirPrefix+"state")
	pool := &ownedPool{
		name:     "sylve-test-1234-abcdef",
		owner:    "owner",
		stateDir: stateDir,
	}
	if err := pool.validate(); err == nil {
		t.Fatal("state directory outside the direct system temp directory was accepted")
	}

	pool.stateDir = filepath.Join(fixtureStateRoot, stateDirPrefix+"state")
	if err := pool.validate(); err != nil {
		t.Fatalf("valid state directory rejected: %v", err)
	}
	pool.stateDir += "/."
	if err := pool.validate(); err == nil {
		t.Fatal("non-canonical state directory was accepted")
	}
}
