// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type jailCoreHandlerStub struct {
	jails      []jailModels.Jail
	jailsErr   error
	jail       *jailModels.Jail
	jailErr    error
	simple     jailServiceInterfaces.SimpleList
	simpleErr  error
	lookupCTID uint

	createReq    jailServiceInterfaces.CreateJailRequest
	createCalled bool
	createErr    error

	descriptionCTID   uint
	description       string
	descriptionCalled bool
	descriptionErr    error

	nameCTID   uint
	name       string
	nameCalled bool
	nameErr    error

	deleteCTID   uint
	deleteMacs   bool
	deleteRootFS bool
	deleteCalled bool
	deleteResult jailServiceInterfaces.DeleteJailResult
	deleteErr    error
	mutateErr    error
	mutateDenied bool
}

func (s *jailCoreHandlerStub) GetJails() ([]jailModels.Jail, error) {
	return s.jails, s.jailsErr
}

func (s *jailCoreHandlerStub) GetJailByCTID(ctID uint) (*jailModels.Jail, error) {
	s.lookupCTID = ctID
	return s.jail, s.jailErr
}

func (s *jailCoreHandlerStub) GetJailsSimple() ([]jailServiceInterfaces.SimpleList, error) {
	return nil, nil
}

func (s *jailCoreHandlerStub) GetSimpleJailByCTID(ctID uint) (jailServiceInterfaces.SimpleList, error) {
	s.lookupCTID = ctID
	return s.simple, s.simpleErr
}

func (s *jailCoreHandlerStub) CreateJail(
	_ context.Context,
	req jailServiceInterfaces.CreateJailRequest,
) error {
	s.createReq = req
	s.createCalled = true
	return s.createErr
}

func (s *jailCoreHandlerStub) UpdateDescription(ctID uint, description string) error {
	s.descriptionCTID = ctID
	s.description = description
	s.descriptionCalled = true
	return s.descriptionErr
}

func (s *jailCoreHandlerStub) UpdateName(ctID uint, name string) error {
	s.nameCTID = ctID
	s.name = name
	s.nameCalled = true
	return s.nameErr
}

func (s *jailCoreHandlerStub) CanMutateProtectedJail(uint) (bool, error) {
	return !s.mutateDenied, s.mutateErr
}

func (s *jailCoreHandlerStub) DeleteJailWithWarnings(
	_ context.Context,
	ctID uint,
	deleteMacs bool,
	deleteRootFS bool,
) (jailServiceInterfaces.DeleteJailResult, error) {
	s.deleteCTID = ctID
	s.deleteMacs = deleteMacs
	s.deleteRootFS = deleteRootFS
	s.deleteCalled = true
	return s.deleteResult, s.deleteErr
}

type jailCoreResponse struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

func decodeJailCoreResponse(t *testing.T, recorder *httptest.ResponseRecorder) jailCoreResponse {
	t.Helper()
	var response jailCoreResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, recorder.Body.String())
	}
	return response
}

func TestGetJailByCTIDHandlerUsesPathIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &jailCoreHandlerStub{jail: &jailModels.Jail{ID: 12, CTID: 901, Name: "ctid-jail"}}
	router := gin.New()
	router.GET("/jail/:ctid", GetJailByCTID(stub))

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/jail/901", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.lookupCTID != 901 {
		t.Fatalf("lookup CTID = %d, want 901", stub.lookupCTID)
	}

	response := decodeJailCoreResponse(t, recorder)
	var data struct {
		ID   uint `json:"id"`
		CTID uint `json:"ctId"`
	}
	if err := json.Unmarshal(response.Data, &data); err != nil {
		t.Fatalf("decode jail data: %v", err)
	}
	if response.Message != "jail_retrieved" || data.ID != 12 || data.CTID != 901 {
		t.Fatalf("response = %+v, data=%+v", response, data)
	}
}

func TestGetJailByCTIDHandlerValidatesAndMapsMissingJail(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("invalid CTID", func(t *testing.T) {
		stub := &jailCoreHandlerStub{}
		router := gin.New()
		router.GET("/jail/:ctid", GetJailByCTID(stub))

		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/jail/0", nil))
		if recorder.Code != http.StatusBadRequest || stub.lookupCTID != 0 {
			t.Fatalf("invalid lookup: status=%d ctid=%d body=%s", recorder.Code, stub.lookupCTID, recorder.Body.String())
		}
		if response := decodeJailCoreResponse(t, recorder); response.Message != "invalid_ctid" {
			t.Fatalf("message = %q, want invalid_ctid", response.Message)
		}
	})

	t.Run("missing jail", func(t *testing.T) {
		stub := &jailCoreHandlerStub{jailErr: errors.Join(errors.New("lookup failed"), gorm.ErrRecordNotFound)}
		router := gin.New()
		router.GET("/jail/:ctid", GetJailByCTID(stub))

		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/jail/902", nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
		}
		if response := decodeJailCoreResponse(t, recorder); response.Message != "jail_not_found" {
			t.Fatalf("message = %q, want jail_not_found", response.Message)
		}
	})
}

func TestGetSimpleJailByCTIDHandlerContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &jailCoreHandlerStub{simple: jailServiceInterfaces.SimpleList{
		ID: 13, CTID: 903, Name: "simple-jail", State: "UNKNOWN",
	}}
	router := gin.New()
	router.GET("/jail/simple/:ctid", GetSimpleJailByCTID(stub))

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/jail/simple/903", nil))
	if recorder.Code != http.StatusOK || stub.lookupCTID != 903 {
		t.Fatalf("simple lookup: status=%d ctid=%d body=%s", recorder.Code, stub.lookupCTID, recorder.Body.String())
	}

	response := decodeJailCoreResponse(t, recorder)
	var data jailServiceInterfaces.SimpleList
	if err := json.Unmarshal(response.Data, &data); err != nil {
		t.Fatalf("decode simple jail: %v", err)
	}
	if data.CTID != 903 || data.State != "UNKNOWN" {
		t.Fatalf("simple jail = %+v", data)
	}
}

func TestCreateJailHandlerReturnsCreatedIdentityAndLocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &jailCoreHandlerStub{}
	router := gin.New()
	router.POST("/jail", CreateJail(stub))

	request := httptest.NewRequest(
		http.MethodPost,
		"/jail",
		strings.NewReader(`{"name":"created-jail","ctId":904,"pool":"zroot","type":"freebsd"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Location") != "/api/jail/904" {
		t.Fatalf("Location = %q, want /api/jail/904", recorder.Header().Get("Location"))
	}
	if !stub.createCalled || stub.createReq.CTID == nil || *stub.createReq.CTID != 904 {
		t.Fatalf("create request CTID = %v, called=%t", stub.createReq.CTID, stub.createCalled)
	}

	response := decodeJailCoreResponse(t, recorder)
	var data JailCreateResponse
	if err := json.Unmarshal(response.Data, &data); err != nil {
		t.Fatalf("decode created identity: %v", err)
	}
	if response.Message != "jail_created" || data.CTID != 904 || data.Name != "created-jail" {
		t.Fatalf("response = %+v, data=%+v", response, data)
	}
}

func TestJailUpdateHandlersUsePathCTIDAndPreserveEmptyDescription(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("empty description", func(t *testing.T) {
		stub := &jailCoreHandlerStub{}
		router := gin.New()
		router.PATCH("/jail/:ctid/description", UpdateJailDescription(stub))

		request := httptest.NewRequest(http.MethodPatch, "/jail/905/description", strings.NewReader(`{"description":""}`))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK || !stub.descriptionCalled {
			t.Fatalf("description update: status=%d called=%t body=%s", recorder.Code, stub.descriptionCalled, recorder.Body.String())
		}
		if stub.descriptionCTID != 905 || stub.description != "" {
			t.Fatalf("description call = ctid:%d value:%q", stub.descriptionCTID, stub.description)
		}
	})

	t.Run("duplicate name", func(t *testing.T) {
		stub := &jailCoreHandlerStub{nameErr: errors.New("jail_name_already_in_use")}
		router := gin.New()
		router.PATCH("/jail/:ctid/name", UpdateJailName(stub, nil))

		request := httptest.NewRequest(http.MethodPatch, "/jail/906/name", strings.NewReader(`{"name":"duplicate"}`))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusConflict || !stub.nameCalled {
			t.Fatalf("name update: status=%d called=%t body=%s", recorder.Code, stub.nameCalled, recorder.Body.String())
		}
		if stub.nameCTID != 906 || stub.name != "duplicate" {
			t.Fatalf("name call = ctid:%d value:%q", stub.nameCTID, stub.name)
		}
	})
}

func TestDeleteJailHandlerRequiresFlagsAndForwardsFalseValues(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("forwards explicit false flags", func(t *testing.T) {
		stub := &jailCoreHandlerStub{deleteResult: jailServiceInterfaces.DeleteJailResult{
			Warnings:         []string{"root dataset retained"},
			RetainedDatasets: []string{"zroot/sylve/jails/907"},
		}}
		router := gin.New()
		router.DELETE("/jail/:ctid", DeleteJail(stub))

		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(
			http.MethodDelete,
			"/jail/907?deletemacs=false&deleterootfs=false",
			nil,
		))
		if recorder.Code != http.StatusOK || !stub.deleteCalled {
			t.Fatalf("delete: status=%d called=%t body=%s", recorder.Code, stub.deleteCalled, recorder.Body.String())
		}
		if stub.deleteCTID != 907 || stub.deleteMacs || stub.deleteRootFS {
			t.Fatalf("delete call = ctid:%d macs:%t rootfs:%t", stub.deleteCTID, stub.deleteMacs, stub.deleteRootFS)
		}
		if response := decodeJailCoreResponse(t, recorder); response.Message != "jail_deleted_with_warnings" {
			t.Fatalf("message = %q, want jail_deleted_with_warnings", response.Message)
		}
	})

	t.Run("rejects missing flag", func(t *testing.T) {
		stub := &jailCoreHandlerStub{}
		router := gin.New()
		router.DELETE("/jail/:ctid", DeleteJail(stub))

		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(
			http.MethodDelete,
			"/jail/908?deletemacs=false",
			nil,
		))
		if recorder.Code != http.StatusBadRequest || stub.deleteCalled {
			t.Fatalf("missing flag: status=%d called=%t body=%s", recorder.Code, stub.deleteCalled, recorder.Body.String())
		}
		if response := decodeJailCoreResponse(t, recorder); response.Message != "missing_deleterootfs_param" {
			t.Fatalf("message = %q, want missing_deleterootfs_param", response.Message)
		}
	})
}
