// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	networkService "github.com/alchemillahq/sylve/internal/services/network"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type dhcpLeaseHandlerFixture struct {
	router      *gin.Engine
	db          *gorm.DB
	dhcpRange   networkModels.DHCPRange
	ipObject    networkModels.Object
	macObject   networkModels.Object
	staticLease networkModels.DHCPStaticLease
}

func setupDHCPLeaseHandlerRouter(t *testing.T, bodyLimit int64) dhcpLeaseHandlerFixture {
	t.Helper()
	db := newNetworkHandlerTestDB(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
		&networkModels.ManualSwitch{},
		&networkModels.DHCPRange{},
		&networkModels.DHCPStaticLease{},
	)

	dhcpRange := networkModels.DHCPRange{Type: "ipv4", StartIP: "192.0.2.10", EndIP: "192.0.2.100"}
	if err := db.Create(&dhcpRange).Error; err != nil {
		t.Fatalf("seed DHCP range: %v", err)
	}
	ipObject := networkModels.Object{
		Name: "host-v4",
		Type: "Host",
		Entries: []networkModels.ObjectEntry{
			{Value: "192.0.2.20"},
		},
	}
	macObject := networkModels.Object{
		Name: "mac",
		Type: "Mac",
		Entries: []networkModels.ObjectEntry{
			{Value: "aa:bb:cc:dd:ee:ff"},
		},
	}
	for _, object := range []*networkModels.Object{&ipObject, &macObject} {
		if err := db.Create(object).Error; err != nil {
			t.Fatalf("seed %s object: %v", object.Type, err)
		}
	}
	staticLease := networkModels.DHCPStaticLease{
		Hostname:    "client-a",
		Comments:    "test lease",
		IPObjectID:  &ipObject.ID,
		MACObjectID: &macObject.ID,
		DHCPRangeID: dhcpRange.ID,
	}
	if err := db.Create(&staticLease).Error; err != nil {
		t.Fatalf("seed static lease: %v", err)
	}

	svc := &networkService.Service{DB: db, TelemetryDB: db}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	leases := router.Group("/network/dhcp/lease")
	if bodyLimit > 0 {
		leases.Use(middleware.LimitRequestBody(bodyLimit))
	}
	leases.GET("", GetDHCPLeases(svc))
	leases.POST("", CreateDHCPLease(svc))
	leases.PUT("/:id", UpdateDHCPLease(svc))
	leases.DELETE("/dynamic", DeleteDynamicDHCPLease(svc))
	leases.DELETE("/:id", DeleteDHCPLease(svc))

	return dhcpLeaseHandlerFixture{
		router:      router,
		db:          db,
		dhcpRange:   dhcpRange,
		ipObject:    ipObject,
		macObject:   macObject,
		staticLease: staticLease,
	}
}

func TestUpdateDHCPLeaseUsesPathIDAndAllowsNoChange(t *testing.T) {
	fixture := setupDHCPLeaseHandlerRouter(t, networkService.MaxRequestBodyBytes)
	body := []byte(`{"id":999,"hostname":"client-a","comments":"test lease","ipId":` + strconvUint(fixture.ipObject.ID) + `,"macId":` + strconvUint(fixture.macObject.ID) + `,"duidId":null,"dhcpRangeId":` + strconvUint(fixture.dhcpRange.ID) + `}`)
	recorder := performNetworkJSONRequest(t, fixture.router, http.MethodPut, "/network/dhcp/lease/"+strconvUint(fixture.staticLease.ID), body)
	response := decodeDHCPConfigHandlerResponse(t, recorder)
	if recorder.Code != http.StatusOK || response.Status != "success" || response.Message != "dhcp_lease_updated" {
		t.Fatalf("unexpected no-change response: status=%d response=%+v", recorder.Code, response)
	}

	var persisted networkModels.DHCPStaticLease
	if err := fixture.db.First(&persisted, fixture.staticLease.ID).Error; err != nil || persisted.ID == 999 {
		t.Fatalf("path ID was not authoritative: lease=%#v error=%v", persisted, err)
	}
}

func TestDHCPLeaseHandlersReturnStableValidationAndResourceErrors(t *testing.T) {
	fixture := setupDHCPLeaseHandlerRouter(t, networkService.MaxRequestBodyBytes)
	tests := []struct {
		name       string
		method     string
		path       string
		body       []byte
		wantStatus int
		wantCode   string
	}{
		{name: "invalid path ID", method: http.MethodDelete, path: "/network/dhcp/lease/not-a-number", wantStatus: http.StatusBadRequest, wantCode: "invalid_dhcp_lease_id"},
		{name: "missing lease", method: http.MethodDelete, path: "/network/dhcp/lease/999", wantStatus: http.StatusNotFound, wantCode: "dhcp_lease_not_found"},
		{name: "missing range", method: http.MethodPost, path: "/network/dhcp/lease", body: []byte(`{"hostname":"client-b","ipId":` + strconvUint(fixture.ipObject.ID) + `,"macId":` + strconvUint(fixture.macObject.ID) + `,"dhcpRangeId":999}`), wantStatus: http.StatusNotFound, wantCode: "dhcp_range_not_found"},
		{name: "duplicate hostname", method: http.MethodPost, path: "/network/dhcp/lease", body: []byte(`{"hostname":"CLIENT-A","ipId":` + strconvUint(fixture.ipObject.ID) + `,"macId":` + strconvUint(fixture.macObject.ID) + `,"dhcpRangeId":` + strconvUint(fixture.dhcpRange.ID) + `}`), wantStatus: http.StatusConflict, wantCode: "duplicate_hostname"},
		{name: "invalid dynamic identifier", method: http.MethodDelete, path: "/network/dhcp/lease/dynamic", body: []byte(`{"identifier":"not-a-mac","ip":"192.0.2.20"}`), wantStatus: http.StatusBadRequest, wantCode: "invalid_dynamic_dhcp_lease_identifier"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := performNetworkJSONRequest(t, fixture.router, test.method, test.path, test.body)
			response := decodeDHCPConfigHandlerResponse(t, recorder)
			if recorder.Code != test.wantStatus || response.Error != test.wantCode {
				t.Fatalf("status=%d response=%+v", recorder.Code, response)
			}
			if strings.Contains(recorder.Body.String(), "record not found") {
				t.Fatalf("response leaked database detail: %s", recorder.Body.String())
			}
		})
	}
}

func TestCreateDHCPLeaseRejectsOversizedJSON(t *testing.T) {
	const limit = 128
	fixture := setupDHCPLeaseHandlerRouter(t, limit)
	body := []byte(`{"hostname":"` + strings.Repeat("a", limit) + `"}`)
	recorder := performNetworkJSONRequest(t, fixture.router, http.MethodPost, "/network/dhcp/lease", body)
	response := decodeDHCPConfigHandlerResponse(t, recorder)
	if recorder.Code != http.StatusRequestEntityTooLarge || response.Error != "dhcp_lease_request_too_large" {
		t.Fatalf("expected stable 413 response: status=%d response=%+v", recorder.Code, response)
	}
}

func TestDHCPLeaseRoutesAndAnnotationsMatch(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	handlerDir := filepath.Dir(filename)
	routesSource, err := os.ReadFile(filepath.Join(handlerDir, "..", "routes.go"))
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	handlerSource, err := os.ReadFile(filepath.Join(handlerDir, "dhcp_lease.go"))
	if err != nil {
		t.Fatalf("read DHCP lease handler: %v", err)
	}

	for name, pattern := range map[string]*regexp.Regexp{
		"resource group":                  regexp.MustCompile(`dhcpLeases := network\.Group\("/dhcp/lease"\)`),
		"local-admin write authorization": regexp.MustCompile(`dhcpLeases\.Use\(middleware\.RequireLocalAdminForWrites\(authService\)\)`),
		"GET registration":                regexp.MustCompile(`dhcpLeases\.GET\("", networkHandlers\.GetDHCPLeases`),
		"POST registration":               regexp.MustCompile(`dhcpLeases\.POST\("", networkHandlers\.CreateDHCPLease`),
		"PUT registration":                regexp.MustCompile(`dhcpLeases\.PUT\("/:id", networkHandlers\.UpdateDHCPLease`),
		"dynamic DELETE registration":     regexp.MustCompile(`dhcpLeases\.DELETE\("/dynamic", networkHandlers\.DeleteDynamicDHCPLease`),
		"static DELETE registration":      regexp.MustCompile(`dhcpLeases\.DELETE\("/:id", networkHandlers\.DeleteDHCPLease`),
	} {
		if !pattern.Match(routesSource) {
			t.Errorf("missing %s", name)
		}
	}

	annotations := string(handlerSource)
	for _, expected := range []string{
		`// @Success 200 {object} internal.APIResponse[networkServiceInterfaces.Leases] "Success"`,
		`// @Success 201 {object} internal.APIResponse[uint] "Created"`,
		`// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"`,
		`// @Failure 403 {object} internal.APIResponse[any] "Forbidden"`,
		`// @Failure 404 {object} internal.APIResponse[any] "Not Found"`,
		`// @Failure 409 {object} internal.APIResponse[any] "Conflict"`,
		`// @Failure 413 {object} internal.APIResponse[any] "Request Entity Too Large"`,
		`// @Router /network/dhcp/lease [get]`,
		`// @Router /network/dhcp/lease [post]`,
		`// @Router /network/dhcp/lease/{id} [put]`,
		`// @Router /network/dhcp/lease/{id} [delete]`,
		`// @Router /network/dhcp/lease/dynamic [delete]`,
	} {
		if !strings.Contains(annotations, expected) {
			t.Errorf("missing source annotation %q", expected)
		}
	}
}
