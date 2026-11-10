// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func TestReplicatedStateManifestCoversSnapshotAndExcludesLocalState(t *testing.T) {
	expectedTables := []string{
		"acl_grants",
		"acl_groups",
		"acl_roles",
		"auth_realms",
		"backup_job_operations",
		"backup_job_runner_rebind_items",
		"backup_job_runner_rebinds",
		"backup_jobs",
		"backup_target_node_readinesses",
		"backup_target_provision_operations",
		"backup_target_restore_operations",
		"backup_target_ssh_host_trusts",
		"backup_targets",
		"cluster_notes",
		"cluster_options",
		"cluster_ssh_identities",
		"encryption_keys",
		"guest_identity_claims",
		"oidc_users",
		"replication_guest_operation_receipts",
		"replication_guest_operations",
		"replication_leases",
		"replication_policies",
		"replication_policy_targets",
		"replication_run_operations",
		"replication_transition_events",
		"scheduled_run_receipts",
		"sylve_users",
	}
	manifest := ReplicatedStateManifest()
	actualTables := make([]string, 0, len(manifest))
	for _, entry := range manifest {
		actualTables = append(actualTables, entry.Table)
	}
	sort.Strings(actualTables)
	if !reflect.DeepEqual(actualTables, expectedTables) {
		t.Fatalf("replicated tables = %#v, want %#v", actualTables, expectedTables)
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

func TestSnapshotRestoreReproducesIDsAndSequences(t *testing.T) {
	source := testutil.NewSQLiteTestDB(t, allSnapshotModels()...)
	now := time.Date(2026, time.July, 30, 2, 3, 4, 0, time.FixedZone("source", 5*60*60))
	for _, uuid := range []string{"key-a", "key-b", "key-c"} {
		if err := source.Create(&EncryptionKey{UUID: uuid, KeyData: "secret", KeyFormat: "passphrase", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := source.Where("uuid = ?", "key-c").Delete(&EncryptionKey{}).Error; err != nil {
		t.Fatal(err)
	}
	snapshot, sourceDigest, err := CaptureReplicatedStateDigest(source)
	if err != nil {
		t.Fatal(err)
	}

	destination := testutil.NewSQLiteTestDB(t, allSnapshotModels()...)
	if err := NewFSMDispatcher(destination).Restore(snapshotReader(t, snapshot)); err != nil {
		t.Fatal(err)
	}
	_, destinationDigest, err := CaptureReplicatedStateDigest(destination)
	if err != nil {
		t.Fatal(err)
	}
	if sourceDigest != destinationDigest {
		t.Fatalf("digest after restore = %s, want %s", destinationDigest, sourceDigest)
	}

	for _, db := range []*gorm.DB{source, destination} {
		key := EncryptionKey{UUID: "key-d", KeyData: "secret", KeyFormat: "passphrase"}
		if err := db.Create(&key).Error; err != nil {
			t.Fatal(err)
		}
		if key.ID != 4 {
			t.Fatalf("next key ID = %d, want 4", key.ID)
		}
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

func TestClusterSnapshotDigestIgnoresAppliedIndex(t *testing.T) {
	left := testutil.NewSQLiteTestDB(t, allSnapshotModels()...)
	right := testutil.NewSQLiteTestDB(t, allSnapshotModels()...)
	now := time.Date(2026, time.September, 24, 5, 6, 7, 0, time.UTC)
	for _, database := range []*gorm.DB{left, right} {
		if err := database.Create(&ClusterNote{
			ID: 1, Title: "note", Content: "content", CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatalf("seed note: %v", err)
		}
	}

	leftSnapshot, _, err := CaptureReplicatedStateDigest(left)
	if err != nil {
		t.Fatalf("capture left snapshot: %v", err)
	}
	rightSnapshot, _, err := CaptureReplicatedStateDigest(right)
	if err != nil {
		t.Fatalf("capture right snapshot: %v", err)
	}
	leftSnapshot.AppliedIndex = 12
	rightSnapshot.AppliedIndex = 340

	leftDigest, err := ClusterSnapshotDigest(leftSnapshot)
	if err != nil {
		t.Fatalf("digest left snapshot: %v", err)
	}
	rightDigest, err := ClusterSnapshotDigest(rightSnapshot)
	if err != nil {
		t.Fatalf("digest right snapshot: %v", err)
	}
	if leftDigest != rightDigest {
		t.Fatalf(
			"applied index changed the digest: left=%s right=%s",
			leftDigest,
			rightDigest,
		)
	}
}

func TestClusterSnapshotDigestIsTheSameInEveryProcess(t *testing.T) {
	if mode := os.Getenv("SYLVE_DIGEST_PROBE"); mode != "" {
		db := newClusterModelTestDB(t)
		fsm := NewFSMDispatcher(db)
		at := time.Date(2026, time.November, 5, 10, 0, 0, 123456000, time.UTC)
		if err := db.Create(&ClusterNote{ID: 7, Title: "t", Content: "c", CreatedAt: at, UpdatedAt: at}).Error; err != nil {
			t.Fatal(err)
		}
		if mode == "after-persist" {
			snapshot, err := fsm.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if err := snapshot.Persist(&writerSnapSink{buf: &bytes.Buffer{}}); err != nil {
				t.Fatal(err)
			}
		}
		_, digest, err := CaptureReplicatedStateDigest(db)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("digest=" + digest)
		return
	}
	digestIn := func(mode string) string {
		t.Helper()
		probe := exec.Command(os.Args[0], "-test.run=^TestClusterSnapshotDigestIsTheSameInEveryProcess$", "-test.v")
		probe.Env = append(os.Environ(), "SYLVE_DIGEST_PROBE="+mode)
		output, err := probe.CombinedOutput()
		if err != nil {
			t.Fatalf("%s probe: %v\n%s", mode, err, output)
		}
		for _, line := range strings.Split(string(output), "\n") {
			if digest, ok := strings.CutPrefix(line, "digest="); ok {
				return digest
			}
		}
		t.Fatalf("%s probe printed no digest:\n%s", mode, output)
		return ""
	}
	if fresh, leader := digestIn("fresh"), digestIn("after-persist"); fresh != leader {
		t.Fatalf("digest depends on the process: fresh=%s after-persist=%s", fresh, leader)
	}
}

func TestClusterSnapshotIgnoresTheTableLayout(t *testing.T) {
	upgraded := testutil.NewSQLiteTestDB(t)
	if err := upgraded.Exec("CREATE TABLE `cluster_notes` (`updated_at` datetime,`retired` integer NOT NULL DEFAULT 0," +
		"`content` text,`title` text,`created_at` datetime,`id` integer PRIMARY KEY AUTOINCREMENT)").Error; err != nil {
		t.Fatal(err)
	}
	NewFSMDispatcher(upgraded)
	var firstColumn string
	if err := upgraded.Raw("SELECT name FROM pragma_table_info('cluster_notes') WHERE cid = 0").Scan(&firstColumn).Error; err != nil || firstColumn != "updated_at" {
		t.Fatalf("migration rebuilt the upgraded table: first column %q, %v", firstColumn, err)
	}
	fresh := newClusterModelTestDB(t)
	NewFSMDispatcher(fresh)

	at := time.Date(2026, time.November, 5, 10, 0, 0, 0, time.FixedZone("IST", 5*60*60+30*60))
	for _, db := range []*gorm.DB{upgraded, fresh} {
		if err := db.Create(&ClusterNote{ID: 1, Title: "t", Content: "c", CreatedAt: at, UpdatedAt: at}).Error; err != nil {
			t.Fatal(err)
		}
	}
	snapshot, upgradedDigest, err := CaptureReplicatedStateDigest(upgraded)
	if err != nil {
		t.Fatal(err)
	}
	_, freshDigest, err := CaptureReplicatedStateDigest(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if upgradedDigest != freshDigest {
		t.Fatalf("digest depends on the table layout: upgraded=%s fresh=%s", upgradedDigest, freshDigest)
	}

	restored := newClusterModelTestDB(t)
	if err := NewFSMDispatcher(restored).Restore(snapshotReader(t, snapshot)); err != nil {
		t.Fatalf("restore an upgraded node's snapshot on a fresh node: %v", err)
	}
	if _, restoredDigest, err := CaptureReplicatedStateDigest(restored); err != nil || restoredDigest != upgradedDigest {
		t.Fatalf("restored digest = %s, %v; want %s", restoredDigest, err, upgradedDigest)
	}
	var original, copied string
	if err := upgraded.Raw("SELECT CAST(created_at AS TEXT) FROM cluster_notes WHERE id = 1").Scan(&original).Error; err != nil {
		t.Fatal(err)
	}
	if err := restored.Raw("SELECT CAST(created_at AS TEXT) FROM cluster_notes WHERE id = 1").Scan(&copied).Error; err != nil {
		t.Fatal(err)
	}
	if copied != original {
		t.Fatalf("restore rewrote the stored time: %q, want %q", copied, original)
	}
}

func TestRestoreSnapshotTableKeepsBinaryValues(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t)
	if err := db.Exec("CREATE TABLE `blobs` (`data` blob,`id` integer PRIMARY KEY AUTOINCREMENT)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return restoreSnapshotTable(tx, SnapshotTable{
			Name: "blobs", Columns: []string{"data", "id"}, Rows: [][]any{{[]byte("ab"), int64(1)}},
		})
	}); err != nil {
		t.Fatal(err)
	}
	var data []byte
	if err := db.Raw("SELECT data FROM blobs WHERE id = 1").Row().Scan(&data); err != nil || string(data) != "ab" {
		t.Fatalf("restored blob = %q, %v", data, err)
	}
}

func TestDropRetiredReplicatedState(t *testing.T) {
	db := newClusterModelTestDB(t)
	for _, statement := range []string{
		"CREATE TABLE guest_identity_registries (id integer PRIMARY KEY)",
		"ALTER TABLE guest_identity_claims ADD COLUMN `token` text",
		"ALTER TABLE backup_target_node_readinesses ADD COLUMN `raft_applied_index` integer NOT NULL DEFAULT 0",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := dropRetiredReplicatedState(db); err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasTable("guest_identity_registries") ||
		db.Migrator().HasColumn(&GuestIdentityClaim{}, "token") ||
		db.Migrator().HasColumn(&BackupTargetNodeReadiness{}, "raft_applied_index") {
		t.Fatal("retired state survived")
	}
	if err := dropRetiredReplicatedState(db); err != nil {
		t.Fatalf("second run: %v", err)
	}
}

func TestLeavingKeepsAccessRulesAndJoiningClearsThem(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, allSnapshotModels()...)
	seed := func() {
		for _, value := range []any{
			&ACLGrant{Path: "/", SubjectType: ACLSubjectUser, Subject: "alice@sylve", Role: "Auditor", Propagate: true},
			&ACLRole{Name: "Operator", Privileges: []string{"Guest.PowerMgmt"}},
			&ClusterNote{Title: "replicated", Content: "state"},
		} {
			if err := database.Create(value).Error; err != nil {
				t.Fatalf("seed %T: %v", value, err)
			}
		}
	}
	count := func(model any) int64 {
		var n int64
		if err := database.Model(model).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}

	seed()
	if err := database.Transaction(ClearDeclusteredStateTx); err != nil {
		t.Fatal(err)
	}
	if count(&ACLGrant{}) != 1 || count(&ACLRole{}) != 1 || count(&ClusterNote{}) != 0 {
		t.Fatalf("after leaving: grants=%d roles=%d notes=%d", count(&ACLGrant{}), count(&ACLRole{}), count(&ClusterNote{}))
	}

	if err := database.Transaction(ClearReplicatedStateTx); err != nil {
		t.Fatal(err)
	}
	if count(&ACLGrant{}) != 0 || count(&ACLRole{}) != 0 {
		t.Fatalf("after joining: grants=%d roles=%d", count(&ACLGrant{}), count(&ACLRole{}))
	}
}
