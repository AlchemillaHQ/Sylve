// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"testing"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

type networkServiceTestVM struct {
	ID  uint `gorm:"primaryKey"`
	RID uint `gorm:"column:rid"`
}

func (networkServiceTestVM) TableName() string { return "vms" }

type networkServiceTestJail struct {
	ID   uint `gorm:"primaryKey"`
	CTID uint `gorm:"column:ct_id"`
}

func (networkServiceTestJail) TableName() string { return "jails" }

func newNetworkServiceTestDB(t *testing.T, migrateModels ...any) *gorm.DB {
	models := append([]any{}, migrateModels...)
	models = append(models,
		&networkServiceTestVM{},
		&vmModels.Network{},
		&networkServiceTestJail{},
		&jailModels.Network{},
		&networkModels.StandardSwitch{},
		&networkModels.ObjectListSnapshot{},
		&infoModels.FirewallRuleDelta{},
		&infoModels.FirewallRuleCounterTotal{},
	)
	return testutil.NewSQLiteTestDB(t, models...)
}

func newNetworkServiceForTest(t *testing.T, migrateModels ...any) (*Service, *gorm.DB) {
	t.Helper()

	db := newNetworkServiceTestDB(t, migrateModels...)
	return &Service{
		DB:                db,
		TelemetryDB:       db,
		firewallTelemetry: newFirewallTelemetryRuntime(),
	}, db
}

func seedFilteredStandardSwitchInterface(t *testing.T) *Service {
	t.Helper()

	svc, db := newNetworkServiceForTest(t, &networkModels.StandardSwitch{})
	switches := []networkModels.StandardSwitch{
		{Name: "filtered", BridgeName: "vm-filtered", VLANFiltering: true},
		{Name: "ordinary", BridgeName: "vm-ordinary", VLANFiltering: false},
	}
	if err := db.Create(&switches).Error; err != nil {
		t.Fatalf("seed Standard Switches: %v", err)
	}
	return svc
}
