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
	"testing"
	"time"
)

func TestFSMDispatcherReplicationTransitionEventCommands(t *testing.T) {
	db := newClusterModelTestDB(t, &ReplicationEvent{}, &ReplicationTransitionEvent{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	startedAt := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	event := ReplicationTransitionEvent{
		ID: 10, PolicyID: ptr[uint](3), TransitionRunID: "transition-10",
		EventType: "failover", Status: "demoting", StartedAt: startedAt,
	}
	raw, _ := json.Marshal(event)
	if err := applyFSMCommand(t, fsm, Command{
		Type: "replication_transition_event", Action: "create", Data: raw,
	}); err != nil {
		t.Fatalf("create transition event: %v", err)
	}

	event.Status = "active"
	completedAt := startedAt.Add(time.Minute)
	event.CompletedAt = &completedAt
	raw, _ = json.Marshal(event)
	if err := applyFSMCommand(t, fsm, Command{
		Type: "replication_transition_event", Action: "update", Data: raw,
	}); err != nil {
		t.Fatalf("update transition event: %v", err)
	}

	var stored ReplicationTransitionEvent
	if err := db.First(&stored, event.ID).Error; err != nil {
		t.Fatalf("load transition event: %v", err)
	}
	if stored.Status != "active" || stored.CompletedAt == nil ||
		stored.TransitionRunID != "transition-10" {
		t.Fatalf("transition event mismatch: %+v", stored)
	}

	var localCount int64
	if err := db.Model(&ReplicationEvent{}).Count(&localCount).Error; err != nil {
		t.Fatalf("count local events: %v", err)
	}
	if localCount != 0 {
		t.Fatalf("new transition command wrote local telemetry: %d", localCount)
	}
}
