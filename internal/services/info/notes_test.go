// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package info

import (
	"testing"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func newNotesTestDB(t *testing.T) *Service {
	db := testutil.NewSQLiteTestDB(t, &infoModels.Note{})
	return &Service{DB: db}
}

func TestGetNotes(t *testing.T) {
	svc := newNotesTestDB(t)

	notes, err := svc.GetNotes()
	if err != nil {
		t.Fatalf("GetNotes returned error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected empty, got %d notes", len(notes))
	}

	n1, err := svc.AddNote("Title One", "Content one")
	if err != nil {
		t.Fatalf("AddNote failed: %v", err)
	}
	n2, err := svc.AddNote("Title Two", "Content two")
	if err != nil {
		t.Fatalf("AddNote failed: %v", err)
	}

	notes, err = svc.GetNotes()
	if err != nil {
		t.Fatalf("GetNotes returned error: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes, got %d", len(notes))
	}
	if notes[0].ID != n1.ID || notes[1].ID != n2.ID {
		t.Fatalf("unexpected note order: %v, %v", notes[0].ID, notes[1].ID)
	}
}

func TestGetNoteByID(t *testing.T) {
	svc := newNotesTestDB(t)

	n, err := svc.AddNote("Test Note", "Test content")
	if err != nil {
		t.Fatalf("AddNote failed: %v", err)
	}

	got, err := svc.GetNoteByID(int(n.ID))
	if err != nil {
		t.Fatalf("GetNoteByID failed: %v", err)
	}
	if got.Title != "Test Note" || got.Content != "Test content" {
		t.Fatalf("wrong note: %+v", got)
	}

	_, err = svc.GetNoteByID(9999)
	if err == nil {
		t.Fatal("expected error for non-existent note")
	}
}

func TestAddNote(t *testing.T) {
	svc := newNotesTestDB(t)

	n, err := svc.AddNote("Hello", "World")
	if err != nil {
		t.Fatalf("AddNote failed: %v", err)
	}
	if n.ID == 0 {
		t.Fatal("expected non-zero ID")
	}
	if n.Title != "Hello" || n.Content != "World" {
		t.Fatalf("wrong fields: %+v", n)
	}
	if n.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to be set")
	}

	_, err = svc.AddNote("", "")
	if err != nil {
		t.Fatalf("AddNote with empty fields should not fail at DB level: %v", err)
	}
}

func TestUpdateNoteByID(t *testing.T) {
	svc := newNotesTestDB(t)

	n, err := svc.AddNote("Original", "Original content")
	if err != nil {
		t.Fatalf("AddNote failed: %v", err)
	}

	err = svc.UpdateNoteByID(int(n.ID), "Updated Title", "Updated content")
	if err != nil {
		t.Fatalf("UpdateNoteByID failed: %v", err)
	}

	got, err := svc.GetNoteByID(int(n.ID))
	if err != nil {
		t.Fatalf("GetNoteByID failed: %v", err)
	}
	if got.Title != "Updated Title" || got.Content != "Updated content" {
		t.Fatalf("note not updated: %+v", got)
	}

	err = svc.UpdateNoteByID(9999, "x", "y")
	if err != nil {
		t.Fatalf("UpdateNoteByID on non-existent should not error at DB level: %v", err)
	}
}

func TestDeleteNoteByID(t *testing.T) {
	svc := newNotesTestDB(t)

	n, err := svc.AddNote("Delete me", "Content")
	if err != nil {
		t.Fatalf("AddNote failed: %v", err)
	}

	err = svc.DeleteNoteByID(int(n.ID))
	if err != nil {
		t.Fatalf("DeleteNoteByID failed: %v", err)
	}

	err = svc.DeleteNoteByID(int(n.ID))
	if err == nil {
		t.Fatal("expected error deleting already-deleted note")
	}
	if err.Error() != "record not found" {
		t.Fatalf("expected 'record not found', got %q", err.Error())
	}

	err = svc.DeleteNoteByID(9999)
	if err == nil {
		t.Fatal("expected error deleting non-existent note")
	}
}

func TestBulkDeleteNotes(t *testing.T) {
	svc := newNotesTestDB(t)

	n1, err := svc.AddNote("Note 1", "Content 1")
	if err != nil {
		t.Fatalf("AddNote failed: %v", err)
	}
	n2, err := svc.AddNote("Note 2", "Content 2")
	if err != nil {
		t.Fatalf("AddNote failed: %v", err)
	}
	n3, err := svc.AddNote("Note 3", "Content 3")
	if err != nil {
		t.Fatalf("AddNote failed: %v", err)
	}

	err = svc.BulkDeleteNotes([]int{int(n1.ID), int(n2.ID)})
	if err != nil {
		t.Fatalf("BulkDeleteNotes failed: %v", err)
	}

	notes, err := svc.GetNotes()
	if err != nil {
		t.Fatalf("GetNotes failed: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("expected 1 remaining note, got %d", len(notes))
	}
	if notes[0].ID != n3.ID {
		t.Fatalf("expected note 3 to remain, got ID %d", notes[0].ID)
	}

	err = svc.BulkDeleteNotes([]int{})
	if err == nil {
		t.Fatal("BulkDeleteNotes with empty IDs should error (GORM requires WHERE conditions)")
	}

	err = svc.BulkDeleteNotes([]int{9999, int(n3.ID)})
	if err != nil {
		t.Fatalf("BulkDeleteNotes with mix of existing/non-existing should not error: %v", err)
	}

	notes, err = svc.GetNotes()
	if err != nil {
		t.Fatalf("GetNotes failed: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected 0 remaining notes, got %d", len(notes))
	}

	n4, err := svc.AddNote("Note 4", "Content 4")
	if err != nil {
		t.Fatalf("AddNote failed: %v", err)
	}
	err = svc.BulkDeleteNotes([]int{int(n4.ID)})
	if err != nil {
		t.Fatalf("BulkDeleteNotes for single ID failed: %v", err)
	}
	_, err = svc.GetNoteByID(int(n4.ID))
	if err == nil {
		t.Fatal("expected error fetching deleted note")
	}
}
