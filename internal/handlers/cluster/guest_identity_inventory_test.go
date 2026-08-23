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

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

func TestGuestIdentityInventoryInternalReadsDurableRowsWithoutRuntimeServices(t *testing.T) {
	db := newClusterHandlerTestDB(t, &vmModels.VM{}, &jailModels.Jail{})
	service := &clusterService.Service{DB: db, NodeID: "node-handler"}

	if err := db.Create(&vmModels.VM{RID: 101, Name: "vm-101"}).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}
	if err := db.Create(&jailModels.Jail{CTID: 202, Name: "jail-202"}).Error; err != nil {
		t.Fatalf("seed jail: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/intra-cluster/guest-identity-inventory", GuestIdentityInventoryInternal(service))

	recorder := performJSONRequest(
		t,
		router,
		http.MethodGet,
		"/api/intra-cluster/guest-identity-inventory",
		nil,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var response handlerAPIResponse[clusterService.GuestIdentityInventorySnapshot]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "success" || response.Message != "guest_identity_inventory_listed" {
		t.Fatalf("unexpected response: %+v", response)
	}
	if response.Data.NodeID != "node-handler" {
		t.Fatalf("node ID = %q, want node-handler", response.Data.NodeID)
	}
	entries := response.Data.Report.Entries
	if len(entries) != 2 || entries[0].GuestID != 101 || entries[1].GuestID != 202 {
		t.Fatalf("unexpected durable inventory: %+v", response.Data.Report)
	}
	canonical := clusterService.BuildGuestIdentityInventoryReport(entries)
	if response.Data.Report.Digest != canonical.Digest || len(response.Data.Report.Conflicts) != 0 {
		t.Fatalf("response report is not canonical: %+v", response.Data.Report)
	}
}

func TestGuestIdentityInventoryInternalFailsClosedWithoutService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/inventory", GuestIdentityInventoryInternal(nil))

	recorder := performJSONRequest(t, router, http.MethodGet, "/inventory", nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var response handlerAPIResponse[any]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Message != "guest_identity_inventory_scan_failed" {
		t.Fatalf("unexpected response: %+v", response)
	}
}
