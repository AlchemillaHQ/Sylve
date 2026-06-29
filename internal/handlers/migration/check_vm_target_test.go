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
	"testing"

	"github.com/alchemillahq/sylve/internal/config"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

type checkVMTargetResponse struct {
	Data struct {
		MissingMedia      []string `json:"missingMedia"`
		VNCPortInUse      bool     `json:"vncPortInUse"`
		MissingSwitches   []string `json:"missingSwitches"`
		MissingFsDatasets []string `json:"missingFsDatasets"`
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

	reqBody := CheckVMTargetRequest{
		RID:        999,
		MediaUUIDs: []string{"present-uuid", "missing-uuid", "present-uuid"},
		VNCPort:    5900,
		Switches: []CheckVMTargetSwitch{
			{Name: "WAN", Type: "manual", Bridge: "bridge0"},
			{Name: "LAN", Type: "manual", Bridge: "bridge9"},
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
	if !resp.Data.VNCPortInUse {
		t.Fatalf("expected vncPortInUse true (port 5900 already used by another VM)")
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
