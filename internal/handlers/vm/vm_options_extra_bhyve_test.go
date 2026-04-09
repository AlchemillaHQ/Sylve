// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

func requireSystemUUIDForVMOptionsOrSkip(t *testing.T) {
	t.Helper()
	if _, err := utils.GetSystemUUID(); err != nil {
		t.Skipf("system uuid unavailable in test environment: %v", err)
	}
}

func TestModifyExtraBhyveOptions_InvalidRID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/options/extra-bhyve-options/:rid", ModifyExtraBhyveOptions(&libvirt.Service{}))

	req := httptest.NewRequest(
		http.MethodPut,
		"/options/extra-bhyve-options/not-a-number",
		bytes.NewBufferString(`{"extraBhyveOptions":["-S"]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d, body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
}

func TestModifyExtraBhyveOptions_InvalidPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/options/extra-bhyve-options/:rid", ModifyExtraBhyveOptions(&libvirt.Service{}))

	req := httptest.NewRequest(
		http.MethodPut,
		"/options/extra-bhyve-options/101",
		bytes.NewBufferString(`{"extraBhyveOptions":"-S"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d, body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
}

func TestModifyExtraBhyveOptions_ServiceErrorReturns500(t *testing.T) {
	requireSystemUUIDForVMOptionsOrSkip(t)
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(
		t,
		&vmModels.VM{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
	)

	vmRecord := vmModels.VM{
		Name:        "vm-extra-bhyve-handler-test",
		RID:         101,
		CPUSockets:  1,
		CPUCores:    1,
		CPUThreads:  1,
		RAM:         1024 * 1024 * 512,
		VNCBind:     "127.0.0.1",
		VNCPort:     5901,
		VNCEnabled:  false,
		TimeOffset:  vmModels.TimeOffsetUTC,
		StartAtBoot: false,
		StartOrder:  0,
	}
	if err := db.Create(&vmRecord).Error; err != nil {
		t.Fatalf("failed to seed vm row: %v", err)
	}

	libvirtService := &libvirt.Service{DB: db}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/options/extra-bhyve-options/:rid", ModifyExtraBhyveOptions(libvirtService))

	req := httptest.NewRequest(
		http.MethodPut,
		"/options/extra-bhyve-options/101",
		bytes.NewBufferString(`{"extraBhyveOptions":["-S","-u"]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf(
			"status code = %d, want %d, body=%s",
			rr.Code,
			http.StatusInternalServerError,
			rr.Body.String(),
		)
	}

	if !strings.Contains(rr.Body.String(), `"internal_server_error"`) {
		t.Fatalf("expected internal_server_error response, got body=%s", rr.Body.String())
	}
}
