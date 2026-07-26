// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package dynamicdns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	dynamicDNSModels "github.com/alchemillahq/sylve/internal/db/models/dynamicdns"
)

func TestSylveValidateChecksTokenOwnership(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/ddns/status" {
			t.Fatalf("unexpected Sylve.app request %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("unexpected authorization header %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("Accept") != "application/json" {
			t.Fatalf("unexpected accept header %q", request.Header.Get("Accept"))
		}
		writeSylveResponse(t, writer, http.StatusOK, map[string]any{
			"hostname": "ARES.SYLVE.APP.",
			"ipv4":     "203.0.113.42",
			"ipv6":     "2001:db8::42",
			"paused":   false,
		})
	}))
	defer server.Close()

	provider := &SylveProvider{BaseURL: server.URL, Client: server.Client()}
	settings, err := provider.Validate(context.Background(), "test-token", "ares.sylve.app", dynamicDNSModels.RecordTypeBoth, map[string]string{"discard": "me"})
	if err != nil {
		t.Fatalf("validating Sylve.app token failed: %v", err)
	}
	if len(settings) != 0 {
		t.Fatalf("Sylve.app validation persisted unexpected settings: %#v", settings)
	}
}

func TestSylveValidateRejectsMismatchedOrPausedHostname(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		paused   bool
		want     string
	}{
		{name: "mismatched hostname", hostname: "other.sylve.app", want: "belongs to"},
		{name: "paused hostname", hostname: "ares.sylve.app", paused: true, want: "is paused"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeSylveResponse(t, writer, http.StatusOK, map[string]any{
					"hostname": test.hostname,
					"ipv4":     "203.0.113.42",
					"ipv6":     "",
					"paused":   test.paused,
				})
			}))
			defer server.Close()

			provider := &SylveProvider{BaseURL: server.URL, Client: server.Client()}
			_, err := provider.Validate(context.Background(), "test-token", "ares.sylve.app", dynamicDNSModels.RecordTypeA, nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q validation error, got %v", test.want, err)
			}
		})
	}
}

func TestSylveAddressMatchesStatus(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.Method != http.MethodGet || request.URL.Path != "/api/ddns/status" {
			t.Fatalf("unexpected Sylve.app request %s %s", request.Method, request.URL.Path)
		}
		writeSylveResponse(t, writer, http.StatusOK, map[string]any{
			"hostname": "ares.sylve.app",
			"ipv4":     "203.0.113.42",
			"ipv6":     "2001:0db8:0:0:0:0:0:42",
			"paused":   false,
		})
	}))
	defer server.Close()

	provider := &SylveProvider{BaseURL: server.URL, Client: server.Client()}
	matches, err := provider.AddressMatches(context.Background(), "test-token", nil, "ares.sylve.app", dynamicDNSModels.RecordTypeAAAA, netip.MustParseAddr("2001:db8::42"))
	if err != nil {
		t.Fatalf("checking Sylve.app address failed: %v", err)
	}
	if !matches || requests != 1 {
		t.Fatalf("expected canonical IPv6 address to match, matches=%v requests=%d", matches, requests)
	}
}

func TestSylveUpsertSendsOneAddressFamily(t *testing.T) {
	tests := []struct {
		name       string
		recordType string
		address    string
		field      string
	}{
		{name: "IPv4", recordType: dynamicDNSModels.RecordTypeA, address: "203.0.113.42", field: "ipv4"},
		{name: "IPv6", recordType: dynamicDNSModels.RecordTypeAAAA, address: "2001:db8::42", field: "ipv6"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.Path != "/api/ddns/update" {
					t.Fatalf("unexpected Sylve.app request %s %s", request.Method, request.URL.Path)
				}
				if request.Header.Get("Authorization") != "Bearer test-token" {
					t.Fatalf("unexpected authorization header %q", request.Header.Get("Authorization"))
				}
				if request.Header.Get("Content-Type") != "application/json" {
					t.Fatalf("unexpected content type %q", request.Header.Get("Content-Type"))
				}

				var payload map[string]string
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Fatalf("failed to decode Sylve.app update: %v", err)
				}
				if len(payload) != 1 || payload[test.field] != test.address {
					t.Fatalf("unexpected Sylve.app payload: %#v", payload)
				}
				writeSylveResponse(t, writer, http.StatusOK, map[string]any{
					"hostname": "ares.sylve.app",
					"changed":  false,
				})
			}))
			defer server.Close()

			provider := &SylveProvider{BaseURL: server.URL, Client: server.Client()}
			err := provider.Upsert(context.Background(), "test-token", nil, "ares.sylve.app", test.recordType, netip.MustParseAddr(test.address))
			if err != nil {
				t.Fatalf("updating Sylve.app %s record failed: %v", test.recordType, err)
			}
		})
	}
}

func TestSylveClassifiesHTTPFailures(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       any
		retryAfter string
		wantKind   providerErrorKind
		wantDelay  time.Duration
	}{
		{name: "bad request", statusCode: http.StatusBadRequest, body: map[string]string{"error": "invalid IP"}, wantKind: providerErrorPermanent},
		{name: "invalid token", statusCode: http.StatusUnauthorized, body: map[string]string{"error": "invalid token"}, wantKind: providerErrorPermanent},
		{name: "missing hostname", statusCode: http.StatusNotFound, body: map[string]string{"error": "not found"}, wantKind: providerErrorPermanent},
		{name: "wrong method", statusCode: http.StatusMethodNotAllowed, body: map[string]string{"error": "wrong method"}, wantKind: providerErrorPermanent},
		{name: "request timeout", statusCode: http.StatusRequestTimeout, body: map[string]string{"error": "proxy timeout"}, wantKind: providerErrorTransient},
		{name: "rate limited", statusCode: http.StatusTooManyRequests, body: map[string]string{"error": "slow down"}, retryAfter: "10", wantKind: providerErrorTransient, wantDelay: 10 * time.Second},
		{name: "server error", statusCode: http.StatusInternalServerError, body: map[string]string{"error": "unexpected"}, wantKind: providerErrorTransient},
		{name: "bad gateway", statusCode: http.StatusBadGateway, body: map[string]string{"error": "upstream unavailable"}, wantKind: providerErrorTransient},
		{name: "unavailable", statusCode: http.StatusServiceUnavailable, body: map[string]string{"error": "try later"}, wantKind: providerErrorTransient},
		{name: "publication delayed", statusCode: http.StatusServiceUnavailable, body: map[string]string{"message": "Record saved but DNS publication is delayed"}, wantKind: providerErrorPending},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				if test.retryAfter != "" {
					writer.Header().Set("Retry-After", test.retryAfter)
				}
				writeSylveResponse(t, writer, test.statusCode, test.body)
			}))
			defer server.Close()

			provider := &SylveProvider{BaseURL: server.URL, Client: server.Client()}
			err := provider.Upsert(context.Background(), "test-token", nil, "ares.sylve.app", dynamicDNSModels.RecordTypeA, netip.MustParseAddr("203.0.113.42"))
			kind, delay, ok := providerErrorDetails(err)
			if !ok || kind != test.wantKind || delay != test.wantDelay {
				t.Fatalf("unexpected provider error classification: err=%v kind=%v delay=%s", err, kind, delay)
			}
			if strings.Contains(err.Error(), "test-token") {
				t.Fatalf("provider error exposed token: %v", err)
			}
		})
	}
}

func TestSylveTreatsMalformedStatusAsTransient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeSylveResponse(t, writer, http.StatusOK, map[string]any{
			"hostname": "ares.sylve.app",
			"ipv4":     "not-an-address",
			"ipv6":     "",
			"paused":   false,
		})
	}))
	defer server.Close()

	provider := &SylveProvider{BaseURL: server.URL, Client: server.Client()}
	_, err := provider.AddressMatches(context.Background(), "test-token", nil, "ares.sylve.app", dynamicDNSModels.RecordTypeA, netip.MustParseAddr("203.0.113.42"))
	kind, _, ok := providerErrorDetails(err)
	if !ok || kind != providerErrorTransient {
		t.Fatalf("malformed status was not transient: %v", err)
	}
}

func TestSylveRequiresPausedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeSylveResponse(t, writer, http.StatusOK, map[string]any{
			"hostname": "ares.sylve.app",
			"ipv4":     "203.0.113.42",
			"ipv6":     "",
		})
	}))
	defer server.Close()

	provider := &SylveProvider{BaseURL: server.URL, Client: server.Client()}
	_, err := provider.AddressMatches(context.Background(), "test-token", nil, "ares.sylve.app", dynamicDNSModels.RecordTypeA, netip.MustParseAddr("203.0.113.42"))
	kind, _, ok := providerErrorDetails(err)
	if !ok || kind != providerErrorTransient || !strings.Contains(err.Error(), "paused state") {
		t.Fatalf("missing paused state was not rejected as transient: %v", err)
	}
}

func TestSylveParsesRetryAfterHTTPDate(t *testing.T) {
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	provider := &SylveProvider{now: func() time.Time { return now }}
	delay := provider.retryAfter(now.Add(3 * time.Minute).Format(http.TimeFormat))
	if delay != 3*time.Minute {
		t.Fatalf("unexpected Retry-After date delay: %s", delay)
	}
}

func writeSylveResponse(t *testing.T, writer http.ResponseWriter, statusCode int, value any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Fatalf("failed to encode Sylve.app response: %v", err)
	}
}
