// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"errors"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func TestGetSimpleJailByCTIDDoesNotDependOnDatabaseIDOrServiceToggle(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &jailModels.Jail{})
	guest := jailModels.Jail{CTID: 911, Name: "simple-by-ctid", Cores: 2, Memory: 1024}
	if err := db.Create(&guest).Error; err != nil {
		t.Fatalf("create jail: %v", err)
	}

	service := &Service{
		DB: db,
		liveStateByCTID: map[uint]jailServiceInterfaces.State{
			guest.CTID: {CTID: guest.CTID, State: "ACTIVE"},
		},
	}

	result, err := service.GetSimpleJailByCTID(guest.CTID)
	if err != nil {
		t.Fatalf("GetSimpleJailByCTID failed: %v", err)
	}
	if result.ID != guest.ID || result.CTID != guest.CTID || result.State != "ACTIVE" {
		t.Fatalf("simple jail = %+v", result)
	}

	_, err = service.GetSimpleJailByCTID(guest.ID + 1000)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing jail error = %v, want gorm.ErrRecordNotFound", err)
	}
}

func TestUpdateDescriptionUsesCTIDAndPreservesEmptyValue(t *testing.T) {
	requireSystemUUIDOrSkip(t)
	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Storage{},
		&jailModels.JailHooks{},
		&jailModels.JailSnapshot{},
		&jailModels.Network{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
	)
	guest := jailModels.Jail{CTID: 912, Name: "description-jail", Description: "old"}
	if err := db.Create(&guest).Error; err != nil {
		t.Fatalf("create jail: %v", err)
	}

	service := &Service{DB: db}
	if err := service.UpdateDescription(guest.CTID, ""); err != nil {
		t.Fatalf("UpdateDescription failed: %v", err)
	}

	var refreshed jailModels.Jail
	if err := db.First(&refreshed, guest.ID).Error; err != nil {
		t.Fatalf("reload jail: %v", err)
	}
	if refreshed.Description != "" {
		t.Fatalf("description = %q, want empty", refreshed.Description)
	}

	if err := service.UpdateDescription(guest.ID+1000, "missing"); err == nil || !strings.Contains(err.Error(), "jail_not_found") {
		t.Fatalf("missing CTID error = %v", err)
	}
	if err := service.UpdateDescription(guest.CTID, strings.Repeat("x", 1025)); err == nil || !strings.Contains(err.Error(), "invalid_description") {
		t.Fatalf("oversized description error = %v", err)
	}
}

func TestUpdateDescriptionReportsMetadataSyncFailure(t *testing.T) {
	requireSystemUUIDOrSkip(t)
	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Storage{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
	)
	guest := jailModels.Jail{CTID: 913, Name: "sync-failure-jail"}
	if err := db.Create(&guest).Error; err != nil {
		t.Fatalf("create jail: %v", err)
	}
	if err := db.Create(&jailModels.Storage{
		JailID: guest.ID,
		Pool:   "dev/null",
		GUID:   "sync-failure-guid",
		Name:   "root",
		IsBase: true,
	}).Error; err != nil {
		t.Fatalf("create jail storage: %v", err)
	}

	service := &Service{DB: db}
	err := service.UpdateDescription(guest.CTID, "persisted before sync")
	if err == nil || !strings.Contains(err.Error(), "failed_to_sync_jail_metadata") {
		t.Fatalf("metadata sync error = %v", err)
	}

	var refreshed jailModels.Jail
	if err := db.First(&refreshed, guest.ID).Error; err != nil {
		t.Fatalf("reload jail: %v", err)
	}
	if refreshed.Description != "persisted before sync" {
		t.Fatalf("description = %q", refreshed.Description)
	}
}
