// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
)

func TestPrepareJailNetworkMetadataWritesVersionedAttachment(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &networkModels.StandardSwitch{})
	filtered := networkModels.StandardSwitch{
		Name: "tenant", BridgeName: "vm-tenant", VLANFiltering: true,
	}
	if err := db.Create(&filtered).Error; err != nil {
		t.Fatalf("create filtered switch: %v", err)
	}

	accessVLAN := 10
	jail := jailModels.Jail{Networks: []jailModels.Network{{
		Name: "vnet0", SwitchID: filtered.ID, SwitchType: "standard",
		VLANPolicy: bridgevlan.PortPolicy{
			Mode: bridgevlan.ModeAccess, UntaggedVLAN: &accessVLAN,
		},
		StandardSwitch: &filtered,
	}}}

	originalValidate := jailValidateFilteredBridge
	validateCalls := 0
	jailValidateFilteredBridge = func(bridge string, expected *int) (bridgevlan.BridgeState, error) {
		validateCalls++
		return bridgevlan.BridgeState{}, fmt.Errorf(
			"managed bridge %s should not be inspected while writing metadata", bridge,
		)
	}
	t.Cleanup(func() { jailValidateFilteredBridge = originalValidate })

	if err := (&Service{DB: db}).prepareJailNetworkMetadata(&jail); err != nil {
		t.Fatalf("prepare jail network metadata: %v", err)
	}
	if validateCalls != 0 {
		t.Fatalf("metadata inspected managed bridge %d times", validateCalls)
	}
	got := jail.Networks[0]
	if got.Attachment == nil || got.Attachment.Version != networkAttachment.CurrentVersion ||
		got.Attachment.Kind != networkAttachment.KindJail || got.Attachment.SwitchName != filtered.Name ||
		got.Attachment.SwitchType != "standard" || !got.Attachment.VLANFiltering ||
		got.Attachment.VLANPolicy == nil || got.Attachment.VLANPolicy.UntaggedVLAN == nil ||
		*got.Attachment.VLANPolicy.UntaggedVLAN != accessVLAN {
		t.Fatalf("unexpected attachment metadata: %#v", got.Attachment)
	}
	if got.StandardSwitch != nil || got.ManualSwitch != nil {
		t.Fatalf("metadata retained full switch models: standard=%#v manual=%#v", got.StandardSwitch, got.ManualSwitch)
	}
}

func TestJailNetworkAttachmentFromMetadataHasNarrowLegacyFallback(t *testing.T) {
	legacy := jailModels.Network{
		SwitchType: "manual",
		ManualSwitch: &networkModels.ManualSwitch{
			Name: "legacy-lan", Bridge: "bridge0",
		},
	}
	contract, err := NetworkAttachmentFromMetadata(legacy)
	if err != nil {
		t.Fatalf("read legacy unfiltered metadata: %v", err)
	}
	if contract.SwitchName != "legacy-lan" || contract.SwitchType != "manual" || contract.VLANFiltering {
		t.Fatalf("unexpected legacy contract: %#v", contract)
	}

	accessVLAN := 10
	legacy.VLANPolicy = bridgevlan.PortPolicy{
		Mode: bridgevlan.ModeAccess, UntaggedVLAN: &accessVLAN,
	}
	if _, err := NetworkAttachmentFromMetadata(legacy); err == nil ||
		!strings.Contains(err.Error(), "legacy_filtered_jail_network_attachment_unsupported") {
		t.Fatalf("legacy metadata inferred a filtered attachment: %v", err)
	}

	filtered := networkAttachment.Contract{
		Version: networkAttachment.CurrentVersion,
		Kind:    networkAttachment.KindJail, SwitchName: "tenant", SwitchType: "manual",
		VLANFiltering: true,
		VLANPolicy: &bridgevlan.PortPolicy{
			Mode: bridgevlan.ModeAccess, UntaggedVLAN: &accessVLAN,
		},
	}
	legacy.Attachment = &filtered
	contract, err = NetworkAttachmentFromMetadata(legacy)
	if err != nil {
		t.Fatalf("read versioned filtered metadata: %v", err)
	}
	if contract.VLANPolicy == nil || contract.VLANPolicy.UntaggedVLAN == nil ||
		*contract.VLANPolicy.UntaggedVLAN != accessVLAN {
		t.Fatalf("versioned contract was not authoritative: %#v", contract)
	}
}

func TestFindJailNetworkSwitchPreservesDatabaseErrors(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
	)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get SQL database: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close SQL database: %v", err)
	}

	_, _, _, err = findJailNetworkSwitch(db, "LAN")
	if err == nil || !strings.Contains(err.Error(), "failed_to_load_switch") {
		t.Fatalf("database error = %v", err)
	}
	if errors.Is(err, errJailNetworkSwitchNotFound) {
		t.Fatalf("database error was misclassified as a missing switch: %v", err)
	}
}
