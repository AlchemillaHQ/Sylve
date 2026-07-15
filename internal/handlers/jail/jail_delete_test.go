// SPDX-License-Identifier: BSD-2-Clause

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
)

type jailDeletionHandlerStub struct {
	result jailServiceInterfaces.DeleteJailResult
	err    error
}

func (s *jailDeletionHandlerStub) CanMutateProtectedJail(uint) (bool, error) {
	return true, nil
}

func (s *jailDeletionHandlerStub) DeleteJailWithWarnings(
	context.Context,
	uint,
	bool,
	bool,
) (jailServiceInterfaces.DeleteJailResult, error) {
	return s.result, s.err
}

func performJailDeleteRequest(t *testing.T, service jailDeletionService) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.DELETE("/jail/:ctid", DeleteJail(service))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodDelete,
		"/jail/100?deletemacs=false&deleterootfs=false",
		nil,
	)
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestDeleteJailHandlerMapsRevalidatedPolicyConflict(t *testing.T) {
	recorder := performJailDeleteRequest(t, &jailDeletionHandlerStub{
		err: errors.New("guest_delete_requires_replication_policy_removed"),
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "error" || response.Message != "guest_delete_requires_replication_policy_removed" {
		t.Fatalf("response = %+v", response)
	}
}
