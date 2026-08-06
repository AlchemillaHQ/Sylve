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
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/services/certificates"
	"github.com/gin-gonic/gin"
)

type certificateArchiveStub struct {
	archive     []byte
	err         error
	requestedID uint
}

func TestBindCertificateInputRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	request := httptest.NewRequest(http.MethodPost, "/certificates", strings.NewReader(`{"name":"`+strings.Repeat("x", 64)+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Body = http.MaxBytesReader(response, request.Body, 32)
	context.Request = request

	var input certificates.CertificateInput
	if bindCertificateInput(context, &input) {
		t.Fatal("expected oversized body to be rejected")
	}
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusRequestEntityTooLarge, response.Body.String())
	}
}

func (s *certificateArchiveStub) ExportCertificateArchive(_ context.Context, id uint) ([]byte, error) {
	s.requestedID = id
	return s.archive, s.err
}

func TestDownloadCertificateArchive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &certificateArchiveStub{archive: []byte("zip-data")}
	router := gin.New()
	router.GET("/certificates/:id/archive", Download(service))

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/certificates/42/archive", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d", response.Code, http.StatusOK)
	}
	if service.requestedID != 42 {
		t.Fatalf("requested certificate=%d want=42", service.requestedID)
	}
	if response.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("content type=%q", response.Header().Get("Content-Type"))
	}
	if response.Header().Get("Content-Disposition") != `attachment; filename="sylve-certificate-42.zip"` {
		t.Fatalf("content disposition=%q", response.Header().Get("Content-Disposition"))
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache control=%q", response.Header().Get("Cache-Control"))
	}
	if response.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("pragma=%q", response.Header().Get("Pragma"))
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("content type options=%q", response.Header().Get("X-Content-Type-Options"))
	}
	if response.Body.String() != "zip-data" {
		t.Fatalf("body=%q", response.Body.String())
	}
}

func TestDownloadCertificateArchiveErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		id         string
		err        error
		wantStatus int
	}{
		{name: "invalid id", id: "invalid", wantStatus: http.StatusBadRequest},
		{name: "not found", id: "7", err: certificates.ErrCertificateNotFound, wantStatus: http.StatusNotFound},
		{name: "not ready", id: "7", err: certificates.ErrCertificateConflict, wantStatus: http.StatusConflict},
		{name: "service failure", id: "7", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &certificateArchiveStub{err: test.err}
			router := gin.New()
			router.GET("/certificates/:id/archive", Download(service))

			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/certificates/"+test.id+"/archive", nil))
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if response.Header().Get("Content-Disposition") != "" {
				t.Fatalf("error response included content disposition %q", response.Header().Get("Content-Disposition"))
			}
		})
	}
}
