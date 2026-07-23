// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

func newJailDeleteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Storage{},
		&jailModels.Network{},
		&jailModels.JailHooks{},
		&jailModels.JailStats{},
		&jailModels.JailSnapshot{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
	)
}

func seedJailDeleteGraph(t *testing.T, db *gorm.DB, ctID uint, pool string, withMAC bool) (uint, uint) {
	t.Helper()

	jail := jailModels.Jail{
		CTID: ctID,
		Name: fmt.Sprintf("delete-jail-%d", ctID),
		Type: jailModels.JailTypeFreeBSD,
	}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("seed jail: %v", err)
	}

	storage := jailModels.Storage{
		JailID: jail.ID,
		Pool:   pool,
		GUID:   fmt.Sprintf("delete-guid-%d", ctID),
		Name:   "Base Filesystem",
		IsBase: true,
	}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatalf("seed jail storage: %v", err)
	}

	var macID uint
	if withMAC {
		mac := networkModels.Object{
			Name: fmt.Sprintf("delete-mac-%d", ctID),
			Type: "Mac",
		}
		if err := db.Create(&mac).Error; err != nil {
			t.Fatalf("seed MAC object: %v", err)
		}
		macID = mac.ID
		if err := db.Create(&networkModels.ObjectEntry{
			ObjectID: mac.ID,
			Value:    "02:00:00:00:00:01",
		}).Error; err != nil {
			t.Fatalf("seed MAC entry: %v", err)
		}
		if err := db.Create(&networkModels.ObjectResolution{
			ObjectID:      mac.ID,
			ResolvedValue: "02:00:00:00:00:01",
		}).Error; err != nil {
			t.Fatalf("seed MAC resolution: %v", err)
		}
	}

	network := jailModels.Network{
		JailID:     jail.ID,
		Name:       "epair0",
		SwitchID:   999999,
		SwitchType: "standard",
	}
	if macID != 0 {
		network.MacID = &macID
	}
	if err := db.Create(&network).Error; err != nil {
		t.Fatalf("seed jail network: %v", err)
	}

	if err := db.Create(&jailModels.JailHooks{
		JailID: jail.ID,
		Phase:  jailModels.JailHookPhaseStart,
	}).Error; err != nil {
		t.Fatalf("seed jail hook: %v", err)
	}
	if err := db.Create(&jailModels.JailStats{JailID: jail.ID}).Error; err != nil {
		t.Fatalf("seed jail stats: %v", err)
	}
	if err := db.Create(&jailModels.JailSnapshot{
		JailID:       jail.ID,
		CTID:         ctID,
		Name:         "delete-snapshot",
		SnapshotName: fmt.Sprintf("delete_snapshot_%d", ctID),
		RootDataset:  fmt.Sprintf("%s/sylve/jails/%d", pool, ctID),
	}).Error; err != nil {
		t.Fatalf("seed jail snapshot: %v", err)
	}

	return jail.ID, macID
}

func inactiveJailDeleteRuntime() jailDeleteRuntime {
	return jailDeleteRuntime{
		isRunning: func(uint) (bool, error) { return false, nil },
		stop:      func(uint) error { return nil },
		removeConfig: func(string) error {
			return nil
		},
		removeDevfs: func(uint) error { return nil },
	}
}

type jailDeleteNetworkService struct {
	jailNetworkValidationFakeNetworkService
	deletedEpairs []string
	deleteErr     error
}

func (s *jailDeleteNetworkService) DeleteEpair(name string) error {
	s.deletedEpairs = append(s.deletedEpairs, name)
	return s.deleteErr
}

func countJailDeleteRows(t *testing.T, db *gorm.DB, model any, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := db.Model(model).Where(query, args...).Count(&count).Error; err != nil {
		t.Fatalf("count %T rows: %v", model, err)
	}
	return count
}

func assertJailDeleteGraphAbsent(t *testing.T, db *gorm.DB, jailID, ctID uint) {
	t.Helper()
	checks := []struct {
		model any
		query string
		arg   uint
	}{
		{&jailModels.Jail{}, "ct_id = ?", ctID},
		{&jailModels.Storage{}, "jid = ?", jailID},
		{&jailModels.Network{}, "jid = ?", jailID},
		{&jailModels.JailHooks{}, "jid = ?", jailID},
		{&jailModels.JailStats{}, "jid = ?", jailID},
		{&jailModels.JailSnapshot{}, "jid = ?", jailID},
	}
	for _, check := range checks {
		if count := countJailDeleteRows(t, db, check.model, check.query, check.arg); count != 0 {
			t.Fatalf("expected no %T rows after delete, got %d", check.model, count)
		}
	}
}

func TestDeleteJailRetainsRootAndDeletesMinimalDatabaseGraph(t *testing.T) {
	dataPath := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", dataPath)

	db := newJailDeleteTestDB(t)
	const ctID uint = 641
	jailID, macID := seedJailDeleteGraph(t, db, ctID, "tank", true)

	jailDir := filepath.Join(dataPath, "jails", fmt.Sprintf("%d", ctID))
	if err := os.MkdirAll(jailDir, 0755); err != nil {
		t.Fatalf("create jail runtime directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jailDir, fmt.Sprintf("%d.conf", ctID)), []byte("test"), 0644); err != nil {
		t.Fatalf("create jail runtime config: %v", err)
	}

	runtime := inactiveJailDeleteRuntime()
	runtime.removeConfig = os.RemoveAll
	service := &Service{DB: db}
	result, err := service.deleteJailWithRuntime(t.Context(), ctID, false, false, runtime)
	if err != nil {
		t.Fatalf("delete jail while retaining root: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", result.Warnings)
	}
	wantDataset := "tank/sylve/jails/641"
	if len(result.RetainedDatasets) != 1 || result.RetainedDatasets[0] != wantDataset {
		t.Fatalf("retained datasets = %v, want [%s]", result.RetainedDatasets, wantDataset)
	}
	if _, err := os.Stat(jailDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("jail runtime directory still exists or stat failed: %v", err)
	}

	assertJailDeleteGraphAbsent(t, db, jailID, ctID)
	if count := countJailDeleteRows(t, db, &networkModels.Object{}, "id = ?", macID); count != 1 {
		t.Fatalf("MAC object should be retained when deleteMacs=false, got count %d", count)
	}
}

func TestDeleteJailCleansRecordedEpairsBeforeRemovingNetworks(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	db := newJailDeleteTestDB(t)
	const ctID uint = 642
	jailID, _ := seedJailDeleteGraph(t, db, ctID, "tank", false)
	var networkIDs []uint
	if err := db.Model(&jailModels.Network{}).Where("jid = ?", jailID).Pluck("id", &networkIDs).Error; err != nil {
		t.Fatalf("load seeded jail network: %v", err)
	}
	if len(networkIDs) != 1 {
		t.Fatalf("seeded jail network IDs = %v, want one ID", networkIDs)
	}
	network := &jailDeleteNetworkService{}
	service := &Service{DB: db, NetworkService: network}

	result, err := service.deleteJailWithRuntime(t.Context(), ctID, false, false, inactiveJailDeleteRuntime())
	if err != nil {
		t.Fatalf("delete jail: %v", err)
	}
	wantEpair := fmt.Sprintf("%s_net%d", utils.HashIntToNLetters(int(ctID), 5), networkIDs[0])
	if fmt.Sprint(network.deletedEpairs) != fmt.Sprint([]string{wantEpair}) {
		t.Fatalf("deleted epairs = %v, want [%s]", network.deletedEpairs, wantEpair)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", result.Warnings)
	}
	assertJailDeleteGraphAbsent(t, db, jailID, ctID)
}

func TestDeleteJailReportsEpairCleanupRefusalAndRemovesIdentity(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	db := newJailDeleteTestDB(t)
	const ctID uint = 643
	jailID, _ := seedJailDeleteGraph(t, db, ctID, "tank", false)
	network := &jailDeleteNetworkService{deleteErr: errors.New("refusing to delete unmanaged epair")}
	service := &Service{DB: db, NetworkService: network}

	result, err := service.deleteJailWithRuntime(t.Context(), ctID, false, false, inactiveJailDeleteRuntime())
	if err != nil {
		t.Fatalf("delete jail: %v", err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "epair_cleanup_incomplete") {
		t.Fatalf("warnings = %v, want epair cleanup warning", result.Warnings)
	}
	assertJailDeleteGraphAbsent(t, db, jailID, ctID)
}

func TestDeleteJailRuntimeFailuresKeepIdentityAndSkipStorageCleanup(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*jailDeleteRuntime)
		wantError string
	}{
		{
			name: "runtime state lookup",
			configure: func(runtime *jailDeleteRuntime) {
				runtime.isRunning = func(uint) (bool, error) { return false, errors.New("state unavailable") }
			},
			wantError: "failed_to_check_jail_runtime_before_delete",
		},
		{
			name: "stop failure",
			configure: func(runtime *jailDeleteRuntime) {
				runtime.isRunning = func(uint) (bool, error) { return true, nil }
				runtime.stop = func(uint) error { return errors.New("stop failed") }
			},
			wantError: "failed_to_stop_jail_before_delete",
		},
		{
			name: "runtime config removal",
			configure: func(runtime *jailDeleteRuntime) {
				runtime.removeConfig = func(string) error { return errors.New("remove failed") }
			},
			wantError: "failed_to_remove_jail_directory",
		},
		{
			name: "devfs identity removal",
			configure: func(runtime *jailDeleteRuntime) {
				runtime.removeDevfs = func(uint) error { return errors.New("devfs failed") }
			},
			wantError: "failed_to_remove_jail_devfs_rules",
		},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SYLVE_DATA_PATH", t.TempDir())
			db := newJailDeleteTestDB(t)
			ctID := uint(650 + index)
			jailID, _ := seedJailDeleteGraph(t, db, ctID, "tank", false)
			runtime := inactiveJailDeleteRuntime()
			tt.configure(&runtime)

			service := &Service{DB: db}
			_, err := service.deleteJailWithRuntime(t.Context(), ctID, false, true, runtime)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("delete error = %v, want %q", err, tt.wantError)
			}
			if count := countJailDeleteRows(t, db, &jailModels.Jail{}, "id = ?", jailID); count != 1 {
				t.Fatalf("jail identity count = %d, want 1", count)
			}
			if count := countJailDeleteRows(t, db, &jailModels.Storage{}, "jid = ?", jailID); count != 1 {
				t.Fatalf("storage metadata count = %d, want 1", count)
			}
		})
	}
}

func TestDeleteJailFinalRuntimeRevalidationKeepsIdentity(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	db := newJailDeleteTestDB(t)
	const ctID uint = 660
	jailID, _ := seedJailDeleteGraph(t, db, ctID, "tank", false)

	checks := 0
	runtime := inactiveJailDeleteRuntime()
	runtime.isRunning = func(uint) (bool, error) {
		checks++
		return checks > 1, nil
	}

	service := &Service{DB: db}
	_, err := service.deleteJailWithRuntime(t.Context(), ctID, false, true, runtime)
	if err == nil || !strings.Contains(err.Error(), "jail_became_active_before_delete") {
		t.Fatalf("delete error = %v, want final runtime conflict", err)
	}
	if count := countJailDeleteRows(t, db, &jailModels.Jail{}, "id = ?", jailID); count != 1 {
		t.Fatalf("jail identity count = %d, want 1", count)
	}
}

func TestDeleteJailStopVerificationCancellationKeepsIdentity(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	db := newJailDeleteTestDB(t)
	const ctID uint = 661
	jailID, _ := seedJailDeleteGraph(t, db, ctID, "tank", false)

	runtime := inactiveJailDeleteRuntime()
	runtime.isRunning = func(uint) (bool, error) { return true, nil }

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	service := &Service{DB: db}
	_, err := service.deleteJailWithRuntime(ctx, ctID, false, true, runtime)
	if err == nil || !strings.Contains(err.Error(), "jail_stop_verification_canceled") {
		t.Fatalf("delete error = %v, want canceled stop verification", err)
	}
	if count := countJailDeleteRows(t, db, &jailModels.Jail{}, "id = ?", jailID); count != 1 {
		t.Fatalf("jail identity count = %d, want 1", count)
	}
}

func TestDeleteJailDatabaseFailureRollsBackIdentityAndSkipsZFS(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	db := newJailDeleteTestDB(t)
	const ctID uint = 670
	jailID, _ := seedJailDeleteGraph(t, db, ctID, "tank", false)

	injected := errors.New("injected jail delete failure")
	if err := db.Callback().Delete().Before("gorm:delete").Register("test:fail_jail_delete", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Jail" {
			tx.AddError(injected)
		}
	}); err != nil {
		t.Fatalf("register delete callback: %v", err)
	}

	service := &Service{DB: db}
	_, err := service.deleteJailWithRuntime(t.Context(), ctID, false, true, inactiveJailDeleteRuntime())
	if err == nil || !strings.Contains(err.Error(), injected.Error()) {
		t.Fatalf("delete error = %v, want injected DB failure", err)
	}
	if count := countJailDeleteRows(t, db, &jailModels.Jail{}, "id = ?", jailID); count != 1 {
		t.Fatalf("jail identity count = %d, want 1", count)
	}
	if count := countJailDeleteRows(t, db, &jailModels.Network{}, "jid = ?", jailID); count != 1 {
		t.Fatalf("network count = %d, want transaction rollback", count)
	}
	if count := countJailDeleteRows(t, db, &jailModels.Storage{}, "jid = ?", jailID); count != 1 {
		t.Fatalf("storage count = %d, want transaction rollback", count)
	}
}

func TestDeleteJailMACCleanupFailureIsOnlyAWarning(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	db := newJailDeleteTestDB(t)
	const ctID uint = 680
	jailID, macID := seedJailDeleteGraph(t, db, ctID, "tank", true)

	injected := errors.New("injected MAC cleanup failure")
	if err := db.Callback().Delete().Before("gorm:delete").Register("test:fail_mac_delete", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Object" {
			tx.AddError(injected)
		}
	}); err != nil {
		t.Fatalf("register MAC delete callback: %v", err)
	}

	service := &Service{DB: db}
	result, err := service.deleteJailWithRuntime(t.Context(), ctID, true, false, inactiveJailDeleteRuntime())
	if err != nil {
		t.Fatalf("delete jail: %v", err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "mac_cleanup_incomplete") {
		t.Fatalf("warnings = %v, want MAC cleanup warning", result.Warnings)
	}
	assertJailDeleteGraphAbsent(t, db, jailID, ctID)
	if count := countJailDeleteRows(t, db, &networkModels.Object{}, "id = ?", macID); count != 1 {
		t.Fatalf("MAC object count = %d, want failed cleanup to roll back", count)
	}
}

func TestDeleteJailRetainedRootIsUntouchedRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS jail deletion integration test in short mode")
	}

	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

	const ctID uint = 691
	datasetName := fmt.Sprintf("%s/sylve/jails/%d", pool, ctID)
	zfstest.EnsureDataset(t, client, datasetName)
	dataset, err := client.ZFS.Get(t.Context(), datasetName, false)
	if err != nil {
		t.Fatalf("get jail dataset: %v", err)
	}
	beforeGUID := dataset.GUID
	markerPath := filepath.Join(dataset.Mountpoint, "retained-marker")
	if err := os.WriteFile(markerPath, []byte("retained"), 0644); err != nil {
		t.Fatalf("write retained marker: %v", err)
	}

	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	db := newJailDeleteTestDB(t)
	jailID, _ := seedJailDeleteGraph(t, db, ctID, pool, false)
	service := &Service{DB: db, GZFS: client}
	result, err := service.deleteJailWithRuntime(t.Context(), ctID, false, false, inactiveJailDeleteRuntime())
	if err != nil {
		t.Fatalf("delete jail retaining real dataset: %v", err)
	}
	if len(result.RetainedDatasets) != 1 || result.RetainedDatasets[0] != datasetName {
		t.Fatalf("retained datasets = %v, want [%s]", result.RetainedDatasets, datasetName)
	}
	assertJailDeleteGraphAbsent(t, db, jailID, ctID)

	after, err := client.ZFS.Get(t.Context(), datasetName, false)
	if err != nil {
		t.Fatalf("retained dataset is missing: %v", err)
	}
	if after.GUID != beforeGUID {
		t.Fatalf("retained dataset GUID changed from %q to %q", beforeGUID, after.GUID)
	}
	contents, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read retained marker: %v", err)
	}
	if string(contents) != "retained" {
		t.Fatalf("retained marker contents = %q", contents)
	}
}

func TestDeleteJailStorageCleanupFailureReleasesIdentityRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS jail cleanup-failure integration test in short mode")
	}

	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

	const ctID uint = 692
	datasetName := fmt.Sprintf("%s/sylve/jails/%d", pool, ctID)
	zfstest.EnsureDataset(t, client, datasetName)
	dataset, err := client.ZFS.Get(t.Context(), datasetName, false)
	if err != nil {
		t.Fatalf("get jail dataset: %v", err)
	}
	beforeGUID := dataset.GUID
	heldSnapshot, err := dataset.Snapshot(t.Context(), "held_for_delete_test", false)
	if err != nil {
		t.Fatalf("create held snapshot: %v", err)
	}
	const holdTag = "sylve-delete-test"
	if out, err := exec.Command("zfs", "hold", holdTag, heldSnapshot.Name).CombinedOutput(); err != nil {
		t.Fatalf("hold snapshot: %v, output: %s", err, strings.TrimSpace(string(out)))
	}
	defer func() {
		_, _ = exec.Command("zfs", "release", holdTag, heldSnapshot.Name).CombinedOutput()
	}()

	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	db := newJailDeleteTestDB(t)
	jailID, _ := seedJailDeleteGraph(t, db, ctID, pool, false)
	service := &Service{DB: db, GZFS: client}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := service.deleteJailWithRuntime(ctx, ctID, false, true, inactiveJailDeleteRuntime())
	if err != nil {
		t.Fatalf("storage cleanup failure should not fail jail deletion: %v", err)
	}
	if len(result.Warnings) != 1 || !strings.HasPrefix(result.Warnings[0], "storage_cleanup_incomplete") {
		t.Fatalf("warnings = %v, want storage cleanup warning", result.Warnings)
	}
	if len(result.RetainedDatasets) != 1 || result.RetainedDatasets[0] != datasetName {
		t.Fatalf("retained datasets = %v, want [%s]", result.RetainedDatasets, datasetName)
	}
	assertJailDeleteGraphAbsent(t, db, jailID, ctID)

	after, err := client.ZFS.Get(context.Background(), datasetName, false)
	if err != nil {
		t.Fatalf("failed cleanup unexpectedly removed dataset: %v", err)
	}
	if after.GUID != beforeGUID {
		t.Fatalf("failed cleanup changed dataset GUID from %q to %q", beforeGUID, after.GUID)
	}
}

func TestDeleteJailDependentCloneSurvivesCleanupRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS dependent-clone jail deletion integration test in short mode")
	}

	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

	const ctID uint = 693
	datasetName := fmt.Sprintf("%s/sylve/jails/%d", pool, ctID)
	cloneName := fmt.Sprintf("%s/external-dependent-clone-%d", pool, ctID)
	zfstest.EnsureDataset(t, client, datasetName)
	dataset, err := client.ZFS.Get(t.Context(), datasetName, false)
	if err != nil {
		t.Fatalf("get jail dataset: %v", err)
	}
	rootGUID := dataset.GUID
	snapshot, err := dataset.Snapshot(t.Context(), "clone_source", false)
	if err != nil {
		t.Fatalf("create clone source snapshot: %v", err)
	}
	clone, err := snapshot.Clone(t.Context(), cloneName, nil)
	if err != nil {
		t.Fatalf("create external dependent clone: %v", err)
	}
	cloneGUID := clone.GUID

	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	db := newJailDeleteTestDB(t)
	jailID, _ := seedJailDeleteGraph(t, db, ctID, pool, false)
	service := &Service{DB: db, GZFS: client}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := service.deleteJailWithRuntime(ctx, ctID, false, true, inactiveJailDeleteRuntime())
	if err != nil {
		t.Fatalf("dependent clone must only produce a cleanup warning: %v", err)
	}
	if len(result.Warnings) != 1 || !strings.HasPrefix(result.Warnings[0], "storage_cleanup_incomplete") {
		t.Fatalf("warnings = %v, want storage cleanup warning", result.Warnings)
	}
	if len(result.RetainedDatasets) != 1 || result.RetainedDatasets[0] != datasetName {
		t.Fatalf("retained datasets = %v, want [%s]", result.RetainedDatasets, datasetName)
	}
	assertJailDeleteGraphAbsent(t, db, jailID, ctID)

	afterRoot, err := client.ZFS.Get(context.Background(), datasetName, false)
	if err != nil {
		t.Fatalf("dependent-clone cleanup removed the jail root: %v", err)
	}
	if afterRoot.GUID != rootGUID {
		t.Fatalf("jail root GUID changed from %q to %q", rootGUID, afterRoot.GUID)
	}
	afterClone, err := client.ZFS.Get(context.Background(), cloneName, false)
	if err != nil {
		t.Fatalf("dependent-clone cleanup removed the external clone: %v", err)
	}
	if afterClone.GUID != cloneGUID {
		t.Fatalf("external clone GUID changed from %q to %q", cloneGUID, afterClone.GUID)
	}
}
