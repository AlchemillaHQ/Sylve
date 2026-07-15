// SPDX-License-Identifier: BSD-2-Clause

package zelta

import (
	"errors"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
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
