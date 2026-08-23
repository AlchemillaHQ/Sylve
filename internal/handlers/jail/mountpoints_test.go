// SPDX-License-Identifier: BSD-2-Clause

package jailHandlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type jailRootMountPointHandlerStub struct {
	mountPoint string
	err        error
	ctID       uint
	called     bool
}

func (s *jailRootMountPointHandlerStub) GetJailBaseMountPoint(ctID uint) (string, error) {
	s.ctID = ctID
	s.called = true
	return s.mountPoint, s.err
}

func jailRootMountPointTestRouter(service jailRootMountPointService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/jail/:ctid/root-mountpoint", GetJailRootMountPoint(service))
	return router
}

func TestGetJailRootMountPointReturnsResolvedPath(t *testing.T) {
	stub := &jailRootMountPointHandlerStub{mountPoint: "/custom/jails/901"}
	recorder := httptest.NewRecorder()
	jailRootMountPointTestRouter(stub).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/jail/901/root-mountpoint", nil),
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !stub.called || stub.ctID != 901 {
		t.Fatalf("service call: called=%t ctid=%d, want called=true ctid=901", stub.called, stub.ctID)
	}

	response := testutil.DecodeJSONResponse[internal.APIResponse[JailRootMountPointResponse]](t, recorder)
	if response.Message != "jail_root_mountpoint_resolved" ||
		response.Data.MountPoint != "/custom/jails/901" {
		t.Fatalf("response = %+v", response)
	}
}

func TestGetJailRootMountPointRejectsInvalidCTID(t *testing.T) {
	stub := &jailRootMountPointHandlerStub{}
	recorder := httptest.NewRecorder()
	jailRootMountPointTestRouter(stub).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/jail/0/root-mountpoint", nil),
	)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.called {
		t.Fatal("service was called for an invalid CTID")
	}

	response := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, recorder)
	if response.Message != "invalid_ctid" {
		t.Fatalf("message = %q, want invalid_ctid", response.Message)
	}
}

func TestGetJailRootMountPointMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantMessage string
	}{
		{
			name:        "missing jail",
			err:         fmt.Errorf("failed_to_fetch_jail_by_ctid: %w", gorm.ErrRecordNotFound),
			wantStatus:  http.StatusNotFound,
			wantMessage: "jail_not_found",
		},
		{
			name:        "missing base storage",
			err:         errors.New("jail_base_storage_not_found"),
			wantStatus:  http.StatusConflict,
			wantMessage: "jail_base_storage_not_found",
		},
		{
			name:        "unusable dataset mountpoint",
			err:         errors.New("jail_dataset_mountpoint_not_usable: filesystem_dataset_identity_mismatch"),
			wantStatus:  http.StatusConflict,
			wantMessage: "jail_dataset_mountpoint_not_usable",
		},
		{
			name:        "zfs dependency unavailable",
			err:         errors.New("jail_dataset_mountpoint_not_usable: gzfs_not_initialized"),
			wantStatus:  http.StatusServiceUnavailable,
			wantMessage: "gzfs_not_initialized",
		},
		{
			name:        "unexpected failure",
			err:         errors.New("database_unavailable"),
			wantStatus:  http.StatusInternalServerError,
			wantMessage: "failed_to_resolve_jail_root_mountpoint",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &jailRootMountPointHandlerStub{err: test.err}
			recorder := httptest.NewRecorder()
			jailRootMountPointTestRouter(stub).ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, "/jail/902/root-mountpoint", nil),
			)

			if recorder.Code != test.wantStatus {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					recorder.Code,
					test.wantStatus,
					recorder.Body.String(),
				)
			}
			if !stub.called || stub.ctID != 902 {
				t.Fatalf("service call: called=%t ctid=%d, want called=true ctid=902", stub.called, stub.ctID)
			}

			response := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, recorder)
			if response.Message != test.wantMessage {
				t.Fatalf("message = %q, want %q", response.Message, test.wantMessage)
			}
		})
	}
}
