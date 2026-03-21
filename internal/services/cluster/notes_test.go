// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"gorm.io/gorm"
)

func newClusterNoteTestDB(t *testing.T) *gorm.DB {
	return newClusterServiceTestDB(t, &clusterModels.ClusterNote{})
}

func TestListNotesOrdersByID(t *testing.T) {
	db := newClusterNoteTestDB(t)
	s := &Service{DB: db}

	if err := db.Create(&clusterModels.ClusterNote{ID: 2, Title: "second", Content: "two"}).Error; err != nil {
		t.Fatalf("failed to seed note 2: %v", err)
	}
	if err := db.Create(&clusterModels.ClusterNote{ID: 1, Title: "first", Content: "one"}).Error; err != nil {
		t.Fatalf("failed to seed note 1: %v", err)
	}

	notes, err := s.ListNotes()
	if err != nil {
		t.Fatalf("ListNotes failed: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes, got %d", len(notes))
	}
	if notes[0].ID != 1 || notes[1].ID != 2 {
		t.Fatalf("expected notes ordered by ascending ID, got IDs %d then %d", notes[0].ID, notes[1].ID)
	}
}

func TestProposeNoteCRUDBypassRaft(t *testing.T) {
	db := newClusterNoteTestDB(t)
	s := &Service{DB: db}

	if err := s.ProposeNoteCreate("first", "content one", true); err != nil {
		t.Fatalf("ProposeNoteCreate bypass failed: %v", err)
	}
	if err := s.ProposeNoteCreate("second", "content two", true); err != nil {
		t.Fatalf("second ProposeNoteCreate bypass failed: %v", err)
	}

	var notes []clusterModels.ClusterNote
	if err := db.Order("id ASC").Find(&notes).Error; err != nil {
		t.Fatalf("failed to fetch created notes: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes after create, got %d", len(notes))
	}

	firstID := int(notes[0].ID)
	secondID := int(notes[1].ID)

	if err := s.ProposeNoteUpdate(firstID, "first-updated", "content one updated", true); err != nil {
		t.Fatalf("ProposeNoteUpdate bypass failed: %v", err)
	}

	var updated clusterModels.ClusterNote
	if err := db.First(&updated, firstID).Error; err != nil {
		t.Fatalf("failed to fetch updated note: %v", err)
	}
	if updated.Title != "first-updated" || updated.Content != "content one updated" {
		t.Fatalf("note update mismatch: got title=%q content=%q", updated.Title, updated.Content)
	}

	if err := s.ProposeNoteDelete(secondID, true); err != nil {
		t.Fatalf("ProposeNoteDelete bypass failed: %v", err)
	}

	var count int64
	if err := db.Model(&clusterModels.ClusterNote{}).Where("id = ?", secondID).Count(&count).Error; err != nil {
		t.Fatalf("failed to count deleted note: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected deleted note to be removed, still found %d row(s)", count)
	}

	if err := s.ProposeNoteCreate("third", "content three", true); err != nil {
		t.Fatalf("third ProposeNoteCreate bypass failed: %v", err)
	}
	if err := db.Order("id ASC").Find(&notes).Error; err != nil {
		t.Fatalf("failed to refresh notes before bulk delete: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes before bulk delete, got %d", len(notes))
	}

	ids := []int{int(notes[0].ID), int(notes[1].ID)}
	if err := s.ProposeNoteBulkDelete(ids, true); err != nil {
		t.Fatalf("ProposeNoteBulkDelete bypass failed: %v", err)
	}

	if err := db.Model(&clusterModels.ClusterNote{}).Count(&count).Error; err != nil {
		t.Fatalf("failed to count notes after bulk delete: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 notes after bulk delete, found %d", count)
	}
}

func TestProposeNoteRequiresRaftWhenBypassDisabled(t *testing.T) {
	s := &Service{DB: newClusterNoteTestDB(t), Raft: nil}

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "create",
			call: func() error {
				return s.ProposeNoteCreate("title", "content", false)
			},
		},
		{
			name: "update",
			call: func() error {
				return s.ProposeNoteUpdate(1, "title", "content", false)
			},
		},
		{
			name: "delete",
			call: func() error {
				return s.ProposeNoteDelete(1, false)
			},
		},
		{
			name: "bulk delete",
			call: func() error {
				return s.ProposeNoteBulkDelete([]int{1, 2}, false)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), "raft_not_initialized") {
				t.Fatalf("expected raft_not_initialized error, got: %v", err)
			}
		})
	}
}
