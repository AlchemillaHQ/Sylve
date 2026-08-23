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
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	internalDB "github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func newRestoreObservabilityService(t *testing.T) (*Service, *gorm.DB, clusterModels.BackupTarget) {
	t.Helper()
	mainDB := newZeltaServiceTestDB(t,
		&clusterModels.BackupTarget{},
		&clusterModels.BackupJob{},
		&clusterModels.BackupJobOperation{},
		&clusterModels.BackupTargetRestoreOperation{},
		&clusterModels.BackupEvent{},
	)
	telemetryDB := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})
	service := newTestZeltaService(mainDB)
	service.TelemetryDB = telemetryDB
	target := clusterModels.BackupTarget{
		ID: 91, Name: "observability-target", SSHHost: "root@backup", SSHPort: 22,
		BackupRoot: "tank/backups", Enabled: true,
	}
	if err := mainDB.Create(&target).Error; err != nil {
		t.Fatalf("create target: %v", err)
	}
	return service, telemetryDB, target
}

func newStartedRestoreAudit(t *testing.T, telemetryDB *gorm.DB, path string) infoModels.AuditRecord {
	t.Helper()
	audit := infoModels.AuditRecord{
		Status:  "started",
		Started: time.Now().UTC(),
		Action:  fmt.Sprintf(`{"method":"POST","path":%q}`, path),
	}
	if err := telemetryDB.Create(&audit).Error; err != nil {
		t.Fatalf("create audit: %v", err)
	}
	return audit
}

func TestRestoreJobInvalidRemoteValuesHaveNoDurableSideEffects(t *testing.T) {
	service, _, target := newRestoreObservabilityService(t)
	job := clusterModels.BackupJob{
		ID: 92, Name: "invalid-restore", TargetID: target.ID,
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "zroot/data",
		DestSuffix: "data", CronExpr: "0 0 * * *",
	}
	if err := service.DB.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}
	publications := 0
	service.backupOperationEnqueue = func(context.Context, string, any) error {
		publications++
		return nil
	}
	for _, snapshot := range []string{
		"@bk_j92_c1_valid;touch",
		"tank/backups/../data@bk_j92_c1_valid",
		"tank/backups-old/data@bk_j92_c1_valid",
	} {
		if err := service.EnqueueRestoreJob(context.Background(), job.ID, snapshot); err == nil {
			t.Fatalf("accepted invalid snapshot %q", snapshot)
		}
	}
	var operations int64
	if err := service.DB.Model(&clusterModels.BackupJobOperation{}).Count(&operations).Error; err != nil {
		t.Fatalf("count operations: %v", err)
	}
	var events int64
	if err := service.DB.Model(&clusterModels.BackupEvent{}).Count(&events).Error; err != nil {
		t.Fatalf("count events: %v", err)
	}
	if operations != 0 || events != 0 || publications != 0 || len(service.runningJobs) != 0 {
		t.Fatalf(
			"side effects: operations=%d events=%d publications=%d reservations=%d",
			operations, events, publications, len(service.runningJobs),
		)
	}
}

func TestRestoreEventAndAuditTerminalTransitionsAreExactlyCorrelated(t *testing.T) {
	service, telemetryDB, target := newRestoreObservabilityService(t)
	firstAudit := newStartedRestoreAudit(t, telemetryDB, "/api/cluster/backups/targets/91/restore")
	secondAudit := newStartedRestoreAudit(t, telemetryDB, "/api/cluster/backups/targets/91/restore")

	firstOp := "target-restore:local:first"
	secondOp := "target-restore:local:second"
	first, firstEvent, err := service.prepareRestoreObservability(
		internalDB.ContextWithAuditRecordID(context.Background(), firstAudit.ID),
		restoreAuditTypeTarget,
		target.ID,
		firstOp,
		restoreEventSpec{SourceDataset: "tank/backups/data@first", TargetEndpoint: "zroot/first"},
	)
	if err != nil {
		t.Fatalf("prepare first: %v", err)
	}
	second, secondEvent, err := service.prepareRestoreObservability(
		internalDB.ContextWithAuditRecordID(context.Background(), secondAudit.ID),
		restoreAuditTypeTarget,
		target.ID,
		secondOp,
		restoreEventSpec{SourceDataset: "tank/backups/data@second", TargetEndpoint: "zroot/second"},
	)
	if err != nil {
		t.Fatalf("prepare second: %v", err)
	}
	if first.EventID == second.EventID || first.Audit.RecordID == second.Audit.RecordID {
		t.Fatalf("correlations collided: first=%+v second=%+v", first, second)
	}
	if started, err := service.beginRestoreEvent(firstEvent.ID, firstOp); err != nil || !started {
		t.Fatalf("start first event: started=%v err=%v", started, err)
	}
	if started, err := service.beginRestoreEvent(secondEvent.ID, secondOp); err != nil || !started {
		t.Fatalf("start second event: started=%v err=%v", started, err)
	}

	firstFailure := errors.New("first restore failed")
	if err := service.finalizeRestoreEventByID(firstEvent.ID, firstFailure, "first output"); err != nil {
		t.Fatalf("finalize first: %v", err)
	}
	if err := telemetryDB.First(&firstAudit, firstAudit.ID).Error; err != nil {
		t.Fatalf("reload first audit: %v", err)
	}
	if err := telemetryDB.First(&secondAudit, secondAudit.ID).Error; err != nil {
		t.Fatalf("reload second audit: %v", err)
	}
	if firstAudit.Status != "failed" || firstAudit.Error != firstFailure.Error() {
		t.Fatalf("first audit = %+v", firstAudit)
	}
	if secondAudit.Status != "pending" || secondAudit.AsyncOperationID != secondOp {
		t.Fatalf("second concurrent audit was finalized: %+v", secondAudit)
	}

	if err := service.finalizeRestoreEventByID(secondEvent.ID, nil, "second output"); err != nil {
		t.Fatalf("finalize second: %v", err)
	}
	if err := service.finalizeRestoreEventByID(firstEvent.ID, nil, "tampered terminal replay"); err != nil {
		t.Fatalf("replay first finalizer: %v", err)
	}
	if err := service.DB.First(firstEvent, firstEvent.ID).Error; err != nil {
		t.Fatalf("reload first event: %v", err)
	}
	if firstEvent.Status != "failed" || firstEvent.Error != firstFailure.Error() || firstEvent.Output != "first output" {
		t.Fatalf("terminal event was overwritten: %+v", firstEvent)
	}
}

func TestConcurrentRestoreAuditsOnOneTargetEachReachOneTerminalState(t *testing.T) {
	service, telemetryDB, target := newRestoreObservabilityService(t)
	const operations = 8
	type observed struct {
		audit infoModels.AuditRecord
		event *clusterModels.BackupEvent
		fail  bool
	}
	items := make([]observed, 0, operations)
	for i := 0; i < operations; i++ {
		audit := newStartedRestoreAudit(t, telemetryDB, "/api/cluster/backups/targets/91/restore")
		operationID := fmt.Sprintf("target-restore:local:concurrent-%d", i)
		_, event, err := service.prepareRestoreObservability(
			internalDB.ContextWithAuditRecordID(context.Background(), audit.ID),
			restoreAuditTypeTarget,
			target.ID,
			operationID,
			restoreEventSpec{
				SourceDataset:  fmt.Sprintf("tank/backups/data@%d", i),
				TargetEndpoint: fmt.Sprintf("zroot/concurrent-%d", i),
			},
		)
		if err != nil {
			t.Fatalf("prepare operation %d: %v", i, err)
		}
		if started, err := service.beginRestoreEvent(event.ID, operationID); err != nil || !started {
			t.Fatalf("start operation %d: started=%v err=%v", i, started, err)
		}
		items = append(items, observed{audit: audit, event: event, fail: i%2 != 0})
	}

	var wg sync.WaitGroup
	errs := make(chan error, operations)
	for i := range items {
		item := items[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			var runErr error
			if item.fail {
				runErr = errors.New("injected concurrent failure")
			}
			errs <- service.finalizeRestoreEventByID(item.event.ID, runErr, "")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent finalizer: %v", err)
		}
	}
	for i := range items {
		if err := telemetryDB.First(&items[i].audit, items[i].audit.ID).Error; err != nil {
			t.Fatalf("reload audit %d: %v", i, err)
		}
		want := "success"
		if items[i].fail {
			want = "failed"
		}
		if items[i].audit.Status != want {
			t.Fatalf("audit %d status=%s want=%s", i, items[i].audit.Status, want)
		}
	}
}

func TestRestoreEventAndAuditAreDurableBeforeQueuePublication(t *testing.T) {
	service, telemetryDB, target := newRestoreObservabilityService(t)
	audit := newStartedRestoreAudit(t, telemetryDB, "/api/cluster/backups/targets/91/restore")
	var queued restoreFromTargetPayload
	service.restoreFromTargetOperationEnqueue = func(_ context.Context, _ string, value any) error {
		queued = value.(restoreFromTargetPayload)
		var event clusterModels.BackupEvent
		if err := service.DB.First(&event, queued.EventID).Error; err != nil {
			t.Fatalf("event was not persisted before queue publication: %v", err)
		}
		if event.Status != "queued" || event.OperationID == nil || *event.OperationID != queued.OperationToken {
			t.Fatalf("pre-queue event = %+v payload=%+v", event, queued)
		}
		var currentAudit infoModels.AuditRecord
		if err := telemetryDB.First(&currentAudit, queued.AuditRecordID).Error; err != nil {
			t.Fatalf("audit was not persisted before queue publication: %v", err)
		}
		if currentAudit.Status != "pending" || currentAudit.AsyncOperationID != queued.OperationToken ||
			queued.AuditOperationID != queued.OperationToken {
			t.Fatalf("pre-queue audit = %+v payload=%+v", currentAudit, queued)
		}
		return nil
	}
	service.restoreFromTargetRun = func(context.Context, *clusterModels.BackupTarget, restoreFromTargetPayload) error {
		return nil
	}
	if err := service.EnqueueRestoreFromTarget(
		internalDB.ContextWithAuditRecordID(context.Background(), audit.ID),
		target.ID,
		"tank/backups/data",
		"@bk_j1_c1_ordering",
		"zroot/ordering",
		true,
		"00000000-1111-4222-8333-444444444444",
	); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if queued.EventID == 0 || queued.AuditRecordID != audit.ID {
		t.Fatalf("missing queue correlation: %+v", queued)
	}
	if err := service.handleRestoreFromTargetQueue(context.Background(), queued); err != nil {
		t.Fatalf("execute queued restore: %v", err)
	}
}

func TestRestoreQueueFailureFinalizesPrecreatedEventAndAudit(t *testing.T) {
	service, telemetryDB, target := newRestoreObservabilityService(t)
	audit := newStartedRestoreAudit(t, telemetryDB, "/api/cluster/backups/targets/91/restore")
	service.restoreFromTargetOperationEnqueue = func(context.Context, string, any) error {
		return context.DeadlineExceeded
	}

	err := service.EnqueueRestoreFromTarget(
		internalDB.ContextWithAuditRecordID(context.Background(), audit.ID),
		target.ID,
		"tank/backups/data",
		"@bk_j1_c1_queue_failure",
		"zroot/queue-failure",
		true,
		"11111111-2222-4333-8444-555555555555",
	)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("enqueue error = %v", err)
	}

	var event clusterModels.BackupEvent
	if err := service.DB.First(&event).Error; err != nil {
		t.Fatalf("load event: %v", err)
	}
	if event.Status != "failed" || !strings.Contains(event.Error, "restore_queue_failed") || event.CompletedAt == nil {
		t.Fatalf("queue failure event = %+v", event)
	}
	if err := telemetryDB.First(&audit, audit.ID).Error; err != nil {
		t.Fatalf("load audit: %v", err)
	}
	if audit.Status != "failed" || audit.AsyncOperationID == "" || !strings.Contains(audit.Error, "restore_queue_failed") {
		t.Fatalf("queue failure audit = %+v", audit)
	}
	var operations int64
	if err := service.DB.Model(&clusterModels.BackupTargetRestoreOperation{}).Count(&operations).Error; err != nil || operations != 0 {
		t.Fatalf("aborted operation count=%d err=%v", operations, err)
	}
}

func TestAmbiguousQueueTimeoutCannotOverwriteFastWorkerOutcome(t *testing.T) {
	service, telemetryDB, target := newRestoreObservabilityService(t)
	firstAudit := newStartedRestoreAudit(t, telemetryDB, "/api/cluster/backups/targets/91/restore")
	service.restoreFromTargetRun = func(context.Context, *clusterModels.BackupTarget, restoreFromTargetPayload) error {
		return nil
	}
	var publications int
	service.restoreFromTargetOperationEnqueue = func(_ context.Context, _ string, value any) error {
		publications++
		if err := service.handleRestoreFromTargetQueue(context.Background(), value.(restoreFromTargetPayload)); err != nil {
			return err
		}
		return context.DeadlineExceeded
	}
	operationID := "22222222-3333-4444-8555-666666666666"
	err := service.EnqueueRestoreFromTarget(
		internalDB.ContextWithAuditRecordID(context.Background(), firstAudit.ID),
		target.ID,
		"tank/backups/data",
		"@bk_j1_c1_ambiguous",
		"zroot/ambiguous",
		true,
		operationID,
	)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ambiguous enqueue result = %v", err)
	}
	var event clusterModels.BackupEvent
	if err := service.DB.First(&event).Error; err != nil {
		t.Fatalf("load event: %v", err)
	}
	if event.Status != "success" {
		t.Fatalf("queue error overwrote worker outcome: %+v", event)
	}
	if err := telemetryDB.First(&firstAudit, firstAudit.ID).Error; err != nil || firstAudit.Status != "success" {
		t.Fatalf("first audit=%+v err=%v", firstAudit, err)
	}

	secondAudit := newStartedRestoreAudit(t, telemetryDB, "/api/cluster/backups/targets/91/restore")
	if err := service.EnqueueRestoreFromTarget(
		internalDB.ContextWithAuditRecordID(context.Background(), secondAudit.ID),
		target.ID,
		"tank/backups/data",
		"@bk_j1_c1_ambiguous",
		"zroot/ambiguous",
		true,
		operationID,
	); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if publications != 1 {
		t.Fatalf("completed retry republished queue work %d times", publications)
	}
	if err := telemetryDB.First(&secondAudit, secondAudit.ID).Error; err != nil || secondAudit.Status != "success" {
		t.Fatalf("retry audit=%+v err=%v", secondAudit, err)
	}
}

func TestRestoreTargetQueuePanicIsTerminallyObserved(t *testing.T) {
	service, telemetryDB, target := newRestoreObservabilityService(t)
	audit := newStartedRestoreAudit(t, telemetryDB, "/api/cluster/backups/targets/91/restore")
	var payload restoreFromTargetPayload
	service.restoreFromTargetOperationEnqueue = func(_ context.Context, _ string, value any) error {
		payload = value.(restoreFromTargetPayload)
		return nil
	}
	service.restoreFromTargetRun = func(context.Context, *clusterModels.BackupTarget, restoreFromTargetPayload) error {
		panic("injected restore panic")
	}
	if err := service.EnqueueRestoreFromTarget(
		internalDB.ContextWithAuditRecordID(context.Background(), audit.ID),
		target.ID,
		"tank/backups/data",
		"@bk_j1_c1_panic",
		"zroot/panic",
		true,
		"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
	); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := service.handleRestoreFromTargetQueue(context.Background(), payload); err != nil {
		t.Fatalf("consume panic operation: %v", err)
	}

	var event clusterModels.BackupEvent
	if err := service.DB.First(&event, payload.EventID).Error; err != nil {
		t.Fatalf("load event: %v", err)
	}
	if event.Status != "failed" || !strings.Contains(event.Error, "panic_in_restore_from_target") {
		t.Fatalf("panic event = %+v", event)
	}
	if err := telemetryDB.First(&audit, audit.ID).Error; err != nil {
		t.Fatalf("load audit: %v", err)
	}
	if audit.Status != "failed" || !strings.Contains(audit.Error, "panic_in_restore_from_target") {
		t.Fatalf("panic audit = %+v", audit)
	}
	var operation clusterModels.BackupTargetRestoreOperation
	if err := service.DB.Where("token = ?", payload.OperationToken).First(&operation).Error; err != nil {
		t.Fatalf("load completion receipt: %v", err)
	}
	if operation.State != clusterModels.BackupTargetRestoreOperationCompleted {
		t.Fatalf("panic operation = %+v", operation)
	}
}

func TestOOBRestoreKindsUseOneTerminalEventAndAuditFinalizer(t *testing.T) {
	service, telemetryDB, target := newRestoreObservabilityService(t)
	cases := []struct {
		kind        string
		operationID string
		remote      string
		destination string
	}{
		{kind: "dataset", operationID: "10000000-0000-4000-8000-000000000001", remote: "tank/backups/data", destination: "zroot/oob-data"},
		{kind: "jail", operationID: "10000000-0000-4000-8000-000000000002", remote: "tank/backups/jails/42/job-1", destination: "zroot/sylve/jails/142"},
		{kind: "vm", operationID: "10000000-0000-4000-8000-000000000003", remote: "tank/backups/virtual-machines/43/job-1", destination: "zroot/sylve/virtual-machines/143"},
	}
	failureByToken := make(map[string]error, len(cases))
	service.restoreFromTargetRun = func(_ context.Context, _ *clusterModels.BackupTarget, payload restoreFromTargetPayload) error {
		return failureByToken[payload.OperationToken]
	}

	for _, tc := range cases {
		audit := newStartedRestoreAudit(t, telemetryDB, "/api/cluster/backups/targets/91/restore")
		restoreNetwork := true
		handle, payload, err := service.acquireDurableBackupTargetRestoreOperation(
			context.Background(),
			restoreFromTargetPayload{
				TargetID: target.ID, RemoteDataset: tc.remote, Snapshot: "@bk_j1_c1_failure",
				DestinationDataset: tc.destination, RestoreNetwork: &restoreNetwork,
			},
			tc.operationID,
		)
		if err != nil {
			t.Fatalf("acquire %s: %v", tc.kind, err)
		}
		execution, event, err := service.prepareRestoreObservability(
			internalDB.ContextWithAuditRecordID(context.Background(), audit.ID),
			restoreAuditTypeTarget,
			target.ID,
			handle.Token,
			restoreEventSpec{SourceDataset: payload.RemoteDataset + payload.Snapshot, TargetEndpoint: payload.DestinationDataset},
		)
		if err != nil {
			t.Fatalf("prepare %s observability: %v", tc.kind, err)
		}
		payload.OperationToken = handle.Token
		payload.HolderNodeID = handle.HolderNodeID
		payload.EventID = execution.EventID
		payload.AuditRecordID = execution.Audit.RecordID
		payload.AuditOperationID = execution.Audit.OperationID
		failureByToken[handle.Token] = fmt.Errorf("injected_%s_oob_failure", tc.kind)
		if err := service.handleRestoreFromTargetQueue(context.Background(), payload); err != nil {
			t.Fatalf("execute %s: %v", tc.kind, err)
		}
		if err := service.DB.First(event, event.ID).Error; err != nil {
			t.Fatalf("reload %s event: %v", tc.kind, err)
		}
		if event.Status != "failed" || !strings.Contains(event.Error, "injected_"+tc.kind+"_oob_failure") {
			t.Fatalf("%s OOB event = %+v", tc.kind, event)
		}
		if err := telemetryDB.First(&audit, audit.ID).Error; err != nil {
			t.Fatalf("reload %s audit: %v", tc.kind, err)
		}
		if audit.Status != "failed" || audit.AsyncOperationID != handle.Token {
			t.Fatalf("%s OOB audit = %+v", tc.kind, audit)
		}
	}
}

func TestRestoreTargetLookupFailureAfterAcceptanceIsObserved(t *testing.T) {
	service, telemetryDB, target := newRestoreObservabilityService(t)
	audit := newStartedRestoreAudit(t, telemetryDB, "/api/cluster/backups/targets/91/restore")
	var payload restoreFromTargetPayload
	service.restoreFromTargetOperationEnqueue = func(_ context.Context, _ string, value any) error {
		payload = value.(restoreFromTargetPayload)
		return nil
	}
	if err := service.EnqueueRestoreFromTarget(
		internalDB.ContextWithAuditRecordID(context.Background(), audit.ID),
		target.ID,
		"tank/backups/data",
		"@bk_j1_c1_target_deleted",
		"zroot/target-deleted",
		true,
		"bbbbbbbb-cccc-4ddd-8eee-ffffffffffff",
	); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// Bypass the public deletion fence to model loss/corruption between queue
	// acceptance and worker lookup.
	if err := service.DB.Exec("DELETE FROM backup_targets WHERE id = ?", target.ID).Error; err != nil {
		t.Fatalf("delete target: %v", err)
	}
	if err := service.handleRestoreFromTargetQueue(context.Background(), payload); err != nil {
		t.Fatalf("consume target lookup failure: %v", err)
	}

	var event clusterModels.BackupEvent
	if err := service.DB.First(&event, payload.EventID).Error; err != nil {
		t.Fatalf("load event: %v", err)
	}
	if event.Status != "failed" || !strings.Contains(strings.ToLower(event.Error), "backup_target_not_found") {
		t.Fatalf("target lookup event = %+v", event)
	}
	if err := telemetryDB.First(&audit, audit.ID).Error; err != nil {
		t.Fatalf("load audit: %v", err)
	}
	if audit.Status != "failed" || !strings.Contains(strings.ToLower(audit.Error), "backup_target_not_found") {
		t.Fatalf("target lookup audit = %+v", audit)
	}
}

func TestRestoreTargetDisableAfterAcceptanceIsObserved(t *testing.T) {
	service, telemetryDB, target := newRestoreObservabilityService(t)
	audit := newStartedRestoreAudit(t, telemetryDB, "/api/cluster/backups/targets/91/restore")
	var payload restoreFromTargetPayload
	service.restoreFromTargetOperationEnqueue = func(_ context.Context, _ string, value any) error {
		payload = value.(restoreFromTargetPayload)
		return nil
	}
	if err := service.EnqueueRestoreFromTarget(
		internalDB.ContextWithAuditRecordID(context.Background(), audit.ID),
		target.ID,
		"tank/backups/data",
		"@bk_j1_c1_target_disabled",
		"zroot/target-disabled",
		true,
		"cccccccc-dddd-4eee-8fff-aaaaaaaaaaaa",
	); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := service.DB.Model(&clusterModels.BackupTarget{}).
		Where("id = ?", target.ID).
		Update("enabled", false).Error; err != nil {
		t.Fatalf("disable target: %v", err)
	}
	if err := service.handleRestoreFromTargetQueue(context.Background(), payload); err != nil {
		t.Fatalf("consume disabled target: %v", err)
	}

	var event clusterModels.BackupEvent
	if err := service.DB.First(&event, payload.EventID).Error; err != nil {
		t.Fatalf("load event: %v", err)
	}
	if event.Status != "failed" || !strings.Contains(strings.ToLower(event.Error), "backup_target_disabled") {
		t.Fatalf("disabled target event = %+v", event)
	}
	if err := telemetryDB.First(&audit, audit.ID).Error; err != nil {
		t.Fatalf("load audit: %v", err)
	}
	if audit.Status != "failed" || !strings.Contains(strings.ToLower(audit.Error), "backup_target_disabled") {
		t.Fatalf("disabled target audit = %+v", audit)
	}
	var operationCount int64
	if err := service.DB.Model(&clusterModels.BackupTargetRestoreOperation{}).
		Where("token = ?", payload.OperationToken).
		Count(&operationCount).Error; err != nil || operationCount != 0 {
		t.Fatalf("disabled target reservation count=%d err=%v", operationCount, err)
	}
}

func TestRestoreJobModesFinalizeTheirOwnEventAndAuditOnFailure(t *testing.T) {
	service, telemetryDB, target := newRestoreObservabilityService(t)
	jobs := []clusterModels.BackupJob{
		{ID: 201, Name: "dataset", TargetID: target.ID, Mode: clusterModels.BackupJobModeDataset, SourceDataset: "zroot/data", CronExpr: "0 0 * * *"},
		{ID: 202, Name: "jail", TargetID: target.ID, Mode: clusterModels.BackupJobModeJail, JailRootDataset: "zroot/sylve/jails/42", CronExpr: "0 0 * * *"},
		{ID: 203, Name: "vm", TargetID: target.ID, Mode: clusterModels.BackupJobModeVM, SourceDataset: "zroot/sylve/virtual-machines/43", CronExpr: "0 0 * * *"},
	}
	if err := service.DB.Create(&jobs).Error; err != nil {
		t.Fatalf("create jobs: %v", err)
	}
	payloads := make(map[uint]restoreJobPayload)
	service.backupOperationEnqueue = func(_ context.Context, name string, value any) error {
		if name != restoreJobQueueName {
			return fmt.Errorf("unexpected queue %s", name)
		}
		payload := value.(restoreJobPayload)
		payloads[payload.JobID] = payload
		return nil
	}
	service.restoreJobRun = func(_ context.Context, job *clusterModels.BackupJob, _, _ string) error {
		return fmt.Errorf("injected_%s_restore_failure", job.Mode)
	}

	for i := range jobs {
		job := jobs[i]
		audit := newStartedRestoreAudit(t, telemetryDB, fmt.Sprintf("/api/cluster/backups/jobs/%d/restore", job.ID))
		if err := service.EnqueueRestoreJob(
			internalDB.ContextWithAuditRecordID(context.Background(), audit.ID),
			job.ID,
			"@bk_j1_c1_failure",
		); err != nil {
			t.Fatalf("enqueue %s: %v", job.Mode, err)
		}
		payload := payloads[job.ID]
		if payload.EventID == 0 || payload.AuditRecordID != audit.ID || payload.AuditOperationID != payload.OperationToken {
			t.Fatalf("%s payload correlation = %+v", job.Mode, payload)
		}
		if err := service.handleRestoreJobQueue(context.Background(), payload); err != nil {
			t.Fatalf("execute %s: %v", job.Mode, err)
		}

		var event clusterModels.BackupEvent
		if err := service.DB.First(&event, payload.EventID).Error; err != nil {
			t.Fatalf("load %s event: %v", job.Mode, err)
		}
		if event.Status != "failed" || !strings.Contains(event.Error, "injected_"+job.Mode+"_restore_failure") {
			t.Fatalf("%s event = %+v", job.Mode, event)
		}
		if err := telemetryDB.First(&audit, audit.ID).Error; err != nil {
			t.Fatalf("load %s audit: %v", job.Mode, err)
		}
		if audit.Status != "failed" || audit.AsyncOperationID != payload.OperationToken {
			t.Fatalf("%s audit = %+v", job.Mode, audit)
		}
	}
}

func TestReconcileRestoreObservabilityAfterCrash(t *testing.T) {
	service, telemetryDB, target := newRestoreObservabilityService(t)
	old := time.Now().UTC().Add(-time.Hour)
	service.startedAt = time.Now().UTC()

	terminalOp := "target-restore:local:terminal"
	activeOp := "target-restore:local:active"
	orphanOp := "target-restore:local:orphan"
	terminalAudit := infoModels.AuditRecord{
		Status: "pending", Started: old, CreatedAt: old, Action: `{}`,
		AsyncJobID: &target.ID, AsyncJobType: restoreAuditTypeTarget, AsyncOperationID: terminalOp,
	}
	activeAudit := terminalAudit
	activeAudit.AsyncOperationID = activeOp
	orphanAudit := terminalAudit
	orphanAudit.AsyncOperationID = orphanOp
	legacyAudit := terminalAudit
	legacyAudit.AsyncOperationID = ""
	startedAudit := infoModels.AuditRecord{
		Status: "started", Started: old, CreatedAt: old,
		Action: `{"method":"POST","path":"/api/cluster/backups/targets/91/restore"}`,
	}
	freshLegacyAudit := legacyAudit
	freshLegacyAudit.CreatedAt = service.startedAt.Add(time.Second)
	if err := telemetryDB.Create(&[]*infoModels.AuditRecord{
		&terminalAudit, &activeAudit, &orphanAudit, &legacyAudit, &startedAudit, &freshLegacyAudit,
	}).Error; err != nil {
		t.Fatalf("create audits: %v", err)
	}

	terminalEvent := clusterModels.BackupEvent{
		OperationID: &terminalOp, AuditRecordID: &terminalAudit.ID,
		Mode: "restore", Status: "success", SourceDataset: "remote@terminal", TargetEndpoint: "zroot/terminal",
		StartedAt: old, CompletedAt: ptrTime(old.Add(time.Minute)),
	}
	activeEvent := clusterModels.BackupEvent{
		OperationID: &activeOp, AuditRecordID: &activeAudit.ID,
		Mode: "restore", Status: "queued", SourceDataset: "remote@active", TargetEndpoint: "zroot/active", StartedAt: old,
	}
	orphanEvent := clusterModels.BackupEvent{
		OperationID: &orphanOp, AuditRecordID: &orphanAudit.ID,
		Mode: "restore", Status: "running", SourceDataset: "remote@orphan", TargetEndpoint: "zroot/orphan", StartedAt: old,
	}
	if err := service.DB.Create(&[]*clusterModels.BackupEvent{&terminalEvent, &activeEvent, &orphanEvent}).Error; err != nil {
		t.Fatalf("create events: %v", err)
	}
	if err := service.DB.Create(&clusterModels.BackupTargetRestoreOperation{
		Token: activeOp, TargetID: target.ID, HolderNodeID: "local", DestinationDataset: "zroot/active",
		RequestPayload: `{}`, State: clusterModels.BackupTargetRestoreOperationQueued,
		Revision: 1, AcquiredAt: old, UpdatedAt: old,
	}).Error; err != nil {
		t.Fatalf("create active operation: %v", err)
	}

	if err := service.ReconcileRestoreObservabilityAfterRestart(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	for _, audit := range []*infoModels.AuditRecord{&terminalAudit, &activeAudit, &orphanAudit, &legacyAudit, &startedAudit, &freshLegacyAudit} {
		if err := telemetryDB.First(audit, audit.ID).Error; err != nil {
			t.Fatalf("reload audit %d: %v", audit.ID, err)
		}
	}
	if terminalAudit.Status != "success" {
		t.Fatalf("terminal audit = %+v", terminalAudit)
	}
	if activeAudit.Status != "pending" {
		t.Fatalf("active audit = %+v", activeAudit)
	}
	if orphanAudit.Status != "failed" || !strings.Contains(orphanAudit.Error, "disappeared") && !strings.Contains(orphanAudit.Error, "restarted") {
		t.Fatalf("orphan audit = %+v", orphanAudit)
	}
	if legacyAudit.Status != "failed" || startedAudit.Status != "failed" {
		t.Fatalf("legacy pending=%+v started=%+v", legacyAudit, startedAudit)
	}
	if freshLegacyAudit.Status != "pending" {
		t.Fatalf("fresh audit was reconciled: %+v", freshLegacyAudit)
	}
	if err := service.DB.First(&activeEvent, activeEvent.ID).Error; err != nil || activeEvent.Status != "queued" {
		t.Fatalf("active event=%+v err=%v", activeEvent, err)
	}
	if err := service.DB.First(&orphanEvent, orphanEvent.ID).Error; err != nil || orphanEvent.Status != "interrupted" {
		t.Fatalf("orphan event=%+v err=%v", orphanEvent, err)
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
