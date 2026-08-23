// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package diskHandlers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	"github.com/alchemillahq/sylve/internal/services/disk"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

type diskMutationHandlerStub struct {
	operation string
	device    string
	sizes     []uint64
	calls     int
	err       error
}

func (s *diskMutationHandlerStub) record(operation, device string, sizes []uint64) error {
	s.operation = operation
	s.device = device
	s.sizes = append([]uint64(nil), sizes...)
	s.calls++
	return s.err
}

func (s *diskMutationHandlerStub) DestroyPartitionTableContext(_ context.Context, device string) error {
	return s.record("clear", device, nil)
}

func (s *diskMutationHandlerStub) InitializeGPTContext(_ context.Context, device string) error {
	return s.record("initialize", device, nil)
}

func (s *diskMutationHandlerStub) CreatePartitionsContext(_ context.Context, device string, sizes []uint64) error {
	return s.record("create", device, sizes)
}

func (s *diskMutationHandlerStub) DeletePartitionContext(_ context.Context, partition string) error {
	return s.record("delete", partition, nil)
}

func newDiskMutationRouter(service diskMutationService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/disk")
	group.DELETE("/partitions/:partition", DeletePartition(service))
	group.POST("/:device/partition-table", InitializeGPT(service))
	group.DELETE("/:device/partition-table", ClearPartitionTable(service))
	group.POST("/:device/partitions", CreatePartitions(service))
	return router
}

func TestDiskMutationHandlers(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		path          string
		body          []byte
		wantStatus    int
		wantOperation string
		wantDevice    string
		wantSizes     []uint64
		wantMessage   string
	}{
		{name: "clear partition table", method: http.MethodDelete, path: "/disk/nda0/partition-table", wantStatus: http.StatusOK, wantOperation: "clear", wantDevice: "nda0", wantMessage: "partition_table_cleared"},
		{name: "initialize GPT", method: http.MethodPost, path: "/disk/nda0/partition-table", wantStatus: http.StatusOK, wantOperation: "initialize", wantDevice: "nda0", wantMessage: "gpt_initialized"},
		{name: "create partitions", method: http.MethodPost, path: "/disk/nda0/partitions", body: []byte(`{"sizes":[1048576,2097152]}`), wantStatus: http.StatusCreated, wantOperation: "create", wantDevice: "nda0", wantSizes: []uint64{1048576, 2097152}, wantMessage: "partitions_created"},
		{name: "delete partition", method: http.MethodDelete, path: "/disk/partitions/nda0p1", wantStatus: http.StatusOK, wantOperation: "delete", wantDevice: "nda0p1", wantMessage: "partition_deleted"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &diskMutationHandlerStub{}
			response := testutil.PerformJSONRequest(t, newDiskMutationRouter(service), tt.method, tt.path, tt.body)
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d; want %d; body = %s", response.Code, tt.wantStatus, response.Body.String())
			}
			if service.calls != 1 || service.operation != tt.wantOperation || service.device != tt.wantDevice || !reflect.DeepEqual(service.sizes, tt.wantSizes) {
				t.Fatalf("service call = operation %q device %q sizes %v calls %d", service.operation, service.device, service.sizes, service.calls)
			}
			body := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, response)
			if body.Status != "success" || body.Message != tt.wantMessage || body.Error != "" {
				t.Fatalf("unexpected response: %+v", body)
			}
		})
	}
}

func TestDiskMutationErrorStatus(t *testing.T) {
	tests := []struct {
		err        error
		wantStatus int
	}{
		{err: disk.ErrInvalidDiskRequest, wantStatus: http.StatusBadRequest},
		{err: disk.ErrDiskResourceNotFound, wantStatus: http.StatusNotFound},
		{err: disk.ErrDiskOperationConflict, wantStatus: http.StatusConflict},
		{err: errors.New("failure"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		service := &diskMutationHandlerStub{err: tt.err}
		response := testutil.PerformJSONRequest(t, newDiskMutationRouter(service), http.MethodDelete, "/disk/nda0/partition-table", nil)
		if response.Code != tt.wantStatus {
			t.Fatalf("error %v: status = %d; want %d; body = %s", tt.err, response.Code, tt.wantStatus, response.Body.String())
		}
	}
}

func TestCreatePartitionsRejectsInvalidPayload(t *testing.T) {
	tests := [][]byte{
		[]byte(`{}`),
		[]byte(`{"sizes":[]}`),
		[]byte(`{"sizes":[0]}`),
		[]byte(`{"sizes":[1048575]}`),
		[]byte(`{"sizes":`),
	}

	for _, body := range tests {
		service := &diskMutationHandlerStub{}
		response := testutil.PerformJSONRequest(t, newDiskMutationRouter(service), http.MethodPost, "/disk/nda0/partitions", body)
		if response.Code != http.StatusBadRequest || service.calls != 0 {
			t.Fatalf("payload %q: status = %d calls = %d body = %s", body, response.Code, service.calls, response.Body.String())
		}
	}
}

func TestDiskRequestBodyLimit(t *testing.T) {
	service := &diskMutationHandlerStub{}
	router := gin.New()
	router.Use(middleware.LimitRequestBody(16))
	router.POST("/disk/:device/partitions", CreatePartitions(service))

	request := httptest.NewRequest(http.MethodPost, "/disk/nda0/partitions", bytes.NewBufferString(`{"sizes":[1048576]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || service.calls != 0 {
		t.Fatalf("status = %d calls = %d body = %s", response.Code, service.calls, response.Body.String())
	}
}
