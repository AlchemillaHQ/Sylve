// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migrationHandlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/config"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func TestCheckVMTargetChecksHelperWithoutNetworks(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{})
	var request CheckVMTargetRequest
	if err := json.Unmarshal(
		[]byte(`{"bootRequirement":{"bootRom":"uefi_csmwrap","cpuSockets":1,"cpuCores":1,"cpuThreads":1}}`), &request,
	); err != nil {
		t.Fatal(err)
	}
	result, err := checkVMTarget(&libvirt.Service{DB: db})(t.Context(), "", request)
	if err != nil {
		t.Fatalf("target check: %v", err)
	}
	if len(result.BootErrors) != 1 {
		t.Fatalf("incompatible helper not rejected: %+v", result)
	}
}

func TestCheckVMTarget(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(t,
		&utilitiesModels.Downloads{},
		&utilitiesModels.DownloadedFile{},
		&networkModels.ManualSwitch{},
		&vmModels.VM{},
	)
	svc := &libvirt.Service{DB: db}
	originalValidate := validateTargetNetworkAttachments
	validateTargetNetworkAttachments = func(
		_ *gorm.DB,
		attachments []networkAttachment.NamedContract,
		expectedKind networkAttachment.Kind,
	) networkAttachment.TargetResult {
		if expectedKind != networkAttachment.KindVM || len(attachments) != 2 ||
			attachments[0].Attachment.Version != networkAttachment.CurrentVersion {
			t.Fatalf("unexpected target attachments: kind=%q attachments=%+v", expectedKind, attachments)
		}
		return networkAttachment.TargetResult{
			MissingSwitches:      []string{"LAN"},
			IncompatibleSwitches: []string{"WAN: vm_network_vlan_mode_mismatch"},
		}
	}
	t.Cleanup(func() { validateTargetNetworkAttachments = originalValidate })

	httpDir := config.GetDownloadsPath("http")
	if err := os.MkdirAll(httpDir, 0o755); err != nil {
		t.Fatalf("failed to create downloads dir: %v", err)
	}
	isoPath := filepath.Join(httpDir, "present.iso")
	if err := os.WriteFile(isoPath, []byte("iso"), 0o644); err != nil {
		t.Fatalf("failed to write iso file: %v", err)
	}
	if err := db.Create(&utilitiesModels.Downloads{
		UUID:     "present-uuid",
		Path:     isoPath,
		Name:     "present.iso",
		Type:     utilitiesModels.DownloadTypeHTTP,
		URL:      "https://example.invalid/present.iso",
		Progress: 100,
		Size:     3,
		Status:   utilitiesModels.DownloadStatusDone,
	}).Error; err != nil {
		t.Fatalf("failed to seed download: %v", err)
	}

	if err := db.Create(&networkModels.ManualSwitch{Name: "WAN", Bridge: "bridge0"}).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}

	if err := db.Create(&vmModels.VM{RID: 100, VNCEnabled: true, VNCPort: 5900}).Error; err != nil {
		t.Fatalf("failed to seed vm: %v", err)
	}

	expectedVLAN := 10
	reqBody := CheckVMTargetRequest{
		RID:        999,
		MediaUUIDs: []string{"present-uuid", "missing-uuid", "present-uuid"},
		VNCPort:    5900,
		Switches: []networkAttachment.NamedContract{
			{
				Name: "WAN",
				Attachment: networkAttachment.Contract{
					Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindVM,
					SwitchName: "WAN", SwitchType: "manual", VLANFiltering: true,
					DefaultAccessVLAN: &expectedVLAN,
				},
			},
			{
				Name: "LAN",
				Attachment: networkAttachment.Contract{
					Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindVM,
					SwitchName: "LAN", SwitchType: "manual",
				},
			},
		},
	}
	result, err := checkVMTarget(svc)(t.Context(), "", reqBody)
	if err != nil {
		t.Fatalf("target check: %v", err)
	}
	if len(result.MissingMedia) != 1 || result.MissingMedia[0] != "missing-uuid" {
		t.Fatalf("expected missingMedia [missing-uuid], got %v", result.MissingMedia)
	}
	if len(result.MissingSwitches) != 1 || result.MissingSwitches[0] != "LAN" {
		t.Fatalf("expected missingSwitches [LAN], got %v", result.MissingSwitches)
	}
	if len(result.IncompatibleSwitches) != 1 ||
		!strings.Contains(result.IncompatibleSwitches[0], "vm_network_vlan_mode_mismatch") {
		t.Fatalf("expected WAN VLAN incompatibility, got %v", result.IncompatibleSwitches)
	}
	if !result.VNCPortInUse {
		t.Fatalf("expected vncPortInUse true (port 5900 already used by another VM)")
	}
}

func TestCheckVMTargetWithoutLibvirt(t *testing.T) {
	_, err := checkVMTarget(nil)(t.Context(), "", CheckVMTargetRequest{MediaUUIDs: []string{"x"}})
	if err == nil || !strings.Contains(err.Error(), "libvirt_not_configured") {
		t.Fatalf("nil libvirt error = %v", err)
	}
}
