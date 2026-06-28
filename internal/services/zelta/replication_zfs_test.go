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
	"os/exec"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func zfsGetProperty(t *testing.T, dataset, prop string) string {
	t.Helper()
	out, err := exec.Command("zfs", "get", "-H", "-o", "value", prop, dataset).CombinedOutput()
	if err != nil {
		t.Fatalf("zfs get %s %s: %v\noutput: %s", prop, dataset, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func TestFenceReplicationGuestDatasets(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ctx := context.Background()

	// create datasets with VM naming pattern so findLocalGuestDatasets can match them
	vmDS := pool + "/virtual-machines/100"
	zfstest.EnsureDataset(t, client, vmDS)

	s := &Service{GZFS: client}

	policy := &clusterModels.ReplicationPolicy{
		ID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
	}

	if err := s.fenceReplicationGuestDatasets(ctx, policy, "test-fencing"); err != nil {
		t.Fatalf("fenceReplicationGuestDatasets: %v", err)
	}

	if got := zfsGetProperty(t, vmDS, "readonly"); got != "on" {
		t.Fatalf("expected readonly=on after fence, got %q", got)
	}
}

func TestFenceReplicationGuestDatasetsAlreadyFenced(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ctx := context.Background()

	vmDS := pool + "/virtual-machines/200"
	zfstest.EnsureDataset(t, client, vmDS)

	s := &Service{GZFS: client}

	// fence once
	policy := &clusterModels.ReplicationPolicy{
		ID: 2, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 200,
	}
	if err := s.fenceReplicationGuestDatasets(ctx, policy, "test"); err != nil {
		t.Fatalf("first fence: %v", err)
	}

	// fence again — should be idempotent, no error
	if err := s.fenceReplicationGuestDatasets(ctx, policy, "test"); err != nil {
		t.Fatalf("second fence on already-fenced dataset: %v", err)
	}
}

func TestFenceReplicationGuestDatasetsJail(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ctx := context.Background()

	jailDS := pool + "/jails/50"
	zfstest.EnsureDataset(t, client, jailDS)

	s := &Service{GZFS: client}

	policy := &clusterModels.ReplicationPolicy{
		ID: 3, GuestType: clusterModels.ReplicationGuestTypeJail, GuestID: 50,
	}

	if err := s.fenceReplicationGuestDatasets(ctx, policy, "jail-fence"); err != nil {
		t.Fatalf("fenceReplicationGuestDatasets jail: %v", err)
	}

	if got := zfsGetProperty(t, jailDS, "readonly"); got != "on" {
		t.Fatalf("expected readonly=on for jailed dataset, got %q", got)
	}
}

func TestFenceReplicationGuestDatasetsNoMatch(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	_, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ctx := context.Background()

	s := &Service{GZFS: client}

	policy := &clusterModels.ReplicationPolicy{
		ID: 4, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 9999,
	}

	// should not error when no local datasets match
	if err := s.fenceReplicationGuestDatasets(ctx, policy, "no-match"); err != nil {
		t.Fatalf("fence with no matching datasets: %v", err)
	}
}

func TestFenceReplicationGuestDatasetsNilPolicy(t *testing.T) {
	s := &Service{}
	if err := s.fenceReplicationGuestDatasets(context.Background(), nil, "test"); err != nil {
		t.Fatalf("nil policy should be no-op: %v", err)
	}
}

func TestUnfenceReplicationGuestDatasetsIfNeeded(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ctx := context.Background()

	vmDS := pool + "/virtual-machines/300"
	zfstest.EnsureDataset(t, client, vmDS)

	s := &Service{GZFS: client}

	policy := &clusterModels.ReplicationPolicy{
		ID: 5, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 300,
	}

	// fence first
	if err := s.fenceReplicationGuestDatasets(ctx, policy, "fence"); err != nil {
		t.Fatalf("fence: %v", err)
	}

	// unfence
	if err := s.unfenceReplicationGuestDatasetsIfNeeded(ctx, policy); err != nil {
		t.Fatalf("unfenceReplicationGuestDatasetsIfNeeded: %v", err)
	}

	if got := zfsGetProperty(t, vmDS, "readonly"); got != "off" {
		t.Fatalf("expected readonly=off after unfence, got %q", got)
	}
}

func TestFindLocalGuestDatasets(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ctx := context.Background()

	zfstest.EnsureDataset(t, client, pool+"/virtual-machines/100")
	zfstest.EnsureDataset(t, client, pool+"/virtual-machines/100/disk0")
	zfstest.EnsureDataset(t, client, pool+"/jails/50")

	s := &Service{GZFS: client}

	datasets, err := s.findLocalGuestDatasets(ctx, clusterModels.ReplicationGuestTypeVM, 100)
	if err != nil {
		t.Fatalf("findLocalGuestDatasets VM: %v", err)
	}
	if len(datasets) == 0 {
		t.Fatal("expected at least 1 dataset for VM 100")
	}

	datasets, err = s.findLocalGuestDatasets(ctx, clusterModels.ReplicationGuestTypeJail, 50)
	if err != nil {
		t.Fatalf("findLocalGuestDatasets jail: %v", err)
	}
	if len(datasets) == 0 {
		t.Fatal("expected at least 1 dataset for jail 50")
	}

	datasets, err = s.findLocalGuestDatasets(ctx, clusterModels.ReplicationGuestTypeJail, 9999)
	if err != nil {
		t.Fatalf("findLocalGuestDatasets no-match: %v", err)
	}
	if len(datasets) != 0 {
		t.Fatalf("expected 0 datasets for non-existent jail, got %d", len(datasets))
	}
}

func TestGetLocalDatasetGZFSNotInitialized(t *testing.T) {
	s := &Service{GZFS: nil}
	_, err := s.getLocalDataset(context.Background(), "pool/ds")
	if err == nil {
		t.Fatal("expected error when GZFS is nil")
	}
}

func TestLocalDatasetExistsGZFSNotInitialized(t *testing.T) {
	s := &Service{GZFS: nil}
	_, err := s.localDatasetExists(context.Background(), "pool/ds")
	if err == nil {
		t.Fatal("expected error when GZFS is nil")
	}
}

func TestGZFSDatasetNotFoundErrors(t *testing.T) {
	if isGZFSDatasetNotFoundError(nil) {
		t.Fatal("nil should not be not-found error")
	}
	if !isGZFSDatasetNotFoundError(errFromStr("dataset does not exist")) {
		t.Fatal("expected match on 'dataset does not exist'")
	}
	if !isGZFSDatasetNotFoundError(errFromStr("cannot open 'tank/foo': dataset does not exist")) {
		t.Fatal("expected match on 'cannot open'")
	}
	if isGZFSDatasetNotFoundError(errFromStr("connection refused")) {
		t.Fatal("unrelated error should not match")
	}
}

func TestGZFSPoolNotFoundErrors(t *testing.T) {
	if isGZFSPoolNotFoundError(nil) {
		t.Fatal("nil should not be pool-not-found error")
	}
	if !isGZFSPoolNotFoundError(errFromStr("no such pool 'nonexistent'")) {
		t.Fatal("expected match on 'no such pool'")
	}
	if isGZFSPoolNotFoundError(errFromStr("connection refused")) {
		t.Fatal("unrelated error should not match")
	}
}

func TestIsReplicationGuestIntentionallyStoppedVM(t *testing.T) {
	db := newZeltaServiceTestDB(t, &vmModels.VM{})
	s := &Service{DB: db}

	db.Create(&vmModels.VM{RID: 100, IntentionallyStopped: true})
	stopped, err := s.isReplicationGuestIntentionallyStopped(clusterModels.ReplicationGuestTypeVM, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stopped {
		t.Fatal("expected intentionally stopped")
	}
}

func TestIsReplicationGuestIntentionallyStoppedJail(t *testing.T) {
	db := newZeltaServiceTestDB(t, &jailModels.Jail{})
	s := &Service{DB: db}

	db.Create(&jailModels.Jail{CTID: 50, IntentionallyStopped: true})
	stopped, err := s.isReplicationGuestIntentionallyStopped(clusterModels.ReplicationGuestTypeJail, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stopped {
		t.Fatal("expected intentionally stopped jail")
	}
}

func errFromStr(s string) error {
	return errStr(s)
}

type errStr string

func (e errStr) Error() string { return string(e) }
