// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"gorm.io/gorm"
)

type objectUsageColumn struct {
	name        string
	legacyOwner string
}

type objectUsageSource struct {
	table         string
	usageType     string
	nameColumn    string
	guestTable    string
	guestIDColumn string
	columns       []objectUsageColumn
}

type objectUsageKey struct {
	objectID   uint
	usageType  string
	consumerID uint
}

func (s *Service) populateObjectUsage(objects []networkModels.Object) error {
	if len(objects) == 0 {
		return nil
	}

	objectIDs := make([]uint, 0, len(objects))
	for _, object := range objects {
		objectIDs = append(objectIDs, object.ID)
	}

	sources := []objectUsageSource{
		{
			table: "firewall_traffic_rules", usageType: "firewall-traffic", nameColumn: "name",
			columns: []objectUsageColumn{
				{"source_obj_id", "firewall"},
				{"dest_obj_id", "firewall"},
				{"src_port_obj_id", "firewall"},
				{"dst_port_obj_id", "firewall"},
			},
		},
		{
			table: "firewall_nat_rules", usageType: "firewall-nat", nameColumn: "name",
			columns: []objectUsageColumn{
				{"source_obj_id", "firewall"},
				{"dest_obj_id", "firewall"},
				{"translate_to_obj_id", "firewall"},
				{"dnat_target_obj_id", "firewall"},
				{"dst_port_obj_id", "firewall"},
				{"redirect_port_obj_id", "firewall"},
			},
		},
		{
			table: "dhcp_static_leases", usageType: "dhcp", nameColumn: "hostname",
			columns: []objectUsageColumn{
				{"ip_object_id", ""},
				{"mac_object_id", "dhcp"},
				{"d_uid_object_id", "dhcp"},
			},
		},
		{
			table: "vm_networks", usageType: "vm", nameColumn: "name",
			guestTable: "vms", guestIDColumn: "vm_id",
			columns: []objectUsageColumn{{"mac_id", ""}},
		},
		{
			table: "jail_networks", usageType: "jail", nameColumn: "name",
			guestTable: "jails", guestIDColumn: "jid",
			columns: []objectUsageColumn{
				{"mac_id", ""},
				{"ipv4_id", ""},
				{"ipv4_gw_id", ""},
				{"ipv6_id", ""},
				{"ipv6_gw_id", ""},
			},
		},
		{
			table: "standard_switches", usageType: "switch", nameColumn: "name",
			columns: []objectUsageColumn{
				{"address_object_id", ""},
				{"address6_object_id", ""},
				{"network_object_id", ""},
				{"network6_object_id", ""},
				{"gateway_address_object_id", ""},
				{"gateway6_address_object_id", ""},
				{"bridge_mac_object_id", "switch"},
			},
		},
		{
			table: "static_routes", usageType: "route", nameColumn: "name",
			columns: []objectUsageColumn{
				{"destination_obj_id", "route"},
				{"gateway_obj_id", "route"},
			},
		},
	}

	usedBy := make(map[uint][]networkModels.ObjectUsage, len(objects))
	legacyOwners := make(map[uint]string, len(objects))
	seen := make(map[objectUsageKey]struct{}, len(objects))
	for _, source := range sources {
		if !s.DB.Migrator().HasTable(source.table) {
			continue
		}

		query := s.DB.Table(source.table)
		idExpression := source.table + ".id"
		nameExpression := "''"
		if source.guestTable != "" {
			if s.DB.Migrator().HasColumn(source.table, source.guestIDColumn) {
				guestID := source.table + "." + source.guestIDColumn
				idExpression = "COALESCE(NULLIF(" + guestID + ", 0), " + idExpression + ")"
				if s.DB.Migrator().HasTable(source.guestTable) {
					query = query.Joins("LEFT JOIN " + source.guestTable + " ON " + source.guestTable + ".id = " + guestID)
					if s.DB.Migrator().HasColumn(source.guestTable, source.nameColumn) {
						nameExpression = "COALESCE(" + source.guestTable + "." + source.nameColumn + ", '')"
					}
				}
			}
		} else if s.DB.Migrator().HasColumn(source.table, source.nameColumn) {
			nameExpression = "COALESCE(" + source.table + "." + source.nameColumn + ", '')"
		}

		for _, column := range source.columns {
			if !s.DB.Migrator().HasColumn(source.table, column.name) {
				continue
			}
			objectColumn := source.table + "." + column.name
			var references []struct {
				ObjectID uint
				ID       uint
				Name     string
			}
			if err := query.Session(&gorm.Session{}).
				Select(objectColumn+" AS object_id, "+idExpression+" AS id, "+nameExpression+" AS name").
				Where(objectColumn+" IN ?", objectIDs).
				Scan(&references).Error; err != nil {
				return fmt.Errorf("retrieve %s object usage: %w", source.usageType, err)
			}

			for _, reference := range references {
				id := reference.ObjectID
				usage := networkModels.ObjectUsage{Type: source.usageType, ID: reference.ID, Name: reference.Name}
				key := objectUsageKey{objectID: id, usageType: source.usageType, consumerID: reference.ID}
				if _, exists := seen[key]; !exists {
					usedBy[id] = append(usedBy[id], usage)
					seen[key] = struct{}{}
				}

				if column.legacyOwner == "dhcp" || legacyOwners[id] == "" {
					legacyOwners[id] = column.legacyOwner
				}
			}
		}
	}

	for i := range objects {
		usage := usedBy[objects[i].ID]
		if usage == nil {
			usage = []networkModels.ObjectUsage{}
		}
		slices.SortFunc(usage, func(a, b networkModels.ObjectUsage) int {
			if result := strings.Compare(a.Type, b.Type); result != 0 {
				return result
			}
			if result := strings.Compare(a.Name, b.Name); result != 0 {
				return result
			}
			return cmp.Compare(a.ID, b.ID)
		})
		objects[i].IsUsed = len(usage) > 0
		objects[i].IsUsedBy = legacyOwners[objects[i].ID]
		objects[i].UsedBy = usage
	}

	return nil
}
