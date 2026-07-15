// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"testing"

	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestBackupDatasetWithinSamePoolRejectsEveryOverlapDirection(t *testing.T) {
	tests := []struct {
		name  string
		left  backupPoolIdentity
		right backupPoolIdentity
		want  bool
	}{
		{
			name:  "equal",
			left:  backupPoolIdentity{GUID: 1, Dataset: "zroot/data"},
			right: backupPoolIdentity{GUID: 1, Dataset: "alias/data"},
			want:  true,
		},
		{
			name:  "target below source",
			left:  backupPoolIdentity{GUID: 1, Dataset: "zroot/data"},
			right: backupPoolIdentity{GUID: 1, Dataset: "zroot/data/backups"},
			want:  true,
		},
		{
			name:  "source below target",
			left:  backupPoolIdentity{GUID: 1, Dataset: "zroot/data/child"},
			right: backupPoolIdentity{GUID: 1, Dataset: "zroot/data"},
			want:  true,
		},
		{
			name:  "siblings",
			left:  backupPoolIdentity{GUID: 1, Dataset: "zroot/data"},
			right: backupPoolIdentity{GUID: 1, Dataset: "zroot/backups"},
			want:  false,
		},
		{
			name:  "different pools",
			left:  backupPoolIdentity{GUID: 1, Dataset: "zroot/data"},
			right: backupPoolIdentity{GUID: 2, Dataset: "zroot/data"},
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := backupDatasetWithinSamePool(tt.left, tt.right); got != tt.want {
				t.Fatalf("got %t want %t", got, tt.want)
			}
		})
	}
}

func TestParseBackupPoolGUIDIgnoresMissingIdentityWarningOnly(t *testing.T) {
	t.Parallel()

	guid, err := parseBackupPoolGUID("Warning: Identity file /missing not accessible: No such file or directory.\n12345\n")
	if err != nil || guid != 12345 {
		t.Fatalf("guid=%d err=%v", guid, err)
	}
	if _, err := parseBackupPoolGUID("unexpected banner\n12345\n"); err == nil {
		t.Fatal("unexpected SSH output was accepted")
	}
	if _, err := parseBackupPoolGUID("12345\n67890\n"); err == nil {
		t.Fatal("multiple GUIDs were accepted")
	}
}

func TestLocalBackupPoolIdentityUsesRealZFSPoolGUID(t *testing.T) {
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	dataset := pool + "/source"
	zfstest.EnsureDataset(t, client, dataset)

	service := &Service{GZFS: client}
	identity, err := service.localBackupPoolIdentity(context.Background(), dataset)
	if err != nil {
		t.Fatalf("local pool identity: %v", err)
	}
	if identity.GUID == 0 {
		t.Fatal("expected nonzero pool GUID")
	}
	if identity.Dataset != dataset {
		t.Fatalf("dataset mismatch: got %s want %s", identity.Dataset, dataset)
	}
}
