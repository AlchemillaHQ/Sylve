// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Service) getFileLeases() ([]networkServiceInterfaces.FileLeases, error) {
	const path = "/var/db/dnsmasq.leases"

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []networkServiceInterfaces.FileLeases{}, nil
		}
		return nil, err
	}
	defer f.Close()

	leases := make([]networkServiceInterfaces.FileLeases, 0, 16)

	// Cache "duid <MAC>" mapping lines so we can backfill IPv6 MACs.
	duidToMAC := make(map[string]string)

	sc := bufio.NewScanner(f)
	const maxLine = 64 * 1024
	sc.Buffer(make([]byte, 0, 4*1024), maxLine)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Fields(line)

		// Handle the special "duid <mac>" line dnsmasq writes.
		if len(parts) >= 2 && parts[0] == "duid" {
			duid := strings.ToLower(parts[1])
			duidToMAC[duid] = duid // sometimes this is already a MAC-like string
			// Some dnsmasq builds write "duid <DUID>" and elsewhere provide MAC.
			// If you keep a separate map DUID->MAC from another source, merge here.
			continue
		}

		// Normal lease lines need at least 4 fields.
		if len(parts) < 4 {
			continue
		}

		expiry, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil {
			// fmt.Printf("lease: bad expiry %q in line: %s\n", parts[0], line)
			continue
		}

		ipStr := parts[2]
		ip := net.ParseIP(ipStr)
		if ip == nil {
			// fmt.Printf("lease: bad IP %q in line: %s\n", ipStr, line)
			continue
		}

		var l networkServiceInterfaces.FileLeases
		l.Expiry = expiry
		l.IP = ipStr

		if parts[3] != "*" {
			l.Hostname = parts[3]
		}

		if ip.To4() != nil {
			// IPv4: expiry, MAC, IP, hostname, [clientid], [duid]
			l.MAC = parts[1]
			if len(parts) > 4 && parts[4] != "*" {
				l.ClientID = parts[4]
			}
			if len(parts) > 5 && parts[5] != "*" {
				l.DUID = strings.ToLower(parts[5])
			}
		} else {
			// IPv6: expiry, IAID, IP, hostname, [DUID], [MAC]
			l.IAID = parts[1]
			if len(parts) > 4 && parts[4] != "*" {
				l.DUID = strings.ToLower(parts[4])
			}
			// Some dnsmasq builds include MAC as a 6th field; many don't.
			if len(parts) > 5 && parts[5] != "*" {
				l.MAC = parts[5]
			}
			// Backfill MAC from the "duid ..." line if we have it and MAC missing.
			if l.MAC == "" && l.DUID != "" {
				if mac, ok := duidToMAC[l.DUID]; ok {
					l.MAC = mac
				}
			}
		}

		leases = append(leases, l)
	}

	if err := sc.Err(); err != nil {
		return nil, err
	}
	return leases, nil
}

func (s *Service) GetLeases() (networkServiceInterfaces.Leases, error) {
	fileLeases, err := s.getFileLeases()
	if err != nil {
		return networkServiceInterfaces.Leases{}, err
	}

	var dbLeases []networkModels.DHCPStaticLease
	if err := s.DB.
		Preload("DHCPRange.StandardSwitch").
		Preload("DHCPRange.ManualSwitch").
		Preload("IPObject.Entries").
		Preload("MACObject.Entries").
		Preload("DUIDObject.Entries").
		Find(&dbLeases).Error; err != nil {
		return networkServiceInterfaces.Leases{}, err
	}

	return networkServiceInterfaces.Leases{File: fileLeases, DB: dbLeases}, nil
}

type objWant string

const (
	wantMAC  objWant = "Mac"
	wantDUID objWant = "DUID"
	wantIPv4 objWant = "Host"
	wantIPv6 objWant = "Host"
)

func loadAndRequireType(db *gorm.DB, id *uint, want objWant) (uint, error) {
	if id == nil {
		return 0, nil
	}
	var o networkModels.Object
	if err := db.Select("id,type").First(&o, *id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, fmt.Errorf("object_not_found")
		}
		return 0, err
	}
	if o.Type != string(want) {
		return 0, fmt.Errorf("object_wrong_type: want=%s got=%s", want, o.Type)
	}
	return o.ID, nil
}

func mapDBErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "uniq_ip_per_range"):
		return fmt.Errorf("duplicate_ip_in_range")
	case strings.Contains(msg, "uniq_mac_per_range"):
		return fmt.Errorf("duplicate_mac_in_range")
	case strings.Contains(msg, "uniq_duid_per_range"):
		return fmt.Errorf("duplicate_duid_in_range")
	default:
		return err
	}
}

func (s *Service) CreateStaticMap(req *networkServiceInterfaces.CreateStaticMapRequest) error {
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if strings.TrimSpace(req.Hostname) == "" {
			return fmt.Errorf("hostname_required")
		}

		var dhcpRange networkModels.DHCPRange
		if err := tx.Select("id,type,start_ip,end_ip").First(&dhcpRange, "id = ?", req.DHCPRangeID).Error; err != nil {
			return fmt.Errorf("invalid_dhcp_range_id: %w", err)
		}

		// validate and resolve IDs (respecting family and range)
		var ipID, macID, duidID uint
		var err error

		if req.IPObjectID != nil && *req.IPObjectID != 0 {
			ipID, err = loadAndRequireType(tx, req.IPObjectID, wantIPv4 /* or wantIPv6 + family check */)
			if err != nil {
				return fmt.Errorf("invalid_ip_object: %w", err)
			}
		}

		if req.MACObjectID != nil && *req.MACObjectID != 0 {
			macID, err = loadAndRequireType(tx, req.MACObjectID, wantMAC)
			if err != nil {
				return fmt.Errorf("invalid_mac_object: %w", err)
			}
		}

		if req.DUIDObjectID != nil && *req.DUIDObjectID != 0 {
			duidID, err = loadAndRequireType(tx, req.DUIDObjectID, wantDUID)
			if err != nil {
				return fmt.Errorf("invalid_duid_object: %w", err)
			}
		}

		// required identifiers
		if macID == 0 && duidID == 0 {
			return fmt.Errorf("at_least_one_identifier_required")
		}
		if dhcpRange.Type == "ipv4" && macID == 0 {
			return fmt.Errorf("mac_required_for_ipv4")
		}
		if dhcpRange.Type == "ipv6" && duidID == 0 {
			return fmt.Errorf("duid_required_for_ipv6")
		}

		// (Optional) hostname uniqueness scoped to range:
		var hcount int64
		if err := tx.Model(&networkModels.DHCPStaticLease{}).
			Where("dhcp_range_id = ? AND hostname = ?", req.DHCPRangeID, req.Hostname).Count(&hcount).Error; err != nil {
			return fmt.Errorf("failed_to_check_hostname_uniqueness: %w", err)
		}
		if hcount > 0 {
			return fmt.Errorf("duplicate_hostname")
		}

		newMap := &networkModels.DHCPStaticLease{
			Hostname:     req.Hostname,
			IPObjectID:   utils.PtrIfNonZero(ipID),
			MACObjectID:  utils.PtrIfNonZero(macID),
			DUIDObjectID: utils.PtrIfNonZero(duidID),
			DHCPRangeID:  req.DHCPRangeID,
			Comments:     req.Comments,
		}

		if err := tx.Create(newMap).Error; err != nil {
			return mapDBErr(fmt.Errorf("failed_to_create_static_map: %w", err))
		}

		return nil
	})

	if err != nil {
		return err
	}

	if err := s.WriteDHCPConfig(); err != nil {
		return fmt.Errorf("failed_to_apply_created_static_map: %w", err)
	}

	return nil
}

func (s *Service) ModifyStaticMap(req *networkServiceInterfaces.ModifyStaticMapRequest) error {
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if strings.TrimSpace(req.Hostname) == "" {
			return fmt.Errorf("hostname_required")
		}

		var dhcpRange networkModels.DHCPRange
		if err := tx.Select("id,type,start_ip,end_ip").First(&dhcpRange, "id = ?", req.DHCPRangeID).Error; err != nil {
			return fmt.Errorf("invalid_dhcp_range_id: %w", err)
		}

		var current networkModels.DHCPStaticLease
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("IPObject.Entries").Preload("MACObject.Entries").Preload("DUIDObject.Entries").
			First(&current, "id = ?", req.ID).Error; err != nil {
			return fmt.Errorf("invalid_static_map_id: %w", err)
		}

		// hostname uniqueness (choose global or per-range)
		var count int64
		if err := tx.Model(&networkModels.DHCPStaticLease{}).
			Where("dhcp_range_id = ? AND hostname = ? AND id != ?", req.DHCPRangeID, req.Hostname, req.ID).
			Count(&count).Error; err != nil {
			return fmt.Errorf("failed_to_check_hostname_uniqueness: %w", err)
		}
		if count > 0 {
			return fmt.Errorf("duplicate_hostname")
		}

		// Apply tri-state mutations
		current.Hostname = req.Hostname
		current.DHCPRangeID = req.DHCPRangeID

		// IP
		if req.IPObjectID != nil {
			if *req.IPObjectID == 0 {
				current.IPObjectID = nil
			} else {
				id, err := loadAndRequireType(tx, req.IPObjectID, wantIPv4 /* or wantIPv6 with family check */)
				if err != nil {
					return fmt.Errorf("invalid_ip_object: %w", err)
				}
				// ensure in-range
				current.IPObjectID = utils.PtrIfNonZero(id)
			}
		}
		// MAC
		if req.MACObjectID != nil {
			if *req.MACObjectID == 0 {
				current.MACObjectID = nil
			} else {
				id, err := loadAndRequireType(tx, req.MACObjectID, wantMAC)
				if err != nil {
					return fmt.Errorf("invalid_mac_object: %w", err)
				}
				current.MACObjectID = utils.PtrIfNonZero(id)
			}
		}
		// DUID
		if req.DUIDObjectID != nil {
			if *req.DUIDObjectID == 0 {
				current.DUIDObjectID = nil
			} else {
				id, err := loadAndRequireType(tx, req.DUIDObjectID, wantDUID)
				if err != nil {
					return fmt.Errorf("invalid_duid_object: %w", err)
				}
				current.DUIDObjectID = utils.PtrIfNonZero(id)
			}
		}

		// Re-check identifier requirements after mutations
		if current.MACObjectID == nil && current.DUIDObjectID == nil {
			return fmt.Errorf("at_least_one_identifier_required")
		}
		if dhcpRange.Type == "ipv4" && current.MACObjectID == nil {
			return fmt.Errorf("mac_required_for_ipv4")
		}
		if dhcpRange.Type == "ipv6" && current.DUIDObjectID == nil {
			return fmt.Errorf("duid_required_for_ipv6")
		}

		// Friendly uniqueness prechecks (only when field is explicitly set & non-zero)
		var ipCount, macCount, duidCount int64
		if current.IPObjectID != nil {
			if err := tx.Model(&networkModels.DHCPStaticLease{}).
				Where("dhcp_range_id = ? AND id != ? AND ip_object_id = ?", current.DHCPRangeID, current.ID, *current.IPObjectID).
				Count(&ipCount).Error; err != nil {
				return fmt.Errorf("failed_to_check_ip_uniqueness: %w", err)
			}
			if ipCount > 0 {
				return fmt.Errorf("duplicate_ip_in_range")
			}
		}
		if current.MACObjectID != nil {
			if err := tx.Model(&networkModels.DHCPStaticLease{}).
				Where("dhcp_range_id = ? AND id != ? AND mac_object_id = ?", current.DHCPRangeID, current.ID, *current.MACObjectID).
				Count(&macCount).Error; err != nil {
				return fmt.Errorf("failed_to_check_mac_uniqueness: %w", err)
			}
			if macCount > 0 {
				return fmt.Errorf("duplicate_mac_in_range")
			}
		}

		if current.DUIDObjectID != nil {
			if err := tx.Model(&networkModels.DHCPStaticLease{}).
				Where("dhcp_range_id = ? AND id != ? AND d_uid_object_id = ?", current.DHCPRangeID, current.ID, *current.DUIDObjectID).
				Count(&duidCount).Error; err != nil {
				return fmt.Errorf("failed_to_check_duid_uniqueness: %w", err)
			}
			if duidCount > 0 {
				return fmt.Errorf("duplicate_duid_in_range")
			}
		}

		current.Comments = req.Comments

		if err := tx.Save(&current).Error; err != nil {
			return mapDBErr(fmt.Errorf("failed_to_modify_static_map: %w", err))
		}

		return nil
	})

	if err != nil {
		return err
	}

	if err := s.WriteDHCPConfig(); err != nil {
		return fmt.Errorf("failed_to_apply_modified_static_map: %w", err)
	}

	return nil
}

func (s *Service) DeleteStaticMap(id uint) error {
	var current networkModels.DHCPStaticLease
	if err := s.DB.
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&current, "id = ?", id).Error; err != nil {
		return fmt.Errorf("invalid_static_map_id: %w", err)
	}

	if err := s.DB.Delete(&networkModels.DHCPStaticLease{}, id).Error; err != nil {
		return fmt.Errorf("failed_to_delete_static_map: %w", err)
	}

	if err := s.WriteDHCPConfig(); err != nil {
		_ = s.DB.Create(&current).Error
		return fmt.Errorf("failed_to_apply_deleted_static_map: %w", err)
	}

	return nil
}
