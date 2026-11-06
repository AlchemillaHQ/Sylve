// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migrationHandlers

import (
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	"gorm.io/gorm"
)

func TestCheckJailTarget(t *testing.T) {
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
			MissingSwitches:      []string{"vnet1"},
			IncompatibleSwitches: []string{"vnet0: filtered_switch_runtime_mismatch"},
		}
	}
	t.Cleanup(func() { validateTargetNetworkAttachments = originalValidate })

	untagged := 10
	policy := bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: &untagged}
	result, err := checkJailTarget(&jail.Service{DB: db})(t.Context(), "", CheckJailTargetRequest{
		Networks: []networkAttachment.NamedContract{
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
		},
	})
	if err != nil {
		t.Fatalf("check jail target: %v", err)
	}
	if len(result.MissingSwitches) != 1 || result.MissingSwitches[0] != "vnet1" {
		t.Fatalf("missing switches = %v", result.MissingSwitches)
	}
	if len(result.IncompatibleSwitches) != 1 ||
		!strings.Contains(result.IncompatibleSwitches[0], "filtered_switch_runtime_mismatch") {
		t.Fatalf("incompatible switches = %v", result.IncompatibleSwitches)
	}
	if len(validated) != 2 || validated[0].Attachment.SwitchName != switchModel.Name ||
		validated[0].Attachment.SwitchType != "standard" || validated[0].Attachment.VLANPolicy == nil ||
		validated[0].Attachment.VLANPolicy.UntaggedVLAN == nil ||
		*validated[0].Attachment.VLANPolicy.UntaggedVLAN != 10 {
		t.Fatalf("validated network = %+v", validated)
	}
}
