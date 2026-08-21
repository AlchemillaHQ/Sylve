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

type mockVMStorageService struct {
	attachFn      func(libvirtServiceInterfaces.StorageAttachRequest, context.Context) (*vmModels.Storage, error)
	updateFn      func(libvirtServiceInterfaces.StorageUpdateRequest, context.Context) (*vmModels.Storage, error)
	detachFn      func(libvirtServiceInterfaces.StorageDetachRequest, context.Context) error
	attachCalls   int
	updateCalls   int
	detachCalls   int
	lastAttachReq *libvirtServiceInterfaces.StorageAttachRequest
	lastUpdateReq *libvirtServiceInterfaces.StorageUpdateRequest
	lastDetachReq *libvirtServiceInterfaces.StorageDetachRequest
}

func (m *mockVMStorageService) StorageAttach(
	req libvirtServiceInterfaces.StorageAttachRequest,
	ctx context.Context,
) (*vmModels.Storage, error) {
	m.attachCalls++
	copied := req
	m.lastAttachReq = &copied
	if m.attachFn != nil {
		return m.attachFn(req, ctx)
	}
	return &vmModels.Storage{
		ID:        44,
		Name:      req.Name,
		Type:      vmModels.VMStorageType(req.StorageType),
		Emulation: vmModels.VMStorageEmulationType(req.Emulation),
	}, nil
}

func (m *mockVMStorageService) StorageUpdate(
	req libvirtServiceInterfaces.StorageUpdateRequest,
	ctx context.Context,
) (*vmModels.Storage, error) {
	m.updateCalls++
	copied := req
	m.lastUpdateReq = &copied
	if m.updateFn != nil {
		return m.updateFn(req, ctx)
	}
	return &vmModels.Storage{ID: req.ID}, nil
}

func (m *mockVMStorageService) StorageDetach(
	req libvirtServiceInterfaces.StorageDetachRequest,
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

type vmStorageHandlerResponse struct {
	Status  string           `json:"status"`
	Message string           `json:"message"`
	Error   string           `json:"error"`
	Data    vmModels.Storage `json:"data"`
}

func newVMStorageRouter(storageSvc vmStorageService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/vm/:rid/storage", StorageAttach(storageSvc))
	router.PATCH("/vm/:rid/storage/:storageId", StorageUpdate(storageSvc))
	router.DELETE("/vm/:rid/storage/:storageId", StorageDetach(storageSvc))
	return router
}

func TestStorageAttachAcceptsSupportedStorageTypesAndUsesPathRID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		body             []byte
		wantStorageType  libvirtServiceInterfaces.StorageType
		wantAttachType   libvirtServiceInterfaces.StorageAttachType
		wantEmulation    libvirtServiceInterfaces.StorageEmulationType
		assertFilesystem func(*testing.T, *libvirtServiceInterfaces.StorageAttachRequest)
	}{
		{
			name:            "raw new",
			body:            []byte(`{"rid":999,"name":"disk-raw","attachType":"new","storageType":"raw","emulation":"virtio-blk","pool":"tank","size":1073741824,"bootOrder":1}`),
			wantStorageType: libvirtServiceInterfaces.StorageTypeRaw,
			wantAttachType:  libvirtServiceInterfaces.StorageAttachTypeNew,
			wantEmulation:   libvirtServiceInterfaces.VirtIOStorageEmulation,
		},
		{
			name:            "zvol new",
			body:            []byte(`{"name":"disk-zvol","attachType":"new","storageType":"zvol","emulation":"nvme","pool":"tank","size":2147483648,"bootOrder":2}`),
			wantStorageType: libvirtServiceInterfaces.StorageTypeZVOL,
			wantAttachType:  libvirtServiceInterfaces.StorageAttachTypeNew,
			wantEmulation:   libvirtServiceInterfaces.NVMEStorageEmulation,
		},
		{
			name:            "image import",
			body:            []byte(`{"name":"ubuntu-iso","attachType":"import","storageType":"image","emulation":"ahci-cd","downloadUUID":"0a2d0fb0-d6da-46f1-bd34-74913b80b31f","bootOrder":3}`),
			wantStorageType: libvirtServiceInterfaces.StorageTypeDiskImage,
			wantAttachType:  libvirtServiceInterfaces.StorageAttachTypeImport,
			wantEmulation:   libvirtServiceInterfaces.AHCICDStorageEmulation,
		},
		{
			name:            "filesystem new",
			body:            []byte(`{"name":"shared-data","attachType":"new","storageType":"filesystem","emulation":"virtio-9p","dataset":"2532139689919762401","filesystemTarget":"shared_data","readOnly":true}`),
			wantStorageType: libvirtServiceInterfaces.StorageTypeFilesystem,
			wantAttachType:  libvirtServiceInterfaces.StorageAttachTypeNew,
			wantEmulation:   libvirtServiceInterfaces.VirtIO9PStorageEmulation,
			assertFilesystem: func(t *testing.T, req *libvirtServiceInterfaces.StorageAttachRequest) {
				t.Helper()
				if req.Dataset != "2532139689919762401" || req.FilesystemTarget != "shared_data" {
					t.Fatalf("unexpected filesystem binding: %+v", req)
				}
				if req.ReadOnly == nil || !*req.ReadOnly {
					t.Fatal("expected readOnly=true to be preserved")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			service := &mockVMStorageService{}
			router := newVMStorageRouter(service)

			response := testutil.PerformJSONRequest(t, router, http.MethodPost, "/vm/101/storage", tt.body)
			if response.Code != http.StatusCreated {
				t.Fatalf("expected status 201, got %d body=%s", response.Code, response.Body.String())
			}
			decoded := testutil.DecodeJSONResponse[vmStorageHandlerResponse](t, response)
			if decoded.Status != "success" || decoded.Message != "storage_attached" || decoded.Data.ID != 44 {
				t.Fatalf("unexpected response: %+v", decoded)
			}
			if service.attachCalls != 1 || service.lastAttachReq == nil {
				t.Fatalf("expected one service call, got %d", service.attachCalls)
			}

			got := service.lastAttachReq
			if got.RID != 101 {
				t.Fatalf("expected path RID 101, got %d", got.RID)
			}
			if got.StorageType != tt.wantStorageType || got.AttachType != tt.wantAttachType || got.Emulation != tt.wantEmulation {
				t.Fatalf("unexpected request binding: %+v", got)
			}
			if tt.assertFilesystem != nil {
				tt.assertFilesystem(t, got)
			}
		})
	}
}

func TestStorageAttachRejectsInvalidEnumsBeforeService(t *testing.T) {
	t.Parallel()

	for _, body := range [][]byte{
		[]byte(`{"name":"bad-storage","attachType":"new","storageType":"qcow2","emulation":"virtio-blk"}`),
		[]byte(`{"name":"bad-emu","attachType":"new","storageType":"raw","emulation":"ide"}`),
		[]byte(`{"name":"bad-attach","attachType":"clone","storageType":"raw","emulation":"virtio-blk"}`),
	} {
		service := &mockVMStorageService{}
		response := testutil.PerformJSONRequest(t, newVMStorageRouter(service), http.MethodPost, "/vm/101/storage", body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d body=%s", response.Code, response.Body.String())
		}
		if service.attachCalls != 0 {
			t.Fatalf("invalid request reached service %d times", service.attachCalls)
		}
	}
}

func TestStorageUpdatePreservesExplicitFalseAndUsesPathIdentity(t *testing.T) {
	t.Parallel()

	service := &mockVMStorageService{}
	body := []byte(`{"rid":999,"id":999,"name":"shared-data","emulation":"virtio-9p","enable":false,"filesystemTarget":"shared_rw","readOnly":false}`)
	response := testutil.PerformJSONRequest(t, newVMStorageRouter(service), http.MethodPatch, "/vm/101/storage/44", body)
	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", response.Code, response.Body.String())
	}
	if service.updateCalls != 1 || service.lastUpdateReq == nil {
		t.Fatalf("expected one service call, got %d", service.updateCalls)
	}

	got := service.lastUpdateReq
	if got.RID != 101 || got.ID != 44 {
		t.Fatalf("expected path identity RID=101 storage=44, got RID=%d storage=%d", got.RID, got.ID)
	}
	if got.Enable == nil || *got.Enable || got.ReadOnly == nil || *got.ReadOnly {
		t.Fatalf("explicit false values were not preserved: %+v", got)
	}
	if got.Emulation == nil || *got.Emulation != libvirtServiceInterfaces.VirtIO9PStorageEmulation {
		t.Fatalf("unexpected emulation: %+v", got.Emulation)
	}
}

func TestStorageUpdateRejectsEmptyOrInvalidPatch(t *testing.T) {
	t.Parallel()

	for _, body := range [][]byte{
		[]byte(`{}`),
		[]byte(`{"emulation":"sata"}`),
	} {
		service := &mockVMStorageService{}
		response := testutil.PerformJSONRequest(t, newVMStorageRouter(service), http.MethodPatch, "/vm/101/storage/44", body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d body=%s", response.Code, response.Body.String())
		}
		if service.updateCalls != 0 {
			t.Fatalf("invalid patch reached service %d times", service.updateCalls)
		}
	}
}

func TestStorageDetachUsesNestedPathIdentity(t *testing.T) {
	t.Parallel()

	service := &mockVMStorageService{}
	response := testutil.PerformJSONRequest(t, newVMStorageRouter(service), http.MethodDelete, "/vm/101/storage/44", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", response.Code, response.Body.String())
	}
	if service.detachCalls != 1 || service.lastDetachReq == nil {
		t.Fatalf("expected one service call, got %d", service.detachCalls)
	}
	if service.lastDetachReq.RID != 101 || service.lastDetachReq.StorageID != 44 {
		t.Fatalf("unexpected detach identity: %+v", service.lastDetachReq)
	}
}

func TestStorageHandlerMapsServiceErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid", err: errors.New("invalid_size"), wantStatus: http.StatusBadRequest},
		{name: "ownership", err: errors.New("replication_lease_not_owned"), wantStatus: http.StatusForbidden},
		{name: "missing row", err: errors.Join(errors.New("failed_to_find_storage_record"), gorm.ErrRecordNotFound), wantStatus: http.StatusNotFound},
		{name: "nested missing pool", err: errors.New("failed_to_create_storage_parent: pool_not_found: missing"), wantStatus: http.StatusNotFound},
		{name: "nested capacity conflict", err: errors.New("failed_to_create_vm_disk: insufficient_space_in_pool: tank"), wantStatus: http.StatusConflict},
		{name: "topology conflict", err: errors.New("replication_storage_topology_change_requires_policy_disabled"), wantStatus: http.StatusConflict},
		{name: "replication running", err: errors.New("replication_run_in_progress"), wantStatus: http.StatusConflict},
		{name: "unavailable", err: errors.New("failed_to_create_vm_disk: gzfs_not_initialized"), wantStatus: http.StatusServiceUnavailable},
		{name: "internal", err: errors.New("failed_to_commit_storage_metadata"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			service := &mockVMStorageService{
				attachFn: func(libvirtServiceInterfaces.StorageAttachRequest, context.Context) (*vmModels.Storage, error) {
					return nil, tt.err
				},
			}
			body := []byte(`{"name":"disk","attachType":"new","storageType":"raw","emulation":"virtio-blk","pool":"tank","size":1073741824,"bootOrder":1}`)
			response := testutil.PerformJSONRequest(t, newVMStorageRouter(service), http.MethodPost, "/vm/101/storage", body)
			if response.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d body=%s", tt.wantStatus, response.Code, response.Body.String())
			}
		})
	}
}

func TestStorageHandlersRejectInvalidPathIDs(t *testing.T) {
	t.Parallel()

	service := &mockVMStorageService{}
	response := testutil.PerformJSONRequest(t, newVMStorageRouter(service), http.MethodDelete, "/vm/not-a-rid/storage/44", nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", response.Code, response.Body.String())
	}
	if service.detachCalls != 0 {
		t.Fatalf("invalid path reached service %d times", service.detachCalls)
	}
}
