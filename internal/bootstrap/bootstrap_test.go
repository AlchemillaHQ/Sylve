// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package bootstrap

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
)

type fakeSettings struct {
	mu      sync.Mutex
	calls   []systemServiceInterfaces.BootstrapSettingsRequest
	respond func(systemServiceInterfaces.BootstrapSettingsRequest) (systemServiceInterfaces.BootstrapSettingsResult, error)
}

func (f *fakeSettings) ApplyBootstrapSettings(_ context.Context, request systemServiceInterfaces.BootstrapSettingsRequest) (systemServiceInterfaces.BootstrapSettingsResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, request)
	respond := f.respond
	f.mu.Unlock()
	if respond == nil {
		return systemServiceInterfaces.BootstrapSettingsResult{Items: []systemServiceInterfaces.BootstrapItemResult{}}, nil
	}
	return respond(request)
}

func (f *fakeSettings) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type manualCall struct {
	name   string
	bridge string
}

type fakeNetwork struct {
	mu            sync.Mutex
	standardCalls []networkServiceInterfaces.CreateStandardSwitchRequest
	manualCalls   []manualCall
	standardFn    func(networkServiceInterfaces.CreateStandardSwitchRequest) (uint, error)
	manualFn      func(string, string) (*networkModels.ManualSwitch, error)
}

func (f *fakeNetwork) NewStandardSwitch(request networkServiceInterfaces.CreateStandardSwitchRequest) (uint, error) {
	f.mu.Lock()
	f.standardCalls = append(f.standardCalls, request)
	standardFn := f.standardFn
	f.mu.Unlock()
	if standardFn != nil {
		return standardFn(request)
	}
	return 1, nil
}

func (f *fakeNetwork) CreateManualSwitch(name, bridge string) (*networkModels.ManualSwitch, error) {
	f.mu.Lock()
	f.manualCalls = append(f.manualCalls, manualCall{name: name, bridge: bridge})
	manualFn := f.manualFn
	f.mu.Unlock()
	if manualFn != nil {
		return manualFn(name, bridge)
	}
	return &networkModels.ManualSwitch{ID: 1, Name: name, Bridge: bridge}, nil
}

func (f *fakeNetwork) standardCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.standardCalls)
}

func (f *fakeNetwork) manualCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.manualCalls)
}

func newTestEngine(t *testing.T, settings *fakeSettings, network *fakeNetwork) (*Service, string, string) {
	t.Helper()
	directory := t.TempDir()
	source := filepath.Join(directory, "bootstrap.json")
	resolver := filepath.Join(directory, "resolv.conf")
	service := NewService(settings, network)
	service.startupPath = source
	service.resolverPath = resolver
	return service, source, resolver
}

func writeSource(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
}

func mustFindItem(t *testing.T, report Report, kind string, index int) systemServiceInterfaces.BootstrapItemResult {
	t.Helper()
	for _, item := range report.Items {
		if item.Kind == kind && item.Index == index {
			return item
		}
	}
	t.Fatalf("missing %s[%d] in report %+v", kind, index, report.Items)
	return systemServiceInterfaces.BootstrapItemResult{}
}

func echoSettings(warningServices map[models.AvailableService]bool, restart bool) func(systemServiceInterfaces.BootstrapSettingsRequest) (systemServiceInterfaces.BootstrapSettingsResult, error) {
	return func(request systemServiceInterfaces.BootstrapSettingsRequest) (systemServiceInterfaces.BootstrapSettingsResult, error) {
		result := systemServiceInterfaces.BootstrapSettingsResult{Items: []systemServiceInterfaces.BootstrapItemResult{}}
		for index, pool := range request.Pools {
			result.Items = append(result.Items, itemResult("pool", index, pool, systemServiceInterfaces.BootstrapApplied, ""))
		}
		for index, service := range request.Services {
			status := systemServiceInterfaces.BootstrapApplied
			message := ""
			if warningServices[service] {
				status = systemServiceInterfaces.BootstrapWarning
				message = "precheck_failed_service_not_enabled"
			}
			result.Items = append(result.Items, itemResult("service", index, string(service), status, message))
		}
		if request.Initialized {
			result.Items = append(result.Items, itemResult("initialized", 0, "initialized", systemServiceInterfaces.BootstrapApplied, ""))
		}
		result.RestartRequired = restart
		return result, nil
	}
}

func TestApplyRejectsEnvelopeBeforeAnyMutation(t *testing.T) {
	cases := []struct {
		name string
		doc  string
	}{
		{"empty", ``},
		{"null root", `null`},
		{"array root", `[]`},
		{"string root", `"hello"`},
		{"missing version", `{"services":["jails"],"switches":[{"type":"manual","manual":{"name":"a","bridge":"b"}}]}`},
		{"version two", `{"version":2}`},
		{"version string", `{"version":"1"}`},
		{"version float", `{"version":1.0}`},
		{"services object", `{"version":1,"services":{}}`},
		{"pools object", `{"version":1,"pools":{}}`},
		{"dns object", `{"version":1,"dns":{}}`},
		{"switches object", `{"version":1,"switches":{}}`},
		{"initialized string", `{"version":1,"initialized":"yes"}`},
		{"initialized null with valid siblings", `{"version":1,"initialized":null,"services":["jails"],"pools":["tank"],"switches":[{"type":"manual","manual":{"name":"a","bridge":"b"}}]}`},
		{"services null with valid siblings", `{"version":1,"services":null,"initialized":true}`},
		{"pools null with valid siblings", `{"version":1,"pools":null,"services":["jails"]}`},
		{"switches null with valid siblings", `{"version":1,"switches":null,"services":["jails"]}`},
		{"dns null with valid siblings", `{"version":1,"dns":null,"services":["jails"]}`},
		{"dns bad ip", `{"version":1,"dns":["not-an-ip"]}`},
		{"dns non string", `{"version":1,"dns":[123]}`},
		{"malformed json", `{"version":1,`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			settings := &fakeSettings{}
			network := &fakeNetwork{}
			engine, source, resolver := newTestEngine(t, settings, network)
			writeSource(t, source, testCase.doc)

			report := engine.Apply(context.Background(), source)

			if report.DocumentError == "" {
				t.Fatalf("expected DocumentError, got %+v", report)
			}
			if !report.Failed() {
				t.Fatalf("expected Failed report, got %+v", report)
			}
			if report.Archived {
				t.Fatalf("document must not be archived: %+v", report)
			}
			if settings.callCount() != 0 || network.standardCount() != 0 || network.manualCount() != 0 {
				t.Fatalf("envelope failure caused mutations: settings=%d standard=%d manual=%d",
					settings.callCount(), network.standardCount(), network.manualCount())
			}
			if _, err := os.Stat(source); err != nil {
				t.Fatalf("source must be retained: %v", err)
			}
			if _, err := os.Stat(resolver); err == nil {
				t.Fatalf("resolver must not be created by a rejected document")
			}
		})
	}
}

func TestApplyPartialEntriesContinueAndPreserveOriginalIndices(t *testing.T) {
	document := `{
		"version": 1,
		"services": ["jails", 123, "bogus-service", "wireguard"],
		"pools": ["tank", 5, ""],
		"switches": [
			{"type":"standard","standard":{"name":"good","ports":["em0"],"bridgeMac":{"mode":"port","port":"em0"}}},
			{"type":"standard","manual":{"name":"mismatch","bridge":"bridge0"}},
			{"type":"manual","manual":{"name":"uplink","bridge":"bridge0"}}
		]
	}`

	settings := &fakeSettings{respond: echoSettings(nil, false)}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)
	writeSource(t, source, document)

	report := engine.Apply(context.Background(), source)

	if !report.Failed() {
		t.Fatalf("partial document must report failure: %+v", report)
	}
	if report.Archived {
		t.Fatalf("partial document must not be archived: %+v", report)
	}

	if settings.callCount() != 1 {
		t.Fatalf("settings call count = %d", settings.callCount())
	}
	request := settings.calls[0]
	if request.Initialized {
		t.Fatal("absent initialized must remain false")
	}
	if strings.Join(request.Pools, ",") != "tank" {
		t.Fatalf("settings pools = %v", request.Pools)
	}
	if len(request.Services) != 2 || request.Services[0] != models.Jails || request.Services[1] != models.WireGuard {
		t.Fatalf("settings services = %v", request.Services)
	}

	if item := mustFindItem(t, report, "service", 0); item.Status != systemServiceInterfaces.BootstrapApplied {
		t.Fatalf("service[0] = %+v", item)
	}
	if item := mustFindItem(t, report, "service", 1); item.Status != systemServiceInterfaces.BootstrapFailed {
		t.Fatalf("service[1] = %+v", item)
	}
	if item := mustFindItem(t, report, "service", 2); item.Status != systemServiceInterfaces.BootstrapFailed {
		t.Fatalf("service[2] = %+v", item)
	}
	if item := mustFindItem(t, report, "service", 3); item.Status != systemServiceInterfaces.BootstrapApplied || item.Name != "wireguard" {
		t.Fatalf("service[3] = %+v", item)
	}

	if item := mustFindItem(t, report, "pool", 0); item.Status != systemServiceInterfaces.BootstrapApplied {
		t.Fatalf("pool[0] = %+v", item)
	}
	for _, index := range []int{1, 2} {
		if item := mustFindItem(t, report, "pool", index); item.Status != systemServiceInterfaces.BootstrapFailed {
			t.Fatalf("pool[%d] = %+v", index, item)
		}
	}

	if item := mustFindItem(t, report, "switch", 0); item.Status != systemServiceInterfaces.BootstrapApplied || item.Name != "good" {
		t.Fatalf("switch[0] = %+v", item)
	}
	if item := mustFindItem(t, report, "switch", 1); item.Status != systemServiceInterfaces.BootstrapFailed {
		t.Fatalf("switch[1] = %+v", item)
	}
	if item := mustFindItem(t, report, "switch", 2); item.Status != systemServiceInterfaces.BootstrapApplied || item.Name != "uplink" {
		t.Fatalf("switch[2] = %+v", item)
	}

	if network.standardCount() != 1 || network.manualCount() != 1 {
		t.Fatalf("network calls standard=%d manual=%d", network.standardCount(), network.manualCount())
	}
	if network.standardCalls[0].Name != "good" {
		t.Fatalf("standard request = %+v", network.standardCalls[0])
	}
	if network.manualCalls[0].name != "uplink" || network.manualCalls[0].bridge != "bridge0" {
		t.Fatalf("manual request = %+v", network.manualCalls[0])
	}
}

func TestApplyWarningsDoNotBlockArchive(t *testing.T) {
	settings := &fakeSettings{respond: echoSettings(map[models.AvailableService]bool{models.Jails: true}, false)}
	network := &fakeNetwork{}
	engine, source, resolver := newTestEngine(t, settings, network)
	writeSource(t, source, `{"version":1,"services":["jails"]}`)
	if err := os.WriteFile(resolver, []byte("nameserver 9.9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report := engine.Apply(context.Background(), source)

	if report.Failed() {
		t.Fatalf("warning must not fail the report: %+v", report)
	}
	if !report.Archived {
		t.Fatalf("warning must allow archive: %+v", report)
	}
	item := mustFindItem(t, report, "service", 0)
	if item.Status != systemServiceInterfaces.BootstrapWarning {
		t.Fatalf("service[0] = %+v", item)
	}
	content, err := os.ReadFile(resolver)
	if err != nil || string(content) != "nameserver 9.9.9.9\n" {
		t.Fatalf("resolver changed without dns: %q error=%v", content, err)
	}
}

func TestApplyRestartRequiredSurvivesPartialFailure(t *testing.T) {
	settings := &fakeSettings{respond: echoSettings(nil, true)}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)
	writeSource(t, source, `{"version":1,"initialized":true,"switches":[{"type":"not-real"}]}`)

	report := engine.Apply(context.Background(), source)

	if !report.RestartRequired {
		t.Fatalf("restart requirement lost: %+v", report)
	}
	if !report.Failed() || report.Archived {
		t.Fatalf("partial apply must fail and retain file: %+v", report)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source retained: %v", err)
	}
}

func TestApplyArchiveRenameFailureRetainsFile(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)
	writeSource(t, source, `{"version":1}`)

	renameErr := errors.New("simulated rename failure")
	engine.renamePath = func(_, _ string) error { return renameErr }

	report := engine.Apply(context.Background(), source)

	if report.ArchiveError == "" || !report.Failed() {
		t.Fatalf("rename failure not reported: %+v", report)
	}
	if report.Archived {
		t.Fatalf("archived flag set on rename failure: %+v", report)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source must survive rename failure: %v", err)
	}
	if _, err := os.Stat(source + archiveSuffix); err == nil {
		t.Fatal("archive must not exist after rename failure")
	}
}

func TestApplyStartupMissingFileIsNoOpAndOnce(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)

	report := engine.ApplyStartup(context.Background())
	if report.Failed() || report.Archived || report.DocumentError != "" {
		t.Fatalf("missing startup file must be a clean no-op: %+v", report)
	}
	if settings.callCount() != 0 || network.standardCount() != 0 {
		t.Fatal("missing startup file caused mutations")
	}

	writeSource(t, source, `{"version":1}`)
	second := engine.ApplyStartup(context.Background())
	if second.Failed() || second.Archived {
		t.Fatalf("second startup apply must be guarded: %+v", second)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("guarded startup must not touch source: %v", err)
	}
}

func TestExplicitApplyAlwaysAttemptsDespiteStartupGuard(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)
	writeSource(t, source, `{"version":1}`)

	first := engine.ApplyStartup(context.Background())
	if first.Failed() || !first.Archived {
		t.Fatalf("startup apply = %+v", first)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source should be archived, stat err = %v", err)
	}

	writeSource(t, source, `{"version":1}`)
	second := engine.Apply(context.Background(), source)
	if second.Failed() || !second.Archived {
		t.Fatalf("explicit apply after guard = %+v", second)
	}
}

func TestStartupGuardDoesNotBlockExplicitAfterParseFailure(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)
	writeSource(t, source, `{invalid`)

	first := engine.ApplyStartup(context.Background())
	if first.DocumentError == "" {
		t.Fatalf("malformed startup document = %+v", first)
	}

	writeSource(t, source, `{"version":1}`)
	second := engine.Apply(context.Background(), source)
	if second.Failed() || !second.Archived {
		t.Fatalf("explicit retry after startup failure = %+v", second)
	}
}

func TestApplySourceModificationIsNeverArchived(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, resolver := newTestEngine(t, settings, network)
	writeSource(t, source, `{"version":1,"dns":["1.1.1.1"],"switches":[{"type":"standard","standard":{"name":"good","ports":["em0"],"bridgeMac":{"mode":"port","port":"em0"}}}]}`)

	network.standardFn = func(networkServiceInterfaces.CreateStandardSwitchRequest) (uint, error) {
		writeSource(t, source, `{"version":1,"dns":["1.1.1.1"],"switches":[{"type":"standard","standard":{"name":"good","ports":["em0"],"bridgeMac":{"mode":"port","port":"em0"}}}]} `)
		return 1, nil
	}

	writeCalls := 0
	engine.writeResolver = func(string, []byte, os.FileMode) error { writeCalls++; return nil }

	report := engine.Apply(context.Background(), source)

	if report.GeneralError == "" || !report.Failed() {
		t.Fatalf("modified source not detected: %+v", report)
	}
	if report.Archived {
		t.Fatalf("modified source archived: %+v", report)
	}
	if writeCalls != 0 {
		t.Fatalf("resolver written despite source change: %d", writeCalls)
	}
	if item := mustFindItem(t, report, "dns", 0); item.Status != systemServiceInterfaces.BootstrapDeferred {
		t.Fatalf("dns item = %+v", item)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("modified source retained: %v", err)
	}
	if _, err := os.Stat(resolver); err == nil {
		t.Fatal("resolver must not exist")
	}
}

func TestApplySourceReplacementDetectedByIdentity(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)
	writeSource(t, source, `{"version":1,"switches":[{"type":"standard","standard":{"name":"good","ports":["em0"],"bridgeMac":{"mode":"port","port":"em0"}}}]}`)

	network.standardFn = func(networkServiceInterfaces.CreateStandardSwitchRequest) (uint, error) {
		replacement := source + ".replacement"
		writeSource(t, replacement, `{"version":1,"switches":[{"type":"standard","standard":{"name":"good","ports":["em0"],"bridgeMac":{"mode":"port","port":"em0"}}}]}`)
		if err := os.Rename(replacement, source); err != nil {
			t.Fatalf("replace source: %v", err)
		}
		return 1, nil
	}

	report := engine.Apply(context.Background(), source)

	if !strings.Contains(report.GeneralError, "replaced") {
		t.Fatalf("replacement not detected: %+v", report)
	}
	if report.Archived {
		t.Fatalf("replaced source archived: %+v", report)
	}
}

func TestApplyWithoutDNSNeverTouchesResolver(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, resolver := newTestEngine(t, settings, network)
	writeSource(t, source, `{"version":1}`)
	if err := os.WriteFile(resolver, []byte("nameserver 9.9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	readCalls := 0
	baseRead := engine.readResolver
	engine.readResolver = func(path string) ([]byte, error) {
		if path == resolver {
			readCalls++
		}
		return baseRead(path)
	}
	writeCalls := 0
	engine.writeResolver = func(string, []byte, os.FileMode) error { writeCalls++; return nil }

	report := engine.Apply(context.Background(), source)

	if report.Failed() || !report.Archived {
		t.Fatalf("clean apply = %+v", report)
	}
	if readCalls != 0 || writeCalls != 0 {
		t.Fatalf("resolver touched without dns: reads=%d writes=%d", readCalls, writeCalls)
	}
	content, err := os.ReadFile(resolver)
	if err != nil || string(content) != "nameserver 9.9.9.9\n" {
		t.Fatalf("resolver content changed: %q error=%v", content, err)
	}
}

func TestApplyDNSDeferredWhenOrdinaryItemFails(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, resolver := newTestEngine(t, settings, network)
	writeSource(t, source, `{"version":1,"dns":["1.1.1.1"],"switches":[{"type":"nope"}]}`)
	if err := os.WriteFile(resolver, []byte("nameserver 9.9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeCalls := 0
	engine.writeResolver = func(string, []byte, os.FileMode) error { writeCalls++; return nil }

	report := engine.Apply(context.Background(), source)

	if !report.Failed() || report.Archived {
		t.Fatalf("failing document = %+v", report)
	}
	item := mustFindItem(t, report, "dns", 0)
	if item.Status != systemServiceInterfaces.BootstrapDeferred {
		t.Fatalf("dns item = %+v", item)
	}
	if writeCalls != 0 {
		t.Fatalf("deferred DNS wrote resolver %d times", writeCalls)
	}
	content, _ := os.ReadFile(resolver)
	if string(content) != "nameserver 9.9.9.9\n" {
		t.Fatalf("deferred DNS modified resolver: %q", content)
	}
}

func TestApplyDNSReplacesNameserversAndPreservesOtherLines(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, resolver := newTestEngine(t, settings, network)
	writeSource(t, source, `{"version":1,"dns":["1.1.1.1","2606:4700:4700::1111"]}`)
	original := "# managed by hand\nsearch example.com\nnameserver 8.8.8.8\nnameserver 8.8.4.4\noptions edns0\n"
	if err := os.WriteFile(resolver, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(resolver, 0o640); err != nil {
		t.Fatal(err)
	}
	originalFile, err := os.Open(resolver)
	if err != nil {
		t.Fatal(err)
	}
	defer originalFile.Close()

	report := engine.Apply(context.Background(), source)

	if report.Failed() || !report.Archived {
		t.Fatalf("dns apply = %+v", report)
	}
	if item := mustFindItem(t, report, "dns", 0); item.Status != systemServiceInterfaces.BootstrapApplied {
		t.Fatalf("dns item = %+v", item)
	}
	content, err := os.ReadFile(resolver)
	if err != nil {
		t.Fatal(err)
	}
	got := string(content)
	for _, preserved := range []string{"# managed by hand", "search example.com", "options edns0"} {
		if !strings.Contains(got, preserved) {
			t.Fatalf("resolver lost %q: %q", preserved, got)
		}
	}
	if strings.Contains(got, "8.8.8.8") || strings.Contains(got, "8.8.4.4") {
		t.Fatalf("old nameservers retained: %q", got)
	}
	expected := "# managed by hand\nsearch example.com\nnameserver 1.1.1.1\nnameserver 2606:4700:4700::1111\noptions edns0\n"
	if got != expected {
		t.Fatalf("resolver = %q, want %q", got, expected)
	}
	info, err := os.Stat(resolver)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("resolver permissions not preserved: info=%v error=%v", info, err)
	}
	previous, err := io.ReadAll(originalFile)
	if err != nil || string(previous) != original {
		t.Fatalf("resolver was overwritten in place: %q error=%v", previous, err)
	}
}

func TestApplyDNSCreatesMissingResolver(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, resolver := newTestEngine(t, settings, network)
	writeSource(t, source, `{"version":1,"dns":["1.1.1.1"]}`)

	report := engine.Apply(context.Background(), source)

	if report.Failed() || !report.Archived {
		t.Fatalf("dns apply = %+v", report)
	}
	content, err := os.ReadFile(resolver)
	if err != nil {
		t.Fatalf("resolver not created: %v", err)
	}
	if string(content) != "nameserver 1.1.1.1\n" {
		t.Fatalf("resolver = %q", content)
	}
}

func TestRewriteResolver(t *testing.T) {
	cases := []struct {
		name     string
		existing string
		servers  []string
		want     string
	}{
		{"empty", "", []string{"1.1.1.1"}, "nameserver 1.1.1.1\n"},
		{"append when absent", "search x\n", []string{"1.1.1.1"}, "search x\nnameserver 1.1.1.1\n"},
		{"replace in place", "a\nnameserver 8.8.8.8\nb\n", []string{"1.1.1.1", "2.2.2.2"}, "a\nnameserver 1.1.1.1\nnameserver 2.2.2.2\nb\n"},
		{"comment untouched", "#nameserver 8.8.8.8\nnameserver 8.8.8.8\n", []string{"1.1.1.1"}, "#nameserver 8.8.8.8\nnameserver 1.1.1.1\n"},
		{"no trailing newline preserved", "search x", []string{"1.1.1.1"}, "search x\nnameserver 1.1.1.1\n"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := string(rewriteResolver([]byte(testCase.existing), testCase.servers))
			if got != testCase.want {
				t.Fatalf("rewriteResolver = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestApplyRejectsInvalidSwitchDiscriminators(t *testing.T) {
	validStandard := `{"name":"good","ports":["em0"],"bridgeMac":{"mode":"port","port":"em0"}}`
	cases := []struct {
		name  string
		entry string
	}{
		{"missing type", `{"standard":` + validStandard + `}`},
		{"unknown type", `{"type":"bogus","standard":` + validStandard + `}`},
		{"missing payload", `{"type":"standard"}`},
		{"standard payload explicit null", `{"type":"standard","standard":null}`},
		{"manual payload explicit null", `{"type":"manual","manual":null}`},
		{"both payloads", `{"type":"standard","standard":` + validStandard + `,"manual":{"name":"x","bridge":"y"}}`},
		{"opposite manual explicit null", `{"type":"standard","standard":` + validStandard + `,"manual":null}`},
		{"opposite standard explicit null", `{"type":"manual","manual":{"name":"x","bridge":"y"},"standard":null}`},
		{"mismatched standard", `{"type":"standard","manual":{"name":"x","bridge":"y"}}`},
		{"mismatched manual", `{"type":"manual","standard":` + validStandard + `}`},
		{"display only json", `{"type":"standard","standard":` + validStandard + `,"json":true}`},
		{"non object", `"not-an-object"`},
		{"array entry", `[]`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			settings := &fakeSettings{}
			network := &fakeNetwork{}
			engine, source, _ := newTestEngine(t, settings, network)
			writeSource(t, source, `{"version":1,"switches":[`+testCase.entry+`]}`)

			report := engine.Apply(context.Background(), source)

			if !report.Failed() || report.Archived {
				t.Fatalf("invalid switch accepted: %+v", report)
			}
			item := mustFindItem(t, report, "switch", 0)
			if item.Status != systemServiceInterfaces.BootstrapFailed {
				t.Fatalf("switch item = %+v", item)
			}
			if network.standardCount() != 0 || network.manualCount() != 0 {
				t.Fatalf("invalid switch reached network: standard=%d manual=%d",
					network.standardCount(), network.manualCount())
			}
		})
	}
}

func TestApplyRoutesStandardAndManualSwitches(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)
	writeSource(t, source, `{
		"version": 1,
		"switches": [
			{"type":"standard","standard":{
				"name":" good ",
				"mtu":1500,
				"vlan":42,
				"network4Manual":"192.0.2.10/24",
				"gateway4Manual":"192.0.2.1",
				"disableIPv6":false,
				"slaac":true,
				"ports":["em0"],
				"bridgeMac":{"mode":"port","port":"em0"},
				"private":true,
				"defaultRoute":true
			}},
			{"type":"manual","manual":{"name":" uplink ","bridge":" bridge0 "}}
		]
	}`)

	report := engine.Apply(context.Background(), source)

	if report.Failed() || !report.Archived {
		t.Fatalf("valid switches = %+v", report)
	}
	if network.standardCount() != 1 || network.manualCount() != 1 {
		t.Fatalf("network calls standard=%d manual=%d", network.standardCount(), network.manualCount())
	}
	standard := network.standardCalls[0]
	if standard.Name != "good" || standard.VLAN != 42 || !standard.Private || !standard.DefaultRoute {
		t.Fatalf("standard request = %+v", standard)
	}
	if standard.MACSource.Mode != "port" || standard.MACSource.Port != "em0" {
		t.Fatalf("standard MAC source = %+v", standard.MACSource)
	}
	if standard.Manual.Network4 != "192.0.2.10/24" || standard.Manual.Gateway4 != "192.0.2.1" {
		t.Fatalf("standard manual addresses = %+v", standard.Manual)
	}
	if !standard.SLAAC {
		t.Fatal("standard SLAAC not carried through")
	}
	manual := network.manualCalls[0]
	if manual.name != "uplink" || manual.bridge != "bridge0" {
		t.Fatalf("manual request = %+v", manual)
	}
}

func TestApplySettingsAbsentOrFalseInitializedNeverDeinitializes(t *testing.T) {
	settings := &fakeSettings{respond: echoSettings(nil, false)}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)

	writeSource(t, source, `{"version":1,"initialized":false}`)
	report := engine.Apply(context.Background(), source)
	if report.Failed() || !report.Archived {
		t.Fatalf("false initialized = %+v", report)
	}
	if settings.callCount() != 0 {
		t.Fatalf("no settings work should be dispatched for a lone false initialized: %d", settings.callCount())
	}
	if report.RestartRequired {
		t.Fatalf("false initialized requested restart: %+v", report)
	}
}

func TestApplySettingsNilApplierIsReported(t *testing.T) {
	network := &fakeNetwork{}
	engine := NewService(nil, network)
	engine.startupPath = filepath.Join(t.TempDir(), "bootstrap.json")
	engine.resolverPath = filepath.Join(t.TempDir(), "resolv.conf")
	source := engine.startupPath
	writeSource(t, source, `{"version":1,"services":["jails"]}`)

	report := engine.Apply(context.Background(), source)

	if report.GeneralError == "" || !report.Failed() || report.Archived {
		t.Fatalf("nil settings applier = %+v", report)
	}
}

func TestApplyNilNetworkFailsSwitchesOnly(t *testing.T) {
	settings := &fakeSettings{}
	engine := NewService(settings, nil)
	engine.startupPath = filepath.Join(t.TempDir(), "bootstrap.json")
	engine.resolverPath = filepath.Join(t.TempDir(), "resolv.conf")
	source := engine.startupPath
	writeSource(t, source, `{"version":1,"switches":[{"type":"manual","manual":{"name":"uplink","bridge":"bridge0"}}]}`)

	report := engine.Apply(context.Background(), source)

	item := mustFindItem(t, report, "switch", 0)
	if item.Status != systemServiceInterfaces.BootstrapFailed {
		t.Fatalf("switch item = %+v", item)
	}
	if !report.Failed() || report.Archived {
		t.Fatalf("nil network = %+v", report)
	}
}

func TestApplyRejectsNonRegularAndOversizedSources(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		settings := &fakeSettings{}
		network := &fakeNetwork{}
		engine, source, _ := newTestEngine(t, settings, network)
		if err := os.Mkdir(source, 0o700); err != nil {
			t.Fatal(err)
		}
		report := engine.Apply(context.Background(), source)
		if report.GeneralError == "" || !report.Failed() {
			t.Fatalf("directory source = %+v", report)
		}
	})

	t.Run("oversized", func(t *testing.T) {
		settings := &fakeSettings{}
		network := &fakeNetwork{}
		engine, source, _ := newTestEngine(t, settings, network)
		oversized := make([]byte, maxDocumentBytes+1)
		for index := range oversized {
			oversized[index] = ' '
		}
		oversized[0] = '{'
		if err := os.WriteFile(source, oversized, 0o600); err != nil {
			t.Fatal(err)
		}
		report := engine.Apply(context.Background(), source)
		if report.GeneralError == "" || !report.Failed() || report.Archived {
			t.Fatalf("oversized source = %+v", report)
		}
	})
}

func TestApplyCanceledContextMakesNoChanges(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)
	writeSource(t, source, `{"version":1,"services":["jails"]}`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	report := engine.Apply(ctx, source)

	if report.GeneralError == "" || !report.Failed() {
		t.Fatalf("canceled context = %+v", report)
	}
	if settings.callCount() != 0 {
		t.Fatal("canceled context reached settings")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source retained: %v", err)
	}
}

func TestApplyMissingExplicitSourceDoesNotResurrectArchive(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)
	writeSource(t, source, `{"version":1}`)

	first := engine.Apply(context.Background(), source)
	if first.Failed() || !first.Archived {
		t.Fatalf("first apply = %+v", first)
	}
	if _, err := os.Stat(source + archiveSuffix); err != nil {
		t.Fatalf("archive missing: %v", err)
	}

	settings2 := &fakeSettings{}
	network2 := &fakeNetwork{}
	fresh, freshSource, _ := newTestEngine(t, settings2, network2)
	fresh.startupPath = source
	freshSource = source

	startup := fresh.ApplyStartup(context.Background())
	if startup.Failed() || startup.Archived || len(startup.Items) != 0 {
		t.Fatalf("fresh startup = %+v", startup)
	}
	explicit := fresh.Apply(context.Background(), freshSource)
	if explicit.DocumentError == "" || !explicit.Failed() || explicit.Archived {
		t.Fatalf("explicit missing source = %+v", explicit)
	}
	if settings2.callCount() != 0 || network2.standardCount() != 0 || network2.manualCount() != 0 {
		t.Fatal("missing source triggered domain work")
	}
}

func TestApplyCorrectedDocumentAfterPartialFailureArchives(t *testing.T) {
	settings := &fakeSettings{respond: echoSettings(nil, false)}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)

	writeSource(t, source, `{"version":1,"switches":[{"type":"bad"},{"type":"manual","manual":{"name":"uplink","bridge":"bridge0"}}]}`)
	first := engine.Apply(context.Background(), source)
	if !first.Failed() || first.Archived {
		t.Fatalf("partial apply = %+v", first)
	}
	if network.manualCount() != 1 {
		t.Fatalf("successful manual switch not attempted: %d", network.manualCount())
	}

	writeSource(t, source, `{"version":1,"switches":[{"type":"manual","manual":{"name":"uplink","bridge":"bridge0"}}]}`)
	second := engine.Apply(context.Background(), source)
	if second.Failed() || !second.Archived {
		t.Fatalf("corrected apply = %+v", second)
	}
}

func TestApplyDNSFailureRetainsFileAndPermitsRetry(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, resolver := newTestEngine(t, settings, network)
	writeSource(t, source, `{"version":1,"dns":["1.1.1.1"]}`)
	if err := os.WriteFile(resolver, []byte("nameserver 8.8.8.8\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeResolver := engine.writeResolver
	engine.writeResolver = func(string, []byte, os.FileMode) error { return errors.New("simulated resolver failure") }

	first := engine.Apply(context.Background(), source)
	if !first.Failed() || first.Archived {
		t.Fatalf("dns failure = %+v", first)
	}
	if item := mustFindItem(t, first, "dns", 0); item.Status != systemServiceInterfaces.BootstrapFailed {
		t.Fatalf("dns item = %+v", item)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source retained after dns failure: %v", err)
	}

	engine.writeResolver = writeResolver
	second := engine.Apply(context.Background(), source)
	if second.Failed() || !second.Archived {
		t.Fatalf("dns retry = %+v", second)
	}
	content, err := os.ReadFile(resolver)
	if err != nil || string(content) != "nameserver 1.1.1.1\n" {
		t.Fatalf("resolver after retry = %q error=%v", content, err)
	}
}

func TestApplyInitializedOnlySetsAndArchives(t *testing.T) {
	settings := &fakeSettings{respond: echoSettings(nil, true)}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)
	writeSource(t, source, `{"version":1,"initialized":true}`)

	report := engine.Apply(context.Background(), source)

	if report.Failed() || !report.Archived || !report.RestartRequired {
		t.Fatalf("initialized-only apply = %+v", report)
	}
	if settings.callCount() != 1 || !settings.calls[0].Initialized {
		t.Fatalf("settings request = %+v", settings.calls)
	}
	if item := mustFindItem(t, report, "initialized", 0); item.Status != systemServiceInterfaces.BootstrapApplied {
		t.Fatalf("initialized item = %+v", item)
	}
}

func TestReadSourceRejectsReplacementAtOpenBoundary(t *testing.T) {
	directory := t.TempDir()
	snapshotPath := filepath.Join(directory, "snapshot.json")
	openedPath := filepath.Join(directory, "opened.json")
	writeSource(t, snapshotPath, `{"version":1}`)
	writeSource(t, openedPath, `{"version":1}`)

	engine := NewService(&fakeSettings{}, &fakeNetwork{})
	engine.lstatPath = func(string) (os.FileInfo, error) { return os.Lstat(snapshotPath) }
	engine.openPath = func(string) (openedSource, error) { return os.Open(openedPath) }

	if _, _, err := engine.readSource(snapshotPath); !errors.Is(err, errSourceReplaced) {
		t.Fatalf("expected replacement at open boundary, got %v", err)
	}
}

func TestReadSourceRejectsSymlinkAtOpenBoundary(t *testing.T) {
	directory := t.TempDir()
	snapshotPath := filepath.Join(directory, "snapshot.json")
	targetPath := filepath.Join(directory, "target.json")
	linkPath := filepath.Join(directory, "link.json")
	writeSource(t, snapshotPath, `{"version":1}`)
	writeSource(t, targetPath, `{"version":1}`)
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Fatal(err)
	}

	engine := NewService(&fakeSettings{}, &fakeNetwork{})
	engine.lstatPath = func(string) (os.FileInfo, error) { return os.Lstat(snapshotPath) }

	if _, _, err := engine.readSource(linkPath); err == nil {
		t.Fatal("no-follow open must reject a symlink source")
	}

	engine.openPath = func(string) (openedSource, error) { return os.Open(linkPath) }
	if _, _, err := engine.readSource(linkPath); !errors.Is(err, errSourceReplaced) {
		t.Fatalf("expected identity mismatch for symlink, got %v", err)
	}
}

func TestReadSourceRejectsFIFOWithoutBlocking(t *testing.T) {
	directory := t.TempDir()
	snapshotPath := filepath.Join(directory, "snapshot.json")
	fifoPath := filepath.Join(directory, "fifo.json")
	writeSource(t, snapshotPath, `{"version":1}`)
	if err := unix.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatal(err)
	}

	engine := NewService(&fakeSettings{}, &fakeNetwork{})
	engine.lstatPath = func(string) (os.FileInfo, error) { return os.Lstat(snapshotPath) }

	done := make(chan error, 1)
	go func() {
		_, _, err := engine.readSource(fifoPath)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO source must be rejected")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readSource blocked opening a FIFO; O_NONBLOCK is required")
	}
}

func TestArchiveReplacementAtClaimBoundaryIsNotConsumed(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)
	writeSource(t, source, `{"version":1}`)
	replacement := `{"version":1,"dns":["1.1.1.1"]}`

	engine.linkPath = func(oldPath, newPath string) error {
		replacementPath := source + ".replacement"
		writeSource(t, replacementPath, replacement)
		if err := os.Rename(replacementPath, source); err != nil {
			return err
		}
		return os.Link(oldPath, newPath)
	}

	report := engine.Apply(context.Background(), source)

	if report.Archived {
		t.Fatalf("replacement must not be archived: %+v", report)
	}
	if !report.Failed() || !strings.Contains(report.GeneralError, "replaced") {
		t.Fatalf("replacement not reported: %+v", report)
	}
	if _, err := os.Stat(source + archiveSuffix); err == nil {
		t.Fatal("archive must not exist when the claim was replaced")
	}
	content, err := os.ReadFile(source)
	if err != nil || string(content) != replacement {
		t.Fatalf("replacement must remain at the original path: %q error=%v", content, err)
	}
	if claims, _ := filepath.Glob(source + archiveSuffix + ".claim-*"); len(claims) != 0 {
		t.Fatalf("claim hard link leaked: %v", claims)
	}
}

func TestArchiveReplacementDuringRenameIsNotConsumed(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)
	original := `{"version":1}`
	replacement := `{"version":1,"dns":["1.1.1.1"]}`
	writeSource(t, source, original)

	engine.renamePath = func(oldPath, newPath string) error {
		replacementPath := source + ".replacement"
		writeSource(t, replacementPath, replacement)
		if err := os.Rename(replacementPath, source); err != nil {
			return err
		}
		return os.Rename(oldPath, newPath)
	}

	report := engine.Apply(context.Background(), source)

	content, err := os.ReadFile(source)
	if err != nil || string(content) != replacement {
		t.Fatalf("replacement must remain at the original path: %q error=%v", content, err)
	}
	if report.Archived {
		t.Fatalf("replaced source must not report Archived: %+v", report)
	}
	if !report.Failed() || !strings.Contains(report.GeneralError, "replaced") {
		t.Fatalf("replacement during archive not reported: %+v", report)
	}
	archived, err := os.ReadFile(source + archiveSuffix)
	if err != nil || string(archived) != original {
		t.Fatalf("archive must hold the verified source, not the replacement: %q error=%v", archived, err)
	}
}

func TestConsumeVerifiedSourceUnlinksMatchingVnode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.json")
	writeSource(t, path, `{"version":1}`)

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	if err := consumeVerifiedSource(path, int(file.Fd())); err != nil {
		t.Fatalf("matching vnode unlink: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("matching vnode not unlinked: %v", err)
	}
}

func TestConsumeVerifiedSourcePreservesDifferentVnode(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.json")
	other := filepath.Join(directory, "other.json")
	writeSource(t, source, `{"version":1}`)
	writeSource(t, other, `{"version":1}`)

	file, err := os.Open(other)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	err = consumeVerifiedSource(source, int(file.Fd()))
	if !errors.Is(err, syscall.EDEADLK) {
		t.Fatalf("want EDEADLK for a different vnode, got %v", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("different vnode must be preserved: %v", err)
	}
}

func TestArchiveReplacementBeforeConditionalUnlinkPreservesReplacement(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)
	original := `{"version":1}`
	replacement := `{"version":1,"dns":["1.1.1.1"]}`
	writeSource(t, source, original)

	injected := false
	engine.consumeSource = func(path string, fd int) error {
		replacementPath := source + ".replacement"
		writeSource(t, replacementPath, replacement)
		if err := os.Rename(replacementPath, source); err != nil {
			return err
		}
		injected = true
		return consumeVerifiedSource(path, fd)
	}

	report := engine.Apply(context.Background(), source)

	if !injected {
		t.Fatal("conditional unlink hook was not invoked")
	}
	if report.Archived {
		t.Fatalf("replacement must not be consumed: %+v", report)
	}
	if !report.Failed() || !strings.Contains(report.GeneralError, "replaced") {
		t.Fatalf("replacement not reported: %+v", report)
	}
	content, err := os.ReadFile(source)
	if err != nil || string(content) != replacement {
		t.Fatalf("replacement must remain at the original path: %q error=%v", content, err)
	}
	archived, err := os.ReadFile(source + archiveSuffix)
	if err != nil || string(archived) != original {
		t.Fatalf("verified source must remain recoverable in the archive: %q error=%v", archived, err)
	}
}

func TestApplySettingsGlobalErrorFillsMissingOutcomes(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)
	writeSource(t, source, `{
		"version": 1,
		"initialized": true,
		"services": ["jails", 5, "wireguard"],
		"pools": ["tank", "backup"]
	}`)

	settings.respond = func(systemServiceInterfaces.BootstrapSettingsRequest) (systemServiceInterfaces.BootstrapSettingsResult, error) {
		result := systemServiceInterfaces.BootstrapSettingsResult{Items: []systemServiceInterfaces.BootstrapItemResult{
			itemResult("pool", 0, "tank", systemServiceInterfaces.BootstrapWarning, "precheck_failed_existing_pool_preserved"),
			itemResult("service", 1, "wireguard", systemServiceInterfaces.BootstrapApplied, ""),
		}}
		return result, errors.New("db unavailable")
	}

	report := engine.Apply(context.Background(), source)

	if !report.Failed() || report.Archived || report.DocumentError != "" {
		t.Fatalf("global settings failure = %+v", report)
	}
	if report.GeneralError != "db unavailable" {
		t.Fatalf("general error = %q", report.GeneralError)
	}

	if item := mustFindItem(t, report, "pool", 0); item.Status != systemServiceInterfaces.BootstrapWarning {
		t.Fatalf("returned pool item lost: %+v", item)
	}
	if item := mustFindItem(t, report, "service", 2); item.Status != systemServiceInterfaces.BootstrapApplied {
		t.Fatalf("returned service item lost: %+v", item)
	}

	assertFailedWithMessage(t, report, "service", 0, "db unavailable")
	assertFailedWithMessage(t, report, "pool", 1, "db unavailable")
	assertFailedWithMessage(t, report, "initialized", 0, "db unavailable")

	if item := mustFindItem(t, report, "service", 1); item.Status != systemServiceInterfaces.BootstrapFailed {
		t.Fatalf("local invalid service = %+v", item)
	}
}

func assertFailedWithMessage(t *testing.T, report Report, kind string, index int, message string) {
	t.Helper()
	item := mustFindItem(t, report, kind, index)
	if item.Status != systemServiceInterfaces.BootstrapFailed || item.Message != message {
		t.Fatalf("%s[%d] = %+v, want failed %q", kind, index, item, message)
	}
}

func TestApplyCancellationAfterSettingsStopsMutations(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)
	writeSource(t, source, `{
		"version": 1,
		"initialized": true,
		"dns": ["1.1.1.1"],
		"switches": [
			{"type":"manual","manual":{"name":"a","bridge":"bridge0"}},
			{"type":"manual","manual":{"name":"b","bridge":"bridge1"}}
		]
	}`)

	ctx, cancel := context.WithCancel(context.Background())
	settings.respond = func(systemServiceInterfaces.BootstrapSettingsRequest) (systemServiceInterfaces.BootstrapSettingsResult, error) {
		cancel()
		result := systemServiceInterfaces.BootstrapSettingsResult{Items: []systemServiceInterfaces.BootstrapItemResult{
			itemResult("initialized", 0, "initialized", systemServiceInterfaces.BootstrapApplied, ""),
		}}
		result.RestartRequired = true
		return result, nil
	}

	report := engine.Apply(ctx, source)

	if !report.RestartRequired {
		t.Fatalf("persisted restart requirement lost on cancel: %+v", report)
	}
	if report.Archived {
		t.Fatalf("archive must not run after cancellation: %+v", report)
	}
	if network.standardCount() != 0 || network.manualCount() != 0 {
		t.Fatal("network mutated after canceled settings call")
	}
	for _, index := range []int{0, 1} {
		item := mustFindItem(t, report, "switch", index)
		if item.Status != systemServiceInterfaces.BootstrapFailed || item.Message != "context_canceled" {
			t.Fatalf("switch[%d] = %+v", index, item)
		}
	}
	if item := mustFindItem(t, report, "dns", 0); item.Status != systemServiceInterfaces.BootstrapDeferred || item.Message != "context_canceled" {
		t.Fatalf("dns item = %+v", item)
	}
	if report.GeneralError == "" {
		t.Fatal("cancellation must be reported")
	}
}

func TestApplyCancellationBeforeArchiveRetainsSuccesses(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)
	writeSource(t, source, `{"version":1,"switches":[{"type":"manual","manual":{"name":"a","bridge":"bridge0"}}]}`)

	ctx, cancel := context.WithCancel(context.Background())
	network.manualFn = func(name, bridge string) (*networkModels.ManualSwitch, error) {
		cancel()
		return &networkModels.ManualSwitch{ID: 1, Name: name, Bridge: bridge}, nil
	}

	report := engine.Apply(ctx, source)

	if report.Archived {
		t.Fatalf("archive must not run after cancellation: %+v", report)
	}
	if item := mustFindItem(t, report, "switch", 0); item.Status != systemServiceInterfaces.BootstrapApplied {
		t.Fatalf("persisted switch result lost: %+v", item)
	}
	if report.GeneralError == "" || !report.Failed() {
		t.Fatalf("cancellation must be reported: %+v", report)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source retained: %v", err)
	}
}

func TestApplyCancellationDuringResolverReadDoesNotWrite(t *testing.T) {
	settings := &fakeSettings{}
	network := &fakeNetwork{}
	engine, source, resolver := newTestEngine(t, settings, network)
	writeSource(t, source, `{"version":1,"dns":["1.1.1.1"]}`)
	if err := os.WriteFile(resolver, []byte("nameserver 8.8.8.8\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	engine.readResolver = func(string) ([]byte, error) {
		cancel()
		return []byte("nameserver 8.8.8.8\n"), nil
	}
	writeCalls := 0
	engine.writeResolver = func(string, []byte, os.FileMode) error { writeCalls++; return nil }

	report := engine.Apply(ctx, source)

	if writeCalls != 0 {
		t.Fatalf("resolver written after cancellation: %d", writeCalls)
	}
	if report.Archived {
		t.Fatalf("archive must not run after cancellation: %+v", report)
	}
	if item := mustFindItem(t, report, "dns", 0); item.Status != systemServiceInterfaces.BootstrapFailed {
		t.Fatalf("dns item = %+v", item)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source retained: %v", err)
	}
}

func TestArchiveClaimCollisionRetriesWithoutRemovingStaleFile(t *testing.T) {
	engine, source, _ := newTestEngine(t, &fakeSettings{}, &fakeNetwork{})
	writeSource(t, source, `{"version":1}`)
	var stale string
	calls := 0
	engine.linkPath = func(oldPath, claim string) error {
		calls++
		if calls == 1 {
			stale = claim
			if err := os.WriteFile(claim, []byte("unrelated stale claim"), 0600); err != nil {
				return err
			}
		} else if claim == stale {
			t.Fatal("retry reused the colliding claim name")
		}
		return os.Link(oldPath, claim)
	}
	report := engine.Apply(context.Background(), source)
	if report.Failed() || !report.Archived || calls != 2 {
		t.Fatalf("claim collision apply = %+v, calls=%d", report, calls)
	}
	data, err := os.ReadFile(stale)
	if err != nil || string(data) != "unrelated stale claim" {
		t.Fatalf("colliding claim was altered: %q error=%v", data, err)
	}
}

func TestArchiveClaimFailureIsAnArchiveError(t *testing.T) {
	engine, source, _ := newTestEngine(t, &fakeSettings{}, &fakeNetwork{})
	writeSource(t, source, `{"version":1}`)
	calls := 0
	engine.linkPath = func(string, string) error { calls++; return os.ErrExist }
	report := engine.Apply(context.Background(), source)
	if !report.Failed() || report.Archived || report.ArchiveError == "" || report.GeneralError != "" || calls != 8 {
		t.Fatalf("exhausted claims apply = %+v, calls=%d", report, calls)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("failed archive consumed the source: %v", err)
	}
}

func TestAutomaticAndExplicitApplyShareSerializationLock(t *testing.T) {
	settings := &fakeSettings{}
	engine, automaticSource, _ := newTestEngine(t, settings, &fakeNetwork{})
	explicitSource := filepath.Join(t.TempDir(), "explicit.json")
	writeSource(t, automaticSource, `{"version":1,"initialized":true}`)
	writeSource(t, explicitSource, `{"version":1,"initialized":true}`)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	settings.respond = func(systemServiceInterfaces.BootstrapSettingsRequest) (systemServiceInterfaces.BootstrapSettingsResult, error) {
		if engine.mutex.TryLock() {
			engine.mutex.Unlock()
			t.Error("settings were applied without holding the engine's shared lock")
		}
		entered <- struct{}{}
		<-release
		return systemServiceInterfaces.BootstrapSettingsResult{}, nil
	}
	automaticDone := make(chan Report, 1)
	explicitDone := make(chan Report, 1)
	go func() { automaticDone <- engine.ApplyStartup(t.Context()) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("automatic apply did not enter settings")
	}
	explicitStarted := make(chan struct{})
	go func() {
		close(explicitStarted)
		explicitDone <- engine.Apply(t.Context(), explicitSource)
	}()
	<-explicitStarted
	select {
	case <-entered:
		close(release)
		t.Fatal("explicit apply entered settings while automatic apply was blocked")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	for _, done := range []<-chan Report{automaticDone, explicitDone} {
		select {
		case report := <-done:
			if report.Failed() || !report.Archived {
				t.Fatalf("serialized apply = %+v", report)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("serialized apply did not finish")
		}
	}
	if settings.callCount() != 2 {
		t.Fatalf("settings calls = %d, want both automatic and explicit applies", settings.callCount())
	}
	if repeat := engine.ApplyStartup(t.Context()); repeat.Failed() || repeat.Archived || settings.callCount() != 2 {
		t.Fatalf("automatic guard was not retained: %+v", repeat)
	}
}

func TestDocumentedBootstrapExampleParsesWithoutItemFailures(t *testing.T) {
	guide, err := os.ReadFile("../../docs/app-docs/src/content/docs/guides/node/first-boot-bootstrap/index.mdx")
	if err != nil {
		t.Fatal(err)
	}
	_, example, found := strings.Cut(string(guide), "```json\n")
	if !found {
		t.Fatal("bootstrap guide has no JSON example")
	}
	example, _, found = strings.Cut(example, "\n```")
	if !found {
		t.Fatal("bootstrap guide JSON example has no closing fence")
	}
	doc, err := parseDocument([]byte(example))
	if err != nil || doc == nil {
		t.Fatalf("invalid documented envelope: %v", err)
	}
	if len(doc.localItems) != 0 || len(doc.services) != 3 || len(doc.switches) != 2 {
		t.Fatalf("documented example has rejected or missing items: %+v", doc)
	}
	if doc.switches[0].kind != "standard" || doc.switches[0].standard.Name != "lan" ||
		doc.switches[0].standard.Network4Manual == "" || doc.switches[0].standard.Network6Manual == "" ||
		doc.switches[1].kind != "manual" || doc.switches[1].manual.Bridge != "bridge0" {
		t.Fatalf("documented switches lost fields: %+v", doc.switches)
	}
}

func TestReportFailedSemantics(t *testing.T) {
	if (Report{}).Failed() {
		t.Fatal("empty report must not be failed")
	}
	if !(Report{DocumentError: "x"}).Failed() {
		t.Fatal("document error must fail")
	}
	if !(Report{ArchiveError: "x"}).Failed() {
		t.Fatal("archive error must fail")
	}
	if !(Report{GeneralError: "x"}).Failed() {
		t.Fatal("general error must fail")
	}
	if (Report{Items: []systemServiceInterfaces.BootstrapItemResult{{Status: systemServiceInterfaces.BootstrapWarning}}}).Failed() {
		t.Fatal("warning must not fail")
	}
	if (Report{Items: []systemServiceInterfaces.BootstrapItemResult{{Status: systemServiceInterfaces.BootstrapDeferred}}}).Failed() {
		t.Fatal("deferred must not fail")
	}
	if !(Report{Items: []systemServiceInterfaces.BootstrapItemResult{{Status: systemServiceInterfaces.BootstrapFailed}}}).Failed() {
		t.Fatal("failed item must fail")
	}
}
