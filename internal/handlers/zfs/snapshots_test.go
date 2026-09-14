// SPDX-License-Identifier: BSD-2-Clause

package zfsHandlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	zfsService "github.com/alchemillahq/sylve/internal/services/zfs"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestSnapshotCreationErrorResponse(t *testing.T) {
	tests := []struct {
		err         error
		wantStatus  int
		wantMessage string
	}{
		{fmt.Errorf("%w:ha_", zfsService.ErrReservedSnapshotNamespace), http.StatusBadRequest, "snapshot_namespace_reserved"},
		{fmt.Errorf("%w:guest_operation", zfsService.ErrSnapshotCreationBlocked), http.StatusConflict, "snapshot_creation_blocked"},
		{fmt.Errorf("zfs failed"), http.StatusInternalServerError, "internal_server_error"},
	}
	for _, test := range tests {
		status, message := snapshotCreationErrorResponse(test.err)
		if status != test.wantStatus || message != test.wantMessage {
			t.Fatalf("got (%d, %q), want (%d, %q)", status, message, test.wantStatus, test.wantMessage)
		}
	}
}

func TestBulkDeletePeriodicSnapshotsHandlerDeletesBatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := testutil.NewSQLiteTestDB(t, &zfsModels.PeriodicSnapshot{})
	jobs := []zfsModels.PeriodicSnapshot{
		{GUID: "dataset-guid", Interval: 60, Prefix: "minute"},
		{GUID: "dataset-guid", Interval: 3600, Prefix: "hour"},
	}
	if err := database.Create(&jobs).Error; err != nil {
		t.Fatalf("create periodic snapshot jobs: %v", err)
	}

	service := &zfsService.Service{DB: database}
	router := gin.New()
	router.DELETE("/snapshot/periodic", BulkDeletePeriodicSnapshots(service))
	body, err := json.Marshal(zfsServiceInterfaces.BulkDeletePeriodicSnapshotsRequest{
		IDs: []uint{jobs[0].ID, jobs[1].ID},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	response := testutil.PerformJSONRequest(t, router, http.MethodDelete, "/snapshot/periodic", body)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	decoded := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, response)
	if decoded.Status != "success" || decoded.Message != "deleted_periodic_snapshots" {
		t.Fatalf("unexpected response: %#v", decoded)
	}

	var count int64
	if err := database.Model(&zfsModels.PeriodicSnapshot{}).Count(&count).Error; err != nil {
		t.Fatalf("count periodic snapshot jobs: %v", err)
	}
	if count != 0 {
		t.Fatalf("bulk delete left %d jobs, want 0", count)
	}
}

func TestBulkDeletePeriodicSnapshotsHandlerIsAllOrNothing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := testutil.NewSQLiteTestDB(t, &zfsModels.PeriodicSnapshot{})
	job := zfsModels.PeriodicSnapshot{GUID: "dataset-guid", Interval: 60, Prefix: "minute"}
	if err := database.Create(&job).Error; err != nil {
		t.Fatalf("create periodic snapshot job: %v", err)
	}

	service := &zfsService.Service{DB: database}
	router := gin.New()
	router.DELETE("/snapshot/periodic", BulkDeletePeriodicSnapshots(service))
	body, err := json.Marshal(zfsServiceInterfaces.BulkDeletePeriodicSnapshotsRequest{
		IDs: []uint{job.ID, 999999},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	response := testutil.PerformJSONRequest(t, router, http.MethodDelete, "/snapshot/periodic", body)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusNotFound, response.Body.String())
	}

	var count int64
	if err := database.Model(&zfsModels.PeriodicSnapshot{}).Count(&count).Error; err != nil {
		t.Fatalf("count periodic snapshot jobs: %v", err)
	}
	if count != 1 {
		t.Fatalf("rejected bulk delete left %d jobs, want 1", count)
	}
}

func TestBulkDeletePeriodicSnapshotsHandlerRejectsInvalidIDSets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{}`,
		`{"ids":[]}`,
		`{"ids":[0]}`,
		`{"ids":[1,1]}`,
	} {
		t.Run(body, func(t *testing.T) {
			router := gin.New()
			router.DELETE("/snapshot/periodic", BulkDeletePeriodicSnapshots(nil))
			response := testutil.PerformJSONRequest(
				t,
				router,
				http.MethodDelete,
				"/snapshot/periodic",
				[]byte(body),
			)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
			}
		})
	}
}
