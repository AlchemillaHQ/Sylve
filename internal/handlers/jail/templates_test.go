// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"net/http"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestGetJailTemplateByID(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &jailModels.JailTemplate{})

	template := jailModels.JailTemplate{
		Name:           "Template 106",
		SourceJailName: "web-106",
		Pool:           "zroot",
		RootDataset:    "zroot/sylve/jails/clones/106",
		Type:           jailModels.JailTypeFreeBSD,
	}
	if err := dbConn.Create(&template).Error; err != nil {
		t.Fatalf("failed to seed template: %v", err)
	}

	svc := &jail.Service{DB: dbConn}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/jail/templates/:id", GetJailTemplateByID(svc))

	t.Run("success", func(t *testing.T) {
		rr := testutil.PerformRequest(t, r, http.MethodGet, "/jail/templates/1", nil, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, rr.Code, rr.Body.String())
		}

		resp := testutil.DecodeJSONResponse[internal.APIResponse[jailModels.JailTemplate]](t, rr)
		if resp.Status != "success" {
			t.Fatalf("expected success, got %q", resp.Status)
		}
		if resp.Data.ID != template.ID {
			t.Fatalf("expected template id %d, got %d", template.ID, resp.Data.ID)
		}
		if resp.Data.SourceJailName != template.SourceJailName {
			t.Fatalf("expected source jail name %q, got %q", template.SourceJailName, resp.Data.SourceJailName)
		}
	})

	t.Run("not found", func(t *testing.T) {
		rr := testutil.PerformRequest(t, r, http.MethodGet, "/jail/templates/999", nil, nil)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected status %d, got %d body=%s", http.StatusNotFound, rr.Code, rr.Body.String())
		}

		resp := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, rr)
		if resp.Message != "template_not_found" {
			t.Fatalf("expected template_not_found message, got %q", resp.Message)
		}
	})
}
