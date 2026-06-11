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
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/gin-gonic/gin"
)

func uintPtr(v uint) *uint { return &v }

func newBackupEventsRouter(cS *cluster.Service, zS *zelta.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/cluster/backups/events", BackupEvents(cS, zS))
	r.GET("/cluster/backups/events/remote", BackupEventsRemote(cS, zS))
	r.GET("/cluster/backups/events/:id", BackupEventByID(cS, zS))
	r.GET("/cluster/backups/events/:id/progress", BackupEventProgressByID(cS, zS))
	return r
}

func TestBackupEventsHandlerGet(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.BackupEvent{})
	cS := &cluster.Service{DB: db}
	zS := &zelta.Service{DB: db}
	r := newBackupEventsRouter(cS, zS)

	rr := performJSONRequest(t, r, http.MethodGet, "/cluster/backups/events", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[[]clusterModels.BackupEvent]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.Status != "success" {
		t.Fatalf("expected success, got %s", resp.Status)
	}
	if len(resp.Data) != 0 {
		t.Fatalf("expected empty list, got %d events", len(resp.Data))
	}

	db.Create(&clusterModels.BackupEvent{ID: 1, JobID: uintPtr(10), Status: "success"})
	db.Create(&clusterModels.BackupEvent{ID: 2, JobID: uintPtr(10), Status: "failed"})
	db.Create(&clusterModels.BackupEvent{ID: 3, JobID: uintPtr(20), Status: "running"})

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/backups/events", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data) != 3 {
		t.Fatalf("expected 3 events, got %d", len(resp.Data))
	}

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/backups/events?jobId=10", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 events for jobId=10, got %d", len(resp.Data))
	}

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/backups/events?jobId=999", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data) != 0 {
		t.Fatalf("expected 0 events for non-existent jobId, got %d", len(resp.Data))
	}

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/backups/events?limit=2", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 events with limit=2, got %d", len(resp.Data))
	}
}

func TestBackupEventByIDHandler(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.BackupEvent{})
	cS := &cluster.Service{DB: db}
	zS := &zelta.Service{DB: db}
	r := newBackupEventsRouter(cS, zS)

	rr := performJSONRequest(t, r, http.MethodGet, "/cluster/backups/events/1", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent, got %d: %s", rr.Code, rr.Body.String())
	}

	db.Create(&clusterModels.BackupEvent{ID: 1, JobID: uintPtr(10), Status: "success", Mode: "backup"})

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/backups/events/1", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp handlerAPIResponse[*clusterModels.BackupEvent]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.Data == nil || resp.Data.ID != 1 || resp.Data.Status != "success" {
		t.Fatalf("unexpected event data: %+v", resp.Data)
	}
}

func TestBackupEventProgressByIDHandler(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.BackupEvent{}, &clusterModels.BackupJob{}, &clusterModels.BackupTarget{})
	cS := &cluster.Service{DB: db}
	zS := &zelta.Service{DB: db}
	r := newBackupEventsRouter(cS, zS)

	rr := performJSONRequest(t, r, http.MethodGet, "/cluster/backups/events/1/progress", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent, got %d: %s", rr.Code, rr.Body.String())
	}

	outputStr := "syncing: 10.00M\n5.00M sent\n"
	db.Create(&clusterModels.BackupEvent{
		ID: 1, JobID: uintPtr(10), Status: "running", Mode: "backup",
		Output: outputStr,
	})

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/backups/events/1/progress", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for running event, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp handlerAPIResponse[*zelta.BackupEventProgress]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.Status != "success" {
		t.Fatalf("expected success, got %s: %+v", resp.Status, resp)
	}
	if resp.Data == nil || resp.Data.Event == nil {
		t.Fatal("expected progress data with event")
	}
	if resp.Data.Event.ID != 1 || resp.Data.Event.Status != "running" {
		t.Fatalf("unexpected event in progress: %+v", resp.Data.Event)
	}
	if resp.Data.TotalBytes == nil || *resp.Data.TotalBytes != 10000000 {
		t.Fatalf("expected total bytes 10000000, got %v", resp.Data.TotalBytes)
	}
	if resp.Data.MovedBytes == nil || *resp.Data.MovedBytes != 5000000 {
		t.Fatalf("expected moved bytes 5000000, got %v", resp.Data.MovedBytes)
	}

	db.Create(&clusterModels.BackupEvent{
		ID: 2, JobID: uintPtr(10), Status: "success", Mode: "backup",
		Output: "syncing: 100.00M\n100.00M sent\n",
	})

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/backups/events/2/progress", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for completed event, got %d: %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.Data.TotalBytes == nil || *resp.Data.TotalBytes != 100000000 {
		t.Fatalf("expected total bytes 100000000, got %v", resp.Data.TotalBytes)
	}
}

func TestBackupEventsRemoteHandler(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.BackupEvent{})
	cS := &cluster.Service{DB: db}
	zS := &zelta.Service{DB: db}
	r := newBackupEventsRouter(cS, zS)

	for i := uint(1); i <= 30; i++ {
		db.Create(&clusterModels.BackupEvent{
			ID: i, JobID: uintPtr(10), Status: "success", Mode: "backup",
		})
	}

	rr := performJSONRequest(t, r, http.MethodGet, "/cluster/backups/events/remote", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp handlerAPIResponse[*zelta.BackupEventsResponse]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.Status != "success" {
		t.Fatalf("expected success, got %s", resp.Status)
	}
	if resp.Data == nil {
		t.Fatal("expected data")
	}
	if resp.Data.LastPage < 1 {
		t.Fatalf("expected lastPage >= 1, got %d", resp.Data.LastPage)
	}
	if len(resp.Data.Data) != 25 {
		t.Fatalf("expected default page size 25, got %d", len(resp.Data.Data))
	}

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/backups/events/remote?size=5", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data.Data) != 5 {
		t.Fatalf("expected page size 5, got %d", len(resp.Data.Data))
	}

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/backups/events/remote?jobId=10&size=100", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data.Data) != 30 {
		t.Fatalf("expected all 30 events for jobId=10, got %d", len(resp.Data.Data))
	}

	rr = performJSONRequest(t, r, http.MethodGet, "/cluster/backups/events/remote?search=success&size=10", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Data.Data) != 10 {
		t.Fatalf("expected 10 events matching search, got %d", len(resp.Data.Data))
	}
}
