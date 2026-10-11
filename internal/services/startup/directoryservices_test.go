// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package startup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alchemillahq/sylve/internal/services/directoryservices"
)

func TestPendingRecoveryPreventsSambaStartupAfterProcessRestart(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", root)
	dir := filepath.Join(root, "directory-services")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"", "unreadable-interrupted-journal"} {
		if err := os.WriteFile(filepath.Join(dir, "recovery-pending.enc"), []byte(marker), 0600); err != nil {
			t.Fatal(err)
		}
		// A fresh instance has no in-memory recovery state. Missing service
		// dependencies ensure any attempted configuration/start would fail.
		service := &Service{Directory: directoryservices.NewService(nil)}
		service.startSamba(context.Background(), context.Background(), true)
	}
	if _, err := os.Stat(filepath.Join(dir, "recovery-pending.enc")); err != nil {
		t.Fatal("startup discarded the protected recovery marker")
	}
}

func TestDisabledSambaStartupHasNoSideEffects(t *testing.T) {
	service := &Service{}
	service.startSamba(context.Background(), context.Background(), false)
}
