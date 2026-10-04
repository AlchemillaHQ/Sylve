// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func backupTargetUpdateV2Command(existing, proposed BackupTarget, kind string) BackupTargetUpdateV2 {
	return BackupTargetUpdateV2{
		TargetID:            existing.ID,
		Kind:                kind,
		ExpectedFingerprint: BackupTargetConfigurationFingerprint(&existing),
		ProposedFingerprint: BackupTargetConfigurationFingerprint(&proposed),
		Name:                proposed.Name,
		Description:         proposed.Description,
		Enabled:             proposed.Enabled,
		SSHKey:              proposed.SSHKey,
	}
}

func TestBackupTargetConfigurationFingerprintIgnoresNodeLocalPath(t *testing.T) {
	left := BackupTarget{ID: 90, Name: "target", SSHHost: "root@backup", SSHKey: "key", SSHKeyPath: "/node-a/key", BackupRoot: "tank/backups", Enabled: true}
	right := left
	right.SSHKeyPath = "/node-b/key"
	right.SSHHostKey = testutil.SSHHostKey(t)
	right.SSHHostKeyRevision = 9
	right.SSHClusterNodeID = "execution-only"
	if BackupTargetConfigurationFingerprint(&left) != BackupTargetConfigurationFingerprint(&right) {
		t.Fatal("node-local path changed replicated configuration fingerprint")
	}
	right.SSHKey = "replacement"
	if BackupTargetConfigurationFingerprint(&left) == BackupTargetConfigurationFingerprint(&right) {
		t.Fatal("replacement key did not change configuration fingerprint")
	}
}

func newBackupHostTrustTestState(t *testing.T, trusted bool) (*gorm.DB, BackupTarget, BackupTargetSSHHostTrustChange) {
	t.Helper()
	database := newClusterModelTestDB(t, &BackupTarget{}, &BackupTargetSSHHostTrust{}, &BackupTargetNodeReadiness{},
		&BackupJob{}, &BackupJobOperation{}, &BackupTargetRestoreOperation{})
	target := BackupTarget{ID: 42, Name: "target", SSHHost: "root@backup", SSHPort: 2222, SSHKey: "login key",
		BackupRoot: "tank/backups", Enabled: true}
	if err := database.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	endpoint, err := BackupTargetSSHEndpointFingerprint(&target)
	if err != nil {
		t.Fatal(err)
	}
	change := BackupTargetSSHHostTrustChange{TargetID: target.ID, EndpointFingerprint: endpoint, OccurredAt: time.Now().UTC()}
	if trusted {
		change.PublicKey = testutil.SSHHostKey(t)
	}
	if err := ApplyBackupTargetSSHHostTrustTxn(database, "initialize_v1", &change); err != nil {
		t.Fatal(err)
	}
	return database, target, change
}

func TestBackupHostTrustInstallResetAndReplay(t *testing.T) {
	database, target, initialize := newBackupHostTrustTestState(t, false)
	fingerprint := BackupTargetConfigurationFingerprint(&target)
	install := initialize
	install.ExpectedRevision, install.PublicKey = 1, testutil.SSHHostKey(t)
	install.OccurredAt = initialize.OccurredAt.Add(time.Second)
	if err := ApplyBackupTargetSSHHostTrustTxn(database, "install_v1", &install); err != nil {
		t.Fatal(err)
	}
	if err := ApplyBackupTargetSSHHostTrustTxn(database, "install_v1", &install); err != nil {
		t.Fatalf("exact install replay: %v", err)
	}
	competitor := install
	competitor.OccurredAt = install.OccurredAt.Add(time.Second)
	if err := ApplyBackupTargetSSHHostTrustTxn(database, "install_v1", &competitor); err == nil {
		t.Fatal("competing installation with same key accepted")
	}
	competitor.PublicKey = testutil.SSHHostKey(t)
	if err := ApplyBackupTargetSSHHostTrustTxn(database, "install_v1", &competitor); err == nil {
		t.Fatal("competing different key accepted")
	}
	readyUntil := install.OccurredAt.Add(10 * time.Minute)
	readiness := BackupTargetNodeReadinessUpdate{TargetID: target.ID, NodeID: "node-a", HostKeyRevision: 2,
		TargetFingerprint: BackupTargetConnectivityFingerprint(&target), ValidationSucceeded: true,
		LastVerifiedAt: install.OccurredAt, ReadyUntil: &readyUntil}
	if err := ApplyBackupTargetNodeReadinessUpdateV2Txn(database, &readiness); err != nil {
		t.Fatal(err)
	}
	reset := initialize
	reset.ExpectedRevision, reset.PublicKey, reset.OccurredAt = 2, "", install.OccurredAt.Add(2*time.Second)
	if err := ApplyBackupTargetSSHHostTrustTxn(database, "reset_v1", &reset); err != nil {
		t.Fatal(err)
	}
	if err := ApplyBackupTargetSSHHostTrustTxn(database, "reset_v1", &reset); err != nil {
		t.Fatalf("exact reset replay: %v", err)
	}
	if err := ApplyBackupTargetNodeReadinessUpdateV2Txn(database, &readiness); err == nil {
		t.Fatal("delayed readiness revived old trust")
	}
	if err := ApplyBackupTargetSSHHostTrustTxn(database, "install_v1", &install); err == nil {
		t.Fatal("delayed enrollment accepted after reset")
	}
	var readinessCount int64
	if err := database.Model(&BackupTargetNodeReadiness{}).Count(&readinessCount).Error; err != nil || readinessCount != 0 {
		t.Fatalf("readiness count=%d err=%v", readinessCount, err)
	}
	trust, err := GetBackupTargetSSHHostTrust(database, &target)
	if err != nil || trust.Revision != 3 || trust.PublicKey != "" {
		t.Fatalf("reset trust=%+v err=%v", trust, err)
	}
	install.ExpectedRevision, install.OccurredAt = 3, reset.OccurredAt.Add(time.Second)
	if err := ApplyBackupTargetSSHHostTrustTxn(database, "install_v1", &install); err != nil {
		t.Fatal(err)
	}
	if err := ApplyBackupTargetSSHHostTrustTxn(database, "reset_v1", &reset); err == nil {
		t.Fatal("old reset erased replacement key")
	}
	install.ExpectedRevision = math.MaxUint64
	if err := ApplyBackupTargetSSHHostTrustTxn(database, "install_v1", &install); err == nil {
		t.Fatal("revision overflow accepted")
	}
	var stored BackupTarget
	if err := database.First(&stored, target.ID).Error; err != nil {
		t.Fatal(err)
	}
	if BackupTargetConfigurationFingerprint(&stored) != fingerprint {
		t.Fatal("host trust changed target configuration")
	}
}

func TestBackupHostTrustAdmissionOrdersResetAgainstOperations(t *testing.T) {
	for _, operation := range []string{"backup", "restore", "target restore"} {
		for _, resetFirst := range []bool{true, false} {
			t.Run(operation+" reset first="+strconv.FormatBool(resetFirst), func(t *testing.T) {
				database, target, reset := newBackupHostTrustTestState(t, true)
				reset.PublicKey, reset.ExpectedRevision = "", 1
				job := BackupJob{ID: 51, Name: "job", TargetID: target.ID, RunnerNodeID: "node-a", Mode: BackupJobModeDataset, CronExpr: "0 0 * * *"}
				if err := database.Create(&job).Error; err != nil {
					t.Fatal(err)
				}
				acquire := func() error {
					if operation == "target restore" {
						return AcquireBackupTargetRestoreOperationV2Txn(database, &BackupTargetRestoreOperationAcquire{
							Token: "restore-token", TargetID: target.ID, HolderNodeID: "node-a", DestinationDataset: "tank/restored",
							RequestPayload: "{}", AcquiredAt: reset.OccurredAt, HostKeyRevision: 1, RequireEnabledTarget: true})
					}
					return AcquireBackupJobOperationV2Txn(database, &BackupJobOperationAcquire{JobID: job.ID,
						Token: "operation-token", Operation: operation, HolderNodeID: "node-a", AcquiredAt: reset.OccurredAt,
						HostKeyRevision: 1, RequireEnabledTarget: true})
				}
				if resetFirst {
					if err := ApplyBackupTargetSSHHostTrustTxn(database, "reset_v1", &reset); err != nil {
						t.Fatal(err)
					}
					if err := acquire(); err == nil || !strings.Contains(err.Error(), "revision_conflict") {
						t.Fatalf("stale admission: %v", err)
					}
				} else {
					if err := acquire(); err != nil {
						t.Fatal(err)
					}
					if err := ApplyBackupTargetSSHHostTrustTxn(database, "reset_v1", &reset); err == nil || !strings.Contains(err.Error(), "reset_busy") {
						t.Fatalf("active reset: %v", err)
					}
				}
			})
		}
	}
}

func TestBackupHostTrustMissingAndEmptyState(t *testing.T) {
	database, target, _ := newBackupHostTrustTestState(t, false)
	if err := RequireBackupTargetHostKeyRevision(database, target.ID, 1, false); err != nil {
		t.Fatalf("authorized empty backup trust rejected: %v", err)
	}
	if err := RequireBackupTargetHostKeyRevision(database, target.ID, 1, true); err == nil || !strings.Contains(err.Error(), "unlearned") {
		t.Fatalf("unlearned restore admitted: %v", err)
	}
	if err := database.Delete(&BackupTargetSSHHostTrust{}, "target_id = ?", target.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := RequireBackupTargetHostKeyRevision(database, target.ID, 1, false); err == nil || !strings.Contains(err.Error(), "state_unavailable") {
		t.Fatalf("missing state admitted: %v", err)
	}
}

func TestBackupHostTrustUpgradeInitializationRunsOnce(t *testing.T) {
	database := newClusterModelTestDB(t, &BackupTarget{}, &BackupTargetSSHHostTrust{}, &ClusterOption{})
	options := ClusterOption{ID: 1, KeyboardLayout: "us"}
	if err := database.Create(&options).Error; err != nil {
		t.Fatal(err)
	}
	target := BackupTarget{ID: 71, Name: "legacy", SSHHost: "root@backup", BackupRoot: "tank/backups"}
	if err := database.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := InitializeBackupTargetSSHHostTrustTxn(database, now); err != nil {
		t.Fatal(err)
	}
	trust, err := GetBackupTargetSSHHostTrust(database, &target)
	if err != nil || trust.PublicKey != "" || trust.Revision != 1 {
		t.Fatalf("upgrade trust=%+v err=%v", trust, err)
	}
	if err := database.First(&options, 1).Error; err != nil || !options.SSHHostTrustInitialized || options.KeyboardLayout != "us" {
		t.Fatalf("upgrade options=%+v err=%v", options, err)
	}
	if err := upsertOption(database, &ClusterOption{ID: 1, KeyboardLayout: "de", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := database.Delete(&BackupTargetSSHHostTrust{}, "target_id = ?", target.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := InitializeBackupTargetSSHHostTrustTxn(database, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := GetBackupTargetSSHHostTrust(database, &target); err == nil {
		t.Fatal("lost trust was silently reinitialized")
	}
}

func TestBackupHostTrustCreateAtomicityAndHistoricalCommands(t *testing.T) {
	database := newClusterModelTestDB(t, &BackupTarget{}, &BackupTargetSSHHostTrust{}, &BackupJob{})
	target := BackupTarget{ID: 61, Name: "created", SSHHost: "root@backup", SSHKey: "login", BackupRoot: "tank/backups"}
	legacy := BackupTargetCreateV2{Target: BackupTargetToReplicationPayload(target), ProposedFingerprint: BackupTargetConfigurationFingerprint(&target)}
	if err := ApplyBackupTargetCreateV2Txn(database, &legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := GetBackupTargetSSHHostTrust(database, &target); err == nil {
		t.Fatal("historical create silently learned host trust")
	}
	target.ID, target.Name = 62, "new"
	command := BackupTargetCreateV3{BackupTargetCreateV2: BackupTargetCreateV2{
		Target: BackupTargetToReplicationPayload(target), ProposedFingerprint: BackupTargetConfigurationFingerprint(&target)}, HostKey: "invalid", OccurredAt: time.Now().UTC()}
	if err := ApplyBackupTargetCreateV3Txn(database, &command); err == nil {
		t.Fatal("invalid host key create accepted")
	}
	var count int64
	if err := database.Model(&BackupTarget{}).Where("id = ?", target.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed create left target count=%d err=%v", count, err)
	}
	command.HostKey = testutil.SSHHostKey(t)
	if err := ApplyBackupTargetCreateV3Txn(database, &command); err != nil {
		t.Fatal(err)
	}
	if err := ApplyBackupTargetCreateV3Txn(database, &command); err != nil {
		t.Fatalf("create replay: %v", err)
	}
	trust, err := GetBackupTargetSSHHostTrust(database, &target)
	if err != nil || trust.PublicKey != command.HostKey || trust.Revision != 1 {
		t.Fatalf("created trust=%+v err=%v", trust, err)
	}
	if err := DeleteBackupTargetTxn(database, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&BackupTargetSSHHostTrust{}).Where("target_id = ?", target.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("orphan trust count=%d err=%v", count, err)
	}
}

func TestBackupTargetUpdateV2Contracts(t *testing.T) {
	db := newClusterModelTestDB(t,
		&BackupTarget{}, &BackupTargetNodeReadiness{}, &BackupJob{},
		&BackupJobOperation{}, &BackupTargetRestoreOperation{},
	)
	target := BackupTarget{
		ID: 91, Name: "target", SSHHost: "root@backup", SSHPort: 22,
		SSHKey: "key-one", BackupRoot: "tank/backups", CreateBackupRoot: true,
		Description: "old", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}

	metadata := target
	metadata.Name = "renamed"
	metadata.Description = "new description"
	metadataCommand := backupTargetUpdateV2Command(target, metadata, BackupTargetUpdateKindMetadata)
	if err := ApplyBackupTargetUpdateV2Txn(db, &metadataCommand); err != nil {
		t.Fatalf("metadata update: %v", err)
	}
	if err := ApplyBackupTargetUpdateV2Txn(db, &metadataCommand); err != nil {
		t.Fatalf("metadata replay: %v", err)
	}

	disable := metadata
	disable.Enabled = false
	disableCommand := backupTargetUpdateV2Command(metadata, disable, BackupTargetUpdateKindDisable)
	if err := ApplyBackupTargetUpdateV2Txn(db, &disableCommand); err != nil {
		t.Fatalf("disable: %v", err)
	}

	if err := db.Create(&BackupTargetNodeReadiness{
		TargetID: target.ID, NodeID: "node-a",
		TargetFingerprint: BackupTargetConnectivityFingerprint(&disable), Revision: 1,
	}).Error; err != nil {
		t.Fatalf("seed readiness: %v", err)
	}
	rotation := disable
	rotation.SSHKey = "key-two"
	rotationCommand := backupTargetUpdateV2Command(disable, rotation, BackupTargetUpdateKindRotateKey)
	if err := ApplyBackupTargetUpdateV2Txn(db, &rotationCommand); err != nil {
		t.Fatalf("rotate key: %v", err)
	}
	var readinessCount int64
	if err := db.Model(&BackupTargetNodeReadiness{}).Where("target_id = ?", target.ID).Count(&readinessCount).Error; err != nil || readinessCount != 0 {
		t.Fatalf("rotation readiness count=%d err=%v", readinessCount, err)
	}
	if err := ApplyBackupTargetUpdateV2Txn(db, &rotationCommand); err != nil {
		t.Fatalf("rotate replay: %v", err)
	}

	enable := rotation
	enable.Enabled = true
	enableCommand := backupTargetUpdateV2Command(rotation, enable, BackupTargetUpdateKindEnable)
	if err := ApplyBackupTargetUpdateV2Txn(db, &enableCommand); err != nil {
		t.Fatalf("enable: %v", err)
	}

	var stored BackupTarget
	if err := db.First(&stored, target.ID).Error; err != nil {
		t.Fatalf("load target: %v", err)
	}
	if stored.Name != "renamed" || stored.Description != "new description" ||
		stored.SSHHost != target.SSHHost || stored.SSHPort != target.SSHPort ||
		stored.BackupRoot != target.BackupRoot || stored.CreateBackupRoot != target.CreateBackupRoot ||
		stored.SSHKey != "key-two" || !stored.Enabled || stored.SSHKeyPath != "" {
		t.Fatalf("stored target: %+v", stored)
	}
}

func TestBackupTargetUpdateV2RotationRequiresDisabledQuiescentTarget(t *testing.T) {
	db := newClusterModelTestDB(t,
		&BackupTarget{}, &BackupJob{}, &BackupJobOperation{}, &BackupTargetRestoreOperation{},
	)
	target := BackupTarget{ID: 92, Name: "target", SSHHost: "root@backup", SSHKey: "old", BackupRoot: "tank/backups", Enabled: true}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	proposed := target
	proposed.SSHKey = "new"
	proposed.Enabled = false
	command := backupTargetUpdateV2Command(target, proposed, BackupTargetUpdateKindRotateKey)
	if err := ApplyBackupTargetUpdateV2Txn(db, &command); err == nil ||
		!strings.Contains(err.Error(), "must_be_disabled") {
		t.Fatalf("enabled rotation error=%v", err)
	}

	if err := db.Model(&BackupTarget{}).Where("id = ?", target.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable target: %v", err)
	}
	target.Enabled = false
	proposed.Enabled = false
	command = backupTargetUpdateV2Command(target, proposed, BackupTargetUpdateKindRotateKey)
	job := BackupJob{ID: 501, Name: "job", TargetID: target.ID, Mode: BackupJobModeDataset, CronExpr: "0 0 * * *"}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	if err := db.Create(&BackupJobOperation{JobID: job.ID, Token: "token", Operation: BackupJobOperationBackup, State: BackupJobOperationRunning, HolderNodeID: "node", Revision: 1}).Error; err != nil {
		t.Fatalf("seed operation: %v", err)
	}
	if err := ApplyBackupTargetUpdateV2Txn(db, &command); err == nil ||
		!strings.Contains(err.Error(), "active_operations") {
		t.Fatalf("active rotation error=%v", err)
	}
	if err := db.Delete(&BackupJobOperation{}, job.ID).Error; err != nil {
		t.Fatalf("delete operation: %v", err)
	}
	if err := ApplyBackupTargetUpdateV2Txn(db, &command); err != nil {
		t.Fatalf("quiescent rotation: %v", err)
	}
}

func TestBackupTargetDeleteRequiresDisabledTarget(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupTarget{}, &BackupJob{})
	target := BackupTarget{ID: 94, Name: "target", SSHHost: "root@backup", BackupRoot: "tank/backups", Enabled: true}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := DeleteBackupTargetTxn(db, target.ID); err == nil || !strings.Contains(err.Error(), "must_be_disabled") {
		t.Fatalf("enabled delete error=%v", err)
	}
	if err := db.Model(&BackupTarget{}).Where("id = ?", target.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable target: %v", err)
	}
	if err := DeleteBackupTargetTxn(db, target.ID); err != nil {
		t.Fatalf("delete disabled target: %v", err)
	}
}

func TestBackupTargetUpdateV2RejectsStaleCommand(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupTarget{})
	target := BackupTarget{ID: 93, Name: "target", SSHHost: "root@backup", SSHKey: "key", BackupRoot: "tank/backups", Enabled: true}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	proposed := target
	proposed.Description = "proposed"
	command := backupTargetUpdateV2Command(target, proposed, BackupTargetUpdateKindMetadata)
	if err := db.Model(&BackupTarget{}).Where("id = ?", target.ID).Update("description", "concurrent").Error; err != nil {
		t.Fatalf("concurrent update: %v", err)
	}
	if err := ApplyBackupTargetUpdateV2Txn(db, &command); err == nil || !strings.Contains(err.Error(), "update_stale") {
		t.Fatalf("stale error=%v", err)
	}
}
