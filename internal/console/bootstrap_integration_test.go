// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/bootstrap"
	"github.com/alchemillahq/sylve/internal/db/models"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	networkService "github.com/alchemillahq/sylve/internal/services/network"
	systemService "github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

type bootstrapIntegrationFixture struct {
	db      *gorm.DB
	system  *systemService.Service
	network *networkService.Service
	engine  *bootstrap.Service
	pool    string
	path    string
	name    string
	port    string
	bridge  string
}

type bootstrapIntegrationVM struct {
	ID  uint `gorm:"primaryKey"`
	RID uint
}

func (bootstrapIntegrationVM) TableName() string { return "vms" }

type bootstrapIntegrationJail struct {
	ID   uint `gorm:"primaryKey"`
	CTID uint
}

func (bootstrapIntegrationJail) TableName() string { return "jails" }

func newBootstrapIntegrationFixture(t *testing.T, withSwitch bool) *bootstrapIntegrationFixture {
	t.Helper()
	if testing.Short() {
		t.Skip("requires an isolated real ZFS pool and FreeBSD interfaces")
	}
	pool, client := zfstest.DedicatedPool(t)
	db := testutil.NewSQLiteTestDB(t,
		&models.BasicSettings{}, &models.ZFSCacheInvalidation{},
		&utilitiesModels.Downloads{}, &utilitiesModels.Upload{},
		&networkModels.StandardSwitch{}, &networkModels.NetworkPort{},
		&networkModels.ManualSwitch{}, &networkModels.Object{},
		&networkModels.ObjectEntry{}, &networkModels.HostInterfaceL3{},
		&networkModels.HostInterfaceL3Address{}, &networkModels.PendingApply{},
		&networkModels.DHCPConfig{}, &networkModels.DHCPRange{},
		&bootstrapIntegrationVM{}, &vmModels.Network{},
		&bootstrapIntegrationJail{}, &jailModels.Network{},
	)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	system := systemService.NewSystemService(db, client).(*systemService.Service)
	network := &networkService.Service{DB: db, TelemetryDB: db}
	f := &bootstrapIntegrationFixture{
		db: db, system: system, network: network,
		engine: bootstrap.NewService(system, network),
		pool:   pool, path: filepath.Join(t.TempDir(), "bootstrap.json"),
		name: "bootstrap-" + strings.TrimPrefix(pool, "sylve-test-"),
	}
	if !withSwitch {
		return f
	}
	output, err := exec.Command("/sbin/ifconfig", "epair", "create").CombinedOutput()
	if err != nil {
		t.Fatalf("create isolated epair: %v: %s", err, output)
	}
	f.port = strings.TrimSpace(string(output))
	if f.port == "" {
		t.Fatal("epair creation returned an empty interface name")
	}
	f.bridge = utils.ShortHash("vm-" + f.name)
	if output, err := exec.Command("/sbin/ifconfig", f.bridge).CombinedOutput(); err == nil {
		_, _ = exec.Command("/sbin/ifconfig", f.port, "destroy").CombinedOutput()
		t.Fatalf("refusing to use an existing generated bridge %s: %s", f.bridge, output)
	}
	t.Cleanup(func() {
		_, _ = exec.Command("/sbin/ifconfig", f.bridge, "destroy").CombinedOutput()
		if output, err := exec.Command("/sbin/ifconfig", f.port, "destroy").CombinedOutput(); err != nil {
			t.Errorf("destroy owned epair %s: %v: %s", f.port, err, output)
		}
	})
	return f
}

func (f *bootstrapIntegrationFixture) document(initialized bool, badSwitch bool) map[string]any {
	doc := map[string]any{"version": 1, "pools": []string{f.pool}}
	if initialized {
		doc["initialized"] = true
	}
	if f.port != "" {
		switches := []any{map[string]any{
			"type": "standard",
			"standard": map[string]any{
				"name": f.name, "ports": []string{f.port}, "disableIPv6": true,
				"bridgeMac": map[string]any{"mode": "port", "port": f.port},
			},
		}}
		if badSwitch {
			switches = append(switches, map[string]any{
				"type": "not-a-switch-type", "manual": map[string]any{"name": "bad", "bridge": "missing"},
			})
		}
		doc["switches"] = switches
	}
	return doc
}

func (f *bootstrapIntegrationFixture) write(t *testing.T, doc map[string]any) {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func (f *bootstrapIntegrationFixture) switchRows(t *testing.T) (networkModels.StandardSwitch, networkModels.NetworkPort) {
	t.Helper()
	var sw networkModels.StandardSwitch
	var port networkModels.NetworkPort
	if err := f.db.Where("name = ?", f.name).First(&sw).Error; err != nil {
		t.Fatalf("load applied switch: %v", err)
	}
	if err := f.db.Where("switch_id = ?", sw.ID).First(&port).Error; err != nil {
		t.Fatalf("load applied port: %v", err)
	}
	return sw, port
}

func TestIntegrationBootstrapRealDatasetsAndSwitchLifecycle(t *testing.T) {
	f := newBootstrapIntegrationFixture(t, true)
	f.write(t, f.document(true, false))
	report := f.engine.Apply(t.Context(), f.path)
	if report.Failed() || !report.Archived || !report.RestartRequired {
		t.Fatalf("first apply = %+v", report)
	}
	var settings models.BasicSettings
	if err := f.db.First(&settings).Error; err != nil || !settings.Initialized || len(settings.Pools) != 1 || settings.Pools[0] != f.pool {
		t.Fatalf("settings = %+v, error = %v", settings, err)
	}
	for _, suffix := range []string{"sylve", "sylve/virtual-machines", "sylve/jails", "sylve/bootstraps"} {
		if output, err := exec.Command("/sbin/zfs", "list", "-H", "-o", "name", f.pool+"/"+suffix).CombinedOutput(); err != nil {
			t.Fatalf("required dataset %s/%s is missing: %v: %s", f.pool, suffix, err, output)
		}
	}
	sw, port := f.switchRows(t)
	if output, err := exec.Command("/sbin/ifconfig", f.bridge).CombinedOutput(); err != nil || !strings.Contains(string(output), "member: "+f.port) {
		t.Fatalf("applied runtime is missing its member: %v: %s", err, output)
	}
	f.write(t, f.document(true, false))
	report = f.engine.Apply(t.Context(), f.path)
	if report.Failed() || !report.Archived || report.RestartRequired {
		t.Fatalf("idempotent apply = %+v", report)
	}
	after, afterPort := f.switchRows(t)
	if after.ID != sw.ID || afterPort.ID != port.ID || !after.UpdatedAt.Equal(sw.UpdatedAt) {
		t.Fatalf("no-op changed switch/port identity or timestamp: before=%+v/%+v after=%+v/%+v", sw, port, after, afterPort)
	}
	if err := f.network.DeleteStandardSwitch(sw.ID); err != nil {
		t.Fatalf("delete normally applied switch: %v", err)
	}
	newProcess := bootstrap.NewService(f.system, f.network)
	if missing := newProcess.Apply(t.Context(), f.path); !missing.Failed() {
		t.Fatalf("missing explicit source unexpectedly succeeded: %+v", missing)
	}
	var count int64
	if err := f.db.Model(&networkModels.StandardSwitch{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("deleted switch resurrected: count=%d error=%v", count, err)
	}
	if _, err := os.Stat(f.path + ".applied"); err != nil {
		t.Fatalf("archive is missing: %v", err)
	}
}

func TestIntegrationBootstrapPartialRetryUsesRealRows(t *testing.T) {
	f := newBootstrapIntegrationFixture(t, true)
	f.write(t, f.document(true, true))
	first := f.engine.Apply(t.Context(), f.path)
	if !first.Failed() || first.Archived || !first.RestartRequired {
		t.Fatalf("partial apply = %+v", first)
	}
	sw, port := f.switchRows(t)
	second := bootstrap.NewService(f.system, f.network).Apply(t.Context(), f.path)
	if !second.Failed() || second.Archived || second.RestartRequired {
		t.Fatalf("unchanged partial retry = %+v", second)
	}
	after, afterPort := f.switchRows(t)
	if after.ID != sw.ID || afterPort.ID != port.ID {
		t.Fatal("successful switch/port rows were recreated by a partial retry")
	}
	f.write(t, f.document(true, false))
	final := f.engine.Apply(t.Context(), f.path)
	if final.Failed() || !final.Archived || final.RestartRequired {
		t.Fatalf("corrected document = %+v", final)
	}
}

func TestIntegrationBootstrapPoolOnlyKeepsUIWizardUsable(t *testing.T) {
	f := newBootstrapIntegrationFixture(t, false)
	f.write(t, f.document(false, false))
	report := f.engine.Apply(t.Context(), f.path)
	if report.Failed() || !report.Archived || report.RestartRequired {
		t.Fatalf("pool-only apply = %+v", report)
	}
	var before models.BasicSettings
	if err := f.db.First(&before).Error; err != nil || before.Initialized {
		t.Fatalf("uninitialized settings = %+v, error=%v", before, err)
	}
	errs := f.system.Initialize(t.Context(), systemServiceInterfaces.InitializeRequest{Pools: []string{f.pool}})
	if len(errs) != 0 {
		t.Fatalf("UI initialization after bootstrap failed: %v", errs)
	}
	var after models.BasicSettings
	if err := f.db.First(&after).Error; err != nil || !after.Initialized || after.ID != before.ID {
		t.Fatalf("UI initialization did not update the existing row: %s", fmt.Sprint(after, err))
	}
}

func TestIntegrationBootstrapPoolFailurePreservesSuccessfulPool(t *testing.T) {
	f := newBootstrapIntegrationFixture(t, false)
	doc := f.document(false, false)
	doc["pools"] = []string{f.pool + "/not-a-pool", f.pool}
	f.write(t, doc)
	report := f.engine.Apply(t.Context(), f.path)
	if !report.Failed() || report.Archived || report.RestartRequired {
		t.Fatalf("partial pool apply = %+v", report)
	}
	if len(report.Items) != 2 || report.Items[0].Index != 0 || report.Items[0].Status != systemServiceInterfaces.BootstrapFailed ||
		report.Items[1].Index != 1 || report.Items[1].Status != systemServiceInterfaces.BootstrapApplied {
		t.Fatalf("pool outcomes = %+v", report.Items)
	}
	var settings models.BasicSettings
	if err := f.db.First(&settings).Error; err != nil || settings.Initialized || len(settings.Pools) != 1 || settings.Pools[0] != f.pool {
		t.Fatalf("successful pool was not retained: settings=%+v error=%v", settings, err)
	}
	if _, err := os.Stat(f.path); err != nil {
		t.Fatalf("partial source was not retained: %v", err)
	}
	f.write(t, f.document(false, false))
	retry := f.engine.Apply(t.Context(), f.path)
	if retry.Failed() || !retry.Archived || retry.RestartRequired {
		t.Fatalf("corrected pool retry = %+v", retry)
	}
}

func TestIntegrationBootstrapManualSwitchPreservesExternalBridges(t *testing.T) {
	f := newBootstrapIntegrationFixture(t, false)
	createBridge := func() string {
		t.Helper()
		output, err := exec.Command("/sbin/ifconfig", "bridge", "create").CombinedOutput()
		if err != nil {
			t.Fatalf("create owned external bridge: %v: %s", err, output)
		}
		bridge := strings.TrimSpace(string(output))
		if bridge == "" {
			t.Fatal("owned bridge creation returned no interface name")
		}
		t.Cleanup(func() {
			if _, err := exec.Command("/sbin/ifconfig", bridge).CombinedOutput(); err == nil {
				if output, err := exec.Command("/sbin/ifconfig", bridge, "destroy").CombinedOutput(); err != nil {
					t.Errorf("destroy owned bridge %s: %v: %s", bridge, err, output)
				}
			}
		})
		return bridge
	}
	firstBridge, secondBridge := createBridge(), createBridge()
	writeManual := func(bridge string) {
		t.Helper()
		doc := f.document(false, false)
		doc["switches"] = []any{map[string]any{
			"type": "manual", "manual": map[string]any{"name": f.name, "bridge": bridge},
		}}
		f.write(t, doc)
	}
	loadManual := func() networkModels.ManualSwitch {
		t.Helper()
		var sw networkModels.ManualSwitch
		if err := f.db.Where("name = ?", f.name).First(&sw).Error; err != nil {
			t.Fatalf("load manual switch: %v", err)
		}
		return sw
	}
	writeManual(firstBridge)
	if report := f.engine.Apply(t.Context(), f.path); report.Failed() || !report.Archived || report.RestartRequired {
		t.Fatalf("manual create = %+v", report)
	}
	before := loadManual()
	writeManual(firstBridge)
	if report := f.engine.Apply(t.Context(), f.path); report.Failed() || !report.Archived {
		t.Fatalf("manual no-op = %+v", report)
	}
	if after := loadManual(); after.ID != before.ID || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("manual no-op changed identity/timestamp: before=%+v after=%+v", before, after)
	}
	writeManual(secondBridge)
	if report := f.engine.Apply(t.Context(), f.path); report.Failed() || !report.Archived || report.RestartRequired {
		t.Fatalf("manual update = %+v", report)
	}
	if after := loadManual(); after.ID != before.ID || after.Bridge != secondBridge {
		t.Fatalf("manual update recreated row or retained old bridge: %+v", after)
	}
	for _, bridge := range []string{firstBridge, secondBridge} {
		if output, err := exec.Command("/sbin/ifconfig", bridge).CombinedOutput(); err != nil {
			t.Fatalf("manual update destroyed external bridge %s: %v: %s", bridge, err, output)
		}
	}
	if output, err := exec.Command("/sbin/ifconfig", secondBridge, "destroy").CombinedOutput(); err != nil {
		t.Fatalf("remove owned bridge to test disappearance: %v: %s", err, output)
	}
	writeManual(secondBridge)
	if report := f.engine.Apply(t.Context(), f.path); !report.Failed() || report.Archived {
		t.Fatalf("manual no-op ignored disappeared bridge: %+v", report)
	}
	if err := f.network.DeleteManualSwitch(before.ID); err != nil {
		t.Fatalf("delete manual registration: %v", err)
	}
	if output, err := exec.Command("/sbin/ifconfig", firstBridge).CombinedOutput(); err != nil {
		t.Fatalf("manual deletion touched external bridge: %v: %s", err, output)
	}
}
