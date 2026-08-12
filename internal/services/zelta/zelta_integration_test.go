// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
)

func installZeltaCounterCache(t *testing.T) {
	t.Helper()
	previous := db.CacheDB
	cache := db.SetupCache(&internal.SylveConfig{DataPath: t.TempDir()})
	t.Cleanup(func() {
		_ = cache.Close()
		db.CacheDB = previous
	})
}

func TestReplicationCounters(t *testing.T) {
	installZeltaCounterCache(t)
	service := &Service{}

	t.Run("crash lifecycle and cap", func(t *testing.T) {
		const policyID = uint(1000)
		defer service.replicationCountersDelete(policyID)
		service.crashMissesReset(policyID)
		if got := service.crashMissesIncr(policyID, 3); got != 1 {
			t.Fatalf("first increment = %d, want 1", got)
		}
		for range 5 {
			service.crashMissesIncr(policyID, 3)
		}
		if got := badgerCounterGet(badgerCrashKey(policyID)); got != 3 {
			t.Fatalf("capped crash count = %d, want 3", got)
		}
		service.crashMissesReset(policyID)
		if got := badgerCounterGet(badgerCrashKey(policyID)); got != 0 {
			t.Fatalf("reset crash count = %d, want 0", got)
		}
	})

	t.Run("policies are independent", func(t *testing.T) {
		const left, right = uint(1100), uint(1200)
		defer service.replicationCountersDelete(left)
		defer service.replicationCountersDelete(right)
		service.crashMissesIncr(left, 5)
		service.crashMissesIncr(left, 5)
		service.crashMissesIncr(right, 5)
		if got := badgerCounterGet(badgerCrashKey(left)); got != 2 {
			t.Fatalf("left count = %d, want 2", got)
		}
		if got := badgerCounterGet(badgerCrashKey(right)); got != 1 {
			t.Fatalf("right count = %d, want 1", got)
		}
	})

	t.Run("down count and deletion", func(t *testing.T) {
		const policyID = uint(1300)
		service.downMissesSet(policyID, 2)
		if got := service.downMissesIncr(policyID, 10); got != 3 {
			t.Fatalf("down count = %d, want 3", got)
		}
		service.replicationCountersDelete(policyID)
		if got := badgerCounterGet(badgerDownKey(policyID)); got != 0 {
			t.Fatalf("deleted down count = %d, want 0", got)
		}
	})
}

func TestFailoverControllerLeaderTickUsesControlledHA(t *testing.T) {
	installZeltaCounterCache(t)
	database := newZeltaServiceTestDB(t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationEvent{},
		&clusterModels.ClusterNode{},
	)
	clusterService := &cluster.Service{DB: database, NodeID: "node-local"}
	service := newTestZeltaService(database)
	service.Cluster = clusterService

	for _, node := range []clusterModels.ClusterNode{
		{NodeUUID: "node-local", Status: "online"},
		{NodeUUID: "node-owner", Status: "offline"},
		{NodeUUID: "node-third", Status: "online"},
	} {
		if err := database.Create(&node).Error; err != nil {
			t.Fatalf("seed node %s: %v", node.NodeUUID, err)
		}
	}
	policies := []clusterModels.ReplicationPolicy{
		{
			ID: 7001, Name: "offline-auto-safe", GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 7001, SourceNodeID: "node-owner", ActiveNodeID: "node-owner", OwnerEpoch: 1,
			FailoverMode: clusterModels.ReplicationFailoverAutoSafe, Enabled: true,
			ProtectionState: clusterModels.ReplicationProtectionStateArmed,
			Targets:         []clusterModels.ReplicationPolicyTarget{{NodeID: "node-local", Weight: 100}},
		},
		{
			ID: 7002, Name: "offline-manual", GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 7002, SourceNodeID: "node-owner", ActiveNodeID: "node-owner", OwnerEpoch: 1,
			FailoverMode: clusterModels.ReplicationFailoverManual, Enabled: true,
			ProtectionState: clusterModels.ReplicationProtectionStateArmed,
			Targets:         []clusterModels.ReplicationPolicyTarget{{NodeID: "node-local", Weight: 100}},
		},
		{
			ID: 7003, Name: "healthy", GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 7003, SourceNodeID: "node-local", ActiveNodeID: "node-local", OwnerEpoch: 1,
			FailoverMode: clusterModels.ReplicationFailoverAutoSafe, Enabled: true,
			ProtectionState: clusterModels.ReplicationProtectionStateArmed,
			Targets:         []clusterModels.ReplicationPolicyTarget{{NodeID: "node-third", Weight: 100}},
		},
	}
	for i := range policies {
		if err := clusterModels.UpsertReplicationPolicyTxn(database, &policies[i], policies[i].Targets); err != nil {
			t.Fatalf("seed policy %d: %v", policies[i].ID, err)
		}
		defer service.replicationCountersDelete(policies[i].ID)
	}
	service.downMissesSet(7002, uint64(replicationFailoverDownMissLimit-1))
	service.downMissesSet(7003, 2)
	evaluate := func(*clusterModels.ReplicationPolicy) cluster.ReplicationPolicyHAEvaluation {
		return cluster.ReplicationPolicyHAEvaluation{Eligible: true}
	}

	if err := service.runFailoverControllerLeaderTick(t.Context(), evaluate); err != nil {
		t.Fatalf("controller tick: %v", err)
	}
	if got := badgerCounterGet(badgerDownKey(7001)); got != 1 {
		t.Fatalf("offline auto-safe count = %d, want 1", got)
	}
	if got := badgerCounterGet(badgerDownKey(7002)); got != uint64(replicationFailoverDownMissLimit) {
		t.Fatalf("offline manual count = %d, want %d", got, replicationFailoverDownMissLimit)
	}
	if got := badgerCounterGet(badgerDownKey(7003)); got != 0 {
		t.Fatalf("healthy owner count = %d, want 0", got)
	}
	var manual clusterModels.ReplicationPolicy
	if err := database.First(&manual, 7002).Error; err != nil {
		t.Fatal(err)
	}
	if transitionStateInProgress(manual.TransitionState) {
		t.Fatalf("manual policy transitioned: %+v", manual)
	}
}
