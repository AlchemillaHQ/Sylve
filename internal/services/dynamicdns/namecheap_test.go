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
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	dynamicDNSModels "github.com/alchemillahq/sylve/internal/db/models/dynamicdns"
)

func TestNamecheapValidateDerivesHost(t *testing.T) {
	provider := NewNamecheapProvider()
	settings, err := provider.Validate(context.Background(), "test-password", "router.example.co.uk", dynamicDNSModels.RecordTypeA, map[string]string{
		NamecheapSettingDomain: "Example.Co.Uk",
	})
	if err != nil {
		t.Fatalf("validating namecheap entry failed: %v", err)
	}
	if settings[NamecheapSettingDomain] != "Example.Co.Uk" || settings[NamecheapSettingHost] != "router" {
		t.Fatalf("unexpected namecheap settings: %#v", settings)
	}
}

func TestNamecheapValidateRejectsUnsupportedRecordType(t *testing.T) {
	provider := NewNamecheapProvider()
	_, err := provider.Validate(context.Background(), "test-password", "router.example.com", dynamicDNSModels.RecordTypeAAAA, map[string]string{
		NamecheapSettingDomain: "example.com",
	})
	if err == nil || !strings.Contains(err.Error(), "A records only") {
		t.Fatalf("expected an unsupported record type error, got %v", err)
	}
}

func TestNamecheapUpsertUsesDynamicDNSUpdateEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/update" {
			t.Fatalf("unexpected namecheap request %s %s", request.Method, request.URL.Path)
		}
		query := request.URL.Query()
		if query.Get("host") != "router" || query.Get("domain") != "Example.com" || query.Get("password") != "test-password" || query.Get("ip") != "203.0.113.9" {
			t.Fatalf("unexpected namecheap update query: %s", request.URL.RawQuery)
		}
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = writer.Write([]byte("<interface-response><ErrCount>0</ErrCount><errors></errors><Done>true</Done></interface-response>"))
	}))
	defer server.Close()

	provider := &NamecheapProvider{BaseURL: server.URL, Client: server.Client()}
	err := provider.Upsert(context.Background(), "test-password", map[string]string{
		NamecheapSettingDomain: "Example.com",
		NamecheapSettingHost:   "router",
	}, "router.example.com", dynamicDNSModels.RecordTypeA, netip.MustParseAddr("203.0.113.9"))
	if err != nil {
		t.Fatalf("updating namecheap record failed: %v", err)
	}
}

func TestNamecheapUpsertAcceptsUTF16DeclaredResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = writer.Write([]byte("<?xml version=\"1.0\" encoding=\"utf-16\"?><interface-response><ErrCount>0</ErrCount><errors></errors><Done>true</Done></interface-response>"))
	}))
	defer server.Close()

	provider := &NamecheapProvider{BaseURL: server.URL, Client: server.Client()}
	err := provider.Upsert(context.Background(), "test-password", map[string]string{
		NamecheapSettingDomain: "example.com",
		NamecheapSettingHost:   "router",
	}, "router.example.com", dynamicDNSModels.RecordTypeA, netip.MustParseAddr("203.0.113.9"))
	if err != nil {
		t.Fatalf("updating namecheap record with a UTF-16 declaration failed: %v", err)
	}
}

func TestNamecheapUpsertReportsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = writer.Write([]byte("<?xml version=\"1.0\" encoding=\"utf-16\"?><interface-response><ErrCount>1</ErrCount><errors><Err1>Passwords do not match</Err1></errors><Done>true</Done></interface-response>"))
	}))
	defer server.Close()

	provider := &NamecheapProvider{BaseURL: server.URL, Client: server.Client()}
	err := provider.Upsert(context.Background(), "test-password", map[string]string{
		NamecheapSettingDomain: "example.com",
		NamecheapSettingHost:   "router",
	}, "router.example.com", dynamicDNSModels.RecordTypeA, netip.MustParseAddr("203.0.113.9"))
	if err == nil || !strings.Contains(err.Error(), "Passwords do not match") {
		t.Fatalf("expected namecheap API error, got %v", err)
	}
}
