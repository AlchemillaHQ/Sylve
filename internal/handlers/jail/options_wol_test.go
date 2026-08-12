// SPDX-License-Identifier: BSD-2-Clause

package jailHandlers

import (
	"errors"
	"net/http"
	"testing"

	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

type jailOptionsHandlerStub struct {
	err               error
	calls             []string
	ctID              uint
	startAtBoot       bool
	bootOrder         int
	enabled           bool
	text              string
	allowedOptions    []string
	metadata          string
	env               string
	lifecycleHookData jailServiceInterfaces.Hooks
}

func (s *jailOptionsHandlerStub) record(call string, ctID uint) error {
	s.calls = append(s.calls, call)
	s.ctID = ctID
	return s.err
}

func (s *jailOptionsHandlerStub) ModifyBootOrder(ctID uint, startAtBoot bool, bootOrder int) error {
	s.startAtBoot = startAtBoot
	s.bootOrder = bootOrder
	return s.record("boot-order", ctID)
}

func (s *jailOptionsHandlerStub) ModifyWakeOnLan(ctID uint, enabled bool) error {
	s.enabled = enabled
	return s.record("wol", ctID)
}

func (s *jailOptionsHandlerStub) ModifyFstab(ctID uint, fstab string) error {
	s.text = fstab
	return s.record("fstab", ctID)
}

func (s *jailOptionsHandlerStub) ModifyResolvConf(ctID uint, resolvConf string) error {
	s.text = resolvConf
	return s.record("resolv-conf", ctID)
}

func (s *jailOptionsHandlerStub) ModifyDevfsRuleset(ctID uint, rules string) error {
	s.text = rules
	return s.record("devfs-rules", ctID)
}

func (s *jailOptionsHandlerStub) ModifyAdditionalOptions(ctID uint, options string) error {
	s.text = options
	return s.record("additional-options", ctID)
}

func (s *jailOptionsHandlerStub) ModifyAllowedOptions(ctID uint, options []string) error {
	s.allowedOptions = append([]string{}, options...)
	return s.record("allowed-options", ctID)
}

func (s *jailOptionsHandlerStub) ModifyMetadata(ctID uint, metadata, env string) error {
	s.metadata = metadata
	s.env = env
	return s.record("metadata", ctID)
}

func (s *jailOptionsHandlerStub) ModifyLifecycleHooks(
	ctID uint,
	hooks jailServiceInterfaces.Hooks,
) error {
	s.lifecycleHookData = hooks
	return s.record("lifecycle-hooks", ctID)
}

func jailOptionsTestRouter(service jailOptionsService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/jail/:ctid/options/boot-order", ModifyBootOrder(service))
	router.PUT("/jail/:ctid/options/wol", ModifyWakeOnLan(service))
	router.PUT("/jail/:ctid/options/fstab", ModifyFstab(service))
	router.PUT("/jail/:ctid/options/resolv-conf", ModifyResolvConf(service))
	router.PUT("/jail/:ctid/options/devfs-rules", ModifyDevFSRules(service))
	router.PUT("/jail/:ctid/options/additional-options", ModifyAdditionalOptions(service))
	router.PUT("/jail/:ctid/options/allowed-options", ModifyAllowedOptions(service))
	router.PUT("/jail/:ctid/options/metadata", ModifyMetadata(service))
	router.PUT("/jail/:ctid/options/lifecycle-hooks", ModifyLifecycleHooks(service))
	return router
}

func TestJailOptionHandlersPreserveExplicitZeroValues(t *testing.T) {
	stub := &jailOptionsHandlerStub{}
	router := jailOptionsTestRouter(stub)
	hooksBody := `{"hooks":{"prestart":{"enabled":false,"script":""},"start":{"enabled":false,"script":""},"poststart":{"enabled":false,"script":""},"prestop":{"enabled":false,"script":""},"stop":{"enabled":false,"script":""},"poststop":{"enabled":false,"script":""}}}`
	tests := []struct {
		path string
		body string
		call string
	}{
		{path: "/jail/101/options/boot-order", body: `{"startAtBoot":false,"bootOrder":0}`, call: "boot-order"},
		{path: "/jail/101/options/wol", body: `{"enabled":false}`, call: "wol"},
		{path: "/jail/101/options/fstab", body: `{"fstab":""}`, call: "fstab"},
		{path: "/jail/101/options/resolv-conf", body: `{"resolvConf":""}`, call: "resolv-conf"},
		{path: "/jail/101/options/devfs-rules", body: `{"devFSRules":""}`, call: "devfs-rules"},
		{path: "/jail/101/options/additional-options", body: `{"additionalOptions":""}`, call: "additional-options"},
		{path: "/jail/101/options/allowed-options", body: `{"allowedOptions":[]}`, call: "allowed-options"},
		{path: "/jail/101/options/metadata", body: `{"metadata":"","env":""}`, call: "metadata"},
		{path: "/jail/101/options/lifecycle-hooks", body: hooksBody, call: "lifecycle-hooks"},
	}

	for _, test := range tests {
		response := testutil.PerformJSONRequest(
			t,
			router,
			http.MethodPut,
			test.path,
			[]byte(test.body),
		)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", test.path, response.Code, response.Body.String())
		}
		if stub.ctID != 101 || stub.calls[len(stub.calls)-1] != test.call {
			t.Fatalf("%s dispatched incorrectly: ctid=%d calls=%v", test.path, stub.ctID, stub.calls)
		}
	}

	if stub.startAtBoot || stub.bootOrder != 0 || stub.enabled || stub.text != "" {
		t.Fatalf("explicit false/zero/empty values changed: %+v", stub)
	}
	if len(stub.allowedOptions) != 0 {
		t.Fatalf("explicit empty allowed-options list changed: %#v", stub.allowedOptions)
	}
	if stub.lifecycleHookData.Prestart.Enabled || stub.lifecycleHookData.Prestart.Script != "" ||
		stub.lifecycleHookData.Poststop.Enabled || stub.lifecycleHookData.Poststop.Script != "" {
		t.Fatalf("explicit disabled lifecycle hooks changed: %+v", stub.lifecycleHookData)
	}
}

func TestJailOptionHandlersRejectOmittedRequiredFields(t *testing.T) {
	router := jailOptionsTestRouter(&jailOptionsHandlerStub{})
	for _, test := range []struct {
		path string
		body string
	}{
		{path: "/jail/101/options/boot-order", body: `{}`},
		{path: "/jail/101/options/wol", body: `{}`},
		{path: "/jail/101/options/fstab", body: `{}`},
		{path: "/jail/101/options/resolv-conf", body: `{}`},
		{path: "/jail/101/options/devfs-rules", body: `{}`},
		{path: "/jail/101/options/additional-options", body: `{}`},
		{path: "/jail/101/options/allowed-options", body: `{}`},
		{path: "/jail/101/options/metadata", body: `{}`},
		{path: "/jail/101/options/lifecycle-hooks", body: `{"hooks":{}}`},
	} {
		response := testutil.PerformJSONRequest(t, router, http.MethodPut, test.path, []byte(test.body))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s expected 400, got %d body=%s", test.path, response.Code, response.Body.String())
		}
	}
}

func TestJailOptionHandlersRejectInvalidCTID(t *testing.T) {
	router := jailOptionsTestRouter(&jailOptionsHandlerStub{})
	for _, path := range []string{
		"/jail/not-a-number/options/wol",
		"/jail/0/options/wol",
	} {
		response := testutil.PerformJSONRequest(
			t,
			router,
			http.MethodPut,
			path,
			[]byte(`{"enabled":false}`),
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s expected 400, got %d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestJailOptionErrorClassification(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		err     error
		status  int
		message string
	}{
		{err: errors.New("invalid_jail_metadata"), status: http.StatusBadRequest, message: "invalid_jail_metadata"},
		{err: errors.New("replication_lease_not_owned"), status: http.StatusForbidden, message: "replication_lease_not_owned"},
		{err: errors.New("jail_not_found"), status: http.StatusNotFound, message: "jail_not_found"},
		{err: errors.New("restore_in_progress"), status: http.StatusConflict, message: "restore_in_progress"},
		{err: errors.New("jail_option_config_conflict: malformed block"), status: http.StatusConflict, message: "jail_option_config_conflict"},
		{err: errors.New("devfs_management_disabled"), status: http.StatusServiceUnavailable, message: "devfs_management_disabled"},
		{err: errors.New("database unavailable"), status: http.StatusInternalServerError, message: "internal_server_error"},
	} {
		status, message := classifyJailOptionError(test.err)
		if status != test.status || message != test.message {
			t.Fatalf("classify(%v)=(%d,%q), want=(%d,%q)", test.err, status, message, test.status, test.message)
		}
	}
}
