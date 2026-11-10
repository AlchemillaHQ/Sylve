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
	"time"

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

	t.Run("command without the leader's time", func(t *testing.T) {
		note, _ := json.Marshal(ClusterNote{Title: "untimed", Content: "x"})
		raw, _ := json.Marshal(Command{Version: CurrentCommandVersion, Type: "note", Action: "create", Data: note})
		if err := applyFSMRaftLog(t, fsm, raw); err == nil {
			t.Fatal("applied a command without the leader's time")
		}
		var count int64
		if err := db.Model(&ClusterNote{}).Where("title = ?", "untimed").Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("notes written: %d, %v", count, err)
		}
	})

	t.Run("commands this build can't apply", func(t *testing.T) {
		note, _ := json.Marshal(ClusterNote{Title: "unknown", Content: "x"})
		for _, command := range []Command{
			{Type: "note", Action: "create", Data: note},
			{Version: CurrentCommandVersion + 1, DecidedAt: time.Now(), Type: "note", Action: "create", Data: note},
			{Version: CurrentCommandVersion, DecidedAt: time.Now(), Type: "unknown_type", Action: "create", Data: note},
			{Version: CurrentCommandVersion, DecidedAt: time.Now(), Type: "note", Action: "unknown_action", Data: note},
		} {
			raw, _ := json.Marshal(command)
			before := fsm.AppliedIndex()
			if recovered := applyFSMRaftLogPanic(t, fsm, raw); recovered == nil {
				t.Fatalf("applied %+v", command)
			}
			if fsm.AppliedIndex() != before {
				t.Fatalf("%+v recorded its entry", command)
			}
		}
		var count int64
		if err := db.Model(&ClusterNote{}).Where("title = ?", "unknown").Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("notes written: %d, %v", count, err)
		}
	})

	t.Run("malformed command payload", func(t *testing.T) {
		err := applyFSMRaftLog(t, fsm, []byte("{invalid"))
		if err == nil {
			t.Fatal("expected error for malformed command payload, got nil")
		}
		if !strings.Contains(err.Error(), "unmarshal") {
			t.Fatalf("expected unmarshal error, got: %v", err)
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
	On(fsm, "partial", "run", func(tx *gorm.DB, _ *struct{}) error {
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

func TestFSMDispatcherRunsAfterApplyHooks(t *testing.T) {
	db := newClusterModelTestDB(t, &ClusterNote{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)
	var notes, others int
	var seen int64
	fsm.AfterApply("note", func() {
		notes++
		_ = db.Model(&ClusterNote{}).Count(&seen).Error
	})
	fsm.AfterApply("other", func() { others++ })
	want := func(step string, wantNotes, wantOthers int) {
		t.Helper()
		if notes != wantNotes || others != wantOthers {
			t.Fatalf("%s: hooks ran note=%d other=%d, want %d and %d", step, notes, others, wantNotes, wantOthers)
		}
	}

	note, _ := json.Marshal(ClusterNote{Title: "hooked", Content: "x"})
	if err := applyFSMCommand(t, fsm, Command{Type: "note", Action: "create", Data: note}); err != nil {
		t.Fatal(err)
	}
	want("raft apply", 1, 0)
	if seen != 1 {
		t.Fatalf("hook saw %d notes, want the committed one", seen)
	}
	missing, _ := json.Marshal(ClusterNote{ID: 99, Title: "missing", Content: "x"})
	if err := applyFSMCommand(t, fsm, Command{Type: "note", Action: "update", Data: missing}); err == nil {
		t.Fatal("update of a missing note was accepted")
	}
	want("rejected apply", 1, 0)
	local := Command{Type: "note", Action: "create", Data: note}
	if err := PrepareCommand(&local, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := fsm.ApplyLocal(local); err != nil {
		t.Fatal(err)
	}
	want("local apply", 2, 0)

	snapshot, err := fsm.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := fsm.Restore(snapshotReader(t, snapshot.(*ClusterSnapshot))); err != nil {
		t.Fatal(err)
	}
	want("restore", 3, 1)
	if err := fsm.ClearReplicatedState(""); err != nil {
		t.Fatal(err)
	}
	want("reset", 4, 2)
}

// After a restart Raft replays entries the database already holds. Note IDs
// come from MAX(id)+1, so applying one twice would create a second note.
func TestFSMDispatcherSkipsEntriesAlreadyApplied(t *testing.T) {
	db := newClusterModelTestDB(t, &ClusterNote{})
	note, _ := json.Marshal(ClusterNote{Title: "once", Content: "x"})
	entry, _ := json.Marshal(Command{Version: CurrentCommandVersion, DecidedAt: time.Now(), Type: "note", Action: "create", Data: note})
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

func TestClusterProtocolIsTheNewestCommandsProtocol(t *testing.T) {
	f := NewFSMDispatcher(newClusterModelTestDB(t))
	RegisterDefaultHandlers(f)
	newest := 0
	for _, protocol := range f.protocols {
		newest = max(newest, protocol)
	}
	if newest != ClusterProtocol {
		t.Fatalf("newest command protocol = %d, ClusterProtocol = %d", newest, ClusterProtocol)
	}
	if f.Protocol("note", "create") != 1 || f.Protocol("not", "registered") != 1 {
		t.Fatal("baseline and unknown commands must report protocol 1")
	}
}
