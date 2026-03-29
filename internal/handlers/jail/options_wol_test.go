// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

func requireSystemUUIDOrSkip(t *testing.T) {
	t.Helper()
	if _, err := utils.GetSystemUUID(); err != nil {
		t.Skipf("system uuid unavailable in test environment: %v", err)
	}
}

func TestModifyWakeOnLan_Success(t *testing.T) {
	requireSystemUUIDOrSkip(t)
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Storage{},
		&jailModels.JailHooks{},
		&jailModels.JailSnapshot{},
		&jailModels.Network{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
	)

	jailRecord := jailModels.Jail{
		CTID: 101,
		Name: "jail-wol-test",
		Type: jailModels.JailTypeFreeBSD,
	}
	if err := db.Create(&jailRecord).Error; err != nil {
		t.Fatalf("failed to seed jail: %v", err)
	}

	jailService := &jail.Service{DB: db}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/options/wol/:rid", ModifyWakeOnLan(jailService))

	req := httptest.NewRequest(http.MethodPut, "/options/wol/101", bytes.NewBufferString(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d, body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}

	if !strings.Contains(rr.Body.String(), `"wol_modified"`) {
		t.Fatalf("response body missing wol_modified: %s", rr.Body.String())
	}

	var refreshed jailModels.Jail
	if err := db.Where("ct_id = ?", 101).First(&refreshed).Error; err != nil {
		t.Fatalf("failed to reload jail: %v", err)
	}
	if !refreshed.WoL {
		t.Fatalf("expected jail wol to be true, got %v", refreshed.WoL)
	}
}

func TestModifyWakeOnLan_InvalidRID(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &jailModels.Jail{})
	jailService := &jail.Service{DB: db}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/options/wol/:rid", ModifyWakeOnLan(jailService))

	req := httptest.NewRequest(http.MethodPut, "/options/wol/not-a-number", bytes.NewBufferString(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d, body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
}

func TestModifyWakeOnLan_InvalidPayload(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &jailModels.Jail{})
	jailService := &jail.Service{DB: db}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/options/wol/:rid", ModifyWakeOnLan(jailService))

	req := httptest.NewRequest(http.MethodPut, "/options/wol/101", bytes.NewBufferString(`{"enabled":"yes"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d, body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
}
