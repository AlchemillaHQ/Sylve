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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/gin-gonic/gin"
)

type vmRemovalHandlerStub struct {
	result libvirt.VMRemovalResult
	err    error
}

func (s *vmRemovalHandlerStub) PurgeVMRegistration(uint, bool) ([]string, error) {
	return nil, errors.New("unexpected purge call")
}

func (s *vmRemovalHandlerStub) ForceRemoveVM(uint, bool, context.Context) ([]string, error) {
	return nil, errors.New("unexpected force call")
}

func (s *vmRemovalHandlerStub) RemoveVMWithWarnings(
	uint,
	bool,
	bool,
	bool,
	context.Context,
) (libvirt.VMRemovalResult, error) {
	return s.result, s.err
}

func performNormalVMDeleteRequest(t *testing.T, service vmRemovalService) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.DELETE("/vm/:id", RemoveVM(service))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodDelete,
		"/vm/100?deletemacs=true&deleterawdisks=false&deletevolumes=false&force=false",
		nil,
	)
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestRemoveVMHandlerReturnsStructuredCleanupWarnings(t *testing.T) {
	recorder := performNormalVMDeleteRequest(t, &vmRemovalHandlerStub{
		result: libvirt.VMRemovalResult{
			Warnings: []string{
				"storage_cleanup_incomplete: dataset=tank/sylve/virtual-machines/100/raw-1: busy",
			},
			RetainedDatasets: []string{"tank/sylve/virtual-machines/100/raw-1"},
		},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		Status  string                  `json:"status"`
		Message string                  `json:"message"`
		Data    libvirt.VMRemovalResult `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "success" || response.Message != "vm_removed_with_warnings" {
		t.Fatalf("response = %+v", response)
	}
	if len(response.Data.Warnings) != 1 || len(response.Data.RetainedDatasets) != 1 {
		t.Fatalf("structured cleanup data = %+v", response.Data)
	}
}

func TestRemoveVMHandlerReturnsRetainedDatasetsWithoutWarning(t *testing.T) {
	recorder := performNormalVMDeleteRequest(t, &vmRemovalHandlerStub{
		result: libvirt.VMRemovalResult{
			Warnings:         []string{},
			RetainedDatasets: []string{"tank/sylve/virtual-machines/100/raw-1"},
		},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		Message string                  `json:"message"`
		Data    libvirt.VMRemovalResult `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Message != "vm_removed" {
		t.Fatalf("message = %q, want vm_removed", response.Message)
	}
	if len(response.Data.RetainedDatasets) != 1 {
		t.Fatalf("retained datasets = %v", response.Data.RetainedDatasets)
	}
}

func TestRemoveVMHandlerCriticalFailureRemainsError(t *testing.T) {
	recorder := performNormalVMDeleteRequest(t, &vmRemovalHandlerStub{
		err: errors.New("failed_to_remove_vm_identity: database unavailable"),
	})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "error" || response.Message != "failed_to_remove_vm" {
		t.Fatalf("response = %+v", response)
	}
}

func TestRemoveVMHandlerMapsRevalidatedPolicyConflict(t *testing.T) {
	recorder := performNormalVMDeleteRequest(t, &vmRemovalHandlerStub{
		err: errors.New("failed_to_remove_vm_identity: guest_delete_requires_replication_policy_removed"),
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "error" || response.Message != "guest_delete_requires_replication_policy_removed" {
		t.Fatalf("response = %+v", response)
	}
}
