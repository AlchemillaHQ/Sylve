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
	"net/http"
	"strconv"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

func newClusterNotesRouter(cS *cluster.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/cluster/notes", Notes(cS))
	r.POST("/cluster/notes", CreateNote(cS))
	r.PUT("/cluster/notes/:id", UpdateNote(cS))
	r.DELETE("/cluster/notes/:id", DeleteNote(cS))
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
}

func TestClusterNotesHandlerCreateUpdateDeleteSuccess(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ClusterNote{})
	cS := &cluster.Service{DB: db}
	r := newClusterNotesRouter(cS)

	createBody := []byte(`{"title":"created title","content":"created content"}`)
	createResp := performJSONRequest(t, r, http.MethodPost, "/cluster/notes", createBody)
	if createResp.Code != http.StatusOK {
		t.Fatalf("expected create status 200, got %d with body %s", createResp.Code, createResp.Body.String())
	}

	var createJSON handlerAPIResponse[any]
	if err := json.Unmarshal(createResp.Body.Bytes(), &createJSON); err != nil {
		t.Fatalf("create response invalid json: %v", err)
	}
	if createJSON.Message != "note_created" {
		t.Fatalf("expected note_created message, got %q", createJSON.Message)
	}

	var created clusterModels.ClusterNote
	if err := db.First(&created).Error; err != nil {
		t.Fatalf("failed to fetch created note: %v", err)
	}

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
