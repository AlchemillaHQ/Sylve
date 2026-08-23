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

	forceWarnings []string
	forceErr      error
	purgeWarnings []string
	purgeErr      error

	normalCalled bool
	forceCalled  bool
	purgeCalled  bool
	rid          uint
	deleteMacs   bool
	deleteRaw    bool
	deleteVolume bool
}

func (s *vmRemovalHandlerStub) PurgeVMRegistration(rid uint, deleteMacs bool) ([]string, error) {
	s.purgeCalled = true
	s.rid = rid
	s.deleteMacs = deleteMacs
	return s.purgeWarnings, s.purgeErr
}

func (s *vmRemovalHandlerStub) ForceRemoveVM(rid uint, deleteMacs bool, _ context.Context) ([]string, error) {
	s.forceCalled = true
	s.rid = rid
	s.deleteMacs = deleteMacs
	return s.forceWarnings, s.forceErr
}

func (s *vmRemovalHandlerStub) RemoveVMWithWarnings(
	rid uint,
	cleanUpMacs bool,
	deleteRawDisks bool,
	deleteVolumes bool,
	_ context.Context,
) (libvirt.VMRemovalResult, error) {
	s.normalCalled = true
	s.rid = rid
	s.deleteMacs = cleanUpMacs
	s.deleteRaw = deleteRawDisks
	s.deleteVolume = deleteVolumes
	return s.result, s.err
}

func performNormalVMDeleteRequest(t *testing.T, service vmRemovalService) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.DELETE("/vm/:rid", RemoveVM(service))

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

func TestRemoveVMHandlerDispatchesNormalModeWithExplicitFalseValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &vmRemovalHandlerStub{}
	router := gin.New()
	router.DELETE("/vm/:rid", RemoveVM(stub))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodDelete,
		"/vm/120?deletemacs=false&deleterawdisks=false&deletevolumes=true",
		nil,
	)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !stub.normalCalled || stub.forceCalled || stub.purgeCalled {
		t.Fatalf("dispatch = normal:%t force:%t purge:%t", stub.normalCalled, stub.forceCalled, stub.purgeCalled)
	}
	if stub.rid != 120 || stub.deleteMacs || stub.deleteRaw || !stub.deleteVolume {
		t.Fatalf(
			"normal arguments = rid:%d macs:%t raw:%t volumes:%t",
			stub.rid,
			stub.deleteMacs,
			stub.deleteRaw,
			stub.deleteVolume,
		)
	}
}

func TestRemoveVMHandlerDispatchesForceModeWithDefaultMACCleanup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &vmRemovalHandlerStub{forceWarnings: []string{"cleanup_warning"}}
	router := gin.New()
	router.DELETE("/vm/:rid", RemoveVM(stub))

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/vm/121?force=true", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !stub.forceCalled || stub.normalCalled || stub.purgeCalled {
		t.Fatalf("dispatch = normal:%t force:%t purge:%t", stub.normalCalled, stub.forceCalled, stub.purgeCalled)
	}
	if stub.rid != 121 || !stub.deleteMacs {
		t.Fatalf("force arguments = rid:%d deleteMacs:%t", stub.rid, stub.deleteMacs)
	}

	var response struct {
		Message string                  `json:"message"`
		Data    libvirt.VMRemovalResult `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Message != "vm_force_removed_with_warnings" || len(response.Data.Warnings) != 1 {
		t.Fatalf("force response = %+v", response)
	}
	if response.Data.RetainedDatasets == nil || len(response.Data.RetainedDatasets) != 0 {
		t.Fatalf("retained datasets = %#v, want empty array", response.Data.RetainedDatasets)
	}
}

func TestPurgeVMRegistrationHandlerUsesSeparateModeAndDefaults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &vmRemovalHandlerStub{purgeWarnings: []string{"runtime_cleanup_warning"}}
	router := gin.New()
	router.DELETE("/vm/:rid/registration", PurgeVMRegistration(stub))

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/vm/122/registration", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !stub.purgeCalled || stub.normalCalled || stub.forceCalled {
		t.Fatalf("dispatch = normal:%t force:%t purge:%t", stub.normalCalled, stub.forceCalled, stub.purgeCalled)
	}
	if stub.rid != 122 || !stub.deleteMacs {
		t.Fatalf("purge arguments = rid:%d deleteMacs:%t", stub.rid, stub.deleteMacs)
	}

	var response struct {
		Message string                  `json:"message"`
		Data    libvirt.VMRemovalResult `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Message != "vm_registration_purged_with_warnings" || len(response.Data.Warnings) != 1 {
		t.Fatalf("purge response = %+v", response)
	}
}

func TestRemoveVMHandlerRejectsMissingOrInvalidModeFlagsBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		target string
		code   string
	}{
		{name: "missing normal flag", target: "/vm/123?deletemacs=true&deleterawdisks=false", code: "missing_deletevolumes_param"},
		{name: "invalid force flag", target: "/vm/123?force=maybe", code: "invalid_force_param"},
		{name: "invalid force MAC flag", target: "/vm/123?force=true&deletemacs=maybe", code: "invalid_deletemacs_param"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &vmRemovalHandlerStub{}
			router := gin.New()
			router.DELETE("/vm/:rid", RemoveVM(stub))

			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, tt.target, nil))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			if stub.normalCalled || stub.forceCalled || stub.purgeCalled {
				t.Fatalf("service called for invalid request: %+v", stub)
			}
			var response struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Message != tt.code {
				t.Fatalf("message = %q, want %q", response.Message, tt.code)
			}
		})
	}
}
