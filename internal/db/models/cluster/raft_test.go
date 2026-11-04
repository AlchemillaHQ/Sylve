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
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/raft"
	"gorm.io/gorm"
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

func TestFSMDispatcherNoteCommandsRejectMissingAndPartialMutations(t *testing.T) {
	db := newClusterModelTestDB(t, &ClusterNote{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	notes := []ClusterNote{
		{ID: 1, Title: "first", Content: "first content"},
		{ID: 2, Title: "second", Content: "second content"},
	}
	if err := db.Create(&notes).Error; err != nil {
		t.Fatalf("failed to seed notes: %v", err)
	}

	missingUpdate, _ := json.Marshal(map[string]any{
		"id": 999, "title": "missing", "content": "missing content",
	})
	if err := applyFSMCommand(t, fsm, Command{
		Type: "note", Action: "update", Data: missingUpdate,
	}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing update error=%v want=%v", err, gorm.ErrRecordNotFound)
	}

	missingDelete, _ := json.Marshal(map[string]any{"id": 999})
	if err := applyFSMCommand(t, fsm, Command{
		Type: "note", Action: "delete", Data: missingDelete,
	}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing delete error=%v want=%v", err, gorm.ErrRecordNotFound)
	}

	partialBulkDelete, _ := json.Marshal(map[string]any{"ids": []int{1, 999}})
	if err := applyFSMCommand(t, fsm, Command{
		Type: "note", Action: "bulk_delete", Data: partialBulkDelete,
	}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("partial bulk delete error=%v want=%v", err, gorm.ErrRecordNotFound)
	}

	var stored []ClusterNote
	if err := db.Order("id ASC").Find(&stored).Error; err != nil {
		t.Fatalf("failed to load notes after rejected mutations: %v", err)
	}
	if len(stored) != 2 || stored[0].ID != 1 || stored[1].ID != 2 {
		t.Fatalf("rejected mutations changed notes: %+v", stored)
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

// A handler that fails part-way must leave nothing behind on any node.
func TestFSMDispatcherRollsBackFailedHandler(t *testing.T) {
	db := newClusterModelTestDB(t, &ClusterNote{})
	fsm := NewFSMDispatcher(db)
	fsm.Register("partial", func(tx *gorm.DB, _ string, _ json.RawMessage) error {
		if err := tx.Create(&ClusterNote{ID: 7, Title: "first write"}).Error; err != nil {
			return err
		}
		return errors.New("second write failed")
	})

	if err := applyFSMCommand(t, fsm, Command{Type: "partial", Action: "run", Data: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("failed handler was accepted")
	}
	var notes int64
	if err := db.Model(&ClusterNote{}).Count(&notes).Error; err != nil {
		t.Fatal(err)
	}
	if notes != 0 {
		t.Fatalf("partial write kept: %d notes", notes)
	}
}

// After a restart Raft replays entries the database already holds. Note IDs
// come from MAX(id)+1, so applying one twice would create a second note.
func TestFSMDispatcherSkipsEntriesAlreadyApplied(t *testing.T) {
	db := newClusterModelTestDB(t, &ClusterNote{})
	note, _ := json.Marshal(ClusterNote{Title: "once", Content: "x"})
	entry, _ := json.Marshal(Command{Type: "note", Action: "create", Data: note})
	log := &raft.Log{Type: raft.LogCommand, Index: 7, Data: entry}

	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)
	if resp := fsm.Apply(log); resp != nil {
		t.Fatalf("apply: %v", resp)
	}

	restarted := NewFSMDispatcher(db)
	RegisterDefaultHandlers(restarted)
	if restarted.AppliedIndex() != 7 {
		t.Fatalf("applied index after restart = %d, want 7", restarted.AppliedIndex())
	}
	if resp := restarted.Apply(log); resp != nil {
		t.Fatalf("replay: %v", resp)
	}
	var notes int64
	if err := db.Model(&ClusterNote{}).Count(&notes).Error; err != nil {
		t.Fatal(err)
	}
	if notes != 1 {
		t.Fatalf("notes after replay = %d, want 1", notes)
	}

	// A wiped log restarts at index 1, so the recorded index must go with it.
	if err := restarted.ResetAppliedIndex(); err != nil {
		t.Fatal(err)
	}
	if persisted, err := restarted.HasPersistedAppliedIndex(); err != nil || persisted {
		t.Fatalf("persisted=%v err=%v after reset", persisted, err)
	}
}
