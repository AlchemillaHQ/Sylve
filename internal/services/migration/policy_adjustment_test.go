// SPDX-License-Identifier: BSD-2-Clause

package migration

import (
	"context"
	"errors"
	"strings"
	"testing"

	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
)

type migrationWorkloadGuardStub struct {
	ownershipErr   error
	ownershipToken string
	ownershipCalls int
	abortFn        func(context.Context, string, uint, string) error
	completeFn     func(context.Context, string, uint, string, string) error
}

func (m *migrationWorkloadGuardStub) AcquireGuestLock(string, uint, string) (bool, string) {
	return true, ""
}

func (m *migrationWorkloadGuardStub) ReleaseGuestLock(string, uint) {}

func (m *migrationWorkloadGuardStub) AcquireGuestMigrationInterlock(context.Context, string, uint, string, uint, string) error {
	return nil
}

func (m *migrationWorkloadGuardStub) WaitGuestMigrationInterlockAcquired(context.Context, string, uint, string, string) error {
	return nil
}

func (m *migrationWorkloadGuardStub) SealGuestMigrationInterlock(context.Context, string, uint, string) error {
	return nil
}

func (m *migrationWorkloadGuardStub) WaitGuestMigrationInterlockApplied(context.Context, string, uint, string, string) error {
	return nil
}

func (m *migrationWorkloadGuardStub) AbortGuestMigrationInterlock(ctx context.Context, guestType string, guestID uint, token string) error {
	if m.abortFn != nil {
		return m.abortFn(ctx, guestType, guestID, token)
	}
	return nil
}

func (m *migrationWorkloadGuardStub) CompleteGuestMigrationInterlock(ctx context.Context, guestType string, guestID uint, targetNodeID, token string) error {
	if m.completeFn != nil {
		return m.completeFn(ctx, guestType, guestID, targetNodeID, token)
	}
	return nil
}

func (m *migrationWorkloadGuardStub) MigrateGuestOwnership(
	_ context.Context,
	_ string,
	_ uint,
	_ string,
	operationToken ...string,
) error {
	m.ownershipCalls++
	if len(operationToken) == 1 {
		m.ownershipToken = operationToken[0]
	}
	return m.ownershipErr
}

func TestPhasePolicyAdjustmentFailsClosed(t *testing.T) {
	task := taskModels.GuestLifecycleTask{GuestType: taskModels.GuestTypeVM, GuestID: 501}
	mp := &migrationPayload{TargetNodeUUID: "node-b"}

	if err := (&Service{}).phasePolicyAdjustment(context.Background(), mp, task, "migration:node-a:1"); err == nil ||
		!strings.Contains(err.Error(), "guard_unavailable") {
		t.Fatalf("missing ownership guard was not fatal: %v", err)
	}

	stub := &migrationWorkloadGuardStub{ownershipErr: errors.New("raft unavailable")}
	svc := &Service{WorkloadGuard: stub}
	if err := svc.phasePolicyAdjustment(context.Background(), mp, task, "migration:node-a:1"); err == nil ||
		!strings.Contains(err.Error(), "raft unavailable") {
		t.Fatalf("ownership reassignment failure was swallowed: %v", err)
	}
	if stub.ownershipCalls != 1 || stub.ownershipToken != "migration:node-a:1" {
		t.Fatalf("unexpected ownership call: calls=%d token=%q", stub.ownershipCalls, stub.ownershipToken)
	}
}
