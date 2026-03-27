// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func testCreateRequest(rid uint, vncPort int) libvirtServiceInterfaces.CreateVMRequest {
	return libvirtServiceInterfaces.CreateVMRequest{
		Name:                 fmt.Sprintf("vm-%d", rid),
		RID:                  &rid,
		Description:          "test vm",
		StorageType:          libvirtServiceInterfaces.StorageTypeNone,
		StorageEmulationType: libvirtServiceInterfaces.AHCIHDStorageEmulation,
		SwitchName:           "none",
		CPUSockets:           1,
		CPUCores:             1,
		CPUThreads:           1,
		RAM:                  1024 * 1024 * 512,
		VNCPort:              vncPort,
		VNCPassword:          "test-password",
		VNCResolution:        "640x480",
		StartOrder:           0,
		TimeOffset:           libvirtServiceInterfaces.TimeOffsetUTC,
	}
}

type fakeVMCreateSystemService struct {
	systemServiceInterfaces.SystemServiceInterface
	pools []*gzfs.ZPool
	err   error
}

func (f fakeVMCreateSystemService) GetUsablePools(_ context.Context) ([]*gzfs.ZPool, error) {
	if f.err != nil {
		return nil, f.err
	}

	return f.pools, nil
}

type vmCreatePrecheckZFSRunner struct {
	existing map[string]struct{}
}

func (r *vmCreatePrecheckZFSRunner) Run(_ context.Context, _ io.Reader, stdout, _ io.Writer, _ string, args ...string) error {
	datasetName := ""
	recursive := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "list", "-p", "-j":
			continue
		case "-r":
			recursive = true
			continue
		case "-o", "-t":
			i++
			continue
		default:
			if strings.HasPrefix(args[i], "-") {
				continue
			}
			datasetName = args[i]
		}
	}

	if stdout == nil {
		return nil
	}

	datasets := make(map[string]string)

	switch {
	case datasetName == "":
		for existing := range r.existing {
			datasets[existing] = existing
		}
	case recursive:
		prefix := datasetName + "/"
		for existing := range r.existing {
			if existing == datasetName || strings.HasPrefix(existing, prefix) {
				datasets[existing] = existing
			}
		}
	default:
		if _, ok := r.existing[datasetName]; ok {
			datasets[datasetName] = datasetName
		}
	}

	if len(datasets) > 0 {
		builder := strings.Builder{}
		builder.WriteString(`{"output_version":{"name":"zfs","vers_major":0,"vers_minor":0},"datasets":{`)
		i := 0
		for name := range datasets {
			if i > 0 {
				builder.WriteString(",")
			}
			builder.WriteString(fmt.Sprintf(
				`"%s":{"name":"%s","pool":"%s","properties":{"guid":{"value":"1"},"mountpoint":{"value":"/"},"used":{"value":"0"},"available":{"value":"0"},"referenced":{"value":"0"},"compressratio":{"value":"1.00x"}}}`,
				name,
				name,
				strings.SplitN(strings.TrimPrefix(name, "/"), "/", 2)[0],
			))
			i++
		}
		builder.WriteString("}}")

		_, err := io.WriteString(stdout, builder.String())
		return err
	}

	_, err := io.WriteString(stdout, `{"output_version":{"name":"zfs","vers_major":0,"vers_minor":0},"datasets":{}}`)
	return err
}

func newVMCreatePrecheckTestService(db *gorm.DB, pools []string, existingDatasets []string) *Service {
	poolList := make([]*gzfs.ZPool, 0, len(pools))
	for _, pool := range pools {
		poolList = append(poolList, &gzfs.ZPool{Name: pool})
	}

	existing := make(map[string]struct{}, len(existingDatasets))
	for _, dataset := range existingDatasets {
		existing[dataset] = struct{}{}
	}

	return &Service{
		DB:     db,
		System: fakeVMCreateSystemService{pools: poolList},
		GZFS: gzfs.NewClient(gzfs.Options{
			Runner: &vmCreatePrecheckZFSRunner{existing: existing},
		}),
	}
}

func TestValidateCreate_FailsWhenISONotResolvable(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(
		t,
		&vmModels.VM{},
		&vmModels.VMStorageDataset{},
		&utilitiesModels.Downloads{},
		&utilitiesModels.DownloadedFile{},
	)
	svc := newVMCreatePrecheckTestService(db, nil, nil)

	download := utilitiesModels.Downloads{
		UUID:     "iso-missing",
		Path:     "/unused/missing.iso",
		Name:     "missing.iso",
		Type:     utilitiesModels.DownloadTypePath,
		URL:      "https://example.invalid/missing.iso",
		Progress: 100,
		Size:     1024,
		UType:    utilitiesModels.DownloadUTypeOther,
		Status:   utilitiesModels.DownloadStatusDone,
	}
	if err := db.Create(&download).Error; err != nil {
		t.Fatalf("failed to seed download row: %v", err)
	}

	req := testCreateRequest(510, 59010)
	req.ISO = download.UUID

	err := svc.validateCreate(req, context.Background())
	if err == nil || !strings.Contains(err.Error(), "image_not_resolvable") {
		t.Fatalf("expected image_not_resolvable error, got %v", err)
	}
}

func TestValidateCreate_FailsWhenStaleRootDatasetExists(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.VMStorageDataset{})
	svc := newVMCreatePrecheckTestService(
		db,
		[]string{"tank"},
		[]string{"tank/sylve/virtual-machines/511"},
	)

	req := testCreateRequest(511, 59011)
	err := svc.validateCreate(req, context.Background())
	if err == nil || !strings.Contains(err.Error(), "vm_create_stale_artifacts_detected") {
		t.Fatalf("expected stale artifact error, got %v", err)
	}
}

func TestValidateCreate_FailsWhenStaleStorageDatasetRowsExist(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.VMStorageDataset{})
	svc := newVMCreatePrecheckTestService(db, nil, nil)

	stale := vmModels.VMStorageDataset{
		Pool: "tank",
		Name: "tank/sylve/virtual-machines/512/raw-1",
		GUID: "guid-512",
	}
	if err := db.Create(&stale).Error; err != nil {
		t.Fatalf("failed to seed stale storage dataset row: %v", err)
	}

	req := testCreateRequest(512, 59012)
	err := svc.validateCreate(req, context.Background())
	if err == nil || !strings.Contains(err.Error(), "vm_create_stale_artifacts_detected") {
		t.Fatalf("expected stale artifact error, got %v", err)
	}
}

func TestValidateCreate_FailsWhenStaleZFSDatasetsExistWithoutDBRows(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.VMStorageDataset{})
	svc := newVMCreatePrecheckTestService(
		db,
		[]string{"tank"},
		[]string{"tank/sylve/virtual-machines/513.raw-1"},
	)

	req := testCreateRequest(513, 59013)
	err := svc.validateCreate(req, context.Background())
	if err == nil || !strings.Contains(err.Error(), "vm_create_stale_artifacts_detected") {
		t.Fatalf("expected stale artifact error, got %v", err)
	}
}

func TestCleanupFailedVMCreate_RemovesAutoMACAndStaleDatasetRows(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(
		t,
		&vmModels.VM{},
		&vmModels.Network{},
		&vmModels.Storage{},
		&vmModels.VMStats{},
		&vmModels.VMCPUPinning{},
		&vmModels.VMStorageDataset{},
		&jailModels.Network{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
		&networkModels.DHCPRange{},
		&networkModels.DHCPStaticLease{},
	)
	svc := &Service{DB: db}

	const rid uint = 900

	autoMAC := networkModels.Object{Name: "auto-mac-900", Type: "Mac"}
	if err := db.Create(&autoMAC).Error; err != nil {
		t.Fatalf("failed to seed auto MAC object: %v", err)
	}
	if err := db.Create(&networkModels.ObjectEntry{ObjectID: autoMAC.ID, Value: "02:00:00:00:09:00"}).Error; err != nil {
		t.Fatalf("failed to seed auto MAC entry: %v", err)
	}
	if err := db.Create(&networkModels.ObjectResolution{ObjectID: autoMAC.ID, ResolvedIP: "192.0.2.90"}).Error; err != nil {
		t.Fatalf("failed to seed auto MAC resolution: %v", err)
	}

	userMAC := networkModels.Object{Name: "user-mac-900", Type: "Mac"}
	if err := db.Create(&userMAC).Error; err != nil {
		t.Fatalf("failed to seed user MAC object: %v", err)
	}
	if err := db.Create(&networkModels.ObjectEntry{ObjectID: userMAC.ID, Value: "02:00:00:00:09:01"}).Error; err != nil {
		t.Fatalf("failed to seed user MAC entry: %v", err)
	}

	staleDataset := vmModels.VMStorageDataset{
		Pool: "tank",
		Name: "tank/sylve/virtual-machines/900/raw-1",
		GUID: "guid-stale-900",
	}
	if err := db.Create(&staleDataset).Error; err != nil {
		t.Fatalf("failed to seed stale vm_storage_datasets row: %v", err)
	}

	svc.cleanupFailedVMCreate(rid, []uint{autoMAC.ID})

	var autoObjectCount int64
	if err := db.Model(&networkModels.Object{}).Where("id = ?", autoMAC.ID).Count(&autoObjectCount).Error; err != nil {
		t.Fatalf("failed to count auto MAC object rows: %v", err)
	}
	if autoObjectCount != 0 {
		t.Fatalf("expected auto-created MAC object to be deleted, found %d rows", autoObjectCount)
	}

	var autoEntryCount int64
	if err := db.Model(&networkModels.ObjectEntry{}).Where("object_id = ?", autoMAC.ID).Count(&autoEntryCount).Error; err != nil {
		t.Fatalf("failed to count auto MAC entry rows: %v", err)
	}
	if autoEntryCount != 0 {
		t.Fatalf("expected auto-created MAC entries to be deleted, found %d rows", autoEntryCount)
	}

	var autoResolutionCount int64
	if err := db.Model(&networkModels.ObjectResolution{}).Where("object_id = ?", autoMAC.ID).Count(&autoResolutionCount).Error; err != nil {
		t.Fatalf("failed to count auto MAC resolution rows: %v", err)
	}
	if autoResolutionCount != 0 {
		t.Fatalf("expected auto-created MAC resolutions to be deleted, found %d rows", autoResolutionCount)
	}

	var userObjectCount int64
	if err := db.Model(&networkModels.Object{}).Where("id = ?", userMAC.ID).Count(&userObjectCount).Error; err != nil {
		t.Fatalf("failed to count user MAC object rows: %v", err)
	}
	if userObjectCount != 1 {
		t.Fatalf("expected user-selected MAC object to remain, found %d rows", userObjectCount)
	}

	var staleDatasetCount int64
	if err := db.Model(&vmModels.VMStorageDataset{}).Where("id = ?", staleDataset.ID).Count(&staleDatasetCount).Error; err != nil {
		t.Fatalf("failed to count stale vm_storage_datasets rows: %v", err)
	}
	if staleDatasetCount != 0 {
		t.Fatalf("expected stale vm_storage_datasets row to be deleted, found %d rows", staleDatasetCount)
	}
}
