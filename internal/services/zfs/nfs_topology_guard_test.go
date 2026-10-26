// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestFilesystemTopologyChangesConsultNFSBeforeZFSSet(t *testing.T) {
	for _, property := range []string{"mountpoint", "canmount", "readonly", "sharenfs", "compression"} {
		t.Run(property, func(t *testing.T) {
			runner := &editFilesystemRunner{datasetName: "tank/data", guid: "123"}
			guarded := false
			service := &Service{
				DB:   testutil.NewSQLiteTestDB(t, &models.ZFSCacheInvalidation{}, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{}),
				GZFS: gzfs.NewClient(gzfs.Options{Runner: runner}), syncMutex: &sync.Mutex{},
				StorageTopologyGuard: func(context.Context) (func(), error) {
					guarded = true
					return nil, errors.New("disable NFS first")
				},
			}
			err := service.EditFilesystem(t.Context(), "123", map[string]string{property: "value"})
			if property == "compression" {
				if err != nil || guarded || runner.setCalls != 1 {
					t.Fatalf("unrelated property was blocked: guarded=%t calls=%d err=%v", guarded, runner.setCalls, err)
				}
			} else if !errors.Is(err, ErrConflict) || !guarded || runner.setCalls != 0 {
				t.Fatalf("topology guard not enforced: guarded=%t calls=%d err=%v", guarded, runner.setCalls, err)
			}
		})
	}
}

func TestSnapshotRollbackConsultsNFSBeforeMutation(t *testing.T) {
	service, runner := newSnapshotIdentityService(t)
	service.StorageTopologyGuard = func(context.Context) (func(), error) { return nil, errors.New("disable NFS first") }
	if err := service.RollbackSnapshot(t.Context(), "1", "tank/source@selected", true); !errors.Is(err, ErrConflict) {
		t.Fatalf("rollback crossed the NFS fence: %v", err)
	}
	for _, call := range runner.CallHistory {
		if strings.HasPrefix(call.Cmd, "zfs rollback ") {
			t.Fatal("ZFS rollback was issued before NFS coordination")
		}
	}
}
