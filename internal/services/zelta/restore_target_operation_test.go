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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func newBackupTargetRestoreOperationService(t *testing.T) (*Service, clusterModels.BackupTarget) {
	t.Helper()
	database := newZeltaServiceTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupEvent{})
	target := clusterModels.BackupTarget{
		ID: 81, Name: "target", SSHHost: "root@backup", SSHPort: 22,
		BackupRoot: "tank/backups", Enabled: true,
	}
	if err := database.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	return newTestZeltaService(database), target
}

func enqueueTargetRestore(
	ctx context.Context,
	service *Service,
	targetID uint,
	destination string,
) error {
	return enqueueTargetRestoreWithOperationID(ctx, service, targetID, destination, "")
}

func enqueueTargetRestoreWithOperationID(
	ctx context.Context,
	service *Service,
	targetID uint,
	destination string,
	operationID string,
) error {
	return service.EnqueueRestoreFromTarget(
		ctx,
		targetID,
		"tank/backups/data",
		"@bk_j1_c1_test",
		destination,
		true,
		operationID,
	)
}

func TestOOBRestoreInvalidRemoteValuesHaveNoDurableSideEffects(t *testing.T) {
	service, target := newBackupTargetRestoreOperationService(t)
	harness := newFakeSSHHarness(t)
	publications := 0
	service.restoreFromTargetOperationEnqueue = func(context.Context, string, any) error {
		publications++
		return nil
	}
	tests := []struct {
		remote      string
		snapshot    string
		destination string
	}{
		{remote: "tank/backups/data;touch", snapshot: "@bk_j1_c1_test", destination: "zroot/restored"},
		{remote: "tank/backups/data", snapshot: "@bk_j1_c1_test;touch", destination: "zroot/restored"},
		{remote: "tank/backups/data", snapshot: "@bk_j1_c1_test", destination: "/zroot/restored"},
	}
	for _, test := range tests {
		if err := service.EnqueueRestoreFromTarget(
			context.Background(),
			target.ID,
			test.remote,
			test.snapshot,
			test.destination,
			true,
			"",
		); err == nil {
			t.Fatalf("accepted invalid request: %+v", test)
		}
	}
	var operations int64
	if err := service.DB.Model(&clusterModels.BackupTargetRestoreOperation{}).Count(&operations).Error; err != nil {
		t.Fatalf("count operations: %v", err)
	}
	var events int64
	if err := service.DB.Model(&clusterModels.BackupEvent{}).Count(&events).Error; err != nil {
		t.Fatalf("count events: %v", err)
	}
	if calls := harness.Calls(); operations != 0 || events != 0 || publications != 0 || len(calls) != 0 {
		t.Fatalf("side effects: operations=%d events=%d publications=%d ssh=%v", operations, events, publications, calls)
	}
}

func TestOOBRestoreReservationExistsWhileQueueLaneIsSaturated(t *testing.T) {
	service, target := newBackupTargetRestoreOperationService(t)
	enqueueEntered := make(chan restoreFromTargetPayload, 1)
	releaseEnqueue := make(chan struct{})
	service.restoreFromTargetOperationEnqueue = func(_ context.Context, name string, payload any) error {
		if name != restoreFromTargetQueueName {
			t.Fatalf("queue name = %q", name)
		}
		queued, ok := payload.(restoreFromTargetPayload)
		if !ok {
			t.Fatalf("queue payload = %#v", payload)
		}
		enqueueEntered <- queued
		<-releaseEnqueue
		return nil
	}

	firstResult := make(chan error, 1)
	go func() {
		firstResult <- enqueueTargetRestore(context.Background(), service, target.ID, "zroot/restored")
	}()

	var firstPayload restoreFromTargetPayload
	select {
	case firstPayload = <-enqueueEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("first request did not reach the saturated queue")
	}
	if firstPayload.OperationToken == "" || firstPayload.HolderNodeID != "local" ||
		firstPayload.DestinationDataset != "zroot/restored" {
		t.Fatalf("durable queue payload = %+v", firstPayload)
	}

	secondErr := enqueueTargetRestore(context.Background(), service, target.ID, "zroot/restored/child")
	if secondErr == nil || !strings.Contains(secondErr.Error(), "restore_destination_reserved") {
		t.Fatalf("overlapping request error = %v", secondErr)
	}
	var operations []clusterModels.BackupTargetRestoreOperation
	if err := service.DB.Find(&operations).Error; err != nil {
		t.Fatalf("load operations: %v", err)
	}
	if len(operations) != 1 || operations[0].State != clusterModels.BackupTargetRestoreOperationQueued ||
		operations[0].Token != firstPayload.OperationToken {
		t.Fatalf("queued reservations = %+v", operations)
	}

	close(releaseEnqueue)
	if err := <-firstResult; err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if retryErr := enqueueTargetRestore(context.Background(), service, target.ID, "zroot/restored"); retryErr == nil || !strings.Contains(retryErr.Error(), "restore_destination_reserved") {
		t.Fatalf("client retry after committed enqueue error = %v", retryErr)
	}
}

func TestOOBRestoreAmbiguousEnqueueTimeoutMakesStaleMessageNoOp(t *testing.T) {
	service, target := newBackupTargetRestoreOperationService(t)
	var queued []restoreFromTargetPayload
	service.restoreFromTargetOperationEnqueue = func(_ context.Context, _ string, payload any) error {
		queued = append(queued, payload.(restoreFromTargetPayload))
		if len(queued) == 1 {
			// Model an enqueue that committed but whose caller observed a timeout.
			return context.DeadlineExceeded
		}
		return nil
	}

	if err := enqueueTargetRestore(context.Background(), service, target.ID, "zroot/restored"); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first enqueue error = %v", err)
	}
	var count int64
	if err := service.DB.Model(&clusterModels.BackupTargetRestoreOperation{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("reservation after failed enqueue = %d err=%v", count, err)
	}

	if err := enqueueTargetRestore(context.Background(), service, target.ID, "zroot/restored"); err != nil {
		t.Fatalf("retry enqueue: %v", err)
	}
	if len(queued) != 2 || queued[0].OperationToken == queued[1].OperationToken {
		t.Fatalf("queued operation tokens = %q, %q", queued[0].OperationToken, queued[1].OperationToken)
	}

	if _, _, execute, err := service.prepareQueuedBackupTargetRestoreOperation(context.Background(), queued[0]); err != nil || execute {
		t.Fatalf("ambiguous stale message execute=%v err=%v", execute, err)
	}
	handle, _, execute, err := service.prepareQueuedBackupTargetRestoreOperation(context.Background(), queued[1])
	if err != nil || !execute {
		t.Fatalf("retry message execute=%v err=%v", execute, err)
	}
	if _, _, duplicateExecute, err := service.prepareQueuedBackupTargetRestoreOperation(context.Background(), queued[1]); err != nil || duplicateExecute {
		t.Fatalf("duplicate retry message execute=%v err=%v", duplicateExecute, err)
	}
	if err := service.finishDurableBackupTargetRestoreOperation(handle); err != nil {
		t.Fatalf("finish retry operation: %v", err)
	}
}

func TestOOBRestoreQueueDuplicateExecutesOneExactToken(t *testing.T) {
	service, target := newBackupTargetRestoreOperationService(t)
	var payload restoreFromTargetPayload
	service.restoreFromTargetOperationEnqueue = func(_ context.Context, _ string, value any) error {
		payload = value.(restoreFromTargetPayload)
		return nil
	}
	if err := enqueueTargetRestore(context.Background(), service, target.ID, "zroot/restored"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	runEntered := make(chan struct{}, 1)
	releaseRun := make(chan struct{})
	var runs atomic.Int32
	service.restoreFromTargetRun = func(_ context.Context, _ *clusterModels.BackupTarget, got restoreFromTargetPayload) error {
		runs.Add(1)
		if got.OperationToken != payload.OperationToken || got.DestinationDataset != "zroot/restored" {
			t.Errorf("executed payload = %+v", got)
		}
		runEntered <- struct{}{}
		<-releaseRun
		return nil
	}

	tampered := payload
	tampered.Snapshot = "@bk_j1_c1_tampered"
	if err := service.handleRestoreFromTargetQueue(context.Background(), tampered); err != nil {
		t.Fatalf("tampered queue message: %v", err)
	}
	wrongHolder := payload
	wrongHolder.HolderNodeID = "other-node"
	if err := service.handleRestoreFromTargetQueue(context.Background(), wrongHolder); err != nil {
		t.Fatalf("wrong-holder queue message: %v", err)
	}
	if runs.Load() != 0 {
		t.Fatalf("tampered or wrong-holder message executed %d time(s)", runs.Load())
	}

	firstResult := make(chan error, 1)
	go func() { firstResult <- service.handleRestoreFromTargetQueue(context.Background(), payload) }()
	select {
	case <-runEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("first queue message did not execute")
	}
	if err := service.handleRestoreFromTargetQueue(context.Background(), payload); err != nil {
		t.Fatalf("concurrent duplicate: %v", err)
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("runs while first active = %d", got)
	}

	close(releaseRun)
	if err := <-firstResult; err != nil {
		t.Fatalf("first queue result: %v", err)
	}
	if err := service.handleRestoreFromTargetQueue(context.Background(), payload); err != nil {
		t.Fatalf("stale duplicate: %v", err)
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("effective runs = %d, want 1", got)
	}
}

func TestOOBRestoreOperationIDRetryAfterCompletionIsIdempotent(t *testing.T) {
	service, target := newBackupTargetRestoreOperationService(t)
	var payloads []restoreFromTargetPayload
	service.restoreFromTargetOperationEnqueue = func(_ context.Context, _ string, value any) error {
		payloads = append(payloads, value.(restoreFromTargetPayload))
		return nil
	}
	var runs atomic.Int32
	service.restoreFromTargetRun = func(_ context.Context, _ *clusterModels.BackupTarget, _ restoreFromTargetPayload) error {
		runs.Add(1)
		return nil
	}

	if err := enqueueTargetRestoreWithOperationID(
		context.Background(), service, target.ID, "zroot/idempotent", "not-a-uuid",
	); err == nil || !strings.Contains(err.Error(), "restore_operation_id_invalid") {
		t.Fatalf("invalid operation ID error = %v", err)
	}
	operationID := "11111111-2222-4333-8444-555555555555"
	if err := enqueueTargetRestoreWithOperationID(
		context.Background(), service, target.ID, "zroot/idempotent", operationID,
	); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if err := service.handleRestoreFromTargetQueue(context.Background(), payloads[0]); err != nil {
		t.Fatalf("first execution: %v", err)
	}
	if runs.Load() != 1 {
		t.Fatalf("first effective runs = %d", runs.Load())
	}

	if err := enqueueTargetRestoreWithOperationID(
		context.Background(), service, target.ID, "zroot/idempotent", operationID,
	); err != nil {
		t.Fatalf("same-operation retry: %v", err)
	}
	if len(payloads) != 1 {
		t.Fatalf("completed retry published another queue message: %+v", payloads)
	}
	if runs.Load() != 1 {
		t.Fatalf("completed retry produced %d effective runs", runs.Load())
	}
	if err := enqueueTargetRestoreWithOperationID(
		context.Background(), service, target.ID, "zroot/tampered-intent", operationID,
	); err == nil || !strings.Contains(err.Error(), "token_mismatch") {
		t.Fatalf("operation ID reused with different request error = %v", err)
	}

	if err := enqueueTargetRestoreWithOperationID(
		context.Background(), service, target.ID, "zroot/idempotent",
		"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
	); err != nil {
		t.Fatalf("new intentional operation: %v", err)
	}
	if payloads[1].OperationToken == payloads[0].OperationToken {
		t.Fatal("new operation ID reused completed token")
	}
	if err := service.handleRestoreFromTargetQueue(context.Background(), payloads[1]); err != nil {
		t.Fatalf("new intentional execution: %v", err)
	}
	if runs.Load() != 2 {
		t.Fatalf("new operation effective runs = %d", runs.Load())
	}
}

func TestOOBRestoreQueuedRunningAndFinishingRestartReconciliation(t *testing.T) {
	service, target := newBackupTargetRestoreOperationService(t)
	makeOperation := func(destination string) (backupTargetRestoreOperationHandle, restoreFromTargetPayload) {
		t.Helper()
		restoreNetwork := true
		handle, payload, err := service.acquireDurableBackupTargetRestoreOperation(context.Background(), restoreFromTargetPayload{
			TargetID: target.ID, RemoteDataset: "tank/backups/data", Snapshot: "@bk_j1_c1_test",
			DestinationDataset: destination, RestoreNetwork: &restoreNetwork,
		}, "")
		if err != nil {
			t.Fatalf("acquire %s: %v", destination, err)
		}
		payload.OperationToken = handle.Token
		payload.HolderNodeID = handle.HolderNodeID
		return handle, payload
	}

	queuedHandle, _ := makeOperation("zroot/queued")
	runningHandle, runningPayload := makeOperation("zroot/running")
	finishingHandle, _ := makeOperation("zroot/finishing")
	if err := service.transitionDurableBackupTargetRestoreOperation(context.Background(), "start", runningHandle); err != nil {
		t.Fatalf("start running operation: %v", err)
	}
	if err := service.transitionDurableBackupTargetRestoreOperation(context.Background(), "start", finishingHandle); err != nil {
		t.Fatalf("start finishing operation: %v", err)
	}
	if err := service.transitionDurableBackupTargetRestoreOperation(context.Background(), "finish", finishingHandle); err != nil {
		t.Fatalf("finish operation: %v", err)
	}

	restarted := newTestZeltaService(service.DB)
	var requeued []restoreFromTargetPayload
	restarted.restoreFromTargetOperationEnqueue = func(_ context.Context, name string, value any) error {
		if name != restoreFromTargetQueueName {
			t.Fatalf("queue = %q", name)
		}
		requeued = append(requeued, value.(restoreFromTargetPayload))
		return nil
	}
	if err := restarted.ReconcileBackupTargetRestoreOperationsAfterRestart(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(requeued) != 2 {
		t.Fatalf("requeued payloads = %+v", requeued)
	}
	seen := map[string]restoreFromTargetPayload{}
	for _, item := range requeued {
		seen[item.OperationToken] = item
	}
	if _, ok := seen[queuedHandle.Token]; !ok {
		t.Fatalf("queued token was not restored: %+v", seen)
	}
	if got, ok := seen[runningHandle.Token]; !ok || got.DestinationDataset != runningPayload.DestinationDataset {
		t.Fatalf("running token was not restored: %+v", seen)
	}

	var operations []clusterModels.BackupTargetRestoreOperation
	if err := service.DB.Order("destination_dataset ASC").Find(&operations).Error; err != nil {
		t.Fatalf("load reconciled operations: %v", err)
	}
	if len(operations) != 3 {
		t.Fatalf("operations after reconcile = %+v", operations)
	}
	for _, operation := range operations {
		wantState := clusterModels.BackupTargetRestoreOperationQueued
		if operation.Token == finishingHandle.Token {
			wantState = clusterModels.BackupTargetRestoreOperationCompleted
		}
		if operation.State != wantState {
			t.Fatalf("reconciled operation state = %+v, want %s", operation, wantState)
		}
	}
	if _, ok := seen[finishingHandle.Token]; ok {
		t.Fatal("finishing operation was re-enqueued")
	}
}

func TestOOBRestoreConcurrentQueueMessagesHaveSingleQueuedToRunningWinner(t *testing.T) {
	service, target := newBackupTargetRestoreOperationService(t)
	restoreNetwork := true
	handle, payload, err := service.acquireDurableBackupTargetRestoreOperation(context.Background(), restoreFromTargetPayload{
		TargetID: target.ID, RemoteDataset: "tank/backups/data", Snapshot: "@bk_j1_c1_test",
		DestinationDataset: "zroot/cas", RestoreNetwork: &restoreNetwork,
	}, "")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	payload.OperationToken = handle.Token
	payload.HolderNodeID = handle.HolderNodeID

	const contenders = 12
	start := make(chan struct{})
	var winners atomic.Int32
	var failures atomic.Int32
	var wg sync.WaitGroup
	for range contenders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, execute, prepareErr := service.prepareQueuedBackupTargetRestoreOperation(context.Background(), payload)
			if prepareErr != nil {
				failures.Add(1)
				return
			}
			if execute {
				winners.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if failures.Load() != 0 || winners.Load() != 1 {
		t.Fatalf("CAS winners=%d failures=%d", winners.Load(), failures.Load())
	}
	if err := service.finishDurableBackupTargetRestoreOperation(handle); err != nil {
		t.Fatalf("finish: %v", err)
	}
}

func TestLegacyOOBRestoreQueueMessageAcquiresExactReservationBeforeExecution(t *testing.T) {
	service, target := newBackupTargetRestoreOperationService(t)
	restoreNetwork := true
	payload := restoreFromTargetPayload{
		TargetID: target.ID, RemoteDataset: "tank/backups/data", Snapshot: "@bk_j1_c1_legacy",
		DestinationDataset: "zroot/legacy", RestoreNetwork: &restoreNetwork,
	}
	var runs atomic.Int32
	service.restoreFromTargetRun = func(_ context.Context, _ *clusterModels.BackupTarget, got restoreFromTargetPayload) error {
		runs.Add(1)
		if got.DestinationDataset != "zroot/legacy" {
			t.Fatalf("legacy destination = %q", got.DestinationDataset)
		}
		return nil
	}
	if err := service.handleRestoreFromTargetQueue(context.Background(), payload); err != nil {
		t.Fatalf("handle legacy payload: %v", err)
	}
	if runs.Load() != 1 {
		t.Fatalf("legacy effective runs = %d", runs.Load())
	}
	var completed clusterModels.BackupTargetRestoreOperation
	if err := service.DB.First(&completed).Error; err != nil ||
		completed.State != clusterModels.BackupTargetRestoreOperationCompleted {
		t.Fatalf("legacy completion receipt = %+v err=%v", completed, err)
	}
}

func TestOOBRestoreSameRemoteLineageAllowsIndependentDestinations(t *testing.T) {
	service, target := newBackupTargetRestoreOperationService(t)
	var payloads []restoreFromTargetPayload
	service.restoreFromTargetOperationEnqueue = func(_ context.Context, _ string, value any) error {
		payloads = append(payloads, value.(restoreFromTargetPayload))
		return nil
	}
	if err := enqueueTargetRestore(context.Background(), service, target.ID, "zroot/first"); err != nil {
		t.Fatalf("first destination: %v", err)
	}
	if err := enqueueTargetRestore(context.Background(), service, target.ID, "tank/second"); err != nil {
		t.Fatalf("independent destination sharing remote lineage: %v", err)
	}
	if len(payloads) != 2 || payloads[0].OperationToken == payloads[1].OperationToken {
		t.Fatalf("independent payloads = %+v", payloads)
	}
}
