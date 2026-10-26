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
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/alchemillahq/gzfs"
	gzfstest "github.com/alchemillahq/gzfs/testutil"
	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func snapshotIdentityResponse(t *testing.T, datasets ...*gzfs.Dataset) string {
	t.Helper()
	entries := make(map[string]*gzfs.Dataset, len(datasets))
	for _, dataset := range datasets {
		entries[dataset.Name] = dataset
	}
	response, err := json.Marshal(gzfs.DatasetList{Datasets: entries})
	if err != nil {
		t.Fatal(err)
	}
	return string(response)
}

func newSnapshotIdentityService(t *testing.T) (*Service, *gzfstest.MockRunner) {
	t.Helper()
	database := testutil.NewSQLiteTestDB(t,
		&models.ZFSCacheInvalidation{}, &clusterModels.ReplicationPolicy{},
		&utilitiesModels.Downloads{}, &utilitiesModels.Upload{}, &vmModels.VMStorageDataset{},
	)
	runner := gzfstest.NewMockRunner()
	client := gzfs.NewClient(gzfs.Options{Runner: runner})
	snapshots := []*gzfs.Dataset{
		{Name: "tank/source@selected", Pool: "tank", Type: gzfs.DatasetTypeSnapshot, Properties: map[string]gzfs.ZFSProperty{"guid": {Value: "1"}}},
		{Name: "backup/replica@selected", Pool: "backup", Type: gzfs.DatasetTypeSnapshot, Properties: map[string]gzfs.ZFSProperty{"guid": {Value: "1"}}},
		{Name: "tank/unique@selected", Pool: "tank", Type: gzfs.DatasetTypeSnapshot, Properties: map[string]gzfs.ZFSProperty{"guid": {Value: "2"}}},
	}
	runner.AddCommand(" -r -t snap -j", snapshotIdentityResponse(t, snapshots...), "", nil)
	for _, snapshot := range snapshots {
		runner.AddCommand(" "+snapshot.Name+" -j", snapshotIdentityResponse(t, snapshot), "", nil)
	}
	runner.AddCommand("zfs destroy ", "", "", nil)
	runner.AddCommand("zfs rollback ", "", "", nil)
	return NewZfsService(database, nil, nil, client).(*Service), runner
}

func TestSnapshotMutationsSelectExactCopy(t *testing.T) {
	for _, mutation := range []struct {
		name string
		run  func(*Service, context.Context, string, string, bool) error
	}{
		{"delete", (*Service).DeleteSnapshot},
		{"rollback", (*Service).RollbackSnapshot},
	} {
		for _, target := range []struct{ name, guid, resolved string }{
			{"tank/source@selected", "1", "tank/source@selected"},
			{"backup/replica@selected", "1", "backup/replica@selected"},
			{"", "2", "tank/unique@selected"},
		} {
			t.Run(mutation.name+"/"+target.resolved, func(t *testing.T) {
				service, runner := newSnapshotIdentityService(t)
				if err := mutation.run(service, t.Context(), target.guid, target.name, true); err != nil {
					t.Fatal(err)
				}
				command := "zfs destroy -r " + target.resolved
				if mutation.name == "rollback" {
					command = "zfs rollback -r " + target.resolved
				}
				if runner.GetLastCall().Cmd != command {
					t.Fatalf("mutation targeted another copy: %#v", runner.CallHistory)
				}
				wantCalls := 2
				if target.name == "" {
					wantCalls++
				}
				if len(runner.CallHistory) != wantCalls {
					t.Fatalf("unexpected lookup commands: %#v", runner.CallHistory)
				}
			})
		}
	}
}

func TestSnapshotMutationsRejectAmbiguousStaleAndInvalidTargets(t *testing.T) {
	for _, mutation := range []struct {
		name string
		run  func(*Service, context.Context, string, string, bool) error
	}{
		{"delete", (*Service).DeleteSnapshot},
		{"rollback", (*Service).RollbackSnapshot},
	} {
		for _, target := range []struct {
			label, guid, name string
			response          string
			wantErr           error
		}{
			{label: "ambiguous legacy GUID", guid: "1", wantErr: ErrConflict},
			{label: "missing legacy GUID", guid: "3", wantErr: ErrDatasetNotFound},
			{label: "missing GUID", name: "tank/source@selected", wantErr: ErrInvalidRequest},
			{label: "stale GUID", guid: "3", name: "tank/source@selected", wantErr: ErrConflict},
			{label: "missing snapshot", guid: "1", name: "tank/missing@selected", response: `{"datasets":{}}`, wantErr: ErrDatasetNotFound},
			{label: "legacy snapshot disappeared", guid: "2", response: `{"datasets":{}}`, wantErr: ErrDatasetNotFound},
			{label: "legacy identity changed", guid: "2", response: `{"datasets":{"tank/unique@selected":{"name":"tank/unique@selected","pool":"tank","type":"SNAPSHOT","properties":{"guid":{"value":"3"}}}}}`, wantErr: ErrConflict},
			{label: "wrong type", guid: "1", name: "tank/source@selected", response: `{"datasets":{"tank/source@selected":{"name":"tank/source@selected","pool":"tank","type":"FILESYSTEM","properties":{"guid":{"value":"1"}}}}}`, wantErr: ErrDatasetNotFound},
			{label: "not a snapshot name", guid: "1", name: "tank/source", wantErr: ErrInvalidRequest},
			{label: "option injection", guid: "1", name: "-r@selected", wantErr: ErrInvalidRequest},
			{label: "multiple snapshots", guid: "1", name: "tank/source@selected@other", wantErr: ErrInvalidRequest},
		} {
			t.Run(mutation.name+"/"+target.label, func(t *testing.T) {
				service, runner := newSnapshotIdentityService(t)
				if target.response != "" {
					name := target.name
					if name == "" {
						name = "tank/unique@selected"
					}
					runner.AddCommand(" "+name+" -j", target.response, "", nil)
				}
				if err := mutation.run(service, t.Context(), target.guid, target.name, false); !errors.Is(err, target.wantErr) {
					t.Fatalf("got %v, want %v", err, target.wantErr)
				}
				for _, call := range runner.CallHistory {
					if strings.HasPrefix(call.Cmd, "zfs destroy ") || strings.HasPrefix(call.Cmd, "zfs rollback ") {
						t.Fatalf("rejected target was mutated: %#v", runner.CallHistory)
					}
				}
			})
		}
	}
}

func TestSnapshotMutationsRetainReplicationProtection(t *testing.T) {
	for _, mutation := range []struct {
		name string
		run  func(*Service, context.Context, string, string, bool) error
	}{
		{"delete", (*Service).DeleteSnapshot},
		{"rollback", (*Service).RollbackSnapshot},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			_, runner := newSnapshotIdentityService(t)
			client := gzfs.NewClient(gzfs.Options{Runner: runner})
			service := newReplicationGuardService(t, "tank", client)
			service.syncMutex = &sync.Mutex{}
			if err := service.DB.AutoMigrate(&utilitiesModels.Downloads{}, &utilitiesModels.Upload{}); err != nil {
				t.Fatal(err)
			}
			name := "tank/sylve/virtual-machines/701@selected"
			snapshot := &gzfs.Dataset{Name: name, Pool: "tank", Type: gzfs.DatasetTypeSnapshot, Properties: map[string]gzfs.ZFSProperty{"guid": {Value: "1"}}}
			runner.AddCommand(" "+name+" -j", snapshotIdentityResponse(t, snapshot), "", nil)
			if err := mutation.run(service, t.Context(), "1", name, false); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "replication_protected_dataset_mutation_blocked") {
				t.Fatalf("replication protection was bypassed: %v", err)
			}
			if len(runner.CallHistory) != 1 {
				t.Fatalf("protected snapshot was mutated: %#v", runner.CallHistory)
			}
		})
	}
}

func TestLegacySnapshotLookupRejectsIncompleteInventory(t *testing.T) {
	for _, response := range []string{
		`{"datasets":{"tank/source@selected":{"name":"tank/source@selected","type":"SNAPSHOT"}}}`,
		`{"datasets":{"tank/source@selected":{"type":"SNAPSHOT","properties":{"guid":{"value":"1"}}}}}`,
		`{"datasets":{"tank/source@selected":{"name":"tank/source@selected","type":"FILESYSTEM","properties":{"guid":{"value":"1"}}}}}`,
	} {
		service, runner := newSnapshotIdentityService(t)
		runner.AddCommand(" -r -t snap -j", response, "", nil)
		if dataset, err := service.snapshotForMutation(t.Context(), "1", ""); err == nil || dataset != nil {
			t.Fatalf("incomplete inventory resolved a snapshot: %#v, %v", dataset, err)
		}
		if len(runner.CallHistory) != 1 {
			t.Fatalf("incomplete inventory was used for a lookup: %#v", runner.CallHistory)
		}
	}
}

func TestBulkDeleteSnapshotsWithSharedGUID(t *testing.T) {
	service, runner := newSnapshotIdentityService(t)
	targets := []zfsServiceInterfaces.DatasetDeletionTarget{
		{Name: "tank/source@selected", GUID: "1"},
		{Name: "backup/replica@selected", GUID: "1"},
	}
	roots := []*gzfs.Dataset{
		{Name: targets[0].Name, GUID: "1", Type: gzfs.DatasetTypeSnapshot},
		{Name: targets[1].Name, GUID: "1", Type: gzfs.DatasetTypeSnapshot},
	}
	affected, err := service.datasetsAffectedByDeletion(t.Context(), roots)
	if err != nil || len(affected) != 2 {
		t.Fatalf("replicated snapshots were conflated: %#v, %v", affected, err)
	}
	if err := service.BulkDeleteDataset(t.Context(), targets); err != nil {
		t.Fatal(err)
	}
	var destroyed []string
	for _, call := range runner.CallHistory {
		if strings.HasPrefix(call.Cmd, "zfs destroy ") {
			destroyed = append(destroyed, call.Cmd)
		}
	}
	if len(destroyed) != 2 || destroyed[0] != "zfs destroy -r "+targets[0].Name || destroyed[1] != "zfs destroy -r "+targets[1].Name {
		t.Fatalf("bulk deletion did not target both exact copies: %v", destroyed)
	}
}
