// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFSMDispatcherReplicationEventCommands(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationEvent{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	t.Run("create new event", func(t *testing.T) {
		now := time.Now()
		raw, _ := json.Marshal(ReplicationEvent{
			ID: 1, PolicyID: ptr[uint](1), EventType: "run", Status: "running",
			TransitionRunID: "transition-1",
			SourceNodeID:    "node-1", TargetNodeID: "node-2",
			GuestType: ReplicationGuestTypeVM, GuestID: 100,
			StartedAt: now,
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "replication_event", Action: "create", Data: raw,
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}

		var event ReplicationEvent
		if err := db.First(&event, 1).Error; err != nil {
			t.Fatalf("fetch event: %v", err)
		}
		if event.EventType != "run" || event.Status != "running" {
			t.Fatalf("event mismatch: type=%q status=%q", event.EventType, event.Status)
		}
		if event.TransitionRunID != "transition-1" {
			t.Fatalf("transition run ID mismatch: %q", event.TransitionRunID)
		}
		if event.SourceNodeID != "node-1" || event.TargetNodeID != "node-2" {
			t.Fatalf("node mismatch: src=%q tgt=%q", event.SourceNodeID, event.TargetNodeID)
		}
	})

	t.Run("create with id=0 returns error", func(t *testing.T) {
		raw, _ := json.Marshal(ReplicationEvent{
			ID: 0, EventType: "run", Status: "running",
		})
		err := applyFSMCommand(t, fsm, Command{
			Type: "replication_event", Action: "create", Data: raw,
		})
		if err == nil {
			t.Fatal("expected error for id=0, got nil")
		}
		if !strings.Contains(err.Error(), "replication_event_id_required") {
			t.Fatalf("expected id required error, got: %v", err)
		}
	})

	t.Run("update existing event via OnConflict upsert", func(t *testing.T) {
		raw, _ := json.Marshal(ReplicationEvent{
			ID: 1, PolicyID: ptr[uint](2), EventType: "run", Status: "success",
			TransitionRunID: "transition-1", Message: "completed successfully",
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "replication_event", Action: "update", Data: raw,
		}); err != nil {
			t.Fatalf("update failed: %v", err)
		}

		var event ReplicationEvent
		db.First(&event, 1)
		if event.Status != "success" {
			t.Fatalf("status not updated: %q", event.Status)
		}
		if event.Message != "completed successfully" {
			t.Fatalf("message not updated: %q", event.Message)
		}
		if event.PolicyID == nil || *event.PolicyID != 2 {
			t.Fatalf("policy_id not updated")
		}
		if event.TransitionRunID != "transition-1" {
			t.Fatalf("transition run ID was not preserved: %q", event.TransitionRunID)
		}
	})

	t.Run("backfill legacy event transition run ID", func(t *testing.T) {
		legacy := ReplicationEvent{
			ID: 3, PolicyID: ptr[uint](3), EventType: "failover", Status: "promoting",
			StartedAt: time.Now().UTC(),
		}
		if err := db.Create(&legacy).Error; err != nil {
			t.Fatalf("create legacy event: %v", err)
		}
		legacy.TransitionRunID = "transition-legacy"
		raw, _ := json.Marshal(legacy)
		if err := applyFSMCommand(t, fsm, Command{
			Type: "replication_event", Action: "update", Data: raw,
		}); err != nil {
			t.Fatalf("backfill failed: %v", err)
		}

		var stored ReplicationEvent
		if err := db.First(&stored, legacy.ID).Error; err != nil {
			t.Fatalf("load backfilled event: %v", err)
		}
		if stored.TransitionRunID != "transition-legacy" {
			t.Fatalf("transition run ID was not backfilled: %q", stored.TransitionRunID)
		}
	})

	t.Run("create with minimum required fields", func(t *testing.T) {
		db2 := newClusterModelTestDB(t, &ReplicationEvent{})
		fsm2 := NewFSMDispatcher(db2)
		RegisterDefaultHandlers(fsm2)

		now := time.Now()
		raw, _ := json.Marshal(ReplicationEvent{
			ID: 2, EventType: "failover", Status: "running",
			StartedAt: now,
		})
		if err := applyFSMCommand(t, fsm2, Command{
			Type: "replication_event", Action: "create", Data: raw,
		}); err != nil {
			t.Fatalf("create minimal event failed: %v", err)
		}

		var event ReplicationEvent
		db2.First(&event, 2)
		if event.EventType != "failover" || event.Status != "running" {
			t.Fatalf("event mismatch: type=%q status=%q", event.EventType, event.Status)
		}
	})

	t.Run("malformed payload returns error", func(t *testing.T) {
		err := applyFSMCommand(t, fsm, Command{
			Type: "replication_event", Action: "create",
			Data: json.RawMessage(`"bad"`),
		})
		if err == nil {
			t.Fatal("expected error for malformed payload, got nil")
		}
	})
}
