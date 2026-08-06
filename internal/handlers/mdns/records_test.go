// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package mdnsHandlers

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	mdnsModels "github.com/alchemillahq/sylve/internal/db/models/mdns"
	mdnsInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/mdns"
	mdnsService "github.com/alchemillahq/sylve/internal/services/mdns"
	"github.com/alchemillahq/sylve/internal/testutil"

	"github.com/gin-gonic/gin"
)

type mdnsHandlerTestService struct {
	createdRecord mdnsModels.MdnsRecord
	createErr     error
	updateErr     error
	deleteErr     error
}

func (s *mdnsHandlerTestService) Rebuild() error {
	return nil
}

func (s *mdnsHandlerTestService) GetSettings() (mdnsModels.MdnsSettings, error) {
	return mdnsModels.MdnsSettings{}, nil
}

func (s *mdnsHandlerTestService) SetSettings(string, string) error {
	return nil
}

func (s *mdnsHandlerTestService) GetRecords() ([]mdnsInterfaces.MdnsRecordWithManaged, error) {
	return nil, nil
}

func (s *mdnsHandlerTestService) CreateRecord(string, string, int, map[string]string, string) (mdnsModels.MdnsRecord, error) {
	return s.createdRecord, s.createErr
}

func (s *mdnsHandlerTestService) UpdateRecord(uint, string, string, int, map[string]string, string) error {
	return s.updateErr
}

func (s *mdnsHandlerTestService) DeleteRecord(uint) error {
	return s.deleteErr
}

func newMdnsRecordsTestRouter(service mdnsInterfaces.MdnsServiceInterface) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/mdns/records", CreateRecord(service))
	router.PUT("/mdns/records/:id", UpdateRecord(service))
	router.DELETE("/mdns/records/:id", DeleteRecord(service))
	return router
}

var validMdnsRecordRequest = []byte(`{
	"name":"printer",
	"type":"_ipp._tcp",
	"port":631,
	"txt":{},
	"interfaces":""
}`)

func TestCreateRecordReturnsCreated(t *testing.T) {
	service := &mdnsHandlerTestService{
		createdRecord: mdnsModels.MdnsRecord{
			ID:   7,
			Name: "printer",
			Type: "_ipp._tcp",
			Port: 631,
		},
	}
	router := newMdnsRecordsTestRouter(service)

	response := testutil.PerformJSONRequest(t, router, http.MethodPost, "/mdns/records", validMdnsRecordRequest)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d body=%s", response.Code, response.Body.String())
	}

	body := testutil.DecodeJSONResponse[internal.APIResponse[mdnsModels.MdnsRecord]](t, response)
	if body.Data.ID != service.createdRecord.ID || body.Message != "mdns_record_created" {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestCreateRecordMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid record", err: fmt.Errorf("details: %w", mdnsService.ErrInvalidRecord), wantStatus: http.StatusBadRequest},
		{name: "conflicting record", err: fmt.Errorf("details: %w", mdnsService.ErrRecordConflict), wantStatus: http.StatusConflict},
		{name: "internal error", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := newMdnsRecordsTestRouter(&mdnsHandlerTestService{createErr: test.err})
			response := testutil.PerformJSONRequest(t, router, http.MethodPost, "/mdns/records", validMdnsRecordRequest)
			if response.Code != test.wantStatus {
				t.Fatalf("expected status %d, got %d body=%s", test.wantStatus, response.Code, response.Body.String())
			}
		})
	}
}

func TestUpdateRecordMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid record", err: fmt.Errorf("details: %w", mdnsService.ErrInvalidRecord), wantStatus: http.StatusBadRequest},
		{name: "missing record", err: fmt.Errorf("details: %w", mdnsService.ErrRecordNotFound), wantStatus: http.StatusNotFound},
		{name: "conflicting record", err: fmt.Errorf("details: %w", mdnsService.ErrRecordConflict), wantStatus: http.StatusConflict},
		{name: "internal error", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := newMdnsRecordsTestRouter(&mdnsHandlerTestService{updateErr: test.err})
			response := testutil.PerformJSONRequest(t, router, http.MethodPut, "/mdns/records/7", validMdnsRecordRequest)
			if response.Code != test.wantStatus {
				t.Fatalf("expected status %d, got %d body=%s", test.wantStatus, response.Code, response.Body.String())
			}
		})
	}
}

func TestDeleteRecordMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "missing record", err: fmt.Errorf("details: %w", mdnsService.ErrRecordNotFound), wantStatus: http.StatusNotFound},
		{name: "internal error", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := newMdnsRecordsTestRouter(&mdnsHandlerTestService{deleteErr: test.err})
			response := testutil.PerformJSONRequest(t, router, http.MethodDelete, "/mdns/records/7", nil)
			if response.Code != test.wantStatus {
				t.Fatalf("expected status %d, got %d body=%s", test.wantStatus, response.Code, response.Body.String())
			}
		})
	}
}

func TestRecordHandlersRejectInvalidIDs(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   []byte
	}{
		{name: "update non-numeric", method: http.MethodPut, path: "/mdns/records/not-a-number", body: validMdnsRecordRequest},
		{name: "update zero", method: http.MethodPut, path: "/mdns/records/0", body: validMdnsRecordRequest},
		{name: "delete negative", method: http.MethodDelete, path: "/mdns/records/-1"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := newMdnsRecordsTestRouter(&mdnsHandlerTestService{})
			response := testutil.PerformJSONRequest(t, router, test.method, test.path, test.body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
