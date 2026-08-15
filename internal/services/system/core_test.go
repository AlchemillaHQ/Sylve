// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package system

import (
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestIsJailedReadsSysctl(t *testing.T) {
	orig := rebootSysctlGetInt64
	t.Cleanup(func() { rebootSysctlGetInt64 = orig })

	rebootSysctlGetInt64 = func(string) (int64, error) { return 1, nil }
	if !(&Service{}).IsJailed() {
		t.Fatal("expected IsJailed()=true when security.jail.jailed=1")
	}

	rebootSysctlGetInt64 = func(string) (int64, error) { return 0, nil }
	if (&Service{}).IsJailed() {
		t.Fatal("expected IsJailed()=false when security.jail.jailed=0")
	}
}

// Inside a jail, RebootSystem must not shell out to shutdown; it flips
// Restarted and re-execs the process in place.
func TestRebootSystemJailedSelfRestarts(t *testing.T) {
	origJail, origReexec := rebootSysctlGetInt64, rebootReexec
	t.Cleanup(func() { rebootSysctlGetInt64 = origJail; rebootReexec = origReexec })

	rebootSysctlGetInt64 = func(string) (int64, error) { return 1, nil } // jailed
	reexeced := make(chan struct{}, 1)
	rebootReexec = func() { reexeced <- struct{}{} }

	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	if err := db.Create(&models.BasicSettings{Initialized: true, Restarted: false}).Error; err != nil {
		t.Fatalf("seed BasicSettings: %v", err)
	}
	s := &Service{DB: db}

	if err := s.RebootSystem(); err != nil {
		t.Fatalf("RebootSystem returned error: %v", err)
	}

	var bs models.BasicSettings
	if err := db.First(&bs).Error; err != nil {
		t.Fatalf("load BasicSettings: %v", err)
	}
	if !bs.Restarted {
		t.Fatal("expected Restarted=true after jailed RebootSystem")
	}

	select {
	case <-reexeced:
		// re-exec was invoked as expected
	case <-time.After(2 * time.Second):
		t.Fatal("expected self-restart re-exec, none happened")
	}
}
