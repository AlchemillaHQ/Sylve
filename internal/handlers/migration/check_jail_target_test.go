// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
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
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type checkJailTargetResponse struct {
	Data struct {
		MissingSwitches             []string `json:"missingSwitches"`
		IncompatibleSwitches        []string `json:"incompatibleSwitches"`
		NetworkCompatibilityChecked bool     `json:"networkCompatibilityChecked"`
	} `json:"data"`
}

func TestIntraClusterCheckJailTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testutil.NewSQLiteTestDB(t, &networkModels.StandardSwitch{})
	switchModel := networkModels.StandardSwitch{Name: "LAN", BridgeName: "bridge0"}
	if err := db.Create(&switchModel).Error; err != nil {
		t.Fatalf("seed switch: %v", err)
	}

	originalValidate := validateTargetNetworkAttachments
	var validated []networkAttachment.NamedContract
	validateTargetNetworkAttachments = func(
		_ *gorm.DB,
		networks []networkAttachment.NamedContract,
		expectedKind networkAttachment.Kind,
	) networkAttachment.TargetResult {
		validated = append([]networkAttachment.NamedContract(nil), networks...)
		if expectedKind != networkAttachment.KindJail {
			t.Fatalf("target validation kind=%q, want jail", expectedKind)
		}
		return networkAttachment.TargetResult{
			MissingSwitches:             []string{"vnet1"},
			IncompatibleSwitches:        []string{"vnet0: filtered_switch_runtime_mismatch"},
			NetworkCompatibilityChecked: true,
		}
	}
	t.Cleanup(func() { validateTargetNetworkAttachments = originalValidate })

	untagged := 10
	policy := bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: &untagged}
	body, err := json.Marshal(CheckJailTargetRequest{Networks: []networkAttachment.NamedContract{
		{
			Name: "vnet0",
			Attachment: networkAttachment.Contract{
				Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindJail,
				SwitchName: "LAN", SwitchType: "standard", VLANFiltering: true,
				VLANPolicy: &policy,
			},
		},
		{
			Name: "vnet1",
			Attachment: networkAttachment.Contract{
				Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindJail,
				SwitchName: "missing", SwitchType: "standard",
			},
		},
	}})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/intra-cluster/migration/check-jail-target", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	IntraClusterCheckJailTarget(&jail.Service{DB: db})(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var response checkJailTargetResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data.MissingSwitches) != 1 || response.Data.MissingSwitches[0] != "vnet1" {
		t.Fatalf("missing switches = %v", response.Data.MissingSwitches)
	}
	if len(response.Data.IncompatibleSwitches) != 1 ||
		!strings.Contains(response.Data.IncompatibleSwitches[0], "filtered_switch_runtime_mismatch") {
		t.Fatalf("incompatible switches = %v", response.Data.IncompatibleSwitches)
	}
	if !response.Data.NetworkCompatibilityChecked {
		t.Fatal("expected explicit network compatibility acknowledgement")
	}
	if len(validated) != 2 || validated[0].Attachment.SwitchName != switchModel.Name ||
		validated[0].Attachment.SwitchType != "standard" || validated[0].Attachment.VLANPolicy == nil ||
		validated[0].Attachment.VLANPolicy.UntaggedVLAN == nil ||
		*validated[0].Attachment.VLANPolicy.UntaggedVLAN != 10 {
		t.Fatalf("validated network = %+v", validated)
	}
}
