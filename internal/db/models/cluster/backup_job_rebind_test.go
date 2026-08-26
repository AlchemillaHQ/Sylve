// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/testutil"
)

func backupJobRunnerRebindModels() []any {
	return []any{
		&BackupTarget{}, &BackupJob{}, &BackupJobOperation{},
		&BackupJobRunnerRebind{}, &BackupJobRunnerRebindItem{},
		&ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{},
		&ReplicationGuestOperation{}, &ReplicationGuestOperationReceipt{},
	}
}

func TestBackupJobRunnerRebindDurableTerminalDecisions(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, backupJobRunnerRebindModels()...)
	if err := db.Create(&BackupTarget{
		ID: 1, Name: "target", SSHHost: "backup", BackupRoot: "tank/backups", Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	jobs := []BackupJob{
		{
			ID: 10, Name: "valid", TargetID: 1, RunnerNodeID: "node-a", Mode: BackupJobModeVM,
			SourceDataset: "tank/sylve/virtual-machines/77", DestSuffix: "virtual-machines/77/j-a/active",
			Recursive: true, CronExpr: "0 0 * * *", Enabled: true,
		},
		{
			ID: 11, Name: "legacy-invalid", TargetID: 1, RunnerNodeID: "node-a", Mode: BackupJobModeVM,
			SourceDataset: "tank/sylve/virtual-machines/77", DestSuffix: "virtual-machines/77/j-b/active",
			Recursive: false, CronExpr: "0 1 * * *", Enabled: true,
		},
	}
	if err := db.Create(&jobs).Error; err != nil {
		t.Fatalf("seed jobs: %v", err)
	}
	token := "migration:node-a:700"
	if err := db.Create(&ReplicationGuestOperation{
		GuestType: BackupJobModeVM, GuestID: 77, Operation: ReplicationGuestOperationMigration,
		State: ReplicationGuestOperationPreCutover, Token: token,
		OwnerNodeID: "node-a", TargetNodeID: "node-b", TaskID: 700, AcquiredAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("seed guest operation: %v", err)
	}
	plan := BackupJobRunnerRebindPlan{
		Token: token, Kind: BackupJobRunnerRebindKindMigration,
		GuestType: BackupJobModeVM, GuestID: 77, OldRunnerNodeID: "node-a", NewRunnerNodeID: "node-b",
		Items: []BackupJobRunnerRebindPlanItem{
			{JobID: jobs[0].ID, ExpectedRunnerID: "node-a", ExpectedFingerprint: BackupJobConfigurationFingerprint(&jobs[0])},
			{JobID: jobs[1].ID, ExpectedRunnerID: "node-a", ExpectedFingerprint: BackupJobConfigurationFingerprint(&jobs[1])},
		},
	}
	if err := PrepareBackupJobRunnerRebindTxn(db, &plan); err != nil {
		t.Fatalf("prepare plan: %v", err)
	}
	if err := PrepareBackupJobRunnerRebindTxn(db, &plan); err != nil {
		t.Fatalf("replay plan: %v", err)
	}
	sealedAt := time.Now().UTC()
	if err := SealReplicationGuestOperationTxn(db, &ReplicationGuestOperationTransition{
		GuestType: BackupJobModeVM, GuestID: 77, Operation: ReplicationGuestOperationMigration,
		Token: token, OccurredAt: sealedAt,
	}); err != nil {
		t.Fatalf("seal guest operation: %v", err)
	}
	if err := ReadyBackupJobRunnerRebindTxn(db, &BackupJobRunnerRebindReady{Token: token}); err != nil {
		t.Fatalf("ready plan: %v", err)
	}
	if err := CompleteReplicationGuestOperationTxn(db, &ReplicationGuestOperationTransition{
		GuestType: BackupJobModeVM, GuestID: 77, Operation: ReplicationGuestOperationMigration,
		Token: token, TargetNodeID: "node-b", OccurredAt: time.Now().UTC(),
	}); err == nil || !strings.Contains(err.Error(), "backup_job_runner_rebind_not_terminal") {
		t.Fatalf("pending rebind did not fence migration completion: %v", err)
	}
	if err := AcquireBackupJobOperationTxn(db, &BackupJobOperationAcquire{
		JobID: jobs[0].ID, Token: "backup:node-a:during-rebind", Operation: BackupJobOperationBackup,
		HolderNodeID: "node-a", AcquiredAt: time.Now().UTC(),
	}); err == nil || !strings.Contains(err.Error(), "backup_job_runner_rebind_pending") {
		t.Fatalf("ready rebind did not fence new job operation: %v", err)
	}

	previousFence, err := LoadBackupJobPlacementFence(db, BackupJobModeVM, 77, "node-a")
	if err != nil {
		t.Fatalf("previous fence: %v", err)
	}
	newFence, err := LoadBackupJobPlacementFence(db, BackupJobModeVM, 77, "node-b")
	if err != nil {
		t.Fatalf("new fence: %v", err)
	}
	if err := ApplyBackupJobRunnerRebindTxn(db, &BackupJobRunnerRebindApply{
		Token: token, JobID: jobs[0].ID, ExpectedFingerprint: plan.Items[0].ExpectedFingerprint,
		FriendlySource: "migrated-vm", PlacementFence: &newFence, PreviousPlacementFence: &previousFence,
	}); err != nil {
		t.Fatalf("apply valid item: %v", err)
	}
	if err := RepairBackupJobRunnerRebindTxn(db, &BackupJobRunnerRebindRepair{
		Token: token, JobID: jobs[1].ID, ExpectedFingerprint: plan.Items[1].ExpectedFingerprint,
		Reason: "vm_backup_requires_recursive",
	}); err != nil {
		t.Fatalf("repair invalid item: %v", err)
	}

	var operation BackupJobRunnerRebind
	if err := db.First(&operation, "token = ?", token).Error; err != nil {
		t.Fatalf("load rebind: %v", err)
	}
	if operation.State != BackupJobRunnerRebindStateCompletedWithRepairs {
		t.Fatalf("operation state = %q", operation.State)
	}
	if err := RequireBackupJobRunnerRebindTerminalTxn(db, token); err != nil {
		t.Fatalf("terminal plan rejected: %v", err)
	}
	if err := CompleteReplicationGuestOperationTxn(db, &ReplicationGuestOperationTransition{
		GuestType: BackupJobModeVM, GuestID: 77, Operation: ReplicationGuestOperationMigration,
		Token: token, TargetNodeID: "node-b", OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("terminal rebind did not permit migration completion: %v", err)
	}

	var valid, invalid BackupJob
	if err := db.First(&valid, jobs[0].ID).Error; err != nil {
		t.Fatalf("load valid job: %v", err)
	}
	if valid.RunnerNodeID != "node-b" || valid.FriendlySrc != "migrated-vm" || !valid.Enabled {
		t.Fatalf("valid job = %+v", valid)
	}
	if err := db.First(&invalid, jobs[1].ID).Error; err != nil {
		t.Fatalf("load invalid job: %v", err)
	}
	if invalid.RunnerNodeID != "node-b" || invalid.Enabled || invalid.NextRunAt != nil ||
		invalid.LastStatus != BackupJobRunnerRebindItemRepairRequired ||
		!strings.Contains(invalid.LastError, "requires_recursive") {
		t.Fatalf("repair-required job = %+v", invalid)
	}

	acquire := BackupJobOperationAcquire{
		JobID: invalid.ID, Token: "backup:node-b:blocked", Operation: BackupJobOperationBackup,
		HolderNodeID: "node-b", AcquiredAt: time.Now().UTC(),
	}
	if err := AcquireBackupJobOperationTxn(db, &acquire); err == nil ||
		!strings.Contains(err.Error(), "backup_job_repair_required") {
		t.Fatalf("repair-required operation acquire = %v", err)
	}
	if err := ClearBackupJobRepairRequiredTxn(db, invalid.ID); err != nil {
		t.Fatalf("clear repair state: %v", err)
	}
	if err := db.First(&operation, "token = ?", token).Error; err != nil {
		t.Fatalf("reload operation: %v", err)
	}
	if operation.State != BackupJobRunnerRebindStateCompleted {
		t.Fatalf("operation did not converge after repair: %+v", operation)
	}
}

func TestBackupJobRunnerRebindDeletionRaceIsTerminal(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, backupJobRunnerRebindModels()...)
	if err := db.Create(&BackupTarget{ID: 1, Name: "target"}).Error; err != nil {
		t.Fatal(err)
	}
	job := BackupJob{
		ID: 20, Name: "delete-me", TargetID: 1, RunnerNodeID: "node-a", Mode: BackupJobModeJail,
		JailRootDataset: "tank/sylve/jails/88", DestSuffix: "jails/88/j-k/active", Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	token := "migration:node-a:800"
	if err := db.Create(&ReplicationGuestOperation{
		GuestType: BackupJobModeJail, GuestID: 88, Operation: ReplicationGuestOperationMigration,
		State: ReplicationGuestOperationPreCutover, Token: token,
		OwnerNodeID: "node-a", TargetNodeID: "node-b", TaskID: 800, AcquiredAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	plan := BackupJobRunnerRebindPlan{
		Token: token, Kind: BackupJobRunnerRebindKindMigration,
		GuestType: BackupJobModeJail, GuestID: 88, OldRunnerNodeID: "node-a", NewRunnerNodeID: "node-b",
		Items: []BackupJobRunnerRebindPlanItem{{
			JobID: job.ID, ExpectedRunnerID: "node-a", ExpectedFingerprint: BackupJobConfigurationFingerprint(&job),
		}},
	}
	if err := PrepareBackupJobRunnerRebindTxn(db, &plan); err != nil {
		t.Fatal(err)
	}
	if err := DeleteBackupJobTxn(db, job.ID); err != nil {
		t.Fatalf("delete planned job: %v", err)
	}
	if err := SealReplicationGuestOperationTxn(db, &ReplicationGuestOperationTransition{
		GuestType: BackupJobModeJail, GuestID: 88, Operation: ReplicationGuestOperationMigration,
		Token: token, OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := ReadyBackupJobRunnerRebindTxn(db, &BackupJobRunnerRebindReady{Token: token}); err != nil {
		t.Fatal(err)
	}
	var item BackupJobRunnerRebindItem
	if err := db.Where("operation_token = ? AND job_id = ?", token, job.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.State != BackupJobRunnerRebindItemDeleted {
		t.Fatalf("item state = %q", item.State)
	}
	var operation BackupJobRunnerRebind
	if err := db.First(&operation, "token = ?", token).Error; err != nil {
		t.Fatal(err)
	}
	if operation.State != BackupJobRunnerRebindStateCompleted {
		t.Fatalf("operation state = %q", operation.State)
	}
}

func TestBackupJobRunnerRebindZeroJobsCompletesAndPreCutoverAbortIsDurable(t *testing.T) {
	t.Run("zero jobs", func(t *testing.T) {
		db := testutil.NewSQLiteTestDB(t, backupJobRunnerRebindModels()...)
		token := "migration:node-a:zero"
		if err := db.Create(&ReplicationGuestOperation{
			GuestType: BackupJobModeVM, GuestID: 55, Operation: ReplicationGuestOperationMigration,
			State: ReplicationGuestOperationPreCutover, Token: token,
			OwnerNodeID: "node-a", TargetNodeID: "node-b", TaskID: 55, AcquiredAt: time.Now().UTC(),
		}).Error; err != nil {
			t.Fatal(err)
		}
		plan := BackupJobRunnerRebindPlan{
			Token: token, Kind: BackupJobRunnerRebindKindMigration,
			GuestType: BackupJobModeVM, GuestID: 55, OldRunnerNodeID: "node-a", NewRunnerNodeID: "node-b",
		}
		if err := PrepareBackupJobRunnerRebindTxn(db, &plan); err != nil {
			t.Fatal(err)
		}
		if err := SealReplicationGuestOperationTxn(db, &ReplicationGuestOperationTransition{
			GuestType: BackupJobModeVM, GuestID: 55, Operation: ReplicationGuestOperationMigration,
			Token: token, OccurredAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		if err := ReadyBackupJobRunnerRebindTxn(db, &BackupJobRunnerRebindReady{Token: token}); err != nil {
			t.Fatal(err)
		}
		var operation BackupJobRunnerRebind
		if err := db.First(&operation, "token = ?", token).Error; err != nil {
			t.Fatal(err)
		}
		if operation.State != BackupJobRunnerRebindStateCompleted {
			t.Fatalf("zero-job operation state = %q", operation.State)
		}
	})

	t.Run("abort", func(t *testing.T) {
		db := testutil.NewSQLiteTestDB(t, backupJobRunnerRebindModels()...)
		token := "migration:node-a:abort"
		if err := db.Create(&ReplicationGuestOperation{
			GuestType: BackupJobModeJail, GuestID: 56, Operation: ReplicationGuestOperationMigration,
			State: ReplicationGuestOperationPreCutover, Token: token,
			OwnerNodeID: "node-a", TargetNodeID: "node-b", TaskID: 56, AcquiredAt: time.Now().UTC(),
		}).Error; err != nil {
			t.Fatal(err)
		}
		plan := BackupJobRunnerRebindPlan{
			Token: token, Kind: BackupJobRunnerRebindKindMigration,
			GuestType: BackupJobModeJail, GuestID: 56, OldRunnerNodeID: "node-a", NewRunnerNodeID: "node-b",
		}
		if err := PrepareBackupJobRunnerRebindTxn(db, &plan); err != nil {
			t.Fatal(err)
		}
		if err := AbortReplicationGuestOperationTxn(db, &ReplicationGuestOperationTransition{
			GuestType: BackupJobModeJail, GuestID: 56, Operation: ReplicationGuestOperationMigration, Token: token,
		}); err != nil {
			t.Fatal(err)
		}
		var operation BackupJobRunnerRebind
		if err := db.First(&operation, "token = ?", token).Error; err != nil {
			t.Fatal(err)
		}
		if operation.State != BackupJobRunnerRebindStateAborted {
			t.Fatalf("aborted operation state = %q", operation.State)
		}
	})
}

func TestBackupJobRunnerRebindObservedConfigurationChangeBecomesRepairRequired(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, backupJobRunnerRebindModels()...)
	if err := db.Create(&BackupTarget{ID: 1, Name: "target"}).Error; err != nil {
		t.Fatal(err)
	}
	job := BackupJob{
		ID: 25, Name: "before", TargetID: 1, RunnerNodeID: "node-a", Mode: BackupJobModeVM,
		SourceDataset: "tank/sylve/virtual-machines/89", Recursive: true, Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	token := "migration:node-a:changed"
	if err := db.Create(&ReplicationGuestOperation{
		GuestType: BackupJobModeVM, GuestID: 89, Operation: ReplicationGuestOperationMigration,
		State: ReplicationGuestOperationPreCutover, Token: token,
		OwnerNodeID: "node-a", TargetNodeID: "node-b", TaskID: 89, AcquiredAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	expected := BackupJobConfigurationFingerprint(&job)
	plan := BackupJobRunnerRebindPlan{
		Token: token, Kind: BackupJobRunnerRebindKindMigration,
		GuestType: BackupJobModeVM, GuestID: 89, OldRunnerNodeID: "node-a", NewRunnerNodeID: "node-b",
		Items: []BackupJobRunnerRebindPlanItem{{
			JobID: job.ID, ExpectedRunnerID: "node-a", ExpectedFingerprint: expected,
		}},
	}
	if err := PrepareBackupJobRunnerRebindTxn(db, &plan); err != nil {
		t.Fatal(err)
	}
	// Simulate an old/unfenced writer racing the new protocol.
	if err := db.Model(&BackupJob{}).Where("id = ?", job.ID).UpdateColumn("name", "changed").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&job, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	observed := BackupJobConfigurationFingerprint(&job)
	if err := SealReplicationGuestOperationTxn(db, &ReplicationGuestOperationTransition{
		GuestType: BackupJobModeVM, GuestID: 89, Operation: ReplicationGuestOperationMigration,
		Token: token, OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := ReadyBackupJobRunnerRebindTxn(db, &BackupJobRunnerRebindReady{Token: token}); err != nil {
		t.Fatal(err)
	}
	if err := RepairBackupJobRunnerRebindTxn(db, &BackupJobRunnerRebindRepair{
		Token: token, JobID: job.ID, ExpectedFingerprint: expected,
		ObservedFingerprint: observed, Reason: "backup_job_configuration_changed_during_rebind",
	}); err != nil {
		t.Fatalf("repair changed job: %v", err)
	}
	if err := db.First(&job, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if job.RunnerNodeID != "node-b" || job.Enabled || job.LastStatus != BackupJobRunnerRebindItemRepairRequired {
		t.Fatalf("changed job was not safely disabled: %+v", job)
	}
}

func TestFailoverOwnershipCutoverRequiresReplicatedRebindPlan(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, backupJobRunnerRebindModels()...)
	seedControlPlanePolicy(t, db)
	payload := ownershipCommitPayload()
	payload.BackupJobRunnerRebindToken = payload.ExpectedTransitionRunID
	if err := ApplyReplicationOwnershipTransitionTxn(db, &payload); err == nil {
		t.Fatal("ownership cutover without a rebind plan succeeded")
	}
	var policy ReplicationPolicy
	if err := db.First(&policy, 1).Error; err != nil {
		t.Fatal(err)
	}
	if policy.ActiveNodeID != "node-1" || policy.OwnerEpoch != 1 ||
		policy.TransitionState != ReplicationTransitionStateDemoting {
		t.Fatalf("cutover partially committed without plan: %+v", policy)
	}
	var lease ReplicationLease
	if err := db.Where("policy_id = ?", 1).First(&lease).Error; err != nil {
		t.Fatal(err)
	}
	if lease.OwnerNodeID != "node-1" || lease.OwnerEpoch != 1 {
		t.Fatalf("lease partially committed without plan: %+v", lease)
	}
}

func TestFailoverBackupJobRunnerRebindIsAtomicWithOwnershipAndRollback(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, backupJobRunnerRebindModels()...)
	seedControlPlanePolicy(t, db)
	if err := db.Create(&BackupTarget{ID: 1, Name: "backup"}).Error; err != nil {
		t.Fatal(err)
	}
	job := BackupJob{
		ID: 30, Name: "failover-job", TargetID: 1, RunnerNodeID: "node-1",
		Mode: BackupJobModeVM, SourceDataset: "tank/sylve/virtual-machines/100",
		Recursive: true, CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	plan := BackupJobRunnerRebindPlan{
		Token: "run-1", Kind: BackupJobRunnerRebindKindFailover,
		GuestType: BackupJobModeVM, GuestID: 100,
		OldRunnerNodeID: "node-1", NewRunnerNodeID: "node-2",
		Items: []BackupJobRunnerRebindPlanItem{{
			JobID: job.ID, ExpectedRunnerID: "node-1",
			ExpectedFingerprint: BackupJobConfigurationFingerprint(&job),
		}},
	}
	if err := PrepareBackupJobRunnerRebindTxn(db, &plan); err != nil {
		t.Fatalf("prepare failover plan: %v", err)
	}

	cutover := ownershipCommitPayload()
	cutover.BackupJobRunnerRebindToken = plan.Token
	if err := ApplyReplicationOwnershipTransitionTxn(db, &cutover); err != nil {
		t.Fatalf("atomic ownership/rebind cutover: %v", err)
	}
	var operation BackupJobRunnerRebind
	if err := db.First(&operation, "token = ?", plan.Token).Error; err != nil {
		t.Fatal(err)
	}
	if operation.State != BackupJobRunnerRebindStateReady {
		t.Fatalf("rebind was not made ready atomically: %+v", operation)
	}
	if err := db.First(&job, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if job.LastStatus != BackupJobRunnerRebindStatusPending {
		t.Fatalf("pending job was not exposed: %+v", job)
	}
	if err := AcquireBackupJobOperationTxn(db, &BackupJobOperationAcquire{
		JobID: job.ID, Token: "backup:node-1:stale", Operation: BackupJobOperationBackup,
		HolderNodeID: "node-1", AcquiredAt: time.Now().UTC(),
	}); err == nil || !strings.Contains(err.Error(), "backup_job_runner_rebind_pending") {
		t.Fatalf("old runner was not fenced after cutover: %v", err)
	}
	if err := ApplyBackupJobRunnerRebindTxn(db, &BackupJobRunnerRebindApply{
		Token: plan.Token, JobID: job.ID, ExpectedFingerprint: plan.Items[0].ExpectedFingerprint,
	}); err == nil || !strings.Contains(err.Error(), "transition_not_completed") {
		t.Fatalf("job decision was allowed before failover became terminal: %v", err)
	}

	var policy ReplicationPolicy
	if err := db.First(&policy, 1).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rollbackSource := "node-1"
	rollback := ReplicationOwnershipTransitionPayload{
		PolicyID: 1, ExpectedActiveNodeID: "node-2", ExpectedOwnerEpoch: 2,
		ExpectedTransitionRunID: "run-1", BackupJobRunnerRebindToken: "run-1",
		ActiveNodeID: "node-1", SourceNodeID: &rollbackSource, OwnerEpoch: 3,
		ReplaceTargets: true,
		Targets:        []ReplicationPolicyTarget{{NodeID: "node-2", Weight: 100}},
		Lease: ReplicationLease{
			PolicyID: 1, GuestType: ReplicationGuestTypeVM, GuestID: 100,
			OwnerNodeID: "node-1", OwnerEpoch: 3, Version: 30,
			ExpiresAt: now.Add(time.Hour), LastReason: "rollback", LastActor: "leader",
		},
		Transition: ReplicationPolicyTransition{
			State: ReplicationTransitionStateRollingBack, RunID: "run-1", Reason: "rollback",
			SourceNodeID: "node-2", TargetNodeID: "node-1", OwnerEpoch: 3,
			RequestedAt: policy.TransitionRequestedAt, DemotedAt: policy.TransitionDemotedAt,
			CatchupAt: policy.TransitionCatchupAt, OriginalRunning: policy.TransitionOriginalRunning,
			OriginalSourceNodeID: policy.TransitionOriginalSourceNodeID,
			AllowUnsafe:          policy.TransitionAllowUnsafe, MovePinnedSource: policy.TransitionMovePinnedSource,
			TriggerValidationRun: policy.TransitionTriggerValidationRun,
		},
		ProtectionState: ReplicationProtectionStateDegraded,
	}
	if err := ApplyReplicationOwnershipTransitionTxn(db, &rollback); err != nil {
		t.Fatalf("atomic ownership/rebind rollback: %v", err)
	}
	if err := db.First(&operation, "token = ?", plan.Token).Error; err != nil {
		t.Fatal(err)
	}
	if operation.State != BackupJobRunnerRebindStateAborted {
		t.Fatalf("rollback did not abort plan: %+v", operation)
	}
	if err := db.First(&job, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if job.RunnerNodeID != "node-1" || job.LastStatus != "" || job.LastError != "" {
		t.Fatalf("rollback left stale job state: %+v", job)
	}
}

func TestBackupJobRunnerRebindPlanRejectsIncompleteManifest(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, backupJobRunnerRebindModels()...)
	if err := db.Create(&BackupTarget{ID: 1, Name: "target"}).Error; err != nil {
		t.Fatal(err)
	}
	jobs := []BackupJob{
		{ID: 31, Name: "one", TargetID: 1, RunnerNodeID: "node-a", Mode: BackupJobModeVM, SourceDataset: "tank/sylve/virtual-machines/99"},
		{ID: 32, Name: "two", TargetID: 1, RunnerNodeID: "node-a", Mode: BackupJobModeVM, SourceDataset: "tank/sylve/virtual-machines/99"},
	}
	if err := db.Create(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	token := "migration:node-a:900"
	if err := db.Create(&ReplicationGuestOperation{
		GuestType: BackupJobModeVM, GuestID: 99, Operation: ReplicationGuestOperationMigration,
		State: ReplicationGuestOperationPreCutover, Token: token,
		OwnerNodeID: "node-a", TargetNodeID: "node-b", TaskID: 900, AcquiredAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	plan := BackupJobRunnerRebindPlan{
		Token: token, Kind: BackupJobRunnerRebindKindMigration,
		GuestType: BackupJobModeVM, GuestID: 99, OldRunnerNodeID: "node-a", NewRunnerNodeID: "node-b",
		Items: []BackupJobRunnerRebindPlanItem{{
			JobID: jobs[0].ID, ExpectedRunnerID: "node-a", ExpectedFingerprint: BackupJobConfigurationFingerprint(&jobs[0]),
		}},
	}
	if err := PrepareBackupJobRunnerRebindTxn(db, &plan); err == nil ||
		!strings.Contains(err.Error(), "plan_incomplete") {
		t.Fatalf("incomplete manifest error = %v", err)
	}
}
