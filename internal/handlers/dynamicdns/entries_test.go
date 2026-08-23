// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package dynamicDNSHandlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	"github.com/alchemillahq/sylve/internal/services/dynamicdns"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

type dynamicDNSHandlerTestService struct {
	createErr   error
	updateErr   error
	createCalls int
	updateCalls int
}

func (*dynamicDNSHandlerTestService) ListEntries(context.Context) ([]dynamicdns.EntryView, error) {
	return nil, nil
}

func (s *dynamicDNSHandlerTestService) CreateEntry(context.Context, dynamicdns.EntryInput) (*dynamicdns.EntryView, error) {
	s.createCalls++
	return nil, s.createErr
}

func (s *dynamicDNSHandlerTestService) UpdateEntry(context.Context, uint, dynamicdns.EntryInput) (*dynamicdns.EntryView, error) {
	s.updateCalls++
	return nil, s.updateErr
}

func (*dynamicDNSHandlerTestService) DeleteEntry(context.Context, uint) error {
	return nil
}

func (*dynamicDNSHandlerTestService) SyncEntry(context.Context, uint) (*dynamicdns.EntryView, error) {
	return nil, nil
}

func TestEntryMutationsMapServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		message    string
		wantStatus int
		wantError  error
		register   func(*gin.Engine, dynamicDNSService)
		service    *dynamicDNSHandlerTestService
	}{
		{
			name:       "create conflict",
			method:     http.MethodPost,
			path:       "/dynamic-dns/entries",
			message:    "error_creating_dynamic_dns_entry",
			wantStatus: http.StatusConflict,
			wantError:  dynamicdns.ErrEntryConflict,
			register: func(router *gin.Engine, service dynamicDNSService) {
				router.POST("/dynamic-dns/entries", CreateEntry(service))
			},
			service: &dynamicDNSHandlerTestService{
				createErr: fmt.Errorf("target unavailable: %w", dynamicdns.ErrEntryConflict),
			},
		},
		{
			name:       "update conflict",
			method:     http.MethodPut,
			path:       "/dynamic-dns/entries/7",
			message:    "error_updating_dynamic_dns_entry",
			wantStatus: http.StatusConflict,
			wantError:  dynamicdns.ErrEntryConflict,
			register: func(router *gin.Engine, service dynamicDNSService) {
				router.PUT("/dynamic-dns/entries/:id", UpdateEntry(service))
			},
			service: &dynamicDNSHandlerTestService{
				updateErr: fmt.Errorf("target unavailable: %w", dynamicdns.ErrEntryConflict),
			},
		},
		{
			name:       "invalid provider configuration",
			method:     http.MethodPost,
			path:       "/dynamic-dns/entries",
			message:    "error_creating_dynamic_dns_entry",
			wantStatus: http.StatusBadRequest,
			wantError:  dynamicdns.ErrInvalidEntry,
			register: func(router *gin.Engine, service dynamicDNSService) {
				router.POST("/dynamic-dns/entries", CreateEntry(service))
			},
			service: &dynamicDNSHandlerTestService{
				createErr: fmt.Errorf("invalid credential: %w", dynamicdns.ErrInvalidEntry),
			},
		},
		{
			name:       "create provider unavailable",
			method:     http.MethodPost,
			path:       "/dynamic-dns/entries",
			message:    "error_creating_dynamic_dns_entry",
			wantStatus: http.StatusBadGateway,
			wantError:  dynamicdns.ErrProviderUnavailable,
			register: func(router *gin.Engine, service dynamicDNSService) {
				router.POST("/dynamic-dns/entries", CreateEntry(service))
			},
			service: &dynamicDNSHandlerTestService{
				createErr: fmt.Errorf("validation unavailable: %w", dynamicdns.ErrProviderUnavailable),
			},
		},
		{
			name:       "update provider unavailable",
			method:     http.MethodPut,
			path:       "/dynamic-dns/entries/7",
			message:    "error_updating_dynamic_dns_entry",
			wantStatus: http.StatusBadGateway,
			wantError:  dynamicdns.ErrProviderUnavailable,
			register: func(router *gin.Engine, service dynamicDNSService) {
				router.PUT("/dynamic-dns/entries/:id", UpdateEntry(service))
			},
			service: &dynamicDNSHandlerTestService{
				updateErr: fmt.Errorf("validation unavailable: %w", dynamicdns.ErrProviderUnavailable),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			router := gin.New()
			test.register(router, test.service)

			response := testutil.PerformJSONRequest(t, router, test.method, test.path, []byte(`{}`))
			if response.Code != test.wantStatus {
				t.Fatalf("expected status %d, got %d body=%s", test.wantStatus, response.Code, response.Body.String())
			}

			body := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, response)
			if body.Message != test.message || !strings.Contains(body.Error, test.wantError.Error()) {
				t.Fatalf("unexpected conflict response: %+v", body)
			}
		})
	}
}

func TestEntryMutationsRejectOversizedBodiesThroughAuditMiddleware(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		path      string
		register  func(*gin.Engine, dynamicDNSService)
		callCount func(*dynamicDNSHandlerTestService) int
	}{
		{
			name:   "create",
			method: http.MethodPost,
			path:   "/api/dynamic-dns/entries",
			register: func(router *gin.Engine, service dynamicDNSService) {
				router.POST("/api/dynamic-dns/entries", CreateEntry(service))
			},
			callCount: func(service *dynamicDNSHandlerTestService) int { return service.createCalls },
		},
		{
			name:   "update",
			method: http.MethodPut,
			path:   "/api/dynamic-dns/entries/7",
			register: func(router *gin.Engine, service dynamicDNSService) {
				router.PUT("/api/dynamic-dns/entries/:id", UpdateEntry(service))
			},
			callCount: func(service *dynamicDNSHandlerTestService) int { return service.updateCalls },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			auditDB := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})
			service := &dynamicDNSHandlerTestService{}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set("UserID", uint(1))
				c.Set("Username", "admin")
				c.Set("AuthType", "sylve")
				c.Next()
			})
			router.Use(middleware.LimitRequestBody(dynamicdns.MaxRequestBodyBytes))
			router.Use(middleware.RequestLoggerMiddleware(auditDB, nil))
			test.register(router, service)

			payload := []byte(`{"hostname":"` + strings.Repeat("x", int(dynamicdns.MaxRequestBodyBytes)) + `"}`)
			response := testutil.PerformJSONRequest(t, router, test.method, test.path, payload)
			if response.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("expected status 413, got %d body=%s", response.Code, response.Body.String())
			}
			if calls := test.callCount(service); calls != 0 {
				t.Fatalf("service called %d times for oversized body", calls)
			}

			body := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, response)
			if body.Message != "dynamic_dns_request_too_large" || body.Error != "dynamic DNS request body is too large" {
				t.Fatalf("unexpected oversized response: %+v", body)
			}
		})
	}
}
