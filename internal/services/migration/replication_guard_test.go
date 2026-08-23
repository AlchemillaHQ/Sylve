// SPDX-License-Identifier: BSD-2-Clause

package migration

import (
	"context"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestRequireReplicationDisabledForMigration(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &clusterModels.ReplicationPolicy{})
	svc := &Service{DB: db}
	ctx := context.Background()

	if err := svc.requireReplicationDisabledForMigration(ctx, taskModels.GuestTypeVM, 101); err != nil {
		t.Fatalf("unprotected guest rejected: %v", err)
	}

	disabled := clusterModels.ReplicationPolicy{
		Name:            "disabled",
		GuestType:       taskModels.GuestTypeVM,
		GuestID:         101,
		Enabled:         false,
		TransitionState: clusterModels.ReplicationTransitionStateCompleted,
	}
	if err := db.Create(&disabled).Error; err != nil {
		t.Fatalf("create disabled policy: %v", err)
	}
	if err := svc.requireReplicationDisabledForMigration(ctx, taskModels.GuestTypeVM, 101); err != nil {
		t.Fatalf("disabled policy rejected: %v", err)
	}

	if err := db.Model(&disabled).Update("enabled", true).Error; err != nil {
		t.Fatalf("enable policy: %v", err)
	}
	if err := svc.requireReplicationDisabledForMigration(ctx, taskModels.GuestTypeVM, 101); err == nil ||
		!strings.Contains(err.Error(), replicationPolicyMustBeDisabledReason) {
		t.Fatalf("enabled policy was not rejected: %v", err)
	}
}

func TestRequireReplicationDisabledForMigrationFailsClosed(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t)
	svc := &Service{DB: db}
	err := svc.requireReplicationDisabledForMigration(context.Background(), taskModels.GuestTypeJail, 202)
	if err == nil || !strings.Contains(err.Error(), "replication_policy_lookup_failed") {
		t.Fatalf("missing policy table did not fail closed: %v", err)
	}
}

func TestExecuteMigrationRechecksReplicationPolicyBeforeSideEffects(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&clusterModels.ReplicationPolicy{},
		&taskModels.GuestLifecycleTask{},
	)
	policy := clusterModels.ReplicationPolicy{
		Name:            "protected-jail",
		GuestType:       taskModels.GuestTypeJail,
		GuestID:         303,
		Enabled:         true,
		TransitionState: clusterModels.ReplicationTransitionStateRollingBack,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}
	task := taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeJail,
		GuestID:   303,
		Action:    "migrate",
		Source:    taskModels.LifecycleTaskSourceUser,
		Status:    taskModels.LifecycleTaskStatusQueued,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}

	svc := &Service{DB: db}
	err := svc.ExecuteMigration(context.Background(), task.ID)
	if err == nil || !strings.Contains(err.Error(), replicationPolicyMustBeDisabledReason) {
		t.Fatalf("queued protected migration was not rejected: %v", err)
	}

	var got taskModels.GuestLifecycleTask
	if err := db.First(&got, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if got.Status != taskModels.LifecycleTaskStatusFailed || got.StartedAt != nil {
		t.Fatalf("unexpected task state after early rejection: status=%q startedAt=%v", got.Status, got.StartedAt)
	}
	if !strings.Contains(got.Error, replicationPolicyMustBeDisabledReason) {
		t.Fatalf("task error does not explain the guard: %q", got.Error)
	}
}
