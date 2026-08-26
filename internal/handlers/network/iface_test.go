// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	iface "github.com/alchemillahq/sylve/pkg/network/iface"
	"github.com/gin-gonic/gin"
)

type interfaceListHandlerResponse struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

func setupInterfaceListHandlerRouter(t *testing.T, list func() ([]*iface.Interface, error)) *gin.Engine {
	t.Helper()
	original := listInterfaces
	listInterfaces = list
	t.Cleanup(func() {
		listInterfaces = original
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/network/interface", ListInterfaces())
	return router
}

func decodeInterfaceListHandlerResponse(t *testing.T, rr *httptest.ResponseRecorder) interfaceListHandlerResponse {
	t.Helper()
	var response interfaceListHandlerResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response body %q: %v", rr.Body.String(), err)
	}
	return response
}

func TestListInterfacesReturnsConcreteArrayResponse(t *testing.T) {
	router := setupInterfaceListHandlerRouter(t, func() ([]*iface.Interface, error) {
		return []*iface.Interface{{Name: "bridge0", MTU: 1500}}, nil
	})

	rr := performNetworkJSONRequest(t, router, http.MethodGet, "/network/interface", nil)
	response := decodeInterfaceListHandlerResponse(t, rr)
	if rr.Code != http.StatusOK || response.Status != "success" || response.Message != "interfaces_list" {
		t.Fatalf("unexpected list response: status=%d response=%+v", rr.Code, response)
	}

	var interfaces []*iface.Interface
	if err := json.Unmarshal(response.Data, &interfaces); err != nil {
		t.Fatalf("decode interface data: %v", err)
	}
	if len(interfaces) != 1 || interfaces[0].Name != "bridge0" {
		t.Fatalf("unexpected interface data: %+v", interfaces)
	}
}

func TestListInterfacesNormalizesNilResultToEmptyArray(t *testing.T) {
	router := setupInterfaceListHandlerRouter(t, func() ([]*iface.Interface, error) {
		return nil, nil
	})

	rr := performNetworkJSONRequest(t, router, http.MethodGet, "/network/interface", nil)
	response := decodeInterfaceListHandlerResponse(t, rr)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if string(response.Data) != "[]" {
		t.Fatalf("expected an empty JSON array, got %s", response.Data)
	}
}

func TestListInterfacesReturnsStableRuntimeError(t *testing.T) {
	const sensitiveDetail = "getifaddrs sensitive runtime detail"
	router := setupInterfaceListHandlerRouter(t, func() ([]*iface.Interface, error) {
		return nil, errors.New(sensitiveDetail)
	})

	rr := performNetworkJSONRequest(t, router, http.MethodGet, "/network/interface", nil)
	response := decodeInterfaceListHandlerResponse(t, rr)
	if rr.Code != http.StatusInternalServerError || response.Message != "failed_to_list_interfaces" || response.Error != "interface_list_failed" {
		t.Fatalf("expected stable 500 response, status=%d response=%+v", rr.Code, response)
	}
	if strings.Contains(rr.Body.String(), sensitiveDetail) {
		t.Fatalf("response leaked runtime detail: %s", rr.Body.String())
	}
}
