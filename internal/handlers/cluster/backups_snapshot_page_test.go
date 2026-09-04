// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package clusterHandlers

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/gin-gonic/gin"
)

func snapshotPageRequestForURL(t *testing.T, target string) (zelta.SnapshotPageRequest, bool, int) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = request
	page, ok := backupSnapshotPageRequest(context)
	return page, ok, recorder.Code
}

func TestBackupSnapshotPageRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	request, ok, _ := snapshotPageRequestForURL(t, "/snapshots")
	if !ok || request.Limit != zelta.DefaultSnapshotPageLimit || request.Cursor != "" {
		t.Fatalf("default request: %+v ok=%v", request, ok)
	}

	cursor := base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"c":"2026-01-01T00:00:00Z","n":"backup/root@selected"}`))
	request, ok, _ = snapshotPageRequestForURL(t, "/snapshots?limit=500&cursor="+cursor)
	if !ok || request.Limit != zelta.MaxSnapshotPageLimit || request.Cursor != cursor {
		t.Fatalf("explicit request: %+v ok=%v", request, ok)
	}
}

func TestBackupSnapshotPageRequestRejectsInvalidQueries(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, target := range []string{
		"/snapshots?limit=0",
		"/snapshots?limit=501",
		"/snapshots?limit=nope",
		"/snapshots?limit=10&limit=20",
		"/snapshots?cursor=invalid",
		"/snapshots?cursor=one&cursor=two",
	} {
		_, ok, status := snapshotPageRequestForURL(t, target)
		if ok || status != http.StatusBadRequest {
			t.Fatalf("query %q: ok=%v status=%d", target, ok, status)
		}
	}
}
