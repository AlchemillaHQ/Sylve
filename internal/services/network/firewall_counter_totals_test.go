// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
)

func TestFirewallCountersRemainCumulativeAcrossPFReloadAndServiceRestart(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &models.BasicSettings{}, &networkModels.FirewallTrafficRule{})
	if err := db.Create(&models.BasicSettings{Services: []models.AvailableService{models.Firewall}}).Error; err != nil {
		t.Fatalf("failed to enable firewall service: %v", err)
	}

	rule := networkModels.FirewallTrafficRule{
		ID:        901,
		Name:      "counter-test",
		Visible:   true,
		Enabled:   true,
		Priority:  1,
		Action:    "pass",
		Direction: "in",
		Protocol:  "any",
		Family:    "any",
	}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatalf("failed to create traffic rule: %v", err)
	}

	packets, bytes := uint64(10), uint64(100)
	previousRunCommand := firewallRunCommand
	firewallRunCommand = func(command string, args ...string) (string, error) {
		if command != "/sbin/pfctl" || len(args) != 3 || args[0] != "-a" {
			return "", nil
		}
		switch args[1] {
		case "sylve/traffic-rules":
			return fmt.Sprintf(
				"@1 pass in from any to any label \"sylve_trf_%d\"\n  [ Evaluations: 1 Packets: %d Bytes: %d States: 0 ]",
				rule.ID,
				packets,
				bytes,
			), nil
		case "sylve/nat-rules":
			return "", nil
		default:
			return "", nil
		}
	}
	t.Cleanup(func() {
		firewallRunCommand = previousRunCommand
	})

	svc.sampleFirewallCounters()
	packets, bytes = 15, 150
	svc.sampleFirewallCounters()

	// PF counters reset when the generated rules are reloaded.
	packets, bytes = 2, 20
	svc.sampleFirewallCounters()

	totals, _ := svc.cumulativeCounterTotalsByType("traffic")
	if got := totals[rule.ID]; got.Packets != 17 || got.Bytes != 170 {
		t.Fatalf("expected cumulative total 17 packets / 170 bytes after PF reload, got %+v", got)
	}

	svc.flushFirewallCounterDeltas()

	var persisted infoModels.FirewallRuleCounterTotal
	if err := db.Where("rule_type = ? AND rule_id = ?", "traffic", rule.ID).First(&persisted).Error; err != nil {
		t.Fatalf("failed to load persisted counter total: %v", err)
	}
	if persisted.Packets != 17 || persisted.Bytes != 170 || persisted.LastPFPackets != 2 || persisted.LastPFBytes != 20 {
		t.Fatalf("unexpected persisted counter total: %+v", persisted)
	}

	// A new Service has an empty runtime, so this verifies recovery from the telemetry DB.
	restarted := &Service{
		DB:                db,
		TelemetryDB:       db,
		firewallTelemetry: newFirewallTelemetryRuntime(),
	}
	packets, bytes = 5, 50
	restarted.sampleFirewallCounters()

	counters, err := restarted.GetFirewallTrafficRuleCounters()
	if err != nil {
		t.Fatalf("failed to get cumulative traffic counters: %v", err)
	}
	if len(counters) != 1 {
		t.Fatalf("expected one traffic counter, got %d", len(counters))
	}
	if counters[0].Packets != 20 || counters[0].Bytes != 200 {
		t.Fatalf("expected cumulative endpoint result 20 packets / 200 bytes, got %+v", counters[0])
	}
}

func TestResetFirewallCounterBaselinesPreservesTotals(t *testing.T) {
	svc, db := newNetworkServiceForTest(t)
	persisted := infoModels.FirewallRuleCounterTotal{
		RuleType:      "traffic",
		RuleID:        902,
		Packets:       100,
		Bytes:         1000,
		LastPFPackets: 10,
		LastPFBytes:   100,
	}
	if err := db.Create(&persisted).Error; err != nil {
		t.Fatalf("failed to create persisted counter total: %v", err)
	}

	svc.loadFirewallCounterState()
	svc.resetFirewallCounterBaselines()

	var updated infoModels.FirewallRuleCounterTotal
	if err := db.First(&updated, persisted.ID).Error; err != nil {
		t.Fatalf("failed to load reset counter total: %v", err)
	}
	if updated.Packets != 100 || updated.Bytes != 1000 {
		t.Fatalf("reset changed cumulative totals: %+v", updated)
	}
	if updated.LastPFPackets != 0 || updated.LastPFBytes != 0 {
		t.Fatalf("expected persisted PF baseline to reset, got %+v", updated)
	}
}

func TestEnsureFirewallCountersFreshSharesConcurrentSample(t *testing.T) {
	svc := &Service{firewallTelemetry: newFirewallTelemetryRuntime()}
	svc.SetFirewallServiceEnabledForTelemetry(true)
	previousRunCommand := firewallRunCommand
	t.Cleanup(func() { firewallRunCommand = previousRunCommand })
	var calls atomic.Int32
	firewallRunCommand = func(command string, args ...string) (string, error) {
		calls.Add(1)
		if command != "/sbin/pfctl" || len(args) != 3 || args[0] != "-a" {
			t.Errorf("unexpected counter command: %s %v", command, args)
		}
		return "", nil
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			svc.ensureFirewallCountersFresh()
		}()
	}
	close(start)
	wg.Wait()
	if got := calls.Load(); got != 2 {
		t.Fatalf("concurrent reads executed %d commands, want 2", got)
	}

	rt := svc.getFirewallTelemetryRuntime()
	rt.mu.Lock()
	rt.countersUpdatedAt = time.Now().Add(-10 * time.Second)
	rt.mu.Unlock()
	svc.ensureFirewallCountersFresh()
	if got := calls.Load(); got != 2 {
		t.Fatalf("fresh cached counters executed more commands: %d", got)
	}

	rt.mu.Lock()
	rt.countersUpdatedAt = time.Now().Add(-firewallCounterSampleInterval - time.Second)
	rt.mu.Unlock()
	svc.ensureFirewallCountersFresh()
	if got := calls.Load(); got != 4 {
		t.Fatalf("expired counters executed %d commands, want 4", got)
	}

	svc.sampleFirewallCounters()
	if got := calls.Load(); got != 6 {
		t.Fatalf("forced sample was incorrectly cached: commands=%d, want 6", got)
	}
}
