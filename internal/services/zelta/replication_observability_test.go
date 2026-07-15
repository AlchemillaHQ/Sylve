// SPDX-License-Identifier: BSD-2-Clause

package zelta

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
)

func TestReplicationRunSummaryTargetNodeID(t *testing.T) {
	tests := []struct {
		name    string
		targets []clusterModels.ReplicationPolicyTarget
		local   string
		want    string
	}{
		{
			name:    "single target remains visible",
			targets: []clusterModels.ReplicationPolicyTarget{{NodeID: "hera"}},
			local:   "ares",
			want:    "hera",
		},
		{
			name: "fan out is a summary rather than the last target",
			targets: []clusterModels.ReplicationPolicyTarget{
				{NodeID: "hera", Weight: 200},
				{NodeID: "zeus", Weight: 100},
			},
			local: "ares",
			want:  "",
		},
		{
			name: "local node is not a replication destination",
			targets: []clusterModels.ReplicationPolicyTarget{
				{NodeID: "ares"},
				{NodeID: "hera"},
			},
			local: "ares",
			want:  "hera",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := replicationRunSummaryTargetNodeID(tt.targets, tt.local); got != tt.want {
				t.Fatalf("replicationRunSummaryTargetNodeID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReplicationRunOutcomeError(t *testing.T) {
	tests := []struct {
		name               string
		eligible           int
		transfers          int
		succeeded          int
		failed             []string
		offline            int
		missingIdentity    int
		wantErrorSubstring string
	}{
		{
			name:      "all targets succeeded",
			eligible:  2,
			transfers: 2,
			succeeded: 2,
		},
		{
			name:               "healthy target plus offline target is degraded",
			eligible:           1,
			transfers:          1,
			succeeded:          1,
			failed:             []string{"hera"},
			offline:            1,
			wantErrorSubstring: "replication_degraded:1_succeeded_1_failed",
		},
		{
			name:               "all unavailable targets fail the run",
			failed:             []string{"hera", "zeus"},
			offline:            1,
			missingIdentity:    1,
			wantErrorSubstring: "replication_all_targets_failed:2_targets",
		},
		{
			name:               "no configured eligible target fails the run",
			wantErrorSubstring: "no_eligible_replication_targets",
		},
		{
			name:               "eligible target without a transfer fails the run",
			eligible:           1,
			wantErrorSubstring: "no_replication_transfers_executed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := replicationRunOutcomeError(
				tt.eligible,
				tt.transfers,
				tt.succeeded,
				tt.failed,
				tt.offline,
				tt.missingIdentity,
			)
			if tt.wantErrorSubstring == "" {
				if err != nil {
					t.Fatalf("replicationRunOutcomeError() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrorSubstring) {
				t.Fatalf("replicationRunOutcomeError() error = %v, want substring %q", err, tt.wantErrorSubstring)
			}
		})
	}
}

func TestJoinReplicationRunCriticalErrorsPreservesRunAndFailClosedErrors(t *testing.T) {
	runErr := errors.New("replication_degraded:1_succeeded_1_failed")
	criticalErr := errors.Join(
		errReplicationTargetFallbackReadinessInvalidation,
		errors.New("raft_publish_failed"),
	)

	joined := joinReplicationRunCriticalErrors(runErr, []error{criticalErr})
	if !errors.Is(joined, runErr) {
		t.Fatalf("joined error lost run outcome: %v", joined)
	}
	if !errors.Is(joined, errReplicationTargetFallbackReadinessInvalidation) {
		t.Fatalf("joined error lost fail-closed error: %v", joined)
	}
}

func TestAppendReplicationTargetEventOutputLabelsEveryLine(t *testing.T) {
	database := newZeltaServiceTestDB(t, &clusterModels.ReplicationEvent{})
	service := newTestZeltaService(database)
	event := clusterModels.ReplicationEvent{
		EventType: "replication",
		Status:    replicationEventStatusRunning,
	}
	if err := database.Create(&event).Error; err != nil {
		t.Fatalf("create event: %v", err)
	}

	if err := service.AppendReplicationTargetEventOutput(
		event.ID,
		"hera",
		"first line\r\nsecond line\rthird line",
	); err != nil {
		t.Fatalf("append labelled output: %v", err)
	}

	var stored clusterModels.ReplicationEvent
	if err := database.First(&stored, event.ID).Error; err != nil {
		t.Fatalf("reload event: %v", err)
	}
	want := "[target=hera] first line\n[target=hera] second line\n[target=hera] third line\n"
	if stored.Output != want {
		t.Fatalf("stored output = %q, want %q", stored.Output, want)
	}
}

func TestInterruptOrphanedLocalReplicationEvents(t *testing.T) {
	database := newZeltaServiceTestDB(t, &clusterModels.ReplicationEvent{})
	service := newTestZeltaService(database)
	cutoff := time.Now().UTC()
	old := cutoff.Add(-time.Hour)
	recent := cutoff.Add(time.Second)

	localPolicyID := uint(1)
	remotePolicyID := uint(2)
	secondLocalPolicyID := uint(3)
	recentPolicyID := uint(4)
	terminalPolicyID := uint(5)
	events := []clusterModels.ReplicationEvent{
		{PolicyID: &localPolicyID, EventType: "replication", Status: replicationEventStatusRunning, SourceNodeID: "local", StartedAt: old, Output: "partial\n"},
		{PolicyID: &remotePolicyID, EventType: "replication", Status: replicationEventStatusRunning, SourceNodeID: "remote", StartedAt: old},
		{PolicyID: &secondLocalPolicyID, EventType: "replication", Status: replicationEventStatusRunning, SourceNodeID: "local", StartedAt: old},
		{PolicyID: &recentPolicyID, EventType: "replication", Status: replicationEventStatusRunning, SourceNodeID: "local", StartedAt: recent},
		{PolicyID: &localPolicyID, EventType: "failover", Status: replicationEventStatusPromoting, SourceNodeID: "local", StartedAt: old},
		{PolicyID: &terminalPolicyID, EventType: "replication", Status: replicationEventStatusSuccess, SourceNodeID: "local", StartedAt: old, CompletedAt: &cutoff},
	}
	for i := range events {
		if err := database.Create(&events[i]).Error; err != nil {
			t.Fatalf("create event %d: %v", i, err)
		}
	}
	if err := service.interruptOrphanedLocalReplicationEvents(cutoff, "local"); err != nil {
		t.Fatalf("interrupt orphaned events: %v", err)
	}

	var stored []clusterModels.ReplicationEvent
	if err := database.Order("id ASC").Find(&stored).Error; err != nil {
		t.Fatalf("load events: %v", err)
	}
	if stored[0].Status != replicationEventStatusInterrupted || stored[0].CompletedAt == nil ||
		stored[0].Error != "process_crashed_or_restarted" || stored[0].Output != "partial\n" {
		t.Fatalf("local orphan was not interrupted safely: %+v", stored[0])
	}
	if stored[1].Status != replicationEventStatusRunning {
		t.Fatalf("remote event was changed: %+v", stored[1])
	}
	if stored[2].Status != replicationEventStatusInterrupted {
		t.Fatalf("second local orphan was not interrupted: %+v", stored[2])
	}
	if stored[3].Status != replicationEventStatusRunning {
		t.Fatalf("post-cutoff event was changed: %+v", stored[3])
	}
	if stored[4].Status != replicationEventStatusPromoting {
		t.Fatalf("failover event was changed: %+v", stored[4])
	}
	if stored[5].Status != replicationEventStatusSuccess {
		t.Fatalf("terminal event was changed: %+v", stored[5])
	}
}

func TestInterruptOrphanedLocalReplicationEventsRequiresNodeIdentity(t *testing.T) {
	database := newZeltaServiceTestDB(t, &clusterModels.ReplicationEvent{})
	service := newTestZeltaService(database)
	policyID := uint(1)
	event := clusterModels.ReplicationEvent{
		PolicyID: &policyID, EventType: "replication", Status: replicationEventStatusRunning,
		SourceNodeID: "local", StartedAt: time.Now().UTC().Add(-time.Hour),
	}
	if err := database.Create(&event).Error; err != nil {
		t.Fatalf("create event: %v", err)
	}

	if err := service.interruptOrphanedLocalReplicationEvents(time.Now().UTC(), ""); err != nil {
		t.Fatalf("blank-node cleanup: %v", err)
	}
	if err := database.First(&event, event.ID).Error; err != nil {
		t.Fatalf("reload event: %v", err)
	}
	if event.Status != replicationEventStatusRunning {
		t.Fatalf("blank node identity changed event: %+v", event)
	}
}

func TestInterruptOrphanedLocalReplicationEventsFinalizesAudit(t *testing.T) {
	database := newZeltaServiceTestDB(t, &clusterModels.ReplicationEvent{})
	telemetry := newZeltaServiceTestDB(t, &infoModels.AuditRecord{})
	service := newTestZeltaService(database)
	service.TelemetryDB = telemetry
	policyID := uint(6)
	event := clusterModels.ReplicationEvent{
		PolicyID: &policyID, EventType: "replication", Status: replicationEventStatusRunning,
		SourceNodeID: "local", StartedAt: time.Now().UTC().Add(-time.Hour),
	}
	if err := database.Create(&event).Error; err != nil {
		t.Fatalf("create event: %v", err)
	}
	audit := infoModels.AuditRecord{
		AsyncJobID: &policyID, AsyncJobType: "replication_policy_run", Status: "pending", Action: "{}",
	}
	if err := telemetry.Create(&audit).Error; err != nil {
		t.Fatalf("create audit: %v", err)
	}

	if err := service.interruptOrphanedLocalReplicationEvents(time.Now().UTC(), "local"); err != nil {
		t.Fatalf("interrupt orphaned events: %v", err)
	}
	if err := telemetry.First(&audit, audit.ID).Error; err != nil {
		t.Fatalf("reload audit: %v", err)
	}
	if audit.Status != "failed" || audit.Error != "process_crashed_or_restarted" {
		t.Fatalf("orphan audit was not finalized: %+v", audit)
	}

	retryAudit := infoModels.AuditRecord{
		AsyncJobID: &policyID, AsyncJobType: "replication_policy_run", Status: "pending", Action: "{}",
	}
	if err := telemetry.Create(&retryAudit).Error; err != nil {
		t.Fatalf("create retry audit: %v", err)
	}
	if err := service.interruptOrphanedLocalReplicationEvents(time.Now().UTC(), "local"); err != nil {
		t.Fatalf("retry interrupted audit reconciliation: %v", err)
	}
	if err := telemetry.First(&retryAudit, retryAudit.ID).Error; err != nil {
		t.Fatalf("reload retry audit: %v", err)
	}
	if retryAudit.Status != "failed" || retryAudit.Error != "process_crashed_or_restarted" {
		t.Fatalf("previously interrupted audit was not retried: %+v", retryAudit)
	}

	cutoff := time.Now().UTC()
	newAudit := infoModels.AuditRecord{
		AsyncJobID: &policyID, AsyncJobType: "replication_policy_run", Status: "pending", Action: "{}",
	}
	if err := telemetry.Create(&newAudit).Error; err != nil {
		t.Fatalf("create new-run audit: %v", err)
	}
	if err := telemetry.Model(&newAudit).UpdateColumn("created_at", cutoff.Add(time.Second)).Error; err != nil {
		t.Fatalf("move new-run audit after cutoff: %v", err)
	}
	if err := service.interruptOrphanedLocalReplicationEvents(cutoff, "local"); err != nil {
		t.Fatalf("reconcile with newer pending audit: %v", err)
	}
	if err := telemetry.First(&newAudit, newAudit.ID).Error; err != nil {
		t.Fatalf("reload new-run audit: %v", err)
	}
	if newAudit.Status != "pending" {
		t.Fatalf("newer audit was finalized by an older interrupted event: %+v", newAudit)
	}
}

func TestReconcileReplicationTransitionEventFinalizesRecoveredPromotion(t *testing.T) {
	clusterSvc, _, cleanup := setupRaftClusterService(t)
	defer cleanup()
	service := newTestZeltaService(clusterSvc.DB)
	service.Cluster = clusterSvc
	requestedAt := time.Now().UTC().Add(-time.Minute)
	completedAt := requestedAt.Add(30 * time.Second)
	policyID := uint(7001)
	policy := &clusterModels.ReplicationPolicy{
		ID: policyID, Name: "recovered", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 107,
		TransitionState: clusterModels.ReplicationTransitionStateCompleted,
		TransitionRunID: "transition-completed", TransitionReason: "manual_failover",
		TransitionRequestedAt: &requestedAt, TransitionCompletedAt: &completedAt,
	}
	eventID, err := clusterSvc.CreateOrUpdateReplicationEvent(clusterModels.ReplicationEvent{
		PolicyID: &policyID, TransitionRunID: policy.TransitionRunID,
		EventType: "failover", Status: replicationEventStatusPromoting,
		SourceNodeID: "node-a", TargetNodeID: "node-b", StartedAt: requestedAt,
	}, false)
	if err != nil {
		t.Fatalf("create transition event: %v", err)
	}
	if err := clusterSvc.DB.Create(policy).Error; err != nil {
		t.Fatalf("create terminal policy: %v", err)
	}

	if err := service.runTransitionRecoveryTick(context.Background()); err != nil {
		t.Fatalf("run transition recovery tick: %v", err)
	}
	var stored clusterModels.ReplicationEvent
	if err := clusterSvc.DB.First(&stored, eventID).Error; err != nil {
		t.Fatalf("load transition event: %v", err)
	}
	if stored.Status != replicationEventStatusActive || stored.CompletedAt == nil ||
		!stored.CompletedAt.Equal(completedAt) || stored.Error != "" {
		t.Fatalf("recovered promotion event was not finalized: %+v", stored)
	}
}

func TestReconcileReplicationTransitionEventBackfillsLegacyRollback(t *testing.T) {
	clusterSvc, _, cleanup := setupRaftClusterService(t)
	defer cleanup()
	service := newTestZeltaService(clusterSvc.DB)
	service.Cluster = clusterSvc
	requestedAt := time.Now().UTC().Add(-time.Minute)
	completedAt := requestedAt.Add(45 * time.Second)
	policyID := uint(7002)
	legacy := clusterModels.ReplicationEvent{
		PolicyID: &policyID, EventType: "failover", Status: replicationEventStatusPromoting,
		Error: "activation_failed", SourceNodeID: "node-a", TargetNodeID: "node-b", StartedAt: requestedAt,
	}
	if err := clusterSvc.DB.Create(&legacy).Error; err != nil {
		t.Fatalf("create legacy event: %v", err)
	}
	policy := &clusterModels.ReplicationPolicy{
		ID: policyID, GuestType: clusterModels.ReplicationGuestTypeJail, GuestID: 108,
		TransitionState: clusterModels.ReplicationTransitionStateFailed,
		TransitionRunID: "transition-rollback", TransitionReason: "node_down_auto_force_recovery_rollback",
		TransitionRequestedAt: &requestedAt, TransitionCompletedAt: &completedAt,
		TransitionError: "failover_failed_rollback_succeeded",
	}

	if err := service.reconcileReplicationTransitionEvent(policy, nil); err != nil {
		t.Fatalf("reconcile legacy rollback event: %v", err)
	}
	var stored clusterModels.ReplicationEvent
	if err := clusterSvc.DB.First(&stored, legacy.ID).Error; err != nil {
		t.Fatalf("load legacy event: %v", err)
	}
	if stored.Status != replicationEventStatusFailed || stored.CompletedAt == nil ||
		stored.TransitionRunID != policy.TransitionRunID || stored.Message != policy.TransitionError ||
		!strings.Contains(stored.Error, "activation_failed") || !strings.Contains(stored.Error, policy.TransitionError) {
		t.Fatalf("legacy rollback event was not reconciled: %+v", stored)
	}
}

func TestReconcileReplicationTransitionEventRecreatesRecentMissingEvent(t *testing.T) {
	clusterSvc, _, cleanup := setupRaftClusterService(t)
	defer cleanup()
	service := newTestZeltaService(clusterSvc.DB)
	service.Cluster = clusterSvc
	requestedAt := time.Now().UTC().Add(-time.Minute)
	completedAt := time.Now().UTC()
	policy := &clusterModels.ReplicationPolicy{
		ID: 7008, GuestType: clusterModels.ReplicationGuestTypeJail, GuestID: 110,
		TransitionState: clusterModels.ReplicationTransitionStateFailed,
		TransitionRunID: "transition-missing", TransitionReason: "manual_failover_rollback",
		TransitionSourceNodeID: "node-b", TransitionTargetNodeID: "node-a",
		TransitionRequestedAt: &requestedAt, TransitionCompletedAt: &completedAt,
		TransitionError: "failover_failed_rollback_succeeded",
	}

	if err := service.reconcileReplicationTransitionEvent(policy, nil); err != nil {
		t.Fatalf("recreate missing transition event: %v", err)
	}
	var stored clusterModels.ReplicationEvent
	if err := clusterSvc.DB.Where("transition_run_id = ?", policy.TransitionRunID).First(&stored).Error; err != nil {
		t.Fatalf("load recreated event: %v", err)
	}
	if stored.Status != replicationEventStatusFailed || stored.CompletedAt == nil ||
		stored.SourceNodeID != "node-a" || stored.TargetNodeID != "node-b" {
		t.Fatalf("missing terminal event was not recreated safely: %+v", stored)
	}
	if err := service.reconcileReplicationTransitionEvent(policy, nil); err != nil {
		t.Fatalf("repeat missing-event reconciliation: %v", err)
	}
	var count int64
	if err := clusterSvc.DB.Model(&clusterModels.ReplicationEvent{}).
		Where("transition_run_id = ?", policy.TransitionRunID).Count(&count).Error; err != nil {
		t.Fatalf("count recreated events: %v", err)
	}
	if count != 1 {
		t.Fatalf("terminal event was recreated more than once: %d", count)
	}
}

func TestReconcileReplicationTransitionEventDoesNotDuplicateCompletedLegacyEvent(t *testing.T) {
	clusterSvc, _, cleanup := setupRaftClusterService(t)
	defer cleanup()
	service := newTestZeltaService(clusterSvc.DB)
	service.Cluster = clusterSvc
	requestedAt := time.Now().UTC().Add(-time.Minute)
	completedAt := time.Now().UTC()
	policyID := uint(7011)
	legacy := clusterModels.ReplicationEvent{
		PolicyID: &policyID, EventType: "failover", Status: replicationEventStatusActive,
		SourceNodeID: "node-a", TargetNodeID: "node-b", StartedAt: requestedAt.Add(time.Second),
		CompletedAt: &completedAt,
	}
	if err := clusterSvc.DB.Create(&legacy).Error; err != nil {
		t.Fatalf("create completed legacy event: %v", err)
	}
	policy := &clusterModels.ReplicationPolicy{
		ID: policyID, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 113,
		TransitionState: clusterModels.ReplicationTransitionStateCompleted,
		TransitionRunID: "transition-completed-legacy", TransitionReason: "manual_failover",
		TransitionSourceNodeID: "node-a", TransitionTargetNodeID: "node-b",
		TransitionRequestedAt: &requestedAt, TransitionCompletedAt: &completedAt,
	}

	if err := service.reconcileReplicationTransitionEvent(policy, nil); err != nil {
		t.Fatalf("reconcile completed legacy transition: %v", err)
	}
	var count int64
	if err := clusterSvc.DB.Model(&clusterModels.ReplicationEvent{}).Where("policy_id = ?", policyID).Count(&count).Error; err != nil {
		t.Fatalf("count completed legacy events: %v", err)
	}
	if count != 1 {
		t.Fatalf("completed legacy event was duplicated: %d", count)
	}
}

func TestReconcileReplicationTransitionEventDoesNotRecreateExpiredHistory(t *testing.T) {
	clusterSvc, _, cleanup := setupRaftClusterService(t)
	defer cleanup()
	service := newTestZeltaService(clusterSvc.DB)
	service.Cluster = clusterSvc
	requestedAt := time.Now().UTC().Add(-2 * replicationTerminalEventRecreateWindow)
	completedAt := requestedAt.Add(time.Minute)
	policy := &clusterModels.ReplicationPolicy{
		ID: 7009, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 111,
		TransitionState: clusterModels.ReplicationTransitionStateCompleted,
		TransitionRunID: "transition-expired", TransitionReason: "manual_failover",
		TransitionSourceNodeID: "node-a", TransitionTargetNodeID: "node-b",
		TransitionRequestedAt: &requestedAt, TransitionCompletedAt: &completedAt,
	}

	if err := service.reconcileReplicationTransitionEvent(policy, nil); err != nil {
		t.Fatalf("reconcile expired transition history: %v", err)
	}
	var count int64
	if err := clusterSvc.DB.Model(&clusterModels.ReplicationEvent{}).
		Where("transition_run_id = ?", policy.TransitionRunID).Count(&count).Error; err != nil {
		t.Fatalf("count expired transition events: %v", err)
	}
	if count != 0 {
		t.Fatalf("expired transition event was recreated: %d", count)
	}
}

func TestReconcileReplicationTransitionEventDoesNotCreateMigrationFailoverEvent(t *testing.T) {
	clusterSvc, _, cleanup := setupRaftClusterService(t)
	defer cleanup()
	service := newTestZeltaService(clusterSvc.DB)
	service.Cluster = clusterSvc
	requestedAt := time.Now().UTC().Add(-time.Minute)
	completedAt := time.Now().UTC()
	policy := &clusterModels.ReplicationPolicy{
		ID: 7010, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 112,
		TransitionState: clusterModels.ReplicationTransitionStateCompleted,
		TransitionRunID: "migration-run", TransitionReason: "manual_migration_ownership",
		TransitionSourceNodeID: "node-a", TransitionTargetNodeID: "node-b",
		TransitionRequestedAt: &requestedAt, TransitionCompletedAt: &completedAt,
	}

	if err := service.reconcileReplicationTransitionEvent(policy, nil); err != nil {
		t.Fatalf("reconcile migration transition: %v", err)
	}
	var count int64
	if err := clusterSvc.DB.Model(&clusterModels.ReplicationEvent{}).Count(&count).Error; err != nil {
		t.Fatalf("count migration events: %v", err)
	}
	if count != 0 {
		t.Fatalf("migration synthesized a failover event: %d", count)
	}
}

func TestEnsureReplicationTransitionEventIsIdempotent(t *testing.T) {
	clusterSvc, _, cleanup := setupRaftClusterService(t)
	defer cleanup()
	service := newTestZeltaService(clusterSvc.DB)
	service.Cluster = clusterSvc
	startedAt := time.Now().UTC()
	policy := &clusterModels.ReplicationPolicy{
		ID: 7003, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 109,
	}

	first, err := service.ensureReplicationTransitionEvent(
		policy, "transition-idempotent", startedAt, "node-a", "node-b",
		replicationEventStatusDemoting, "manual_failover_demoting",
	)
	if err != nil {
		t.Fatalf("ensure first event: %v", err)
	}
	second, err := service.ensureReplicationTransitionEvent(
		policy, "transition-idempotent", startedAt, "node-a", "node-b",
		replicationEventStatusDemoting, "manual_failover_demoting",
	)
	if err != nil {
		t.Fatalf("ensure second event: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("ensure created duplicate events: first=%d second=%d", first.ID, second.ID)
	}
	var count int64
	if err := clusterSvc.DB.Model(&clusterModels.ReplicationEvent{}).
		Where("transition_run_id = ?", "transition-idempotent").Count(&count).Error; err != nil {
		t.Fatalf("count transition events: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one transition event, got %d", count)
	}
}

func TestFindReplicationTransitionEventDoesNotHijackLegacyEvent(t *testing.T) {
	database := newZeltaServiceTestDB(t, &clusterModels.ReplicationEvent{})
	service := newTestZeltaService(database)
	policyID := uint(7004)
	startedAt := time.Now().UTC().Add(-time.Hour)
	legacy := clusterModels.ReplicationEvent{
		PolicyID: &policyID, EventType: "failover", Status: replicationEventStatusPromoting,
		SourceNodeID: "node-a", TargetNodeID: "node-b", StartedAt: startedAt,
	}
	if err := database.Create(&legacy).Error; err != nil {
		t.Fatalf("create legacy event: %v", err)
	}

	requestedAt := startedAt.Add(time.Minute)
	if event, err := service.findReplicationTransitionEvent(
		policyID, "different-run", &requestedAt, nil, "node-a", "node-b",
	); err == nil || event != nil {
		t.Fatalf("mismatched legacy event was selected: event=%+v err=%v", event, err)
	}
}

func TestFindReplicationTransitionEventMatchesDelayedLegacyEvent(t *testing.T) {
	database := newZeltaServiceTestDB(t, &clusterModels.ReplicationEvent{})
	service := newTestZeltaService(database)
	policyID := uint(7005)
	requestedAt := time.Now().UTC().Add(-time.Hour)
	legacy := clusterModels.ReplicationEvent{
		PolicyID: &policyID, EventType: "failover", Status: replicationEventStatusDemoting,
		SourceNodeID: "node-a", TargetNodeID: "node-b", StartedAt: requestedAt.Add(30 * time.Second),
	}
	if err := database.Create(&legacy).Error; err != nil {
		t.Fatalf("create delayed legacy event: %v", err)
	}

	matched, err := service.findReplicationTransitionEvent(
		policyID, "transition-delayed", &requestedAt, nil, "node-a", "node-b",
	)
	if err != nil {
		t.Fatalf("find delayed legacy event: %v", err)
	}
	if matched.ID != legacy.ID {
		t.Fatalf("matched event %d, want %d", matched.ID, legacy.ID)
	}
}

func TestFindReplicationTransitionEventRejectsLateOrAmbiguousLegacyEvents(t *testing.T) {
	t.Run("after transition completion", func(t *testing.T) {
		database := newZeltaServiceTestDB(t, &clusterModels.ReplicationEvent{})
		service := newTestZeltaService(database)
		policyID := uint(7006)
		requestedAt := time.Now().UTC().Add(-time.Hour)
		completedAt := requestedAt.Add(time.Minute)
		late := clusterModels.ReplicationEvent{
			PolicyID: &policyID, EventType: "failover", Status: replicationEventStatusDemoting,
			SourceNodeID: "node-a", TargetNodeID: "node-b", StartedAt: completedAt.Add(time.Second),
		}
		if err := database.Create(&late).Error; err != nil {
			t.Fatalf("create late legacy event: %v", err)
		}
		if event, err := service.findReplicationTransitionEvent(
			policyID, "transition-late", &requestedAt, &completedAt, "node-a", "node-b",
		); err == nil || event != nil {
			t.Fatalf("post-completion event was selected: event=%+v err=%v", event, err)
		}
	})

	t.Run("multiple compatible candidates", func(t *testing.T) {
		database := newZeltaServiceTestDB(t, &clusterModels.ReplicationEvent{})
		service := newTestZeltaService(database)
		policyID := uint(7007)
		requestedAt := time.Now().UTC().Add(-time.Hour)
		for range 2 {
			event := clusterModels.ReplicationEvent{
				PolicyID: &policyID, EventType: "failover", Status: replicationEventStatusDemoting,
				SourceNodeID: "node-a", TargetNodeID: "node-b", StartedAt: requestedAt,
			}
			if err := database.Create(&event).Error; err != nil {
				t.Fatalf("create ambiguous legacy event: %v", err)
			}
		}
		if event, err := service.findReplicationTransitionEvent(
			policyID, "transition-ambiguous", &requestedAt, nil, "node-a", "node-b",
		); err == nil || event != nil {
			t.Fatalf("ambiguous event was selected: event=%+v err=%v", event, err)
		}
	})
}

func TestEmitReplicationOutputEmitsEachReturnedLineOnce(t *testing.T) {
	var lines []string
	emitReplicationOutput(func(line string) {
		lines = append(lines, line)
	}, "seed line\n\ntransfer line\n")

	want := []string{"seed line", "transfer line"}
	if len(lines) != len(want) {
		t.Fatalf("emitted lines = %#v, want %#v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("emitted lines = %#v, want %#v", lines, want)
		}
	}
}

func newReplicationAttemptTestFixture(
	t *testing.T,
	enabled bool,
) (*Service, *clusterModels.ReplicationPolicy, clusterModels.ReplicationPolicyTarget, clusterModels.ReplicationEvent) {
	t.Helper()
	database := newZeltaServiceTestDB(
		t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationEvent{},
	)
	service := newTestZeltaService(database)
	service.Cluster = &clusterService.Service{DB: database}

	policy := &clusterModels.ReplicationPolicy{
		ID:              701,
		Name:            "observability-attempt",
		GuestType:       clusterModels.ReplicationGuestTypeVM,
		GuestID:         701,
		SourceNodeID:    "ares",
		ActiveNodeID:    "ares",
		OwnerEpoch:      1,
		CronExpr:        "*/5 * * * *",
		Enabled:         enabled,
		ProtectionState: clusterModels.ReplicationProtectionStateArmed,
		TransitionState: clusterModels.ReplicationTransitionStateNone,
	}
	if err := database.Create(policy).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}

	verifiedAt := time.Now().UTC().Add(-time.Minute)
	readyUntil := verifiedAt.Add(10 * time.Minute)
	target := clusterModels.ReplicationPolicyTarget{
		PolicyID:              policy.ID,
		NodeID:                "hera",
		Weight:                200,
		Ready:                 true,
		GenerationID:          "generation-old",
		OwnerEpoch:            policy.OwnerEpoch,
		ManifestHash:          "manifest-old",
		RequiredDatasetCount:  1,
		CompletedDatasetCount: 1,
		LastVerifiedAt:        &verifiedAt,
		ReadyUntil:            &readyUntil,
	}
	if err := database.Create(&target).Error; err != nil {
		t.Fatalf("create target: %v", err)
	}

	event := clusterModels.ReplicationEvent{
		PolicyID:  &policy.ID,
		EventType: "replication",
		Status:    replicationEventStatusRunning,
		StartedAt: time.Now().UTC(),
	}
	if err := database.Create(&event).Error; err != nil {
		t.Fatalf("create event: %v", err)
	}
	return service, policy, target, event
}

func TestReplicationTargetAttemptInvalidatesReadinessBeforeTransfer(t *testing.T) {
	service, policy, target, event := newReplicationAttemptTestFixture(t, true)
	transferCalled := false

	result, err := service.runReplicationTargetGenerationAttempt(
		policy,
		target,
		event.ID,
		func() (replicationGenerationTransferResult, error) {
			transferCalled = true
			var duringTransfer clusterModels.ReplicationPolicyTarget
			if lookupErr := service.DB.First(&duringTransfer, target.ID).Error; lookupErr != nil {
				t.Fatalf("load target during transfer: %v", lookupErr)
			}
			if duringTransfer.Ready {
				t.Fatal("target remained ready when transfer began")
			}
			if duringTransfer.LastError != "replication_generation_attempt_in_progress" {
				t.Fatalf("pre-transfer marker = %q", duringTransfer.LastError)
			}
			return replicationGenerationTransferResult{
				GenerationID:          "generation-new",
				ManifestHash:          "manifest-new",
				RequiredDatasetCount:  1,
				CompletedDatasetCount: 1,
			}, nil
		},
	)
	if err != nil {
		t.Fatalf("run target attempt: %v", err)
	}
	if !transferCalled {
		t.Fatal("transfer was not called")
	}
	if result.GenerationID != "generation-new" {
		t.Fatalf("generation = %q", result.GenerationID)
	}

	var finalized clusterModels.ReplicationPolicyTarget
	if err := service.DB.First(&finalized, target.ID).Error; err != nil {
		t.Fatalf("reload finalized target: %v", err)
	}
	if !finalized.Ready || finalized.GenerationID != "generation-new" || finalized.LastError != "" {
		t.Fatalf("unexpected finalized target: %+v", finalized)
	}
}

func TestReplicationTargetAttemptFailureRemainsUnready(t *testing.T) {
	service, policy, target, event := newReplicationAttemptTestFixture(t, true)
	transferErr := errors.New("synthetic_transfer_failure")

	_, err := service.runReplicationTargetGenerationAttempt(
		policy,
		target,
		event.ID,
		func() (replicationGenerationTransferResult, error) {
			var duringTransfer clusterModels.ReplicationPolicyTarget
			if lookupErr := service.DB.First(&duringTransfer, target.ID).Error; lookupErr != nil {
				t.Fatalf("load target during failed transfer: %v", lookupErr)
			}
			if duringTransfer.Ready {
				t.Fatal("target remained ready during failed transfer")
			}
			return replicationGenerationTransferResult{}, transferErr
		},
	)
	if !errors.Is(err, transferErr) {
		t.Fatalf("attempt error = %v, want %v", err, transferErr)
	}

	var failed clusterModels.ReplicationPolicyTarget
	if err := service.DB.First(&failed, target.ID).Error; err != nil {
		t.Fatalf("reload failed target: %v", err)
	}
	if failed.Ready {
		t.Fatal("failed target was left ready")
	}
	if !strings.Contains(failed.LastError, transferErr.Error()) {
		t.Fatalf("target last error = %q", failed.LastError)
	}
}

func TestReplicationTargetAttemptDoesNotTransferWhenInvalidationFails(t *testing.T) {
	service, policy, target, event := newReplicationAttemptTestFixture(t, false)
	transferCalled := false

	_, err := service.runReplicationTargetGenerationAttempt(
		policy,
		target,
		event.ID,
		func() (replicationGenerationTransferResult, error) {
			transferCalled = true
			return replicationGenerationTransferResult{}, nil
		},
	)
	if transferCalled {
		t.Fatal("transfer ran despite pre-transfer invalidation failure")
	}
	if err == nil || !strings.Contains(err.Error(), "replication_target_pre_transfer_readiness_invalidation_failed") {
		t.Fatalf("attempt error = %v", err)
	}
	if !strings.Contains(err.Error(), "replication_target_fallback_readiness_invalidation_failed") {
		t.Fatalf("fallback invalidation error was not joined: %v", err)
	}

	var storedEvent clusterModels.ReplicationEvent
	if err := service.DB.First(&storedEvent, event.ID).Error; err != nil {
		t.Fatalf("reload event: %v", err)
	}
	if !strings.Contains(storedEvent.Output, "[target=hera] replication_target_fallback_readiness_invalidation_failed") {
		t.Fatalf("fallback failure was not target-labelled: %q", storedEvent.Output)
	}
}

func TestFinalizeReplicationEventPersistsSuccess(t *testing.T) {
	database := newZeltaServiceTestDB(t, &clusterModels.ReplicationEvent{})
	service := newTestZeltaService(database)
	event := clusterModels.ReplicationEvent{
		EventType: "replication",
		Status:    replicationEventStatusRunning,
		StartedAt: time.Now().UTC(),
	}
	if err := database.Create(&event).Error; err != nil {
		t.Fatalf("create event: %v", err)
	}

	if err := service.finalizeReplicationEvent(&event, nil); err != nil {
		t.Fatalf("finalize event: %v", err)
	}
	var stored clusterModels.ReplicationEvent
	if err := database.First(&stored, event.ID).Error; err != nil {
		t.Fatalf("reload event: %v", err)
	}
	if stored.Status != replicationEventStatusSuccess || stored.CompletedAt == nil {
		t.Fatalf("event was not finalized: %+v", stored)
	}
}

func TestFinalizeReplicationEventPersistenceFailurePropagates(t *testing.T) {
	database := newZeltaServiceTestDB(t, &clusterModels.ReplicationEvent{})
	service := newTestZeltaService(database)
	event := clusterModels.ReplicationEvent{
		ID:        999999,
		EventType: "replication",
		Status:    replicationEventStatusRunning,
		StartedAt: time.Now().UTC(),
	}

	err := service.finalizeReplicationEvent(&event, nil)
	if err == nil || !strings.Contains(err.Error(), "replication_event_finalize_persist_failed") {
		t.Fatalf("finalize error = %v", err)
	}
}
