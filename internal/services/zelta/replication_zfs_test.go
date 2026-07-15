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
	"strconv"
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

func setReplicationTestTreeReadonly(t *testing.T, root, value string) {
	t.Helper()
	output, err := exec.Command(
		"zfs", "list", "-H", "-o", "name", "-r", "-t", "filesystem,volume", root,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("list replication test tree %s: %v\n%s", root, err, output)
	}
	for _, dataset := range strings.Fields(string(output)) {
		setOutput, setErr := exec.Command("zfs", "set", "readonly="+value, dataset).CombinedOutput()
		if setErr != nil {
			t.Fatalf("set readonly=%s on %s: %v\n%s", value, dataset, setErr, setOutput)
		}
	}
}

func TestValidateReplicationTransitionGenerationForActivation(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	tests := []struct {
		name      string
		guestType string
		guestID   uint
		childName string
		volume    bool
	}{
		{name: "vm with zvol", guestType: clusterModels.ReplicationGuestTypeVM, guestID: 910, childName: "zvol-1", volume: true},
		{name: "jail with child filesystem", guestType: clusterModels.ReplicationGuestTypeJail, guestID: 911, childName: "data"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, client, cleanup := zfstest.Pool(t)
			defer cleanup()
			root := pool + "/sylve/"
			if tt.guestType == clusterModels.ReplicationGuestTypeVM {
				root += "virtual-machines/"
			} else {
				root += "jails/"
			}
			root += strconv.FormatUint(uint64(tt.guestID), 10)
			child := root + "/" + tt.childName
			zfstest.EnsureDataset(t, client, root)
			if tt.volume {
				zfstest.EnsureVolume(t, client, child, 8)
			} else {
				zfstest.EnsureDataset(t, client, child)
			}

			service := &Service{GZFS: client}
			scopeLocalFilesystemDatasetsToPool(t, service, pool)
			generationID := "replication-validator-" + strconv.FormatUint(uint64(tt.guestID), 10)
			snapshotName, err := replicationGenerationSnapshotName(generationID)
			if err != nil {
				t.Fatalf("generation snapshot name: %v", err)
			}
			if output, err := exec.Command("zfs", "snapshot", "-r", root+"@"+snapshotName).CombinedOutput(); err != nil {
				t.Fatalf("create generation snapshot: %v\n%s", err, output)
			}
			snapshotGUID := zfsGetProperty(t, root+"@"+snapshotName, "guid")
			policyID := uint(700 + tt.guestID)
			ownerEpoch := uint64(4)
			sourceRoot := pool + "/source/" + strconv.FormatUint(uint64(tt.guestID), 10)
			setRealZFSProperties(t, root, replicationProvenanceProperties(
				ReplicationZFSTransferOptions{
					PolicyID: policyID, RunID: generationID, OwnerEpoch: ownerEpoch,
					SnapshotName: snapshotName, SnapshotGUID: snapshotGUID,
				},
				sourceRoot,
				root,
				replicationStateReady,
			))
			manifest, err := service.replicationSnapshotTreeManifestLocal(
				context.Background(), root, sourceRoot, snapshotName,
			)
			if err != nil {
				t.Fatalf("build generation manifest: %v", err)
			}
			policy := &clusterModels.ReplicationPolicy{
				ID: policyID, GuestType: tt.guestType, GuestID: tt.guestID,
				TransitionGenerationID: generationID, TransitionGenerationOwnerEpoch: ownerEpoch,
				TransitionGenerationRootCount: 1,
				TransitionGenerationManifest:  replicationSnapshotManifestHash(policyID, ownerEpoch, generationID, manifest),
			}
			setReplicationTestTreeReadonly(t, root, "on")

			if err := service.validateReplicationTransitionGenerationForActivation(context.Background(), policy, "on"); err != nil {
				t.Fatalf("valid %s generation rejected: %v", tt.guestType, err)
			}
			if output, err := exec.Command("zfs", "set", "readonly=off", child).CombinedOutput(); err != nil {
				t.Fatalf("make child writable: %v\n%s", err, output)
			}
			if err := service.validateReplicationTransitionGenerationForActivation(context.Background(), policy, "on"); err == nil || !strings.Contains(err.Error(), "replication_transition_generation_readonly_mismatch_") {
				t.Fatalf("writable %s child was not rejected: %v", tt.guestType, err)
			}
		})
	}
}

func TestValidateAlreadyRunningReplicationActivationIgnoresSnapshots(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	tests := []struct {
		name      string
		guestType string
		guestID   uint
		childName string
		volume    bool
	}{
		{name: "vm with zvol", guestType: clusterModels.ReplicationGuestTypeVM, guestID: 920, childName: "zvol-1", volume: true},
		{name: "jail with child filesystem", guestType: clusterModels.ReplicationGuestTypeJail, guestID: 921, childName: "data"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, client, cleanup := zfstest.Pool(t)
			defer cleanup()
			root := pool + "/sylve/"
			if tt.guestType == clusterModels.ReplicationGuestTypeVM {
				root += "virtual-machines/"
			} else {
				root += "jails/"
			}
			root += strconv.FormatUint(uint64(tt.guestID), 10)
			child := root + "/" + tt.childName
			zfstest.EnsureDataset(t, client, root)
			if tt.volume {
				zfstest.EnsureVolume(t, client, child, 8)
			} else {
				zfstest.EnsureDataset(t, client, child)
			}
			if output, err := exec.Command("zfs", "snapshot", "-r", root+"@ha_existing").CombinedOutput(); err != nil {
				t.Fatalf("create recursive snapshot: %v\n%s", err, output)
			}

			database := newZeltaServiceTestDB(t, &vmModels.VM{}, &jailModels.Jail{})
			if tt.guestType == clusterModels.ReplicationGuestTypeVM {
				if err := database.Create(&vmModels.VM{RID: tt.guestID, Name: tt.name}).Error; err != nil {
					t.Fatalf("register VM: %v", err)
				}
			} else {
				if err := database.Create(&jailModels.Jail{CTID: tt.guestID, Name: tt.name}).Error; err != nil {
					t.Fatalf("register jail: %v", err)
				}
			}
			service := newTestZeltaService(database)
			service.GZFS = client
			scopeLocalFilesystemDatasetsToPool(t, service, pool)
			policyID := uint(800 + tt.guestID)
			setRealZFSProperties(t, root, map[string]string{
				replicationPropertyPolicyID: strconv.FormatUint(uint64(policyID), 10),
				replicationPropertyRole:     replicationRoleStandby,
				replicationPropertyState:    replicationStateReady,
			})
			setReplicationTestTreeReadonly(t, root, "off")
			policy := &clusterModels.ReplicationPolicy{
				ID: policyID, GuestType: tt.guestType, GuestID: tt.guestID,
				TransitionState: clusterModels.ReplicationTransitionStateCompleted,
			}

			if err := service.validateAlreadyRunningReplicationActivation(context.Background(), policy); err != nil {
				t.Fatalf("valid running %s rejected because snapshots exist: %v", tt.guestType, err)
			}
			if output, err := exec.Command("zfs", "set", "readonly=on", child).CombinedOutput(); err != nil {
				t.Fatalf("make child readonly: %v\n%s", err, output)
			}
			if err := service.validateAlreadyRunningReplicationActivation(context.Background(), policy); err == nil || !strings.Contains(err.Error(), "replication_running_dataset_not_writable_") {
				t.Fatalf("readonly %s child was not rejected: %v", tt.guestType, err)
			}
		})
	}
}

func TestFenceReplicationGuestDatasets(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ctx := context.Background()

	// create datasets with VM naming pattern so findLocalGuestDatasets can match them
	vmDS := pool + "/sylve/virtual-machines/100"
	zfstest.EnsureDataset(t, client, vmDS)
	if output, err := exec.Command("zfs", "snapshot", vmDS+"@ha_existing").CombinedOutput(); err != nil {
		t.Fatalf("create existing replication snapshot: %v\n%s", err, output)
	}

	s := &Service{GZFS: client}
	scopeLocalFilesystemDatasetsToPool(t, s, pool)

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

	vmDS := pool + "/sylve/virtual-machines/200"
	zfstest.EnsureDataset(t, client, vmDS)

	s := &Service{GZFS: client}
	scopeLocalFilesystemDatasetsToPool(t, s, pool)

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

	jailDS := pool + "/sylve/jails/50"
	zfstest.EnsureDataset(t, client, jailDS)

	s := &Service{GZFS: client}
	scopeLocalFilesystemDatasetsToPool(t, s, pool)

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
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ctx := context.Background()

	s := &Service{GZFS: client}
	scopeLocalFilesystemDatasetsToPool(t, s, pool)

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

	vmDS := pool + "/sylve/virtual-machines/300"
	zfstest.EnsureDataset(t, client, vmDS)

	s := &Service{GZFS: client}
	scopeLocalFilesystemDatasetsToPool(t, s, pool)

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

	zfstest.EnsureDataset(t, client, pool+"/sylve/virtual-machines/100")
	zfstest.EnsureDataset(t, client, pool+"/sylve/virtual-machines/100/disk0")
	zfstest.EnsureDataset(t, client, pool+"/sylve/jails/50")
	// Similar-looking backup and numeric-alias paths must never be selected as
	// active guest roots.
	zfstest.EnsureDataset(t, client, pool+"/backups/virtual-machines/100")
	zfstest.EnsureDataset(t, client, pool+"/sylve/virtual-machines/0100")

	s := &Service{GZFS: client}
	scopeLocalFilesystemDatasetsToPool(t, s, pool)

	datasets, err := s.findLocalGuestDatasets(ctx, clusterModels.ReplicationGuestTypeVM, 100)
	if err != nil {
		t.Fatalf("findLocalGuestDatasets VM: %v", err)
	}
	if len(datasets) == 0 {
		t.Fatal("expected at least 1 dataset for VM 100")
	}
	if len(datasets) != 1 || datasets[0] != pool+"/sylve/virtual-machines/100" {
		t.Fatalf("VM discovery included a noncanonical dataset: %#v", datasets)
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
