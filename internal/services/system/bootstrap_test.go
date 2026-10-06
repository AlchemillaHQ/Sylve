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
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	dbpkg "github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func newBootstrapTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testutil.NewSQLiteTestDB(t, &models.BasicSettings{}, &models.ZFSCacheInvalidation{})
}

func loadBootstrapSettings(t *testing.T, db *gorm.DB) models.BasicSettings {
	t.Helper()
	var settings models.BasicSettings
	if err := db.First(&settings).Error; err != nil {
		t.Fatalf("failed to load basic settings: %v", err)
	}
	return settings
}

func zfsCacheGeneration(t *testing.T, db *gorm.DB, kind string) uint64 {
	t.Helper()
	var row models.ZFSCacheInvalidation
	err := db.Where("kind = ?", kind).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0
	}
	if err != nil {
		t.Fatalf("failed to load zfs cache invalidation: %v", err)
	}
	return row.Generation
}

func itemAt(
	t *testing.T,
	result systemServiceInterfaces.BootstrapSettingsResult,
	index int,
) systemServiceInterfaces.BootstrapItemResult {
	t.Helper()
	if index < 0 || index >= len(result.Items) {
		t.Fatalf("result has %d items, want index %d: %+v", len(result.Items), index, result.Items)
	}
	return result.Items[index]
}

func TestApplyBootstrapSettingsAddsPoolsServicesAndInitialization(t *testing.T) {
	db := newBootstrapTestDB(t)
	hookCalls := 0
	service := &Service{
		DB: db,
		ensureBootstrapPoolFn: func(context.Context, string) (bool, error) {
			return false, nil
		},
		bootstrapServicePrecheckFn: func(models.AvailableService) error { return nil },
	}
	service.OnUsablePoolsChanged = func(context.Context) error {
		hookCalls++
		return nil
	}

	result, err := service.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
		Initialized: true,
		Pools:       []string{"tank"},
		Services:    []models.AvailableService{models.Jails},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.RestartRequired {
		t.Fatalf("expected restart required after initialization")
	}
	if len(result.Items) != 3 {
		t.Fatalf("items = %+v; want 3", result.Items)
	}

	poolItem := itemAt(t, result, 0)
	if poolItem.Kind != "pool" || poolItem.Index != 0 || poolItem.Name != "tank" ||
		poolItem.Status != systemServiceInterfaces.BootstrapApplied {
		t.Fatalf("pool item = %+v", poolItem)
	}
	serviceItem := itemAt(t, result, 1)
	if serviceItem.Kind != "service" || serviceItem.Index != 0 ||
		serviceItem.Status != systemServiceInterfaces.BootstrapApplied {
		t.Fatalf("service item = %+v", serviceItem)
	}
	initItem := itemAt(t, result, 2)
	if initItem.Kind != "initialized" || initItem.Status != systemServiceInterfaces.BootstrapApplied {
		t.Fatalf("initialized item = %+v", initItem)
	}

	settings := loadBootstrapSettings(t, db)
	if !settings.Initialized {
		t.Fatalf("settings not initialized: %+v", settings)
	}
	if !reflect.DeepEqual(settings.Pools, []string{"tank"}) {
		t.Fatalf("pools = %v; want [tank]", settings.Pools)
	}
	if !hasService(settings.Services, models.Jails) {
		t.Fatalf("jails not enabled: %v", settings.Services)
	}
	if settings.Restarted {
		t.Fatalf("restarted flag should be preserved as false")
	}
	if hookCalls != 1 {
		t.Fatalf("telemetry hook calls = %d; want 1", hookCalls)
	}
}

func TestApplyBootstrapSettingsPoolFailureContinues(t *testing.T) {
	db := newBootstrapTestDB(t)
	service := &Service{
		DB: db,
		ensureBootstrapPoolFn: func(_ context.Context, name string) (bool, error) {
			if name == "bad" {
				return false, errors.New("pool_not_found_bad")
			}
			return false, nil
		},
	}

	result, err := service.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
		Pools: []string{"bad", "good"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RestartRequired {
		t.Fatalf("pools alone must not require a restart")
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %+v; want 2", result.Items)
	}
	if got := itemAt(t, result, 0); got.Status != systemServiceInterfaces.BootstrapFailed || got.Name != "bad" {
		t.Fatalf("first pool item = %+v; want failed bad", got)
	}
	if got := itemAt(t, result, 1); got.Status != systemServiceInterfaces.BootstrapApplied || got.Name != "good" {
		t.Fatalf("second pool item = %+v; want applied good", got)
	}

	settings := loadBootstrapSettings(t, db)
	if !reflect.DeepEqual(settings.Pools, []string{"good"}) {
		t.Fatalf("persisted pools = %v; want [good]", settings.Pools)
	}
}

func TestApplyBootstrapSettingsUnknownServiceFails(t *testing.T) {
	db := newBootstrapTestDB(t)
	service := &Service{DB: db}

	result, err := service.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
		Services: []models.AvailableService{"not-a-service"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	item := itemAt(t, result, 0)
	if item.Status != systemServiceInterfaces.BootstrapFailed {
		t.Fatalf("item = %+v; want failed", item)
	}
	if item.Message != "unsupported_service_not-a-service" {
		t.Fatalf("message = %q", item.Message)
	}

	var count int64
	if err := db.Model(&models.BasicSettings{}).Count(&count).Error; err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("invalid request created %d settings row(s)", count)
	}
}

func TestApplyBootstrapSettingsPrecheckWarningDropsNewEnable(t *testing.T) {
	db := newBootstrapTestDB(t)
	service := &Service{
		DB: db,
		bootstrapServicePrecheckFn: func(models.AvailableService) error {
			return errors.New("virt_required_package_libvirt_not_installed")
		},
	}

	result, err := service.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
		Services: []models.AvailableService{models.Virtualization},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RestartRequired {
		t.Fatalf("dropped new enable must not require a restart")
	}
	item := itemAt(t, result, 0)
	if item.Status != systemServiceInterfaces.BootstrapWarning {
		t.Fatalf("item = %+v; want warning", item)
	}

	var count int64
	if err := db.Model(&models.BasicSettings{}).Count(&count).Error; err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("warning-only request created %d settings row(s)", count)
	}
}

func TestApplyBootstrapSettingsPreservesExistingEnabledServiceOnPrecheckFailure(t *testing.T) {
	db := newBootstrapTestDB(t)
	if err := db.Create(&models.BasicSettings{
		ID:          1,
		Initialized: true,
		Restarted:   true,
		Services:    []models.AvailableService{models.Jails},
	}).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}

	service := &Service{
		DB: db,
		bootstrapServicePrecheckFn: func(models.AvailableService) error {
			return errors.New("jails_racct_not_enabled")
		},
	}

	result, err := service.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
		Services: []models.AvailableService{models.Jails},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RestartRequired {
		t.Fatalf("preserving an existing enable must not require a restart")
	}
	item := itemAt(t, result, 0)
	if item.Status != systemServiceInterfaces.BootstrapWarning {
		t.Fatalf("item = %+v; want warning", item)
	}

	settings := loadBootstrapSettings(t, db)
	if !hasService(settings.Services, models.Jails) {
		t.Fatalf("existing enabled service was removed: %v", settings.Services)
	}
	if !settings.Initialized || !settings.Restarted {
		t.Fatalf("existing flags not preserved: %+v", settings)
	}
}

func TestApplyBootstrapSettingsDeduplicatesAndKeepsOrderStable(t *testing.T) {
	db := newBootstrapTestDB(t)
	if err := db.Create(&models.BasicSettings{
		ID:          1,
		Initialized: true,
		Services:    []models.AvailableService{models.Jails},
	}).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}

	precheckCalls := 0
	service := &Service{
		DB: db,
		ensureBootstrapPoolFn: func(context.Context, string) (bool, error) {
			return false, nil
		},
		bootstrapServicePrecheckFn: func(service models.AvailableService) error {
			if service != models.Jails {
				t.Fatalf("unexpected service precheck: %s", service)
			}
			precheckCalls++
			return nil
		},
	}

	result, err := service.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
		Pools:    []string{"tank", "tank"},
		Services: []models.AvailableService{models.Jails, models.Jails},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RestartRequired {
		t.Fatalf("duplicate-only request must not require a restart")
	}
	if len(result.Items) != 4 {
		t.Fatalf("items = %+v; want 4", result.Items)
	}
	if precheckCalls != 1 {
		t.Fatalf("precheck calls = %d; want 1 for duplicate enabled services", precheckCalls)
	}
	if got := itemAt(t, result, 1); got.Status != systemServiceInterfaces.BootstrapApplied || got.Kind != "pool" {
		t.Fatalf("duplicate pool item = %+v", got)
	}
	if got := itemAt(t, result, 3); got.Status != systemServiceInterfaces.BootstrapApplied || got.Kind != "service" {
		t.Fatalf("duplicate service item = %+v", got)
	}

	settings := loadBootstrapSettings(t, db)
	if !reflect.DeepEqual(settings.Pools, []string{"tank"}) {
		t.Fatalf("pools = %v; want [tank]", settings.Pools)
	}
	if !reflect.DeepEqual(settings.Services, []models.AvailableService{models.Jails}) {
		t.Fatalf("services = %v; want [jails]", settings.Services)
	}
}

func TestApplyBootstrapSettingsPreservesRestartedAndMonotonicInitialized(t *testing.T) {
	db := newBootstrapTestDB(t)
	if err := db.Create(&models.BasicSettings{
		ID:          1,
		Pools:       []string{"tank"},
		Initialized: true,
		Restarted:   true,
	}).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}

	ensureCalls := 0
	service := &Service{
		DB: db,
		ensureBootstrapPoolFn: func(context.Context, string) (bool, error) {
			ensureCalls++
			return false, nil
		},
	}

	result, err := service.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
		Initialized: false,
		Pools:       []string{"tank"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RestartRequired {
		t.Fatalf("absent initialized must not require a restart")
	}
	if ensureCalls != 1 {
		t.Fatalf("ensure calls = %d; want 1 even on initialized systems", ensureCalls)
	}
	if len(result.Items) != 1 || result.Items[0].Status != systemServiceInterfaces.BootstrapApplied {
		t.Fatalf("items = %+v; want a single applied pool", result.Items)
	}

	settings := loadBootstrapSettings(t, db)
	if !settings.Initialized || !settings.Restarted {
		t.Fatalf("flags not preserved: %+v", settings)
	}
}

func TestApplyBootstrapSettingsNormalizedServiceSetControlsRestart(t *testing.T) {
	t.Run("added service requires restart", func(t *testing.T) {
		db := newBootstrapTestDB(t)
		if err := db.Create(&models.BasicSettings{
			ID:       1,
			Services: []models.AvailableService{models.SambaServer},
		}).Error; err != nil {
			t.Fatalf("failed to seed settings: %v", err)
		}
		service := &Service{
			DB:                         db,
			bootstrapServicePrecheckFn: func(models.AvailableService) error { return nil },
		}

		result, err := service.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
			Services: []models.AvailableService{models.SambaServer, models.Jails},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.RestartRequired {
			t.Fatalf("adding a service must require a restart")
		}
		if item := itemAt(t, result, 0); item.Name != string(models.SambaServer) ||
			item.Status != systemServiceInterfaces.BootstrapApplied {
			t.Fatalf("samba item = %+v; want applied", item)
		}
		settings := loadBootstrapSettings(t, db)
		if !reflect.DeepEqual(settings.Services, []models.AvailableService{models.SambaServer, models.Jails}) {
			t.Fatalf("services = %v", settings.Services)
		}
	})

	t.Run("reordered same set does not require restart", func(t *testing.T) {
		db := newBootstrapTestDB(t)
		if err := db.Create(&models.BasicSettings{
			ID:       1,
			Services: []models.AvailableService{models.Jails, models.SambaServer},
		}).Error; err != nil {
			t.Fatalf("failed to seed settings: %v", err)
		}
		service := &Service{
			DB:                         db,
			bootstrapServicePrecheckFn: func(models.AvailableService) error { return nil },
		}

		result, err := service.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
			Services: []models.AvailableService{models.SambaServer, models.Jails},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.RestartRequired {
			t.Fatalf("reordered identical set must not require a restart")
		}
		if item := itemAt(t, result, 0); item.Name != string(models.SambaServer) ||
			item.Status != systemServiceInterfaces.BootstrapApplied {
			t.Fatalf("samba item = %+v; want applied", item)
		}
	})
}

func TestApplyBootstrapSettingsPersistenceFailureDowngradesAcceptedItems(t *testing.T) {
	db := newBootstrapTestDB(t)
	if err := db.Create(&models.BasicSettings{ID: 1}).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}
	if err := db.Callback().Update().Before("gorm:update").Register(
		"force_bootstrap_settings_update_failure",
		func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "basic_settings" {
				tx.AddError(errors.New("forced settings persistence failure"))
			}
		},
	); err != nil {
		t.Fatalf("failed to install update callback: %v", err)
	}

	service := &Service{
		DB: db,
		ensureBootstrapPoolFn: func(context.Context, string) (bool, error) {
			return false, nil
		},
		bootstrapServicePrecheckFn: func(models.AvailableService) error { return nil },
	}

	result, err := service.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
		Initialized: true,
		Pools:       []string{"tank"},
		Services:    []models.AvailableService{models.Jails},
	})
	if err == nil {
		t.Fatal("expected a global persistence error")
	}
	if result.RestartRequired {
		t.Fatalf("persistence failure must not signal a restart")
	}
	for _, item := range result.Items {
		if item.Status == systemServiceInterfaces.BootstrapApplied {
			t.Fatalf("accepted item falsely reported as applied after persistence failure: %+v", item)
		}
	}

	settings := loadBootstrapSettings(t, db)
	if settings.Initialized || len(settings.Pools) != 0 || len(settings.Services) != 0 {
		t.Fatalf("settings were partially persisted: %+v", settings)
	}
}

func TestApplyBootstrapSettingsEnsuresPoolBeforeRegistering(t *testing.T) {
	db := newBootstrapTestDB(t)
	observedExistingRegistration := false
	service := &Service{
		DB: db,
		ensureBootstrapPoolFn: func(_ context.Context, name string) (bool, error) {
			var settings models.BasicSettings
			err := db.First(&settings).Error
			if err == nil {
				for _, pool := range settings.Pools {
					if pool == name {
						observedExistingRegistration = true
					}
				}
			}
			return false, nil
		},
	}

	if _, err := service.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
		Pools: []string{"tank"},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if observedExistingRegistration {
		t.Fatal("pool was registered before the namespace datasets were ensured")
	}

	settings := loadBootstrapSettings(t, db)
	if !reflect.DeepEqual(settings.Pools, []string{"tank"}) {
		t.Fatalf("pools = %v; want [tank]", settings.Pools)
	}
}

func TestApplyBootstrapSettingsJailsRacctWarningDoesNotAutoconfigure(t *testing.T) {
	db := newBootstrapTestDB(t)
	service := &Service{
		DB: db,
		bootstrapServicePrecheckFn: func(models.AvailableService) error {
			return errors.New("jails_racct_not_enabled")
		},
	}

	result, err := service.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
		Services: []models.AvailableService{models.Jails},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	item := itemAt(t, result, 0)
	if item.Status != systemServiceInterfaces.BootstrapWarning {
		t.Fatalf("item = %+v; want warning", item)
	}
	if result.RestartRequired {
		t.Fatalf("racct warning must not require a restart")
	}
}

func TestApplyBootstrapSettingsDefaultEnsureUsesZFS(t *testing.T) {
	db := newBootstrapTestDB(t)
	if err := db.Create(&models.BasicSettings{ID: 1}).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}

	runner := newSettingsPoolRunner()
	service := &Service{
		DB:   db,
		GZFS: gzfs.NewClient(gzfs.Options{Runner: runner}),
	}

	result, err := service.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
		Pools: []string{"tank"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item := itemAt(t, result, 0); item.Status != systemServiceInterfaces.BootstrapApplied {
		t.Fatalf("pool item = %+v; want applied", item)
	}
	for _, dataset := range []string{
		"tank/sylve/bootstraps",
		"tank/sylve/jails",
		"tank/sylve/virtual-machines",
		"tank/sylve",
	} {
		if !runner.created[dataset] {
			t.Fatalf("expected dataset %s to be created; created=%v", dataset, runner.created)
		}
	}

	settings := loadBootstrapSettings(t, db)
	if !reflect.DeepEqual(settings.Pools, []string{"tank"}) {
		t.Fatalf("pools = %v; want [tank]", settings.Pools)
	}
}

func TestApplyBootstrapSettingsSerializesConcurrentApplies(t *testing.T) {
	db := newBootstrapTestDB(t)
	if err := db.Create(&models.BasicSettings{ID: 1}).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}

	service := &Service{
		DB: db,
		ensureBootstrapPoolFn: func(context.Context, string) (bool, error) {
			return false, nil
		},
	}

	pools := []string{"tank", "zroot"}
	var wg sync.WaitGroup
	for _, pool := range pools {
		wg.Add(1)
		go func(poolName string) {
			defer wg.Done()
			if _, err := service.ApplyBootstrapSettings(context.Background(), systemServiceInterfaces.BootstrapSettingsRequest{
				Pools: []string{poolName},
			}); err != nil {
				t.Errorf("concurrent apply failed: %v", err)
			}
		}(pool)
	}
	wg.Wait()

	settings := loadBootstrapSettings(t, db)
	persisted := append([]string(nil), settings.Pools...)
	slices.Sort(persisted)
	want := append([]string(nil), pools...)
	slices.Sort(want)
	if !reflect.DeepEqual(persisted, want) {
		t.Fatalf("persisted pools = %v; want %v", persisted, want)
	}
}

func TestApplyBootstrapSettingsNamespaceRepairInvalidatesCachePerApply(t *testing.T) {
	db := newBootstrapTestDB(t)
	if err := db.Create(&models.BasicSettings{ID: 1, Pools: []string{"tank"}}).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}

	settingsUpdates := 0
	if err := db.Callback().Update().Before("gorm:update").Register(
		"count_basic_settings_updates",
		func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "basic_settings" {
				settingsUpdates++
			}
		},
	); err != nil {
		t.Fatalf("failed to install update counter: %v", err)
	}

	runner := newSettingsPoolRunner()
	hookCalls := 0
	service := &Service{
		DB:   db,
		GZFS: gzfs.NewClient(gzfs.Options{Runner: runner}),
	}
	service.OnUsablePoolsChanged = func(context.Context) error {
		hookCalls++
		return nil
	}

	result, err := service.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
		Pools: []string{"tank"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item := itemAt(t, result, 0); item.Status != systemServiceInterfaces.BootstrapApplied {
		t.Fatalf("pool item = %+v; want applied", item)
	}
	if !runner.created["tank/sylve"] {
		t.Fatalf("expected namespace repair to create datasets; created=%v", runner.created)
	}
	firstGeneration := zfsCacheGeneration(t, db, dbpkg.ZFSCacheKindGenericDataset)
	if firstGeneration == 0 {
		t.Fatalf("expected zfs caches invalidated after namespace repair")
	}
	datasetsAfterFirst := len(runner.created)

	result, err = service.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
		Pools: []string{"tank"},
	})
	if err != nil {
		t.Fatalf("unexpected error on repeat: %v", err)
	}
	if result.RestartRequired {
		t.Fatalf("namespace repair must not require a restart")
	}
	if item := itemAt(t, result, 0); item.Status != systemServiceInterfaces.BootstrapApplied {
		t.Fatalf("repeat pool item = %+v; want applied", item)
	}
	if len(runner.created) != datasetsAfterFirst {
		t.Fatalf("datasets recreated on repeat: %v", runner.created)
	}
	if got := zfsCacheGeneration(t, db, dbpkg.ZFSCacheKindGenericDataset); got != firstGeneration+1 {
		t.Fatalf("cache generation = %d; want %d (one bump per successful ensure)", got, firstGeneration+1)
	}
	if settingsUpdates != 0 {
		t.Fatalf("settings row updated %d time(s); want 0 for unchanged settings", settingsUpdates)
	}
	if hookCalls != 0 {
		t.Fatalf("telemetry callback calls = %d; want 0 when pool registration is unchanged", hookCalls)
	}
}

func TestApplyBootstrapSettingsCacheInvalidationFailureConvergesOnRetry(t *testing.T) {
	db := newBootstrapTestDB(t)
	if err := db.Create(&models.BasicSettings{ID: 1, Pools: []string{"tank"}}).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}

	settingsUpdates := 0
	if err := db.Callback().Update().Before("gorm:update").Register(
		"count_basic_settings_updates",
		func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "basic_settings" {
				settingsUpdates++
			}
		},
	); err != nil {
		t.Fatalf("failed to install update counter: %v", err)
	}

	failNextCacheInvalidation := true
	if err := db.Callback().Raw().Before("gorm:raw").Register(
		"fail_first_cache_invalidation",
		func(tx *gorm.DB) {
			if !strings.Contains(tx.Statement.SQL.String(), "zfs_cache_invalidations") {
				return
			}
			if failNextCacheInvalidation {
				failNextCacheInvalidation = false
				tx.AddError(errors.New("transient cache invalidation failure"))
			}
		},
	); err != nil {
		t.Fatalf("failed to install raw failure callback: %v", err)
	}

	runner := newSettingsPoolRunner()
	first := &Service{DB: db, GZFS: gzfs.NewClient(gzfs.Options{Runner: runner})}
	_, err := first.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
		Pools: []string{"tank"},
	})
	if err == nil {
		t.Fatal("expected cache invalidation failure")
	}
	if !strings.Contains(err.Error(), "failed_to_invalidate_zfs_caches") {
		t.Fatalf("error = %v; want cache invalidation failure", err)
	}
	if !runner.created["tank/sylve"] {
		t.Fatalf("expected datasets created before the invalidation failure")
	}
	if got := zfsCacheGeneration(t, db, dbpkg.ZFSCacheKindGenericDataset); got != 0 {
		t.Fatalf("cache generation = %d; want 0 after failed invalidation", got)
	}

	retry := &Service{DB: db, GZFS: gzfs.NewClient(gzfs.Options{Runner: runner})}
	result, err := retry.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
		Pools: []string{"tank"},
	})
	if err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if item := itemAt(t, result, 0); item.Status != systemServiceInterfaces.BootstrapApplied {
		t.Fatalf("retry pool item = %+v; want applied", item)
	}
	if got := zfsCacheGeneration(t, db, dbpkg.ZFSCacheKindGenericDataset); got == 0 {
		t.Fatal("retry did not invalidate caches")
	}
	if settingsUpdates != 0 {
		t.Fatalf("settings row updated %d time(s); want 0", settingsUpdates)
	}
}

func TestApplyBootstrapSettingsNoPoolsDoesNotInvalidateCaches(t *testing.T) {
	db := newBootstrapTestDB(t)
	if err := db.Create(&models.BasicSettings{ID: 1}).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}

	service := &Service{
		DB: db,
		ensureBootstrapPoolFn: func(context.Context, string) (bool, error) {
			return false, errors.New("pool_not_found_bad")
		},
	}

	result, err := service.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
		Pools: []string{"bad"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item := itemAt(t, result, 0); item.Status != systemServiceInterfaces.BootstrapFailed {
		t.Fatalf("pool item = %+v; want failed", item)
	}
	if got := zfsCacheGeneration(t, db, dbpkg.ZFSCacheKindGenericDataset); got != 0 {
		t.Fatalf("cache generation = %d; want 0 when all ensures failed with no datasets created", got)
	}
}

func TestApplyBootstrapSettingsNilGZFSFailsPoolItem(t *testing.T) {
	db := newBootstrapTestDB(t)
	if err := db.Create(&models.BasicSettings{ID: 1, Pools: []string{"tank"}}).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}

	service := &Service{DB: db}
	result, err := service.ApplyBootstrapSettings(t.Context(), systemServiceInterfaces.BootstrapSettingsRequest{
		Pools: []string{"tank"},
	})
	if err != nil {
		t.Fatalf("unexpected global error: %v", err)
	}
	item := itemAt(t, result, 0)
	if item.Status != systemServiceInterfaces.BootstrapFailed {
		t.Fatalf("item = %+v; want failed", item)
	}
	if !strings.Contains(item.Message, "zfs_client_not_configured") {
		t.Fatalf("message = %q; want zfs_client_not_configured", item.Message)
	}
}

func TestInitializeHoldsSharedLocksDuringCommit(t *testing.T) {
	db := newBootstrapTestDB(t)
	if err := db.Create(&models.BasicSettings{ID: 1}).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	if err := db.Callback().Query().After("gorm:query").Register(
		"bootstrap_block_initialize_query",
		func(tx *gorm.DB) {
			if tx.Statement.Schema == nil || tx.Statement.Schema.Table != "basic_settings" {
				return
			}
			once.Do(func() { close(entered) })
			<-release
		},
	); err != nil {
		t.Fatalf("failed to install query callback: %v", err)
	}

	service := &Service{DB: db}
	done := make(chan []error, 1)
	go func() {
		done <- service.Initialize(context.Background(), systemServiceInterfaces.InitializeRequest{})
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for initialize to reach its critical section")
	}

	if service.initMutex.TryLock() {
		service.initMutex.Unlock()
		t.Fatal("Initialize did not hold initMutex")
	}
	if service.serviceSettingsMutex.TryLock() {
		service.serviceSettingsMutex.Unlock()
		t.Fatal("Initialize did not hold serviceSettingsMutex")
	}

	close(release)
	select {
	case errs := <-done:
		if len(errs) != 0 {
			t.Fatalf("unexpected initialization errors: %v", errs)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for initialize to finish")
	}
}

func TestApplyBootstrapSettingsHoldsSharedLocksDuringApply(t *testing.T) {
	db := newBootstrapTestDB(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	service := &Service{
		DB: db,
		ensureBootstrapPoolFn: func(context.Context, string) (bool, error) {
			once.Do(func() { close(entered) })
			<-release
			return false, nil
		},
	}

	done := make(chan error, 1)
	go func() {
		_, err := service.ApplyBootstrapSettings(context.Background(), systemServiceInterfaces.BootstrapSettingsRequest{
			Pools: []string{"tank"},
		})
		done <- err
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for bootstrap to reach its critical section")
	}

	if service.initMutex.TryLock() {
		service.initMutex.Unlock()
		t.Fatal("ApplyBootstrapSettings did not hold initMutex")
	}
	if service.serviceSettingsMutex.TryLock() {
		service.serviceSettingsMutex.Unlock()
		t.Fatal("ApplyBootstrapSettings did not hold serviceSettingsMutex")
	}

	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unexpected bootstrap error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for bootstrap to finish")
	}
}

func TestInitializeAndBootstrapDoNotDeadlock(t *testing.T) {
	db := newBootstrapTestDB(t)
	if err := db.Create(&models.BasicSettings{ID: 1}).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}
	service := &Service{
		DB: db,
		ensureBootstrapPoolFn: func(context.Context, string) (bool, error) {
			return false, nil
		},
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		service.Initialize(context.Background(), systemServiceInterfaces.InitializeRequest{})
	}()
	go func() {
		defer wg.Done()
		_, _ = service.ApplyBootstrapSettings(context.Background(), systemServiceInterfaces.BootstrapSettingsRequest{
			Pools: []string{"tank"},
		})
	}()

	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("Initialize and bootstrap deadlocked")
	}
}

func TestInitializeUpdatesExistingUninitializedRow(t *testing.T) {
	db := newBootstrapTestDB(t)
	if err := db.Create(&models.BasicSettings{
		ID:          1,
		Pools:       []string{"stale"},
		Initialized: false,
		Restarted:   true,
	}).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}

	service := &Service{DB: db}
	errs := service.Initialize(t.Context(), systemServiceInterfaces.InitializeRequest{})
	if len(errs) != 0 {
		t.Fatalf("unexpected initialization errors: %v", errs)
	}

	var rows []models.BasicSettings
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("failed to list settings: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected a single settings row, got %d", len(rows))
	}
	if rows[0].ID != 1 {
		t.Fatalf("row ID = %d; want 1", rows[0].ID)
	}
	if !rows[0].Initialized {
		t.Fatalf("row was not initialized: %+v", rows[0])
	}
	if len(rows[0].Pools) != 0 {
		t.Fatalf("pools = %v; want empty replacement", rows[0].Pools)
	}
	if rows[0].Restarted {
		t.Fatalf("restarted flag = true; want false after initialization")
	}
}

func TestInitializeStillRejectsAlreadyInitializedRow(t *testing.T) {
	db := newBootstrapTestDB(t)
	if err := db.Create(&models.BasicSettings{ID: 1, Initialized: true}).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}

	service := &Service{DB: db}
	errs := service.Initialize(t.Context(), systemServiceInterfaces.InitializeRequest{})
	if len(errs) != 1 {
		t.Fatalf("errors = %v; want one conflict", errs)
	}
	if ClassifyInitializationError(errs[0]) != InitializationErrorConflict {
		t.Fatalf("classification = %v; want conflict", ClassifyInitializationError(errs[0]))
	}
}
