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
func TestIntegrationMoveGuestIdentityOwnerMovesDisabledPolicyAtomically(t *testing.T) {
	fx := SetupZeltaClusterFixture(
		t,
		2,
		&clusterModels.GuestIdentityRegistry{},
		&clusterModels.GuestIdentityClaim{},
	)
	leader := fx.LeaderNode()
	if leader == nil {
		t.Fatal("leader unavailable")
	}
	var target *zeltaRaftNode
	for _, node := range fx.Nodes {
		if node != leader {
			target = node
			break
		}
	}
	if target == nil {
		t.Fatal("target unavailable")
	}

	now := time.Now().UTC()
	policy := clusterModels.ReplicationPolicy{
		ID: 41, Name: "disabled-migration-policy",
		GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 501,
		SourceNodeID: leader.id, ActiveNodeID: leader.id, OwnerEpoch: 1,
		SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr:     "0 * * * *", Enabled: false,
		ProtectionState: clusterModels.ReplicationProtectionStateUnprotected,
		TransitionState: clusterModels.ReplicationTransitionStateNone,
	}
	operation := clusterModels.ReplicationGuestOperation{
		GuestType: policy.GuestType, GuestID: policy.GuestID,
		Operation: clusterModels.ReplicationGuestOperationMigration,
		State:     clusterModels.ReplicationGuestOperationCutover,
		Token:     "migration:source:501", OwnerNodeID: leader.id, TargetNodeID: target.id,
		TaskID: 501, AcquiredAt: now, SealedAt: &now,
	}
	for _, node := range fx.Nodes {
		if err := node.db.Create(&clusterModels.GuestIdentityRegistry{
			ID:      clusterModels.GuestIdentityRegistryID,
			Version: clusterModels.GuestIdentityRegistryVersion,
			Phase:   clusterModels.GuestIdentityRegistryPhaseActive,
		}).Error; err != nil {
			t.Fatalf("seed registry on %s: %v", node.id, err)
		}
		if err := node.db.Create(&clusterModels.GuestIdentityClaim{
			GuestID: policy.GuestID, GuestKind: policy.GuestType,
			OwnerNodeID: leader.id, Token: "claim-generation-1",
		}).Error; err != nil {
			t.Fatalf("seed claim on %s: %v", node.id, err)
		}
		if err := node.db.Create(&policy).Error; err != nil {
			t.Fatalf("seed policy on %s: %v", node.id, err)
		}
		if err := node.db.Create(&operation).Error; err != nil {
			t.Fatalf("seed operation on %s: %v", node.id, err)
		}
	}

	service := &Service{DB: leader.db, Cluster: leader.cService}
	if err := service.MoveGuestIdentityOwner(
		context.Background(), policy.GuestType, policy.GuestID, target.id, operation.Token,
	); err != nil {
		t.Fatalf("move migration ownership: %v", err)
	}
	fx.WaitForCondition(8*time.Second, "disabled policy ownership replication", func() bool {
		for _, node := range fx.Nodes {
			var gotPolicy clusterModels.ReplicationPolicy
			var claim clusterModels.GuestIdentityClaim
			if node.db.First(&gotPolicy, policy.ID).Error != nil ||
				node.db.First(&claim, policy.GuestID).Error != nil ||
				gotPolicy.ActiveNodeID != target.id || gotPolicy.SourceNodeID != target.id ||
				gotPolicy.OwnerEpoch != 2 || claim.OwnerNodeID != target.id {
				return false
			}
		}
		return true
	})
	if err := service.MoveGuestIdentityOwner(
		context.Background(), policy.GuestType, policy.GuestID, target.id, operation.Token,
	); err != nil {
		t.Fatalf("replay migration ownership: %v", err)
	}
}
