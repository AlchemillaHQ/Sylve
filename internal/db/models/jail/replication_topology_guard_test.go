// SPDX-License-Identifier: BSD-2-Clause

package jailModels

import (
	"context"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestReplicationStorageTopologyGuard(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &Jail{}, &Storage{}, &clusterModels.ReplicationPolicy{})
	jail := Jail{CTID: 5101, Name: "protected-jail"}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("create jail: %v", err)
	}
	storage := Storage{JailID: jail.ID, Pool: "tank", GUID: "guid-5101", Name: "root", IsBase: true}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatalf("create storage: %v", err)
	}
	policy := clusterModels.ReplicationPolicy{
		Name:            "jail-policy",
		GuestType:       clusterModels.ReplicationGuestTypeJail,
		GuestID:         jail.CTID,
		Enabled:         true,
		TransitionState: clusterModels.ReplicationTransitionStateCompleted,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}

	storage.Name = "renamed"
	if err := db.Save(&storage).Error; err == nil || !strings.Contains(err.Error(), "requires_policy_disabled") {
		t.Fatalf("protected storage update was not blocked: %v", err)
	}
	if err := db.Delete(&Storage{}, storage.ID).Error; err == nil || !strings.Contains(err.Error(), "requires_policy_disabled") {
		t.Fatalf("protected storage delete was not blocked: %v", err)
	}
	if err := db.Model(&Storage{}).Where("id = ?", storage.ID).Update("name", "bulk-renamed").Error; err == nil {
		t.Fatal("protected bulk storage update bypassed topology guard")
	}

	if err := db.Model(&policy).Updates(map[string]any{
		"transition_state":  clusterModels.ReplicationTransitionStatePromoting,
		"transition_run_id": "run-5101",
	}).Error; err != nil {
		t.Fatalf("begin transition: %v", err)
	}
	if err := db.WithContext(clusterModels.WithReplicationTransitionAuthority(context.Background(), "wrong-run", policy.OwnerEpoch)).
		Save(&storage).Error; err == nil {
		t.Fatal("mismatched transition run bypassed topology guard")
	}
	if err := db.WithContext(clusterModels.WithReplicationTransitionAuthority(context.Background(), "run-5101", policy.OwnerEpoch+1)).
		Save(&storage).Error; err == nil {
		t.Fatal("mismatched transition epoch bypassed topology guard")
	}
	if err := db.WithContext(clusterModels.WithReplicationTransitionAuthority(context.Background(), "run-5101", policy.OwnerEpoch)).
		Save(&storage).Error; err != nil {
		t.Fatalf("exact transition reconciliation was blocked: %v", err)
	}
}
