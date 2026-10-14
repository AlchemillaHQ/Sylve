// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"slices"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestStandardSwitchAddressSchemaKeepsSingleAddressConfigurations(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t,
		&networkModels.Object{}, &networkModels.ObjectEntry{},
		&networkModels.StandardSwitch{}, &networkModels.NetworkPort{},
	)
	object := networkModels.Object{Name: "legacy-v6", Type: "Network", Entries: []networkModels.ObjectEntry{{Value: "fd00::10/64"}}}
	if err := dbConn.Create(&object).Error; err != nil {
		t.Fatal(err)
	}
	legacy := networkModels.StandardSwitch{
		Name: "legacy-single", BridgeName: "vm-legacy", MTU: 1500,
		NetworkManual: "192.0.2.10/24", Network6ID: &object.ID,
		GatewayManual: "192.0.2.1", Gateway6Manual: "fe80::1",
	}
	if err := dbConn.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := dbConn.AutoMigrate(&networkModels.StandardSwitch{}, &networkModels.StandardSwitchAddress{}); err != nil {
			t.Fatalf("migrate address-list schema: %v", err)
		}
	}
	var stored networkModels.StandardSwitch
	if err := dbConn.Preload("Addresses").Preload("Network6Obj.Entries").First(&stored, legacy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if len(stored.Addresses) != 0 || stored.Network(4) != "192.0.2.10/24" || stored.Network(6) != "fd00::10/64" ||
		stored.Gateway(4) != "192.0.2.1" || stored.Gateway(6) != "fe80::1" ||
		!slices.Equal(stored.AddressSources(4), []networkModels.StandardSwitchAddressSource{{CIDR: "192.0.2.10/24"}}) ||
		!slices.Equal(stored.AddressSources(6), []networkModels.StandardSwitchAddressSource{{ObjectID: object.ID}}) {
		t.Fatalf("legacy configuration changed: %#v", stored)
	}

	if err := dbConn.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		t.Fatal(err)
	}
	referenced := networkModels.Object{Name: "selected-source", Type: "Network"}
	if err := dbConn.Create(&referenced).Error; err != nil {
		t.Fatal(err)
	}
	address := networkModels.StandardSwitchAddress{SwitchID: legacy.ID, Family: 6, ObjectID: &referenced.ID}
	if err := dbConn.Create(&address).Error; err != nil {
		t.Fatal(err)
	}
	if err := dbConn.Delete(&referenced).Error; err == nil {
		t.Fatal("database allowed deleting a referenced address object")
	}
	if err := dbConn.Delete(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	var remaining int64
	if err := dbConn.Model(&networkModels.StandardSwitchAddress{}).Count(&remaining).Error; err != nil || remaining != 0 {
		t.Fatalf("switch deletion retained %d address sources: %v", remaining, err)
	}
}
