// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"context"
	"errors"
	"net/http"
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type mockVMNetworkService struct {
	attachFn      func(libvirtServiceInterfaces.NetworkAttachRequest, context.Context) (*vmModels.Network, error)
	updateFn      func(libvirtServiceInterfaces.NetworkUpdateRequest, context.Context) (*vmModels.Network, error)
	detachFn      func(libvirtServiceInterfaces.NetworkDetachRequest, context.Context) error
	attachCalls   int
	updateCalls   int
	detachCalls   int
	lastAttachReq *libvirtServiceInterfaces.NetworkAttachRequest
	lastUpdateReq *libvirtServiceInterfaces.NetworkUpdateRequest
	lastDetachReq *libvirtServiceInterfaces.NetworkDetachRequest
}

func (m *mockVMNetworkService) NetworkAttach(
	req libvirtServiceInterfaces.NetworkAttachRequest,
	ctx context.Context,
) (*vmModels.Network, error) {
	m.attachCalls++
	copied := req
	m.lastAttachReq = &copied
	if m.attachFn != nil {
		return m.attachFn(req, ctx)
	}
	return &vmModels.Network{ID: 44, VMID: 7, Emulation: req.Emulation, Enable: true}, nil
}

func (m *mockVMNetworkService) NetworkUpdate(
	req libvirtServiceInterfaces.NetworkUpdateRequest,
	ctx context.Context,
) (*vmModels.Network, error) {
	m.updateCalls++
	copied := req
	m.lastUpdateReq = &copied
	if m.updateFn != nil {
		return m.updateFn(req, ctx)
	}
	return &vmModels.Network{ID: req.NetworkID}, nil
}

func (m *mockVMNetworkService) NetworkDetach(
	req libvirtServiceInterfaces.NetworkDetachRequest,
	ctx context.Context,
) error {
	m.detachCalls++
	copied := req
	m.lastDetachReq = &copied
	if m.detachFn != nil {
		return m.detachFn(req, ctx)
	}
	return nil
}

type vmNetworkHandlerResponse struct {
	Status  string           `json:"status"`
	Message string           `json:"message"`
	Error   string           `json:"error"`
	Data    vmModels.Network `json:"data"`
}

func newVMNetworkRouter(networkService vmNetworkService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/vm/:rid/networks", NetworkAttach(networkService))
	router.PATCH("/vm/:rid/networks/:networkId", NetworkUpdate(networkService))
	router.DELETE("/vm/:rid/networks/:networkId", NetworkDetach(networkService))
	return router
}

func TestNetworkAttachUsesPathRIDAndReturnsCreatedNetwork(t *testing.T) {
	t.Parallel()

	service := &mockVMNetworkService{}
	response := testutil.PerformJSONRequest(
		t,
		newVMNetworkRouter(service),
		http.MethodPost,
		"/vm/101/networks",
		[]byte(`{"rid":999,"switchName":"lan0","emulation":"virtio","macId":12}`),
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d body=%s", response.Code, response.Body.String())
	}
	decoded := testutil.DecodeJSONResponse[vmNetworkHandlerResponse](t, response)
	if decoded.Status != "success" || decoded.Message != "network_attached" || decoded.Data.ID != 44 {
		t.Fatalf("unexpected response: %+v", decoded)
	}
	if service.attachCalls != 1 || service.lastAttachReq == nil {
		t.Fatalf("expected one attach call, got %d", service.attachCalls)
	}
	if service.lastAttachReq.RID != 101 || service.lastAttachReq.SwitchName != "lan0" {
		t.Fatalf("unexpected attach request: %+v", service.lastAttachReq)
	}
	if service.lastAttachReq.MacID == nil || *service.lastAttachReq.MacID != 12 {
		t.Fatalf("expected macId 12, got %+v", service.lastAttachReq.MacID)
	}
}

func TestNetworkUpdatePreservesFalseAndZeroAndUsesPathIdentity(t *testing.T) {
	t.Parallel()

	service := &mockVMNetworkService{}
	response := testutil.PerformJSONRequest(
		t,
		newVMNetworkRouter(service),
		http.MethodPatch,
		"/vm/101/networks/44",
		[]byte(`{"rid":999,"networkId":999,"enable":false,"macId":0}`),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", response.Code, response.Body.String())
	}
	if service.updateCalls != 1 || service.lastUpdateReq == nil {
		t.Fatalf("expected one update call, got %d", service.updateCalls)
	}
	req := service.lastUpdateReq
	if req.RID != 101 || req.NetworkID != 44 {
		t.Fatalf("expected path identity RID=101 network=44, got %+v", req)
	}
	if req.Enable == nil || *req.Enable {
		t.Fatalf("explicit enable=false was not preserved: %+v", req.Enable)
	}
	if req.MacID == nil || *req.MacID != 0 {
		t.Fatalf("explicit macId=0 was not preserved: %+v", req.MacID)
	}
}

func TestNetworkUpdateRejectsEmptyPatch(t *testing.T) {
	t.Parallel()

	service := &mockVMNetworkService{}
	response := testutil.PerformJSONRequest(
		t,
		newVMNetworkRouter(service),
		http.MethodPatch,
		"/vm/101/networks/44",
		[]byte(`{}`),
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", response.Code, response.Body.String())
	}
	if service.updateCalls != 0 {
		t.Fatalf("empty patch reached service %d times", service.updateCalls)
	}
}

func TestNetworkDetachUsesNestedPathIdentity(t *testing.T) {
	t.Parallel()

	service := &mockVMNetworkService{}
	response := testutil.PerformJSONRequest(
		t,
		newVMNetworkRouter(service),
		http.MethodDelete,
		"/vm/101/networks/44",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", response.Code, response.Body.String())
	}
	if service.detachCalls != 1 || service.lastDetachReq == nil {
		t.Fatalf("expected one detach call, got %d", service.detachCalls)
	}
	if service.lastDetachReq.RID != 101 || service.lastDetachReq.NetworkID != 44 {
		t.Fatalf("unexpected detach request: %+v", service.lastDetachReq)
	}
}

func TestNetworkHandlersRejectInvalidPathIDs(t *testing.T) {
	t.Parallel()

	service := &mockVMNetworkService{}
	for _, request := range []struct {
		method string
		path   string
		body   []byte
	}{
		{method: http.MethodPost, path: "/vm/0/networks", body: []byte(`{"switchName":"lan0","emulation":"virtio"}`)},
		{method: http.MethodPatch, path: "/vm/101/networks/0", body: []byte(`{"enable":false}`)},
		{method: http.MethodDelete, path: "/vm/101/networks/0"},
	} {
		response := testutil.PerformJSONRequest(t, newVMNetworkRouter(service), request.method, request.path, request.body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s %s: expected status 400, got %d body=%s", request.method, request.path, response.Code, response.Body.String())
		}
	}
	if service.attachCalls != 0 || service.updateCalls != 0 || service.detachCalls != 0 {
		t.Fatalf("invalid paths reached service: attach=%d update=%d detach=%d", service.attachCalls, service.updateCalls, service.detachCalls)
	}
}

func TestNetworkHandlerMapsServiceErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "bad request", err: errors.New("invalid_emulation_type: bad"), wantStatus: http.StatusBadRequest},
		{name: "forbidden", err: errors.New("replication_lease_not_owned"), wantStatus: http.StatusForbidden},
		{name: "not found", err: errors.New("failed_to_find_network_record: network_not_found: record not found"), wantStatus: http.StatusNotFound},
		{name: "gorm not found", err: gorm.ErrRecordNotFound, wantStatus: http.StatusNotFound},
		{name: "conflict", err: errors.New("failed_to_sync_vm_networks: domain_state_not_shutoff: 101"), wantStatus: http.StatusConflict},
		{name: "mountpoint conflict", err: errors.New("failed_to_write_vm_json_after_network_sync: filesystem_dataset_mountpoint_not_usable"), wantStatus: http.StatusConflict},
		{name: "unavailable", err: errors.New("failed_to_check_vm_shutoff: libvirt_connection_unavailable"), wantStatus: http.StatusServiceUnavailable},
		{name: "internal", err: errors.New("database_write_failed"), wantStatus: http.StatusInternalServerError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &mockVMNetworkService{
				attachFn: func(libvirtServiceInterfaces.NetworkAttachRequest, context.Context) (*vmModels.Network, error) {
					return nil, test.err
				},
			}
			response := testutil.PerformJSONRequest(
				t,
				newVMNetworkRouter(service),
				http.MethodPost,
				"/vm/101/networks",
				[]byte(`{"switchName":"lan0","emulation":"virtio"}`),
			)
			if response.Code != test.wantStatus {
				t.Fatalf("expected status %d, got %d body=%s", test.wantStatus, response.Code, response.Body.String())
			}
		})
	}
}
