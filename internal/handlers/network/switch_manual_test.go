// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	networkService "github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type manualSwitchHandlerResponse struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

func setupManualSwitchHandlerRouter(t *testing.T, bodyLimit int64) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db := newNetworkHandlerTestDB(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
	)
	for _, statement := range []string{
		`CREATE TABLE vm_networks (id integer primary key, switch_id integer, switch_type text)`,
		`CREATE TABLE jail_networks (id integer primary key, switch_id integer, switch_type text)`,
		`CREATE TABLE dhcp_manual_switches (dhcp_config_id integer, manual_switch_id integer)`,
		`CREATE TABLE dhcp_ranges (id integer primary key, manual_switch_id integer)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create manual switch handler fixture table: %v", err)
		}
	}

	svc := &networkService.Service{DB: db, TelemetryDB: db}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	routes := router.Group("/network/switch/manual")
	if bodyLimit > 0 {
		routes.Use(middleware.LimitRequestBody(bodyLimit))
	}
	routes.POST("", CreateManualSwitch(svc))
	routes.DELETE("/:id", DeleteManualSwitch(svc))
	return router, db
}

func decodeManualSwitchHandlerResponse(t *testing.T, rr *httptest.ResponseRecorder) manualSwitchHandlerResponse {
	t.Helper()
	var response manualSwitchHandlerResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response body %q: %v", rr.Body.String(), err)
	}
	return response
}

func stubManualSwitchHandlerOperations(
	t *testing.T,
	create func(*networkService.Service, string, string) (*networkModels.ManualSwitch, error),
	delete func(*networkService.Service, uint) error,
) {
	t.Helper()
	originalCreate := createManualSwitchOperation
	originalDelete := deleteManualSwitchOperation
	if create != nil {
		createManualSwitchOperation = create
	}
	if delete != nil {
		deleteManualSwitchOperation = delete
	}
	t.Cleanup(func() {
		createManualSwitchOperation = originalCreate
		deleteManualSwitchOperation = originalDelete
	})
}

func TestCreateManualSwitchReturnsCreatedID(t *testing.T) {
	router, _ := setupManualSwitchHandlerRouter(t, networkService.MaxRequestBodyBytes)
	stubManualSwitchHandlerOperations(t, func(_ *networkService.Service, name, bridge string) (*networkModels.ManualSwitch, error) {
		return &networkModels.ManualSwitch{ID: 42, Name: name, Bridge: bridge}, nil
	}, nil)

	rr := performNetworkJSONRequest(t, router, http.MethodPost, "/network/switch/manual", []byte(`{"name":"uplink","bridge":"bridge0"}`))
	response := decodeManualSwitchHandlerResponse(t, rr)
	var id uint
	if rr.Code != http.StatusCreated || response.Status != "success" || json.Unmarshal(response.Data, &id) != nil || id != 42 {
		t.Fatalf("status=%d response=%+v id=%d", rr.Code, response, id)
	}
}

func TestManualSwitchHandlersRejectInvalidAndOversizedRequests(t *testing.T) {
	router, _ := setupManualSwitchHandlerRouter(t, networkService.MaxRequestBodyBytes)
	invalid := performNetworkJSONRequest(t, router, http.MethodPost, "/network/switch/manual", []byte(`{"name":"missing-bridge"}`))
	response := decodeManualSwitchHandlerResponse(t, invalid)
	if invalid.Code != http.StatusBadRequest || response.Error != "invalid_manual_switch_request" {
		t.Fatalf("status=%d response=%+v", invalid.Code, response)
	}

	const limit = 64
	limitedRouter, _ := setupManualSwitchHandlerRouter(t, limit)
	body, err := json.Marshal(map[string]string{"name": strings.Repeat("a", limit), "bridge": "bridge0"})
	if err != nil {
		t.Fatal(err)
	}
	oversized := performNetworkJSONRequest(t, limitedRouter, http.MethodPost, "/network/switch/manual", body)
	response = decodeManualSwitchHandlerResponse(t, oversized)
	if oversized.Code != http.StatusRequestEntityTooLarge || response.Error != "manual_switch_request_too_large" {
		t.Fatalf("status=%d response=%+v", oversized.Code, response)
	}

	for _, id := range []string{"0", "-1", "not-a-number"} {
		rr := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/switch/manual/"+id, nil)
		response = decodeManualSwitchHandlerResponse(t, rr)
		if rr.Code != http.StatusBadRequest || response.Error != "invalid_manual_switch_id" {
			t.Fatalf("id=%q status=%d response=%+v", id, rr.Code, response)
		}
	}
}

func TestDeleteManualSwitchMapsMissingAndInUseErrors(t *testing.T) {
	router, db := setupManualSwitchHandlerRouter(t, networkService.MaxRequestBodyBytes)

	missing := performNetworkJSONRequest(t, router, http.MethodDelete, "/network/switch/manual/999999", nil)
	response := decodeManualSwitchHandlerResponse(t, missing)
	if missing.Code != http.StatusNotFound || response.Error != "manual_switch_not_found" {
		t.Fatalf("status=%d response=%+v", missing.Code, response)
	}
	if strings.Contains(missing.Body.String(), "record not found") {
		t.Fatalf("response leaked database detail: %s", missing.Body.String())
	}

	switchModel := networkModels.ManualSwitch{Name: "in-use", Bridge: "bridge0"}
	if err := db.Create(&switchModel).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO dhcp_ranges (manual_switch_id) VALUES (?)`, switchModel.ID).Error; err != nil {
		t.Fatal(err)
	}
	inUse := performNetworkJSONRequest(
		t,
		router,
		http.MethodDelete,
		"/network/switch/manual/"+strconv.FormatUint(uint64(switchModel.ID), 10),
		nil,
	)
	response = decodeManualSwitchHandlerResponse(t, inUse)
	if inUse.Code != http.StatusConflict || response.Error != "manual_switch_in_use_by_dhcp_range" {
		t.Fatalf("status=%d response=%+v", inUse.Code, response)
	}
}

func TestRegisteredManualSwitchRoutesMatchSourceAnnotations(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	handlerDir := filepath.Dir(filename)
	routesSource, err := os.ReadFile(filepath.Join(handlerDir, "..", "routes.go"))
	if err != nil {
		t.Fatal(err)
	}
	handlerSource, err := os.ReadFile(filepath.Join(handlerDir, "switch_manual.go"))
	if err != nil {
		t.Fatal(err)
	}

	registered := map[string]struct{}{}
	routePattern := regexp.MustCompile(`(?m)^\s*manualSwitches\.(GET|POST|PUT|PATCH|DELETE)\("([^"]*)"`)
	for _, match := range routePattern.FindAllStringSubmatch(string(routesSource), -1) {
		path := regexp.MustCompile(`:([A-Za-z0-9_]+)`).ReplaceAllString("/network/switch/manual"+match[2], `{$1}`)
		registered[match[1]+" "+path] = struct{}{}
	}

	annotated := map[string]struct{}{}
	annotationPattern := regexp.MustCompile(`(?m)^// @Router (\S+) \[(get|post|put|patch|delete)\]$`)
	for _, match := range annotationPattern.FindAllStringSubmatch(string(handlerSource), -1) {
		annotated[strings.ToUpper(match[2])+" "+match[1]] = struct{}{}
	}

	for route := range registered {
		if _, ok := annotated[route]; !ok {
			t.Errorf("registered route has no matching source annotation: %s", route)
		}
	}
	for route := range annotated {
		if _, ok := registered[route]; !ok {
			t.Errorf("source annotation has no matching registered route: %s", route)
		}
	}
	if len(registered) != 2 || len(annotated) != 2 {
		t.Fatalf("unexpected route totals: registered=%d annotated=%d", len(registered), len(annotated))
	}
	if !regexp.MustCompile(`manualSwitches\.Use\(middleware\.RequireLocalAdmin\(authService\)\)`).Match(routesSource) {
		t.Error("manual switch mutations are missing local-admin authorization")
	}
}
