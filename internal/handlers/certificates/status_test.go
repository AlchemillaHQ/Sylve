// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package certificateHandlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/services/certificates"
	"github.com/gin-gonic/gin"
)

type certificateRenewalStub struct {
	certificateService
	item        *certificates.CertificateView
	err         error
	requestedID uint
}

func (s *certificateRenewalStub) RenewCertificate(_ context.Context, id uint) (*certificates.CertificateView, error) {
	s.requestedID = id
	return s.item, s.err
}

func TestCertificateErrorStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid", err: certificates.ErrInvalidCertificate, wantStatus: http.StatusBadRequest},
		{name: "not found", err: certificates.ErrCertificateNotFound, wantStatus: http.StatusNotFound},
		{name: "conflict", err: certificates.ErrCertificateConflict, wantStatus: http.StatusConflict},
		{name: "not renewable", err: certificates.ErrNotRenewable, wantStatus: http.StatusConflict},
		{name: "renewal not due", err: certificates.ErrRenewalNotDue, wantStatus: http.StatusConflict},
		{name: "issuance failed", err: certificates.ErrIssuanceFailed, wantStatus: http.StatusBadGateway},
		{name: "domain check failed", err: certificates.ErrDomainCheckFailed, wantStatus: http.StatusBadGateway},
		{name: "managed broker failed", err: certificates.ErrManagedBrokerRequestFailed, wantStatus: http.StatusBadGateway},
		{name: "internal", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			requestContext, _ := gin.CreateTestContext(response)
			certificateError(requestContext, "certificate_test_error", test.err)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

func TestRenewCertificateResponseReflectsExecutionMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name        string
		certificate models.CertificateType
		wantStatus  int
		wantMessage string
	}{
		{
			name:        "direct Let's Encrypt renewal",
			certificate: models.CertificateTypeLetsEncrypt,
			wantStatus:  http.StatusOK,
			wantMessage: "certificate_renewed",
		},
		{
			name:        "queued managed renewal",
			certificate: models.CertificateTypeSylveManaged,
			wantStatus:  http.StatusAccepted,
			wantMessage: "certificate_renewal_started",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &certificateRenewalStub{item: &certificates.CertificateView{
				ID:   42,
				Type: test.certificate,
			}}
			router := gin.New()
			router.POST("/certificates/:id/renew", Renew(service))

			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/certificates/42/renew", nil))
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if service.requestedID != 42 {
				t.Fatalf("requested certificate=%d want=42", service.requestedID)
			}
			var body internal.APIResponse[certificates.CertificateView]
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Message != test.wantMessage || body.Data.Type != test.certificate {
				t.Fatalf("unexpected response: %#v", body)
			}
		})
	}
}
