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
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
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

type sequencedRuntimeVMStub struct {
	stubVMService
	shutOffStates []bool
}

func (s *sequencedRuntimeVMStub) IsDomainShutOff(_ uint) (bool, error) {
	if len(s.shutOffStates) == 0 {
		return true, nil
	}
	state := s.shutOffStates[0]
	if len(s.shutOffStates) > 1 {
		s.shutOffStates = s.shutOffStates[1:]
	}
	return state, nil
}

type activationErrorGuestDriver struct {
	activateErr    error
	activateBefore func() error
	activateCalls  int
	demoteCalls    int
}

func (*activationErrorGuestDriver) sourceDatasets(context.Context, uint) ([]string, error) {
	return nil, nil
}

func (d *activationErrorGuestDriver) activate(context.Context, uint, string, bool) error {
	d.activateCalls++
	if d.activateBefore != nil {
		if err := d.activateBefore(); err != nil {
			return err
		}
	}
	return d.activateErr
}

func (d *activationErrorGuestDriver) demote(context.Context, uint, string) error {
	d.demoteCalls++
	return nil
}

func (*activationErrorGuestDriver) selfFence(context.Context, uint, uint, string, string, string) {}

func TestReplicationZFSTokensRequireExactValues(t *testing.T) {
	if !validReplicationZFSToken("replication-run_1") {
		t.Fatal("canonical replication token rejected")
	}
	for _, value := range []string{" replication-run_1", "replication-run_1 ", "-option"} {
		if validReplicationZFSToken(value) {
			t.Fatalf("non-exact replication token accepted: %q", value)
		}
	}
	if err := validateReplicationSnapshotName(" ha_replication-1-run"); err == nil {
		t.Fatal("non-exact replication snapshot name accepted")
	}
	if err := (ReplicationZFSTransferOptions{
		PolicyID: 1, OwnerEpoch: 1, RunID: "run-1",
		SnapshotName: "ha_replication-1-run", GenerationName: " ",
	}).validate(); err == nil {
		t.Fatal("non-exact optional generation token accepted")
	}
}

func TestIntegrationValidateReplicationTransitionGenerationForActivation(t *testing.T) {
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
			pool, client, cleanup := zfstest.SharedPool(t)
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
			scopeLocalDatasetsToPool(t, service, pool)
			policyID := uint(700 + tt.guestID)
			generationID := "replication-validator-" + strconv.FormatUint(uint64(tt.guestID), 10)
			snapshotName, err := replicationGenerationSnapshotName(policyID, generationID)
			if err != nil {
				t.Fatalf("generation snapshot name: %v", err)
			}
			if output, err := exec.Command("zfs", "snapshot", "-r", root+"@"+snapshotName).CombinedOutput(); err != nil {
				t.Fatalf("create generation snapshot: %v\n%s", err, output)
			}
			snapshotGUID := zfsGetProperty(t, root+"@"+snapshotName, "guid")
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

func TestIntegrationActivationErrorReconcilesProvenRunningGeneration(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	ctx := context.Background()

	const (
		policyID   = uint(44)
		guestID    = uint(413)
		ownerEpoch = uint64(4)
	)
	root := pool + "/sylve/virtual-machines/413"
	zfstest.EnsureDataset(t, client, root)
	generationID := "replication-44-ambiguous-activation"
	snapshotName, err := replicationGenerationSnapshotName(policyID, generationID)
	if err != nil {
		t.Fatalf("activation generation snapshot name: %v", err)
	}
	if output, err := exec.Command("zfs", "snapshot", "-r", root+"@"+snapshotName).CombinedOutput(); err != nil {
		t.Fatalf("snapshot activation generation: %v\n%s", err, output)
	}
	snapshotGUID := zfsGetProperty(t, root+"@"+snapshotName, "guid")
	sourceRoot := pool + "/source/413"
	setRealZFSProperties(t, root, replicationProvenanceProperties(
		ReplicationZFSTransferOptions{
			PolicyID: policyID, RunID: generationID, OwnerEpoch: ownerEpoch,
			SnapshotName: snapshotName, SnapshotGUID: snapshotGUID,
		},
		sourceRoot,
		root,
		replicationStateReady,
	))
	setReplicationTestTreeReadonly(t, root, "on")

	db := newZeltaServiceTestDB(
		t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&vmModels.VM{},
	)
	if err := db.Create(&vmModels.VM{RID: guestID, Name: "ambiguous-activation", IntentionallyStopped: true}).Error; err != nil {
		t.Fatalf("register activation VM: %v", err)
	}

	service := newTestZeltaService(db)
	service.Cluster = &clusterService.Service{DB: db, NodeID: "node-target"}
	service.GZFS = client
	service.VM = &sequencedRuntimeVMStub{shutOffStates: []bool{true, false}}
	scopeLocalDatasetsToPool(t, service, pool)
	manifest, err := service.replicationSnapshotTreeManifestLocal(ctx, root, sourceRoot, snapshotName)
	if err != nil {
		t.Fatalf("build activation generation manifest: %v", err)
	}
	desiredRunning := true
	policy := clusterModels.ReplicationPolicy{
		ID: policyID, Name: "ambiguous-activation", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: guestID, SourceNodeID: "node-target", ActiveNodeID: "node-target", OwnerEpoch: ownerEpoch,
		CronExpr: "*/5 * * * *", Enabled: true,
		TransitionState: clusterModels.ReplicationTransitionStatePromoting,
		TransitionRunID: generationID, TransitionSourceNodeID: "node-old", TransitionTargetNodeID: "node-target",
		TransitionOwnerEpoch: ownerEpoch, TransitionOriginalRunning: &desiredRunning,
		TransitionGenerationID: generationID, TransitionGenerationOwnerEpoch: ownerEpoch,
		TransitionGenerationRootCount: 1,
		TransitionGenerationManifest:  replicationSnapshotManifestHash(policyID, ownerEpoch, generationID, manifest),
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("create activation policy: %v", err)
	}
	lease := clusterModels.ReplicationLease{
		PolicyID: policyID, GuestType: policy.GuestType, GuestID: guestID,
		OwnerNodeID: "node-target", OwnerEpoch: ownerEpoch,
		ExpiresAt: time.Now().UTC().Add(time.Hour), Version: 2,
	}
	if err := db.Create(&lease).Error; err != nil {
		t.Fatalf("create activation lease: %v", err)
	}
	service.observeReplicationLeaseAuthority(policyID, "node-target", ownerEpoch, &clusterModels.ReplicationLease{
		OwnerNodeID: "node-target", OwnerEpoch: ownerEpoch, Version: 1,
	})
	driver := &activationErrorGuestDriver{
		activateErr: errors.New("activation response lost"),
		activateBefore: func() error {
			return service.prepareReplicatedDatasetForActivation(ctx, root)
		},
	}
	service.replicationGuestDriverFactory = func(string) (replicationGuestDriver, error) {
		return driver, nil
	}

	if err := service.ActivateReplicationPolicyForTransition(
		ctx,
		policyID,
		ownerEpoch,
		generationID,
		&desiredRunning,
	); err != nil {
		t.Fatalf(
			"reconcile ambiguous activation: %v (activate=%d demote=%d remaining_runtime_states=%v)",
			err,
			driver.activateCalls,
			driver.demoteCalls,
			service.VM.(*sequencedRuntimeVMStub).shutOffStates,
		)
	}
	if driver.activateCalls != 1 || driver.demoteCalls != 0 {
		t.Fatalf("activation driver calls = activate:%d demote:%d, want 1/0", driver.activateCalls, driver.demoteCalls)
	}
	var storedVM vmModels.VM
	if err := db.Where("rid = ?", guestID).First(&storedVM).Error; err != nil {
		t.Fatalf("reload activation VM: %v", err)
	}
	if storedVM.IntentionallyStopped {
		t.Fatal("proven running activation did not persist running intent")
	}
	if got := zfsGetProperty(t, root, "readonly"); got != "off" {
		t.Fatalf("proven running activation was fenced readonly=%s", got)
	}
}

func TestIntegrationValidateAlreadyRunningReplicationActivationIgnoresSnapshots(t *testing.T) {
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
			pool, client, cleanup := zfstest.SharedPool(t)
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
			scopeLocalDatasetsToPool(t, service, pool)
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

func TestIntegrationFenceReplicationGuestDatasets(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	ctx := context.Background()

	// create datasets with VM naming pattern so findLocalGuestDatasets can match them
	vmDS := pool + "/sylve/virtual-machines/100"
	zfstest.EnsureDataset(t, client, vmDS)
	if output, err := exec.Command("zfs", "snapshot", vmDS+"@ha_existing").CombinedOutput(); err != nil {
		t.Fatalf("create existing replication snapshot: %v\n%s", err, output)
	}

	s := &Service{GZFS: client}
	scopeLocalDatasetsToPool(t, s, pool)

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

func TestIntegrationFenceReplicationGuestDatasetsAlreadyFenced(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	ctx := context.Background()

	vmDS := pool + "/sylve/virtual-machines/200"
	zfstest.EnsureDataset(t, client, vmDS)

	s := &Service{GZFS: client}
	scopeLocalDatasetsToPool(t, s, pool)

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

func TestIntegrationSealReplicationDatasetRootsAsStandbyEnablesReversePromotion(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	ctx := context.Background()

	firstRoot := pool + "/sylve/virtual-machines/410"
	secondRoot := pool + "/secondary/sylve/virtual-machines/410"
	zfstest.EnsureDataset(t, client, firstRoot+"/old-child")
	zfstest.EnsureVolume(t, client, firstRoot+"/zvol-43", 8)
	zfstest.EnsureDataset(t, client, secondRoot+"/data")

	service := &Service{GZFS: client}
	policyID := uint(41)
	roots := []string{firstRoot, secondRoot}
	for attempt := 0; attempt < 2; attempt++ {
		if err := service.sealReplicationDatasetRootsAsStandby(ctx, policyID, roots); err != nil {
			t.Fatalf("seal transition source attempt %d: %v", attempt+1, err)
		}
	}
	for _, root := range roots {
		expected := replicationCurrentStandbySeedProperties(policyID, root, root)
		for property, value := range expected {
			if got := zfsGetPropertyWithSource(t, root, property); got != value+" local" {
				t.Fatalf("sealed property %s on %s = %q, want %q", property, root, got, value+" local")
			}
		}
		readonlyOutput, err := exec.Command(
			"zfs", "get", "-H", "-o", "value", "-r", "-t", "filesystem,volume", "readonly", root,
		).CombinedOutput()
		if err != nil {
			t.Fatalf("read sealed tree %s: %v\n%s", root, err, readonlyOutput)
		}
		if err := verifyReplicationReadonlyValues(string(readonlyOutput)); err != nil {
			t.Fatalf("sealed tree %s readonly verification failed: %v\n%s", root, err, readonlyOutput)
		}
	}

	stage := firstRoot + "_gen-reverse-run"
	previous := firstRoot + "_previous-reverse-run"
	zfstest.EnsureDataset(t, client, stage+"/new-child")
	snapshotName, err := replicationGenerationSnapshotName(policyID, "reverse-run")
	if err != nil {
		t.Fatalf("reverse snapshot name: %v", err)
	}
	if output, err := exec.Command("zfs", "snapshot", "-r", stage+"@"+snapshotName).CombinedOutput(); err != nil {
		t.Fatalf("snapshot reverse stage: %v\n%s", err, output)
	}
	opts := ReplicationZFSTransferOptions{
		PolicyID: policyID, RunID: "reverse-run", OwnerEpoch: 2,
		SnapshotName: snapshotName, SnapshotGUID: zfsGetProperty(t, stage+"@"+snapshotName, "guid"),
	}
	expectedStage := replicationProvenanceProperties(opts, firstRoot, firstRoot, replicationStateStaged)
	setRealZFSProperties(t, stage, expectedStage)
	setRealZFSReadonly(t, stage)

	promote := buildPromoteStagedReplicationScript(
		stage,
		firstRoot,
		previous,
		expectedStage,
		strconv.FormatUint(uint64(policyID), 10),
	)
	if output, err := runReplicationZFSScript(t, promote); err != nil {
		t.Fatalf("reverse promotion rejected sealed old owner: %v\n%s", err, output)
	}
	if !realZFSDatasetExists(t, firstRoot+"/new-child") || !realZFSDatasetExists(t, previous+"/old-child") {
		t.Fatal("reverse promotion did not preserve the expected current and previous generations")
	}
}

func TestIntegrationAdoptReturnedForceFailoverSourceAsStandby(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	secondPool, _ := zfstest.DedicatedPool(t)
	ctx := context.Background()

	root := pool + "/sylve/virtual-machines/412"
	secondRoot := secondPool + "/sylve/virtual-machines/412"
	zfstest.EnsureDataset(t, client, root)
	zfstest.EnsureVolume(t, client, root+"/zvol-43", 8)
	zfstest.EnsureDataset(t, client, secondRoot+"/data")
	roots := []string{root, secondRoot}

	db := newZeltaServiceTestDB(
		t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&vmModels.VM{},
	)
	policy := clusterModels.ReplicationPolicy{
		ID:                     43,
		Name:                   "returned-source",
		GuestType:              clusterModels.ReplicationGuestTypeVM,
		GuestID:                412,
		SourceNodeID:           "node-new",
		ActiveNodeID:           "node-new",
		OwnerEpoch:             4,
		SourceMode:             clusterModels.ReplicationSourceModeFollowActive,
		CronExpr:               "*/5 * * * *",
		Enabled:                true,
		TransitionState:        clusterModels.ReplicationTransitionStateCompleted,
		TransitionAllowUnsafe:  true,
		TransitionSourceNodeID: "node-old",
		TransitionTargetNodeID: "node-new",
		TransitionOwnerEpoch:   4,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("create force-failover policy: %v", err)
	}

	service := &Service{
		DB:      db,
		Cluster: &clusterService.Service{DB: db, NodeID: "node-old"},
		VM:      stubVMService{shutOff: true},
		GZFS:    client,
	}
	scopeLocalDatasetsToPool(t, service, pool, secondPool)

	if err := db.Model(&clusterModels.ReplicationPolicy{}).
		Where("id = ?", policy.ID).
		Updates(map[string]any{"active_node_id": "node-third", "owner_epoch": 5}).Error; err != nil {
		t.Fatalf("change policy owner before adoption: %v", err)
	}
	err := service.adoptReturnedForceFailoverSourceAsStandby(ctx, &policy, "node-old", "node-new")
	if err == nil || !strings.Contains(err.Error(), "replication_returned_source_ownership_changed") {
		t.Fatalf("changed ownership did not block source adoption: %v", err)
	}
	for _, dataset := range roots {
		if got := zfsGetProperty(t, dataset, replicationPropertyPolicyID); got != "-" {
			t.Fatalf("blocked adoption stamped policy provenance on %s: %q", dataset, got)
		}
		if got := zfsGetProperty(t, dataset, "readonly"); got != "off" {
			t.Fatalf("blocked adoption fenced source dataset %s: readonly=%q", dataset, got)
		}
	}

	if err := db.Model(&clusterModels.ReplicationPolicy{}).
		Where("id = ?", policy.ID).
		Updates(map[string]any{"active_node_id": "node-new", "owner_epoch": 4}).Error; err != nil {
		t.Fatalf("restore policy owner for adoption: %v", err)
	}
	if err := service.selfFenceReplicationPolicy(
		ctx,
		&policy,
		"node-old",
		"node-new",
		replicationFenceReasonPolicyOwnerMismatch,
		true,
	); err != nil {
		t.Fatalf("self-fence and adopt returned force-failover source: %v", err)
	}

	for _, dataset := range roots {
		expected := replicationCurrentStandbySeedProperties(policy.ID, dataset, dataset)
		for property, value := range expected {
			if got := zfsGetPropertyWithSource(t, dataset, property); got != value+" local" {
				t.Fatalf("adopted property %s on %s = %q, want %q", property, dataset, got, value+" local")
			}
		}
		readonlyOutput, err := exec.Command(
			"zfs", "get", "-H", "-o", "value", "-r", "-t", "filesystem,volume", "readonly", dataset,
		).CombinedOutput()
		if err != nil {
			t.Fatalf("read adopted source tree %s: %v\n%s", dataset, err, readonlyOutput)
		}
		if err := verifyReplicationReadonlyValues(string(readonlyOutput)); err != nil {
			t.Fatalf("adopted source tree %s readonly verification failed: %v\n%s", dataset, err, readonlyOutput)
		}
	}
}

func TestIntegrationSealReplicationDatasetRootsAsStandbyFailsBeforeMutation(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	root := pool + "/sylve/virtual-machines/411"
	zfstest.EnsureDataset(t, client, root)
	service := &Service{GZFS: client}
	err := service.sealReplicationDatasetRootsAsStandby(
		context.Background(),
		42,
		[]string{root, pool + "/sylve/virtual-machines/411-missing"},
	)
	if err == nil || !strings.Contains(err.Error(), "replication_transition_source_dataset_missing") {
		t.Fatalf("missing root did not fail source sealing: %v", err)
	}
	if got := zfsGetProperty(t, root, replicationPropertyPolicyID); got != "-" {
		t.Fatalf("failed sealing stamped partial standby provenance: %q", got)
	}
	if got := zfsGetProperty(t, root, "readonly"); got != "off" {
		t.Fatalf("failed sealing mutated a preflight-validated root: readonly=%q", got)
	}
}

func TestIntegrationFenceReplicationGuestDatasetsJail(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	ctx := context.Background()

	jailDS := pool + "/sylve/jails/50"
	zfstest.EnsureDataset(t, client, jailDS)

	s := &Service{GZFS: client}
	scopeLocalDatasetsToPool(t, s, pool)

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

func TestIntegrationFenceReplicationGuestDatasetsNoMatch(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	ctx := context.Background()

	s := &Service{GZFS: client}
	scopeLocalDatasetsToPool(t, s, pool)

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

func TestIntegrationUnfenceReplicationGuestDatasetsIfNeeded(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	ctx := context.Background()

	vmDS := pool + "/sylve/virtual-machines/300"
	zfstest.EnsureDataset(t, client, vmDS)

	s := &Service{GZFS: client}
	scopeLocalDatasetsToPool(t, s, pool)

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

func TestIntegrationFindLocalGuestDatasets(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
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
	scopeLocalDatasetsToPool(t, s, pool)

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
