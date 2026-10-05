// SPDX-License-Identifier: BSD-2-Clause

package iscsiHandlers

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/iscsi"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

type configHandlerRuntime struct {
	running     bool
	reloadFails bool
	validations int
	failGate    int
	reloads     int
}

func setupConfigHandler(t *testing.T) (*iscsi.Service, *configHandlerRuntime, *gin.Engine, string) {
	t.Helper()
	service := newTargetHandlerTestService(t)
	path := t.TempDir() + "/ctl.conf"
	t.Cleanup(iscsi.SetTargetConfigPath(path))
	runtime := &configHandlerRuntime{}
	t.Cleanup(utils.SetCommandWithContextForTest(func(command string, args ...string) *exec.Cmd {
		output := ""
		switch command {
		case "/usr/sbin/ctld":
			runtime.validations++
			if runtime.validations == runtime.failGate {
				return exec.Command("/bin/sh", "-c", "echo 'near credential-near-token' >&2; exit 1")
			}
		case "/usr/sbin/service":
			switch args[1] {
			case "onestatus":
				if !runtime.running {
					return exec.Command("/usr/bin/false")
				}
				output = "ctld is running as pid 23."
			case "onereload":
				runtime.reloads++
				if runtime.reloadFails {
					return exec.Command("/usr/bin/false")
				}
			default:
				t.Fatalf("unexpected service mutation: %s", args[1])
			}
		case "/usr/sbin/ctladm":
			if args[0] == "portlist" {
				output = "<ctlportlist/>"
			} else {
				output = "<ctllunlist/>"
			}
		case "/bin/pgrep":
			return exec.Command("/usr/bin/false")
		case "/bin/ps":
			output = "Mon Oct 5 00:00:00 2026"
		case "/usr/bin/sockstat":
		default:
			t.Fatalf("unexpected config command: %s", command)
		}
		return exec.Command("/usr/bin/printf", "%s", output)
	}))
	router := gin.New()
	router.Use(middleware.LimitRequestBody(iscsi.MaxRequestBodyBytes))
	router.GET("/api/iscsi/config", GetConfig(service))
	router.PUT("/api/iscsi/config", SetConfig(service))
	return service, runtime, router, path
}

func TestISCSIConfigHandlerPreservesTextNoChangeRetryAndClear(t *testing.T) {
	service, runtime, router, _ := setupConfigHandler(t)
	extra := "# exact editor text\nauth-group user { chap username secretpassw0rd }\n"
	body, _ := json.Marshal(ISCSIConfigRequest{ExtraTargetConfig: &extra})
	response := testutil.PerformJSONRequest(t, router, http.MethodPut, "/api/iscsi/config", body)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("save status=%d", response.Code)
	}
	saved := testutil.DecodeJSONResponse[internal.APIResponse[iscsiModels.ISCSIConfig]](t, response)
	if saved.Data.ExtraTargetConfig != extra || saved.Data.ApplyStatus != "disabled" {
		t.Fatal("saved text or disabled status changed")
	}
	for _, body := range []string{"{}", `{"extraTargetConfig":null}`} {
		response = testutil.PerformJSONRequest(t, router, http.MethodPut, "/api/iscsi/config", []byte(body))
		if response.Code != http.StatusOK {
			t.Fatal("no-change request failed")
		}
		saved = testutil.DecodeJSONResponse[internal.APIResponse[iscsiModels.ISCSIConfig]](t, response)
		if saved.Data.ExtraTargetConfig != extra || runtime.reloads != 0 {
			t.Fatal("no-change request cleared text or applied runtime state")
		}
	}
	if err := service.DB.Model(&models.BasicSettings{}).Where("1 = 1").Select("Services").Updates(&models.BasicSettings{Services: []models.AvailableService{models.ISCSI}}).Error; err != nil {
		t.Fatal(err)
	}
	runtime.running, runtime.reloadFails = true, true
	for range 2 {
		response = testutil.PerformJSONRequest(t, router, http.MethodPut, "/api/iscsi/config", body)
		if response.Code != http.StatusAccepted || strings.Contains(response.Body.String(), "secretpassw0rd") {
			t.Fatal("failed retry did not return safe 202")
		}
		result := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, response)
		if result.Status != "success" || result.Message != "iscsi_configuration_saved_apply_pending" {
			t.Fatal("wrong saved-pending response")
		}
	}
	if runtime.reloads != 2 {
		t.Fatal("same-value request did not retry")
	}
	runtime.reloadFails = false
	response = testutil.PerformRequest(t, router, http.MethodGet, "/api/iscsi/config", nil, nil)
	saved = testutil.DecodeJSONResponse[internal.APIResponse[iscsiModels.ISCSIConfig]](t, response)
	if response.Code != http.StatusOK || saved.Data.ApplyStatus != "checked" || saved.Data.ExtraTargetConfig != extra {
		t.Fatal("observable status was cached as an old 202 result")
	}
	response = testutil.PerformJSONRequest(t, router, http.MethodPut, "/api/iscsi/config", []byte(`{"extraTargetConfig":""}`))
	if response.Code != http.StatusOK || runtime.reloads != 3 {
		t.Fatal("clear did not apply")
	}
}

func TestISCSIConfigHandlerByteLimit(t *testing.T) {
	service, runtime, router, path := setupConfigHandler(t)
	var extra string
	for _, size := range []int{iscsi.MaxExtraConfigBytes - 1, iscsi.MaxExtraConfigBytes} {
		extra = "#" + strings.Repeat("x", size-1)
		body, err := json.Marshal(ISCSIConfigRequest{ExtraTargetConfig: &extra})
		if err != nil {
			t.Fatal(err)
		}
		response := testutil.PerformJSONRequest(t, router, http.MethodPut, "/api/iscsi/config", body)
		if response.Code != http.StatusOK {
			t.Fatalf("save of %d bytes returned %d", size, response.Code)
		}
		saved := testutil.DecodeJSONResponse[internal.APIResponse[iscsiModels.ISCSIConfig]](t, response)
		if saved.Data.ExtraTargetConfig != extra || saved.Data.ApplyStatus != "disabled" {
			t.Fatal("saved text or disabled status changed")
		}
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	validations := runtime.validations
	oversized := extra + "x"
	body, err := json.Marshal(ISCSIConfigRequest{ExtraTargetConfig: &oversized})
	if err != nil {
		t.Fatal(err)
	}
	response := testutil.PerformJSONRequest(t, router, http.MethodPut, "/api/iscsi/config", body)
	validation := testutil.DecodeJSONResponse[internal.APIResponse[ISCSIConfigValidation]](t, response)
	if response.Code != http.StatusRequestEntityTooLarge || validation.Data.ReasonCode != "extra_config_too_large" || runtime.validations != validations {
		t.Fatal("oversized text bypassed the field limit or reached native validation")
	}
	var settings iscsiModels.ISCSISettings
	if err := service.DB.First(&settings).Error; err != nil || settings.ExtraTargetConfig != extra {
		t.Fatal("oversized text changed saved configuration")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("oversized text changed the target file")
	}
}

func TestISCSIConfigHandlerPrecommitErrorsAndRedaction(t *testing.T) {
	for _, test := range []struct {
		name   string
		body   string
		gate   int
		status int
	}{
		{name: "syntax", body: `{"extraTargetConfig":"pidfile credential-near-token"}`, status: 400},
		{name: "unknown field", body: `{"credential-near-token":"value"}`, status: 400},
		{name: "duplicate field", body: `{"extraTargetConfig":"credential-near-token","extraTargetConfig":null}`, status: 400},
		{name: "wrong type", body: `{"extraTargetConfig":{"credential-near-token":"value"}}`, status: 400},
		{name: "trailing", body: `{"extraTargetConfig":""} {"credential-near-token":1}`, status: 400},
		{name: "null object", body: "null", status: 400},
		{name: "malformed", body: `{"extraTargetConfig": "credential-near-token`, status: 400},
		{name: "managed gate", body: `{"extraTargetConfig":"# credential-near-token"}`, gate: 1, status: 409},
		{name: "merged gate", body: `{"extraTargetConfig":"# credential-near-token"}`, gate: 2, status: 400},
		{name: "field cap", body: `{"extraTargetConfig":"` + strings.Repeat("X", iscsi.MaxExtraConfigBytes+1) + `"}`, status: 413},
		{name: "body cap", body: `{"extraTargetConfig":""}` + strings.Repeat(" ", int(iscsi.MaxRequestBodyBytes)), status: 413},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, runtime, router, path := setupConfigHandler(t)
			runtime.failGate = test.gate
			before := "unchanged owned file"
			if err := os.WriteFile(path, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			response := testutil.PerformJSONRequest(t, router, http.MethodPut, "/api/iscsi/config", []byte(test.body))
			if response.Code != test.status || strings.Contains(response.Body.String(), "credential-near-token") {
				t.Fatalf("status=%d want=%d; unsafe response=%t", response.Code, test.status, strings.Contains(response.Body.String(), "credential-near-token"))
			}
			var settings iscsiModels.ISCSISettings
			service.DB.First(&settings)
			after, _ := os.ReadFile(path)
			if settings.ExtraTargetConfig != "" || string(after) != before || test.gate == 0 && runtime.validations != 0 {
				t.Fatal("rejected request changed DB or file, or invoked native validator")
			}
		})
	}
}

func TestISCSIConfigHandlerReturnsInvalidStoredTextWithoutApply(t *testing.T) {
	service, runtime, router, path := setupConfigHandler(t)
	extra := "pidfile stored-config-marker"
	if err := service.DB.Create(&iscsiModels.ISCSISettings{ID: 1, ExtraTargetConfig: extra}).Error; err != nil {
		t.Fatal(err)
	}
	before := "previous target file"
	if err := os.WriteFile(path, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	response := testutil.PerformRequest(t, router, http.MethodGet, "/api/iscsi/config", nil, nil)
	result := testutil.DecodeJSONResponse[internal.APIResponse[iscsiModels.ISCSIConfig]](t, response)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || result.Data.ApplyStatus != "invalid" || result.Data.ExtraTargetConfig != extra {
		t.Fatal("invalid stored text was not available through GET")
	}
	if result.Data.ReasonCode == nil || *result.Data.ReasonCode != "extra_config_unsupported_stanza" || result.Data.LineNumber == nil || *result.Data.LineNumber != 1 {
		t.Fatal("invalid stored text lost its safe validation detail")
	}
	var settings iscsiModels.ISCSISettings
	if err := service.DB.First(&settings).Error; err != nil || settings.ExtraTargetConfig != extra {
		t.Fatal("GET changed invalid stored text")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != before || runtime.validations != 0 || runtime.reloads != 0 || runtime.running {
		t.Fatal("invalid-state GET changed the file or daemon")
	}
}

func TestISCSIConfigHandlerDatabaseFailureIsSafe(t *testing.T) {
	service, _, router, path := setupConfigHandler(t)
	if err := service.DB.Exec("CREATE TRIGGER reject_extra_config BEFORE UPDATE ON iscsi_settings BEGIN SELECT RAISE(ABORT, 'credential-near-token'); END").Error; err != nil {
		t.Fatal(err)
	}
	response := testutil.PerformJSONRequest(t, router, http.MethodPut, "/api/iscsi/config", []byte(`{"extraTargetConfig":"# candidate"}`))
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "credential-near-token") {
		t.Fatal("DB error was not safely reported")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("DB failure wrote the file")
	}
}

func TestISCSIConfigRoutesRequireLocalAdminForReadsAndWrites(t *testing.T) {
	service, _, _, _ := setupConfigHandler(t)
	if err := service.DB.AutoMigrate(&models.User{}, &models.Group{}); err != nil {
		t.Fatal(err)
	}
	admin := models.User{Username: "admin", Admin: true}
	reader := models.User{Username: "reader"}
	service.DB.Create(&admin)
	service.DB.Create(&reader)
	auth := &authService.Service{DB: service.DB}
	for _, test := range []struct {
		authType string
		id       uint
		status   int
	}{
		{authType: "sylve", id: admin.ID, status: 200},
		{authType: "pam", id: admin.ID, status: 200},
		{authType: authService.AuthTypeSylvePasskey, id: admin.ID, status: 200},
		{authType: "sylve", id: reader.ID, status: 403},
		{authType: "oidc", id: admin.ID, status: 403},
		{authType: "cluster-key", id: admin.ID, status: 403},
	} {
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("AuthType", test.authType); c.Set("UserID", test.id) })
		group := router.Group("/api/iscsi")
		group.Use(middleware.RequireLocalAdminForWrites(auth))
		group.GET("/config", middleware.RequireLocalAdmin(auth), GetConfig(service))
		group.PUT("/config", SetConfig(service))
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			response := testutil.PerformJSONRequest(t, router, method, "/api/iscsi/config", []byte("{}"))
			if response.Code != test.status {
				t.Fatalf("%s %s status=%d want=%d", test.authType, method, response.Code, test.status)
			}
		}
	}
}
