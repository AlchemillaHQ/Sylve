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
	"net/http"
	"testing"

	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

type mockVMStorageService struct {
	attachFn      func(req libvirtServiceInterfaces.StorageAttachRequest, ctx context.Context) error
	updateFn      func(req libvirtServiceInterfaces.StorageUpdateRequest, ctx context.Context) error
	detachFn      func(req libvirtServiceInterfaces.StorageDetachRequest) error
	attachCalls   int
	updateCalls   int
	detachCalls   int
	lastAttachReq *libvirtServiceInterfaces.StorageAttachRequest
	lastUpdateReq *libvirtServiceInterfaces.StorageUpdateRequest
	lastDetachReq *libvirtServiceInterfaces.StorageDetachRequest
}

func (m *mockVMStorageService) StorageAttach(req libvirtServiceInterfaces.StorageAttachRequest, ctx context.Context) error {
	m.attachCalls++
	copied := req
	m.lastAttachReq = &copied
	if m.attachFn != nil {
		return m.attachFn(req, ctx)
	}
	return nil
}

func (m *mockVMStorageService) StorageUpdate(req libvirtServiceInterfaces.StorageUpdateRequest, ctx context.Context) error {
	m.updateCalls++
	copied := req
	m.lastUpdateReq = &copied
	if m.updateFn != nil {
		return m.updateFn(req, ctx)
	}
	return nil
}

func (m *mockVMStorageService) StorageDetach(req libvirtServiceInterfaces.StorageDetachRequest) error {
	m.detachCalls++
	copied := req
	m.lastDetachReq = &copied
	if m.detachFn != nil {
		return m.detachFn(req)
	}
	return nil
}

type vmStorageHandlerResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Error   string `json:"error"`
}

func newVMStorageRouter(storageSvc vmStorageService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/vm/storage/attach", StorageAttach(storageSvc))
	r.PUT("/vm/storage/update", StorageUpdate(storageSvc))
	r.POST("/vm/storage/detach", StorageDetach(storageSvc))
	return r
}

func TestStorageAttachAcceptsSupportedStorageTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		body             []byte
		wantStorageType  libvirtServiceInterfaces.StorageType
		wantAttachType   libvirtServiceInterfaces.StorageAttachType
		wantEmulation    libvirtServiceInterfaces.StorageEmulationType
		assertFilesystem func(t *testing.T, req *libvirtServiceInterfaces.StorageAttachRequest)
	}{
		{
			name:            "raw new",
			body:            []byte(`{"rid":101,"name":"disk-raw","attachType":"new","storageType":"raw","emulation":"virtio-blk","pool":"tank","size":1073741824,"bootOrder":1}`),
			wantStorageType: libvirtServiceInterfaces.StorageTypeRaw,
			wantAttachType:  libvirtServiceInterfaces.StorageAttachTypeNew,
			wantEmulation:   libvirtServiceInterfaces.VirtIOStorageEmulation,
		},
		{
			name:            "zvol new",
			body:            []byte(`{"rid":101,"name":"disk-zvol","attachType":"new","storageType":"zvol","emulation":"nvme","pool":"tank","size":2147483648,"bootOrder":2}`),
			wantStorageType: libvirtServiceInterfaces.StorageTypeZVOL,
			wantAttachType:  libvirtServiceInterfaces.StorageAttachTypeNew,
			wantEmulation:   libvirtServiceInterfaces.NVMEStorageEmulation,
		},
		{
			name:            "image import",
			body:            []byte(`{"rid":101,"name":"ubuntu-iso","attachType":"import","storageType":"image","emulation":"ahci-cd","downloadUUID":"0a2d0fb0-d6da-46f1-bd34-74913b80b31f","bootOrder":3}`),
			wantStorageType: libvirtServiceInterfaces.StorageTypeDiskImage,
			wantAttachType:  libvirtServiceInterfaces.StorageAttachTypeImport,
			wantEmulation:   libvirtServiceInterfaces.AHCICDStorageEmulation,
		},
		{
			name:            "filesystem 9p new",
			body:            []byte(`{"rid":101,"name":"shared-data","attachType":"new","storageType":"filesystem","emulation":"virtio-9p","dataset":"2532139689919762401","filesystemTarget":"shared_data","readOnly":true,"bootOrder":4}`),
			wantStorageType: libvirtServiceInterfaces.StorageTypeFilesystem,
			wantAttachType:  libvirtServiceInterfaces.StorageAttachTypeNew,
			wantEmulation:   libvirtServiceInterfaces.VirtIO9PStorageEmulation,
			assertFilesystem: func(t *testing.T, req *libvirtServiceInterfaces.StorageAttachRequest) {
				t.Helper()
				if req.Dataset != "2532139689919762401" {
					t.Fatalf("expected dataset guid to be bound, got %q", req.Dataset)
				}
				if req.FilesystemTarget != "shared_data" {
					t.Fatalf("expected filesystemTarget to be bound, got %q", req.FilesystemTarget)
				}
				if req.ReadOnly == nil || !*req.ReadOnly {
					t.Fatalf("expected readOnly=true to be bound")
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			storageSvc := &mockVMStorageService{}
			r := newVMStorageRouter(storageSvc)

			rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/vm/storage/attach", tt.body)
			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d body=%s", rr.Code, rr.Body.String())
			}

			resp := testutil.DecodeJSONResponse[vmStorageHandlerResponse](t, rr)
			if resp.Status != "success" || resp.Message != "storage_attached" {
				t.Fatalf("unexpected response: %+v", resp)
			}

			if storageSvc.attachCalls != 1 {
				t.Fatalf("expected exactly 1 StorageAttach call, got %d", storageSvc.attachCalls)
			}
			if storageSvc.lastAttachReq == nil {
				t.Fatalf("expected StorageAttach request to be captured")
			}

			got := storageSvc.lastAttachReq
			if got.StorageType != tt.wantStorageType {
				t.Fatalf("expected storageType=%q, got %q", tt.wantStorageType, got.StorageType)
			}
			if got.AttachType != tt.wantAttachType {
				t.Fatalf("expected attachType=%q, got %q", tt.wantAttachType, got.AttachType)
			}
			if got.Emulation != tt.wantEmulation {
				t.Fatalf("expected emulation=%q, got %q", tt.wantEmulation, got.Emulation)
			}

			if tt.assertFilesystem != nil {
				tt.assertFilesystem(t, got)
			}
		})
	}
}

func TestStorageAttachRejectsInvalidEnumsBeforeService(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body []byte
	}{
		{
			name: "unsupported storage type",
			body: []byte(`{"rid":101,"name":"bad-storage","attachType":"new","storageType":"qcow2","emulation":"virtio-blk","pool":"tank","size":1073741824,"bootOrder":1}`),
		},
		{
			name: "unsupported emulation",
			body: []byte(`{"rid":101,"name":"bad-emu","attachType":"new","storageType":"raw","emulation":"ide","pool":"tank","size":1073741824,"bootOrder":1}`),
		},
		{
			name: "unsupported attach type",
			body: []byte(`{"rid":101,"name":"bad-attach","attachType":"clone","storageType":"raw","emulation":"virtio-blk","pool":"tank","size":1073741824,"bootOrder":1}`),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			storageSvc := &mockVMStorageService{}
			r := newVMStorageRouter(storageSvc)

			rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/vm/storage/attach", tt.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d body=%s", rr.Code, rr.Body.String())
			}

			resp := testutil.DecodeJSONResponse[vmStorageHandlerResponse](t, rr)
			if resp.Message != "invalid_request" {
				t.Fatalf("expected invalid_request message, got %q", resp.Message)
			}
			if storageSvc.attachCalls != 0 {
				t.Fatalf("expected StorageAttach not to be called, got %d calls", storageSvc.attachCalls)
			}
		})
	}
}

func TestStorageUpdateAcceptsVirtio9PEmulation(t *testing.T) {
	t.Parallel()

	storageSvc := &mockVMStorageService{}
	r := newVMStorageRouter(storageSvc)

	body := []byte(`{"id":44,"name":"shared-data","emulation":"virtio-9p","bootOrder":2,"filesystemTarget":"shared_rw","readOnly":false}`)
	rr := testutil.PerformJSONRequest(t, r, http.MethodPut, "/vm/storage/update", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	resp := testutil.DecodeJSONResponse[vmStorageHandlerResponse](t, rr)
	if resp.Status != "success" || resp.Message != "storage_updated" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	if storageSvc.updateCalls != 1 {
		t.Fatalf("expected exactly 1 StorageUpdate call, got %d", storageSvc.updateCalls)
	}
	if storageSvc.lastUpdateReq == nil {
		t.Fatalf("expected StorageUpdate request to be captured")
	}
	if storageSvc.lastUpdateReq.Emulation != libvirtServiceInterfaces.VirtIO9PStorageEmulation {
		t.Fatalf("expected emulation=virtio-9p, got %q", storageSvc.lastUpdateReq.Emulation)
	}
}

func TestStorageUpdateRejectsUnsupportedEmulation(t *testing.T) {
	t.Parallel()

	storageSvc := &mockVMStorageService{}
	r := newVMStorageRouter(storageSvc)

	body := []byte(`{"id":44,"name":"disk","emulation":"sata","bootOrder":2}`)
	rr := testutil.PerformJSONRequest(t, r, http.MethodPut, "/vm/storage/update", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", rr.Code, rr.Body.String())
	}

	resp := testutil.DecodeJSONResponse[vmStorageHandlerResponse](t, rr)
	if resp.Message != "invalid_request" {
		t.Fatalf("expected invalid_request message, got %q", resp.Message)
	}
	if storageSvc.updateCalls != 0 {
		t.Fatalf("expected StorageUpdate not to be called, got %d calls", storageSvc.updateCalls)
	}
}
