// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoHandlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	infoServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/info"
	"github.com/alchemillahq/sylve/internal/services/info"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func newSummaryHistoryRouter(infoService *info.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/info/summary/history", SummaryHistoryHandler(infoService))
	router.GET("/info/summary/history/delta", SummaryHistoryDeltaHandler(infoService))
	return router
}

func TestSummaryHistoryHandlers(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t,
		&infoModels.CPU{},
		&infoModels.RAM{},
		&infoModels.NetworkInterface{},
	)
	now := time.Now().UTC().Truncate(time.Second)
	if err := database.Create(&infoModels.CPU{ID: 1, Usage: 25, CreatedAt: now}).Error; err != nil {
		t.Fatalf("seed CPU history: %v", err)
	}
	if err := database.Create(&infoModels.RAM{ID: 1, Usage: 50, CreatedAt: now}).Error; err != nil {
		t.Fatalf("seed RAM history: %v", err)
	}
	if err := database.Create(&infoModels.NetworkInterface{
		ID: 1, IsDelta: true, ReceivedBytes: 100, SentBytes: 200, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed network history: %v", err)
	}

	router := newSummaryHistoryRouter(&info.Service{TelemetryDB: database})
	response := testutil.PerformJSONRequest(t, router, http.MethodGet, "/info/summary/history", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("expected bootstrap 200, got %d: %s", response.Code, response.Body.String())
	}

	var bootstrap handlerAPIResponse[infoServiceInterfaces.SummaryHistory]
	if err := json.Unmarshal(response.Body.Bytes(), &bootstrap); err != nil {
		t.Fatalf("decode bootstrap response: %v", err)
	}
	if bootstrap.Data.Cursors.CPU != 1 || bootstrap.Data.Cursors.RAM != 1 || bootstrap.Data.Cursors.Network != 1 {
		t.Fatalf("unexpected bootstrap cursors: %+v", bootstrap.Data.Cursors)
	}

	response = testutil.PerformJSONRequest(
		t, router, http.MethodGet,
		"/info/summary/history/delta?cpuAfter=1&ramAfter=1&networkAfter=1", nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("expected delta 200, got %d: %s", response.Code, response.Body.String())
	}

	var delta handlerAPIResponse[infoServiceInterfaces.SummaryHistory]
	if err := json.Unmarshal(response.Body.Bytes(), &delta); err != nil {
		t.Fatalf("decode delta response: %v", err)
	}
	if len(delta.Data.CPU) != 0 || len(delta.Data.RAM) != 0 || len(delta.Data.Network) != 0 {
		t.Fatalf("expected empty delta, got %+v", delta.Data)
	}
}

func TestSummaryHistoryDeltaRejectsInvalidCursors(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t,
		&infoModels.CPU{},
		&infoModels.RAM{},
		&infoModels.NetworkInterface{},
	)
	router := newSummaryHistoryRouter(&info.Service{TelemetryDB: database})

	tests := []string{
		"/info/summary/history/delta",
		"/info/summary/history/delta?cpuAfter=-1&ramAfter=0&networkAfter=0",
		"/info/summary/history/delta?cpuAfter=abc&ramAfter=0&networkAfter=0",
	}
	for _, path := range tests {
		response := testutil.PerformJSONRequest(t, router, http.MethodGet, path, nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for %s, got %d: %s", path, response.Code, response.Body.String())
		}
	}
}
