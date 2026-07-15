// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func runReplicationZFSScript(t *testing.T, script string) (string, error) {
	t.Helper()
	output, err := exec.Command("sh", "-c", script).CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func scopeLocalFilesystemDatasetsToPool(t *testing.T, service *Service, pool string) {
	t.Helper()
	if service == nil || strings.TrimSpace(pool) == "" {
		t.Fatal("scoped local dataset lister requires a service and pool")
	}
	service.localFilesystemDatasetLister = func(ctx context.Context) ([]string, error) {
		output, err := exec.CommandContext(
			ctx, "zfs", "list", "-H", "-o", "name", "-r", "-t", "filesystem,volume", pool,
		).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("list disposable ZFS pool %s: %w: %s", pool, err, strings.TrimSpace(string(output)))
		}
		return strings.Fields(string(output)), nil
	}
}

func realZFSDatasetExists(t *testing.T, dataset string) bool {
	t.Helper()
	return exec.Command("zfs", "list", "-H", "-o", "name", dataset).Run() == nil
}

func realZFSSnapshotExists(t *testing.T, dataset, snapshot string) bool {
	t.Helper()
	return exec.Command("zfs", "list", "-H", "-t", "snapshot", dataset+"@"+snapshot).Run() == nil
}

func zfsGetPropertyWithSource(t *testing.T, dataset, property string) string {
	t.Helper()
	output, err := exec.Command("zfs", "get", "-H", "-o", "value,source", property, dataset).CombinedOutput()
	if err != nil {
		t.Fatalf("zfs get %s %s: %v\n%s", property, dataset, err, output)
	}
	return strings.Join(strings.Fields(string(output)), " ")
}

func setRealZFSProperties(t *testing.T, dataset string, properties map[string]string) {
	t.Helper()
	for property, value := range properties {
		output, err := exec.Command("zfs", "set", property+"="+value, dataset).CombinedOutput()
		if err != nil {
			t.Fatalf("zfs set %s=%s %s: %v\n%s", property, value, dataset, err, string(output))
		}
	}
}

func setRealZFSReadonly(t *testing.T, dataset string) {
	t.Helper()
	output, err := exec.Command("zfs", "list", "-H", "-o", "name", "-r", "-t", "filesystem,volume", dataset).CombinedOutput()
	if err != nil {
		t.Fatalf("zfs list readonly subtree %s: %v\n%s", dataset, err, string(output))
	}
	for _, child := range strings.Fields(string(output)) {
		if setOutput, setErr := exec.Command("zfs", "set", "readonly=on", child).CombinedOutput(); setErr != nil {
			t.Fatalf("zfs set readonly=on %s: %v\n%s", child, setErr, string(setOutput))
		}
	}
}

func TestCleanTargetFirstReplicationRealZFSOverSSH(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS first-sync integration test in short mode")
	}

	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

	source := pool + "/source/guest"
	zfstest.EnsureDataset(t, client, source+"/child")
	zfstest.EnsureVolume(t, client, source+"/disk", 8)
	localSnapshot := "manual_before_ha"
	if output, err := exec.Command("zfs", "snapshot", "-r", source+"@"+localSnapshot).CombinedOutput(); err != nil {
		t.Fatalf("create source-local snapshot: %v\n%s", err, output)
	}

	targetRoot := pool + "/replicas"
	targetDataset := targetRoot + "/guest"
	target := &clusterModels.BackupTarget{
		SSHHost:    "root@127.0.0.1",
		SSHPort:    22,
		BackupRoot: targetRoot,
	}
	service := &Service{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	preflight := exec.CommandContext(
		ctx,
		"ssh",
		"-n",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=3",
		"root@127.0.0.1",
		"true",
	)
	if output, err := preflight.CombinedOutput(); err != nil {
		t.Skipf("localhost root SSH is unavailable for real replication integration: %v (%s)", err, output)
	}
	if output, err := service.runTargetSSH(ctx, target, "zfs", "version"); err != nil {
		t.Fatalf("production target SSH invocation failed after localhost preflight: %v (%s)", err, output)
	}
	if realZFSDatasetExists(t, targetRoot) {
		t.Fatalf("test target must begin completely absent: %s", targetRoot)
	}
	if exists, output, err := service.remoteDatasetExists(ctx, target, targetRoot); err != nil || exists {
		t.Fatalf("real missing-dataset lookup must report absent: exists=%v err=%v output=%q", exists, err, output)
	}

	snapshot := "ha_first_sync"
	if err := service.CreateReplicationSnapshotGroup(ctx, []string{source}, snapshot); err != nil {
		t.Fatalf("create first-sync snapshot: %v", err)
	}
	manifest, err := service.GetReplicationSnapshotManifest(ctx, []string{source}, snapshot)
	if err != nil || len(manifest) != 1 {
		t.Fatalf("resolve first-sync manifest: entries=%d err=%v", len(manifest), err)
	}
	opts := ReplicationZFSTransferOptions{
		PolicyID:               7,
		RunID:                  "first-sync",
		OwnerEpoch:             1,
		SnapshotName:           snapshot,
		SnapshotGUID:           manifest[0].SnapshotGUID,
		SnapshotAlreadyCreated: true,
		GenerationName:         "first-sync",
	}

	ready, err := service.replicationDatasetGenerationReady(ctx, target, source, "guest", opts)
	if err != nil {
		t.Fatalf("clean target readiness probe must report absence, not an error: %v", err)
	}
	if ready {
		t.Fatal("a completely absent target cannot already be ready")
	}

	staged, output, err := service.ReplicationZFSSendStaged(ctx, target, source, "guest", opts, nil)
	if err != nil {
		readonlyState, _ := exec.Command("zfs", "get", "-H", "-o", "name,value,source", "-r", "readonly", staged.StagingDataset).CombinedOutput()
		t.Fatalf("first staged replication failed: %v\n%s\nreadonly state:\n%s", err, output, readonlyState)
	}
	if !realZFSDatasetExists(t, targetRoot) {
		t.Fatal("first sync did not create the missing target parent")
	}
	if realZFSDatasetExists(t, targetDataset) {
		t.Fatal("canonical target appeared before promotion")
	}
	for _, dataset := range []string{staged.StagingDataset, staged.StagingDataset + "/child", staged.StagingDataset + "/disk"} {
		if !realZFSDatasetExists(t, dataset) {
			t.Fatalf("staged recursive dataset missing: %s", dataset)
		}
		if !realZFSSnapshotExists(t, dataset, localSnapshot) {
			t.Fatalf("recursive send did not carry source-local snapshot into staging: %s@%s", dataset, localSnapshot)
		}
	}

	if err := service.PromoteStagedReplicationDataset(ctx, target, source, "guest", opts); err != nil {
		t.Fatalf("promote first replication generation: %v", err)
	}
	if realZFSDatasetExists(t, staged.StagingDataset) {
		t.Fatalf("staging dataset remains after promotion: %s", staged.StagingDataset)
	}
	for _, dataset := range []string{targetDataset, targetDataset + "/child", targetDataset + "/disk"} {
		if !realZFSDatasetExists(t, dataset) {
			t.Fatalf("promoted recursive dataset missing: %s", dataset)
		}
		if realZFSSnapshotExists(t, dataset, localSnapshot) {
			t.Fatalf("source-local snapshot survived standby promotion: %s@%s", dataset, localSnapshot)
		}
		if !realZFSSnapshotExists(t, dataset, snapshot) {
			t.Fatalf("HA snapshot was removed during standby pruning: %s@%s", dataset, snapshot)
		}
	}
	for _, dataset := range []string{source, source + "/child", source + "/disk"} {
		if !realZFSSnapshotExists(t, dataset, localSnapshot) {
			t.Fatalf("standby pruning touched source-local snapshot: %s@%s", dataset, localSnapshot)
		}
	}

	readonlyOutput, err := exec.Command(
		"zfs", "get", "-H", "-o", "value", "-r", "-t", "filesystem,volume", "readonly", targetDataset,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("read promoted readonly state: %v\n%s", err, readonlyOutput)
	}
	for _, value := range strings.Fields(string(readonlyOutput)) {
		if value != "on" {
			t.Fatalf("promoted target is not recursively readonly: %q", string(readonlyOutput))
		}
	}

	expected := replicationProvenanceProperties(opts, source, targetDataset, replicationStateReady)
	for property, want := range expected {
		if got := zfsGetProperty(t, targetDataset, property); got != want {
			t.Fatalf("promoted provenance %s=%q, want %q", property, got, want)
		}
	}
	ready, err = service.replicationDatasetGenerationReady(ctx, target, source, "guest", opts)
	if err != nil || !ready {
		t.Fatalf("promoted first generation not ready: ready=%v err=%v", ready, err)
	}
}

func TestCleanupReplicationSourceSnapshotGroupRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS failed-source-snapshot cleanup test in short mode")
	}

	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

	first := pool + "/source/first"
	second := pool + "/source/second"
	zfstest.EnsureDataset(t, client, first+"/child")
	zfstest.EnsureVolume(t, client, first+"/disk", 8)
	zfstest.EnsureDataset(t, client, second+"/child")
	service := &Service{}
	keepSnapshot := "ha_keep_cleanup"
	failedSnapshot := "ha_failed_cleanup"
	for _, snapshot := range []string{keepSnapshot, failedSnapshot} {
		if err := service.CreateReplicationSnapshotGroup(context.Background(), []string{first, second}, snapshot); err != nil {
			t.Fatalf("create %s snapshot group: %v", snapshot, err)
		}
	}
	if output, err := exec.Command("zfs", "snapshot", "-r", first+"@user_keep").CombinedOutput(); err != nil {
		t.Fatalf("create unrelated user snapshot: %v\n%s", err, output)
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.cleanupReplicationSourceSnapshotGroup(canceledCtx, []string{first, second}, failedSnapshot); err != nil {
		t.Fatalf("cleanup exact failed snapshot group after cancellation: %v", err)
	}
	for _, dataset := range []string{first, first + "/child", first + "/disk", second, second + "/child"} {
		if realZFSSnapshotExists(t, dataset, failedSnapshot) {
			t.Fatalf("failed generation snapshot remains: %s@%s", dataset, failedSnapshot)
		}
		if !realZFSSnapshotExists(t, dataset, keepSnapshot) {
			t.Fatalf("unrelated HA snapshot was removed: %s@%s", dataset, keepSnapshot)
		}
		if !realZFSDatasetExists(t, dataset) {
			t.Fatalf("source dataset was removed during snapshot cleanup: %s", dataset)
		}
	}
	for _, dataset := range []string{first, first + "/child", first + "/disk"} {
		if !realZFSSnapshotExists(t, dataset, "user_keep") {
			t.Fatalf("unrelated user snapshot was removed: %s@user_keep", dataset)
		}
	}
	if err := service.cleanupReplicationSourceSnapshotGroup(context.Background(), []string{first, second}, failedSnapshot); err != nil {
		t.Fatalf("source snapshot cleanup must be idempotent: %v", err)
	}
}

func TestPolicyGenerationCancellationBeforeFirstProbeCleansSourceSnapshotRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS policy-generation cancellation integration test in short mode")
	}

	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

	guestID := uint(731)
	source := fmt.Sprintf("%s/sylve/jails/%d", pool, guestID)
	zfstest.EnsureDataset(t, client, source+"/root")
	zfstest.EnsureVolume(t, client, source+"/disk", 8)

	db := testutil.NewSQLiteTestDB(t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ClusterSSHIdentity{},
		&clusterModels.ClusterNode{},
		&clusterModels.Cluster{},
	)
	clusterSvc := &clusterService.Service{DB: db}
	localNodeID := strings.TrimSpace(clusterSvc.LocalNodeID())
	if localNodeID == "" {
		t.Fatal("local node identity unavailable")
	}
	policy := &clusterModels.ReplicationPolicy{
		ID:              731,
		Name:            "cancel-before-first-probe",
		GuestType:       clusterModels.ReplicationGuestTypeJail,
		GuestID:         guestID,
		SourceNodeID:    localNodeID,
		ActiveNodeID:    localNodeID,
		OwnerEpoch:      1,
		SourceMode:      clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode:    clusterModels.ReplicationFailbackManual,
		FailoverMode:    clusterModels.ReplicationFailoverManual,
		CronExpr:        "*/5 * * * *",
		Enabled:         true,
		ProtectionState: clusterModels.ReplicationProtectionStateInitializing,
		TransitionState: clusterModels.ReplicationTransitionStateNone,
		Targets: []clusterModels.ReplicationPolicyTarget{
			{NodeID: "target-node", Weight: 100},
		},
	}
	if err := clusterModels.UpsertReplicationPolicyTxn(db, policy, policy.Targets); err != nil {
		t.Fatalf("seed replication policy: %v", err)
	}
	if err := db.Create(&clusterModels.ReplicationLease{
		PolicyID:    policy.ID,
		GuestType:   policy.GuestType,
		GuestID:     policy.GuestID,
		OwnerNodeID: localNodeID,
		OwnerEpoch:  policy.OwnerEpoch,
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
		Version:     1,
	}).Error; err != nil {
		t.Fatalf("seed replication lease: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for readiness SSH probe: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := db.Create(&clusterModels.ClusterSSHIdentity{
		NodeUUID:  "target-node",
		SSHUser:   "root",
		SSHHost:   "127.0.0.1",
		SSHPort:   port,
		PublicKey: "test-listener-does-not-authenticate",
	}).Error; err != nil {
		t.Fatalf("seed target SSH identity: %v", err)
	}
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	service := NewService(db, nil, clusterSvc, nil, nil, nil, client)
	scopeLocalFilesystemDatasetsToPool(t, service, pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	probeAccepted := make(chan struct{})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			cancel()
			_ = connection.Close()
		}
		close(probeAccepted)
	}()

	generationID := "cancel_before_first_probe"
	result, err := service.replicatePolicyGenerationToTarget(
		ctx,
		policy,
		"target-node",
		policy.OwnerEpoch,
		"",
		generationID,
		0,
	)
	if err == nil {
		t.Fatal("expected cancellation during the first readiness probe to fail the generation")
	}
	select {
	case <-probeAccepted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the readiness SSH probe")
	}
	if ctx.Err() == nil {
		t.Fatal("test did not cancel the replication context")
	}
	if errors.Is(err, errReplicationGenerationCommitUncertain) {
		t.Fatalf("unmutated first target was incorrectly rolled back and labeled commit-uncertain: %v", err)
	}
	if !strings.Contains(err.Error(), "verify_replication_dataset_generation_"+source+"_failed") {
		t.Fatalf("generation did not fail at the first readiness probe: %v", err)
	}

	snapshot, err := replicationGenerationSnapshotName(generationID)
	if err != nil {
		t.Fatal(err)
	}
	if result.GenerationID != generationID || result.SnapshotName != snapshot || result.RequiredDatasetCount != 1 {
		t.Fatalf("snapshot manifest was not established before cancellation: %#v", result)
	}
	for _, dataset := range []string{source, source + "/root", source + "/disk"} {
		if realZFSSnapshotExists(t, dataset, snapshot) {
			t.Fatalf("certain failed generation leaked source snapshot: %s@%s", dataset, snapshot)
		}
		if !realZFSDatasetExists(t, dataset) {
			t.Fatalf("source dataset was removed during failed-generation cleanup: %s", dataset)
		}
	}
}

func TestAbortAndDestroyProvenStagingRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS replication cleanup integration test in short mode")
	}

	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

	expected := map[string]string{
		replicationPropertyPolicyID:   "7",
		replicationPropertyRunID:      "run-7",
		replicationPropertyOwnerEpoch: "3",
	}
	proven := pool + "/replication/guest_gen-proven"
	zfstest.EnsureDataset(t, client, proven)
	setRealZFSProperties(t, proven, expected)

	if output, err := runReplicationZFSScript(t, buildAbortAndDestroyProvenStagingScript(proven, expected)); err != nil {
		t.Fatalf("proven staging cleanup failed: %v\n%s", err, output)
	}
	if realZFSDatasetExists(t, proven) {
		t.Fatalf("proven staging dataset still exists: %s", proven)
	}

	foreign := pool + "/replication/guest_gen-foreign"
	zfstest.EnsureDataset(t, client, foreign)
	setRealZFSProperties(t, foreign, map[string]string{
		replicationPropertyPolicyID:   "7",
		replicationPropertyRunID:      "another-run",
		replicationPropertyOwnerEpoch: "3",
	})
	output, err := runReplicationZFSScript(t, buildAbortAndDestroyProvenStagingScript(foreign, expected))
	if err == nil || !strings.Contains(output, "replication_provenance_mismatch_before_abort") {
		t.Fatalf("foreign staging cleanup did not fail closed: err=%v output=%q", err, output)
	}
	if !realZFSDatasetExists(t, foreign) {
		t.Fatalf("foreign staging dataset was destroyed: %s", foreign)
	}
}

func TestCleanupStaleReplicationStagingRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS stale-generation integration test in short mode")
	}

	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

	target := pool + "/replication/guest"
	zfstest.EnsureDataset(t, client, target)
	base := map[string]string{
		replicationPropertyPolicyID:   "7",
		replicationPropertyTarget:     target,
		replicationPropertyRole:       replicationRoleStandby,
		replicationPropertyOwnerEpoch: "3",
		replicationPropertyState:      replicationStateReceiving,
	}

	stale := target + "_gen-stale"
	zfstest.EnsureDataset(t, client, stale)
	staleProperties := copyReplicationProperties(base)
	staleProperties[replicationPropertyRunID] = "run-stale"
	setRealZFSProperties(t, stale, staleProperties)

	kept := target + "_gen-current"
	zfstest.EnsureDataset(t, client, kept)
	keptProperties := copyReplicationProperties(base)
	keptProperties[replicationPropertyRunID] = "run-current"
	setRealZFSProperties(t, kept, keptProperties)

	foreign := target + "_gen-foreign"
	zfstest.EnsureDataset(t, client, foreign)
	foreignProperties := copyReplicationProperties(base)
	foreignProperties[replicationPropertyPolicyID] = "999"
	foreignProperties[replicationPropertyRunID] = "run-foreign"
	setRealZFSProperties(t, foreign, foreignProperties)

	script := buildCleanupStaleReplicationStagingScript(target, 7, 3, "run-current")
	if output, err := runReplicationZFSScript(t, script); err != nil {
		t.Fatalf("stale staging sweep failed: %v\n%s", err, output)
	}
	if realZFSDatasetExists(t, stale) {
		t.Fatalf("proven stale staging generation still exists: %s", stale)
	}
	if !realZFSDatasetExists(t, kept) {
		t.Fatalf("current staging generation was destroyed: %s", kept)
	}
	if !realZFSDatasetExists(t, foreign) {
		t.Fatalf("foreign staging generation was destroyed: %s", foreign)
	}
}

func TestPromoteAndRollbackStagedReplicationRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS promotion integration test in short mode")
	}

	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

	target := pool + "/replication/guest"
	stage := target + "_gen-run-next"
	previous := target + "_previous-run-next"
	blocker := pool + "/snapshot-clone-blocker"
	zfstest.EnsureDataset(t, client, target+"/old-child")
	zfstest.EnsureDataset(t, client, stage+"/new-child")
	for _, snapshot := range []string{"manual_stage", "ha_next"} {
		if output, err := exec.Command("zfs", "snapshot", "-r", stage+"@"+snapshot).CombinedOutput(); err != nil {
			t.Fatalf("create staged %s snapshot: %v\n%s", snapshot, err, output)
		}
	}
	if output, err := exec.Command("zfs", "clone", stage+"@manual_stage", blocker).CombinedOutput(); err != nil {
		t.Fatalf("create dependent clone: %v\n%s", err, output)
	}
	setRealZFSProperties(t, target, map[string]string{
		replicationPropertyPolicyID: "7",
		replicationPropertyRole:     replicationRoleStandby,
	})
	setRealZFSReadonly(t, target)

	opts := ReplicationZFSTransferOptions{
		PolicyID:     7,
		RunID:        "run-next",
		OwnerEpoch:   4,
		SnapshotName: "ha_next",
		SnapshotGUID: "12345",
	}
	expectedStage := replicationProvenanceProperties(opts, pool+"/source", target, replicationStateStaged)
	setRealZFSProperties(t, stage, expectedStage)
	setRealZFSReadonly(t, stage)

	promote := buildPromoteStagedReplicationScript(stage, target, previous, expectedStage, "7")
	output, err := runReplicationZFSScript(t, promote)
	if err == nil || !strings.Contains(output, "replication_staging_snapshot_destroy_failed") {
		t.Fatalf("dependent user snapshot did not fail promotion closed: err=%v\n%s", err, output)
	}
	if !realZFSDatasetExists(t, stage) || !realZFSDatasetExists(t, target) || realZFSDatasetExists(t, previous) {
		t.Fatalf("failed pruning changed promotion topology: stage=%v target=%v previous=%v",
			realZFSDatasetExists(t, stage), realZFSDatasetExists(t, target), realZFSDatasetExists(t, previous))
	}
	if !realZFSDatasetExists(t, blocker) {
		t.Fatal("exact snapshot pruning destroyed a dependent clone")
	}
	if destroyOutput, destroyErr := exec.Command("zfs", "destroy", blocker).CombinedOutput(); destroyErr != nil {
		t.Fatalf("destroy dependent clone: %v\n%s", destroyErr, destroyOutput)
	}
	if output, err := runReplicationZFSScript(t, promote); err != nil {
		t.Fatalf("real ZFS promotion failed: %v\n%s", err, output)
	}
	if realZFSDatasetExists(t, stage) || !realZFSDatasetExists(t, target) || !realZFSDatasetExists(t, previous) {
		t.Fatalf("unexpected post-promotion topology: stage=%v target=%v previous=%v",
			realZFSDatasetExists(t, stage), realZFSDatasetExists(t, target), realZFSDatasetExists(t, previous))
	}
	if got := zfsGetProperty(t, target, replicationPropertyState); got != replicationStateReady {
		t.Fatalf("promoted generation state=%q, want %q", got, replicationStateReady)
	}
	for _, dataset := range []string{target, target + "/new-child"} {
		if realZFSSnapshotExists(t, dataset, "manual_stage") {
			t.Fatalf("non-HA staged snapshot survived promotion: %s@manual_stage", dataset)
		}
		if !realZFSSnapshotExists(t, dataset, "ha_next") {
			t.Fatalf("HA staged snapshot was pruned: %s@ha_next", dataset)
		}
	}

	expectedCurrent := copyReplicationProperties(expectedStage)
	expectedCurrent[replicationPropertyState] = replicationStateReady
	rollback := buildRollbackPromotedReplicationScript(stage, target, previous, expectedCurrent, "7")
	if output, err := runReplicationZFSScript(t, rollback); err != nil {
		t.Fatalf("real ZFS rollback failed: %v\n%s", err, output)
	}
	if !realZFSDatasetExists(t, stage) || !realZFSDatasetExists(t, target) || realZFSDatasetExists(t, previous) {
		t.Fatalf("unexpected post-rollback topology: stage=%v target=%v previous=%v",
			realZFSDatasetExists(t, stage), realZFSDatasetExists(t, target), realZFSDatasetExists(t, previous))
	}
	if !realZFSDatasetExists(t, target+"/old-child") {
		t.Fatal("rollback did not restore the previous generation")
	}
	if !realZFSDatasetExists(t, stage+"/new-child") {
		t.Fatal("rollback did not retain the rejected generation for recovery")
	}
}

func TestColdStartDatabaseFailureFencesCanonicalGuestsRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS cold-start watchdog integration test in short mode")
	}

	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

	vmRoot := pool + "/sylve/virtual-machines/701"
	jailRoot := pool + "/sylve/jails/702"
	unrelated := pool + "/ordinary-data"
	backupLike := pool + "/backups/sylve/virtual-machines/703"
	suffixedGuest := pool + "/sylve/virtual-machines/704_backup"
	nestedNamespace := pool + "/tenant/sylve/jails/705"
	zfstest.EnsureDataset(t, client, vmRoot+"/disk")
	zfstest.EnsureDataset(t, client, jailRoot+"/root")
	zfstest.EnsureDataset(t, client, unrelated)
	zfstest.EnsureDataset(t, client, backupLike+"/disk")
	zfstest.EnsureDataset(t, client, suffixedGuest+"/disk")
	zfstest.EnsureDataset(t, client, nestedNamespace+"/root")

	db := testutil.NewSQLiteTestDB(t, &clusterModels.ReplicationPolicy{})
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open underlying test DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close underlying test DB: %v", err)
	}
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	svc := &Service{
		DB:      db,
		Cluster: &clusterService.Service{DB: db},
		GZFS:    client,
	}
	scopeLocalFilesystemDatasetsToPool(t, svc, pool)
	svc.replaceReplicationFenceCache(map[uint]replicationFenceObservation{
		77: {
			Policy: clusterModels.ReplicationPolicy{
				ID: 77, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 703,
				Enabled: true, ActiveNodeID: "node-a", SourceNodeID: "node-a", OwnerEpoch: 1,
			},
			LeaseOwner: "node-a", LeaseEpoch: 1, LeaseExpiresAt: time.Now().Add(-time.Minute),
		},
	})
	if err := svc.selfFenceExpiredLeases(t.Context()); err == nil {
		t.Fatal("closed policy database unexpectedly returned success")
	}

	for _, dataset := range []string{vmRoot, vmRoot + "/disk", jailRoot, jailRoot + "/root"} {
		if got := zfsGetProperty(t, dataset, "readonly"); got != "on" {
			t.Fatalf("cold-start watchdog left %s writable: readonly=%q", dataset, got)
		}
	}
	for _, dataset := range []string{unrelated, backupLike, backupLike + "/disk", suffixedGuest, suffixedGuest + "/disk", nestedNamespace, nestedNamespace + "/root"} {
		if got := zfsGetProperty(t, dataset, "readonly"); got != "off" {
			t.Fatalf("cold-start watchdog fenced noncanonical dataset %s: readonly=%q", dataset, got)
		}
	}
}

func TestColdStartEmergencyReadonlyRestoredAfterPolicyRecoveryRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS cold-start readonly recovery integration test in short mode")
	}

	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	unprotectedRoot := pool + "/sylve/virtual-machines/706"
	preexistingReadonlyRoot := pool + "/sylve/jails/707"
	zfstest.EnsureDataset(t, client, unprotectedRoot+"/disk")
	zfstest.EnsureDataset(t, client, preexistingReadonlyRoot+"/root")
	setRealZFSReadonly(t, preexistingReadonlyRoot)
	previousReadonly := map[string]string{}
	for _, dataset := range []string{unprotectedRoot, unprotectedRoot + "/disk", preexistingReadonlyRoot, preexistingReadonlyRoot + "/root"} {
		previousReadonly[dataset] = zfsGetPropertyWithSource(t, dataset, "readonly")
	}

	coldService := &Service{GZFS: client}
	scopeLocalFilesystemDatasetsToPool(t, coldService, pool)
	if err := coldService.selfFenceAllCanonicalGuests(t.Context(), "node-a"); err != nil {
		t.Fatalf("cold-start canonical fencing failed: %v", err)
	}
	for _, dataset := range []string{unprotectedRoot, unprotectedRoot + "/disk", preexistingReadonlyRoot, preexistingReadonlyRoot + "/root"} {
		if got := zfsGetProperty(t, dataset, "readonly"); got != "on" {
			t.Fatalf("emergency fence left %s writable: readonly=%q", dataset, got)
		}
	}
	changes, err := loadDurableReplicationEmergencyReadonlyChanges()
	if err != nil {
		t.Fatalf("load emergency readonly journal: %v", err)
	}
	if len(changes) != 4 {
		t.Fatalf("emergency readonly journal entries = %d, want every root and child", len(changes))
	}
	guestTokens := map[string]string{}
	for key, change := range changes {
		if change.DatasetGUID == "" || change.FenceToken == "" || change.Dataset == "" {
			t.Fatalf("journal entry lacks GUID/token/path: %s=%+v", key, change)
		}
		guestKey := replicationGuestKey(change.GuestType, change.GuestID)
		if existing := guestTokens[guestKey]; existing != "" && existing != change.FenceToken {
			t.Fatalf("guest journal has multiple fence tokens: %#v", guestTokens)
		}
		guestTokens[guestKey] = change.FenceToken
	}
	if guestTokens[replicationGuestKey(clusterModels.ReplicationGuestTypeVM, 706)] ==
		guestTokens[replicationGuestKey(clusterModels.ReplicationGuestTypeJail, 707)] {
		t.Fatal("different guests reused the same emergency fence token")
	}

	// Moving the dataset after the emergency fence exercises GUID-bound retry:
	// restoration must follow the original object and never trust the old path.
	movedRoot := pool + "/moved-vm-706"
	if output, renameErr := exec.Command("zfs", "rename", unprotectedRoot, movedRoot).CombinedOutput(); renameErr != nil {
		t.Fatalf("rename emergency-fenced dataset: %v\n%s", renameErr, output)
	}

	fx := SetupZeltaClusterFixture(t, 1)
	defer fx.Cleanup()
	vmFenceToken := guestTokens[replicationGuestKey(clusterModels.ReplicationGuestTypeVM, 706)]
	if err := fx.ClusterSvc.AcquireReplicationGuestOperation(clusterModels.ReplicationGuestOperationAcquire{
		GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 706,
		Operation: clusterModels.ReplicationGuestOperationEmergencyRestore,
		Token:     emergencyRestoreOperationToken(vmFenceToken), OwnerNodeID: fx.LocalNodeID,
	}, false); err != nil {
		t.Fatalf("seed crash-retry emergency restore guard: %v", err)
	}
	migrationToken := "migration:test:jail-707"
	if err := fx.ClusterSvc.AcquireReplicationGuestOperation(clusterModels.ReplicationGuestOperationAcquire{
		GuestType: clusterModels.ReplicationGuestTypeJail, GuestID: 707,
		Operation: clusterModels.ReplicationGuestOperationMigration,
		Token:     migrationToken, OwnerNodeID: fx.LocalNodeID, TargetNodeID: "node-target", TaskID: 707,
	}, false); err != nil {
		t.Fatalf("seed migration guard: %v", err)
	}
	if err := fx.ClusterSvc.SealReplicationGuestOperation(clusterModels.ReplicationGuestOperationTransition{
		GuestType: clusterModels.ReplicationGuestTypeJail, GuestID: 707,
		Operation: clusterModels.ReplicationGuestOperationMigration, Token: migrationToken,
	}, false); err != nil {
		t.Fatalf("seal migration guard: %v", err)
	}
	recoveredService := fx.NewZeltaService()
	recoveredService.GZFS = client
	scopeLocalFilesystemDatasetsToPool(t, recoveredService, pool)
	if err := recoveredService.selfFenceExpiredLeases(t.Context()); err != nil {
		t.Fatalf("first policy recovery pass failed: %v", err)
	}
	for _, dataset := range []string{movedRoot, movedRoot + "/disk"} {
		if got := zfsGetProperty(t, dataset, "readonly"); got != "off" {
			t.Fatalf("unprotected dataset %s remained readonly after recovery: %q", dataset, got)
		}
	}
	changes, err = loadDurableReplicationEmergencyReadonlyChanges()
	if err != nil {
		t.Fatalf("reload migration-deferred emergency journal: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("migration-deferred journal entries = %d, want jail root and child", len(changes))
	}
	for _, change := range changes {
		if change.GuestType != clusterModels.ReplicationGuestTypeJail || change.GuestID != 707 {
			t.Fatalf("restored guest remained in deferred journal: %+v", change)
		}
	}
	if err := fx.ClusterSvc.CompleteReplicationGuestOperation(clusterModels.ReplicationGuestOperationTransition{
		GuestType: clusterModels.ReplicationGuestTypeJail, GuestID: 707,
		Operation: clusterModels.ReplicationGuestOperationMigration,
		Token:     migrationToken, TargetNodeID: "node-target",
	}, false); err != nil {
		t.Fatalf("complete migration guard: %v", err)
	}
	if err := recoveredService.selfFenceExpiredLeases(t.Context()); err != nil {
		t.Fatalf("second policy recovery pass failed: %v", err)
	}
	for _, dataset := range []string{preexistingReadonlyRoot, preexistingReadonlyRoot + "/root"} {
		if got := zfsGetProperty(t, dataset, "readonly"); got != "on" {
			t.Fatalf("preexisting readonly state changed for %s: %q", dataset, got)
		}
	}
	changes, err = loadDurableReplicationEmergencyReadonlyChanges()
	if err != nil {
		t.Fatalf("reload emergency readonly journal: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("restored emergency readonly journal not cleared: %#v", changes)
	}
	for oldDataset, previous := range previousReadonly {
		dataset := oldDataset
		if oldDataset == unprotectedRoot || strings.HasPrefix(oldDataset, unprotectedRoot+"/") {
			dataset = movedRoot + strings.TrimPrefix(oldDataset, unprotectedRoot)
		}
		got := zfsGetPropertyWithSource(t, dataset, "readonly")
		gotFields, previousFields := strings.Fields(got), strings.Fields(previous)
		gotSource := strings.Join(gotFields[1:], " ")
		previousSource := strings.Join(previousFields[1:], " ")
		if gotFields[0] != previousFields[0] ||
			replicationZFSPropertySourceKind(gotSource) != replicationZFSPropertySourceKind(previousSource) {
			t.Fatalf("readonly provenance changed for %s: got %q want %q", dataset, got, previous)
		}
		if got := zfsGetProperty(t, dataset, replicationEmergencyReadonlyProp); got != "-" {
			t.Fatalf("emergency marker remained on %s: %q", dataset, got)
		}
	}
	var operationCount int64
	if err := fx.DB.Model(&clusterModels.ReplicationGuestOperation{}).Count(&operationCount).Error; err != nil {
		t.Fatalf("count emergency restore operations: %v", err)
	}
	if operationCount != 0 {
		t.Fatalf("crash-retry emergency restore guard remained: %d", operationCount)
	}
}

func TestVMReplicationSourceRejectsEnabledFilesystemStorageRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS VM source eligibility integration test in short mode")
	}

	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	managedRoot := pool + "/sylve/virtual-machines/703"
	zfstest.EnsureDataset(t, client, managedRoot)

	db := testutil.NewSQLiteTestDB(t,
		&clusterModels.BackupTarget{},
		&clusterModels.ReplicationPolicy{},
		&vmModels.VM{},
		&vmModels.Storage{},
		&vmModels.VMStorageDataset{},
		&vmModels.Network{},
		&vmModels.VMCPUPinning{},
	)
	vm := vmModels.VM{RID: 703, Name: "filesystem-eligibility"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("create VM: %v", err)
	}
	if err := db.Create(&vmModels.Storage{
		VMID: vm.ID, Name: "disk", Type: vmModels.VMStorageTypeZVol, Pool: pool, Enable: true,
	}).Error; err != nil {
		t.Fatalf("create managed storage: %v", err)
	}
	shareDataset := vmModels.VMStorageDataset{Pool: "zroot", Name: "zroot/9p"}
	if err := db.Create(&shareDataset).Error; err != nil {
		t.Fatalf("create share dataset metadata: %v", err)
	}
	share := vmModels.Storage{
		VMID: vm.ID, Name: "share", Type: vmModels.VMStorageTypeFilesystem,
		Pool: "zroot", Enable: true, DatasetID: &shareDataset.ID,
	}
	if err := db.Create(&share).Error; err != nil {
		t.Fatalf("create filesystem storage: %v", err)
	}

	driver := vmReplicationGuestDriver{service: &Service{DB: db, GZFS: client}}
	if _, err := driver.sourceDatasets(t.Context(), vm.RID); !errors.Is(err, errReplicationVMFilesystemStorageUnsupported) {
		t.Fatalf("enabled filesystem storage did not reject replication source: %v", err)
	}
	if err := db.Model(&share).Update("enable", false).Error; err != nil {
		t.Fatalf("disable filesystem storage: %v", err)
	}
	datasets, err := driver.sourceDatasets(t.Context(), vm.RID)
	if err != nil {
		t.Fatalf("disabled filesystem storage still blocked replication: %v", err)
	}
	if len(datasets) != 1 || datasets[0] != managedRoot {
		t.Fatalf("filesystem pool leaked into replication roots: %v", datasets)
	}
}

func TestRestoredVMFilesystemEligibilityCheckedWhileReadonlyRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS restored VM eligibility integration test in short mode")
	}

	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	svc := &Service{GZFS: client}

	writeMetadata := func(root string, rid uint, enabled bool) {
		zfstest.EnsureDataset(t, client, root)
		mountpoint := zfsGetProperty(t, root, "mountpoint")
		metaDir := filepath.Join(mountpoint, ".sylve")
		if err := os.MkdirAll(metaDir, 0755); err != nil {
			t.Fatalf("create metadata directory: %v", err)
		}
		metadata, err := json.Marshal(vmModels.VM{
			RID: rid,
			Storages: []vmModels.Storage{
				{Type: vmModels.VMStorageTypeFilesystem, Enable: enabled, Pool: pool},
			},
		})
		if err != nil {
			t.Fatalf("marshal VM metadata: %v", err)
		}
		if err := os.WriteFile(filepath.Join(metaDir, "vm.json"), metadata, 0644); err != nil {
			t.Fatalf("write VM metadata: %v", err)
		}
		setRealZFSReadonly(t, root)
	}

	enabledRoot := pool + "/sylve/virtual-machines/704"
	writeMetadata(enabledRoot, 704, true)
	if err := svc.requireRestoredVMReplicationStorageEligibility(t.Context(), enabledRoot, 704); !errors.Is(err, errReplicationVMFilesystemStorageUnsupported) {
		t.Fatalf("enabled restored filesystem share was accepted: %v", err)
	}
	if got := zfsGetProperty(t, enabledRoot, "readonly"); got != "on" {
		t.Fatalf("eligibility check made invalid standby writable: readonly=%q", got)
	}

	disabledRoot := pool + "/sylve/virtual-machines/705"
	writeMetadata(disabledRoot, 705, false)
	if err := svc.requireRestoredVMReplicationStorageEligibility(t.Context(), disabledRoot, 705); err != nil {
		t.Fatalf("disabled restored filesystem share was rejected: %v", err)
	}
	if got := zfsGetProperty(t, disabledRoot, "readonly"); got != "on" {
		t.Fatalf("eligibility check changed standby readonly state: %q", got)
	}
}

func copyReplicationProperties(source map[string]string) map[string]string {
	copy := make(map[string]string, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}
