// SPDX-License-Identifier: BSD-2-Clause

package libvirtHandlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type mockVMSnapshotService struct {
	listFn     func(rid uint) ([]vmModels.VMSnapshot, error)
	createFn   func(ctx context.Context, rid uint, name string, description string) (*vmModels.VMSnapshot, error)
	rollbackFn func(ctx context.Context, rid uint, snapshotID uint) (libvirt.VMSnapshotRollbackResult, error)
	deleteFn   func(ctx context.Context, rid uint, snapshotID uint) error
}

func (m *mockVMSnapshotService) ListVMSnapshots(rid uint) ([]vmModels.VMSnapshot, error) {
	if m.listFn != nil {
		return m.listFn(rid)
	}
	return []vmModels.VMSnapshot{}, nil
}

func (m *mockVMSnapshotService) CreateVMSnapshot(
	ctx context.Context,
	rid uint,
	name string,
	description string,
) (*vmModels.VMSnapshot, error) {
	if m.createFn != nil {
		return m.createFn(ctx, rid, name, description)
	}
	return &vmModels.VMSnapshot{ID: 1, RID: rid, Name: name, Description: description}, nil
}

func (m *mockVMSnapshotService) RollbackVMSnapshot(
	ctx context.Context,
	rid uint,
	snapshotID uint,
) (libvirt.VMSnapshotRollbackResult, error) {
	if m.rollbackFn != nil {
		return m.rollbackFn(ctx, rid, snapshotID)
	}
	return libvirt.VMSnapshotRollbackResult{Warnings: []string{}}, nil
}

func (m *mockVMSnapshotService) DeleteVMSnapshot(
	ctx context.Context,
	rid uint,
	snapshotID uint,
) error {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, rid, snapshotID)
	}
	return nil
}

func TestVMSnapshotErrorStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "validation", err: errors.New("snapshot_name_required"), want: http.StatusBadRequest},
		{name: "ownership", err: errors.New("replication_lease_not_owned"), want: http.StatusForbidden},
		{name: "snapshot missing", err: errors.New("snapshot_not_found: missing"), want: http.StatusNotFound},
		{name: "wrapped VM missing", err: errors.Join(errors.New("failed_to_get_vm"), gorm.ErrRecordNotFound), want: http.StatusNotFound},
		{name: "topology", err: errors.New("replication_storage_topology_change_requires_policy_disabled"), want: http.StatusConflict},
		{name: "replication running", err: errors.New("replication_run_in_progress"), want: http.StatusConflict},
		{name: "physical snapshot missing", err: errors.New("vm_snapshot_dataset_missing: zroot/vm@snap"), want: http.StatusConflict},
		{name: "wrapped mountpoint", err: errors.New("failed_to_write_vm_json_before_snapshot: filesystem_dataset_mountpoint_not_usable"), want: http.StatusConflict},
		{name: "libvirt unavailable", err: errors.New("libvirt_connection_unavailable: refused"), want: http.StatusServiceUnavailable},
		{name: "unexpected", err: errors.New("failed_to_query_database"), want: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := vmSnapshotErrorStatus(tt.err); got != tt.want {
				t.Fatalf("vmSnapshotErrorStatus(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestCreateVMSnapshotHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("returns created snapshot on nested route", func(t *testing.T) {
		var gotRID uint
		var gotName string
		router := gin.New()
		router.POST("/vm/:rid/snapshots", CreateVMSnapshot(&mockVMSnapshotService{
			createFn: func(_ context.Context, rid uint, name string, description string) (*vmModels.VMSnapshot, error) {
				gotRID = rid
				gotName = name
				return &vmModels.VMSnapshot{ID: 9, RID: rid, Name: name, Description: description}, nil
			},
		}))

		response := testutil.PerformJSONRequest(
			t,
			router,
			http.MethodPost,
			"/vm/42/snapshots",
			[]byte(`{"name":"before-upgrade","description":"known good"}`),
		)
		if response.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d body=%s", response.Code, response.Body.String())
		}
		if gotRID != 42 || gotName != "before-upgrade" {
			t.Fatalf("unexpected service input rid=%d name=%q", gotRID, gotName)
		}

		var envelope internal.APIResponse[vmModels.VMSnapshot]
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if envelope.Data.ID != 9 || envelope.Data.RID != 42 {
			t.Fatalf("unexpected snapshot response: %+v", envelope.Data)
		}
	})

	t.Run("rejects invalid request before service", func(t *testing.T) {
		called := false
		router := gin.New()
		router.POST("/vm/:rid/snapshots", CreateVMSnapshot(&mockVMSnapshotService{
			createFn: func(context.Context, uint, string, string) (*vmModels.VMSnapshot, error) {
				called = true
				return nil, nil
			},
		}))

		response := testutil.PerformJSONRequest(
			t,
			router,
			http.MethodPost,
			"/vm/42/snapshots",
			[]byte(`{"name":""}`),
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", response.Code, response.Body.String())
		}
		if called {
			t.Fatal("service should not be called for an invalid body")
		}
	})

	t.Run("rejects overlong name", func(t *testing.T) {
		router := gin.New()
		router.POST("/vm/:rid/snapshots", CreateVMSnapshot(&mockVMSnapshotService{}))
		body := `{"name":"` + strings.Repeat("x", 129) + `"}`
		response := testutil.PerformJSONRequest(
			t,
			router,
			http.MethodPost,
			"/vm/42/snapshots",
			[]byte(body),
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", response.Code, response.Body.String())
		}
	})
}

func TestListVMSnapshotsHandlerMapsMissingVM(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/vm/:rid/snapshots", ListVMSnapshots(&mockVMSnapshotService{
		listFn: func(uint) ([]vmModels.VMSnapshot, error) {
			return nil, errors.Join(errors.New("failed_to_get_vm"), gorm.ErrRecordNotFound)
		},
	}))

	response := testutil.PerformRequest(t, router, http.MethodGet, "/vm/99/snapshots", nil, nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", response.Code, response.Body.String())
	}
}

func TestRollbackVMSnapshotHandlerReturnsRestartDiagnostics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/vm/:rid/snapshots/:snapshotId/rollback", RollbackVMSnapshot(&mockVMSnapshotService{
		rollbackFn: func(_ context.Context, rid uint, snapshotID uint) (libvirt.VMSnapshotRollbackResult, error) {
			if rid != 42 || snapshotID != 7 {
				t.Fatalf("unexpected identities rid=%d snapshot=%d", rid, snapshotID)
			}
			return libvirt.VMSnapshotRollbackResult{
				WasRunning: true,
				Restarted:  false,
				Warnings:   []string{"failed_to_start_vm_after_snapshot_rollback"},
			}, nil
		},
	}))

	response := testutil.PerformJSONRequest(
		t,
		router,
		http.MethodPost,
		"/vm/42/snapshots/7/rollback",
		[]byte(`{}`),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", response.Code, response.Body.String())
	}

	var envelope internal.APIResponse[libvirt.VMSnapshotRollbackResult]
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Message != "vm_snapshot_rolled_back_with_warnings" ||
		!envelope.Data.WasRunning ||
		envelope.Data.Restarted ||
		len(envelope.Data.Warnings) != 1 {
		t.Fatalf("unexpected rollback response: %+v", envelope)
	}
}

func TestDeleteVMSnapshotHandlerMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("success", func(t *testing.T) {
		router := gin.New()
		router.DELETE("/vm/:rid/snapshots/:snapshotId", DeleteVMSnapshot(&mockVMSnapshotService{
			deleteFn: func(_ context.Context, rid uint, snapshotID uint) error {
				if rid != 42 || snapshotID != 7 {
					t.Fatalf("unexpected identities rid=%d snapshot=%d", rid, snapshotID)
				}
				return nil
			},
		}))
		response := testutil.PerformRequest(
			t,
			router,
			http.MethodDelete,
			"/vm/42/snapshots/7",
			nil,
			nil,
		)
		if response.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("topology conflict", func(t *testing.T) {
		router := gin.New()
		router.DELETE("/vm/:rid/snapshots/:snapshotId", DeleteVMSnapshot(&mockVMSnapshotService{
			deleteFn: func(context.Context, uint, uint) error {
				return errors.New("replication_storage_topology_change_requires_policy_disabled")
			},
		}))
		response := testutil.PerformRequest(
			t,
			router,
			http.MethodDelete,
			"/vm/42/snapshots/7",
			nil,
			nil,
		)
		if response.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d body=%s", response.Code, response.Body.String())
		}
	})
}
