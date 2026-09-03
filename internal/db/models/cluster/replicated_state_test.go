// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func TestReplicatedStateManifestCoversSnapshotAndExcludesLocalState(t *testing.T) {
	expectedTables := []string{
		"backup_job_operations",
		"backup_job_runner_rebind_items",
		"backup_job_runner_rebinds",
		"backup_jobs",
		"backup_target_node_readinesses",
		"backup_target_provision_operations",
		"backup_target_restore_operations",
		"backup_targets",
		"cluster_notes",
		"cluster_options",
		"cluster_ssh_identities",
		"encryption_keys",
		"guest_identity_claims",
		"guest_identity_enrollments",
		"guest_identity_registries",
		"replication_guest_operation_receipts",
		"replication_guest_operations",
		"replication_leases",
		"replication_policies",
		"replication_policy_targets",
		"replication_run_operations",
		"replication_transition_events",
		"scheduled_run_receipts",
	}
	manifest := ReplicatedStateManifest()
	actualTables := make([]string, 0, len(manifest))
	coveredFields := make(map[string]bool)
	for _, entry := range manifest {
		actualTables = append(actualTables, entry.Table)
		coveredFields[entry.SnapshotField] = true
	}
	sort.Strings(actualTables)
	if !reflect.DeepEqual(actualTables, expectedTables) {
		t.Fatalf("replicated tables = %#v, want %#v", actualTables, expectedTables)
	}

	snapshotType := reflect.TypeOf(ClusterSnapshot{})
	for index := 0; index < snapshotType.NumField(); index++ {
		field := snapshotType.Field(index)
		if field.Name == "ReplicationEvents" {
			continue
		}
		if !coveredFields[field.Name] {
			t.Fatalf("snapshot field %s is absent from replicated-state manifest", field.Name)
		}
	}

	localTables := map[string]bool{
		"clusters":                      true,
		"cluster_nodes":                 true,
		"backup_events":                 true,
		"replication_events":            true,
		"scheduled_run_result_outboxes": true,
	}
	for _, entry := range manifest {
		if localTables[entry.Table] {
			t.Fatalf("node-local table %s entered replicated-state manifest", entry.Table)
		}
	}
}

func TestReplicatedStateDigestIsIndependentOfInsertionOrder(t *testing.T) {
	left := testutil.NewSQLiteTestDB(t, allSnapshotModels()...)
	right := testutil.NewSQLiteTestDB(t, allSnapshotModels()...)
	now := time.Date(2026, time.July, 30, 1, 2, 3, 0, time.UTC)
	notes := []ClusterNote{
		{ID: 2, Title: "second", Content: "two", CreatedAt: now, UpdatedAt: now},
		{ID: 1, Title: "first", Content: "one", CreatedAt: now, UpdatedAt: now},
	}
	for _, note := range notes {
		if err := left.Create(&note).Error; err != nil {
			t.Fatalf("seed left note: %v", err)
		}
	}
	for index := len(notes) - 1; index >= 0; index-- {
		note := notes[index]
		if err := right.Create(&note).Error; err != nil {
			t.Fatalf("seed right note: %v", err)
		}
	}

	_, leftDigest, err := CaptureReplicatedStateDigest(left)
	if err != nil {
		t.Fatalf("capture left digest: %v", err)
	}
	_, rightDigest, err := CaptureReplicatedStateDigest(right)
	if err != nil {
		t.Fatalf("capture right digest: %v", err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("digests differ by insertion order: left=%s right=%s", leftDigest, rightDigest)
	}
}

func TestReplicatedStateDigestIgnoresSurrogateIDs(t *testing.T) {
	left := testutil.NewSQLiteTestDB(t, allSnapshotModels()...)
	right := testutil.NewSQLiteTestDB(t, allSnapshotModels()...)
	now := time.Date(2026, time.July, 30, 2, 3, 4, 0, time.UTC)

	leftIdentities := []ClusterSSHIdentity{
		{ID: 451, NodeUUID: "node-b", SSHUser: "root", SSHHost: "10.0.0.2", SSHPort: 8183, PublicKey: "key-b", CreatedAt: now, UpdatedAt: now},
		{ID: 5, NodeUUID: "node-a", SSHUser: "root", SSHHost: "10.0.0.1", SSHPort: 8183, PublicKey: "key-a", CreatedAt: now, UpdatedAt: now},
	}
	rightIdentities := []ClusterSSHIdentity{
		{ID: 9, NodeUUID: "node-b", SSHUser: "root", SSHHost: "10.0.0.2", SSHPort: 8183, PublicKey: "key-b", CreatedAt: now, UpdatedAt: now},
		{ID: 8, NodeUUID: "node-a", SSHUser: "root", SSHHost: "10.0.0.1", SSHPort: 8183, PublicKey: "key-a", CreatedAt: now, UpdatedAt: now},
	}
	policies := []ReplicationPolicy{
		{
			ID: 20, Name: "policy-a", GuestType: ReplicationGuestTypeVM, GuestID: 20,
			SourceNodeID: "node-a", ActiveNodeID: "node-a", OwnerEpoch: 1,
			SourceMode: ReplicationSourceModeFollowActive, FailbackMode: ReplicationFailbackManual,
			FailoverMode: ReplicationFailoverManual, CronExpr: "*/5 * * * *", Enabled: true,
			ProtectionState: ReplicationProtectionStateArmed, TransitionState: ReplicationTransitionStateNone,
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: 21, Name: "policy-b", GuestType: ReplicationGuestTypeJail, GuestID: 21,
			SourceNodeID: "node-b", ActiveNodeID: "node-b", OwnerEpoch: 1,
			SourceMode: ReplicationSourceModeFollowActive, FailbackMode: ReplicationFailbackManual,
			FailoverMode: ReplicationFailoverManual, CronExpr: "*/10 * * * *", Enabled: true,
			ProtectionState: ReplicationProtectionStateArmed, TransitionState: ReplicationTransitionStateNone,
			CreatedAt: now, UpdatedAt: now,
		},
	}
	leftTargets := []ReplicationPolicyTarget{
		{ID: 900, PolicyID: 20, NodeID: "node-b", Weight: 90, CreatedAt: now, UpdatedAt: now},
		{ID: 4, PolicyID: 20, NodeID: "node-a", Weight: 100, CreatedAt: now, UpdatedAt: now},
	}
	rightTargets := []ReplicationPolicyTarget{
		{ID: 5, PolicyID: 20, NodeID: "node-b", Weight: 90, CreatedAt: now, UpdatedAt: now},
		{ID: 800, PolicyID: 20, NodeID: "node-a", Weight: 100, CreatedAt: now, UpdatedAt: now},
	}
	leftLeases := []ReplicationLease{
		{ID: 901, PolicyID: 20, GuestType: ReplicationGuestTypeVM, GuestID: 20, OwnerNodeID: "node-a", OwnerEpoch: 1, ExpiresAt: now.Add(time.Hour), Version: 1, CreatedAt: now, UpdatedAt: now},
		{ID: 6, PolicyID: 21, GuestType: ReplicationGuestTypeJail, GuestID: 21, OwnerNodeID: "node-b", OwnerEpoch: 1, ExpiresAt: now.Add(time.Hour), Version: 1, CreatedAt: now, UpdatedAt: now},
	}
	rightLeases := []ReplicationLease{
		{ID: 7, PolicyID: 20, GuestType: ReplicationGuestTypeVM, GuestID: 20, OwnerNodeID: "node-a", OwnerEpoch: 1, ExpiresAt: now.Add(time.Hour), Version: 1, CreatedAt: now, UpdatedAt: now},
		{ID: 850, PolicyID: 21, GuestType: ReplicationGuestTypeJail, GuestID: 21, OwnerNodeID: "node-b", OwnerEpoch: 1, ExpiresAt: now.Add(time.Hour), Version: 1, CreatedAt: now, UpdatedAt: now},
	}
	leftKeys := []EncryptionKey{
		{ID: 902, UUID: "key-b", KeyData: "secret-b", KeyFormat: "passphrase", CreatedAt: now, UpdatedAt: now},
		{ID: 8, UUID: "key-a", KeyData: "secret-a", KeyFormat: "passphrase", CreatedAt: now, UpdatedAt: now},
	}
	rightKeys := []EncryptionKey{
		{ID: 9, UUID: "key-b", KeyData: "secret-b", KeyFormat: "passphrase", CreatedAt: now, UpdatedAt: now},
		{ID: 820, UUID: "key-a", KeyData: "secret-a", KeyFormat: "passphrase", CreatedAt: now, UpdatedAt: now},
	}
	seed := func(db *gorm.DB, identities []ClusterSSHIdentity, targets []ReplicationPolicyTarget, leases []ReplicationLease, keys []EncryptionKey) {
		t.Helper()
		for _, value := range []any{&policies, &identities, &targets, &leases, &keys} {
			if err := db.Create(value).Error; err != nil {
				t.Fatalf("seed %T: %v", value, err)
			}
		}
	}
	seed(left, leftIdentities, leftTargets, leftLeases, leftKeys)
	seed(right, rightIdentities, rightTargets, rightLeases, rightKeys)

	_, leftDigest, err := CaptureReplicatedStateDigest(left)
	if err != nil {
		t.Fatalf("capture left digest: %v", err)
	}
	_, rightDigest, err := CaptureReplicatedStateDigest(right)
	if err != nil {
		t.Fatalf("capture right digest: %v", err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("surrogate IDs changed digest: left=%s right=%s", leftDigest, rightDigest)
	}
}

func TestClearReplicatedStatePreservesNodeLocalRows(t *testing.T) {
	models := append(allSnapshotModels(),
		&Cluster{},
		&ClusterNode{},
		&BackupEvent{},
	)
	database := testutil.NewSQLiteTestDB(t, models...)
	now := time.Date(2026, time.July, 30, 4, 5, 6, 0, time.UTC)
	for _, value := range []any{
		&Cluster{ID: 1, Enabled: true, Key: "cluster-secret"},
		&ClusterNode{ID: 1, NodeUUID: "node-a"},
		&BackupEvent{ID: 1, Status: "success", StartedAt: now},
		&ReplicationEvent{ID: 1, Status: "success", StartedAt: now},
		&ScheduledRunResultOutbox{Token: "outbox-1", Kind: ScheduledRunKindBackup, ObjectID: 1, Payload: `{}`},
		&ClusterNote{ID: 1, Title: "replicated", Content: "state"},
		&EncryptionKey{UUID: "key-1", KeyData: "secret", KeyFormat: "passphrase"},
		&BackupJobRunnerRebind{
			Token: "rebind-1", Kind: BackupJobRunnerRebindKindMigration,
			GuestType: ReplicationGuestTypeVM, GuestID: 1,
			OldRunnerNodeID: "node-a", NewRunnerNodeID: "node-b",
			State: BackupJobRunnerRebindStatePlanned, Revision: 1,
		},
		&BackupJobRunnerRebindItem{
			OperationToken: "rebind-1", JobID: 1, ExpectedRunnerID: "node-a",
			ExpectedFingerprint: "fingerprint", State: BackupJobRunnerRebindItemPending, Revision: 1,
		},
		&ReplicationGuestOperationReceipt{
			Token: "guest-receipt-1", GuestType: ReplicationGuestTypeVM, GuestID: 1,
			Operation: "migration", OwnerNodeID: "node-a", TargetNodeID: "node-b",
			TaskID: 1, AcquiredAt: now, CompletedAt: now,
		},
	} {
		if err := database.Create(value).Error; err != nil {
			t.Fatalf("seed %T: %v", value, err)
		}
	}

	if err := database.Transaction(ClearReplicatedStateTx); err != nil {
		t.Fatalf("clear replicated state: %v", err)
	}
	for _, model := range []any{
		&ClusterNote{},
		&EncryptionKey{},
		&BackupJobRunnerRebind{},
		&BackupJobRunnerRebindItem{},
		&ReplicationGuestOperationReceipt{},
	} {
		var count int64
		if err := database.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("replicated %T count=%d err=%v, want zero", model, count, err)
		}
	}
	for _, model := range []any{
		&Cluster{},
		&ClusterNode{},
		&BackupEvent{},
		&ReplicationEvent{},
		&ScheduledRunResultOutbox{},
	} {
		var count int64
		if err := database.Model(model).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("local %T count=%d err=%v, want one", model, count, err)
		}
	}
}
