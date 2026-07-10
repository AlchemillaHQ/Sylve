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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	diskService "github.com/alchemillahq/sylve/internal/services/disk"
	"github.com/alchemillahq/sylve/pkg/disk/smart"

	"github.com/gin-gonic/gin"
)

type diskListHandlerStub struct {
	fullCalls int
	noneCalls int
}

func (s *diskListHandlerStub) GetDiskDevices(context.Context) ([]diskServiceInterfaces.Disk, error) {
	s.fullCalls++
	return []diskServiceInterfaces.Disk{{Device: "ada0", SmartData: diskServiceInterfaces.SmartData{HealthKnown: true, Passed: true}}}, nil
}

func (s *diskListHandlerStub) GetDiskDevicesWithoutSMART(context.Context) ([]diskServiceInterfaces.Disk, error) {
	s.noneCalls++
	return []diskServiceInterfaces.Disk{{Device: "ada0"}}, nil
}

type selfTestHandlerStub struct {
	getDevice   string
	startDevice string
	startType   string
	stopDevice  string
	getErr      error
	startErr    error
	stopErr     error
}

func handlerSelfTestInfo() *diskServiceInterfaces.DiskSelfTestInfo {
	return &diskServiceInterfaces.DiskSelfTestInfo{
		Device: "ada0",
		Capabilities: diskServiceInterfaces.DiskSelfTestCapabilities{
			Protocol:  "ATA",
			Supported: true,
			Short:     true,
			Extended:  true,
			Abort:     true,
		},
		Status: diskServiceInterfaces.DiskSelfTestState{
			Protocol:       "ATA",
			State:          "running",
			Type:           "short",
			Running:        true,
			ProgressPct:    10,
			ProgressKnown:  true,
			RemainingPct:   90,
			RemainingKnown: true,
			Results:        []diskServiceInterfaces.DiskSelfTestResult{},
		},
	}
}

func (s *selfTestHandlerStub) GetSelfTestInfo(device string) (*diskServiceInterfaces.DiskSelfTestInfo, error) {
	s.getDevice = device
	return handlerSelfTestInfo(), s.getErr
}

func (s *selfTestHandlerStub) StartSelfTestContext(_ context.Context, device, testType string) (*diskServiceInterfaces.DiskSelfTestInfo, error) {
	s.startDevice = device
	s.startType = testType
	return handlerSelfTestInfo(), s.startErr
}

func (s *selfTestHandlerStub) StopSelfTest(device string) (*diskServiceInterfaces.DiskSelfTestInfo, error) {
	s.stopDevice = device
	return handlerSelfTestInfo(), s.stopErr
}

func TestListSmartNoneUsesInventoryPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &diskListHandlerStub{}
	router := gin.New()
	router.GET("/disk/list", List(service))

	request := httptest.NewRequest(http.MethodGet, "/disk/list?smart=none", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.noneCalls != 1 || service.fullCalls != 0 {
		t.Fatalf("status=%d full=%d none=%d body=%s", response.Code, service.fullCalls, service.noneCalls, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/disk/list", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.noneCalls != 1 || service.fullCalls != 1 {
		t.Fatalf("status=%d full=%d none=%d body=%s", response.Code, service.fullCalls, service.noneCalls, response.Body.String())
	}
}

func TestSelfTestHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &selfTestHandlerStub{}
	router := gin.New()
	router.GET("/disk/smart/self-test", GetSelfTestInfo(service))
	router.POST("/disk/smart/self-test", StartSelfTest(service))
	router.POST("/disk/smart/self-test/abort", StopSelfTest(service))

	request := httptest.NewRequest(http.MethodGet, "/disk/smart/self-test?device=ada0", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.getDevice != "ada0" {
		t.Fatalf("status=%d device=%q body=%s", response.Code, service.getDevice, response.Body.String())
	}
	var decoded struct {
		Data diskServiceInterfaces.DiskSelfTestInfo `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Data.Device != "ada0" || !decoded.Data.Capabilities.Short || !decoded.Data.Status.ProgressKnown {
		t.Fatalf("data=%+v", decoded.Data)
	}

	request = httptest.NewRequest(http.MethodPost, "/disk/smart/self-test", bytes.NewBufferString(`{"device":"ada0","testType":"short"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || service.startDevice != "ada0" || service.startType != "short" {
		t.Fatalf("status=%d device=%q type=%q body=%s", response.Code, service.startDevice, service.startType, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/disk/smart/self-test/abort", bytes.NewBufferString(`{"device":"ada0"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.stopDevice != "ada0" {
		t.Fatalf("status=%d device=%q body=%s", response.Code, service.stopDevice, response.Body.String())
	}
}

func TestSelfTestHandlerValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &selfTestHandlerStub{}
	router := gin.New()
	router.POST("/disk/smart/self-test", StartSelfTest(service))
	router.POST("/disk/smart/self-test/abort", StopSelfTest(service))

	for _, path := range []string{"/disk/smart/self-test", "/disk/smart/self-test/abort"} {
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestSelfTestHTTPError(t *testing.T) {
	tests := []struct {
		err    error
		status int
	}{
		{diskService.ErrInvalidPhysicalDisk, http.StatusBadRequest},
		{diskService.ErrSelfTestTypeNotAllowed, http.StatusBadRequest},
		{diskService.ErrPhysicalDiskNotFound, http.StatusNotFound},
		{smart.ErrSelfTestInProgress, http.StatusConflict},
		{diskService.ErrSelfTestNotRunning, http.StatusConflict},
		{diskService.ErrSelfTestSchedulerBusy, http.StatusConflict},
		{smart.ErrUnsupportedFeature, http.StatusUnprocessableEntity},
		{fmt.Errorf("%w: ada0", smart.ErrControllerTimeout), http.StatusServiceUnavailable},
		{errors.New("failed"), http.StatusInternalServerError},
	}
	for _, test := range tests {
		status, _ := selfTestHTTPError(test.err)
		if status != test.status {
			t.Fatalf("err=%v status=%d want=%d", test.err, status, test.status)
		}
	}
}
