// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
	"testing"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func hasService(services []models.AvailableService, wanted models.AvailableService) bool {
	for _, service := range services {
		if service == wanted {
			return true
		}
	}

	return false
}

func TestEnsureMdnsEnabledIsIdempotent(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	if err := db.Create(&models.BasicSettings{
		Services: []models.AvailableService{models.SambaServer},
	}).Error; err != nil {
		t.Fatalf("failed to create basic settings: %v", err)
	}

	service := &Service{DB: db}
	if err := service.WithServiceSettingsLock(func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			if err := service.EnsureMdnsEnabled(tx); err != nil {
				return err
			}
			return service.EnsureMdnsEnabled(tx)
		})
	}); err != nil {
		t.Fatalf("failed to ensure mDNS is enabled: %v", err)
	}

	var current models.BasicSettings
	if err := db.First(&current).Error; err != nil {
		t.Fatalf("failed to load basic settings: %v", err)
	}

	mdnsCount := 0
	for _, enabledService := range current.Services {
		if enabledService == models.Mdns {
			mdnsCount++
		}
	}
	if mdnsCount != 1 {
		t.Fatalf("expected one mDNS service entry, got %d", mdnsCount)
	}
}

func TestSetServiceEnabledMdnsRebuildsAfterPersistingState(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	settings := models.BasicSettings{Services: []models.AvailableService{models.SambaServer}}
	if err := db.Create(&settings).Error; err != nil {
		t.Fatalf("failed to create basic settings: %v", err)
	}

	var rebuildStates []bool
	service := &Service{
		DB: db,
		MdnsRebuild: func() error {
			var current models.BasicSettings
			if err := db.First(&current).Error; err != nil {
				return err
			}
			rebuildStates = append(rebuildStates, hasService(current.Services, models.Mdns))
			return nil
		},
	}

	if changed, err := service.SetServiceEnabled(t.Context(), models.Mdns, true, nil); err != nil || !changed {
		t.Fatalf("enabling mdns failed: %v", err)
	}
	if changed, err := service.SetServiceEnabled(t.Context(), models.Mdns, false, nil); err != nil || !changed {
		t.Fatalf("disabling mdns failed: %v", err)
	}

	if len(rebuildStates) != 2 || !rebuildStates[0] || rebuildStates[1] {
		t.Fatalf("expected rebuilds after enable and disable persistence, got %v", rebuildStates)
	}
}

func TestSetServiceEnabledMdnsRestoresStateAfterRebuildFailure(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	settings := models.BasicSettings{Services: []models.AvailableService{models.SambaServer}}
	if err := db.Create(&settings).Error; err != nil {
		t.Fatalf("failed to create basic settings: %v", err)
	}

	calls := 0
	var rebuildStates []bool
	service := &Service{
		DB: db,
		MdnsRebuild: func() error {
			var current models.BasicSettings
			if err := db.First(&current).Error; err != nil {
				return err
			}
			rebuildStates = append(rebuildStates, hasService(current.Services, models.Mdns))
			calls++
			if calls == 1 {
				return errors.New("responder unavailable")
			}
			return nil
		},
	}

	if _, err := service.SetServiceEnabled(t.Context(), models.Mdns, true, nil); err == nil {
		t.Fatal("expected mdns rebuild failure")
	}

	var current models.BasicSettings
	if err := db.First(&current).Error; err != nil {
		t.Fatalf("failed to load basic settings: %v", err)
	}
	if hasService(current.Services, models.Mdns) {
		t.Fatalf("mdns remained enabled after failed rebuild: %v", current.Services)
	}
	if len(rebuildStates) != 2 || !rebuildStates[0] || rebuildStates[1] {
		t.Fatalf("expected failed enable then restored disabled state, got %v", rebuildStates)
	}
}

func TestSetServiceEnabledNoOpDoesNotApplyRuntimeState(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	settings := models.BasicSettings{Services: []models.AvailableService{models.DHCPServer}}
	if err := db.Create(&settings).Error; err != nil {
		t.Fatalf("failed to create basic settings: %v", err)
	}

	runtimeCalls := 0
	service := &Service{
		DB: db,
		dhcpServiceStateApply: func(bool) error {
			runtimeCalls++
			return nil
		},
	}

	changed, err := service.SetServiceEnabled(t.Context(), models.DHCPServer, true, nil)
	if err != nil {
		t.Fatalf("setting current state failed: %v", err)
	}
	if changed || runtimeCalls != 0 {
		t.Fatalf("changed=%v runtime calls=%d; want unchanged with no runtime call", changed, runtimeCalls)
	}
}

func TestSetServiceEnabledPreservesOrderAndAppliesDesiredState(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	settings := models.BasicSettings{Services: []models.AvailableService{models.Jails, models.SambaServer}}
	if err := db.Create(&settings).Error; err != nil {
		t.Fatalf("failed to create basic settings: %v", err)
	}

	var runtimeStates []bool
	service := &Service{DB: db}
	apply := func(_ context.Context, gotService models.AvailableService, enabled bool) error {
		if gotService != models.Firewall {
			t.Fatalf("runtime service = %q; want firewall", gotService)
		}
		runtimeStates = append(runtimeStates, enabled)
		return nil
	}

	if changed, err := service.SetServiceEnabled(t.Context(), models.Firewall, true, apply); err != nil || !changed {
		t.Fatalf("enabling firewall changed=%v err=%v", changed, err)
	}
	if changed, err := service.SetServiceEnabled(t.Context(), models.Firewall, false, apply); err != nil || !changed {
		t.Fatalf("disabling firewall changed=%v err=%v", changed, err)
	}

	var current models.BasicSettings
	if err := db.First(&current).Error; err != nil {
		t.Fatalf("failed to load basic settings: %v", err)
	}
	if len(current.Services) != 2 || current.Services[0] != models.Jails || current.Services[1] != models.SambaServer {
		t.Fatalf("service ordering changed: %v", current.Services)
	}
	if len(runtimeStates) != 2 || !runtimeStates[0] || runtimeStates[1] {
		t.Fatalf("runtime states = %v; want [true false]", runtimeStates)
	}
}

func TestSetServiceEnabledRestoresExternalRuntimeStateAfterFailure(t *testing.T) {
	for _, targetService := range []models.AvailableService{models.Firewall, models.WireGuard} {
		t.Run(string(targetService), func(t *testing.T) {
			db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
			settings := models.BasicSettings{Services: []models.AvailableService{targetService}}
			if err := db.Create(&settings).Error; err != nil {
				t.Fatalf("failed to create basic settings: %v", err)
			}

			var runtimeStates []bool
			apply := func(_ context.Context, service models.AvailableService, enabled bool) error {
				if service != targetService {
					t.Fatalf("runtime service = %q; want %q", service, targetService)
				}
				runtimeStates = append(runtimeStates, enabled)
				if !enabled {
					return errors.New("runtime disable failed")
				}
				return nil
			}

			service := &Service{DB: db}
			_, stateErr := service.SetServiceEnabled(t.Context(), targetService, false, apply)
			if stateErr == nil {
				t.Fatal("expected runtime failure")
			}

			var current models.BasicSettings
			if err := db.First(&current).Error; err != nil {
				t.Fatalf("failed to load basic settings: %v", err)
			}
			if !hasService(current.Services, targetService) {
				t.Fatalf("%s was not restored after runtime failure: %v", targetService, current.Services)
			}
			if len(runtimeStates) != 2 || runtimeStates[0] || !runtimeStates[1] {
				t.Fatalf("runtime states = %v; want failed false then restored true", runtimeStates)
			}
			if code := SettingsErrorCode(stateErr); code != "service_runtime_update_failed" {
				t.Fatalf("settings error code = %q", code)
			}
		})
	}
}

func TestSetServiceEnabledRestoresDHCPRuntimeStateAfterFailure(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	if err := db.Create(&models.BasicSettings{
		Services: []models.AvailableService{models.DHCPServer},
	}).Error; err != nil {
		t.Fatalf("failed to create basic settings: %v", err)
	}

	var runtimeStates []bool
	service := &Service{
		DB: db,
		dhcpServiceStateApply: func(enabled bool) error {
			runtimeStates = append(runtimeStates, enabled)
			if !enabled {
				return errors.New("dnsmasq stop failed")
			}
			return nil
		},
	}
	if _, err := service.SetServiceEnabled(t.Context(), models.DHCPServer, false, nil); err == nil {
		t.Fatal("expected DHCP runtime failure")
	}

	var current models.BasicSettings
	if err := db.First(&current).Error; err != nil {
		t.Fatalf("failed to load basic settings: %v", err)
	}
	if !hasService(current.Services, models.DHCPServer) {
		t.Fatalf("DHCP was not restored after runtime failure: %v", current.Services)
	}
	if len(runtimeStates) != 2 || runtimeStates[0] || !runtimeStates[1] {
		t.Fatalf("runtime states = %v; want failed false then restored true", runtimeStates)
	}
}

func TestSetServiceEnabledSerializesConcurrentDesiredStateRequests(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	if err := db.Create(&models.BasicSettings{}).Error; err != nil {
		t.Fatalf("failed to create basic settings: %v", err)
	}

	service := &Service{DB: db}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.SetServiceEnabled(context.Background(), models.Jails, true, nil)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent state request failed: %v", err)
		}
	}

	var current models.BasicSettings
	if err := db.First(&current).Error; err != nil {
		t.Fatalf("failed to load basic settings: %v", err)
	}
	if !hasService(current.Services, models.Jails) {
		t.Fatalf("jails was not enabled: %v", current.Services)
	}
}

func TestNormalizeUsablePoolsTrimsAndDeduplicates(t *testing.T) {
	got := normalizeUsablePools([]string{" tank ", "", "zroot", "tank", " zroot "})
	if len(got) != 2 || got[0] != "tank" || got[1] != "zroot" {
		t.Fatalf("normalized pools = %v; want [tank zroot]", got)
	}
}

type settingsPoolRunner struct {
	created   map[string]bool
	destroyed []string
}

func (r *settingsPoolRunner) writeJSON(stdout io.Writer, value any) error {
	if stdout == nil {
		return nil
	}
	return json.NewEncoder(stdout).Encode(value)
}

func (r *settingsPoolRunner) Run(
	_ context.Context,
	_ io.Reader,
	stdout io.Writer,
	_ io.Writer,
	name string,
	args ...string,
) error {
	if name == "zpool" && len(args) > 0 && args[0] == "list" {
		return r.writeJSON(stdout, map[string]any{
			"output_version": map[string]any{"command": "zpool list"},
			"pools":          map[string]any{"tank": map[string]any{"name": "tank"}},
		})
	}
	if name != "zfs" || len(args) == 0 {
		return fmt.Errorf("unexpected command: %s %v", name, args)
	}

	switch args[0] {
	case "list":
		// gzfs appends -j after the dataset name for JSON output.
		datasetName := args[len(args)-2]
		datasets := map[string]any{}
		if r.created[datasetName] {
			datasets[datasetName] = map[string]any{
				"name": datasetName,
				"type": string(gzfs.DatasetTypeFilesystem),
				"pool": "tank",
				"properties": map[string]any{
					"guid":       map[string]any{"value": datasetName + "-guid"},
					"mountpoint": map[string]any{"value": "/" + datasetName},
				},
			}
		}
		return r.writeJSON(stdout, map[string]any{
			"output_version": map[string]any{"command": "zfs list"},
			"datasets":       datasets,
		})
	case "create":
		datasetName := args[len(args)-1]
		r.created[datasetName] = true
		return nil
	case "destroy":
		datasetName := args[len(args)-1]
		r.destroyed = append(r.destroyed, datasetName)
		delete(r.created, datasetName)
		return nil
	default:
		return fmt.Errorf("unexpected zfs command: %v", args)
	}
}

func TestAddUsablePoolsCleansCreatedDatasetsWhenPersistenceFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	if err := db.Create(&models.BasicSettings{}).Error; err != nil {
		t.Fatalf("failed to seed basic settings: %v", err)
	}
	if err := db.Callback().Update().Before("gorm:update").Register(
		"force_basic_settings_update_failure",
		func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "basic_settings" {
				tx.AddError(errors.New("forced settings persistence failure"))
			}
		},
	); err != nil {
		t.Fatalf("failed to install update callback: %v", err)
	}

	runner := &settingsPoolRunner{created: map[string]bool{}}
	service := &Service{
		DB: db,
		GZFS: gzfs.NewClient(gzfs.Options{
			Runner: runner,
		}),
	}

	err := service.AddUsablePools(t.Context(), []string{"tank"})
	if err == nil {
		t.Fatal("expected settings persistence failure")
	}
	if code := SettingsErrorCode(err); code != "usable_pools_update_failed" {
		t.Fatalf("error code = %q; want usable_pools_update_failed", code)
	}

	wantDestroyed := []string{
		"tank/sylve/bootstraps",
		"tank/sylve/jails",
		"tank/sylve/virtual-machines",
		"tank/sylve",
	}
	if !reflect.DeepEqual(runner.destroyed, wantDestroyed) {
		t.Fatalf("destroyed datasets = %v; want %v", runner.destroyed, wantDestroyed)
	}

	var current models.BasicSettings
	if err := db.First(&current).Error; err != nil {
		t.Fatalf("failed to reload basic settings: %v", err)
	}
	if len(current.Pools) != 0 {
		t.Fatalf("persisted pools = %v; want none", current.Pools)
	}
}
