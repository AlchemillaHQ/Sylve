// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package nfs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
	"github.com/alchemillahq/sylve/internal/sharerules"
)

type fakeDatasetCache []*gzfs.Dataset

func (cache fakeDatasetCache) CachedDatasets(context.Context, gzfs.DatasetType) ([]*gzfs.Dataset, error) {
	return cache, nil
}

func TestNewSharesRefuseProtectedDatasetsButExistingExportsStay(t *testing.T) {
	service := newTestService(t, testDataset("1", "tank/data", "/mnt/data"), testDataset("2", "tank/logs", "/var/log"))

	_, err := service.SaveShare(t.Context(), 0, shareInput("2", "192.0.2.1"))
	var detail *ValidationError
	if !errors.Is(err, ErrConflict) || !errors.As(err, &detail) || detail.Code != "dataset_not_shareable" || detail.Block != sharerules.BlockSystemPath {
		t.Fatalf("SaveShare on a system path returned %v; want dataset_not_shareable for system_path", err)
	}

	existing := nfsModels.NFSShare{NFSShareConfig: shareInput("2", "192.0.2.1")}
	if err := service.DB.Create(&existing).Error; err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewExports(t.Context())
	if err != nil || !strings.Contains(preview.Exports, "/var/log") {
		t.Fatalf("an existing export on a now-protected dataset was dropped: %q, %v", preview.Exports, err)
	}
}

func TestShareViewsReportLiveDatasetState(t *testing.T) {
	service := newTestService(t)
	unmounted := testDataset("4", "tank/offline", "/mnt/offline")
	unmounted.Properties["mounted"] = gzfs.ZFSProperty{Value: "no"}
	service.DatasetCache = fakeDatasetCache{
		testDataset("1", "tank/renamed", "/mnt/renamed"),
		testDataset("2", "tank/idle", "/mnt/idle"),
		unmounted,
		testDataset("5", "tank/logs", "/var/log"),
	}
	shares := []nfsModels.NFSShare{
		{NFSShareConfig: shareInput("1", "192.0.2.1"), DatasetName: "tank/old", Mountpoint: "/mnt/old"},
		{NFSShareConfig: nfsModels.NFSShareConfig{Dataset: "2", Clients: []string{"192.0.2.2"}}},
		{NFSShareConfig: shareInput("3", "192.0.2.3")},
		{NFSShareConfig: shareInput("4", "192.0.2.4")},
		{NFSShareConfig: shareInput("5", "192.0.2.5")},
	}
	if err := service.DB.Create(&shares).Error; err != nil {
		t.Fatal(err)
	}

	views, err := service.ShareViews(t.Context())
	if err != nil || len(views) != 5 {
		t.Fatalf("ShareViews = %d views, %v", len(views), err)
	}
	if views[0].State != sharerules.StateActive || views[0].DatasetName != "tank/renamed" || views[0].Mountpoint != "/mnt/renamed" {
		t.Fatalf("renamed dataset view = %+v; want live name and mountpoint", views[0])
	}
	for i, want := range []string{sharerules.StateActive, sharerules.StateDisabled, sharerules.StateDatasetMissing, sharerules.StateDatasetUnmounted, sharerules.StateActive} {
		if views[i].State != want {
			t.Errorf("share %d state = %q; want %q", i+1, views[i].State, want)
		}
	}
	if views[4].DatasetBlock != sharerules.BlockSystemPath || views[0].DatasetBlock != "" {
		t.Fatalf("blocks = %q, %q; want only the system path flagged", views[0].DatasetBlock, views[4].DatasetBlock)
	}
}

func TestStorageGuardWaitsBrieflyForTheTopologyLock(t *testing.T) {
	service := newTestService(t)
	original := storageGuardWait
	t.Cleanup(func() { storageGuardWait = original })

	service.topologyMu.Lock()
	go func() {
		time.Sleep(50 * time.Millisecond)
		service.topologyMu.Unlock()
	}()
	release, err := service.GuardStorageMutation(t.Context())
	if err != nil || release == nil {
		t.Fatalf("a storage change failed during a brief NFS check: %v", err)
	}
	release()

	storageGuardWait = 50 * time.Millisecond
	service.topologyMu.Lock()
	defer service.topologyMu.Unlock()
	release, err = service.GuardStorageMutation(t.Context())
	var detail *ValidationError
	if release != nil || !errors.As(err, &detail) || detail.Code != "storage_busy" {
		t.Fatalf("a long-held fence returned %v; want storage_busy", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if release, err = service.GuardStorageMutation(cancelled); release != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled request returned %v; want the cancellation", err)
	}
}

func TestValidationErrorsCarryCodes(t *testing.T) {
	_, err := normalizeShare(nfsModels.NFSShareConfig{Dataset: "1", Enabled: true})
	var detail *ValidationError
	if !errors.As(err, &detail) || detail.Field != "clients" || detail.Code != "clients_required" {
		t.Fatalf("a share without clients returned %v; want clients_required", err)
	}
	security := shareInput("1", "192.0.2.1")
	security.Security = []string{"sys", "krb5", "krb5i", "krb5p", "sys"}
	if _, err := normalizeShare(security); !errors.As(err, &detail) || detail.Code != "invalid_security" {
		t.Fatalf("five security types returned %v; want invalid_security", err)
	}
	_, err = normalizeConfig(nfsModels.NFSConfig{Protocol: "v4", V4MaxVersion: "4.2", UDP: true})
	if !errors.As(err, &detail) || detail.Field != "udp" || detail.Code != "udp_requires_nfsv3" {
		t.Fatalf("UDP with NFSv4 returned %v; want udp_requires_nfsv3", err)
	}
	encoded, err := json.Marshal(detail)
	if err != nil || !strings.Contains(string(encoded), `"code":"udp_requires_nfsv3"`) {
		t.Fatalf("the response detail does not carry the code: %s, %v", encoded, err)
	}
}

func TestApplyRefreshesRenamedShareLocations(t *testing.T) {
	service, _, share := configuredRuntime(t)
	service.listDatasets = func(context.Context) ([]*gzfs.Dataset, error) {
		return []*gzfs.Dataset{testDataset("1", "tank/renamed", "/mnt/renamed")}, nil
	}
	if err := service.Apply(t.Context()); err != nil {
		t.Fatal(err)
	}
	var saved nfsModels.NFSShare
	if err := service.DB.First(&saved, share.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.DatasetName != "tank/renamed" || saved.Mountpoint != "/mnt/renamed" {
		t.Fatalf("saved location = %s at %s; want the renamed dataset", saved.DatasetName, saved.Mountpoint)
	}
	if !saved.UpdatedAt.Equal(share.UpdatedAt) {
		t.Fatal("refreshing the location changed the share's update time")
	}
	var runtime nfsModels.NFSRuntime
	if err := service.DB.First(&runtime).Error; err != nil {
		t.Fatal(err)
	}
	runtime.Filesystems = nil
	if err := service.DB.Save(&runtime).Error; err != nil {
		t.Fatal(err)
	}
	if release, err := service.GuardStorageMutation(t.Context(), "tank/renamed"); !errors.Is(err, ErrConflict) || release != nil {
		t.Fatalf("the renamed dataset of an enabled share was not protected: %v", err)
	}
}

func TestClientAddressCodes(t *testing.T) {
	for value, want := range map[string]string{
		"0.0.0.0/0":   "client_matches_everyone",
		"::/0":        "client_matches_everyone",
		"10.0.0.1/33": "invalid_client",
		"abc/24":      "invalid_client",
		"ff02::/16":   "invalid_client",
		"example.com": "invalid_client",
	} {
		_, err := clientPrefix(value)
		var detail *ValidationError
		if !errors.As(err, &detail) || detail.Code != want {
			t.Errorf("%s returned %v; want %s", value, err, want)
		}
	}
}

func TestExportPlanNamesTheShareAndKeepsTheDatasetReason(t *testing.T) {
	for _, tc := range []struct {
		name, guid, saved, code, prefix string
	}{
		{"live name", "1", "tank/old", "dataset_not_mounted", "Share of tank/data: "},
		{"saved name", "9", "tank/old", "dataset_missing", "Share of tank/old: "},
		{"no name", "9", "", "dataset_missing", "Share 1: "},
	} {
		unmounted := testDataset("1", "tank/data", "/mnt/data")
		unmounted.Properties["mounted"] = gzfs.ZFSProperty{Value: "no"}
		service := newTestService(t, unmounted)
		share := nfsModels.NFSShare{NFSShareConfig: shareInput(tc.guid, "192.0.2.1"), DatasetName: tc.saved}
		if err := service.DB.Create(&share).Error; err != nil {
			t.Fatal(err)
		}
		_, err := service.PreviewExports(t.Context())
		var detail *ValidationError
		if !errors.As(err, &detail) || detail.Code != tc.code || !strings.HasPrefix(detail.Message, tc.prefix) {
			t.Errorf("%s: PreviewExports returned %v; want %s with prefix %q", tc.name, err, tc.code, tc.prefix)
		}
	}
}

func TestSavingAShareForAMissingDatasetSaysItIsMissing(t *testing.T) {
	service := newTestService(t, testDataset("1", "tank/data", "/mnt/data"))
	_, err := service.SaveShare(t.Context(), 0, shareInput("9", "192.0.2.1"))
	var detail *ValidationError
	if !errors.As(err, &detail) || detail.Code != "dataset_missing" {
		t.Fatalf("SaveShare for a missing dataset returned %v; want dataset_missing like the picker", err)
	}
}
