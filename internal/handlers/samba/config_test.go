// SPDX-License-Identifier: BSD-2-Clause

package sambaHandlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	"github.com/alchemillahq/sylve/internal/services/samba"
	"github.com/gin-gonic/gin"
)

func newSambaConfigRouter(smbService *samba.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.LimitRequestBody(samba.MaxRequestBodyBytes))
	r.PUT("/samba/config", SetGlobalConfig(smbService))
	return r
}

func TestSetGlobalConfigReturnsBadRequestForInvalidConfiguration(t *testing.T) {
	router := newSambaConfigRouter(&samba.Service{})
	body := []byte(`{
		"unixCharset":"",
		"workgroup":"WORKGROUP",
		"serverString":"Sylve SMB Server",
		"interfaces":"lo0",
		"bindInterfacesOnly":false,
		"appleExtensions":false,
		"extraGlobalConfig":"vfs mkdir use tmp name = no\nworkgroup = EXPERT_OVERRIDE"
	}`)

	rr := performSambaJSONRequest(t, router, http.MethodPut, "/samba/config", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Message != "failed_to_set_samba_config" {
		t.Fatalf("expected failed_to_set_samba_config, got %q", resp.Message)
	}
}

func TestSetGlobalConfigReturnsRequestEntityTooLarge(t *testing.T) {
	router := newSambaConfigRouter(&samba.Service{})
	body := []byte(`{"extraGlobalConfig":"` + strings.Repeat("x", int(samba.MaxRequestBodyBytes)) + `"}`)

	rr := performSambaJSONRequest(t, router, http.MethodPut, "/samba/config", body)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Message != "samba_request_too_large" {
		t.Fatalf("expected samba_request_too_large, got %q", resp.Message)
	}
}

func TestSetGlobalConfigRejectsUnknownFields(t *testing.T) {
	router := newSambaConfigRouter(&samba.Service{})
	body := []byte(`{
		"unixCharset":"UTF-8",
		"workgroup":"WORKGROUP",
		"serverString":"Sylve SMB Server",
		"interfaces":"lo0",
		"bindInterfacesOnly":false,
		"appleExtensions":false,
		"unexpected":true
	}`)

	rr := performSambaJSONRequest(t, router, http.MethodPut, "/samba/config", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Message != "invalid_request" {
		t.Fatalf("expected invalid_request, got %q", resp.Message)
	}
}
