// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

func newClusterNotesRouter(cS *cluster.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/cluster/notes", Notes(cS))
	r.POST("/cluster/notes", CreateNote(cS))
	r.PUT("/cluster/notes/:id", UpdateNote(cS))
	r.DELETE("/cluster/notes/:id", DeleteNote(cS))
	r.POST("/cluster/notes/bulk-delete", BulkDeleteNotes(cS))
	return r
}

func TestClusterNotesHandlerGetSuccess(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ClusterNote{})
	cS := &cluster.Service{DB: db}
	r := newClusterNotesRouter(cS)

	if err := db.Create(&clusterModels.ClusterNote{Title: "first", Content: "one"}).Error; err != nil {
		t.Fatalf("failed to seed first note: %v", err)
	}
	if err := db.Create(&clusterModels.ClusterNote{Title: "second", Content: "two"}).Error; err != nil {
		t.Fatalf("failed to seed second note: %v", err)
	}

	rr := performJSONRequest(t, r, http.MethodGet, "/cluster/notes", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d with body %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[[]clusterModels.ClusterNote]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response body is not valid JSON: %v; body=%s", err, rr.Body.String())
	}

	if resp.Status != "success" || resp.Message != "notes_listed" {
		t.Fatalf("unexpected response status/message: %+v", resp)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 notes in response data, got %d", len(resp.Data))
	}
}

func TestClusterNotesHandlerValidationFailures(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ClusterNote{})
	cS := &cluster.Service{DB: db}
	r := newClusterNotesRouter(cS)

	t.Run("create invalid request", func(t *testing.T) {
		rr := performJSONRequest(t, r, http.MethodPost, "/cluster/notes", []byte(`{"title":"x"}`))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d with body %s", rr.Code, rr.Body.String())
		}

		var resp handlerAPIResponse[any]
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid json response: %v", err)
		}
		if resp.Message != "invalid_request" {
			t.Fatalf("expected invalid_request message, got %q", resp.Message)
		}
	})

	t.Run("update invalid id", func(t *testing.T) {
		rr := performJSONRequest(t, r, http.MethodPut, "/cluster/notes/abc", []byte(`{"title":"ok title","content":"ok content"}`))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d with body %s", rr.Code, rr.Body.String())
		}

		var resp handlerAPIResponse[any]
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid json response: %v", err)
		}
		if resp.Message != "invalid_id" {
			t.Fatalf("expected invalid_id message, got %q", resp.Message)
		}
	})

	t.Run("update negative id", func(t *testing.T) {
		rr := performJSONRequest(t, r, http.MethodPut, "/cluster/notes/-1", []byte(`{"title":"ok title","content":"ok content"}`))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d with body %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("delete invalid id", func(t *testing.T) {
		rr := performJSONRequest(t, r, http.MethodDelete, "/cluster/notes/not-a-number", nil)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d with body %s", rr.Code, rr.Body.String())
		}

		var resp handlerAPIResponse[any]
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid json response: %v", err)
		}
		if resp.Message != "invalid_id" {
			t.Fatalf("expected invalid_id message, got %q", resp.Message)
		}
	})

	for _, test := range []struct {
		name string
		body string
	}{
		{name: "bulk zero id", body: `{"ids":[1,0]}`},
		{name: "bulk negative id", body: `{"ids":[1,-2]}`},
		{name: "bulk duplicate id", body: `{"ids":[1,1]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			rr := performJSONRequest(t, r, http.MethodPost, "/cluster/notes/bulk-delete", []byte(test.body))
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d with body %s", rr.Code, rr.Body.String())
			}

			var resp handlerAPIResponse[any]
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("invalid json response: %v", err)
			}
			if resp.Message != "invalid_ids" {
				t.Fatalf("expected invalid_ids message, got %q", resp.Message)
			}
		})
	}
}

func TestClusterNotesHandlerCreateSuccess(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ClusterNote{})
	cS := &cluster.Service{DB: db}
	r := newClusterNotesRouter(cS)

	createBody := []byte(`{"title":"created title","content":"created content"}`)
	createResp := performJSONRequest(t, r, http.MethodPost, "/cluster/notes", createBody)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d with body %s", createResp.Code, createResp.Body.String())
	}

	var createJSON handlerAPIResponse[any]
	if err := json.Unmarshal(createResp.Body.Bytes(), &createJSON); err != nil {
		t.Fatalf("create response invalid json: %v", err)
	}
	if createJSON.Message != "note_created" {
		t.Fatalf("expected note_created message, got %q", createJSON.Message)
	}
}

func TestClusterNotesHandlerMissingMutationsReturnNotFound(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ClusterNote{})
	cS := &cluster.Service{DB: db}
	r := newClusterNotesRouter(cS)
	validBody := []byte(`{"title":"updated title","content":"updated content"}`)

	tests := []struct {
		name   string
		method string
		path   string
		body   []byte
	}{
		{name: "update", method: http.MethodPut, path: "/cluster/notes/999", body: validBody},
		{name: "delete", method: http.MethodDelete, path: "/cluster/notes/999"},
		{name: "bulk delete", method: http.MethodPost, path: "/cluster/notes/bulk-delete", body: []byte(`{"ids":[999]}`)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rr := performJSONRequest(t, r, test.method, test.path, test.body)
			if rr.Code != http.StatusNotFound {
				t.Fatalf("expected status 404, got %d with body %s", rr.Code, rr.Body.String())
			}

			var resp handlerAPIResponse[any]
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("invalid json response: %v", err)
			}
			if resp.Message != "note_not_found" || resp.Error != "note_not_found" {
				t.Fatalf("unexpected missing-note response: %+v", resp)
			}
		})
	}
}

func TestClusterNotesHandlerBulkDeleteIsAllOrNothing(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ClusterNote{})
	cS := &cluster.Service{DB: db}
	r := newClusterNotesRouter(cS)

	notes := []clusterModels.ClusterNote{
		{Title: "first", Content: "first content"},
		{Title: "second", Content: "second content"},
	}
	if err := db.Create(&notes).Error; err != nil {
		t.Fatalf("failed to seed notes: %v", err)
	}

	partialBody := []byte(fmt.Sprintf(`{"ids":[%d,999999]}`, notes[0].ID))
	partial := performJSONRequest(t, r, http.MethodPost, "/cluster/notes/bulk-delete", partialBody)
	if partial.Code != http.StatusNotFound {
		t.Fatalf("expected partial request status 404, got %d with body %s", partial.Code, partial.Body.String())
	}

	var count int64
	if err := db.Model(&clusterModels.ClusterNote{}).Count(&count).Error; err != nil {
		t.Fatalf("failed to count notes after rejected bulk delete: %v", err)
	}
	if count != 2 {
		t.Fatalf("rejected bulk delete removed notes: count=%d", count)
	}

	exactBody := []byte(fmt.Sprintf(`{"ids":[%d,%d]}`, notes[0].ID, notes[1].ID))
	exact := performJSONRequest(t, r, http.MethodPost, "/cluster/notes/bulk-delete", exactBody)
	if exact.Code != http.StatusOK {
		t.Fatalf("expected exact request status 200, got %d with body %s", exact.Code, exact.Body.String())
	}
	if err := db.Model(&clusterModels.ClusterNote{}).Count(&count).Error; err != nil {
		t.Fatalf("failed to count notes after exact bulk delete: %v", err)
	}
	if count != 0 {
		t.Fatalf("exact bulk delete left %d notes", count)
	}
}

func TestClusterNotesHandlerRejectsOversizedBody(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ClusterNote{})
	cS := &cluster.Service{DB: db}
	r := gin.New()
	r.Use(middleware.LimitRequestBody(64))
	r.POST("/cluster/notes", CreateNote(cS))

	body := []byte(`{"title":"valid title","content":"` + strings.Repeat("x", 128) + `"}`)
	rr := performJSONRequest(t, r, http.MethodPost, "/cluster/notes", body)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d with body %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if resp.Message != "request_body_too_large" || resp.Error != "request_body_too_large" {
		t.Fatalf("unexpected oversized-body response: %+v", resp)
	}
}

func TestWriteNoteMutationErrorMappings(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantMessage string
	}{
		{name: "missing", err: gorm.ErrRecordNotFound, wantStatus: http.StatusNotFound, wantMessage: "note_not_found"},
		{name: "not leader", err: raft.ErrNotLeader, wantStatus: http.StatusConflict, wantMessage: "cluster_leadership_changed"},
		{name: "leadership lost", err: raft.ErrLeadershipLost, wantStatus: http.StatusConflict, wantMessage: "cluster_leadership_changed"},
		{name: "raft shutdown", err: raft.ErrRaftShutdown, wantStatus: http.StatusServiceUnavailable, wantMessage: "cluster_consensus_unavailable"},
		{name: "enqueue timeout", err: raft.ErrEnqueueTimeout, wantStatus: http.StatusServiceUnavailable, wantMessage: "cluster_consensus_unavailable"},
		{name: "unexpected", err: errors.New("database failed"), wantStatus: http.StatusInternalServerError, wantMessage: "note_update_failed"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			writeNoteMutationError(ctx, "note_update_failed", fmt.Errorf("wrapped: %w", test.err))

			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			var resp handlerAPIResponse[any]
			if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
				t.Fatalf("invalid json response: %v", err)
			}
			if resp.Message != test.wantMessage || resp.Error != test.wantMessage {
				t.Fatalf("unexpected mapped response: %+v", resp)
			}
		})
	}
}

func TestClusterNotesHandlerUpdateSuccess(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ClusterNote{})
	cS := &cluster.Service{DB: db}
	r := newClusterNotesRouter(cS)

	if err := db.Create(&clusterModels.ClusterNote{
		Title: "original", Content: "original content",
	}).Error; err != nil {
		t.Fatalf("failed to seed note: %v", err)
	}

	var created clusterModels.ClusterNote
	db.First(&created)

	updateBody := []byte(`{"title":"updated title","content":"updated content"}`)
	updatePath := "/cluster/notes/" + strconv.FormatUint(uint64(created.ID), 10)
	updateResp := performJSONRequest(t, r, http.MethodPut, updatePath, updateBody)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("expected update status 200, got %d with body %s", updateResp.Code, updateResp.Body.String())
	}

	var updated clusterModels.ClusterNote
	if err := db.First(&updated, created.ID).Error; err != nil {
		t.Fatalf("failed to fetch updated note: %v", err)
	}
	if updated.Title != "updated title" || updated.Content != "updated content" {
		t.Fatalf("update mismatch: got title=%q content=%q", updated.Title, updated.Content)
	}
}

func TestClusterNotesHandlerDeleteSuccess(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ClusterNote{})
	cS := &cluster.Service{DB: db}
	r := newClusterNotesRouter(cS)

	if err := db.Create(&clusterModels.ClusterNote{
		Title: "to-delete", Content: "to-delete content",
	}).Error; err != nil {
		t.Fatalf("failed to seed note: %v", err)
	}

	var created clusterModels.ClusterNote
	db.First(&created)

	deletePath := "/cluster/notes/" + strconv.FormatUint(uint64(created.ID), 10)
	deleteResp := performJSONRequest(t, r, http.MethodDelete, deletePath, nil)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("expected delete status 200, got %d with body %s", deleteResp.Code, deleteResp.Body.String())
	}

	var count int64
	if err := db.Model(&clusterModels.ClusterNote{}).Where("id = ?", created.ID).Count(&count).Error; err != nil {
		t.Fatalf("failed to count deleted note: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected note to be deleted, found %d row(s)", count)
	}
}

func TestClusterNotesHandlerDeleteFailureReturnsSingleErrorResponse(t *testing.T) {
	// Intentionally skip schema migration to force DB failure in local bypass mode.
	db := newClusterHandlerTestDB(t)
	cS := &cluster.Service{DB: db}
	r := newClusterNotesRouter(cS)

	rr := performJSONRequest(t, r, http.MethodDelete, "/cluster/notes/1", nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d with body %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("expected single valid JSON error response, got parse error: %v; body=%s", err, rr.Body.String())
	}
	if resp.Message != "note_delete_failed" || resp.Status != "error" {
		t.Fatalf("unexpected delete failure response: %+v", resp)
	}
}
