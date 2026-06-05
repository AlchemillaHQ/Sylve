// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoHandlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/services/info"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

type handlerAPIResponse[T any] struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    T      `json:"data"`
	Error   string `json:"error"`
}

func newInfoNotesRouter(infoService *info.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/info/notes", NotesHandler(infoService))
	r.POST("/info/notes", NotesHandler(infoService))
	r.PUT("/info/notes/:id", NotesHandler(infoService))
	r.DELETE("/info/notes/:id", NotesHandler(infoService))
	r.POST("/info/notes/bulk-delete", NotesHandler(infoService))
	return r
}

func TestInfoNotesHandlerGet(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &infoModels.Note{})
	svc := &info.Service{DB: db}
	r := newInfoNotesRouter(svc)

	rr := testutil.PerformJSONRequest(t, r, http.MethodGet, "/info/notes", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[[]infoModels.Note]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.Status != "success" || resp.Message != "notes_fetched" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(resp.Data) != 0 {
		t.Fatalf("expected empty notes, got %d", len(resp.Data))
	}

	svc.AddNote("T1", "C1")
	svc.AddNote("T2", "C2")

	rr = testutil.PerformJSONRequest(t, r, http.MethodGet, "/info/notes", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 notes, got %d", len(resp.Data))
	}
}

func TestInfoNotesHandlerCreate(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &infoModels.Note{})
	svc := &info.Service{DB: db}
	r := newInfoNotesRouter(svc)

	rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/info/notes",
		[]byte(`{"title":"Hello","content":"World"}`))
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[infoModels.Note]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.Status != "success" || resp.Message != "note_created" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Data.Title != "Hello" || resp.Data.Content != "World" {
		t.Fatalf("wrong note data: %+v", resp.Data)
	}
	if resp.Data.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	rr = testutil.PerformJSONRequest(t, r, http.MethodPost, "/info/notes",
		[]byte(`{"title":"ab"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}

	var errResp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if errResp.Message != "invalid_request_payload" {
		t.Fatalf("expected invalid_request_payload, got %q", errResp.Message)
	}
}

func TestInfoNotesHandlerUpdate(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &infoModels.Note{})
	svc := &info.Service{DB: db}
	r := newInfoNotesRouter(svc)

	n, _ := svc.AddNote("Original", "Original content")
	path := "/info/notes/" + strconv.FormatUint(uint64(n.ID), 10)

	rr := testutil.PerformJSONRequest(t, r, http.MethodPut, path,
		[]byte(`{"title":"Updated","content":"Updated content"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.Message != "note_updated" {
		t.Fatalf("expected note_updated, got %q", resp.Message)
	}

	updated, err := svc.GetNoteByID(int(n.ID))
	if err != nil {
		t.Fatalf("GetNoteByID failed: %v", err)
	}
	if updated.Title != "Updated" || updated.Content != "Updated content" {
		t.Fatalf("note not updated: %+v", updated)
	}

	rr = testutil.PerformJSONRequest(t, r, http.MethodPut, "/info/notes/abc",
		[]byte(`{"title":"x","content":"y"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad ID, got %d: %s", rr.Code, rr.Body.String())
	}

	rr = testutil.PerformJSONRequest(t, r, http.MethodPut, "/info/notes/99999",
		[]byte(`{"title":"Valid Title","content":"Valid Content"}`))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for non-existent, got %d: %s", rr.Code, rr.Body.String())
	}

	rr = testutil.PerformJSONRequest(t, r, http.MethodPut, path,
		[]byte(`{"title":"sh"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for short title, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestInfoNotesHandlerDelete(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &infoModels.Note{})
	svc := &info.Service{DB: db}
	r := newInfoNotesRouter(svc)

	n, _ := svc.AddNote("Delete me", "Content")
	path := "/info/notes/" + strconv.FormatUint(uint64(n.ID), 10)

	rr := testutil.PerformJSONRequest(t, r, http.MethodDelete, path, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.Message != "note_deleted" {
		t.Fatalf("expected note_deleted, got %q", resp.Message)
	}

	rr = testutil.PerformJSONRequest(t, r, http.MethodDelete, path, nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for already deleted (fetch fails), got %d: %s", rr.Code, rr.Body.String())
	}

	rr = testutil.PerformJSONRequest(t, r, http.MethodDelete, "/info/notes/abc", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad ID, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestInfoNotesHandlerBulkDelete(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &infoModels.Note{})
	svc := &info.Service{DB: db}
	r := newInfoNotesRouter(svc)

	n1, _ := svc.AddNote("N1", "C1")
	n2, _ := svc.AddNote("N2", "C2")
	n3, _ := svc.AddNote("N3", "C3")

	ids := []int{int(n1.ID), int(n2.ID)}
	body, _ := json.Marshal(map[string][]int{"ids": ids})
	rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/info/notes/bulk-delete", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.Message != "notes_bulk_deleted" {
		t.Fatalf("expected notes_bulk_deleted, got %q", resp.Message)
	}

	notes, _ := svc.GetNotes()
	if len(notes) != 1 {
		t.Fatalf("expected 1 remaining note, got %d", len(notes))
	}
	if notes[0].ID != n3.ID {
		t.Fatalf("expected note 3 to remain, got ID %d", notes[0].ID)
	}

	rr = testutil.PerformJSONRequest(t, r, http.MethodPost, "/info/notes/bulk-delete",
		[]byte(`{}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing ids field, got %d: %s", rr.Code, rr.Body.String())
	}
}
