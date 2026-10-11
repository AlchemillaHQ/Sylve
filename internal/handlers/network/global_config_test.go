// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
)

type globalConfigHandlerService struct {
	state    *networkServiceInterfaces.GlobalConfigState
	err      error
	requests []networkServiceInterfaces.UpdateGlobalConfigRequest
}

func (s *globalConfigHandlerService) GetGlobalConfig(context.Context) (*networkServiceInterfaces.GlobalConfigState, error) {
	return s.state, s.err
}
func (s *globalConfigHandlerService) UpdateGlobalConfig(_ context.Context, request networkServiceInterfaces.UpdateGlobalConfigRequest) error {
	s.requests = append(s.requests, request)
	return s.err
}

func globalConfigHandlerRouter(service *globalConfigHandlerService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.LimitRequestBody(1024))
	router.GET("/network/global-config", GetNetworkGlobalConfig(service))
	router.PUT("/network/global-config", UpdateNetworkGlobalConfig(service))
	return router
}

func TestGlobalConfigHandlerGet(t *testing.T) {
	service := &globalConfigHandlerService{state: &networkServiceInterfaces.GlobalConfigState{
		GlobalConfig: networkModels.GlobalConfig{DNSMode: "os", DNSServers: []string{}, SearchDomains: []string{}, HostAliases: []networkModels.HostAlias{}},
		Hostname:     "zeus", EffectiveDNSServers: []string{"192.0.2.1"}, EffectiveSearchDomains: []string{"lan"}, Warnings: []string{},
	}}
	recorder := performNetworkJSONRequest(t, globalConfigHandlerRouter(service), http.MethodGet, "/network/global-config", nil)
	response := decodeDHCPConfigHandlerResponse(t, recorder)
	if recorder.Code != http.StatusOK || response.Status != "success" {
		t.Fatalf("GET = %d %s", recorder.Code, recorder.Body.String())
	}
	var state networkServiceInterfaces.GlobalConfigState
	if err := json.Unmarshal(response.Data, &state); err != nil || state.Hostname != "zeus" || state.DNSMode != "os" {
		t.Fatalf("state = %+v %v", state, err)
	}
}

func TestGlobalConfigHandlerUpdate(t *testing.T) {
	service := &globalConfigHandlerService{}
	body := []byte(`{"expectedRevision":0,"dnsMode":"manual","dnsServers":["192.0.2.53"],"searchDomainsOverride":true,"searchDomains":[],"hostAliases":[]}`)
	recorder := performNetworkJSONRequest(t, globalConfigHandlerRouter(service), http.MethodPut, "/network/global-config", body)
	if recorder.Code != http.StatusOK || len(service.requests) != 1 {
		t.Fatalf("PUT = %d %s", recorder.Code, recorder.Body.String())
	}
	request := service.requests[0]
	if request.ExpectedRevision == nil || *request.ExpectedRevision != 0 || !request.SearchDomainsOverride || len(request.DNSServers) != 1 {
		t.Fatalf("request = %+v", request)
	}
}

func TestGlobalConfigHandlerErrorMapping(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"validation", &network.GlobalConfigError{Code: "invalid_global_dns_server", Cause: network.ErrInvalidGlobalConfig}, 400, "invalid_global_dns_server"},
		{"conflict", &network.GlobalConfigError{Code: "global_config_revision_conflict", Cause: network.ErrGlobalConfigConflict}, 409, "global_config_revision_conflict"},
		{"internal", errors.New("secret database or host details"), 500, "network_global_config_update_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &globalConfigHandlerService{err: test.err}
			recorder := performNetworkJSONRequest(t, globalConfigHandlerRouter(service), http.MethodPut, "/network/global-config", []byte(`{"expectedRevision":0,"dnsMode":"os"}`))
			response := decodeDHCPConfigHandlerResponse(t, recorder)
			if recorder.Code != test.status || response.Error != test.code {
				t.Fatalf("error = %d %s", recorder.Code, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "secret") {
				t.Fatal("internal details leaked")
			}
		})
	}
}

func TestGlobalConfigHandlerRejectsMalformedAndLargeRequests(t *testing.T) {
	for _, test := range []struct {
		body   string
		status int
	}{
		{`{"expectedRevision":"bad"}`, 400},
		{`{"dnsServers":"192.0.2.53"}`, 400},
		{`{`, 400},
		{`{"dnsMode":"` + strings.Repeat("x", 1024) + `"}`, 413},
	} {
		service := &globalConfigHandlerService{}
		recorder := performNetworkJSONRequest(t, globalConfigHandlerRouter(service), http.MethodPut, "/network/global-config", []byte(test.body))
		if recorder.Code != test.status || len(service.requests) != 0 {
			t.Fatalf("bind error = %d %s", recorder.Code, recorder.Body.String())
		}
	}
}

func TestGlobalConfigHandlerGetFailureDoesNotLeakDetails(t *testing.T) {
	service := &globalConfigHandlerService{err: errors.New("secret filesystem failure")}
	recorder := performNetworkJSONRequest(t, globalConfigHandlerRouter(service), http.MethodGet, "/network/global-config", nil)
	if recorder.Code != 500 || strings.Contains(recorder.Body.String(), "secret") {
		t.Fatalf("failed GET = %d %s", recorder.Code, recorder.Body.String())
	}
}
