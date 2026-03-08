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
)

func TestFSMDispatcherNoteCommands(t *testing.T) {
	db := newClusterModelTestDB(t, &ClusterNote{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	createPayload, _ := json.Marshal(map[string]any{
		"title":   "first",
		"content": "one",
	})
	if err := applyFSMCommand(t, fsm, Command{
		Type:   "note",
		Action: "create",
		Data:   createPayload,
	}); err != nil {
		t.Fatalf("create apply failed: %v", err)
	}

	var created ClusterNote
	if err := db.First(&created, 1).Error; err != nil {
		t.Fatalf("failed to fetch created note: %v", err)
	}
	if created.Title != "first" || created.Content != "one" {
		t.Fatalf("create mismatch: got title=%q content=%q", created.Title, created.Content)
	}

	updatePayload, _ := json.Marshal(map[string]any{
		"id":      1,
		"title":   "first-updated",
		"content": "one-updated",
	})
	if err := applyFSMCommand(t, fsm, Command{
		Type:   "note",
		Action: "update",
		Data:   updatePayload,
	}); err != nil {
		t.Fatalf("update apply failed: %v", err)
	}

	var updated ClusterNote
	if err := db.First(&updated, 1).Error; err != nil {
		t.Fatalf("failed to fetch updated note: %v", err)
	}
	if updated.Title != "first-updated" || updated.Content != "one-updated" {
		t.Fatalf("update mismatch: got title=%q content=%q", updated.Title, updated.Content)
	}

	createTwoPayload, _ := json.Marshal(map[string]any{
		"title":   "second",
		"content": "two",
	})
	if err := applyFSMCommand(t, fsm, Command{
		Type:   "note",
		Action: "create",
		Data:   createTwoPayload,
	}); err != nil {
		t.Fatalf("second create apply failed: %v", err)
	}

	createThreePayload, _ := json.Marshal(map[string]any{
		"title":   "third",
		"content": "three",
	})
	if err := applyFSMCommand(t, fsm, Command{
		Type:   "note",
		Action: "create",
		Data:   createThreePayload,
	}); err != nil {
		t.Fatalf("third create apply failed: %v", err)
	}

	bulkDeletePayload, _ := json.Marshal(map[string]any{
		"ids": []int{2, 3},
	})
	if err := applyFSMCommand(t, fsm, Command{
		Type:   "note",
		Action: "bulk_delete",
		Data:   bulkDeletePayload,
	}); err != nil {
		t.Fatalf("bulk delete apply failed: %v", err)
	}

	deletePayload, _ := json.Marshal(map[string]any{
		"id": 1,
	})
	if err := applyFSMCommand(t, fsm, Command{
		Type:   "note",
		Action: "delete",
		Data:   deletePayload,
	}); err != nil {
		t.Fatalf("delete apply failed: %v", err)
	}

	var count int64
	if err := db.Model(&ClusterNote{}).Count(&count).Error; err != nil {
		t.Fatalf("failed to count notes after deletions: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 notes after delete + bulk delete, found %d", count)
	}
}

func TestFSMDispatcherCommandErrors(t *testing.T) {
	db := newClusterModelTestDB(t, &ClusterNote{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	t.Run("malformed command payload", func(t *testing.T) {
		err := applyFSMRaftLog(t, fsm, []byte("{invalid"))
		if err == nil {
			t.Fatal("expected error for malformed command payload, got nil")
		}
		if !strings.Contains(err.Error(), "unmarshal") {
			t.Fatalf("expected unmarshal error, got: %v", err)
		}
	})

	t.Run("unknown command type", func(t *testing.T) {
		raw, _ := json.Marshal(Command{
			Type:   "unknown_type",
			Action: "create",
			Data:   json.RawMessage(`{"x":1}`),
		})

		err := applyFSMRaftLog(t, fsm, raw)
		if err == nil {
			t.Fatal("expected error for unknown command type, got nil")
		}
		if !strings.Contains(err.Error(), "no handler for unknown_type") {
			t.Fatalf("unexpected unknown type error: %v", err)
		}
	})

	t.Run("malformed note action payload", func(t *testing.T) {
		err := applyFSMCommand(t, fsm, Command{
			Type:   "note",
			Action: "create",
			Data:   json.RawMessage(`"bad-payload"`),
		})
		if err == nil {
			t.Fatal("expected handler error for malformed note payload, got nil")
		}
		if !strings.Contains(err.Error(), "handler") {
			t.Fatalf("expected handler wrapped error, got: %v", err)
		}
	})
}
