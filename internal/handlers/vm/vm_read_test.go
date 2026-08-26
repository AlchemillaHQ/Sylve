// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type vmDomainHandlerStub struct {
	vmID            uint
	vmIDErr         error
	domain          *libvirtServiceInterfaces.LvDomain
	domainErr       error
	registrationRID uint
	domainRID       uint
}

func (s *vmDomainHandlerStub) GetVMIDByRID(rid uint) (uint, error) {
	s.registrationRID = rid
	return s.vmID, s.vmIDErr
}

func (s *vmDomainHandlerStub) GetLvDomain(rid uint) (*libvirtServiceInterfaces.LvDomain, error) {
	s.domainRID = rid
	return s.domain, s.domainErr
}

type vmDomainLifecycleStub struct {
	task      *taskModels.GuestLifecycleTask
	err       error
	guestType string
	guestID   uint
}

func (s *vmDomainLifecycleStub) GetActiveTaskForGuest(
	guestType string,
	guestID uint,
) (*taskModels.GuestLifecycleTask, error) {
	s.guestType = guestType
	s.guestID = guestID
	return s.task, s.err
}

type vmLogsHandlerStub struct {
	logs string
	err  error
	rid  uint
}

func (s *vmLogsHandlerStub) GetVMLogs(rid uint) (string, error) {
	s.rid = rid
	return s.logs, s.err
}

type vmReadEnvelope struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

func decodeVMReadEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) vmReadEnvelope {
	t.Helper()

	var response vmReadEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, recorder.Body.String())
	}
	return response
}

func performVMDomainRequest(
	t *testing.T,
	path string,
	domainService *vmDomainHandlerStub,
	lifecycleService *vmDomainLifecycleStub,
) *httptest.ResponseRecorder {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/vm/:rid/domain", GetLvDomain(domainService, lifecycleService))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

func TestGetLvDomainValidatesRegisteredRIDBeforeLibvirtLookup(t *testing.T) {
	t.Run("invalid RID", func(t *testing.T) {
		domainService := &vmDomainHandlerStub{}
		recorder := performVMDomainRequest(
			t,
			"/vm/0/domain",
			domainService,
			&vmDomainLifecycleStub{},
		)

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
		}
		if domainService.registrationRID != 0 || domainService.domainRID != 0 {
			t.Fatalf("service called for invalid RID: %+v", domainService)
		}
		if response := decodeVMReadEnvelope(t, recorder); response.Message != "invalid_rid_format" {
			t.Fatalf("message = %q, want invalid_rid_format", response.Message)
		}
	})

	t.Run("registration lookup failure", func(t *testing.T) {
		domainService := &vmDomainHandlerStub{vmIDErr: errors.New("database unavailable")}
		recorder := performVMDomainRequest(
			t,
			"/vm/107/domain",
			domainService,
			&vmDomainLifecycleStub{},
		)

		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
		}
		if response := decodeVMReadEnvelope(t, recorder); response.Message != "failed_to_get_vm_domain_registration" {
			t.Fatalf("message = %q", response.Message)
		}
		if domainService.domainRID != 0 {
			t.Fatalf("libvirt lookup ran after registration failure")
		}
	})

	t.Run("VM is not registered", func(t *testing.T) {
		domainService := &vmDomainHandlerStub{}
		recorder := performVMDomainRequest(
			t,
			"/vm/107/domain",
			domainService,
			&vmDomainLifecycleStub{},
		)

		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
		}
		if response := decodeVMReadEnvelope(t, recorder); response.Message != "vm_not_found" {
			t.Fatalf("message = %q, want vm_not_found", response.Message)
		}
		if domainService.domainRID != 0 {
			t.Fatalf("libvirt lookup ran for an unregistered VM")
		}
	})
}

func TestGetLvDomainRepresentsOnlyRegisteredMissingDomainAsOrphan(t *testing.T) {
	domainService := &vmDomainHandlerStub{
		vmID:      9,
		domainErr: errors.New("domain not found"),
	}
	lifecycleService := &vmDomainLifecycleStub{
		task: &taskModels.GuestLifecycleTask{
			Action:            "shutdown",
			OverrideRequested: true,
		},
	}
	recorder := performVMDomainRequest(
		t,
		"/vm/107/domain",
		domainService,
		lifecycleService,
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeVMReadEnvelope(t, recorder)
	if response.Message != "vm_domain_orphaned" {
		t.Fatalf("message = %q, want vm_domain_orphaned", response.Message)
	}

	var domain libvirtServiceInterfaces.LvDomain
	if err := json.Unmarshal(response.Data, &domain); err != nil {
		t.Fatalf("decode domain: %v", err)
	}
	if domain.ID != -1 || domain.Name != "107" || domain.Status != "orphan" {
		t.Fatalf("orphan domain = %+v", domain)
	}
	if domain.PendingAction != "shutdown" || !domain.OverrideRequested {
		t.Fatalf("lifecycle state = %+v", domain)
	}
	if lifecycleService.guestType != taskModels.GuestTypeVM || lifecycleService.guestID != 107 {
		t.Fatalf("lifecycle lookup = %q/%d", lifecycleService.guestType, lifecycleService.guestID)
	}
}

func TestGetLvDomainMapsRuntimeAndLifecycleFailures(t *testing.T) {
	tests := []struct {
		name         string
		domain       *libvirtServiceInterfaces.LvDomain
		domainErr    error
		lifecycleErr error
		wantStatus   int
		wantCode     string
	}{
		{
			name:       "libvirt unavailable",
			domainErr:  errors.New("connection refused"),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "libvirt_connection_unavailable",
		},
		{
			name:       "nil domain",
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "libvirt_connection_unavailable",
		},
		{
			name:         "lifecycle lookup failure",
			domain:       &libvirtServiceInterfaces.LvDomain{ID: 3, Name: "107", Status: "running"},
			lifecycleErr: errors.New("lifecycle database unavailable"),
			wantStatus:   http.StatusInternalServerError,
			wantCode:     "failed_to_get_vm_lifecycle_state",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			domainService := &vmDomainHandlerStub{
				vmID:      9,
				domain:    test.domain,
				domainErr: test.domainErr,
			}
			recorder := performVMDomainRequest(
				t,
				"/vm/107/domain",
				domainService,
				&vmDomainLifecycleStub{err: test.lifecycleErr},
			)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if response := decodeVMReadEnvelope(t, recorder); response.Message != test.wantCode {
				t.Fatalf("message = %q, want %q", response.Message, test.wantCode)
			}
		})
	}
}

func TestGetLvDomainReturnsRuntimeState(t *testing.T) {
	domainService := &vmDomainHandlerStub{
		vmID: 9,
		domain: &libvirtServiceInterfaces.LvDomain{
			ID:     3,
			UUID:   "domain-uuid",
			Name:   "107",
			Status: "running",
		},
	}
	recorder := performVMDomainRequest(
		t,
		"/vm/107/domain",
		domainService,
		&vmDomainLifecycleStub{},
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeVMReadEnvelope(t, recorder)
	if response.Message != "vm_domain_retrieved" {
		t.Fatalf("message = %q", response.Message)
	}
}

func performVMLogsRequest(
	t *testing.T,
	path string,
	service *vmLogsHandlerStub,
) *httptest.ResponseRecorder {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/vm/:rid/logs", GetVMLogs(service))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

func TestGetVMLogsValidatesIdentityAndMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		err        error
		wantStatus int
		wantCode   string
		wantRID    uint
	}{
		{
			name:       "invalid RID",
			path:       "/vm/0/logs",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_rid_format",
		},
		{
			name:       "missing VM",
			path:       "/vm/107/logs",
			err:        gorm.ErrRecordNotFound,
			wantStatus: http.StatusNotFound,
			wantCode:   "vm_not_found",
			wantRID:    107,
		},
		{
			name:       "log read failure",
			path:       "/vm/107/logs",
			err:        errors.New("permission denied"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "failed_to_get_vm_logs",
			wantRID:    107,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &vmLogsHandlerStub{err: test.err}
			recorder := performVMLogsRequest(t, test.path, service)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if response := decodeVMReadEnvelope(t, recorder); response.Message != test.wantCode {
				t.Fatalf("message = %q, want %q", response.Message, test.wantCode)
			}
			if service.rid != test.wantRID {
				t.Fatalf("service RID = %d, want %d", service.rid, test.wantRID)
			}
		})
	}
}

func TestGetVMLogsReturnsNamedResponsePayload(t *testing.T) {
	service := &vmLogsHandlerStub{logs: "console output\n"}
	recorder := performVMLogsRequest(t, "/vm/107/logs", service)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeVMReadEnvelope(t, recorder)
	if response.Message != "vm_logs_retrieved" {
		t.Fatalf("message = %q", response.Message)
	}
	var data VMLogsResponse
	if err := json.Unmarshal(response.Data, &data); err != nil {
		t.Fatalf("decode logs payload: %v", err)
	}
	if data.Logs != "console output\n" || service.rid != 107 {
		t.Fatalf("logs payload = %+v, service RID=%d", data, service.rid)
	}
}
