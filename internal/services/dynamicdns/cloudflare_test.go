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
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

type cloudflareRoundTripFunc func(*http.Request) (*http.Response, error)

func (f cloudflareRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestCloudflareValidateDiscoversZone(t *testing.T) {
	var zoneLookups []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("unexpected authorization header %q", request.Header.Get("Authorization"))
		}

		switch request.URL.Path {
		case "/user/tokens/verify":
			writeCloudflareResponse(t, w, map[string]any{
				"success": true,
				"result":  map[string]string{"status": "active"},
			})
		case "/zones":
			name := request.URL.Query().Get("name")
			zoneLookups = append(zoneLookups, name)
			result := []map[string]string{}
			if name == "example.com" {
				result = append(result, map[string]string{"id": "zone-id", "name": "example.com"})
			}
			writeCloudflareResponse(t, w, map[string]any{"success": true, "result": result})
		default:
			t.Fatalf("unexpected cloudflare request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	provider := &CloudflareProvider{BaseURL: server.URL, Client: server.Client()}
	settings, err := provider.Validate(context.Background(), "test-token", "router.example.com", "A", nil)
	if err != nil {
		t.Fatalf("validating cloudflare token failed: %v", err)
	}
	if settings["zoneId"] != "zone-id" || settings["zoneName"] != "example.com" {
		t.Fatalf("unexpected discovered zone settings: %#v", settings)
	}
	if strings.Join(zoneLookups, ",") != "router.example.com,example.com" {
		t.Fatalf("unexpected zone lookup order: %#v", zoneLookups)
	}
}

func TestCloudflareValidateClassifiesProviderResponses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantKind   providerErrorKind
	}{
		{
			name:       "invalid token",
			statusCode: http.StatusUnauthorized,
			body:       `{"success":false,"errors":[{"code":10000,"message":"authentication error"}]}`,
			wantKind:   providerErrorPermanent,
		},
		{
			name:       "API rejection",
			statusCode: http.StatusOK,
			body:       `{"success":false,"errors":[{"code":10000,"message":"authentication error"}]}`,
			wantKind:   providerErrorPermanent,
		},
		{
			name:       "rate limited",
			statusCode: http.StatusTooManyRequests,
			body:       `{"success":false,"errors":[{"code":1015,"message":"rate limited"}]}`,
			wantKind:   providerErrorTransient,
		},
		{
			name:       "server unavailable",
			statusCode: http.StatusServiceUnavailable,
			body:       `{"success":false,"errors":[{"code":1001,"message":"try later"}]}`,
			wantKind:   providerErrorTransient,
		},
		{
			name:       "malformed response",
			statusCode: http.StatusOK,
			body:       `{`,
			wantKind:   providerErrorTransient,
		},
		{
			name:       "missing success status",
			statusCode: http.StatusOK,
			body:       `{"result":{"status":"active"}}`,
			wantKind:   providerErrorTransient,
		},
		{
			name:       "missing token status",
			statusCode: http.StatusOK,
			body:       `{"success":true,"result":{}}`,
			wantKind:   providerErrorTransient,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.statusCode)
				if _, err := writer.Write([]byte(test.body)); err != nil {
					t.Fatalf("write Cloudflare response: %v", err)
				}
			}))
			defer server.Close()

			provider := &CloudflareProvider{BaseURL: server.URL, Client: server.Client()}
			_, err := provider.Validate(context.Background(), "test-token", "router.example.com", "A", nil)
			kind, _, ok := providerErrorDetails(err)
			if !ok || kind != test.wantKind {
				t.Fatalf("unexpected provider classification: err=%v kind=%v", err, kind)
			}
			if strings.Contains(err.Error(), "test-token") {
				t.Fatalf("provider error exposed token: %v", err)
			}
		})
	}
}

func TestCloudflareValidateTreatsMissingZoneIDAsTransient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/user/tokens/verify":
			writeCloudflareResponse(t, writer, map[string]any{
				"success": true,
				"result":  map[string]string{"status": "active"},
			})
		case "/zones":
			writeCloudflareResponse(t, writer, map[string]any{
				"success": true,
				"result": []map[string]string{{
					"name": request.URL.Query().Get("name"),
				}},
			})
		default:
			t.Fatalf("unexpected Cloudflare request path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	provider := &CloudflareProvider{BaseURL: server.URL, Client: server.Client()}
	_, err := provider.Validate(context.Background(), "test-token", "router.example.com", "A", nil)
	kind, _, ok := providerErrorDetails(err)
	if !ok || kind != providerErrorTransient {
		t.Fatalf("missing zone ID was not transient: %v", err)
	}
}

func TestCloudflareValidateClassifiesNetworkFailureAsTransient(t *testing.T) {
	provider := &CloudflareProvider{
		BaseURL: "https://api.cloudflare.test",
		Client: &http.Client{Transport: cloudflareRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network unavailable")
		})},
	}

	_, err := provider.Validate(context.Background(), "test-token", "router.example.com", "A", nil)
	kind, _, ok := providerErrorDetails(err)
	if !ok || kind != providerErrorTransient {
		t.Fatalf("network failure was not transient: %v", err)
	}
}

func TestCloudflareUpsertUpdatesEveryExactMatch(t *testing.T) {
	updated := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/zones/zone-id/dns_records":
			if request.URL.Query().Get("name") != "router.example.com" || request.URL.Query().Get("type") != "A" {
				t.Fatalf("unexpected record lookup query: %s", request.URL.RawQuery)
			}
			writeCloudflareResponse(t, w, map[string]any{
				"success": true,
				"result": []map[string]string{
					{"id": "one", "type": "A", "name": "router.example.com", "content": "198.51.100.1"},
					{"id": "two", "type": "A", "name": "router.example.com", "content": "198.51.100.2"},
				},
				"result_info": map[string]int{"page": 1, "total_pages": 1},
			})
		case request.Method == http.MethodPatch && strings.HasPrefix(request.URL.Path, "/zones/zone-id/dns_records/"):
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatalf("failed to decode patch payload: %v", err)
			}
			if len(payload) != 1 || payload["content"] != "203.0.113.9" {
				t.Fatalf("PATCH must update content only, got %#v", payload)
			}
			updated[strings.TrimPrefix(request.URL.Path, "/zones/zone-id/dns_records/")] = payload["content"].(string)
			writeCloudflareResponse(t, w, map[string]any{"success": true, "result": map[string]string{}})
		default:
			t.Fatalf("unexpected cloudflare request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	provider := &CloudflareProvider{BaseURL: server.URL, Client: server.Client()}
	if err := provider.Upsert(context.Background(), "test-token", map[string]string{"zoneId": "zone-id"}, "router.example.com", "A", netip.MustParseAddr("203.0.113.9")); err != nil {
		t.Fatalf("upserting cloudflare record failed: %v", err)
	}
	if len(updated) != 2 || updated["one"] != "203.0.113.9" || updated["two"] != "203.0.113.9" {
		t.Fatalf("expected every exact record match to update, got %#v", updated)
	}
}

func TestCloudflareUpsertCreatesMissingRecordWithSafeDefaults(t *testing.T) {
	created := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/zones/zone-id/dns_records":
			writeCloudflareResponse(t, w, map[string]any{
				"success":     true,
				"result":      []map[string]string{},
				"result_info": map[string]int{"page": 1, "total_pages": 1},
			})
		case request.Method == http.MethodPost && request.URL.Path == "/zones/zone-id/dns_records":
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatalf("failed to decode create payload: %v", err)
			}
			if payload["type"] != "AAAA" || payload["name"] != "router.example.com" || payload["content"] != "2001:db8::9" || payload["ttl"] != float64(1) || payload["proxied"] != false {
				t.Fatalf("unexpected create payload: %#v", payload)
			}
			created = true
			writeCloudflareResponse(t, w, map[string]any{"success": true, "result": map[string]string{}})
		default:
			t.Fatalf("unexpected cloudflare request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	provider := &CloudflareProvider{BaseURL: server.URL, Client: server.Client()}
	if err := provider.Upsert(context.Background(), "test-token", map[string]string{"zoneId": "zone-id"}, "router.example.com", "AAAA", netip.MustParseAddr("2001:db8::9")); err != nil {
		t.Fatalf("creating cloudflare record failed: %v", err)
	}
	if !created {
		t.Fatal("expected a missing cloudflare record to be created")
	}
}

func writeCloudflareResponse(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Fatalf("failed to encode cloudflare response: %v", err)
	}
}
