// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"fmt"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"gorm.io/gorm"
)

type localGuestIdentityProvider interface {
	Kind() string
	ScanLocalIdentities(tx *gorm.DB) ([]GuestIdentityInventoryEntry, error)
}

type vmGuestIdentityProvider struct{}

func (vmGuestIdentityProvider) Kind() string {
	return clusterModels.ReplicationGuestTypeVM
}

func (vmGuestIdentityProvider) ScanLocalIdentities(tx *gorm.DB) ([]GuestIdentityInventoryEntry, error) {
	var rows []struct {
		ID   uint
		RID  uint `gorm:"column:rid"`
		Name string
	}
	if err := tx.Model(&vmModels.VM{}).
		Select("id", "rid", "name").
		Order("rid ASC, id ASC").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("scan_vm_guest_identity_inventory: %w", err)
	}
	entries := make([]GuestIdentityInventoryEntry, len(rows))
	for i, row := range rows {
		entries[i] = GuestIdentityInventoryEntry{
			GuestID:  row.RID,
			RecordID: row.ID,
			Name:     row.Name,
		}
	}
	return entries, nil
}

type jailGuestIdentityProvider struct{}

func (jailGuestIdentityProvider) Kind() string {
	return clusterModels.ReplicationGuestTypeJail
}

func (jailGuestIdentityProvider) ScanLocalIdentities(tx *gorm.DB) ([]GuestIdentityInventoryEntry, error) {
	var rows []struct {
		ID   uint
		CTID uint `gorm:"column:ct_id"`
		Name string
	}
	if err := tx.Model(&jailModels.Jail{}).
		Select("id", "ct_id", "name").
		Order("ct_id ASC, id ASC").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("scan_jail_guest_identity_inventory: %w", err)
	}
	entries := make([]GuestIdentityInventoryEntry, len(rows))
	for i, row := range rows {
		entries[i] = GuestIdentityInventoryEntry{
			GuestID:  row.CTID,
			RecordID: row.ID,
			Name:     row.Name,
		}
	}
	return entries, nil
}

func registeredLocalGuestIdentityProviders() []localGuestIdentityProvider {
	return []localGuestIdentityProvider{
		vmGuestIdentityProvider{},
		jailGuestIdentityProvider{},
	}
}
