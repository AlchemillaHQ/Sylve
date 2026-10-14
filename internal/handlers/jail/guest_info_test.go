// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
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
	"testing"

	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type jailGuestInfoHandlerStub struct {
	info   jailServiceInterfaces.GuestInfo
	err    error
	ctID   uint
	ctx    context.Context
	called bool
}

func (s *jailGuestInfoHandlerStub) GetJailGuestInfo(ctx context.Context, ctID uint) (jailServiceInterfaces.GuestInfo, error) {
	s.called, s.ctx, s.ctID = true, ctx, ctID
	return s.info, s.err
}

func TestGetJailGuestInfoHandlerContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, available := range []bool{false, true} {
		stub := &jailGuestInfoHandlerStub{info: jailServiceInterfaces.GuestInfo{
			Available: available, Interfaces: []jailServiceInterfaces.GuestNetworkInterface{},
		}}
		if available {
			stub.info.OSInfo = &jailServiceInterfaces.GuestOSInfo{Name: "FreeBSD", Version: "14.3-RELEASE"}
			stub.info.NetworkMode = "vnet"
		}
		router := gin.New()
		router.GET("/jail/:ctid/guest-info", GetJailGuestInfo(stub))
		request := httptest.NewRequest(http.MethodGet, "/jail/901/guest-info", nil)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || stub.ctID != 901 || stub.ctx != request.Context() {
			t.Fatalf("request: status=%d CTID=%d context=%v body=%s", recorder.Code, stub.ctID, stub.ctx, recorder.Body.String())
		}
		response := decodeJailCoreResponse(t, recorder)
		var info jailServiceInterfaces.GuestInfo
		if err := json.Unmarshal(response.Data, &info); err != nil {
			t.Fatal(err)
		}
		if response.Status != "success" || response.Message != "jail_guest_info_retrieved" || info.Available != available {
			t.Fatalf("response = %+v, info=%+v", response, info)
		}
	}
}

func TestGetJailGuestInfoHandlerValidationAndErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		ctID    string
		err     error
		status  int
		message string
		called  bool
	}{
		{ctID: "0", status: 400, message: "invalid_ctid"},
		{ctID: "-1", status: 400, message: "invalid_ctid"},
		{ctID: "1.5", status: 400, message: "invalid_ctid"},
		{ctID: "4294967296", status: 400, message: "invalid_ctid"},
		{ctID: "901", err: errors.Join(errors.New("lookup failed"), gorm.ErrRecordNotFound), status: 404, message: "jail_not_found", called: true},
		{ctID: "901", err: errors.New("database unavailable"), status: 500, message: "failed_to_get_jail_guest_info", called: true},
	} {
		t.Run(test.ctID+test.message, func(t *testing.T) {
			stub := &jailGuestInfoHandlerStub{err: test.err}
			router := gin.New()
			router.GET("/jail/:ctid/guest-info", GetJailGuestInfo(stub))
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/jail/"+test.ctID+"/guest-info", nil))
			response := decodeJailCoreResponse(t, recorder)
			if recorder.Code != test.status || response.Message != test.message || stub.called != test.called {
				t.Fatalf("status=%d response=%+v called=%v", recorder.Code, response, stub.called)
			}
		})
	}
}
