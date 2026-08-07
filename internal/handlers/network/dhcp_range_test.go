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

func setupDHCPRangeHandlerRouter(t *testing.T, bodyLimit int64) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db := newNetworkHandlerTestDB(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
		&networkModels.ManualSwitch{},
		&networkModels.DHCPConfig{},
		&networkModels.DHCPRange{},
		&networkModels.DHCPStaticLease{},
	)
	config := networkModels.DHCPConfig{DNSServers: []string{}, Domain: "lan", ExpandHosts: true}
	if err := db.Create(&config).Error; err != nil {
		t.Fatalf("seed DHCP config: %v", err)
	}

	svc := &networkService.Service{DB: db, TelemetryDB: db}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	ranges := router.Group("/network/dhcp/range")
	if bodyLimit > 0 {
		ranges.Use(middleware.LimitRequestBody(bodyLimit))
	}
	ranges.GET("", GetDHCPRanges(svc))
	ranges.POST("", CreateDHCPRange(svc))
	ranges.PUT("/:id", ModifyDHCPRange(svc))
	ranges.DELETE("/:id", DeleteDHCPRange(svc))
	return router, db
}

func decodeDHCPRangeResponse(t *testing.T, recorder *httptest.ResponseRecorder) dhcpConfigHandlerResponse {
	t.Helper()
	var response dhcpConfigHandlerResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	return response
}

func TestGetDHCPRangesReturnsConcreteCollection(t *testing.T) {
	router, _ := setupDHCPRangeHandlerRouter(t, networkService.MaxRequestBodyBytes)
	recorder := performNetworkJSONRequest(t, router, http.MethodGet, "/network/dhcp/range", nil)
	response := decodeDHCPRangeResponse(t, recorder)
	if recorder.Code != http.StatusOK || response.Status != "success" || response.Message != "dhcp_ranges_retrieved" {
		t.Fatalf("unexpected GET response: status=%d response=%+v", recorder.Code, response)
	}

	var ranges []networkModels.DHCPRange
	if err := json.Unmarshal(response.Data, &ranges); err != nil || ranges == nil {
		t.Fatalf("expected concrete range collection, ranges=%#v error=%v", ranges, err)
	}
}

func TestDHCPRangeHandlersReturnStableValidationAndResourceErrors(t *testing.T) {
	router, db := setupDHCPRangeHandlerRouter(t, networkService.MaxRequestBodyBytes)
	unconfigured := networkModels.StandardSwitch{Name: "unconfigured", BridgeName: "bridge1"}
	if err := db.Create(&unconfigured).Error; err != nil {
		t.Fatalf("seed standard switch: %v", err)
	}

	tests := []struct {
		name       string
		method     string
		path       string
		body       []byte
		wantStatus int
		wantCode   string
	}{
		{name: "invalid path ID", method: http.MethodDelete, path: "/network/dhcp/range/not-a-number", wantStatus: http.StatusBadRequest, wantCode: "invalid_dhcp_range_id"},
		{name: "missing expiry", method: http.MethodPost, path: "/network/dhcp/range", body: []byte(`{"type":"ipv4","startIp":"192.0.2.10","endIp":"192.0.2.20","standardSwitch":1}`), wantStatus: http.StatusBadRequest, wantCode: "invalid_dhcp_range_request"},
		{name: "missing switch", method: http.MethodPost, path: "/network/dhcp/range", body: []byte(`{"type":"ipv4","startIp":"192.0.2.10","endIp":"192.0.2.20","standardSwitch":999,"expiry":0}`), wantStatus: http.StatusNotFound, wantCode: "dhcp_standard_switch_not_found"},
		{name: "unconfigured switch", method: http.MethodPost, path: "/network/dhcp/range", body: []byte(`{"type":"ipv4","startIp":"192.0.2.10","endIp":"192.0.2.20","standardSwitch":` + strconvUint(unconfigured.ID) + `,"expiry":0}`), wantStatus: http.StatusConflict, wantCode: "dhcp_switch_not_enabled"},
		{name: "missing range", method: http.MethodDelete, path: "/network/dhcp/range/999", wantStatus: http.StatusNotFound, wantCode: "dhcp_range_not_found"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := performNetworkJSONRequest(t, router, test.method, test.path, test.body)
			response := decodeDHCPRangeResponse(t, recorder)
			if recorder.Code != test.wantStatus || response.Error != test.wantCode {
				t.Fatalf("status=%d response=%+v", recorder.Code, response)
			}
			if strings.Contains(recorder.Body.String(), "record not found") {
				t.Fatalf("response leaked database detail: %s", recorder.Body.String())
			}
		})
	}
}

func TestModifyDHCPRangeUsesPathIDAndAllowsNoChange(t *testing.T) {
	router, db := setupDHCPRangeHandlerRouter(t, networkService.MaxRequestBodyBytes)
	standard := networkModels.StandardSwitch{Name: "configured", BridgeName: "bridge0"}
	if err := db.Create(&standard).Error; err != nil {
		t.Fatalf("seed standard switch: %v", err)
	}
	var config networkModels.DHCPConfig
	if err := db.First(&config).Error; err != nil {
		t.Fatalf("load DHCP config: %v", err)
	}
	if err := db.Model(&config).Association("StandardSwitches").Append(&standard); err != nil {
		t.Fatalf("associate standard switch: %v", err)
	}
	rangeModel := networkModels.DHCPRange{
		Type:             "ipv4",
		StartIP:          "192.0.2.10",
		EndIP:            "192.0.2.20",
		StandardSwitchID: &standard.ID,
		Expiry:           0,
	}
	if err := db.Create(&rangeModel).Error; err != nil {
		t.Fatalf("seed DHCP range: %v", err)
	}

	body := []byte(`{"id":999,"type":"ipv4","startIp":"192.0.2.10","endIp":"192.0.2.20","standardSwitch":` + strconvUint(standard.ID) + `,"expiry":0}`)
	recorder := performNetworkJSONRequest(t, router, http.MethodPut, "/network/dhcp/range/"+strconvUint(rangeModel.ID), body)
	response := decodeDHCPRangeResponse(t, recorder)
	if recorder.Code != http.StatusOK || response.Status != "success" || response.Message != "dhcp_range_modified" {
		t.Fatalf("unexpected no-change update response: status=%d response=%+v", recorder.Code, response)
	}
	if err := db.First(&rangeModel, rangeModel.ID).Error; err != nil || rangeModel.ID == 999 {
		t.Fatalf("path ID was not authoritative: range=%#v error=%v", rangeModel, err)
	}
}

func TestCreateDHCPRangeRejectsOversizedJSON(t *testing.T) {
	const limit = 128
	router, _ := setupDHCPRangeHandlerRouter(t, limit)
	body := []byte(`{"type":"ipv4","startIp":"` + strings.Repeat("1", limit) + `"}`)
	recorder := performNetworkJSONRequest(t, router, http.MethodPost, "/network/dhcp/range", body)
	response := decodeDHCPRangeResponse(t, recorder)
	if recorder.Code != http.StatusRequestEntityTooLarge || response.Error != "dhcp_range_request_too_large" {
		t.Fatalf("expected stable 413 response: status=%d response=%+v", recorder.Code, response)
	}
}

func TestDHCPRangeRoutesAndAnnotationsMatch(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	handlerDir := filepath.Dir(filename)
	routesSource, err := os.ReadFile(filepath.Join(handlerDir, "..", "routes.go"))
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	handlerSource, err := os.ReadFile(filepath.Join(handlerDir, "dhcp_range.go"))
	if err != nil {
		t.Fatalf("read DHCP range handler: %v", err)
	}

	for name, pattern := range map[string]*regexp.Regexp{
		"resource group":                  regexp.MustCompile(`dhcpRanges := network\.Group\("/dhcp/range"\)`),
		"local-admin write authorization": regexp.MustCompile(`dhcpRanges\.Use\(middleware\.RequireLocalAdminForWrites\(authService\)\)`),
		"GET registration":                regexp.MustCompile(`dhcpRanges\.GET\("", networkHandlers\.GetDHCPRanges`),
		"POST registration":               regexp.MustCompile(`dhcpRanges\.POST\("", networkHandlers\.CreateDHCPRange`),
		"PUT registration":                regexp.MustCompile(`dhcpRanges\.PUT\("/:id", networkHandlers\.ModifyDHCPRange`),
		"DELETE registration":             regexp.MustCompile(`dhcpRanges\.DELETE\("/:id", networkHandlers\.DeleteDHCPRange`),
	} {
		if !pattern.Match(routesSource) {
			t.Errorf("missing %s", name)
		}
	}

	annotations := string(handlerSource)
	for _, expected := range []string{
		`// @Success 200 {object} internal.APIResponse[[]networkModels.DHCPRange] "Success"`,
		`// @Success 201 {object} internal.APIResponse[uint] "Created"`,
		`// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"`,
		`// @Failure 403 {object} internal.APIResponse[any] "Forbidden"`,
		`// @Failure 404 {object} internal.APIResponse[any] "Not Found"`,
		`// @Failure 409 {object} internal.APIResponse[any] "Conflict"`,
		`// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"`,
		`// @Router /network/dhcp/range [get]`,
		`// @Router /network/dhcp/range [post]`,
		`// @Router /network/dhcp/range/{id} [put]`,
		`// @Router /network/dhcp/range/{id} [delete]`,
	} {
		if !strings.Contains(annotations, expected) {
			t.Errorf("missing source annotation %q", expected)
		}
	}
}

func strconvUint(value uint) string {
	return strconv.FormatUint(uint64(value), 10)
}
