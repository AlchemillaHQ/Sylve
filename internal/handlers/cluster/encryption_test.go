// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"encoding/json"
	"net/http"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

func newEncryptionRouter(cS *cluster.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/cluster/encryption/discover", DiscoverEncryptionKeyInternal(cS))
	return r
}

func TestDiscoverEncryptionKeyInternal(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.EncryptionKey{})
	cS := &cluster.Service{DB: db}
	r := newEncryptionRouter(cS)

	rr := performJSONRequest(t, r, http.MethodPost, "/cluster/encryption/discover",
		[]byte(`{}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty payload, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Message != "invalid_request" {
		t.Fatalf("expected invalid_request, got %q", resp.Message)
	}

	rr = performJSONRequest(t, r, http.MethodPost, "/cluster/encryption/discover",
		[]byte(`{"uuid":"   ","keyData":"some-data"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for whitespace-only UUID, got %d: %s", rr.Code, rr.Body.String())
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Message != "encryption_key_uuid_required" {
		t.Fatalf("expected encryption_key_uuid_required, got %q", resp.Message)
	}

	rr = performJSONRequest(t, r, http.MethodPost, "/cluster/encryption/discover",
		[]byte(`{"uuid":"test-key","keyData":"test-key-data-with-enough-bytes-for-validation"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid key, got %d: %s", rr.Code, rr.Body.String())
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Message != "encryption_key_discovered" {
		t.Fatalf("expected encryption_key_discovered, got %q", resp.Message)
	}

	var count int64
	db.Model(&clusterModels.EncryptionKey{}).Where("uuid = ?", "test-key").Count(&count)
	if count != 1 {
		t.Fatalf("expected key persisted, found %d", count)
	}
}
