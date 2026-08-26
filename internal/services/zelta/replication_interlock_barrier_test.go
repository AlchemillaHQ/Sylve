// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestGuestMigrationInterlockApplyBarriersUseExactLocalState(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &clusterModels.ReplicationGuestOperation{})
	clusterSvc := &clusterService.Service{DB: db}
	localNodeID := strings.TrimSpace(clusterSvc.LocalNodeID())
	if localNodeID == "" {
		t.Skip("local system UUID is unavailable")
	}

	const (
		guestID = uint(701)
		token   = "migration:source:701"
	)
	operation := clusterModels.ReplicationGuestOperation{
		GuestType:    clusterModels.ReplicationGuestTypeVM,
		GuestID:      guestID,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		State:        clusterModels.ReplicationGuestOperationPreCutover,
		Token:        token,
		OwnerNodeID:  localNodeID,
		TargetNodeID: localNodeID,
		TaskID:       701,
		AcquiredAt:   time.Now().UTC(),
	}
	if err := db.Create(&operation).Error; err != nil {
		t.Fatalf("seed migration operation: %v", err)
	}

	svc := &Service{DB: db, Cluster: clusterSvc}
	if err := svc.WaitGuestMigrationInterlockAcquired(
		context.Background(), operation.GuestType, guestID, localNodeID, token,
	); err != nil {
		t.Fatalf("pre-cutover apply barrier rejected exact row: %v", err)
	}

	wrongTokenCtx, cancelWrongToken := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelWrongToken()
	if err := svc.WaitGuestMigrationInterlockAcquired(
		wrongTokenCtx, operation.GuestType, guestID, localNodeID, "stale-token",
	); err == nil || !strings.Contains(err.Error(), "migration_interlock_apply_barrier_timeout") {
		t.Fatalf("pre-cutover apply barrier accepted stale token: %v", err)
	}

	if err := db.Model(&clusterModels.ReplicationGuestOperation{}).
		Where("guest_type = ? AND guest_id = ?", operation.GuestType, guestID).
		Update("state", clusterModels.ReplicationGuestOperationCutover).Error; err != nil {
		t.Fatalf("seal migration operation: %v", err)
	}
	if err := svc.WaitGuestMigrationInterlockApplied(
		context.Background(), operation.GuestType, guestID, localNodeID, token,
	); err != nil {
		t.Fatalf("cutover apply barrier rejected exact row: %v", err)
	}
}
