// SPDX-License-Identifier: BSD-2-Clause

package zelta

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/hashicorp/raft"
)

type failingReplicationVMMetadataWriter struct {
	libvirtServiceInterfaces.LibvirtServiceInterface
	calls int
	rid   uint
	err   error
}

func (s *failingReplicationVMMetadataWriter) WriteVMJson(rid uint) error {
	s.calls++
	s.rid = rid
	return s.err
}

func readyReplicationTarget(nodeID, generation string, epoch uint64, verifiedAt, readyUntil time.Time, weight int) clusterModels.ReplicationPolicyTarget {
	return clusterModels.ReplicationPolicyTarget{
		NodeID:                nodeID,
		Weight:                weight,
		Ready:                 true,
		GenerationID:          generation,
		OwnerEpoch:            epoch,
		ManifestHash:          "manifest-" + generation,
		RequiredDatasetCount:  2,
		CompletedDatasetCount: 2,
		LastVerifiedAt:        &verifiedAt,
		ReadyUntil:            &readyUntil,
	}
}

func TestUnsupportedVMStorageInvalidatesPriorTargetReadiness(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
	)
	now := time.Now().UTC().Add(-time.Hour)
	readyUntil := now.Add(2 * time.Hour)
	policy := clusterModels.ReplicationPolicy{
		ID: 81, Name: "legacy-filesystem", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 8101, SourceNodeID: "node-a", ActiveNodeID: "node-a", OwnerEpoch: 3,
		Enabled: true, ProtectionState: clusterModels.ReplicationProtectionStateArmed,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}
	policy.Targets = []clusterModels.ReplicationPolicyTarget{
		readyReplicationTarget("node-b", "generation-81", policy.OwnerEpoch, now, readyUntil, 100),
	}
	policy.Targets[0].PolicyID = policy.ID
	if err := db.Create(&policy.Targets).Error; err != nil {
		t.Fatalf("create ready target: %v", err)
	}

	svc := &Service{DB: db, Cluster: &clusterService.Service{DB: db}}
	svc.setReplicationRuntimeClock(&fakeReplicationRuntimeClock{now: time.Now().UTC().Add(time.Hour)})
	if err := svc.invalidateReplicationPolicyTargetReadiness(
		&policy, errReplicationVMFilesystemStorageUnsupported,
	); err != nil {
		t.Fatalf("invalidate readiness: %v", err)
	}

	var target clusterModels.ReplicationPolicyTarget
	if err := db.Where("policy_id = ? AND node_id = ?", policy.ID, "node-b").First(&target).Error; err != nil {
		t.Fatalf("reload target: %v", err)
	}
	if target.Ready || target.LastError != errReplicationVMFilesystemStorageUnsupported.Error() {
		t.Fatalf("stale target readiness survived: ready=%v error=%q", target.Ready, target.LastError)
	}
	var updated clusterModels.ReplicationPolicy
	if err := db.First(&updated, policy.ID).Error; err != nil {
		t.Fatalf("reload policy: %v", err)
	}
	if updated.ProtectionState != clusterModels.ReplicationProtectionStateDegraded {
		t.Fatalf("protection state=%q, want degraded", updated.ProtectionState)
	}
}

func TestReplicationValidationForwardTimeoutHonorsContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	timeout, err := replicationValidationForwardTimeout(ctx)
	if err != nil {
		t.Fatalf("derive validation forward timeout: %v", err)
	}
	if timeout <= 0 || timeout > 500*time.Millisecond {
		t.Fatalf("replication validation forward timeout = %s, want within context deadline", timeout)
	}

	canceled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if _, err := replicationValidationForwardTimeout(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled validation forward timeout error = %v", err)
	}
}

func TestReplicationRunMetadataRefreshFailureInvalidatesReadinessBeforeDiscovery(t *testing.T) {
	database := newZeltaServiceTestDB(
		t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
	)
	writerErr := errors.New("vm.json disk full")
	writer := &failingReplicationVMMetadataWriter{err: writerErr}
	service := newTestZeltaService(database)
	service.Cluster = &clusterService.Service{DB: database}
	service.VM = writer
	policy := clusterModels.ReplicationPolicy{
		ID: 804, Name: "dirty-metadata", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 110, OwnerEpoch: 3, Enabled: true,
	}
	if err := database.Create(&policy).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}
	target := clusterModels.ReplicationPolicyTarget{
		PolicyID: policy.ID, NodeID: "hera", OwnerEpoch: policy.OwnerEpoch,
		Ready: true, GenerationID: "old-generation", ManifestHash: "old-manifest",
		RequiredDatasetCount: 1, CompletedDatasetCount: 1,
	}
	if err := database.Create(&target).Error; err != nil {
		t.Fatalf("create target: %v", err)
	}
	policy.Targets = []clusterModels.ReplicationPolicyTarget{target}

	err := service.refreshReplicationSourceMetadataForRun(&policy)
	if !errors.Is(err, writerErr) || !strings.Contains(err.Error(), "replication_vm_metadata_refresh_failed") {
		t.Fatalf("metadata refresh failure was not propagated: %v", err)
	}
	if writer.calls != 1 || writer.rid != policy.GuestID {
		t.Fatalf("unexpected vm.json refresh calls=%d rid=%d", writer.calls, writer.rid)
	}
	var reloaded clusterModels.ReplicationPolicyTarget
	if err := database.First(&reloaded, target.ID).Error; err != nil {
		t.Fatalf("reload target: %v", err)
	}
	if reloaded.Ready || !strings.Contains(reloaded.LastError, "replication_vm_metadata_refresh_failed") {
		t.Fatalf("dirty vm.json left target ready: %+v", reloaded)
	}
}

func TestNextReplicationLeaseVersionIsMonotonicAcrossClockRegression(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	clockVersion := uint64(now.UnixNano())

	got, err := nextReplicationLeaseVersion(now, clockVersion+50)
	if err != nil {
		t.Fatal(err)
	}
	if got != clockVersion+51 {
		t.Fatalf("version = %d, want %d", got, clockVersion+51)
	}

	if _, err := nextReplicationLeaseVersion(now, math.MaxUint64); err == nil {
		t.Fatal("expected exhausted lease version to fail")
	}
}

func TestForceFailoverSelectionRequiresCompleteGenerationAndUsesFreshest(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	s := &Service{}
	s.setReplicationRuntimeClock(&fakeReplicationRuntimeClock{now: now})
	older := now.Add(-2 * time.Hour)
	newer := now.Add(-time.Hour)
	expired := now.Add(-time.Minute)
	policy := &clusterModels.ReplicationPolicy{
		OwnerEpoch: 4,
		Targets: []clusterModels.ReplicationPolicyTarget{
			readyReplicationTarget("node-old", "old", 4, older, expired, 500),
			readyReplicationTarget("node-new", "new", 4, newer, expired, 10),
			{NodeID: "node-partial", Weight: 1000, Ready: true, OwnerEpoch: 4, RequiredDatasetCount: 2, CompletedDatasetCount: 1},
		},
	}
	nodes := map[string]clusterModels.ClusterNode{
		"node-old":     {NodeUUID: "node-old", Status: "online"},
		"node-new":     {NodeUUID: "node-new", Status: "online"},
		"node-partial": {NodeUUID: "node-partial", Status: "online"},
	}

	got, err := s.selectFailoverTargetWithReadiness(policy, "owner", nodes, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if got != "node-new" {
		t.Fatalf("selected %q, want freshest complete node-new", got)
	}

	if _, err := s.selectFailoverTargetWithReadiness(policy, "owner", nodes, true, false); err == nil {
		t.Fatal("expected safe freshness gate to reject expired generations")
	}
}

func TestReplicationTargetGenerationRequiresWholeManifest(t *testing.T) {
	now := time.Now().UTC()
	target := readyReplicationTarget("node-b", "g1", 7, now, now.Add(time.Hour), 100)
	if !replicationTargetEligibleForPromotion(&target, 7, now, false) {
		t.Fatal("complete fresh target should be eligible")
	}
	target.CompletedDatasetCount = 1
	if replicationTargetEligibleForPromotion(&target, 7, now, true) {
		t.Fatal("force mode must not accept a partial multi-dataset generation")
	}
}

func TestRotatedReplicationTargetsInvalidateOldReadiness(t *testing.T) {
	now := time.Now().UTC()
	policy := &clusterModels.ReplicationPolicy{
		ID: 9,
		Targets: []clusterModels.ReplicationPolicyTarget{
			readyReplicationTarget("node-b", "g1", 2, now, now.Add(time.Hour), 200),
			readyReplicationTarget("node-c", "g1", 2, now, now.Add(time.Hour), 100),
		},
	}
	rotated := rotatedReplicationPolicyTargets(policy, "node-a", "node-b", 3)
	if len(rotated) != 2 {
		t.Fatalf("rotated target count = %d, want 2", len(rotated))
	}
	for _, target := range rotated {
		if target.NodeID == "node-b" {
			t.Fatal("new owner must not remain a replication target")
		}
		if target.Ready || target.OwnerEpoch != 3 || target.LastError != "awaiting_post_transition_validation" {
			t.Fatalf("target readiness was not invalidated: %+v", target)
		}
	}
}

func TestReplicationManifestHashBindsEveryRootAndEpoch(t *testing.T) {
	manifest := []ReplicationSnapshotManifestEntry{
		{SourceDataset: "pool-a/vm/1", SnapshotName: "ha_g1", SnapshotGUID: "11"},
		{SourceDataset: "pool-b/vm/1", SnapshotName: "ha_g1", SnapshotGUID: "22"},
	}
	first := replicationSnapshotManifestHash(1, 3, "g1", manifest)
	second := replicationSnapshotManifestHash(1, 3, "g1", manifest)
	if first == "" || first != second {
		t.Fatalf("manifest hash is not deterministic: %q vs %q", first, second)
	}
	changed := append([]ReplicationSnapshotManifestEntry{}, manifest...)
	changed[1].SnapshotGUID = "23"
	if first == replicationSnapshotManifestHash(1, 3, "g1", changed) {
		t.Fatal("manifest hash did not bind the second dataset GUID")
	}
	if first == replicationSnapshotManifestHash(1, 4, "g1", manifest) {
		t.Fatal("manifest hash did not bind the owner epoch")
	}
}

func TestReplicationGenerationSnapshotNameBoundsLongIDs(t *testing.T) {
	name, err := replicationGenerationSnapshotName(strings.Repeat("a", 128))
	if err != nil {
		t.Fatal(err)
	}
	if len(name) > 128 || !strings.HasPrefix(name, haSnapPrefix) {
		t.Fatalf("invalid bounded snapshot name %q", name)
	}
}

func TestReplicationLifecycleBlocksRunsAndNewTransitions(t *testing.T) {
	policy := &clusterModels.ReplicationPolicy{
		Enabled:         true,
		ProtectionState: clusterModels.ReplicationProtectionStateDeleting,
		TransitionState: clusterModels.ReplicationTransitionStateCompleted,
	}
	if replicationPolicyAllowsRuns(policy) || replicationPolicyAcceptsNewTransition(policy) {
		t.Fatal("deleting policy must reject runs and ownership transitions")
	}
	policy.ProtectionState = clusterModels.ReplicationProtectionStateDegraded
	if !replicationPolicyAllowsRuns(policy) || !replicationPolicyAcceptsNewTransition(policy) {
		t.Fatal("degraded terminal policy should remain runnable and movable")
	}
	policy.TransitionState = clusterModels.ReplicationTransitionStateRollingBack
	if replicationPolicyAllowsRuns(policy) || replicationPolicyAcceptsNewTransition(policy) {
		t.Fatal("rolling-back policy must remain durably locked")
	}
}

func TestReplicationLineagePathsAreNeverActiveGuestRoots(t *testing.T) {
	for _, dataset := range []string{
		"zroot/sylve/virtual-machines/100_gen-run-a",
		"zroot/sylve/virtual-machines/100_previous-run-a",
		"tank/sylve/jails/42_gen-run-b/root",
	} {
		if !isReplicationLineageDatasetPath(dataset) {
			t.Fatalf("lineage path was accepted as active: %s", dataset)
		}
	}
	for _, dataset := range []string{
		"zroot/sylve/virtual-machines/100",
		"tank/sylve/jails/42",
		"fast_gen-pool/sylve/virtual-machines/100",
		"tank/sylve/jails/archive_gen-data/42",
	} {
		if isReplicationLineageDatasetPath(dataset) {
			t.Fatalf("active root was classified as lineage: %s", dataset)
		}
	}
}

func TestReplicationLeaseRenewalEligibilityPreservesOnlyAuthoritativeOwner(t *testing.T) {
	policy := &clusterModels.ReplicationPolicy{
		ID:              1,
		Enabled:         true,
		ActiveNodeID:    "node-a",
		OwnerEpoch:      3,
		TransitionState: clusterModels.ReplicationTransitionStateDemoting,
	}
	if !replicationPolicyAllowsLeaseRenewal(policy) {
		t.Fatal("safe demotion must retain the previous owner's lease for rollback")
	}
	policy.TransitionAllowUnsafe = true
	if replicationPolicyAllowsLeaseRenewal(policy) {
		t.Fatal("unsafe demotion must allow the unreachable owner's lease to expire")
	}
	policy.TransitionState = clusterModels.ReplicationTransitionStatePromoting
	if !replicationPolicyAllowsLeaseRenewal(policy) {
		t.Fatal("the committed target lease must renew while promotion is recovering")
	}
	policy.ProtectionState = clusterModels.ReplicationProtectionStateDeleting
	if !replicationPolicyAllowsLeaseRenewal(policy) {
		t.Fatal("deletion must retain authority until cleanup has acknowledged")
	}
	policy.Enabled = false
	if replicationPolicyAllowsLeaseRenewal(policy) {
		t.Fatal("disabled policies do not own a renewable protection lease")
	}
}

func TestOwnershipCommitErrorIsReconciledFromPolicyAndLease(t *testing.T) {
	policy := &clusterModels.ReplicationPolicy{
		ActiveNodeID:         "node-b",
		OwnerEpoch:           8,
		TransitionRunID:      "run-1",
		TransitionState:      clusterModels.ReplicationTransitionStatePromoting,
		TransitionOwnerEpoch: 8,
	}
	lease := &clusterModels.ReplicationLease{OwnerNodeID: "node-b", OwnerEpoch: 8}
	if got := classifyReplicationOwnershipCommit(policy, lease, "node-a", "node-b", 7, 8, "run-1"); got != replicationOwnershipCommitApplied {
		t.Fatalf("committed state classified as %v", got)
	}

	policy.ActiveNodeID = "node-a"
	policy.OwnerEpoch = 7
	policy.TransitionState = clusterModels.ReplicationTransitionStateCatchup
	if got := classifyReplicationOwnershipCommit(policy, lease, "node-a", "node-b", 7, 8, "run-1"); got != replicationOwnershipCommitNotApplied {
		t.Fatalf("unchanged predecessor classified as %v", got)
	}

	policy.TransitionState = clusterModels.ReplicationTransitionStateFailed
	if got := classifyReplicationOwnershipCommit(policy, lease, "node-a", "node-b", 7, 8, "run-1"); got != replicationOwnershipCommitAmbiguous {
		t.Fatalf("terminal/mixed state classified as %v", got)
	}
}

func TestMissingLeaseForceBarrierIsBounded(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &clusterModels.ReplicationLease{})
	start := time.Unix(2_000_000_000, 0).UTC()
	clock := &fakeReplicationRuntimeClock{now: start}
	s := &Service{Cluster: &clusterService.Service{DB: db}}
	s.setReplicationRuntimeClock(clock)
	policy := &clusterModels.ReplicationPolicy{ID: 99}

	if err := s.waitForPreviousOwnerLeaseExpiry(nil, policy, "node-old", 1); err != nil {
		t.Fatal(err)
	}
	elapsed := s.now().Sub(start)
	want := 2*replicationLeaseTTL + replicationLeaseExpirySafetyMargin
	if elapsed < want || elapsed > want+250*time.Millisecond {
		t.Fatalf("missing lease barrier elapsed %s, want bounded near %s", elapsed, want)
	}
}

func TestOlderLeaseForceBarrierIsBounded(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &clusterModels.ReplicationLease{})
	start := time.Unix(2_000_000_000, 0).UTC()
	if err := db.Create(&clusterModels.ReplicationLease{
		PolicyID: 100, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 1,
		OwnerNodeID: "old-owner", OwnerEpoch: 1, Version: 1, ExpiresAt: start.Add(time.Hour),
	}).Error; err != nil {
		t.Fatal(err)
	}
	clock := &fakeReplicationRuntimeClock{now: start}
	s := &Service{Cluster: &clusterService.Service{DB: db}}
	s.setReplicationRuntimeClock(clock)
	policy := &clusterModels.ReplicationPolicy{ID: 100}
	if err := s.waitForPreviousOwnerLeaseExpiry(nil, policy, "current-owner", 2); err != nil {
		t.Fatal(err)
	}
	elapsed := s.now().Sub(start)
	if elapsed < replicationLeaseExpirySafetyMargin || elapsed > replicationLeaseExpirySafetyMargin+250*time.Millisecond {
		t.Fatalf("older lease barrier elapsed %s", elapsed)
	}
}

func TestForceCutoverMarginExceedsFencePollingWindow(t *testing.T) {
	if replicationLeaseExpirySafetyMargin < 2*replicationSelfFenceInterval {
		t.Fatalf(
			"force cutover margin %s is shorter than two fence polls %s",
			replicationLeaseExpirySafetyMargin,
			2*replicationSelfFenceInterval,
		)
	}
}

func TestReplicationNotLeaderErrorRecognition(t *testing.T) {
	for _, err := range []error{
		raft.ErrNotLeader,
		errors.New("not_leader"),
		errors.New("node is not the leader"),
	} {
		if !isReplicationRaftNotLeaderError(err) {
			t.Fatalf("did not recognize follower error %q", err)
		}
	}
	if isReplicationRaftNotLeaderError(errors.New("replication failed")) {
		t.Fatal("ordinary replication error was classified as follower routing")
	}
}

func TestReplicationFenceObservationNeverExtendsLeaseDeadline(t *testing.T) {
	now := time.Date(2026, time.July, 11, 8, 0, 0, 0, time.UTC)
	observation := replicationFenceObservation{
		LeaseOwner:     "node-a",
		LeaseEpoch:     9,
		LeaseExpiresAt: now.Add(time.Second),
	}
	if !replicationFenceObservationLeaseValid(observation, "node-a", "node-a", 9, now) {
		t.Fatal("matching cached lease should remain valid before its observed deadline")
	}
	if replicationFenceObservationLeaseValid(observation, "node-a", "node-a", 9, now.Add(time.Second)) {
		t.Fatal("cached lease must expire at the observed deadline")
	}
	if replicationFenceObservationLeaseValid(observation, "node-a", "node-a", 10, now) {
		t.Fatal("cached lease from an older epoch must not authorize writes")
	}
	if replicationFenceObservationLeaseValid(observation, "node-a", "node-b", 9, now) {
		t.Fatal("cached lease must not override a changed policy owner")
	}

	createdAt := now.Add(-time.Hour)
	if got := replicationFenceMissingLeaseDeadline(now.Add(time.Minute), createdAt); !got.Equal(createdAt.Add(2 * replicationLeaseTTL)) {
		t.Fatalf("creation grace should shorten a later cached deadline: %s", got)
	}
	previous := now.Add(5 * time.Second)
	if got := replicationFenceMissingLeaseDeadline(previous, now); !got.Equal(previous) {
		t.Fatalf("missing lease extended the cached deadline: got %s want %s", got, previous)
	}
}

func TestDurableReplicationFenceObservationRoundTrip(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	now := time.Date(2026, time.July, 11, 9, 0, 0, 0, time.UTC)
	want := map[uint]replicationFenceObservation{
		7: {
			Policy: clusterModels.ReplicationPolicy{
				ID: 7, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 700,
				ActiveNodeID: "node-a", OwnerEpoch: 3, Enabled: true,
			},
			LeaseOwner: "node-a", LeaseEpoch: 3, LeaseExpiresAt: now,
		},
	}
	if err := persistDurableReplicationFenceObservations(want); err != nil {
		t.Fatalf("persist observations: %v", err)
	}
	got, err := loadDurableReplicationFenceObservations()
	if err != nil {
		t.Fatalf("load observations: %v", err)
	}
	observation, ok := got[7]
	if !ok || observation.Policy.GuestID != 700 || observation.LeaseEpoch != 3 ||
		!observation.LeaseExpiresAt.Equal(now) {
		t.Fatalf("observation mismatch: %+v", got)
	}
}
