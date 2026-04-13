// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package notificationsHandlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
	"github.com/alchemillahq/sylve/internal/services/notifications"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

type handlerTestSecretStore struct{}

func (handlerTestSecretStore) GetSecret(name string) (string, error)       { return "", nil }
func (handlerTestSecretStore) UpsertSecret(name string, data string) error { return nil }

func newHandlerTestService(t *testing.T) *notifications.Service {
	t.Helper()

	db := testutil.NewSQLiteTestDB(
		t,
		&models.Notification{},
		&models.NotificationSuppression{},
		&models.NotificationKindRule{},
		&models.NotificationTransportConfig{},
	)

	return notifications.NewService(db, handlerTestSecretStore{})
}

func TestNotificationsCountRequiresAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	group := r.Group("/api/notifications")
	group.Use(middleware.EnsureAuthenticated(nil))
	group.GET("/count", Count(nil))

	req := httptest.NewRequest(http.MethodGet, "/api/notifications/count", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected_401 got: %d", rec.Code)
	}
}

func TestNotificationsListHandlerReturnsItems(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := newHandlerTestService(t)
	_, err := svc.Emit(context.Background(), notifier.EventInput{
		Kind:        "system.alert",
		Title:       "Test Alert",
		Body:        "Something happened",
		Severity:    "warning",
		Fingerprint: "test-alert",
	})
	if err != nil {
		t.Fatalf("failed_to_seed_notification: %v", err)
	}

	r := gin.New()
	r.GET("/api/notifications", List(svc))

	req := httptest.NewRequest(http.MethodGet, "/api/notifications?scope=active", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected_200 got: %d", rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed_to_decode_response: %v", err)
	}

	dataAny, ok := payload["data"]
	if !ok {
		t.Fatalf("expected_data_field")
	}
	dataMap, ok := dataAny.(map[string]any)
	if !ok {
		t.Fatalf("expected_data_object")
	}
	itemsAny, ok := dataMap["items"]
	if !ok {
		t.Fatalf("expected_items_field")
	}
	items, ok := itemsAny.([]any)
	if !ok {
		t.Fatalf("expected_items_array")
	}
	if len(items) != 1 {
		t.Fatalf("expected_1_item got: %d", len(items))
	}
}
