// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migrationHandlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type checkVMTargetResponse struct {
	Data struct {
		MissingMedia                []string `json:"missingMedia"`
		VNCPortInUse                bool     `json:"vncPortInUse"`
		MissingSwitches             []string `json:"missingSwitches"`
		IncompatibleSwitches        []string `json:"incompatibleSwitches"`
		MissingFsDatasets           []string `json:"missingFsDatasets"`
		NetworkCompatibilityChecked bool     `json:"networkCompatibilityChecked"`
	} `json:"data"`
}

func TestIntraClusterCheckVMTarget(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	gin.SetMode(gin.TestMode)

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
			MissingSwitches:             []string{"LAN"},
			IncompatibleSwitches:        []string{"WAN: vm_network_vlan_mode_mismatch"},
			NetworkCompatibilityChecked: true,
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
		Switches: []CheckVMTargetSwitch{
			{
				Name: "WAN",
				Attachment: &networkAttachment.Contract{
					Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindVM,
					SwitchName: "WAN", SwitchType: "manual", VLANFiltering: true,
					DefaultAccessVLAN: &expectedVLAN,
				},
			},
			{
				Name: "LAN",
				Attachment: &networkAttachment.Contract{
					Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindVM,
					SwitchName: "LAN", SwitchType: "manual",
				},
			},
		},
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("failed to marshal body: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/intra-cluster/migration/check-vm-target", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	IntraClusterCheckVMTarget(svc)(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp checkVMTargetResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v (%s)", err, w.Body.String())
	}

	if len(resp.Data.MissingMedia) != 1 || resp.Data.MissingMedia[0] != "missing-uuid" {
		t.Fatalf("expected missingMedia [missing-uuid], got %v", resp.Data.MissingMedia)
	}
	if len(resp.Data.MissingSwitches) != 1 || resp.Data.MissingSwitches[0] != "LAN" {
		t.Fatalf("expected missingSwitches [LAN], got %v", resp.Data.MissingSwitches)
	}
	if len(resp.Data.IncompatibleSwitches) != 1 ||
		!strings.Contains(resp.Data.IncompatibleSwitches[0], "vm_network_vlan_mode_mismatch") {
		t.Fatalf("expected WAN VLAN incompatibility, got %v", resp.Data.IncompatibleSwitches)
	}
	if !resp.Data.VNCPortInUse {
		t.Fatalf("expected vncPortInUse true (port 5900 already used by another VM)")
	}
	if !resp.Data.NetworkCompatibilityChecked {
		t.Fatal("expected explicit network compatibility acknowledgement")
	}
}

func TestValidateVMTargetSwitchesTranslatesLegacyFailuresForOldCallers(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
	)
	if err := db.Create(&networkModels.StandardSwitch{
		Name: "filtered", BridgeName: "bridge-filtered", VLANFiltering: true,
	}).Error; err != nil {
		t.Fatalf("seed filtered switch: %v", err)
	}

	originalValidate := validateTargetNetworkAttachments
	validateTargetNetworkAttachments = func(
		_ *gorm.DB,
		attachments []networkAttachment.NamedContract,
		expectedKind networkAttachment.Kind,
	) networkAttachment.TargetResult {
		result := networkAttachment.TargetResult{
			MissingSwitches:             []string{},
			IncompatibleSwitches:        []string{},
			NetworkCompatibilityChecked: true,
		}
		if expectedKind != networkAttachment.KindVM {
			t.Fatalf("unexpected attachment kind %q", expectedKind)
		}
		for _, attachment := range attachments {
			switch attachment.Attachment.SwitchName {
			case "filtered":
				result.IncompatibleSwitches = append(
					result.IncompatibleSwitches,
					"filtered: vm_network_vlan_mode_mismatch",
				)
			case "plain":
			default:
				t.Fatalf("unexpected attachment: %+v", attachment)
			}
		}
		return result
	}
	t.Cleanup(func() { validateTargetNetworkAttachments = originalValidate })

	result, unsafe := validateVMTargetSwitches(db, []CheckVMTargetSwitch{
		{Name: "absent", Type: "standard", Bridge: "bridge-absent"},
		{Name: "filtered", Type: "standard", Bridge: "bridge-filtered"},
	})
	if !unsafe {
		t.Fatal("legacy target failures must require a non-success response")
	}
	if len(result.IncompatibleSwitches) != 0 {
		t.Fatalf("legacy incompatibilities must not use a field old callers ignore: %v", result.IncompatibleSwitches)
	}
	if len(result.MissingSwitches) != 2 || result.MissingSwitches[0] != "absent" ||
		!strings.Contains(result.MissingSwitches[1], "vm_network_vlan_mode_mismatch") {
		t.Fatalf("legacy failures were not translated: %v", result.MissingSwitches)
	}
}

func TestValidateVMTargetSwitchesAcceptsLegacyBridgeFallback(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
	)
	if err := db.Create(&networkModels.StandardSwitch{
		Name: "target-name", BridgeName: "bridge0",
	}).Error; err != nil {
		t.Fatalf("seed standard switch: %v", err)
	}

	originalValidate := validateTargetNetworkAttachments
	validateTargetNetworkAttachments = func(
		_ *gorm.DB,
		attachments []networkAttachment.NamedContract,
		expectedKind networkAttachment.Kind,
	) networkAttachment.TargetResult {
		if expectedKind != networkAttachment.KindVM {
			t.Fatalf("unexpected attachment kind %q", expectedKind)
		}
		if len(attachments) == 0 {
			return networkAttachment.TargetResult{NetworkCompatibilityChecked: true}
		}
		if len(attachments) != 1 || attachments[0].Attachment.SwitchName != "target-name" ||
			attachments[0].Attachment.VLANFiltering {
			t.Fatalf("unexpected legacy attachment: %+v", attachments)
		}
		return networkAttachment.TargetResult{NetworkCompatibilityChecked: true}
	}
	t.Cleanup(func() { validateTargetNetworkAttachments = originalValidate })

	result, unsafe := validateVMTargetSwitches(db, []CheckVMTargetSwitch{
		{Name: "source-name", Type: "standard", Bridge: "bridge0"},
	})
	if unsafe || !result.NetworkCompatibilityChecked || len(result.MissingSwitches) != 0 ||
		len(result.IncompatibleSwitches) != 0 {
		t.Fatalf("valid legacy bridge fallback rejected: unsafe=%t result=%+v", unsafe, result)
	}
}

func TestValidateVMTargetSwitchesRejectsMalformedContractEntry(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
	)

	result, unsafe := validateVMTargetSwitches(db, []CheckVMTargetSwitch{{Name: "LAN"}})
	if !unsafe || len(result.MissingSwitches) != 1 || result.MissingSwitches[0] != "LAN" {
		t.Fatalf("malformed contract entry was not rejected: unsafe=%t result=%+v", unsafe, result)
	}
}

func TestIntraClusterCheckVMTargetRejectsUnsafeLegacyProbe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testutil.NewSQLiteTestDB(t,
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
	)
	svc := &libvirt.Service{DB: db}

	body := []byte(`{"switches":[{"name":"missing","type":"standard","bridge":"bridge-missing"}]}`)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/intra-cluster/migration/check-vm-target",
		bytes.NewReader(body),
	)
	c.Request.Header.Set("Content-Type", "application/json")

	IntraClusterCheckVMTarget(svc)(c)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected legacy probe failure status 409, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "legacy_vm_network_target_incompatible") {
		t.Fatalf("unexpected legacy probe response: %s", w.Body.String())
	}
}

func TestIntraClusterCheckVMTarget_NilService(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body, _ := json.Marshal(CheckVMTargetRequest{MediaUUIDs: []string{"x"}})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/intra-cluster/migration/check-vm-target", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	IntraClusterCheckVMTarget(nil)(c)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 for nil libvirt service, got %d", w.Code)
	}
}
