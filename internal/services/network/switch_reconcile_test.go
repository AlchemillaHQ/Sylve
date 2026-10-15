// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/bootstrap"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/network/reconcile"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
	"gorm.io/gorm"
)

func waitForSwitchReconciliation(t *testing.T, svc *Service) reconcile.Status {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := reconcile.Lock(ctx, &svc.switchReconcileMutex); err != nil {
		t.Fatalf("reconciliation did not finish: %v", err)
	}
	svc.switchReconcileMutex.Unlock()
	return svc.SwitchSyncStatus()
}

func TestStartupSwitchRetryRepairsPartialApplyInPlace(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.NetworkPort{})
	svc.switchRetryDelays = []time.Duration{0, 0, 0}
	sw := networkModels.StandardSwitch{
		Name: "retry-in-place", BridgeName: "vm-retry", MTU: 1500, DisableIPv6: true,
		NetworkManual: "192.0.2.10/24", GatewayManual: "192.0.2.1", DefaultRoute: true,
	}
	setTestStandardSwitchMACSource(&sw, createTestStandardSwitchMACSource(t, svc))
	if err := db.Create(&sw).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]networkModels.NetworkPort{{Name: "em0", SwitchID: sw.ID}, {Name: "em1", SwitchID: sw.ID}}).Error; err != nil {
		t.Fatal(err)
	}
	live := &iface.Interface{
		Name: sw.BridgeName, Description: sw.Name, MTU: 1500, Ether: testStandardSwitchMAC,
		BridgeMembers: []iface.BridgeMember{{Name: "em0"}, {Name: "tap0"}},
	}
	addressAdds, memberAdds, bridgeCreates, bridgeDeletes := 0, 0, 0, 0
	routeCalls := 0
	routes := map[string]bool{}
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name == sw.BridgeName {
				return live, nil
			}
			return &iface.Interface{Name: name, MTU: 1500}, nil
		},
		createBridge: func(networkModels.StandardSwitch) error { bridgeCreates++; return nil },
		deleteBridge: func(networkModels.StandardSwitch) error { bridgeDeletes++; return nil },
		stopDhclient: func(string) error { return nil },
		runCommand: func(command string, args ...string) (string, error) {
			if command == "/sbin/ifconfig" && len(args) >= 3 && args[0] == sw.BridgeName {
				switch args[1] {
				case "inet":
					if args[2] == sw.NetworkManual {
						addressAdds++
						live.IPv4 = append(live.IPv4, iface.IPv4{IP: net.ParseIP("192.0.2.10"), Netmask: "255.255.255.0"})
					}
				case "addm":
					memberAdds++
					live.BridgeMembers = append(live.BridgeMembers, iface.BridgeMember{Name: args[2]})
				case "deletem":
					t.Errorf("retry detached a live member: %v", args)
				}
			}
			if command == "/sbin/route" && len(args) > 0 {
				if args[0] == "add" {
					routeCalls++
					// Fail after the address and missing member have already been applied.
					if routeCalls == 1 {
						return "", errors.New("transient route failure")
					}
					key := strings.Join(args, " ")
					if routes[key] {
						return "already in table", errors.New("file exists")
					}
					routes[key] = true
				}
				if slices.Equal(args, []string{"-n", "get", "default"}) {
					return "gateway: 192.0.2.1\ninterface: vm-retry\n", nil
				}
			}
			return "", nil
		},
	})
	svc.StartSwitchNetworkReconciliation(t.Context())
	status := waitForSwitchReconciliation(t, svc)
	if status.State != "converged" || status.Attempts != 2 || status.Error != "" {
		t.Fatalf("startup did not recover: %+v", status)
	}
	if err := svc.SyncSwitchNetwork(t.Context()); err != nil {
		t.Fatalf("explicit repeat sync: %v", err)
	}
	if addressAdds != 1 || memberAdds != 1 || bridgeCreates != 0 || bridgeDeletes != 0 || len(routes) != 2 ||
		len(live.IPv4) != 1 || len(live.BridgeMembers) != 3 {
		t.Fatalf("non-idempotent retry: addresses=%d members=%d create=%d delete=%d routes=%v live=%+v",
			addressAdds, memberAdds, bridgeCreates, bridgeDeletes, routes, live)
	}
}

func TestSwitchRetryExhaustionAndManualRecovery(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.NetworkPort{})
	svc.switchRetryDelays = []time.Duration{0, 0, 0}
	sw := networkModels.StandardSwitch{Name: "broken", BridgeName: "vm-broken"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatal(err)
	}
	calls := 0
	failing := true
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(string) (*iface.Interface, error) { return nil, errors.New("interface not found") },
		createBridge: func(networkModels.StandardSwitch) error {
			calls++
			if failing {
				return errors.New("permanent bridge failure")
			}
			return nil
		},
	})
	svc.StartSwitchNetworkReconciliation(t.Context())
	status := waitForSwitchReconciliation(t, svc)
	if status.State != "degraded" || status.Attempts != 4 || calls != 4 || status.CompletedAt == nil ||
		len(status.Failures) != 1 || status.Failures[0].SwitchID != sw.ID || status.Failures[0].Name != sw.Name ||
		!strings.Contains(status.Error, "permanent bridge failure") {
		t.Fatalf("exhausted status=%+v calls=%d", status, calls)
	}
	copy := svc.SwitchSyncStatus()
	copy.Failures[0].Name = "not-the-switch"
	if svc.SwitchSyncStatus().Failures[0].Name != sw.Name {
		t.Fatal("status query leaked mutable service state")
	}
	failing = false
	if err := svc.SyncSwitchNetwork(t.Context()); err != nil {
		t.Fatalf("manual sync: %v", err)
	}
	status = svc.SwitchSyncStatus()
	if status.State != "converged" || status.Attempts != 1 || len(status.Failures) != 0 || status.Error != "" {
		t.Fatalf("manual sync did not clear failure: %+v", status)
	}
}

func TestStartupSwitchRetryDoesNotWaitForBackoffAndCancels(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.NetworkPort{})
	svc.switchRetryDelays = []time.Duration{time.Hour, time.Hour, time.Hour}
	if err := db.Create(&networkModels.StandardSwitch{Name: "missing", BridgeName: "vm-missing"}).Error; err != nil {
		t.Fatal(err)
	}
	calls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(string) (*iface.Interface, error) { return nil, errors.New("interface not found") },
		createBridge: func(networkModels.StandardSwitch) error {
			calls++
			return errors.New("not ready")
		},
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	start := time.Now()
	svc.StartSwitchNetworkReconciliation(ctx)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("startup waited for retry backoff: %s", elapsed)
	}
	if status := svc.SwitchSyncStatus(); status.State != "retrying" || status.Attempts != 1 {
		t.Fatalf("in-flight status = %+v", status)
	}
	if err := svc.SyncSwitchNetwork(t.Context()); !errors.Is(err, ErrSwitchSyncInProgress) {
		t.Fatalf("concurrent trigger error = %v", err)
	}
	// The retry loop must not hold the mutation lock while sleeping.
	if !svc.syncMutex.TryLock() {
		t.Fatal("retry backoff holds the network mutation lock")
	}
	svc.syncMutex.Unlock()
	cancel()
	status := waitForSwitchReconciliation(t, svc)
	if calls != 1 || status.State != "degraded" || !strings.Contains(status.Error, "context canceled") {
		t.Fatalf("canceled status=%+v calls=%d", status, calls)
	}
}

func TestSwitchRetryAlsoRecoversHostIPReconciliationFailure(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.NetworkPort{})
	svc.switchRetryDelays = []time.Duration{0, 0, 0}
	queries := 0
	if err := db.Callback().Query().Before("gorm:query").Register("test:host_ip_transient_failure", func(db *gorm.DB) {
		if db.Statement.Schema != nil && db.Statement.Schema.Name == "HostInterfaceL3" {
			queries++
			if queries == 1 {
				db.AddError(errors.New("Host IP store temporarily unavailable"))
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncSwitchNetwork(t.Context()); err != nil {
		t.Fatal(err)
	}
	if status := svc.SwitchSyncStatus(); status.State != "converged" || status.Attempts != 2 || queries != 2 {
		t.Fatalf("Host IP reconciliation was not retried: status=%+v queries=%d", status, queries)
	}
}

func TestSwitchRetryUsesLatestConcurrentBootstrapConfiguration(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.ManualSwitch{}, &networkModels.NetworkPort{})
	config := StandardSwitchConfig{MTU: 1500, DisableIPv6: true, MACSource: createTestStandardSwitchMACSource(t, svc)}
	sw := networkModels.StandardSwitch{Name: "bootstrap-update", BridgeName: adoptionBridgeName("bootstrap-update"), MTU: 1500, DisableIPv6: true}
	setTestStandardSwitchMACSource(&sw, config.MACSource)
	if err := db.Create(&sw).Error; err != nil {
		t.Fatal(err)
	}
	createCalls := 0
	present := false
	var reconciledMTU int
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name == sw.BridgeName && present {
				return &iface.Interface{Name: name, Ether: testStandardSwitchMAC}, nil
			}
			return nil, errors.New("interface not found")
		},
		createBridge: func(desired networkModels.StandardSwitch) error {
			createCalls++
			if createCalls == 1 {
				return errors.New("first bridge create failed")
			}
			present = true
			return nil
		},
		editBridge: func(_, desired networkModels.StandardSwitch) error { reconciledMTU = desired.MTU; return nil },
	})
	svc.StartSwitchNetworkReconciliation(t.Context())
	// Bootstrap uses this same entry point. It must be admitted during backoff,
	// and its newer desired state must not be overwritten by the startup retry.
	config.MTU = 9000
	if _, err := svc.NewStandardSwitchWithContext(t.Context(), CreateStandardSwitchRequest{Name: sw.Name, StandardSwitchConfig: config}); err != nil {
		t.Fatalf("concurrent bootstrap upsert: %v", err)
	}
	status := waitForSwitchReconciliation(t, svc)
	if status.State != "converged" || status.Attempts != 2 || createCalls != 2 || reconciledMTU != 9000 {
		t.Fatalf("retry replayed stale state: status=%+v creates=%d MTU=%d", status, createCalls, reconciledMTU)
	}
}

type bootstrapRollbackProbe struct {
	*Service
	rowsBeforeAttempt []int64
}

func TestSwitchRetryPreservesNewPendingHostIP(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.NetworkPort{})
	svc.switchRetryDelays = []time.Duration{0, 0, 0}
	if err := db.Create(&networkModels.StandardSwitch{Name: "settling", BridgeName: "vm-settling"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&networkModels.HostInterfaceL3{Interface: "em9"}).Error; err != nil {
		t.Fatal(err)
	}
	pending := networkModels.PendingApply{
		ID: "new-user-edit", Interface: "em9", Kind: networkModels.PendingApplyKindInterface,
		Phase: networkModels.PendingApplyPhaseApplied, Deadline: time.Now().Add(time.Hour),
	}
	if err := db.Create(&pending).Error; err != nil {
		t.Fatal(err)
	}
	calls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name == "em9" {
				t.Error("reconciliation touched an unconfirmed Host IP edit")
			}
			return nil, errors.New("interface not found")
		},
		createBridge: func(networkModels.StandardSwitch) error {
			calls++
			if calls == 1 {
				return errors.New("transient failure")
			}
			return nil
		},
	})
	if err := svc.SyncSwitchNetwork(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&networkModels.PendingApply{}).Where("id = ?", pending.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("retry replayed crash rollback recovery: pending=%d error=%v", count, err)
	}
}

func (p *bootstrapRollbackProbe) NewStandardSwitchWithContext(ctx context.Context, request CreateStandardSwitchRequest) (uint, error) {
	var count int64
	if err := p.DB.Model(&networkModels.StandardSwitch{}).Count(&count).Error; err != nil {
		return 0, err
	}
	p.rowsBeforeAttempt = append(p.rowsBeforeAttempt, count)
	return p.Service.NewStandardSwitchWithContext(ctx, request)
}

func TestBootstrapRetriesCreationAfterDatabaseRollback(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.ManualSwitch{}, &networkModels.NetworkPort{})
	mac := createTestStandardSwitchMACSource(t, svc)
	calls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(string) (*iface.Interface, error) { return nil, errors.New("interface not found") },
		createBridge: func(networkModels.StandardSwitch) error {
			calls++
			if calls == 1 {
				return errors.New("transient create failure")
			}
			return nil
		},
	})
	probe := &bootstrapRollbackProbe{Service: svc}
	engine := bootstrap.NewService(nil, probe)
	source := filepath.Join(t.TempDir(), "bootstrap.json")
	document := fmt.Sprintf(`{"version":1,"switches":[{"type":"standard","standard":{"name":"first-boot","mtu":1500,"bridgeMac":{"mode":"object","macObjectId":%d}}}]}`, mac.MACObjectID)
	if err := os.WriteFile(source, []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	report := engine.Apply(t.Context(), source)
	if report.Failed() || !report.Archived || calls != 2 || !slices.Equal(probe.rowsBeforeAttempt, []int64{0, 0}) {
		t.Fatalf("bootstrap did not retry rolled-back creation: report=%+v calls=%d rows=%v", report, calls, probe.rowsBeforeAttempt)
	}
	var count int64
	if err := db.Model(&networkModels.StandardSwitch{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("saved switches=%d error=%v", count, err)
	}
}
