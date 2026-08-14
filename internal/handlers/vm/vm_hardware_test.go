// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"errors"
	"net/http"
	"testing"

	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type mockVMHardwareService struct {
	cpuFn            func(uint, libvirtServiceInterfaces.ModifyCPURequest) error
	ramFn            func(uint, int) error
	vncFn            func(uint, libvirtServiceInterfaces.ModifyVNCRequest) error
	passthroughFn    func(uint, []int) error
	cpuCalls         int
	ramCalls         int
	vncCalls         int
	passthroughCalls int
	lastRID          uint
	lastCPU          libvirtServiceInterfaces.ModifyCPURequest
	lastRAM          int
	lastVNC          libvirtServiceInterfaces.ModifyVNCRequest
	lastPassthrough  []int
}

func (m *mockVMHardwareService) ModifyCPU(
	rid uint,
	req libvirtServiceInterfaces.ModifyCPURequest,
) error {
	m.cpuCalls++
	m.lastRID = rid
	m.lastCPU = req
	if m.cpuFn != nil {
		return m.cpuFn(rid, req)
	}
	return nil
}

func (m *mockVMHardwareService) ModifyRAM(rid uint, ram int) error {
	m.ramCalls++
	m.lastRID = rid
	m.lastRAM = ram
	if m.ramFn != nil {
		return m.ramFn(rid, ram)
	}
	return nil
}

func (m *mockVMHardwareService) ModifyVNC(
	rid uint,
	req libvirtServiceInterfaces.ModifyVNCRequest,
) error {
	m.vncCalls++
	m.lastRID = rid
	m.lastVNC = req
	if m.vncFn != nil {
		return m.vncFn(rid, req)
	}
	return nil
}

func (m *mockVMHardwareService) ModifyPassthrough(rid uint, pciDevices []int) error {
	m.passthroughCalls++
	m.lastRID = rid
	if pciDevices == nil {
		m.lastPassthrough = nil
	} else {
		m.lastPassthrough = append([]int{}, pciDevices...)
	}
	if m.passthroughFn != nil {
		return m.passthroughFn(rid, pciDevices)
	}
	return nil
}

type vmHardwareHandlerResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Error   string `json:"error"`
}

func newVMHardwareRouter(service vmHardwareService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/vm/:rid/hardware/cpu", ModifyCPU(service))
	router.PUT("/vm/:rid/hardware/ram", ModifyRAM(service))
	router.PUT("/vm/:rid/hardware/vnc", ModifyVNC(service))
	router.PUT("/vm/:rid/hardware/pci-devices", ModifyPassthroughDevices(service))
	return router
}

func TestVMHardwareHandlersUseNestedRID(t *testing.T) {
	t.Parallel()

	service := &mockVMHardwareService{}
	response := testutil.PerformJSONRequest(
		t,
		newVMHardwareRouter(service),
		http.MethodPut,
		"/vm/101/hardware/cpu",
		[]byte(`{"cpuSockets":2,"cpuCores":3,"cpuThreads":1,"cpuPinning":[]}`),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", response.Code, response.Body.String())
	}
	if service.cpuCalls != 1 || service.lastRID != 101 {
		t.Fatalf("expected RID 101 and one call, got rid=%d calls=%d", service.lastRID, service.cpuCalls)
	}
	if service.lastCPU.CPUSockets != 2 || service.lastCPU.CPUCores != 3 || service.lastCPU.CPUThreads != 1 {
		t.Fatalf("unexpected CPU request: %+v", service.lastCPU)
	}
}

func TestModifyVNCPreservesExplicitFalseAndEmptyPassword(t *testing.T) {
	t.Parallel()

	service := &mockVMHardwareService{}
	response := testutil.PerformJSONRequest(
		t,
		newVMHardwareRouter(service),
		http.MethodPut,
		"/vm/101/hardware/vnc",
		[]byte(`{"vncEnabled":false,"vncPort":0,"vncBind":"127.0.0.1","vncResolution":"640x480","vncPassword":"","vncWait":false}`),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", response.Code, response.Body.String())
	}
	if service.vncCalls != 1 || service.lastRID != 101 {
		t.Fatalf("expected RID 101 and one call, got rid=%d calls=%d", service.lastRID, service.vncCalls)
	}
	if service.lastVNC.VNCEnabled == nil || *service.lastVNC.VNCEnabled {
		t.Fatalf("explicit vncEnabled=false was not preserved: %+v", service.lastVNC.VNCEnabled)
	}
	if service.lastVNC.VNCWait == nil || *service.lastVNC.VNCWait {
		t.Fatalf("explicit vncWait=false was not preserved: %+v", service.lastVNC.VNCWait)
	}
	if service.lastVNC.VNCPassword != "" {
		t.Fatalf("empty VNC password was not preserved: %q", service.lastVNC.VNCPassword)
	}
}

func TestModifyVNCRejectsOmittedBooleans(t *testing.T) {
	t.Parallel()

	service := &mockVMHardwareService{}
	response := testutil.PerformJSONRequest(
		t,
		newVMHardwareRouter(service),
		http.MethodPut,
		"/vm/101/hardware/vnc",
		[]byte(`{"vncPort":5900,"vncBind":"127.0.0.1","vncResolution":"640x480","vncPassword":""}`),
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", response.Code, response.Body.String())
	}
	if service.vncCalls != 0 {
		t.Fatalf("invalid request reached service %d times", service.vncCalls)
	}
}

func TestModifyPassthroughAcceptsExplicitEmptyArray(t *testing.T) {
	t.Parallel()

	service := &mockVMHardwareService{}
	response := testutil.PerformJSONRequest(
		t,
		newVMHardwareRouter(service),
		http.MethodPut,
		"/vm/101/hardware/pci-devices",
		[]byte(`{"pciDevices":[]}`),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", response.Code, response.Body.String())
	}
	if service.passthroughCalls != 1 || service.lastPassthrough == nil || len(service.lastPassthrough) != 0 {
		t.Fatalf("explicit empty PCI list was not preserved: %#v", service.lastPassthrough)
	}
}

func TestModifyPassthroughRejectsOmittedDeviceList(t *testing.T) {
	t.Parallel()

	service := &mockVMHardwareService{}
	response := testutil.PerformJSONRequest(
		t,
		newVMHardwareRouter(service),
		http.MethodPut,
		"/vm/101/hardware/pci-devices",
		[]byte(`{}`),
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", response.Code, response.Body.String())
	}
	if service.passthroughCalls != 0 {
		t.Fatalf("request without pciDevices reached service %d times", service.passthroughCalls)
	}
}

func TestVMHardwareHandlersRejectInvalidRID(t *testing.T) {
	t.Parallel()

	service := &mockVMHardwareService{}
	for _, path := range []string{
		"/vm/0/hardware/cpu",
		"/vm/nope/hardware/ram",
		"/vm/0/hardware/vnc",
		"/vm/0/hardware/pci-devices",
	} {
		response := testutil.PerformJSONRequest(t, newVMHardwareRouter(service), http.MethodPut, path, []byte(`{}`))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected status 400, got %d body=%s", path, response.Code, response.Body.String())
		}
	}
	if service.cpuCalls != 0 || service.ramCalls != 0 || service.vncCalls != 0 || service.passthroughCalls != 0 {
		t.Fatalf(
			"invalid RIDs reached service: cpu=%d ram=%d vnc=%d pci=%d",
			service.cpuCalls,
			service.ramCalls,
			service.vncCalls,
			service.passthroughCalls,
		)
	}
}

func TestVMHardwareHandlerMapsServiceErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "bad request", err: errors.New("cpu_topology_must_be_positive"), wantStatus: http.StatusBadRequest},
		{name: "forbidden", err: errors.New("replication_lease_not_owned"), wantStatus: http.StatusForbidden},
		{name: "not found", err: gorm.ErrRecordNotFound, wantStatus: http.StatusNotFound},
		{name: "conflict", err: errors.New("domain_state_not_shutoff: 101"), wantStatus: http.StatusConflict},
		{name: "unavailable", err: errors.New("failed_to_check_vm_shutoff: libvirt_connection_unavailable"), wantStatus: http.StatusServiceUnavailable},
		{name: "internal", err: errors.New("failed_to_update_vm_ram_in_db"), wantStatus: http.StatusInternalServerError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &mockVMHardwareService{ramFn: func(uint, int) error { return test.err }}
			response := testutil.PerformJSONRequest(
				t,
				newVMHardwareRouter(service),
				http.MethodPut,
				"/vm/101/hardware/ram",
				[]byte(`{"ram":134217728}`),
			)
			if response.Code != test.wantStatus {
				t.Fatalf("expected status %d, got %d body=%s", test.wantStatus, response.Code, response.Body.String())
			}
		})
	}
}

func TestVMHardwareNoChangesIsSuccessful(t *testing.T) {
	t.Parallel()

	service := &mockVMHardwareService{ramFn: func(uint, int) error {
		return errors.New("no_changes_detected: 101")
	}}
	response := testutil.PerformJSONRequest(
		t,
		newVMHardwareRouter(service),
		http.MethodPut,
		"/vm/101/hardware/ram",
		[]byte(`{"ram":134217728}`),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", response.Code, response.Body.String())
	}
	decoded := testutil.DecodeJSONResponse[vmHardwareHandlerResponse](t, response)
	if decoded.Status != "success" || decoded.Message != "no_changes_detected" {
		t.Fatalf("unexpected no-op response: %+v", decoded)
	}
}
