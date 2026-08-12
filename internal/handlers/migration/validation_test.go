// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migrationHandlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	migrationIface "github.com/alchemillahq/sylve/internal/interfaces/services/migration"
	"github.com/gin-gonic/gin"
)

func performValidateMigrationRequest(
	t *testing.T,
	target string,
	service migrationIface.MigrationServiceInterface,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/tasks/migration/validate", ValidateMigration(service))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestValidateMigrationRejectsInvalidQuery(t *testing.T) {
	tests := []struct {
		name, query, message string
	}{
		{"missing target", "?guestType=vm&guestId=1", "invalid_request"},
		{"unsupported guest type", "?guestType=template&guestId=1&targetNodeUuid=node-b", "invalid_guest_type"},
		{"zero guest ID", "?guestType=vm&guestId=0&targetNodeUuid=node-b", "invalid_guest_id"},
		{"malformed guest ID", "?guestType=jail&guestId=nope&targetNodeUuid=node-b", "invalid_guest_id"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &migrationRequestServiceStub{}
			recorder := performValidateMigrationRequest(t, "/tasks/migration/validate"+test.query, service)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}

			var response internal.APIResponse[any]
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Message != test.message {
				t.Fatalf("message = %q, want %q", response.Message, test.message)
			}
			if service.request.GuestID != 0 || service.request.GuestType != "" {
				t.Fatalf("service was called for invalid input: %+v", service.request)
			}
		})
	}
}

func TestValidateMigrationNormalizesInputAndReturnsDomainResult(t *testing.T) {
	service := &migrationRequestServiceStub{result: &migrationIface.ValidateResult{
		Allowed: false,
		Reasons: []string{"target_node_not_found"},
	}}
	recorder := performValidateMigrationRequest(
		t,
		"/tasks/migration/validate?guestType=%20VM%20&guestId=%2041%20&targetNodeUuid=%20node-b%20",
		service,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if service.request.GuestType != "vm" || service.request.GuestID != 41 || service.request.TargetNodeUUID != "node-b" {
		t.Fatalf("normalized request = %+v", service.request)
	}

	var response internal.APIResponse[migrationIface.ValidateResult]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "success" || response.Data.Allowed || len(response.Data.Reasons) != 1 {
		t.Fatalf("response = %+v", response)
	}
}

func TestValidateMigrationDoesNotExposeInternalErrors(t *testing.T) {
	service := &migrationRequestServiceStub{err: errors.New("database password appeared here")}
	recorder := performValidateMigrationRequest(
		t,
		"/tasks/migration/validate?guestType=vm&guestId=41&targetNodeUuid=node-b",
		service,
	)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}

	var response internal.APIResponse[any]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Message != "migration_validation_failed" || response.Error != "migration_validation_failed" {
		t.Fatalf("response = %+v", response)
	}
}
