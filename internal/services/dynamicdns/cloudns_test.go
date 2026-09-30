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
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	dynamicDNSModels "github.com/alchemillahq/sylve/internal/db/models/dynamicdns"
)

type cloudnsCall struct {
	path string
	form url.Values
}

type cloudnsCallLog struct {
	mu    sync.Mutex
	calls []cloudnsCall
}

func (l *cloudnsCallLog) record(path string, form url.Values) {
	cloned := url.Values{}
	for key, values := range form {
		cloned[key] = append([]string(nil), values...)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, cloudnsCall{path: path, form: cloned})
}

func (l *cloudnsCallLog) count(path string) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	count := 0
	for _, call := range l.calls {
		if call.path == path {
			count++
		}
	}
	return count
}

func (l *cloudnsCallLog) forms(path string) []url.Values {
	l.mu.Lock()
	defer l.mu.Unlock()

	var forms []url.Values
	for _, call := range l.calls {
		if call.path == path {
			forms = append(forms, call.form)
		}
	}
	return forms
}

func newCloudnsTestProvider(t *testing.T, handler http.HandlerFunc) *CloudnsProvider {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &CloudnsProvider{BaseURL: server.URL, Client: server.Client()}
}

func cloudnsPostForm(t *testing.T, request *http.Request) url.Values {
	t.Helper()

	if request.Method != http.MethodPost {
		t.Errorf("unexpected cloudns method %s", request.Method)
	}
	if request.URL.RawQuery != "" {
		t.Errorf("cloudns request leaked a query string: %q", request.URL.RawQuery)
	}
	if contentType := request.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
		t.Errorf("unexpected cloudns content type %q", contentType)
	}
	if err := request.ParseForm(); err != nil {
		t.Errorf("parsing cloudns form failed: %v", err)
		return url.Values{}
	}
	return request.PostForm
}

func cloudnsExpectAuth(t *testing.T, form url.Values, authID, password string) {
	t.Helper()

	if got := form.Get("auth-id"); got != authID {
		t.Errorf("unexpected cloudns auth-id %q", got)
	}
	if got := form.Get("auth-password"); got != password {
		t.Errorf("unexpected cloudns auth-password %q", got)
	}
}

func writeCloudnsBody(t *testing.T, writer http.ResponseWriter, body string) {
	t.Helper()

	writer.Header().Set("Content-Type", "application/json")
	if _, err := io.WriteString(writer, body); err != nil {
		t.Errorf("writing cloudns response failed: %v", err)
	}
}

func cloudnsUnexpected(t *testing.T, writer http.ResponseWriter, request *http.Request) {
	t.Helper()

	t.Errorf("unexpected cloudns request %s %s", request.Method, request.URL.Path)
	http.Error(writer, "unexpected request", http.StatusInternalServerError)
}

func cloudnsJSON(t *testing.T, value any) string {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding cloudns fixture failed: %v", err)
	}
	return string(encoded)
}

func cloudnsZoneJSON(t *testing.T, name, zoneType, status string) string {
	t.Helper()

	return cloudnsJSON(t, map[string]any{"name": name, "type": zoneType, "zone": name, "status": status})
}

func cloudnsPage(t *testing.T, entries map[string]map[string]any) string {
	t.Helper()

	return cloudnsJSON(t, entries)
}

func cloudnsEntry(id, recordType, host, value, ttl string, status int) map[string]any {
	return map[string]any{"id": id, "type": recordType, "host": host, "record": value, "ttl": ttl, "status": status}
}

func cloudnsSettings(zone, host string) map[string]string {
	return map[string]string{
		CloudnsSettingAuthID: "12345",
		CloudnsSettingZone:   zone,
		CloudnsSettingHost:   host,
	}
}

func requireCloudnsProviderError(t *testing.T, err error, kind providerErrorKind) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected a cloudns provider error of kind %d", kind)
	}
	got, _, ok := providerErrorDetails(err)
	if !ok || got != kind {
		t.Fatalf("expected provider error kind %d, got %v (error: %v)", kind, got, err)
	}
}

func TestCloudnsValidateDiscoversMostSpecificZone(t *testing.T) {
	log := &cloudnsCallLog{}
	provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		form := cloudnsPostForm(t, request)
		log.record(request.URL.Path, form)
		cloudnsExpectAuth(t, form, "12345", "api-password")

		switch request.URL.Path {
		case cloudnsRecordsStatsPath:
			writeCloudnsBody(t, writer, `{"count":"12","limit":"100"}`)
		case cloudnsZoneInfoPath:
			switch form.Get("domain-name") {
			case "home.example.co.uk":
				writeCloudnsBody(t, writer, `{"status":"Failed","statusDescription":"Missing domain-name"}`)
			case "example.co.uk":
				writeCloudnsBody(t, writer, cloudnsZoneJSON(t, "example.co.uk", "master", "1"))
			default:
				cloudnsUnexpected(t, writer, request)
			}
		default:
			cloudnsUnexpected(t, writer, request)
		}
	})

	settings, err := provider.Validate(context.Background(), "api-password", "home.example.co.uk", "A", map[string]string{CloudnsSettingAuthID: "12345"})
	if err != nil {
		t.Fatalf("validating cloudns entry failed: %v", err)
	}
	if settings[CloudnsSettingAuthID] != "12345" || settings[CloudnsSettingZone] != "example.co.uk" || settings[CloudnsSettingHost] != "home" || len(settings) != 3 {
		t.Fatalf("unexpected cloudns settings: %#v", settings)
	}
	if got := log.count(cloudnsZoneInfoPath); got != 2 {
		t.Fatalf("expected 2 zone lookups, got %d", got)
	}
}

func TestCloudnsValidateTreatsEmptyZoneResponseAsMiss(t *testing.T) {
	provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		form := cloudnsPostForm(t, request)

		switch request.URL.Path {
		case cloudnsRecordsStatsPath:
			writeCloudnsBody(t, writer, `{"count":"1","limit":"100"}`)
		case cloudnsZoneInfoPath:
			switch form.Get("domain-name") {
			case "home.example.com":
				writer.WriteHeader(http.StatusOK)
			case "example.com":
				writeCloudnsBody(t, writer, cloudnsZoneJSON(t, "example.com", "master", "1"))
			default:
				cloudnsUnexpected(t, writer, request)
			}
		default:
			cloudnsUnexpected(t, writer, request)
		}
	})

	settings, err := provider.Validate(context.Background(), "api-password", "home.example.com", dynamicDNSModels.RecordTypeBoth, map[string]string{CloudnsSettingAuthID: "12345"})
	if err != nil {
		t.Fatalf("validating cloudns entry failed: %v", err)
	}
	if settings[CloudnsSettingZone] != "example.com" || settings[CloudnsSettingHost] != "home" {
		t.Fatalf("unexpected cloudns settings: %#v", settings)
	}
}

func TestCloudnsValidateStoresApexSentinel(t *testing.T) {
	provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		form := cloudnsPostForm(t, request)

		switch request.URL.Path {
		case cloudnsRecordsStatsPath:
			writeCloudnsBody(t, writer, `{"count":"1","limit":"100"}`)
		case cloudnsZoneInfoPath:
			if form.Get("domain-name") != "example.com" {
				cloudnsUnexpected(t, writer, request)
				return
			}
			writeCloudnsBody(t, writer, cloudnsZoneJSON(t, "example.com", "master", "1"))
		default:
			cloudnsUnexpected(t, writer, request)
		}
	})

	settings, err := provider.Validate(context.Background(), "api-password", "example.com", "A", map[string]string{CloudnsSettingAuthID: "12345"})
	if err != nil {
		t.Fatalf("validating cloudns apex entry failed: %v", err)
	}
	if settings[CloudnsSettingHost] != cloudnsApexHost {
		t.Fatalf("expected apex sentinel, got %#v", settings)
	}
}

func TestCloudnsValidateRejectsInvalidCredentials(t *testing.T) {
	log := &cloudnsCallLog{}
	provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		form := cloudnsPostForm(t, request)
		log.record(request.URL.Path, form)

		if request.URL.Path != cloudnsRecordsStatsPath {
			cloudnsUnexpected(t, writer, request)
			return
		}
		writeCloudnsBody(t, writer, `{"status":"Failed","statusDescription":"Invalid authentication, incorrect user ID or password."}`)
	})

	_, err := provider.Validate(context.Background(), "api-password", "home.example.com", "A", map[string]string{CloudnsSettingAuthID: "12345"})
	requireCloudnsProviderError(t, err, providerErrorPermanent)
	if got := log.count(cloudnsZoneInfoPath); got != 0 {
		t.Fatalf("expected no zone lookups after a credential failure, got %d", got)
	}
}

func TestCloudnsValidateClassifiesProbeFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		kind   providerErrorKind
	}{
		{name: "unknown failure is transient", status: http.StatusOK, body: `{"status":"Failed","statusDescription":"Unexpected backend condition"}`, kind: providerErrorTransient},
		{name: "empty body is transient", status: http.StatusOK, body: "", kind: providerErrorTransient},
		{name: "invalid json is transient", status: http.StatusOK, body: "not json", kind: providerErrorTransient},
		{name: "server failure is transient", status: http.StatusServiceUnavailable, body: "", kind: providerErrorTransient},
		{name: "unauthorized is permanent", status: http.StatusUnauthorized, body: "", kind: providerErrorPermanent},
		{name: "forbidden is transient", status: http.StatusForbidden, body: "", kind: providerErrorTransient},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
				cloudnsPostForm(t, request)
				if request.URL.Path != cloudnsRecordsStatsPath {
					cloudnsUnexpected(t, writer, request)
					return
				}
				if testCase.status != http.StatusOK {
					writer.WriteHeader(testCase.status)
					if testCase.body != "" {
						_, _ = io.WriteString(writer, testCase.body)
					}
					return
				}
				writeCloudnsBody(t, writer, testCase.body)
			})

			_, err := provider.Validate(context.Background(), "api-password", "home.example.com", "A", map[string]string{CloudnsSettingAuthID: "12345"})
			requireCloudnsProviderError(t, err, testCase.kind)
		})
	}
}

func TestCloudnsValidateRejectsInvalidSettings(t *testing.T) {
	cases := []struct {
		name     string
		authID   string
		password string
		record   string
	}{
		{name: "empty auth id", authID: "", password: "api-password", record: "A"},
		{name: "non numeric auth id", authID: "abc", password: "api-password", record: "A"},
		{name: "zero auth id", authID: "0", password: "api-password", record: "A"},
		{name: "negative auth id", authID: "-5", password: "api-password", record: "A"},
		{name: "trailing text auth id", authID: "12x", password: "api-password", record: "A"},
		{name: "empty password", authID: "12345", password: "", record: "A"},
		{name: "unsupported record type", authID: "12345", password: "api-password", record: "CNAME"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
				cloudnsUnexpected(t, writer, request)
			})

			_, err := provider.Validate(context.Background(), testCase.password, "home.example.com", testCase.record, map[string]string{CloudnsSettingAuthID: testCase.authID})
			requireCloudnsProviderError(t, err, providerErrorPermanent)
		})
	}
}

func TestCloudnsValidateRejectsUnknownZone(t *testing.T) {
	provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		cloudnsPostForm(t, request)

		switch request.URL.Path {
		case cloudnsRecordsStatsPath:
			writeCloudnsBody(t, writer, `{"count":"1","limit":"100"}`)
		case cloudnsZoneInfoPath:
			writeCloudnsBody(t, writer, `{"status":"Failed","statusDescription":"Missing domain-name"}`)
		default:
			cloudnsUnexpected(t, writer, request)
		}
	})

	_, err := provider.Validate(context.Background(), "api-password", "a.home.example.com", "A", map[string]string{CloudnsSettingAuthID: "12345"})
	requireCloudnsProviderError(t, err, providerErrorPermanent)
	if err == nil || !strings.Contains(err.Error(), "no ClouDNS zone") {
		t.Fatalf("unexpected unknown zone error: %v", err)
	}
}

func TestCloudnsValidateRejectsUnsupportedZones(t *testing.T) {
	cases := []struct {
		name     string
		hostname string
		body     map[string]any
		kind     providerErrorKind
	}{
		{
			name:     "slave zone",
			hostname: "home.example.com",
			body:     map[string]any{"name": "home.example.com", "type": "slave", "zone": "example.com", "status": "1"},
			kind:     providerErrorPermanent,
		},
		{
			name:     "cloud zone",
			hostname: "home.example.com",
			body:     map[string]any{"name": "home.example.com", "type": "cloud", "zone": "example.com", "status": "1", "cloud-master": "example.com"},
			kind:     providerErrorPermanent,
		},
		{
			name:     "geodns zone",
			hostname: "home.example.com",
			body:     map[string]any{"name": "home.example.com", "type": "geodns", "zone": "example.com", "status": "1"},
			kind:     providerErrorPermanent,
		},
		{
			name:     "unknown zone type",
			hostname: "home.example.com",
			body:     map[string]any{"name": "home.example.com", "type": "parked", "zone": "example.com", "status": "1"},
			kind:     providerErrorPermanent,
		},
		{
			name:     "inactive zone",
			hostname: "home.example.com",
			body:     map[string]any{"name": "home.example.com", "type": "master", "zone": "example.com", "status": "0"},
			kind:     providerErrorPermanent,
		},
		{
			name:     "unknown zone status",
			hostname: "home.example.com",
			body:     map[string]any{"name": "home.example.com", "type": "master", "zone": "example.com", "status": "7"},
			kind:     providerErrorTransient,
		},
		{
			name:     "reverse zone",
			hostname: "1.0.0.127.in-addr.arpa",
			body:     map[string]any{"name": "1.0.0.127.in-addr.arpa", "type": "master", "zone": "1.0.0.127.in-addr.arpa", "status": "1"},
			kind:     providerErrorPermanent,
		},
		{
			name:     "missing type",
			hostname: "home.example.com",
			body:     map[string]any{"name": "home.example.com", "zone": "example.com", "status": "1"},
			kind:     providerErrorTransient,
		},
		{
			name:     "missing name",
			hostname: "home.example.com",
			body:     map[string]any{"type": "master", "zone": "example.com", "status": "1"},
			kind:     providerErrorTransient,
		},
		{
			name:     "name mismatch",
			hostname: "home.example.com",
			body:     map[string]any{"name": "other.example.com", "type": "master", "zone": "example.com", "status": "1"},
			kind:     providerErrorTransient,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
				cloudnsPostForm(t, request)

				switch request.URL.Path {
				case cloudnsRecordsStatsPath:
					writeCloudnsBody(t, writer, `{"count":"1","limit":"100"}`)
				case cloudnsZoneInfoPath:
					writeCloudnsBody(t, writer, cloudnsJSON(t, testCase.body))
				default:
					cloudnsUnexpected(t, writer, request)
				}
			})

			_, err := provider.Validate(context.Background(), "api-password", testCase.hostname, "A", map[string]string{CloudnsSettingAuthID: "12345"})
			requireCloudnsProviderError(t, err, testCase.kind)
		})
	}
}

func TestCloudnsValidateAbortsOnUnexpectedZoneFailure(t *testing.T) {
	log := &cloudnsCallLog{}
	provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		form := cloudnsPostForm(t, request)
		log.record(request.URL.Path, form)

		switch request.URL.Path {
		case cloudnsRecordsStatsPath:
			writeCloudnsBody(t, writer, `{"count":"1","limit":"100"}`)
		case cloudnsZoneInfoPath:
			writeCloudnsBody(t, writer, `{"status":"Failed","statusDescription":"Unexpected backend condition"}`)
		default:
			cloudnsUnexpected(t, writer, request)
		}
	})

	_, err := provider.Validate(context.Background(), "api-password", "home.example.com", "A", map[string]string{CloudnsSettingAuthID: "12345"})
	requireCloudnsProviderError(t, err, providerErrorTransient)
	if got := log.count(cloudnsZoneInfoPath); got != 1 {
		t.Fatalf("expected discovery to abort after the first unexpected failure, got %d lookups", got)
	}
}

func TestCloudnsValidateRespectsCallerDeadline(t *testing.T) {
	provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		writeCloudnsBody(t, writer, `{"count":"1","limit":"100"}`)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := provider.Validate(ctx, "api-password", "home.example.com", "A", map[string]string{CloudnsSettingAuthID: "12345"})
	requireCloudnsProviderError(t, err, providerErrorTransient)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected the caller deadline to abort validation, got %v", err)
	}
}

func TestCloudnsUpsertModifiesChangedRecord(t *testing.T) {
	log := &cloudnsCallLog{}
	recordsBody := cloudnsPage(t, map[string]map[string]any{
		"111": cloudnsEntry("111", "A", "home", "203.0.113.1", "300", 1),
	})

	provider := cloudnsUpsertProvider(t, log, recordsBody, func(writer http.ResponseWriter, request *http.Request, form url.Values) {
		if request.URL.Path != cloudnsModRecordPath {
			cloudnsUnexpected(t, writer, request)
			return
		}
		if form.Get("domain-name") != "example.com" || form.Get("record-id") != "111" || form.Get("host") != "home" || form.Get("record") != "203.0.113.9" || form.Get("ttl") != "300" {
			t.Errorf("unexpected mod-record form: %v", form)
		}
		writeCloudnsBody(t, writer, `{"status":"Success","statusDescription":"The record was modified successfully."}`)
	})

	if err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9")); err != nil {
		t.Fatalf("updating cloudns record failed: %v", err)
	}
	if got := log.count(cloudnsModRecordPath); got != 1 {
		t.Fatalf("expected 1 mod-record call, got %d", got)
	}
	forms := log.forms(cloudnsRecordsPath)
	if len(forms) != 1 || forms[0].Get("rows-per-page") != "100" || forms[0].Get("page") != "1" || forms[0].Get("type") != "A" || forms[0].Get("host") != "home" {
		t.Fatalf("unexpected records listing form: %v", forms)
	}
}

func TestCloudnsUpsertNoopsWhenRecordMatches(t *testing.T) {
	log := &cloudnsCallLog{}
	recordsBody := cloudnsPage(t, map[string]map[string]any{
		"111": cloudnsEntry("111", "A", "home", "203.0.113.9", "300", 1),
	})

	provider := cloudnsUpsertProvider(t, log, recordsBody, func(writer http.ResponseWriter, request *http.Request, _ url.Values) {
		cloudnsUnexpected(t, writer, request)
	})

	if err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9")); err != nil {
		t.Fatalf("noop cloudns update failed: %v", err)
	}
	if got := log.count(cloudnsModRecordPath); got != 0 {
		t.Fatalf("expected no mod-record calls, got %d", got)
	}
}

func TestCloudnsUpsertNoopsOnEquivalentIPv6(t *testing.T) {
	log := &cloudnsCallLog{}
	recordsBody := cloudnsPage(t, map[string]map[string]any{
		"222": cloudnsEntry("222", "AAAA", "home", "2001:0db8:0000:0000:0000:0000:0000:0001", "300", 1),
	})

	provider := cloudnsUpsertProvider(t, log, recordsBody, func(writer http.ResponseWriter, request *http.Request, _ url.Values) {
		cloudnsUnexpected(t, writer, request)
	})

	if err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", dynamicDNSModels.RecordTypeAAAA, netip.MustParseAddr("2001:db8::1")); err != nil {
		t.Fatalf("equivalent IPv6 update failed: %v", err)
	}
	if got := log.count(cloudnsModRecordPath); got != 0 {
		t.Fatalf("expected no mod-record calls, got %d", got)
	}
}

func TestCloudnsUpsertCreatesMissingRecord(t *testing.T) {
	log := &cloudnsCallLog{}
	provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		form := cloudnsPostForm(t, request)
		log.record(request.URL.Path, form)
		cloudnsExpectAuth(t, form, "12345", "api-password")

		switch request.URL.Path {
		case cloudnsRecordsPath:
			writeCloudnsBody(t, writer, `[]`)
		case cloudnsAvailableTTLPath:
			if form.Get("domain-name") != "example.com" {
				t.Errorf("unexpected available-ttl form: %v", form)
			}
			writeCloudnsBody(t, writer, `["3600",300,60]`)
		case cloudnsAddRecordPath:
			if form.Get("domain-name") != "example.com" || form.Get("record-type") != "A" || form.Get("host") != "home" || form.Get("record") != "203.0.113.9" || form.Get("ttl") != "60" {
				t.Errorf("unexpected add-record form: %v", form)
			}
			writeCloudnsBody(t, writer, `{"status":"Success","statusDescription":"The record was added successfully."}`)
		default:
			cloudnsUnexpected(t, writer, request)
		}
	})

	if err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9")); err != nil {
		t.Fatalf("creating cloudns record failed: %v", err)
	}
	if got := log.count(cloudnsAddRecordPath); got != 1 {
		t.Fatalf("expected 1 add-record call, got %d", got)
	}
}

func TestCloudnsUpsertCreatesApexRecordWithEmptyHost(t *testing.T) {
	log := &cloudnsCallLog{}
	provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		form := cloudnsPostForm(t, request)
		log.record(request.URL.Path, form)

		switch request.URL.Path {
		case cloudnsRecordsPath:
			if value, ok := form["host"]; !ok || value[0] != "" {
				t.Errorf("expected an empty apex host, got %v", form["host"])
			}
			writeCloudnsBody(t, writer, `[]`)
		case cloudnsAvailableTTLPath:
			writeCloudnsBody(t, writer, `[300]`)
		case cloudnsAddRecordPath:
			if value, ok := form["host"]; !ok || value[0] != "" {
				t.Errorf("expected an empty apex host on add-record, got %v", form["host"])
			}
			writeCloudnsBody(t, writer, `{"status":"Success"}`)
		default:
			cloudnsUnexpected(t, writer, request)
		}
	})

	if err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", cloudnsApexHost), "example.com", "A", netip.MustParseAddr("203.0.113.9")); err != nil {
		t.Fatalf("creating apex cloudns record failed: %v", err)
	}
	if got := log.count(cloudnsAddRecordPath); got != 1 {
		t.Fatalf("expected 1 add-record call, got %d", got)
	}
}

func TestCloudnsUpsertPropagatesTTLFailures(t *testing.T) {
	cases := []struct {
		name     string
		response func(writer http.ResponseWriter)
	}{
		{
			name: "lookup failure",
			response: func(writer http.ResponseWriter) {
				writer.WriteHeader(http.StatusServiceUnavailable)
			},
		},
		{
			name: "empty ttl list",
			response: func(writer http.ResponseWriter) {
				_, _ = io.WriteString(writer, `[]`)
			},
		},
		{
			name: "malformed ttl list",
			response: func(writer http.ResponseWriter) {
				_, _ = io.WriteString(writer, `{"ttl":"300"}`)
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			log := &cloudnsCallLog{}
			provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
				form := cloudnsPostForm(t, request)
				log.record(request.URL.Path, form)

				switch request.URL.Path {
				case cloudnsRecordsPath:
					writeCloudnsBody(t, writer, `[]`)
				case cloudnsAvailableTTLPath:
					testCase.response(writer)
				case cloudnsAddRecordPath:
					cloudnsUnexpected(t, writer, request)
				default:
					cloudnsUnexpected(t, writer, request)
				}
			})

			err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9"))
			requireCloudnsProviderError(t, err, providerErrorTransient)
			if got := log.count(cloudnsAddRecordPath); got != 0 {
				t.Fatalf("expected no add-record calls, got %d", got)
			}
		})
	}
}

func TestCloudnsUpsertPaginatesRecords(t *testing.T) {
	log := &cloudnsCallLog{}
	provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		form := cloudnsPostForm(t, request)
		log.record(request.URL.Path, form)

		switch request.URL.Path {
		case cloudnsRecordsPath:
			switch form.Get("page") {
			case "1":
				entries := make(map[string]map[string]any, 100)
				for index := 0; index < 100; index++ {
					id := strconv.Itoa(1000 + index)
					entries[id] = cloudnsEntry(id, "A", "other", "198.51.100.1", "300", 1)
				}
				writeCloudnsBody(t, writer, cloudnsPage(t, entries))
			case "2":
				writeCloudnsBody(t, writer, cloudnsPage(t, map[string]map[string]any{
					"2000": cloudnsEntry("2000", "A", "home", "203.0.113.1", "300", 1),
				}))
			default:
				cloudnsUnexpected(t, writer, request)
			}
		case cloudnsModRecordPath:
			if form.Get("record-id") != "2000" {
				t.Errorf("unexpected paginated mod-record form: %v", form)
			}
			writeCloudnsBody(t, writer, `{"status":"Success"}`)
		default:
			cloudnsUnexpected(t, writer, request)
		}
	})

	if err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9")); err != nil {
		t.Fatalf("paginated cloudns update failed: %v", err)
	}
	forms := log.forms(cloudnsRecordsPath)
	if len(forms) != 2 || forms[0].Get("page") != "1" || forms[1].Get("page") != "2" {
		t.Fatalf("unexpected pagination forms: %v", forms)
	}
	if got := log.count(cloudnsModRecordPath); got != 1 {
		t.Fatalf("expected 1 mod-record call, got %d", got)
	}
}

func TestCloudnsUpsertRejectsFullFinalPage(t *testing.T) {
	log := &cloudnsCallLog{}
	provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		form := cloudnsPostForm(t, request)
		log.record(request.URL.Path, form)

		if request.URL.Path != cloudnsRecordsPath {
			cloudnsUnexpected(t, writer, request)
			return
		}
		page, err := strconv.Atoi(form.Get("page"))
		if err != nil {
			t.Errorf("invalid cloudns page %q", form.Get("page"))
		}
		entries := make(map[string]map[string]any, 100)
		for index := 0; index < 100; index++ {
			id := strconv.Itoa(page*1000 + index)
			entries[id] = cloudnsEntry(id, "A", "other", "198.51.100.1", "300", 1)
		}
		writeCloudnsBody(t, writer, cloudnsPage(t, entries))
	})

	err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9"))
	requireCloudnsProviderError(t, err, providerErrorTransient)
	if got := log.count(cloudnsRecordsPath); got != cloudnsMaximumRecordPages {
		t.Fatalf("expected %d page requests, got %d", cloudnsMaximumRecordPages, got)
	}
	if got := log.count(cloudnsModRecordPath) + log.count(cloudnsAddRecordPath); got != 0 {
		t.Fatalf("expected no writes, got %d", got)
	}
}

func TestCloudnsUpsertRejectsRepeatedRecordIDs(t *testing.T) {
	log := &cloudnsCallLog{}
	provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		form := cloudnsPostForm(t, request)
		log.record(request.URL.Path, form)

		if request.URL.Path != cloudnsRecordsPath {
			cloudnsUnexpected(t, writer, request)
			return
		}
		switch form.Get("page") {
		case "1":
			entries := make(map[string]map[string]any, 100)
			for index := 1; index <= 100; index++ {
				id := strconv.Itoa(index)
				entries[id] = cloudnsEntry(id, "A", "other", "198.51.100.1", "300", 1)
			}
			writeCloudnsBody(t, writer, cloudnsPage(t, entries))
		case "2":
			writeCloudnsBody(t, writer, cloudnsPage(t, map[string]map[string]any{
				"1": cloudnsEntry("1", "A", "other", "198.51.100.1", "300", 1),
			}))
		default:
			cloudnsUnexpected(t, writer, request)
		}
	})

	err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9"))
	requireCloudnsProviderError(t, err, providerErrorTransient)
	if got := log.count(cloudnsModRecordPath) + log.count(cloudnsAddRecordPath); got != 0 {
		t.Fatalf("expected no writes, got %d", got)
	}
}

func TestCloudnsUpsertRejectsPageFailure(t *testing.T) {
	log := &cloudnsCallLog{}
	provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		form := cloudnsPostForm(t, request)
		log.record(request.URL.Path, form)

		if request.URL.Path != cloudnsRecordsPath {
			cloudnsUnexpected(t, writer, request)
			return
		}
		switch form.Get("page") {
		case "1":
			entries := make(map[string]map[string]any, 100)
			for index := 0; index < 100; index++ {
				id := strconv.Itoa(1000 + index)
				entries[id] = cloudnsEntry(id, "A", "other", "198.51.100.1", "300", 1)
			}
			writeCloudnsBody(t, writer, cloudnsPage(t, entries))
		default:
			writer.WriteHeader(http.StatusServiceUnavailable)
		}
	})

	err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9"))
	requireCloudnsProviderError(t, err, providerErrorTransient)
	if got := log.count(cloudnsModRecordPath) + log.count(cloudnsAddRecordPath); got != 0 {
		t.Fatalf("expected no writes, got %d", got)
	}
}

func TestCloudnsUpsertSkipsInactiveRecordsInMixedSet(t *testing.T) {
	log := &cloudnsCallLog{}
	recordsBody := cloudnsPage(t, map[string]map[string]any{
		"1": cloudnsEntry("1", "A", "home", "203.0.113.1", "300", 1),
		"2": cloudnsEntry("2", "A", "home", "203.0.113.2", "300", 0),
		"3": cloudnsEntry("3", "A", "home", "203.0.113.3", "300", 1),
	})

	provider := cloudnsUpsertProvider(t, log, recordsBody, func(writer http.ResponseWriter, request *http.Request, _ url.Values) {
		if request.URL.Path != cloudnsModRecordPath {
			cloudnsUnexpected(t, writer, request)
			return
		}
		writeCloudnsBody(t, writer, `{"status":"Success"}`)
	})

	if err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9")); err != nil {
		t.Fatalf("mixed active/inactive cloudns update failed: %v", err)
	}
	forms := log.forms(cloudnsModRecordPath)
	if len(forms) != 2 || forms[0].Get("record-id") != "1" || forms[1].Get("record-id") != "3" {
		t.Fatalf("unexpected mod-record calls: %v", forms)
	}
}

func TestCloudnsUpsertRejectsInactiveOnlySet(t *testing.T) {
	log := &cloudnsCallLog{}
	recordsBody := cloudnsPage(t, map[string]map[string]any{
		"1": cloudnsEntry("1", "A", "home", "203.0.113.1", "300", 0),
	})

	provider := cloudnsUpsertProvider(t, log, recordsBody, func(writer http.ResponseWriter, request *http.Request, _ url.Values) {
		cloudnsUnexpected(t, writer, request)
	})

	err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9"))
	requireCloudnsProviderError(t, err, providerErrorPermanent)
	if got := log.count(cloudnsModRecordPath) + log.count(cloudnsAddRecordPath) + log.count(cloudnsAvailableTTLPath); got != 0 {
		t.Fatalf("expected no writes, got %d", got)
	}
}

func TestCloudnsUpsertRejectsUnknownRecordStatus(t *testing.T) {
	cases := []struct {
		name  string
		entry map[string]any
	}{
		{
			name:  "missing status",
			entry: map[string]any{"id": "1", "type": "A", "host": "home", "record": "203.0.113.1", "ttl": "300"},
		},
		{
			name:  "unknown status value",
			entry: cloudnsEntry("1", "A", "home", "203.0.113.1", "300", 2),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			log := &cloudnsCallLog{}
			recordsBody := cloudnsPage(t, map[string]map[string]any{"1": testCase.entry})
			provider := cloudnsUpsertProvider(t, log, recordsBody, func(writer http.ResponseWriter, request *http.Request, _ url.Values) {
				cloudnsUnexpected(t, writer, request)
			})

			err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9"))
			requireCloudnsProviderError(t, err, providerErrorTransient)
			if got := log.count(cloudnsModRecordPath) + log.count(cloudnsAddRecordPath); got != 0 {
				t.Fatalf("expected no writes, got %d", got)
			}
		})
	}
}

func TestCloudnsUpsertRejectsGeoDNSRecord(t *testing.T) {
	log := &cloudnsCallLog{}
	entry := cloudnsEntry("1", "A", "home", "203.0.113.1", "300", 1)
	entry["geodns-location-code"] = "eu-west"
	recordsBody := cloudnsPage(t, map[string]map[string]any{"1": entry})

	provider := cloudnsUpsertProvider(t, log, recordsBody, func(writer http.ResponseWriter, request *http.Request, _ url.Values) {
		cloudnsUnexpected(t, writer, request)
	})

	err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9"))
	requireCloudnsProviderError(t, err, providerErrorPermanent)
	if got := log.count(cloudnsModRecordPath) + log.count(cloudnsAddRecordPath); got != 0 {
		t.Fatalf("expected no writes, got %d", got)
	}
}

func TestCloudnsUpsertRejectsIncompleteRecordIdentity(t *testing.T) {
	matching := cloudnsEntry("2", "A", "home", "203.0.113.1", "300", 1)
	cases := []struct {
		name    string
		host    string
		entries map[string]map[string]any
	}{
		{
			name: "missing host on the apex",
			host: cloudnsApexHost,
			entries: map[string]map[string]any{
				"1": {"id": "1", "type": "A", "record": "203.0.113.1", "ttl": "300", "status": 1},
			},
		},
		{
			name: "null host",
			host: "home",
			entries: map[string]map[string]any{
				"1": {"id": "1", "type": "A", "host": nil, "record": "203.0.113.1", "ttl": "300", "status": 1},
			},
		},
		{
			name: "missing host beside a matching record",
			host: "home",
			entries: map[string]map[string]any{
				"1": {"id": "1", "type": "A", "record": "198.51.100.1", "ttl": "300", "status": 1},
				"2": matching,
			},
		},
		{
			name: "missing type beside a matching record",
			host: "home",
			entries: map[string]map[string]any{
				"1": {"id": "1", "host": "home", "record": "198.51.100.1", "ttl": "300", "status": 1},
				"2": matching,
			},
		},
		{
			name: "empty type",
			host: "home",
			entries: map[string]map[string]any{
				"1": {"id": "1", "type": "", "host": "home", "record": "198.51.100.1", "ttl": "300", "status": 1},
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			log := &cloudnsCallLog{}
			recordsBody := cloudnsPage(t, testCase.entries)
			provider := cloudnsUpsertProvider(t, log, recordsBody, func(writer http.ResponseWriter, request *http.Request, _ url.Values) {
				cloudnsUnexpected(t, writer, request)
			})

			hostname := "home.example.com"
			if testCase.host == cloudnsApexHost {
				hostname = "example.com"
			}
			err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", testCase.host), hostname, "A", netip.MustParseAddr("203.0.113.9"))
			requireCloudnsProviderError(t, err, providerErrorTransient)
			if got := log.count(cloudnsModRecordPath) + log.count(cloudnsAddRecordPath); got != 0 {
				t.Fatalf("expected no writes, got %d", got)
			}
		})
	}
}

func TestCloudnsUpsertTreatsPresentEmptyHostAsApex(t *testing.T) {
	log := &cloudnsCallLog{}
	recordsBody := cloudnsPage(t, map[string]map[string]any{
		"1": {"id": "1", "type": "A", "host": "", "record": "203.0.113.1", "ttl": "300", "status": 1},
	})

	provider := cloudnsUpsertProvider(t, log, recordsBody, func(writer http.ResponseWriter, request *http.Request, form url.Values) {
		if request.URL.Path != cloudnsModRecordPath {
			cloudnsUnexpected(t, writer, request)
			return
		}
		if value, ok := form["host"]; !ok || value[0] != "" {
			t.Errorf("expected the present empty apex host to be sent, got %v", form["host"])
		}
		writeCloudnsBody(t, writer, `{"status":"Success"}`)
	})

	if err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", cloudnsApexHost), "example.com", "A", netip.MustParseAddr("203.0.113.9")); err != nil {
		t.Fatalf("apex cloudns update failed: %v", err)
	}
	if got := log.count(cloudnsModRecordPath); got != 1 {
		t.Fatalf("expected 1 mod-record call, got %d", got)
	}
}

func TestCloudnsUpsertNormalizesRecordTypeCase(t *testing.T) {
	cases := []struct {
		name       string
		recordType string
		entryType  string
		value      string
		address    string
	}{
		{name: "lowercase a", recordType: "A", entryType: "a", value: "203.0.113.1", address: "203.0.113.9"},
		{name: "lowercase aaaa", recordType: dynamicDNSModels.RecordTypeAAAA, entryType: "aaaa", value: "2001:db8::1", address: "2001:db8::9"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			log := &cloudnsCallLog{}
			recordsBody := cloudnsPage(t, map[string]map[string]any{
				"111": cloudnsEntry("111", testCase.entryType, "home", testCase.value, "300", 1),
			})
			provider := cloudnsUpsertProvider(t, log, recordsBody, func(writer http.ResponseWriter, request *http.Request, form url.Values) {
				if request.URL.Path != cloudnsModRecordPath {
					cloudnsUnexpected(t, writer, request)
					return
				}
				if form.Get("record") != testCase.address {
					t.Errorf("unexpected mod-record form: %v", form)
				}
				writeCloudnsBody(t, writer, `{"status":"Success"}`)
			})

			if err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", testCase.recordType, netip.MustParseAddr(testCase.address)); err != nil {
				t.Fatalf("updating a %s record failed: %v", testCase.entryType, err)
			}
			if got := log.count(cloudnsModRecordPath); got != 1 {
				t.Fatalf("expected 1 mod-record call, got %d", got)
			}
		})
	}
}

func TestCloudnsUpsertRejectsMalformedRecordValues(t *testing.T) {
	cases := []struct {
		name  string
		entry map[string]any
	}{
		{
			name: "object record without geodns metadata",
			entry: func() map[string]any {
				entry := cloudnsEntry("1", "A", "home", "203.0.113.1", "300", 1)
				entry["record"] = map[string]any{}
				return entry
			}(),
		},
		{
			name: "numeric record",
			entry: func() map[string]any {
				entry := cloudnsEntry("1", "A", "home", "203.0.113.1", "300", 1)
				entry["record"] = 123
				return entry
			}(),
		},
		{
			name:  "invalid address",
			entry: cloudnsEntry("1", "A", "home", "not-an-address", "300", 1),
		},
		{
			name:  "wrong family",
			entry: cloudnsEntry("1", "A", "home", "2001:db8::1", "300", 1),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			log := &cloudnsCallLog{}
			recordsBody := cloudnsPage(t, map[string]map[string]any{"1": testCase.entry})
			provider := cloudnsUpsertProvider(t, log, recordsBody, func(writer http.ResponseWriter, request *http.Request, _ url.Values) {
				cloudnsUnexpected(t, writer, request)
			})

			err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9"))
			requireCloudnsProviderError(t, err, providerErrorTransient)
		})
	}
}

func TestCloudnsUpsertRejectsMalformedRecordIdentity(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		entry map[string]any
	}{
		{
			name:  "non numeric key",
			key:   "abc",
			entry: cloudnsEntry("abc", "A", "home", "203.0.113.1", "300", 1),
		},
		{
			name:  "mismatched id field",
			key:   "5",
			entry: cloudnsEntry("6", "A", "home", "203.0.113.1", "300", 1),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			log := &cloudnsCallLog{}
			recordsBody := cloudnsPage(t, map[string]map[string]any{testCase.key: testCase.entry})
			provider := cloudnsUpsertProvider(t, log, recordsBody, func(writer http.ResponseWriter, request *http.Request, _ url.Values) {
				cloudnsUnexpected(t, writer, request)
			})

			err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9"))
			requireCloudnsProviderError(t, err, providerErrorTransient)
		})
	}
}

func TestCloudnsUpsertRejectsMalformedTTL(t *testing.T) {
	cases := []struct {
		name  string
		entry map[string]any
	}{
		{
			name: "non numeric ttl",
			entry: func() map[string]any {
				entry := cloudnsEntry("1", "A", "home", "203.0.113.1", "300", 1)
				entry["ttl"] = "abc"
				return entry
			}(),
		},
		{
			name: "numeric ttl does not match the evidenced encoding",
			entry: func() map[string]any {
				entry := cloudnsEntry("1", "A", "home", "203.0.113.1", "300", 1)
				entry["ttl"] = 300
				return entry
			}(),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			log := &cloudnsCallLog{}
			recordsBody := cloudnsPage(t, map[string]map[string]any{"1": testCase.entry})
			provider := cloudnsUpsertProvider(t, log, recordsBody, func(writer http.ResponseWriter, request *http.Request, _ url.Values) {
				cloudnsUnexpected(t, writer, request)
			})

			err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9"))
			requireCloudnsProviderError(t, err, providerErrorTransient)
		})
	}
}

func TestCloudnsUpsertRejectsUnsupportedEmptyForms(t *testing.T) {
	cases := []string{"{}", "null"}

	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			log := &cloudnsCallLog{}
			provider := cloudnsUpsertProvider(t, log, body, func(writer http.ResponseWriter, request *http.Request, _ url.Values) {
				cloudnsUnexpected(t, writer, request)
			})

			err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9"))
			requireCloudnsProviderError(t, err, providerErrorTransient)
			if got := log.count(cloudnsAddRecordPath); got != 0 {
				t.Fatalf("expected no add-record calls, got %d", got)
			}
		})
	}
}

func TestCloudnsUpsertAcceptsWhitespaceEmptyArray(t *testing.T) {
	cases := []string{"[ ]", "[\n]", "[\n ]"}

	for _, body := range cases {
		t.Run(strconv.Quote(body), func(t *testing.T) {
			log := &cloudnsCallLog{}
			provider := cloudnsUpsertProvider(t, log, body, func(writer http.ResponseWriter, request *http.Request, _ url.Values) {
				switch request.URL.Path {
				case cloudnsAvailableTTLPath:
					writeCloudnsBody(t, writer, `[300]`)
				case cloudnsAddRecordPath:
					writeCloudnsBody(t, writer, `{"status":"Success"}`)
				default:
					cloudnsUnexpected(t, writer, request)
				}
			})

			if err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9")); err != nil {
				t.Fatalf("creating a record from an empty array failed: %v", err)
			}
			if got := log.count(cloudnsAddRecordPath); got != 1 {
				t.Fatalf("expected 1 add-record call, got %d", got)
			}
		})
	}
}

func TestCloudnsUpsertRejectsNonEmptyArrays(t *testing.T) {
	log := &cloudnsCallLog{}
	provider := cloudnsUpsertProvider(t, log, `[{"id":"1"}]`, func(writer http.ResponseWriter, request *http.Request, _ url.Values) {
		cloudnsUnexpected(t, writer, request)
	})

	err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9"))
	requireCloudnsProviderError(t, err, providerErrorTransient)
	if got := log.count(cloudnsAddRecordPath); got != 0 {
		t.Fatalf("expected no add-record calls, got %d", got)
	}
}

func TestCloudnsUpsertRejectsCachedTargetMismatch(t *testing.T) {
	log := &cloudnsCallLog{}
	provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		log.record(request.URL.Path, cloudnsPostForm(t, request))
		cloudnsUnexpected(t, writer, request)
	})

	err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "other"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9"))
	requireCloudnsProviderError(t, err, providerErrorPermanent)
	if len(log.calls) != 0 {
		t.Fatalf("expected no API calls for a mismatched cached target, got %d", len(log.calls))
	}
}

func TestCloudnsUpsertRejectsWrongAddressFamily(t *testing.T) {
	cases := []struct {
		name       string
		recordType string
		address    netip.Addr
	}{
		{name: "ipv4 record with ipv6 address", recordType: "A", address: netip.MustParseAddr("2001:db8::1")},
		{name: "ipv6 record with ipv4 address", recordType: dynamicDNSModels.RecordTypeAAAA, address: netip.MustParseAddr("203.0.113.9")},
		{name: "combined record type", recordType: dynamicDNSModels.RecordTypeBoth, address: netip.MustParseAddr("203.0.113.9")},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			log := &cloudnsCallLog{}
			provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
				log.record(request.URL.Path, cloudnsPostForm(t, request))
				cloudnsUnexpected(t, writer, request)
			})

			err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", testCase.recordType, testCase.address)
			requireCloudnsProviderError(t, err, providerErrorPermanent)
			if len(log.calls) != 0 {
				t.Fatalf("expected no API calls for a family mismatch, got %d", len(log.calls))
			}
		})
	}
}

func TestCloudnsUpsertClassifiesWriteFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		kind   providerErrorKind
	}{
		{name: "documented success", status: http.StatusOK, body: `{"status":"Success","statusDescription":"The record was modified successfully."}`},
		{name: "documented failure is permanent", status: http.StatusOK, body: `{"status":"Failed","statusDescription":"Invalid TTL. Choose from the list of the values we support."}`, kind: providerErrorPermanent},
		{name: "unknown failure is transient", status: http.StatusOK, body: `{"status":"Failed","statusDescription":"Invalid record-id param."}`, kind: providerErrorTransient},
		{name: "missing status is transient", status: http.StatusOK, body: `{}`, kind: providerErrorTransient},
		{name: "lowercase success is transient", status: http.StatusOK, body: `{"status":"success"}`, kind: providerErrorTransient},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			log := &cloudnsCallLog{}
			recordsBody := cloudnsPage(t, map[string]map[string]any{
				"111": cloudnsEntry("111", "A", "home", "203.0.113.1", "300", 1),
			})
			provider := cloudnsUpsertProvider(t, log, recordsBody, func(writer http.ResponseWriter, request *http.Request, _ url.Values) {
				if request.URL.Path != cloudnsModRecordPath {
					cloudnsUnexpected(t, writer, request)
					return
				}
				if testCase.status != http.StatusOK {
					writer.WriteHeader(testCase.status)
				}
				writeCloudnsBody(t, writer, testCase.body)
			})

			err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9"))
			if testCase.kind == 0 {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				return
			}
			requireCloudnsProviderError(t, err, testCase.kind)
		})
	}
}

func TestCloudnsClassifiesBeforeRedaction(t *testing.T) {
	t.Run("authentication prefix survives redaction", func(t *testing.T) {
		password := "authentication"
		provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
			cloudnsPostForm(t, request)
			writeCloudnsBody(t, writer, `{"status":"Failed","statusDescription":"Invalid authentication, incorrect user ID or password."}`)
		})

		_, err := provider.Validate(context.Background(), password, "home.example.com", "A", map[string]string{CloudnsSettingAuthID: "12345"})
		requireCloudnsProviderError(t, err, providerErrorPermanent)
		if strings.Contains(err.Error(), password) {
			t.Fatalf("cloudns error exposed the password: %v", err)
		}
	})

	t.Run("permanent description survives redaction", func(t *testing.T) {
		password := "Invalid"
		log := &cloudnsCallLog{}
		recordsBody := cloudnsPage(t, map[string]map[string]any{
			"111": cloudnsEntry("111", "A", "home", "203.0.113.1", "300", 1),
		})
		provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
			form := cloudnsPostForm(t, request)
			log.record(request.URL.Path, form)
			cloudnsExpectAuth(t, form, "12345", password)

			switch request.URL.Path {
			case cloudnsRecordsPath:
				writeCloudnsBody(t, writer, recordsBody)
			case cloudnsModRecordPath:
				writeCloudnsBody(t, writer, `{"status":"Failed","statusDescription":"Invalid TTL. Choose from the list of the values we support."}`)
			default:
				cloudnsUnexpected(t, writer, request)
			}
		})

		err := provider.Upsert(context.Background(), password, cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9"))
		requireCloudnsProviderError(t, err, providerErrorPermanent)
		if strings.Contains(err.Error(), password) {
			t.Fatalf("cloudns error exposed the password: %v", err)
		}
	})
}

func TestCloudnsUpsertHTTPStatusPrecedence(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		retryAfter string
		body       string
		kind       providerErrorKind
		wantRetry  time.Duration
	}{
		{name: "unauthorized is permanent", status: http.StatusUnauthorized, kind: providerErrorPermanent},
		{name: "forbidden is transient", status: http.StatusForbidden, kind: providerErrorTransient},
		{name: "throttled honors retry-after", status: http.StatusTooManyRequests, retryAfter: "7", kind: providerErrorTransient, wantRetry: 7 * time.Second},
		{name: "server error is transient", status: http.StatusInternalServerError, kind: providerErrorTransient},
		{
			name:   "http status wins over a permanent-looking body",
			status: http.StatusInternalServerError,
			body:   `{"status":"Failed","statusDescription":"Invalid authentication, incorrect user ID or password."}`,
			kind:   providerErrorTransient,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			log := &cloudnsCallLog{}
			recordsBody := cloudnsPage(t, map[string]map[string]any{
				"111": cloudnsEntry("111", "A", "home", "203.0.113.1", "300", 1),
			})
			provider := cloudnsUpsertProvider(t, log, recordsBody, func(writer http.ResponseWriter, request *http.Request, _ url.Values) {
				if request.URL.Path != cloudnsModRecordPath {
					cloudnsUnexpected(t, writer, request)
					return
				}
				if testCase.retryAfter != "" {
					writer.Header().Set("Retry-After", testCase.retryAfter)
				}
				writer.WriteHeader(testCase.status)
				if testCase.body != "" {
					_, _ = io.WriteString(writer, testCase.body)
				}
			})

			err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9"))
			requireCloudnsProviderError(t, err, testCase.kind)
			_, retryAfter, _ := providerErrorDetails(err)
			if retryAfter != testCase.wantRetry {
				t.Fatalf("expected retry after %s, got %s", testCase.wantRetry, retryAfter)
			}
			if testCase.status == http.StatusForbidden && err != nil && !strings.Contains(strings.ToLower(err.Error()), "allowlist") {
				t.Fatalf("expected an allowlist troubleshooting hint, got %v", err)
			}
		})
	}
}

func TestCloudnsUpsertTreatsMissingSavedZoneAsPermanent(t *testing.T) {
	log := &cloudnsCallLog{}
	provider := cloudnsUpsertProvider(t, log, `{"status":"Failed","statusDescription":"Missing domain-name"}`, func(writer http.ResponseWriter, request *http.Request, _ url.Values) {
		cloudnsUnexpected(t, writer, request)
	})

	err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9"))
	requireCloudnsProviderError(t, err, providerErrorPermanent)
}

func TestCloudnsUpsertConvergesAfterPartialModifyFailure(t *testing.T) {
	log := &cloudnsCallLog{}
	var mu sync.Mutex
	values := map[string]string{"1": "198.51.100.1", "2": "198.51.100.2"}
	failSecond := true

	provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		form := cloudnsPostForm(t, request)
		log.record(request.URL.Path, form)

		switch request.URL.Path {
		case cloudnsRecordsPath:
			entries := make(map[string]map[string]any, len(values))
			mu.Lock()
			for id, value := range values {
				entries[id] = cloudnsEntry(id, "A", "home", value, "300", 1)
			}
			mu.Unlock()
			writeCloudnsBody(t, writer, cloudnsPage(t, entries))
		case cloudnsModRecordPath:
			id := form.Get("record-id")
			mu.Lock()
			shouldFail := failSecond && id == "2"
			if shouldFail {
				failSecond = false
			}
			if !shouldFail {
				values[id] = form.Get("record")
			}
			mu.Unlock()

			if shouldFail {
				writer.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			writeCloudnsBody(t, writer, `{"status":"Success"}`)
		default:
			cloudnsUnexpected(t, writer, request)
		}
	})

	err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9"))
	requireCloudnsProviderError(t, err, providerErrorTransient)

	if err := provider.Upsert(context.Background(), "api-password", cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9")); err != nil {
		t.Fatalf("second cloudns sync failed: %v", err)
	}

	forms := log.forms(cloudnsModRecordPath)
	if len(forms) != 3 {
		t.Fatalf("expected 3 mod-record calls, got %v", forms)
	}
	if forms[0].Get("record-id") != "1" || forms[1].Get("record-id") != "2" || forms[2].Get("record-id") != "2" {
		t.Fatalf("unexpected convergence sequence: %v", forms)
	}
}

func TestCloudnsSanitizesPasswordInErrors(t *testing.T) {
	password := "p+a ss%"
	provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		cloudnsPostForm(t, request)
		writeCloudnsBody(t, writer, cloudnsJSON(t, map[string]any{
			"status":            "Failed",
			"statusDescription": "Invalid authentication, incorrect user ID or password: " + password + " (" + url.QueryEscape(password) + ")",
		}))
	})

	_, err := provider.Validate(context.Background(), password, "home.example.com", "A", map[string]string{CloudnsSettingAuthID: "12345"})
	requireCloudnsProviderError(t, err, providerErrorPermanent)

	message := err.Error()
	if strings.Contains(message, password) || strings.Contains(message, url.QueryEscape(password)) {
		t.Fatalf("cloudns error exposed the password: %q", message)
	}
	if !strings.Contains(message, "[REDACTED]") {
		t.Fatalf("expected a redaction marker, got %q", message)
	}
}

func TestCloudnsKeepsCredentialsOutOfURL(t *testing.T) {
	password := "p+a ss%&x"
	log := &cloudnsCallLog{}
	provider := newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		form := cloudnsPostForm(t, request)
		log.record(request.URL.Path, form)
		cloudnsExpectAuth(t, form, "12345", password)

		switch request.URL.Path {
		case cloudnsRecordsStatsPath:
			writeCloudnsBody(t, writer, `{"count":"1","limit":"100"}`)
		case cloudnsZoneInfoPath:
			switch form.Get("domain-name") {
			case "home.example.com":
				writeCloudnsBody(t, writer, `{"status":"Failed","statusDescription":"Missing domain-name"}`)
			case "example.com":
				writeCloudnsBody(t, writer, cloudnsZoneJSON(t, "example.com", "master", "1"))
			default:
				cloudnsUnexpected(t, writer, request)
			}
		case cloudnsRecordsPath:
			writeCloudnsBody(t, writer, `[]`)
		case cloudnsAvailableTTLPath:
			writeCloudnsBody(t, writer, `[300]`)
		case cloudnsAddRecordPath:
			writeCloudnsBody(t, writer, `{"status":"Success"}`)
		default:
			cloudnsUnexpected(t, writer, request)
		}
	})

	if _, err := provider.Validate(context.Background(), password, "home.example.com", "A", map[string]string{CloudnsSettingAuthID: "12345"}); err != nil {
		t.Fatalf("validating cloudns entry with a special password failed: %v", err)
	}
	if err := provider.Upsert(context.Background(), password, cloudnsSettings("example.com", "home"), "home.example.com", "A", netip.MustParseAddr("203.0.113.9")); err != nil {
		t.Fatalf("creating cloudns record with a special password failed: %v", err)
	}
	if len(log.calls) == 0 {
		t.Fatalf("expected cloudns calls to be recorded")
	}
}

func cloudnsUpsertProvider(t *testing.T, log *cloudnsCallLog, recordsBody string, write func(http.ResponseWriter, *http.Request, url.Values)) *CloudnsProvider {
	t.Helper()

	return newCloudnsTestProvider(t, func(writer http.ResponseWriter, request *http.Request) {
		form := cloudnsPostForm(t, request)
		log.record(request.URL.Path, form)
		cloudnsExpectAuth(t, form, "12345", "api-password")

		switch request.URL.Path {
		case cloudnsRecordsPath:
			writeCloudnsBody(t, writer, recordsBody)
		default:
			if write == nil {
				cloudnsUnexpected(t, writer, request)
				return
			}
			write(writer, request, form)
		}
	})
}
