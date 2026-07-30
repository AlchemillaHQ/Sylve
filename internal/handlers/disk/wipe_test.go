// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package diskHandlers

import (
	"errors"
	"net/http"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/testutil"

	"github.com/gin-gonic/gin"
)

type diskWipeHandlerStub struct {
	calls  int
	device string
	err    error
}

func (s *diskWipeHandlerStub) DestroyPartitionTable(device string) error {
	s.calls++
	s.device = device
	return s.err
}

func newDiskWipeRouter(service diskWipeService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/disk/wipe", WipeDisk(service, nil))
	return router
}

func TestWipeDiskDelegatesToService(t *testing.T) {
	service := &diskWipeHandlerStub{}
	router := newDiskWipeRouter(service)

	response := testutil.PerformJSONRequest(
		t,
		router,
		http.MethodPost,
		"/disk/wipe",
		[]byte(`{"device":"/dev/nda0"}`),
	)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if service.calls != 1 {
		t.Fatalf("service calls = %d; want 1", service.calls)
	}
	if service.device != "/dev/nda0" {
		t.Fatalf("service device = %q; want /dev/nda0", service.device)
	}

	body := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, response)
	if body.Status != "success" || body.Message != "disk_wiped" || body.Error != "" {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestWipeDiskReturnsServiceError(t *testing.T) {
	wipeErr := errors.New("Device busy")
	service := &diskWipeHandlerStub{err: wipeErr}
	router := newDiskWipeRouter(service)

	response := testutil.PerformJSONRequest(
		t,
		router,
		http.MethodPost,
		"/disk/wipe",
		[]byte(`{"device":"/dev/nda0"}`),
	)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf(
			"status = %d; want %d; body = %s",
			response.Code,
			http.StatusInternalServerError,
			response.Body.String(),
		)
	}
	if service.calls != 1 || service.device != "/dev/nda0" {
		t.Fatalf("service calls = %d, device = %q; want 1 call for /dev/nda0", service.calls, service.device)
	}

	body := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, response)
	if body.Status != "error" || body.Message != "error_wiping_disk" || body.Error != wipeErr.Error() {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestWipeDiskRejectsInvalidPayloadWithoutCallingService(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing device", body: `{}`},
		{name: "device too short", body: `{"device":"x"}`},
		{name: "malformed JSON", body: `{"device":`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &diskWipeHandlerStub{}
			router := newDiskWipeRouter(service)

			response := testutil.PerformJSONRequest(
				t,
				router,
				http.MethodPost,
				"/disk/wipe",
				[]byte(tt.body),
			)

			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"status = %d; want %d; body = %s",
					response.Code,
					http.StatusBadRequest,
					response.Body.String(),
				)
			}
			if service.calls != 0 {
				t.Fatalf("service calls = %d; want 0", service.calls)
			}

			body := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, response)
			if body.Status != "error" || body.Message != "invalid_request_payload" || body.Error != "validation_error" {
				t.Fatalf("unexpected response: %+v", body)
			}
		})
	}
}
