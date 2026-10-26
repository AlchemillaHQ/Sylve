// SPDX-License-Identifier: BSD-2-Clause

package zfsHandlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	gzfstest "github.com/alchemillahq/gzfs/testutil"
	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	zfsService "github.com/alchemillahq/sylve/internal/services/zfs"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func snapshotHandlerService(t *testing.T) (*zfsService.Service, *gzfstest.MockRunner) {
	t.Helper()
	database := testutil.NewSQLiteTestDB(t,
		&models.ZFSCacheInvalidation{}, &clusterModels.ReplicationPolicy{},
		&utilitiesModels.Downloads{}, &utilitiesModels.Upload{}, &vmModels.VMStorageDataset{},
	)
	runner := gzfstest.NewMockRunner()
	response := `{"datasets":{
		"tank/source@selected":{"name":"tank/source@selected","pool":"tank","type":"SNAPSHOT","properties":{"guid":{"value":"1"}}},
		"backup/replica@selected":{"name":"backup/replica@selected","pool":"backup","type":"SNAPSHOT","properties":{"guid":{"value":"1"}}},
		"tank/unique@selected":{"name":"tank/unique@selected","pool":"tank","type":"SNAPSHOT","properties":{"guid":{"value":"2"}}}
	}}`
	runner.AddCommand("zfs list ", response, "", nil)
	runner.AddCommand("zfs destroy ", "", "", nil)
	runner.AddCommand("zfs rollback ", "", "", nil)
	client := gzfs.NewClient(gzfs.Options{Runner: runner})
	return zfsService.NewZfsService(database, nil, nil, client).(*zfsService.Service), runner
}

func TestSnapshotMutationHandlersUseNameAndExpectedGUID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name, method, target, body, mutation string
		status                               int
	}{
		{name: "delete source", method: http.MethodDelete, target: "/snapshot/1?name=tank%2Fsource%40selected&recursive=true", mutation: "zfs destroy -r tank/source@selected", status: http.StatusOK},
		{name: "delete replica", method: http.MethodDelete, target: "/snapshot/1?name=backup%2Freplica%40selected", mutation: "zfs destroy backup/replica@selected", status: http.StatusOK},
		{name: "rollback replica", method: http.MethodPost, target: "/snapshot/1/rollback", body: `{"name":"backup/replica@selected","destroyMoreRecent":true}`, mutation: "zfs rollback -r backup/replica@selected", status: http.StatusOK},
		{name: "unique legacy GUID", method: http.MethodDelete, target: "/snapshot/2", mutation: "zfs destroy tank/unique@selected", status: http.StatusOK},
		{name: "ambiguous legacy delete", method: http.MethodDelete, target: "/snapshot/1", status: http.StatusConflict},
		{name: "ambiguous legacy rollback", method: http.MethodPost, target: "/snapshot/1/rollback", body: `{}`, status: http.StatusConflict},
		{name: "stale delete", method: http.MethodDelete, target: "/snapshot/2?name=backup%2Freplica%40selected", status: http.StatusConflict},
		{name: "stale rollback", method: http.MethodPost, target: "/snapshot/2/rollback", body: `{"name":"backup/replica@selected"}`, status: http.StatusConflict},
		{name: "repeated name", method: http.MethodDelete, target: "/snapshot/1?name=tank%2Fsource%40selected&name=backup%2Freplica%40selected", status: http.StatusBadRequest},
		{name: "invalid snapshot name", method: http.MethodPost, target: "/snapshot/1/rollback", body: `{"name":"tank/source"}`, status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, runner := snapshotHandlerService(t)
			router := gin.New()
			router.DELETE("/snapshot/:guid", DeleteSnapshot(service))
			router.POST("/snapshot/:guid/rollback", RollbackSnapshot(service))
			response := testutil.PerformJSONRequest(t, router, test.method, test.target, []byte(test.body))
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.status, response.Body.String())
			}
			var mutations []string
			for _, call := range runner.CallHistory {
				if strings.HasPrefix(call.Cmd, "zfs destroy ") || strings.HasPrefix(call.Cmd, "zfs rollback ") {
					mutations = append(mutations, call.Cmd)
				}
			}
			if test.mutation == "" {
				if len(mutations) != 0 {
					t.Fatalf("rejected request mutated snapshots: %v", mutations)
				}
			} else if len(mutations) != 1 || mutations[0] != test.mutation {
				t.Fatalf("mutations = %v, want [%s]", mutations, test.mutation)
			}
		})
	}
}

func TestBulkDeleteSnapshotHandlerAcceptsSharedGUIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprintf("stale=%t", stale), func(t *testing.T) {
			service, runner := snapshotHandlerService(t)
			router := gin.New()
			router.DELETE("/datasets", ReplicationDatasetMutationGuard(service, ReplicationGuardBulkTargets), BulkDeleteDataset(service))
			guid := "1"
			if stale {
				guid = "2"
			}
			target := "/datasets?name=tank%2Fsource%40selected&guid=1&name=backup%2Freplica%40selected&guid=" + guid
			response := testutil.PerformJSONRequest(t, router, http.MethodDelete, target, nil)
			wantStatus, wantMutations := http.StatusOK, 2
			if stale {
				wantStatus, wantMutations = http.StatusConflict, 0
			}
			if response.Code != wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, wantStatus, response.Body.String())
			}
			mutations := 0
			for _, call := range runner.CallHistory {
				if strings.HasPrefix(call.Cmd, "zfs destroy ") {
					mutations++
				}
			}
			if mutations != wantMutations {
				t.Fatalf("destroyed %d snapshots, want %d", mutations, wantMutations)
			}
		})
	}
}

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
