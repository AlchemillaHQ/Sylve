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
	"strings"
	"sync"
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
	err   error
}

func (f bootstrapTestSystemService) GetUsablePools(_ context.Context) ([]*gzfs.ZPool, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.pools, nil
}

func newBootstrapTestService(t *testing.T, existingDatasets []string, pools ...string) (*Service, *jailCreateTestZFSRunner) {
	t.Helper()

	db := testutil.NewSQLiteTestDB(t, &jailModels.JailBootstrap{})

	usablePools := make([]*gzfs.ZPool, 0, len(pools))
	for _, p := range pools {
		usablePools = append(usablePools, &gzfs.ZPool{Name: p})
	}

	runner := newJailCreateTestZFSRunner(t, existingDatasets)

	svc := &Service{
		DB:     db,
		System: bootstrapTestSystemService{pools: usablePools},
		bootstrapHostReleaseFn: func() (string, error) {
			return "15.1-RELEASE", nil
		},
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

func TestListBootstraps_PropagatesPoolLookupFailure(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")
	svc.System = bootstrapTestSystemService{err: errors.New("database unavailable")}

	_, err := svc.ListBootstraps(context.Background(), "tank")
	if err == nil || !strings.Contains(err.Error(), "failed_to_get_usable_pools") {
		t.Fatalf("expected pool lookup failure, got %v", err)
	}
}

func TestListBootstraps_FiltersVersionsNewerThanHost(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")
	svc.bootstrapHostReleaseFn = func() (string, error) { return "15.0-RELEASE-p7", nil }

	entries, err := svc.ListBootstraps(context.Background(), "tank")
	if err != nil {
		t.Fatalf("ListBootstraps returned unexpected error: %v", err)
	}
	if len(entries) != len(jailServiceInterfaces.BootstrapTypes) {
		t.Fatalf("expected only 15.0 entries, got %#v", entries)
	}
	for _, entry := range entries {
		if entry.Major != 15 || entry.Minor != 0 {
			t.Fatalf("newer bootstrap was listed on 15.0 host: %#v", entry)
		}
	}
}

func TestListBootstraps_LabelsIncludeMinorVersion(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	entries, err := svc.ListBootstraps(context.Background(), "tank")
	if err != nil {
		t.Fatalf("ListBootstraps returned unexpected error: %v", err)
	}
	labels := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		labels[entry.Label] = struct{}{}
	}
	for _, expected := range []string{
		"FreeBSD 15.0 Base",
		"FreeBSD 15.0 Minimal",
		"FreeBSD 15.1 Base",
		"FreeBSD 15.1 Minimal",
	} {
		if _, ok := labels[expected]; !ok {
			t.Errorf("missing bootstrap label %q in %#v", expected, labels)
		}
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

func TestListBootstraps_ReportsOrphanWhenDatasetExistsButNoDBRecord(t *testing.T) {
	existing := []string{"tank/sylve/bootstraps/15-0-Base"}
	svc, _ := newBootstrapTestService(t, existing, "tank")

	entries, err := svc.ListBootstraps(context.Background(), "tank")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, e := range entries {
		if e.Name == "15-0-Base" {
			if e.Status != "orphaned" {
				t.Errorf("expected Status=orphaned for unmanaged dataset, got %q", e.Status)
			}
			if e.Error != "bootstrap_record_missing" {
				t.Errorf("expected missing-record diagnostic, got %q", e.Error)
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

func TestListBootstraps_DoesNotSynthesizeAbsentMountPointPaths(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	entries, err := svc.ListBootstraps(context.Background(), "tank")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, e := range entries {
		wantDataset := "tank/sylve/bootstraps/" + e.Name
		if e.Dataset != wantDataset {
			t.Errorf("entry %s: expected Dataset %q, got %q", e.Name, wantDataset, e.Dataset)
		}
		if e.MountPoint != "" {
			t.Errorf("entry %s: expected no mountpoint for absent dataset, got %q", e.Name, e.MountPoint)
		}
	}
}

func TestCreateBootstrap_RejectsVersionNewerThanHost(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")
	svc.bootstrapHostReleaseFn = func() (string, error) { return "15.0-RELEASE", nil }

	_, err := svc.CreateBootstrap(context.Background(), jailServiceInterfaces.BootstrapRequest{
		Pool: "tank", Major: 15, Minor: 1, Type: "base",
	})
	if err == nil || !strings.Contains(err.Error(), "bootstrap_version_newer_than_host") {
		t.Fatalf("expected host compatibility rejection, got %v", err)
	}
}

func TestParseBootstrapHostVersion(t *testing.T) {
	for _, test := range []struct {
		release string
		major   int
		minor   int
	}{
		{release: "15.0-RELEASE-p7", major: 15, minor: 0},
		{release: "15.1-RELEASE", major: 15, minor: 1},
		{release: "16.0-CURRENT", major: 16, minor: 0},
	} {
		version, err := parseBootstrapHostVersion(test.release)
		if err != nil {
			t.Fatalf("parse %q: %v", test.release, err)
		}
		if version.Major != test.major || version.Minor != test.minor {
			t.Fatalf("parse %q = %d.%d, want %d.%d", test.release, version.Major, version.Minor, test.major, test.minor)
		}
	}
}

func TestCreateBootstrap_FailsForUnknownPool(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	req := jailServiceInterfaces.BootstrapRequest{Pool: "nonexistent", Major: 15, Minor: 0, Type: "base"}
	_, err := svc.CreateBootstrap(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "pool_not_found") {
		t.Fatalf("expected pool_not_found, got %v", err)
	}
}

func TestCreateBootstrap_FailsForUnsupportedType(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	req := jailServiceInterfaces.BootstrapRequest{Pool: "tank", Major: 15, Minor: 0, Type: "nonexistent"}
	_, err := svc.CreateBootstrap(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "unsupported_bootstrap_type") {
		t.Fatalf("expected unsupported_bootstrap_type, got %v", err)
	}
}

func TestCreateBootstrap_FailsForUnsupportedVersion(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	req := jailServiceInterfaces.BootstrapRequest{Pool: "tank", Major: 99, Minor: 0, Type: "base"}
	_, err := svc.CreateBootstrap(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "unsupported_bootstrap_version") {
		t.Fatalf("expected unsupported_bootstrap_version, got %v", err)
	}
}

func TestCreateBootstrap_IdempotentWhenAlreadyCompleted(t *testing.T) {
	dataset := "tank/sylve/bootstraps/15-0-Base"
	svc, _ := newBootstrapTestService(t, []string{dataset}, "tank")

	if err := svc.DB.Create(&jailModels.JailBootstrap{
		Pool:          "tank",
		Dataset:       dataset,
		MountPoint:    "/tank/sylve/bootstraps/15-0-Base",
		Name:          "15-0-Base",
		Major:         15,
		Minor:         0,
		BootstrapType: "base",
		Status:        "completed",
	}).Error; err != nil {
		t.Fatalf("failed to seed completed record: %v", err)
	}

	result, err := svc.CreateBootstrap(context.Background(), jailServiceInterfaces.BootstrapRequest{
		Pool: "tank", Major: 15, Minor: 0, Type: "base",
	})
	if err != nil {
		t.Fatalf("expected nil for already-completed bootstrap, got %v", err)
	}
	if result.Outcome != "already_completed" || result.Status != "completed" {
		t.Fatalf("unexpected completed result: %+v", result)
	}
}

func TestCreateBootstrap_RejectsWhenAlreadyInProgress(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	lockKey := "tank:15-0-Base"
	svc.bootstrapActiveMu.Store(lockKey, true)
	defer svc.bootstrapActiveMu.Delete(lockKey)

	req := jailServiceInterfaces.BootstrapRequest{Pool: "tank", Major: 15, Minor: 0, Type: "base"}
	_, err := svc.CreateBootstrap(context.Background(), req)
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

	_, err := svc.CreateBootstrap(context.Background(), jailServiceInterfaces.BootstrapRequest{
		Pool: "tank", Major: 15, Minor: 0, Type: "base",
	})
	if err != nil && strings.Contains(err.Error(), "bootstrap_already_in_progress") {
		t.Fatalf("retry of failed bootstrap must not return already_in_progress, got %v", err)
	}
}

func TestListBootstraps_ReportsCompletedRecordWithMissingDatasetAsFailed(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")
	record := jailModels.JailBootstrap{
		Pool:          "tank",
		Dataset:       "tank/sylve/bootstraps/15-0-Base",
		MountPoint:    "/tank/sylve/bootstraps/15-0-Base",
		Name:          "15-0-Base",
		Major:         15,
		Minor:         0,
		BootstrapType: "base",
		Status:        "completed",
	}
	if err := svc.DB.Create(&record).Error; err != nil {
		t.Fatalf("failed to seed completed record: %v", err)
	}

	entries, err := svc.ListBootstraps(context.Background(), "tank")
	if err != nil {
		t.Fatalf("list bootstraps: %v", err)
	}
	for _, entry := range entries {
		if entry.Name == record.Name {
			if entry.Status != "failed" || entry.Error != "bootstrap_dataset_missing" {
				t.Fatalf("unexpected reconciled entry: %+v", entry)
			}
			return
		}
	}
	t.Fatalf("bootstrap %s not returned", record.Name)
}

func TestCreateBootstrap_RejectsUnmanagedCanonicalDataset(t *testing.T) {
	dataset := "tank/sylve/bootstraps/15-0-Base"
	svc, _ := newBootstrapTestService(t, []string{dataset}, "tank")

	_, err := svc.CreateBootstrap(context.Background(), jailServiceInterfaces.BootstrapRequest{
		Pool: "tank", Major: 15, Minor: 0, Type: "base",
	})
	if err == nil || !strings.Contains(err.Error(), "bootstrap_dataset_unmanaged") {
		t.Fatalf("expected unmanaged dataset conflict, got %v", err)
	}
}

func TestDeleteBootstrap_RejectsNonCanonicalTarget(t *testing.T) {
	dataset := "tank/sylve/bootstraps/15-0-Base"
	svc, runner := newBootstrapTestService(t, []string{dataset}, "tank")

	_, err := svc.DeleteBootstrap(context.Background(), "tank", "15-0-Base/child")
	if err == nil || !strings.Contains(err.Error(), "invalid_bootstrap_name") {
		t.Fatalf("expected invalid bootstrap name, got %v", err)
	}
	if !runner.hasDataset(dataset) {
		t.Fatalf("canonical dataset %s was changed by an invalid delete", dataset)
	}
}

func TestDeleteBootstrap_RemovesCanonicalOrphan(t *testing.T) {
	dataset := "tank/sylve/bootstraps/15-0-Base"
	svc, runner := newBootstrapTestService(t, []string{dataset}, "tank")

	result, err := svc.DeleteBootstrap(context.Background(), "tank", "15-0-Base")
	if err != nil {
		t.Fatalf("delete bootstrap: %v", err)
	}
	if result.Outcome != "deleted" || !result.DatasetDeleted || result.RecordDeleted {
		t.Fatalf("unexpected delete result: %+v", result)
	}
	if runner.hasDataset(dataset) {
		t.Fatalf("orphaned dataset %s still exists", dataset)
	}
}

func TestDeleteBootstrap_IsIdempotentForExactAbsentMember(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	result, err := svc.DeleteBootstrap(context.Background(), "tank", "15-0-Base")
	if err != nil {
		t.Fatalf("delete absent bootstrap: %v", err)
	}
	if result.Outcome != "already_absent" || result.DatasetDeleted || result.RecordDeleted {
		t.Fatalf("unexpected absent delete result: %+v", result)
	}
}

func TestDeleteBootstrap_SerializesConcurrentExactDeletes(t *testing.T) {
	dataset := "tank/sylve/bootstraps/15-0-Base"
	svc, _ := newBootstrapTestService(t, []string{dataset}, "tank")

	type deleteCall struct {
		result jailServiceInterfaces.BootstrapDeleteResult
		err    error
	}
	start := make(chan struct{})
	results := make(chan deleteCall, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	for range 2 {
		go func() {
			defer workers.Done()
			<-start
			result, err := svc.DeleteBootstrap(context.Background(), "tank", "15-0-Base")
			results <- deleteCall{result: result, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	outcomes := map[string]int{}
	for call := range results {
		if call.err != nil {
			t.Fatalf("concurrent delete failed: %v", call.err)
		}
		outcomes[call.result.Outcome]++
	}
	if outcomes["deleted"] != 1 || outcomes["already_absent"] != 1 {
		t.Fatalf("unexpected concurrent outcomes: %#v", outcomes)
	}
}

func TestDeleteBootstrap_RejectsActiveWorkerBeforeLookup(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")
	lockKey := "tank:15-0-Base"
	svc.bootstrapActiveMu.Store(lockKey, true)
	defer svc.bootstrapActiveMu.Delete(lockKey)

	_, err := svc.DeleteBootstrap(context.Background(), "tank", "15-0-Base")
	if err == nil || !strings.Contains(err.Error(), "bootstrap_already_in_progress") {
		t.Fatalf("expected active bootstrap conflict, got %v", err)
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
		MountPoint: runner.datasets[dataset].mountpoint,
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

func TestRecoverInterruptedBootstraps_DestroysPartialDatasetBeforeMountpointWasRecorded(t *testing.T) {
	dataset := "tank/sylve/bootstraps/15-0-Base"
	svc, runner := newBootstrapTestService(t, []string{dataset}, "tank")

	record := jailModels.JailBootstrap{
		Pool: "tank", Dataset: dataset,
		MountPoint: "",
		Name:       "15-0-Base", Major: 15, Minor: 0, BootstrapType: "base",
		Status: "running",
	}
	if err := svc.DB.Create(&record).Error; err != nil {
		t.Fatalf("failed to seed record: %v", err)
	}

	svc.RecoverInterruptedBootstraps(context.Background())

	if runner.hasDataset(dataset) {
		t.Errorf("expected unrecorded partial dataset %s to be destroyed after recovery", dataset)
	}
}

func TestRecoverInterruptedBootstraps_RefusesStoredDatasetOutsideCanonicalMember(t *testing.T) {
	unsafeDataset := "tank/important"
	svc, runner := newBootstrapTestService(t, []string{unsafeDataset}, "tank")
	record := jailModels.JailBootstrap{
		Pool:          "tank",
		Dataset:       unsafeDataset,
		MountPoint:    "/tank/important",
		Name:          "15-0-Base",
		Major:         15,
		Minor:         0,
		BootstrapType: "base",
		Status:        "running",
	}
	if err := svc.DB.Create(&record).Error; err != nil {
		t.Fatalf("failed to seed unsafe record: %v", err)
	}

	svc.RecoverInterruptedBootstraps(context.Background())

	if !runner.hasDataset(unsafeDataset) {
		t.Fatalf("recovery destroyed unsafe stored dataset %s", unsafeDataset)
	}
	var updated jailModels.JailBootstrap
	if err := svc.DB.First(&updated, record.ID).Error; err != nil {
		t.Fatalf("fetch recovered record: %v", err)
	}
	if updated.Status != "failed" || !strings.Contains(updated.Error, "invalid_bootstrap_record") {
		t.Fatalf("unexpected recovery state: %+v", updated)
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
