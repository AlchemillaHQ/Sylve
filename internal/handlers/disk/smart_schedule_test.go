// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
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
	"testing"

	"github.com/alchemillahq/sylve/internal/services/disk"
	"github.com/alchemillahq/sylve/pkg/disk/smart"
	"github.com/gin-gonic/gin"
)

type selfTestScheduleHandlerStub struct {
	schedules []disk.SelfTestScheduleView
	input     disk.SelfTestScheduleInput
	id        uint
	err       error
}

func (s *selfTestScheduleHandlerStub) ListSelfTestSchedules(context.Context) ([]disk.SelfTestScheduleView, error) {
	return s.schedules, s.err
}

func (s *selfTestScheduleHandlerStub) CreateSelfTestSchedule(_ context.Context, input disk.SelfTestScheduleInput) (*disk.SelfTestScheduleView, error) {
	s.input = input
	if s.err != nil {
		return nil, s.err
	}
	view := disk.SelfTestScheduleView{ID: 1, Device: input.Device, TestType: input.TestType, CronExpr: input.CronExpr, Enabled: input.Enabled}
	return &view, nil
}

func (s *selfTestScheduleHandlerStub) UpdateSelfTestSchedule(_ context.Context, id uint, input disk.SelfTestScheduleInput) (*disk.SelfTestScheduleView, error) {
	s.id = id
	s.input = input
	if s.err != nil {
		return nil, s.err
	}
	view := disk.SelfTestScheduleView{ID: id, Device: input.Device, TestType: input.TestType, CronExpr: input.CronExpr, Enabled: input.Enabled}
	return &view, nil
}

func (s *selfTestScheduleHandlerStub) DeleteSelfTestSchedule(_ context.Context, id uint) error {
	s.id = id
	return s.err
}

func TestSelfTestScheduleHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &selfTestScheduleHandlerStub{
		schedules: []disk.SelfTestScheduleView{{ID: 1, Device: "ada0", TestType: "short"}},
	}
	router := gin.New()
	router.GET("/disk/smart/self-test/schedules", ListSelfTestSchedules(service))
	router.POST("/disk/smart/self-test/schedules", CreateSelfTestSchedule(service))
	router.PUT("/disk/smart/self-test/schedules/:id", UpdateSelfTestSchedule(service))
	router.DELETE("/disk/smart/self-test/schedules/:id", DeleteSelfTestSchedule(service))

	tests := []struct {
		method string
		path   string
		body   string
		status int
	}{
		{method: http.MethodGet, path: "/disk/smart/self-test/schedules", status: http.StatusOK},
		{method: http.MethodPost, path: "/disk/smart/self-test/schedules", body: `{"device":"ada0","testType":"short","cronExpr":"0 2 * * *","enabled":true}`, status: http.StatusCreated},
		{method: http.MethodPut, path: "/disk/smart/self-test/schedules/7", body: `{"device":"ada0","testType":"extended","cronExpr":"0 3 * * 0","enabled":false}`, status: http.StatusOK},
		{method: http.MethodDelete, path: "/disk/smart/self-test/schedules/7", status: http.StatusOK},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, bytes.NewBufferString(test.body))
		if test.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s %s: got %d: %s", test.method, test.path, response.Code, response.Body.String())
		}
	}
	if service.id != 7 {
		t.Fatalf("id: %d", service.id)
	}
}

func TestSelfTestScheduleHandlerErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		err    error
		status int
	}{
		{err: disk.ErrInvalidSelfTestSchedule, status: http.StatusBadRequest},
		{err: disk.ErrSelfTestScheduleNotFound, status: http.StatusNotFound},
		{err: disk.ErrSelfTestScheduleRunning, status: http.StatusConflict},
		{err: disk.ErrSelfTestSchedulerBusy, status: http.StatusConflict},
		{err: smart.ErrControllerTimeout, status: http.StatusServiceUnavailable},
		{err: errors.New("failure"), status: http.StatusInternalServerError},
	}
	for _, test := range tests {
		service := &selfTestScheduleHandlerStub{err: test.err}
		router := gin.New()
		router.GET("/disk/smart/self-test/schedules", ListSelfTestSchedules(service))
		request := httptest.NewRequest(http.MethodGet, "/disk/smart/self-test/schedules", nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%v: got %d, want %d", test.err, response.Code, test.status)
		}
	}
}
