// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package bootstrap

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
)

const retrySwitchDocument = `{"version":1,"services":["jails"],"dns":["192.0.2.53"],"switches":[
	{"type":"standard","standard":{"name":"healthy","ports":["em0"],"bridgeMac":{"mode":"port","port":"em0"}}},
	{"type":"standard","standard":{"name":"settling","ports":["em1"],"bridgeMac":{"mode":"port","port":"em1"}}},
	{"type":"manual","manual":{"name":"external","bridge":"bridge0"}}
]}`

func TestBootstrapRetriesOnlyFailedSwitchItems(t *testing.T) {
	settings := &fakeSettings{respond: echoSettings(nil, false)}
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, settings, network)
	writeSource(t, source, retrySwitchDocument)
	calls := map[string]int{}
	network.standardFn = func(request networkServiceInterfaces.CreateStandardSwitchRequest) (uint, error) {
		if !request.PruneMissingPorts {
			t.Fatal("bootstrap did not enable missing-port pruning")
		}
		calls[request.Name]++
		if request.Name == "settling" && calls[request.Name] == 1 {
			return 0, errors.New("bridge port not visible yet")
		}
		return 1, nil
	}
	report := engine.ApplyStartup(t.Context())
	if report.Failed() || !report.Archived {
		t.Fatalf("bootstrap did not converge: %+v", report)
	}
	if calls["healthy"] != 1 || calls["settling"] != 2 || network.manualCount() != 1 {
		t.Fatalf("switch calls=%v manual=%d", calls, network.manualCount())
	}
	if settings.callCount() != 1 || len(network.dnsCalls) != 1 {
		t.Fatalf("non-switch work was replayed: settings=%d DNS=%d", settings.callCount(), len(network.dnsCalls))
	}
	status := engine.SwitchStatus()
	if status.State != "converged" || status.Attempts != 2 || len(status.Failures) != 0 || status.Error != "" {
		t.Fatalf("status = %+v", status)
	}
}

func TestBootstrapExhaustionRetainsSourceAndQueryableFailure(t *testing.T) {
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, &fakeSettings{}, network)
	writeSource(t, source, retrySwitchDocument)
	failedCalls := 0
	network.standardFn = func(request networkServiceInterfaces.CreateStandardSwitchRequest) (uint, error) {
		if request.Name == "settling" {
			failedCalls++
			return 0, errors.New("permanent port failure")
		}
		return 1, nil
	}
	report := engine.ApplyStartup(t.Context())
	status := engine.SwitchStatus()
	if !report.Failed() || report.Archived || failedCalls != 4 {
		t.Fatalf("report=%+v failedCalls=%d", report, failedCalls)
	}
	if status.State != "degraded" || status.Attempts != 4 || len(status.Failures) != 1 || status.Failures[0].Name != "settling" ||
		!strings.Contains(status.Error, "permanent port failure") || status.CompletedAt == nil {
		t.Fatalf("failure status = %+v", status)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("bootstrap source not retained: %v", err)
	}
	if item := mustFindItem(t, report, "dns", 0); item.Status != systemServiceInterfaces.BootstrapDeferred {
		t.Fatalf("DNS was not deferred: %+v", item)
	}
	// Explicit re-apply must be able to recover even though startup is guarded.
	network.standardFn = nil
	report = engine.Apply(t.Context(), source)
	status = engine.SwitchStatus()
	if report.Failed() || !report.Archived || status.State != "converged" || status.Error != "" || len(status.Failures) != 0 {
		t.Fatalf("manual recovery report=%+v status=%+v", report, status)
	}
}

func TestBootstrapRetryCancellation(t *testing.T) {
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, &fakeSettings{}, network)
	writeSource(t, source, retrySwitchDocument)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	network.standardFn = func(networkServiceInterfaces.CreateStandardSwitchRequest) (uint, error) {
		cancel()
		return 0, errors.New("transient failure")
	}
	report := engine.ApplyStartup(ctx)
	status := engine.SwitchStatus()
	if !report.Failed() || report.Archived || network.standardCount() != 1 || status.Attempts != 1 ||
		status.State != "degraded" || !strings.Contains(status.Error, "context canceled") {
		t.Fatalf("report=%+v status=%+v calls=%d", report, status, network.standardCount())
	}
}

func TestBootstrapRetryDoesNotApplyReplacedDocument(t *testing.T) {
	network := &fakeNetwork{}
	engine, source, _ := newTestEngine(t, &fakeSettings{}, network)
	writeSource(t, source, retrySwitchDocument)
	network.standardFn = func(networkServiceInterfaces.CreateStandardSwitchRequest) (uint, error) {
		writeSource(t, source, `{"version":1}`)
		return 0, errors.New("transient failure")
	}
	report := engine.ApplyStartup(t.Context())
	if !report.Failed() || report.Archived || network.standardCount() != 2 {
		t.Fatalf("old desired state was retried: report=%+v calls=%d", report, network.standardCount())
	}
	if !strings.Contains(engine.SwitchStatus().Error, "modified") {
		t.Fatalf("source modification not surfaced: %+v", engine.SwitchStatus())
	}
}
