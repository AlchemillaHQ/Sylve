// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type vmConfigReinitializationStub struct {
	rid uint
	err error
	ctx context.Context
}

func (s *vmConfigReinitializationStub) ReinitializeVMConfig(rid uint, ctx context.Context) error {
	s.rid = rid
	s.ctx = ctx
	return s.err
}

func TestReinitializeVMConfigHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name       string
		rid        string
		err        error
		wantStatus int
		wantCode   string
		wantCalled bool
	}{
		{name: "success", rid: "916", wantStatus: http.StatusOK, wantCode: "vm_config_reinitialized", wantCalled: true},
		{name: "invalid RID", rid: "invalid", wantStatus: http.StatusBadRequest, wantCode: "invalid_vm_rid"},
		{name: "zero RID", rid: "0", wantStatus: http.StatusBadRequest, wantCode: "invalid_vm_rid"},
		{name: "negative RID", rid: "-1", wantStatus: http.StatusBadRequest, wantCode: "invalid_vm_rid"},
		{name: "RID overflow", rid: "4294967296", wantStatus: http.StatusBadRequest, wantCode: "invalid_vm_rid"},
		{name: "missing VM", rid: "916", err: errors.New("vm_not_found"), wantStatus: http.StatusNotFound, wantCode: "vm_not_found", wantCalled: true},
		{name: "live definition", rid: "916", err: errors.New("vm_not_orphaned"), wantStatus: http.StatusConflict, wantCode: "vm_not_orphaned", wantCalled: true},
		{name: "active action", rid: "916", err: errors.New("lifecycle_task_in_progress"), wantStatus: http.StatusConflict, wantCode: "lifecycle_task_in_progress", wantCalled: true},
		{name: "non-owner", rid: "916", err: errors.New("replication_lease_not_owned"), wantStatus: http.StatusForbidden, wantCode: "replication_lease_not_owned", wantCalled: true},
		{name: "claim conflict", rid: "916", err: errors.New("guest_identity_claim_conflict"), wantStatus: http.StatusConflict, wantCode: "guest_identity_claim_conflict", wantCalled: true},
		{name: "registry initializing", rid: "916", err: errors.New("guest_identity_registry_initializing"), wantStatus: http.StatusServiceUnavailable, wantCode: "guest_identity_registry_initializing", wantCalled: true},
		{name: "cluster unavailable", rid: "916", err: errors.New("cluster_consensus_unavailable"), wantStatus: http.StatusServiceUnavailable, wantCode: "cluster_consensus_unavailable", wantCalled: true},
		{name: "connection unavailable", rid: "916", err: errors.New("libvirt_connection_unavailable"), wantStatus: http.StatusServiceUnavailable, wantCode: "libvirt_connection_unavailable", wantCalled: true},
		{name: "orphan check unavailable", rid: "916", err: errors.New("vm_orphan_check_unavailable"), wantStatus: http.StatusServiceUnavailable, wantCode: "libvirt_connection_unavailable", wantCalled: true},
		{name: "definition failed", rid: "916", err: errors.New("failed_to_define_vm_domain"), wantStatus: http.StatusInternalServerError, wantCode: "failed_to_reinitialize_vm_config", wantCalled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub := &vmConfigReinitializationStub{err: test.err}
			router := gin.New()
			router.POST("/vm/:rid/domain/reinitialize", ReinitializeVMConfig(stub))
			request := httptest.NewRequest(http.MethodPost, "/vm/"+test.rid+"/domain/reinitialize", nil)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			response := decodeVMReadEnvelope(t, recorder)
			if response.Message != test.wantCode {
				t.Fatalf("message = %q, want %q", response.Message, test.wantCode)
			}
			if (stub.rid != 0) != test.wantCalled {
				t.Fatalf("service called = %t, want %t", stub.rid != 0, test.wantCalled)
			}
			if test.wantCalled && (stub.rid != 916 || stub.ctx != request.Context()) {
				t.Fatalf("request identity/context not forwarded: %+v", stub)
			}
		})
	}
}
