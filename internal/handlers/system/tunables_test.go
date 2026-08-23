// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemHandlers

import (
	"errors"
	"net/http"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func restoreSetTunableOperation(t *testing.T) {
	t.Helper()
	original := setTunableOperation
	t.Cleanup(func() {
		setTunableOperation = original
	})
}

func TestSetTunableRequestValidationAndBodyLimit(t *testing.T) {
	restoreSetTunableOperation(t)

	called := false
	value := "not-called"
	setTunableOperation = func(_ *system.Service, _ string, requestedValue string) error {
		called = true
		value = requestedValue
		return nil
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.LimitRequestBody(48))
	router.PUT("/system/tunables", SetTunable(&system.Service{}))

	tests := []struct {
		name       string
		body       []byte
		wantStatus int
		wantError  string
		wantCalled bool
		wantValue  string
	}{
		{
			name:       "missing value",
			body:       []byte(`{"name":"kern.alpha"}`),
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid_tunable_request",
		},
		{
			name:       "explicit empty value",
			body:       []byte(`{"name":"kern.alpha","value":""}`),
			wantStatus: http.StatusOK,
			wantCalled: true,
			wantValue:  "",
		},
		{
			name:       "oversized body",
			body:       []byte(`{"name":"kern.alpha","value":"1","padding":"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}`),
			wantStatus: http.StatusRequestEntityTooLarge,
			wantError:  "tunable_request_too_large",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called = false
			value = "not-called"
			response := testutil.PerformJSONRequest(
				t,
				router,
				http.MethodPut,
				"/system/tunables",
				test.body,
			)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d; want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			if called != test.wantCalled {
				t.Fatalf("operation called = %t; want %t", called, test.wantCalled)
			}
			if test.wantCalled && value != test.wantValue {
				t.Fatalf("value = %q; want %q", value, test.wantValue)
			}
			if test.wantError != "" {
				body := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, response)
				if body.Error != test.wantError {
					t.Fatalf("error = %v; want %q", body.Error, test.wantError)
				}
			}
		})
	}
}

func TestSetTunableMapsStableErrorStatuses(t *testing.T) {
	restoreSetTunableOperation(t)

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"missing name", system.ErrTunableNameRequired, http.StatusBadRequest, "tunable_name_required"},
		{"read only", system.ErrTunableNotWritable, http.StatusBadRequest, "tunable_not_writable"},
		{"invalid value", system.ErrInvalidTunableValue, http.StatusBadRequest, "invalid_tunable_value"},
		{"not found", system.ErrTunableNotFound, http.StatusNotFound, "tunable_not_found"},
		{"persistence failure", system.ErrTunablePersistenceFailed, http.StatusInternalServerError, "tunable_persistence_failed"},
		{"unknown failure", errors.New("database details"), http.StatusInternalServerError, "tunable_operation_failed"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setTunableOperation = func(*system.Service, string, string) error {
				return test.err
			}

			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.PUT("/system/tunables", SetTunable(&system.Service{}))
			response := testutil.PerformJSONRequest(
				t,
				router,
				http.MethodPut,
				"/system/tunables",
				[]byte(`{"name":"kern.alpha","value":"1"}`),
			)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d; want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			body := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, response)
			if body.Error != test.wantCode {
				t.Fatalf("error = %v; want %q", body.Error, test.wantCode)
			}
		})
	}
}

func TestTunablesRemoteRejectsInvalidConfiguredOnlyWithoutLeakingParserDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/system/tunables/remote", TunablesRemote(&system.Service{}))
	response := testutil.PerformJSONRequest(
		t,
		router,
		http.MethodGet,
		"/system/tunables/remote?configuredOnly=invalid",
		nil,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	body := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, response)
	if body.Error != "invalid_configured_only_param" {
		t.Fatalf("error = %v; want invalid_configured_only_param", body.Error)
	}
}
