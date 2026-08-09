// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type mockVMOptionsService struct {
	err              error
	calls            int
	lastMethod       string
	lastRID          uint
	lastEnabled      bool
	lastStartAtBoot  bool
	lastBootOrder    int
	lastWaitTime     int
	lastString       string
	lastCloudInit    [3]string
	lastExtraOptions []string
}

func (m *mockVMOptionsService) record(method string, rid uint) error {
	m.calls++
	m.lastMethod = method
	m.lastRID = rid
	return m.err
}

func (m *mockVMOptionsService) ModifyWakeOnLan(rid uint, enabled bool) error {
	m.lastEnabled = enabled
	return m.record("wol", rid)
}

func (m *mockVMOptionsService) ModifyBootOrder(rid uint, startAtBoot bool, bootOrder int) error {
	m.lastStartAtBoot = startAtBoot
	m.lastBootOrder = bootOrder
	return m.record("boot-order", rid)
}

func (m *mockVMOptionsService) ModifyClock(rid uint, timeOffset string) error {
	m.lastString = timeOffset
	return m.record("clock", rid)
}

func (m *mockVMOptionsService) ModifySerial(rid uint, enabled bool) error {
	m.lastEnabled = enabled
	return m.record("serial-console", rid)
}

func (m *mockVMOptionsService) ModifyShutdownWaitTime(rid uint, waitTime int) error {
	m.lastWaitTime = waitTime
	return m.record("shutdown-wait-time", rid)
}

func (m *mockVMOptionsService) ModifyCloudInitData(
	rid uint,
	data string,
	metadata string,
	networkConfig string,
) error {
	m.lastCloudInit = [3]string{data, metadata, networkConfig}
	return m.record("cloud-init", rid)
}

func (m *mockVMOptionsService) ModifyBootROM(rid uint, bootROM string) error {
	m.lastString = bootROM
	return m.record("boot-rom", rid)
}

func (m *mockVMOptionsService) ModifyExtraBhyveOptions(rid uint, options []string) error {
	m.lastExtraOptions = append([]string{}, options...)
	return m.record("extra-bhyve-options", rid)
}

func (m *mockVMOptionsService) ModifyIgnoreUMSRs(rid uint, ignore bool) error {
	m.lastEnabled = ignore
	return m.record("ignore-umsrs", rid)
}

func (m *mockVMOptionsService) ModifyQemuGuestAgent(rid uint, enabled bool) error {
	m.lastEnabled = enabled
	return m.record("qemu-guest-agent", rid)
}

func (m *mockVMOptionsService) ModifyTPMEmulation(rid uint, enabled bool) error {
	m.lastEnabled = enabled
	return m.record("tpm", rid)
}

type mockVMQGAService struct {
	info    libvirtServiceInterfaces.QemuGuestAgentInfo
	err     error
	calls   int
	lastRID uint
}

func (m *mockVMQGAService) GetQemuGuestAgentInfo(
	rid uint,
) (libvirtServiceInterfaces.QemuGuestAgentInfo, error) {
	m.calls++
	m.lastRID = rid
	return m.info, m.err
}

func newVMOptionsRouter(service vmOptionsService, bodyLimit int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if bodyLimit > 0 {
		router.Use(middleware.LimitRequestBody(bodyLimit))
	}
	router.PUT("/vm/:rid/options/wol", ModifyWakeOnLan(service))
	router.PUT("/vm/:rid/options/boot-order", ModifyBootOrder(service))
	router.PUT("/vm/:rid/options/clock", ModifyClock(service))
	router.PUT("/vm/:rid/options/serial-console", ModifySerialConsole(service))
	router.PUT("/vm/:rid/options/shutdown-wait-time", ModifyShutdownWaitTime(service))
	router.PUT("/vm/:rid/options/cloud-init", ModifyCloudInitData(service))
	router.PUT("/vm/:rid/options/boot-rom", ModifyBootROM(service))
	router.PUT("/vm/:rid/options/extra-bhyve-options", ModifyExtraBhyveOptions(service))
	router.PUT("/vm/:rid/options/ignore-umsrs", ModifyIgnoreUMSRs(service))
	router.PUT("/vm/:rid/options/qemu-guest-agent", ModifyQemuGuestAgent(service))
	router.PUT("/vm/:rid/options/tpm", ModifyTPM(service))
	return router
}

func TestVMOptionHandlersPreserveExplicitFalseZeroAndEmptyValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		body       string
		wantMethod string
		assert     func(*testing.T, *mockVMOptionsService)
	}{
		{name: "wol false", path: "/vm/101/options/wol", body: `{"enabled":false}`, wantMethod: "wol", assert: assertVMOptionFalse},
		{name: "boot zero", path: "/vm/101/options/boot-order", body: `{"startAtBoot":false,"bootOrder":0}`, wantMethod: "boot-order", assert: func(t *testing.T, service *mockVMOptionsService) {
			t.Helper()
			if service.lastStartAtBoot || service.lastBootOrder != 0 {
				t.Fatalf("explicit boot values were not preserved: enabled=%t order=%d", service.lastStartAtBoot, service.lastBootOrder)
			}
		}},
		{name: "clock", path: "/vm/101/options/clock", body: `{"timeOffset":"utc"}`, wantMethod: "clock"},
		{name: "serial false", path: "/vm/101/options/serial-console", body: `{"enabled":false}`, wantMethod: "serial-console", assert: assertVMOptionFalse},
		{name: "shutdown", path: "/vm/101/options/shutdown-wait-time", body: `{"waitTime":1}`, wantMethod: "shutdown-wait-time", assert: func(t *testing.T, service *mockVMOptionsService) {
			t.Helper()
			if service.lastWaitTime != 1 {
				t.Fatalf("wait time=%d, want 1", service.lastWaitTime)
			}
		}},
		{name: "cloud init clear", path: "/vm/101/options/cloud-init", body: `{"data":"","metadata":"","networkConfig":""}`, wantMethod: "cloud-init", assert: func(t *testing.T, service *mockVMOptionsService) {
			t.Helper()
			if service.lastCloudInit != [3]string{} {
				t.Fatalf("explicit cloud-init clear was not preserved: %#v", service.lastCloudInit)
			}
		}},
		{name: "boot rom", path: "/vm/101/options/boot-rom", body: `{"bootRom":"none"}`, wantMethod: "boot-rom"},
		{name: "extra options clear", path: "/vm/101/options/extra-bhyve-options", body: `{"extraBhyveOptions":[]}`, wantMethod: "extra-bhyve-options", assert: func(t *testing.T, service *mockVMOptionsService) {
			t.Helper()
			if service.lastExtraOptions == nil || len(service.lastExtraOptions) != 0 {
				t.Fatalf("explicit empty option list was not preserved: %#v", service.lastExtraOptions)
			}
		}},
		{name: "ignore umsrs false", path: "/vm/101/options/ignore-umsrs", body: `{"ignoreUMSRs":false}`, wantMethod: "ignore-umsrs", assert: assertVMOptionFalse},
		{name: "qga false", path: "/vm/101/options/qemu-guest-agent", body: `{"enabled":false}`, wantMethod: "qemu-guest-agent", assert: assertVMOptionFalse},
		{name: "tpm false", path: "/vm/101/options/tpm", body: `{"enabled":false}`, wantMethod: "tpm", assert: assertVMOptionFalse},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &mockVMOptionsService{}
			response := testutil.PerformJSONRequest(
				t,
				newVMOptionsRouter(service, 0),
				http.MethodPut,
				test.path,
				[]byte(test.body),
			)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if service.calls != 1 || service.lastMethod != test.wantMethod || service.lastRID != 101 {
				t.Fatalf("unexpected service call: calls=%d method=%q rid=%d", service.calls, service.lastMethod, service.lastRID)
			}
			if test.assert != nil {
				test.assert(t, service)
			}
		})
	}
}

func assertVMOptionFalse(t *testing.T, service *mockVMOptionsService) {
	t.Helper()
	if service.lastEnabled {
		t.Fatal("explicit false was changed to true")
	}
}

func TestVMOptionHandlersRejectOmittedRequiredFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		body string
	}{
		{path: "/vm/101/options/wol", body: `{}`},
		{path: "/vm/101/options/boot-order", body: `{"bootOrder":0}`},
		{path: "/vm/101/options/boot-order", body: `{"startAtBoot":false}`},
		{path: "/vm/101/options/clock", body: `{}`},
		{path: "/vm/101/options/serial-console", body: `{}`},
		{path: "/vm/101/options/shutdown-wait-time", body: `{}`},
		{path: "/vm/101/options/cloud-init", body: `{"metadata":"","networkConfig":""}`},
		{path: "/vm/101/options/cloud-init", body: `{"data":"","networkConfig":""}`},
		{path: "/vm/101/options/cloud-init", body: `{"data":"","metadata":""}`},
		{path: "/vm/101/options/boot-rom", body: `{}`},
		{path: "/vm/101/options/extra-bhyve-options", body: `{}`},
		{path: "/vm/101/options/ignore-umsrs", body: `{}`},
		{path: "/vm/101/options/qemu-guest-agent", body: `{}`},
		{path: "/vm/101/options/tpm", body: `{}`},
	}

	for _, test := range tests {
		service := &mockVMOptionsService{}
		response := testutil.PerformJSONRequest(
			t,
			newVMOptionsRouter(service, 0),
			http.MethodPut,
			test.path,
			[]byte(test.body),
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s %s: status=%d body=%s", test.path, test.body, response.Code, response.Body.String())
		}
		if service.calls != 0 {
			t.Fatalf("%s %s reached service %d times", test.path, test.body, service.calls)
		}
	}
}

func TestVMOptionHandlersRejectInvalidRIDAndOversizedBody(t *testing.T) {
	t.Parallel()

	service := &mockVMOptionsService{}
	for _, path := range []string{"/vm/0/options/wol", "/vm/nope/options/wol"} {
		response := testutil.PerformJSONRequest(
			t,
			newVMOptionsRouter(service, 0),
			http.MethodPut,
			path,
			[]byte(`{"enabled":false}`),
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s", path, response.Code, response.Body.String())
		}
	}

	response := testutil.PerformJSONRequest(
		t,
		newVMOptionsRouter(service, 32),
		http.MethodPut,
		"/vm/101/options/wol",
		[]byte(`{"enabled":false,"padding":"`+strings.Repeat("x", 128)+`"}`),
	)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status=%d body=%s", response.Code, response.Body.String())
	}
	if service.calls != 0 {
		t.Fatalf("invalid requests reached service %d times", service.calls)
	}
}

func TestVMOptionHandlersMapServiceErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid", err: errors.New("invalid_time_offset"), wantStatus: http.StatusBadRequest},
		{name: "forbidden", err: errors.New("replication_lease_not_owned"), wantStatus: http.StatusForbidden},
		{name: "missing", err: gorm.ErrRecordNotFound, wantStatus: http.StatusNotFound},
		{name: "state conflict", err: errors.New("domain_state_not_shutoff: 101"), wantStatus: http.StatusConflict},
		{name: "agent disabled", err: errors.New("qemu_guest_agent_disabled"), wantStatus: http.StatusConflict},
		{name: "agent unavailable", err: errors.New("qga_error_CommandNotFound: missing"), wantStatus: http.StatusServiceUnavailable},
		{name: "libvirt unavailable", err: errors.New("failed_to_get_domain_state: libvirt unavailable"), wantStatus: http.StatusServiceUnavailable},
		{name: "unexpected", err: errors.New("failed_to_update_vm"), wantStatus: http.StatusInternalServerError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &mockVMOptionsService{err: test.err}
			response := testutil.PerformJSONRequest(
				t,
				newVMOptionsRouter(service, 0),
				http.MethodPut,
				"/vm/101/options/wol",
				[]byte(`{"enabled":true}`),
			)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

func TestVMOptionNoChangesIsSuccessful(t *testing.T) {
	t.Parallel()

	service := &mockVMOptionsService{err: errors.New("no_changes_detected: 101")}
	response := testutil.PerformJSONRequest(
		t,
		newVMOptionsRouter(service, 0),
		http.MethodPut,
		"/vm/101/options/wol",
		[]byte(`{"enabled":false}`),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	decoded := testutil.DecodeJSONResponse[vmHardwareHandlerResponse](t, response)
	if decoded.Status != "success" || decoded.Message != "no_changes_detected" {
		t.Fatalf("unexpected response: %+v", decoded)
	}
}

func TestQGAInfoHandlerUsesNestedRIDAndMapsStates(t *testing.T) {
	t.Parallel()

	service := &mockVMQGAService{info: libvirtServiceInterfaces.QemuGuestAgentInfo{
		OSInfo: libvirtServiceInterfaces.QGAOSInfo{Name: "FreeBSD"},
	}}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/vm/:rid/guest-agent", GetQemuGuestAgentInfo(service))

	response := testutil.PerformJSONRequest(t, router, http.MethodGet, "/vm/101/guest-agent", nil)
	if response.Code != http.StatusOK || service.calls != 1 || service.lastRID != 101 {
		t.Fatalf("status=%d calls=%d rid=%d body=%s", response.Code, service.calls, service.lastRID, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"name":"FreeBSD"`) {
		t.Fatalf("concrete QGA data missing: %s", response.Body.String())
	}

	for _, test := range []struct {
		err        error
		wantStatus int
	}{
		{err: errors.New("qemu_guest_agent_disabled"), wantStatus: http.StatusConflict},
		{err: errors.New("qga_requires_running_vm"), wantStatus: http.StatusConflict},
		{err: errors.New("failed_to_connect_qga_socket"), wantStatus: http.StatusServiceUnavailable},
		{err: errors.New("qga_error_CommandNotFound: missing"), wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(test.err.Error(), func(t *testing.T) {
			errorService := &mockVMQGAService{err: test.err}
			errorRouter := gin.New()
			errorRouter.GET("/vm/:rid/guest-agent", GetQemuGuestAgentInfo(errorService))
			response := testutil.PerformJSONRequest(t, errorRouter, http.MethodGet, "/vm/101/guest-agent", nil)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

func TestVMOptionRoutesAreNestedAndLimitedBeforeLogging(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	handlerDir := filepath.Dir(filename)
	routesSource, err := os.ReadFile(filepath.Join(handlerDir, "..", "routes.go"))
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	source := string(routesSource)

	limitIndex := strings.Index(source, "vm.Use(middleware.LimitRequestBody(libvirt.MaxRequestBodyBytes))")
	loggerIndex := strings.Index(source, "vm.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))")
	hostIndex := strings.Index(source, "vm.Use(EnsureCorrectHost(db, authService))")
	if hostIndex < 0 || limitIndex < 0 || hostIndex > limitIndex {
		t.Error("selected-node routing must run before local VM body processing")
	}
	if limitIndex < 0 || loggerIndex < 0 || limitIndex > loggerIndex {
		t.Error("VM body limit must be installed before request logging")
	}

	for _, route := range []string{
		`vm.PUT("/:rid/options/wol"`,
		`vm.PUT("/:rid/options/boot-order"`,
		`vm.PUT("/:rid/options/clock"`,
		`vm.PUT("/:rid/options/serial-console"`,
		`vm.PUT("/:rid/options/shutdown-wait-time"`,
		`vm.PUT("/:rid/options/cloud-init"`,
		`vm.PUT("/:rid/options/boot-rom"`,
		`vm.PUT("/:rid/options/extra-bhyve-options"`,
		`vm.PUT("/:rid/options/ignore-umsrs"`,
		`vm.PUT("/:rid/options/qemu-guest-agent"`,
		`vm.PUT("/:rid/options/tpm"`,
		`vm.GET("/:rid/guest-agent"`,
	} {
		if !strings.Contains(source, route) {
			t.Errorf("missing normalized route %s", route)
		}
	}
	if strings.Contains(source, `vm.PUT("/options/`) || strings.Contains(source, `vm.GET("/qga/`) {
		t.Error("historical VM option/QGA route registration remains")
	}

	optionsSource, err := os.ReadFile(filepath.Join(handlerDir, "vm_options.go"))
	if err != nil {
		t.Fatalf("read vm_options.go: %v", err)
	}
	qgaSource, err := os.ReadFile(filepath.Join(handlerDir, "vm_qga.go"))
	if err != nil {
		t.Fatalf("read vm_qga.go: %v", err)
	}
	annotations := string(optionsSource) + string(qgaSource)
	for _, annotation := range []string{
		"// @Router /vm/{rid}/options/wol [put]",
		"// @Router /vm/{rid}/options/boot-order [put]",
		"// @Router /vm/{rid}/options/clock [put]",
		"// @Router /vm/{rid}/options/serial-console [put]",
		"// @Router /vm/{rid}/options/shutdown-wait-time [put]",
		"// @Router /vm/{rid}/options/cloud-init [put]",
		"// @Router /vm/{rid}/options/boot-rom [put]",
		"// @Router /vm/{rid}/options/extra-bhyve-options [put]",
		"// @Router /vm/{rid}/options/ignore-umsrs [put]",
		"// @Router /vm/{rid}/options/qemu-guest-agent [put]",
		"// @Router /vm/{rid}/options/tpm [put]",
		"// @Router /vm/{rid}/guest-agent [get]",
	} {
		if !strings.Contains(annotations, annotation) {
			t.Errorf("missing source annotation %s", annotation)
		}
	}
	if !strings.Contains(
		string(qgaSource),
		"internal.APIResponse[libvirtServiceInterfaces.QemuGuestAgentInfo]",
	) {
		t.Error("QGA success annotation does not use the concrete information type")
	}
}
