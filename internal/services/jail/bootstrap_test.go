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
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
)

type bootstrapTestSystemService struct {
	systemServiceInterfaces.SystemServiceInterface
	pools []*gzfs.ZPool
}

func (f bootstrapTestSystemService) GetUsablePools(_ context.Context) ([]*gzfs.ZPool, error) {
	return f.pools, nil
}

func newBootstrapTestService(t *testing.T, existingDatasets []string, pools ...string) (*Service, *jailCreateTestZFSRunner) {
	t.Helper()

	db := testutil.NewSQLiteTestDB(t, &jailModels.JailBootstrap{})

	usablePools := make([]*gzfs.ZPool, 0, len(pools))
	for _, p := range pools {
		usablePools = append(usablePools, &gzfs.ZPool{Name: p})
	}

	runner := newJailCreateTestZFSRunner(existingDatasets)

	svc := &Service{
		DB:     db,
		System: bootstrapTestSystemService{pools: usablePools},
		GZFS: gzfs.NewClient(gzfs.Options{
			Runner:   runner,
			ZFSBin:   "zfs",
			ZpoolBin: "zpool",
			ZDBBin:   "zdb",
		}),
		ctidHashByCTID: make(map[uint]string),
	}

	return svc, runner
}

func TestListBootstraps_ReturnsAllSupportedVersionsAndTypes(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	entries, err := svc.ListBootstraps(context.Background(), "tank")
	if err != nil {
		t.Fatalf("ListBootstraps returned unexpected error: %v", err)
	}

	want := len(jailServiceInterfaces.SupportedVersions) * len(jailServiceInterfaces.BootstrapTypes)
	if len(entries) != want {
		t.Fatalf("expected %d entries, got %d", want, len(entries))
	}
}

func TestListBootstraps_ExistsIsFalseWhenDatasetAbsent(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	entries, err := svc.ListBootstraps(context.Background(), "tank")
	if err != nil {
		t.Fatalf("ListBootstraps returned unexpected error: %v", err)
	}

	for _, e := range entries {
		if e.Exists {
			t.Errorf("expected Exists=false for %s, got true", e.Name)
		}
	}
}

func TestListBootstraps_ExistsIsTrueWhenDatasetPresent(t *testing.T) {
	existing := []string{"tank/sylve/bootstraps/15-0-Base"}
	svc, _ := newBootstrapTestService(t, existing, "tank")

	entries, err := svc.ListBootstraps(context.Background(), "tank")
	if err != nil {
		t.Fatalf("ListBootstraps returned unexpected error: %v", err)
	}

	found := false
	for _, e := range entries {
		if e.Name == "15-0-Base" {
			if !e.Exists {
				t.Errorf("expected 15-0-Base to have Exists=true, got false")
			}
			found = true
		}
	}

	if !found {
		t.Fatal("15-0-Base entry not found in results")
	}
}

func TestListBootstraps_StatusIsCompletedWhenDatasetExistsButNoDBRecord(t *testing.T) {
	existing := []string{"tank/sylve/bootstraps/15-0-Base"}
	svc, _ := newBootstrapTestService(t, existing, "tank")

	entries, err := svc.ListBootstraps(context.Background(), "tank")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, e := range entries {
		if e.Name == "15-0-Base" {
			if e.Status != "completed" {
				t.Errorf("expected Status=completed for manually-created dataset, got %q", e.Status)
			}
		}
	}
}

func TestListBootstraps_DBStatusOverridesWhenRecordExists(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	record := jailModels.JailBootstrap{
		Pool:          "tank",
		Dataset:       "tank/sylve/bootstraps/15-0-Base",
		MountPoint:    "/tank/sylve/bootstraps/15-0-Base",
		Name:          "15-0-Base",
		Major:         15,
		Minor:         0,
		BootstrapType: "base",
		Status:        "running",
		Phase:         "installing",
	}
	if err := svc.DB.Create(&record).Error; err != nil {
		t.Fatalf("failed to seed record: %v", err)
	}

	entries, err := svc.ListBootstraps(context.Background(), "tank")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, e := range entries {
		if e.Name == "15-0-Base" {
			if e.Status != "running" {
				t.Errorf("expected Status=running from DB, got %q", e.Status)
			}
			if e.Phase != "installing" {
				t.Errorf("expected Phase=installing from DB, got %q", e.Phase)
			}
		}
	}
}

func TestListBootstraps_CorrectsDatasetAndMountPointPaths(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	entries, err := svc.ListBootstraps(context.Background(), "tank")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, e := range entries {
		wantDataset := "tank/sylve/bootstraps/" + e.Name
		wantMount := "/tank/sylve/bootstraps/" + e.Name
		if e.Dataset != wantDataset {
			t.Errorf("entry %s: expected Dataset %q, got %q", e.Name, wantDataset, e.Dataset)
		}
		if e.MountPoint != wantMount {
			t.Errorf("entry %s: expected MountPoint %q, got %q", e.Name, wantMount, e.MountPoint)
		}
	}
}

func TestCreateBootstrap_FailsForUnknownPool(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	req := jailServiceInterfaces.BootstrapRequest{Pool: "nonexistent", Major: 15, Minor: 0, Type: "base"}
	err := svc.CreateBootstrap(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "pool_not_found") {
		t.Fatalf("expected pool_not_found, got %v", err)
	}
}

func TestCreateBootstrap_FailsForUnsupportedType(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	req := jailServiceInterfaces.BootstrapRequest{Pool: "tank", Major: 15, Minor: 0, Type: "nonexistent"}
	err := svc.CreateBootstrap(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "unsupported_bootstrap_type") {
		t.Fatalf("expected unsupported_bootstrap_type, got %v", err)
	}
}

func TestCreateBootstrap_FailsForUnsupportedVersion(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	req := jailServiceInterfaces.BootstrapRequest{Pool: "tank", Major: 99, Minor: 0, Type: "base"}
	err := svc.CreateBootstrap(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "unsupported_bootstrap_version") {
		t.Fatalf("expected unsupported_bootstrap_version, got %v", err)
	}
}

func TestCreateBootstrap_IdempotentWhenAlreadyCompleted(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	if err := svc.DB.Create(&jailModels.JailBootstrap{
		Pool:          "tank",
		Dataset:       "tank/sylve/bootstraps/15-0-Base",
		MountPoint:    "/tank/sylve/bootstraps/15-0-Base",
		Name:          "15-0-Base",
		Major:         15,
		Minor:         0,
		BootstrapType: "base",
		Status:        "completed",
	}).Error; err != nil {
		t.Fatalf("failed to seed completed record: %v", err)
	}

	err := svc.CreateBootstrap(context.Background(), jailServiceInterfaces.BootstrapRequest{
		Pool: "tank", Major: 15, Minor: 0, Type: "base",
	})
	if err != nil {
		t.Fatalf("expected nil for already-completed bootstrap, got %v", err)
	}
}

func TestCreateBootstrap_RejectsWhenAlreadyInProgress(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	lockKey := "tank:15-0-Base"
	svc.bootstrapActiveMu.Store(lockKey, true)
	defer svc.bootstrapActiveMu.Delete(lockKey)

	req := jailServiceInterfaces.BootstrapRequest{Pool: "tank", Major: 15, Minor: 0, Type: "base"}
	err := svc.CreateBootstrap(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "bootstrap_already_in_progress") {
		t.Fatalf("expected bootstrap_already_in_progress, got %v", err)
	}
}

func TestCreateBootstrap_ResetsFailedRecordOnRetry(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	record := jailModels.JailBootstrap{
		Pool:          "tank",
		Dataset:       "tank/sylve/bootstraps/15-0-Base",
		MountPoint:    "/tank/sylve/bootstraps/15-0-Base",
		Name:          "15-0-Base",
		Major:         15,
		Minor:         0,
		BootstrapType: "base",
		Status:        "failed",
		Error:         "some_previous_error",
	}
	if err := svc.DB.Create(&record).Error; err != nil {
		t.Fatalf("failed to seed failed record: %v", err)
	}

	err := svc.CreateBootstrap(context.Background(), jailServiceInterfaces.BootstrapRequest{
		Pool: "tank", Major: 15, Minor: 0, Type: "base",
	})
	if err != nil && strings.Contains(err.Error(), "bootstrap_already_in_progress") {
		t.Fatalf("retry of failed bootstrap must not return already_in_progress, got %v", err)
	}
}

func TestRecoverInterruptedBootstraps_MarksRunningRecordsAsFailed(t *testing.T) {
	svc, runner := newBootstrapTestService(t, nil, "tank")

	dataset := "tank/sylve/bootstraps/15-0-Base"
	runner.datasets[dataset] = jailCreateTestZFSDataset{
		guid:       "999",
		mountpoint: "/" + dataset,
	}

	record := jailModels.JailBootstrap{
		Pool:          "tank",
		Dataset:       dataset,
		MountPoint:    "/tank/sylve/bootstraps/15-0-Base",
		Name:          "15-0-Base",
		Major:         15,
		Minor:         0,
		BootstrapType: "base",
		Status:        "running",
		Phase:         "installing",
	}
	if err := svc.DB.Create(&record).Error; err != nil {
		t.Fatalf("failed to seed running record: %v", err)
	}

	svc.RecoverInterruptedBootstraps(context.Background())

	var updated jailModels.JailBootstrap
	if err := svc.DB.First(&updated, record.ID).Error; err != nil {
		t.Fatalf("failed to fetch updated record: %v", err)
	}

	if updated.Status != "failed" {
		t.Errorf("expected status=failed after recovery, got %q", updated.Status)
	}
	if updated.Error != "interrupted_by_server_restart" {
		t.Errorf("expected error=interrupted_by_server_restart, got %q", updated.Error)
	}
	if updated.Phase != "" {
		t.Errorf("expected phase cleared after recovery, got %q", updated.Phase)
	}
}

func TestRecoverInterruptedBootstraps_MarksMultipleStaleRecords(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	stale := []jailModels.JailBootstrap{
		{
			Pool: "tank", Dataset: "tank/sylve/bootstraps/15-0-Base",
			MountPoint: "/tank/sylve/bootstraps/15-0-Base",
			Name:       "15-0-Base", Major: 15, Minor: 0, BootstrapType: "base",
			Status: "running",
		},
		{
			Pool: "tank", Dataset: "tank/sylve/bootstraps/15-0-Minimal",
			MountPoint: "/tank/sylve/bootstraps/15-0-Minimal",
			Name:       "15-0-Minimal", Major: 15, Minor: 0, BootstrapType: "minimal",
			Status: "pending",
		},
	}
	for i := range stale {
		if err := svc.DB.Create(&stale[i]).Error; err != nil {
			t.Fatalf("failed to seed stale record %d: %v", i, err)
		}
	}

	svc.RecoverInterruptedBootstraps(context.Background())

	var results []jailModels.JailBootstrap
	if err := svc.DB.Find(&results).Error; err != nil {
		t.Fatalf("failed to query records: %v", err)
	}

	for _, r := range results {
		if r.Status != "failed" {
			t.Errorf("record %s: expected status=failed, got %q", r.Name, r.Status)
		}
		if r.Error != "interrupted_by_server_restart" {
			t.Errorf("record %s: expected interrupt error, got %q", r.Name, r.Error)
		}
	}
}

func TestRecoverInterruptedBootstraps_DestroysPartialDataset(t *testing.T) {
	dataset := "tank/sylve/bootstraps/15-0-Base"
	svc, runner := newBootstrapTestService(t, []string{dataset}, "tank")

	record := jailModels.JailBootstrap{
		Pool: "tank", Dataset: dataset,
		MountPoint: "/tank/sylve/bootstraps/15-0-Base",
		Name:       "15-0-Base", Major: 15, Minor: 0, BootstrapType: "base",
		Status: "running",
	}
	if err := svc.DB.Create(&record).Error; err != nil {
		t.Fatalf("failed to seed record: %v", err)
	}

	svc.RecoverInterruptedBootstraps(context.Background())

	if runner.hasDataset(dataset) {
		t.Errorf("expected partial dataset %s to be destroyed after recovery, but it still exists", dataset)
	}
}

func TestRecoverInterruptedBootstraps_NoOpWhenNoStaleRecords(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	svc.RecoverInterruptedBootstraps(context.Background())

	var count int64
	svc.DB.Model(&jailModels.JailBootstrap{}).Count(&count)
	if count != 0 {
		t.Errorf("expected 0 records, got %d", count)
	}
}
