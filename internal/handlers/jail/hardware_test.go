// SPDX-License-Identifier: BSD-2-Clause

package jailHandlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

type jailHardwareHandlerStub struct {
	memoryFn   func(uint, int64) (jailServiceInterfaces.JailHardwareResult, error)
	cpuFn      func(uint, int64) (jailServiceInterfaces.JailHardwareResult, error)
	resourceFn func(uint, bool) (jailServiceInterfaces.JailHardwareResult, error)
}

func (s *jailHardwareHandlerStub) UpdateMemory(
	ctID uint,
	memory int64,
) (jailServiceInterfaces.JailHardwareResult, error) {
	return s.memoryFn(ctID, memory)
}

func (s *jailHardwareHandlerStub) UpdateCPU(
	ctID uint,
	cores int64,
) (jailServiceInterfaces.JailHardwareResult, error) {
	return s.cpuFn(ctID, cores)
}

func (s *jailHardwareHandlerStub) UpdateResourceLimits(
	ctID uint,
	enabled bool,
) (jailServiceInterfaces.JailHardwareResult, error) {
	return s.resourceFn(ctID, enabled)
}

func TestJailHardwareHandlerUsesNestedCTIDAndReturnsState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &jailHardwareHandlerStub{
		memoryFn: func(ctID uint, memory int64) (jailServiceInterfaces.JailHardwareResult, error) {
			if ctID != 42 || memory != 2*1024*1024*1024 {
				t.Fatalf("unexpected memory request: ctid=%d memory=%d", ctID, memory)
			}
			return jailServiceInterfaces.JailHardwareResult{
				CTID:           ctID,
				ResourceLimits: true,
				Memory:         memory,
				Cores:          2,
				CPUSet:         []int{0, 1},
			}, nil
		},
	}
	router := gin.New()
	router.PUT("/jail/:ctid/hardware/ram", UpdateJailMemory(stub))

	response := testutil.PerformJSONRequest(
		t,
		router,
		http.MethodPut,
		"/jail/42/hardware/ram",
		[]byte(`{"memory":2147483648}`),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", response.Code, response.Body.String())
	}
	var envelope internal.APIResponse[jailServiceInterfaces.JailHardwareResult]
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.CTID != 42 || envelope.Data.Memory != 2*1024*1024*1024 || len(envelope.Data.CPUSet) != 2 {
		t.Fatalf("unexpected hardware response: %+v", envelope.Data)
	}
}

func TestJailResourceLimitsHandlerPreservesExplicitFalse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	called := false
	stub := &jailHardwareHandlerStub{
		resourceFn: func(ctID uint, enabled bool) (jailServiceInterfaces.JailHardwareResult, error) {
			called = true
			if ctID != 9 || enabled {
				t.Fatalf("unexpected resource-limit request: ctid=%d enabled=%t", ctID, enabled)
			}
			return jailServiceInterfaces.JailHardwareResult{CTID: ctID, CPUSet: []int{}}, nil
		},
	}
	router := gin.New()
	router.PUT("/jail/:ctid/hardware/resource-limits", UpdateResourceLimits(stub))

	response := testutil.PerformJSONRequest(
		t,
		router,
		http.MethodPut,
		"/jail/9/hardware/resource-limits",
		[]byte(`{"enabled":false}`),
	)
	if response.Code != http.StatusOK || !called {
		t.Fatalf("explicit false was not accepted: status=%d called=%t body=%s", response.Code, called, response.Body.String())
	}

	missing := testutil.PerformJSONRequest(
		t,
		router,
		http.MethodPut,
		"/jail/9/hardware/resource-limits",
		[]byte(`{}`),
	)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled should return 400, got %d body=%s", missing.Code, missing.Body.String())
	}
}

func TestJailHardwareErrorStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		want int
	}{
		{err: errors.New("invalid_memory"), want: http.StatusBadRequest},
		{err: errors.New("replication_lease_not_owned"), want: http.StatusForbidden},
		{err: errors.New("jail_not_found"), want: http.StatusNotFound},
		{err: errors.New("restore_in_progress"), want: http.StatusConflict},
		{err: errors.New("jail_hardware_hook_conflict"), want: http.StatusConflict},
		{err: errors.New("jail_dataset_mountpoint_not_usable"), want: http.StatusConflict},
		{err: errors.New("jail_service_not_initialized"), want: http.StatusServiceUnavailable},
		{err: errors.New("host_cpu_unavailable"), want: http.StatusServiceUnavailable},
		{err: errors.New("failed_to_write_post_start_hook"), want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		if got := jailHardwareErrorStatus(test.err); got != test.want {
			t.Fatalf("jailHardwareErrorStatus(%v) = %d, want %d", test.err, got, test.want)
		}
	}
}
