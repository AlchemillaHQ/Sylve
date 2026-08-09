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
	"strings"
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type vmCoreHandlerStub struct {
	vm        vmModels.VM
	vmErr     error
	simple    libvirtServiceInterfaces.SimpleList
	simpleErr error

	createReq    libvirtServiceInterfaces.CreateVMRequest
	createCalled bool
	createErr    error

	descriptionRID    uint
	description       string
	descriptionCalled bool
	descriptionErr    error

	nameRID    uint
	name       string
	nameCalled bool
	nameErr    error

	lookupRID uint
}

func (s *vmCoreHandlerStub) GetVMByRID(rid uint) (vmModels.VM, error) {
	s.lookupRID = rid
	return s.vm, s.vmErr
}

func (s *vmCoreHandlerStub) GetSimpleVMByRID(rid uint) (libvirtServiceInterfaces.SimpleList, error) {
	s.lookupRID = rid
	return s.simple, s.simpleErr
}

func (s *vmCoreHandlerStub) CreateVM(req libvirtServiceInterfaces.CreateVMRequest, _ context.Context) error {
	s.createReq = req
	s.createCalled = true
	return s.createErr
}

func (s *vmCoreHandlerStub) UpdateDescription(rid uint, description string) error {
	s.descriptionRID = rid
	s.description = description
	s.descriptionCalled = true
	return s.descriptionErr
}

func (s *vmCoreHandlerStub) UpdateName(rid uint, name string) error {
	s.nameRID = rid
	s.name = name
	s.nameCalled = true
	return s.nameErr
}

type vmCoreResponse struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

func decodeVMCoreResponse(t *testing.T, recorder *httptest.ResponseRecorder) vmCoreResponse {
	t.Helper()
	var response vmCoreResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, recorder.Body.String())
	}
	return response
}

func TestGetVMByRIDHandlerUsesRIDPathIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &vmCoreHandlerStub{vm: vmModels.VM{ID: 12, RID: 901, Name: "rid-vm"}}
	router := gin.New()
	router.GET("/vm/:rid", GetVMByRID(stub))

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/vm/901", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.lookupRID != 901 {
		t.Fatalf("lookup RID = %d, want 901", stub.lookupRID)
	}

	response := decodeVMCoreResponse(t, recorder)
	if response.Status != "success" || response.Message != "vm_retrieved" {
		t.Fatalf("response = %+v", response)
	}
	var data struct {
		ID  uint `json:"id"`
		RID uint `json:"rid"`
	}
	if err := json.Unmarshal(response.Data, &data); err != nil {
		t.Fatalf("decode VM data: %v", err)
	}
	if data.ID != 12 || data.RID != 901 {
		t.Fatalf("VM identity = %+v, want ID 12 and RID 901", data)
	}
}

func TestGetVMByRIDHandlerValidatesAndMapsMissingRID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("invalid RID", func(t *testing.T) {
		stub := &vmCoreHandlerStub{}
		router := gin.New()
		router.GET("/vm/:rid", GetVMByRID(stub))

		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/vm/0", nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
		}
		if stub.lookupRID != 0 {
			t.Fatalf("service called with RID %d", stub.lookupRID)
		}
		if response := decodeVMCoreResponse(t, recorder); response.Message != "invalid_vm_rid" {
			t.Fatalf("message = %q, want invalid_vm_rid", response.Message)
		}
	})

	t.Run("missing VM", func(t *testing.T) {
		stub := &vmCoreHandlerStub{vmErr: gorm.ErrRecordNotFound}
		router := gin.New()
		router.GET("/vm/:rid", GetVMByRID(stub))

		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/vm/902", nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
		}
		if response := decodeVMCoreResponse(t, recorder); response.Message != "vm_not_found" {
			t.Fatalf("message = %q, want vm_not_found", response.Message)
		}
	})
}

func TestGetSimpleVMByRIDHandlerContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &vmCoreHandlerStub{simple: libvirtServiceInterfaces.SimpleList{
		ID:         13,
		RID:        903,
		Name:       "simple-vm",
		CPUPinning: []vmModels.VMCPUPinning{},
	}}
	router := gin.New()
	router.GET("/vm/simple/:rid", GetSimpleVMByRID(stub))

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/vm/simple/903", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.lookupRID != 903 {
		t.Fatalf("lookup RID = %d, want 903", stub.lookupRID)
	}

	response := decodeVMCoreResponse(t, recorder)
	var data struct {
		RID        uint            `json:"rid"`
		CPUPinning json.RawMessage `json:"cpuPinning"`
	}
	if err := json.Unmarshal(response.Data, &data); err != nil {
		t.Fatalf("decode simple VM data: %v", err)
	}
	if data.RID != 903 || string(data.CPUPinning) != "[]" {
		t.Fatalf("simple VM data = %s", response.Data)
	}
}

func TestCreateVMHandlerReturnsCreatedIdentityAndLocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &vmCoreHandlerStub{}
	router := gin.New()
	router.POST("/vm", CreateVM(stub))

	body := `{
		"name":"created-vm",
		"node":"remote-node",
		"rid":904,
		"cpuSockets":1,
		"cpuCores":1,
		"cpuThreads":1,
		"ram":536870912,
		"startOrder":7,
		"timeOffset":"utc"
	}`
	request := httptest.NewRequest(http.MethodPost, "/vm", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Current-Hostname", "remote-node")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Location") != "/api/vm/904" {
		t.Fatalf("Location = %q, want /api/vm/904", recorder.Header().Get("Location"))
	}
	if !stub.createCalled || stub.createReq.RID == nil || *stub.createReq.RID != 904 {
		t.Fatalf("create request RID = %v, called=%t", stub.createReq.RID, stub.createCalled)
	}
	if stub.createReq.Name != "created-vm" || stub.createReq.StartOrder != 7 {
		t.Fatalf("create request = %+v", stub.createReq)
	}

	response := decodeVMCoreResponse(t, recorder)
	var data VMCreateResponse
	if err := json.Unmarshal(response.Data, &data); err != nil {
		t.Fatalf("decode created identity: %v", err)
	}
	if response.Message != "vm_created" || data.RID != 904 || data.Name != "created-vm" {
		t.Fatalf("response = %+v, data=%+v", response, data)
	}
}

func TestCreateVMHandlerRejectsInvalidBodyBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &vmCoreHandlerStub{}
	router := gin.New()
	router.POST("/vm", CreateVM(stub))

	request := httptest.NewRequest(http.MethodPost, "/vm", strings.NewReader(`{"name":"missing-rid"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest || stub.createCalled {
		t.Fatalf("invalid create: status=%d called=%t body=%s", recorder.Code, stub.createCalled, recorder.Body.String())
	}
}

func TestVMUpdateHandlersUsePathRIDAndPreserveEmptyDescription(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("empty description", func(t *testing.T) {
		stub := &vmCoreHandlerStub{}
		router := gin.New()
		router.PATCH("/vm/:rid/description", UpdateVMDescription(stub))

		request := httptest.NewRequest(http.MethodPatch, "/vm/905/description", strings.NewReader(`{"description":""}`))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK || !stub.descriptionCalled {
			t.Fatalf("description update: status=%d called=%t body=%s", recorder.Code, stub.descriptionCalled, recorder.Body.String())
		}
		if stub.descriptionRID != 905 || stub.description != "" {
			t.Fatalf("description call = rid:%d value:%q", stub.descriptionRID, stub.description)
		}
	})

	t.Run("duplicate name", func(t *testing.T) {
		stub := &vmCoreHandlerStub{nameErr: errors.New("vm_name_already_in_use")}
		router := gin.New()
		router.PATCH("/vm/:rid/name", UpdateVMName(stub, nil))

		request := httptest.NewRequest(http.MethodPatch, "/vm/906/name", strings.NewReader(`{"name":"duplicate"}`))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusConflict || !stub.nameCalled {
			t.Fatalf("name update: status=%d called=%t body=%s", recorder.Code, stub.nameCalled, recorder.Body.String())
		}
		if stub.nameRID != 906 || stub.name != "duplicate" {
			t.Fatalf("name call = rid:%d value:%q", stub.nameRID, stub.name)
		}
		if response := decodeVMCoreResponse(t, recorder); response.Message != "vm_name_already_in_use" {
			t.Fatalf("message = %q, want vm_name_already_in_use", response.Message)
		}
	})
}
