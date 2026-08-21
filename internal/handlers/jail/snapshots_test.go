// SPDX-License-Identifier: BSD-2-Clause

package jailHandlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type mockJailSnapshotService struct {
	listFn     func(ctID uint) ([]jailModels.JailSnapshot, error)
	createFn   func(ctx context.Context, ctID uint, name string, description string) (*jailModels.JailSnapshot, error)
	rollbackFn func(ctx context.Context, ctID uint, snapshotID uint) (jail.JailSnapshotRollbackResult, error)
	deleteFn   func(ctx context.Context, ctID uint, snapshotID uint) error
}

func (m *mockJailSnapshotService) ListJailSnapshots(ctID uint) ([]jailModels.JailSnapshot, error) {
	if m.listFn != nil {
		return m.listFn(ctID)
	}
	return []jailModels.JailSnapshot{}, nil
}

func (m *mockJailSnapshotService) CreateJailSnapshot(
	ctx context.Context,
	ctID uint,
	name string,
	description string,
) (*jailModels.JailSnapshot, error) {
	if m.createFn != nil {
		return m.createFn(ctx, ctID, name, description)
	}
	return &jailModels.JailSnapshot{ID: 1, CTID: ctID, Name: name, Description: description}, nil
}

func (m *mockJailSnapshotService) RollbackJailSnapshot(
	ctx context.Context,
	ctID uint,
	snapshotID uint,
) (jail.JailSnapshotRollbackResult, error) {
	if m.rollbackFn != nil {
		return m.rollbackFn(ctx, ctID, snapshotID)
	}
	return jail.JailSnapshotRollbackResult{Warnings: []string{}}, nil
}

func (m *mockJailSnapshotService) DeleteJailSnapshot(
	ctx context.Context,
	ctID uint,
	snapshotID uint,
) error {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, ctID, snapshotID)
	}
	return nil
}

func TestJailSnapshotErrorStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "validation", err: errors.New("snapshot_name_required"), want: http.StatusBadRequest},
		{name: "ownership", err: errors.New("replication_lease_not_owned"), want: http.StatusForbidden},
		{name: "snapshot missing", err: errors.New("snapshot_not_found: missing"), want: http.StatusNotFound},
		{name: "wrapped jail missing", err: errors.Join(errors.New("failed_to_get_jail"), gorm.ErrRecordNotFound), want: http.StatusNotFound},
		{name: "topology", err: errors.New("replication_storage_topology_change_requires_policy_disabled"), want: http.StatusConflict},
		{name: "replication running", err: errors.New("replication_run_in_progress"), want: http.StatusConflict},
		{name: "physical snapshot missing", err: errors.New("jail_snapshot_dataset_missing: zroot/jail@snap"), want: http.StatusConflict},
		{name: "zfs unavailable", err: errors.New("gzfs_not_initialized"), want: http.StatusServiceUnavailable},
		{name: "unexpected", err: errors.New("failed_to_query_database"), want: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := jailSnapshotErrorStatus(tt.err); got != tt.want {
				t.Fatalf("jailSnapshotErrorStatus(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestCreateJailSnapshotHandlerUsesNestedCTIDRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotCTID uint
	router := gin.New()
	router.POST("/jail/:ctid/snapshots", CreateJailSnapshot(&mockJailSnapshotService{
		createFn: func(_ context.Context, ctID uint, name string, description string) (*jailModels.JailSnapshot, error) {
			gotCTID = ctID
			return &jailModels.JailSnapshot{ID: 9, CTID: ctID, Name: name, Description: description}, nil
		},
	}))

	response := testutil.PerformJSONRequest(
		t,
		router,
		http.MethodPost,
		"/jail/42/snapshots",
		[]byte(`{"name":"before-upgrade","description":"known good"}`),
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", response.Code, response.Body.String())
	}
	if gotCTID != 42 {
		t.Fatalf("service CTID = %d, want 42", gotCTID)
	}

	var envelope internal.APIResponse[jailModels.JailSnapshot]
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.ID != 9 || envelope.Data.CTID != 42 {
		t.Fatalf("unexpected snapshot response: %+v", envelope.Data)
	}
}

func TestCreateJailSnapshotHandlerRejectsOverlongName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/jail/:ctid/snapshots", CreateJailSnapshot(&mockJailSnapshotService{}))
	body := `{"name":"` + strings.Repeat("x", 129) + `"}`
	response := testutil.PerformJSONRequest(t, router, http.MethodPost, "/jail/42/snapshots", []byte(body))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", response.Code, response.Body.String())
	}
}

func TestRollbackJailSnapshotHandlerReturnsRestartDiagnostics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/jail/:ctid/snapshots/:snapshotId/rollback", RollbackJailSnapshot(&mockJailSnapshotService{
		rollbackFn: func(_ context.Context, ctID uint, snapshotID uint) (jail.JailSnapshotRollbackResult, error) {
			if ctID != 42 || snapshotID != 7 {
				t.Fatalf("unexpected identities ctid=%d snapshot=%d", ctID, snapshotID)
			}
			return jail.JailSnapshotRollbackResult{
				WasRunning: true,
				Restarted:  false,
				Warnings:   []string{"failed_to_start_jail_after_snapshot_rollback"},
			}, nil
		},
	}))

	response := testutil.PerformJSONRequest(
		t,
		router,
		http.MethodPost,
		"/jail/42/snapshots/7/rollback",
		[]byte(`{}`),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", response.Code, response.Body.String())
	}

	var envelope internal.APIResponse[jail.JailSnapshotRollbackResult]
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Message != "jail_snapshot_rolled_back_with_warnings" ||
		!envelope.Data.WasRunning || envelope.Data.Restarted || len(envelope.Data.Warnings) != 1 {
		t.Fatalf("unexpected rollback response: %+v", envelope)
	}
}

func TestDeleteJailSnapshotHandlerUsesDistinctResourceIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.DELETE("/jail/:ctid/snapshots/:snapshotId", DeleteJailSnapshot(&mockJailSnapshotService{
		deleteFn: func(_ context.Context, ctID uint, snapshotID uint) error {
			if ctID != 42 || snapshotID != 7 {
				t.Fatalf("unexpected identities ctid=%d snapshot=%d", ctID, snapshotID)
			}
			return nil
		},
	}))

	response := testutil.PerformRequest(t, router, http.MethodDelete, "/jail/42/snapshots/7", nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", response.Code, response.Body.String())
	}
}
