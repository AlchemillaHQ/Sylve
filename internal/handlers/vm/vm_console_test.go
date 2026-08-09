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
	"net/http/httptest"
	"os"
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type vmConsoleHandlerStub struct {
	vm        vmModels.VM
	vmErr     error
	allowed   bool
	guardErr  error
	domain    *libvirtServiceInterfaces.LvDomain
	domainErr error
	vmRID     uint
	guardRID  uint
	domainRID uint
}

func (s *vmConsoleHandlerStub) GetVMByRID(rid uint) (vmModels.VM, error) {
	s.vmRID = rid
	return s.vm, s.vmErr
}

func (s *vmConsoleHandlerStub) CanMutateProtectedVM(rid uint) (bool, error) {
	s.guardRID = rid
	return s.allowed, s.guardErr
}

func (s *vmConsoleHandlerStub) GetLvDomain(
	rid uint,
) (*libvirtServiceInterfaces.LvDomain, error) {
	s.domainRID = rid
	return s.domain, s.domainErr
}

func performVMConsoleRequest(
	t *testing.T,
	path string,
	service *vmConsoleHandlerStub,
) *httptest.ResponseRecorder {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/vm/:rid/console", HandleLibvirtTerminalWebsocket(service))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

func TestParseVMConsoleRequest(t *testing.T) {
	request, err := parseVMConsoleRequest("107", "")
	if err != nil {
		t.Fatalf("parse request: %v", err)
	}
	if request.RID != 107 ||
		request.RIDText != "107" ||
		request.BaudRate != vmConsoleDefaultBaud ||
		request.DevicePath != "/dev/nmdm107B" {
		t.Fatalf("request = %+v", request)
	}

	request, err = parseVMConsoleRequest("000107", "9600")
	if err != nil {
		t.Fatalf("parse normalized request: %v", err)
	}
	if request.RIDText != "107" || request.BaudRate != "9600" {
		t.Fatalf("normalized request = %+v", request)
	}
}

func TestParseVMConsoleRequestRejectsInvalidIdentityAndBaudRate(t *testing.T) {
	tests := []struct {
		name     string
		rid      string
		baudRate string
		wantCode string
	}{
		{name: "zero RID", rid: "0", wantCode: "invalid_rid_format"},
		{name: "negative RID", rid: "-1", wantCode: "invalid_rid_format"},
		{name: "non-numeric RID", rid: "vm", wantCode: "invalid_rid_format"},
		{name: "RID overflow", rid: "4294967296", wantCode: "invalid_rid_format"},
		{name: "non-numeric baud", rid: "107", baudRate: "fast", wantCode: "invalid_baud_rate"},
		{name: "baud below minimum", rid: "107", baudRate: "49", wantCode: "invalid_baud_rate"},
		{name: "baud above maximum", rid: "107", baudRate: "4000001", wantCode: "invalid_baud_rate"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseVMConsoleRequest(test.rid, test.baudRate)
			var validationErr *vmConsoleValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("error = %v, want validation error", err)
			}
			if validationErr.Code != test.wantCode {
				t.Fatalf("code = %q, want %q", validationErr.Code, test.wantCode)
			}
		})
	}
}

func TestVMDomainSupportsConsoleOnlyForRuntimeStates(t *testing.T) {
	for _, status := range []string{"running", "BLOCKED", " paused ", "shutdown", "pmsuspended"} {
		if !vmDomainSupportsConsole(status) {
			t.Fatalf("status %q should support console", status)
		}
	}
	for _, status := range []string{"", "nostate", "shutoff", "crashed", "orphan"} {
		if vmDomainSupportsConsole(status) {
			t.Fatalf("status %q should not support console", status)
		}
	}
}

func TestVMConsoleHandlerReturnsStandardPreUpgradeErrors(t *testing.T) {
	originalDeviceStat := vmConsoleDeviceStat
	vmConsoleDeviceStat = func(string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	t.Cleanup(func() {
		vmConsoleDeviceStat = originalDeviceStat
	})

	tests := []struct {
		name       string
		path       string
		configure  func(*vmConsoleHandlerStub)
		wantStatus int
		wantCode   string
	}{
		{
			name:       "invalid RID",
			path:       "/vm/0/console",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_rid_format",
		},
		{
			name: "missing VM",
			path: "/vm/107/console",
			configure: func(service *vmConsoleHandlerStub) {
				service.vmErr = gorm.ErrRecordNotFound
			},
			wantStatus: http.StatusNotFound,
			wantCode:   "vm_not_found",
		},
		{
			name: "VM lookup failure",
			path: "/vm/107/console",
			configure: func(service *vmConsoleHandlerStub) {
				service.vmErr = errors.New("database unavailable")
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "failed_to_get_vm",
		},
		{
			name: "serial disabled",
			path: "/vm/107/console",
			configure: func(service *vmConsoleHandlerStub) {
				service.vm.Serial = false
			},
			wantStatus: http.StatusConflict,
			wantCode:   "vm_serial_console_disabled",
		},
		{
			name: "ownership guard unavailable",
			path: "/vm/107/console",
			configure: func(service *vmConsoleHandlerStub) {
				service.guardErr = errors.New("replication database unavailable")
			},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "vm_console_guard_unavailable",
		},
		{
			name: "replication lease not owned",
			path: "/vm/107/console",
			configure: func(service *vmConsoleHandlerStub) {
				service.allowed = false
			},
			wantStatus: http.StatusForbidden,
			wantCode:   "replication_lease_not_owned",
		},
		{
			name: "domain not defined",
			path: "/vm/107/console",
			configure: func(service *vmConsoleHandlerStub) {
				service.domainErr = errors.New("domain not found")
			},
			wantStatus: http.StatusConflict,
			wantCode:   "vm_domain_not_defined",
		},
		{
			name: "libvirt unavailable",
			path: "/vm/107/console",
			configure: func(service *vmConsoleHandlerStub) {
				service.domainErr = errors.New("connection refused")
			},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "libvirt_connection_unavailable",
		},
		{
			name: "nil domain",
			path: "/vm/107/console",
			configure: func(service *vmConsoleHandlerStub) {
				service.domain = nil
			},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "libvirt_connection_unavailable",
		},
		{
			name: "VM is powered off",
			path: "/vm/107/console",
			configure: func(service *vmConsoleHandlerStub) {
				service.domain.Status = "shutoff"
			},
			wantStatus: http.StatusConflict,
			wantCode:   "vm_console_requires_running_vm",
		},
		{
			name:       "serial device unavailable",
			path:       "/vm/107/console",
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "vm_serial_device_unavailable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &vmConsoleHandlerStub{
				vm:      vmModels.VM{RID: 107, Serial: true},
				allowed: true,
				domain:  &libvirtServiceInterfaces.LvDomain{Name: "107", Status: "running"},
			}
			if test.configure != nil {
				test.configure(service)
			}

			recorder := performVMConsoleRequest(t, test.path, service)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			response := decodeVMReadEnvelope(t, recorder)
			if response.Status != "error" || response.Message != test.wantCode {
				t.Fatalf("response = %+v, want code %q", response, test.wantCode)
			}
			if len(response.Data) == 0 || string(response.Data) != "null" {
				t.Fatalf("error data = %s, want null", response.Data)
			}
		})
	}
}

func TestVMSessionLastObserverCleanupRemovesManagerEntry(t *testing.T) {
	manager := &VMSessionManager{sessions: make(map[string]*VMSession)}
	observer := &VMObserver{}
	session := &VMSession{
		ID:        "vm-console-107",
		Observers: map[*VMObserver]struct{}{observer: {}},
	}
	manager.sessions[session.ID] = session

	session.RemoveObserver(observer, manager)

	if !session.IsClosed() {
		t.Fatal("session remained open after its last observer disconnected")
	}
	if _, exists := manager.sessions[session.ID]; exists {
		t.Fatal("closed session remained registered")
	}
}

func TestVMSessionHistoryIsBounded(t *testing.T) {
	session := &VMSession{
		ID:           "vm-console-107",
		Observers:    make(map[*VMObserver]struct{}),
		HistoryLimit: 4,
	}

	session.BroadcastBinary([]byte("abcdef"), &VMSessionManager{
		sessions: make(map[string]*VMSession),
	})

	if string(session.History) != "cdef" {
		t.Fatalf("history = %q, want cdef", session.History)
	}
}
