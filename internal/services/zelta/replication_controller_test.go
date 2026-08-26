// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"reflect"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestFailoverWarningsAreDeduplicatedUntilOwnerRecovers(t *testing.T) {
	svc := &Service{}
	if !svc.reserveFailoverWarning(17, "node-a", "owner_unreachable") {
		t.Fatal("first warning should be emitted")
	}
	if svc.reserveFailoverWarning(17, "node-a", "owner_unreachable") {
		t.Fatal("duplicate warning should be suppressed")
	}
	if !svc.reserveFailoverWarning(17, "node-a", "no_healthy_target") {
		t.Fatal("a different warning reason should be emitted")
	}
	if !svc.reserveFailoverWarning(17, "node-b", "owner_unreachable") {
		t.Fatal("a different owner should define a different outage warning")
	}

	svc.clearFailoverWarnings(17)
	if !svc.reserveFailoverWarning(17, "node-a", "owner_unreachable") {
		t.Fatal("warning should be emitted again after owner recovery clears the outage")
	}
}

func TestCrashRecoveryAttemptsConfiguredRestartCountBeforeFailover(t *testing.T) {
	const limit = 3
	for observation := uint64(1); observation <= limit; observation++ {
		if !shouldAttemptLocalCrashRestart(observation, limit) {
			t.Fatalf("observation %d should attempt a local restart", observation)
		}
	}
	for _, observation := range []uint64{limit + 1, limit + 2} {
		if shouldAttemptLocalCrashRestart(observation, limit) {
			t.Fatalf("observation %d should retry failover instead of restarting locally", observation)
		}
	}
}

func TestPoolHealthCounterIsScopedByPolicyAndPool(t *testing.T) {
	left := replicationPoolHealthCounterKey(1, "zroot")
	right := replicationPoolHealthCounterKey(2, "zroot")
	otherPool := replicationPoolHealthCounterKey(1, "tank")
	if left == right || left == otherPool || right == otherPool {
		t.Fatalf("pool health keys must be unique per policy and pool: %q %q %q", left, right, otherPool)
	}
}

func TestReplicationGuestPoolsLoadsJailStorages(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &jailModels.Jail{}, &jailModels.Storage{})
	jail := jailModels.Jail{CTID: 107, Name: "pool-health-jail", Hostname: "pool-health-jail"}
	if err := database.Create(&jail).Error; err != nil {
		t.Fatalf("create jail: %v", err)
	}
	storages := []jailModels.Storage{
		{JailID: jail.ID, Pool: "zroot", GUID: "pool-health-zroot", Name: "root", IsBase: true},
		{JailID: jail.ID, Pool: "tank", GUID: "pool-health-tank", Name: "data"},
		{JailID: jail.ID, Pool: "zroot", GUID: "pool-health-zroot-home", Name: "home"},
	}
	if err := database.Create(&storages).Error; err != nil {
		t.Fatalf("create jail storages: %v", err)
	}

	svc := &Service{DB: database}
	pools, err := svc.replicationGuestPools(&clusterModels.ReplicationPolicy{
		GuestType: clusterModels.ReplicationGuestTypeJail,
		GuestID:   jail.CTID,
	})
	if err != nil {
		t.Fatalf("load jail pools: %v", err)
	}
	if want := []string{"tank", "zroot"}; !reflect.DeepEqual(pools, want) {
		t.Fatalf("jail pools = %v, want %v", pools, want)
	}
}
