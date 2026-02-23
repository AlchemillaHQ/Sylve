// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	infoServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/info"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type basicHealthData struct {
	Hostname string `json:"hostname"`
}

type curInfo struct {
	nodeUUID  string
	api       string
	canonHost string
	rawHost   string
	healthOK  bool

	cpu      int
	cpuUsage float64

	memory   uint64
	memUsage float64

	disk      uint64
	diskUsage float64

	guestIDs []uint
}

/*
This is used to gauge whether we actually write about the cluster node to db or not,
to prevent unnecessary churn DB writes if nothing significant has changed.
*/
const usageThreshold = 5.0

func hasSignificantChange(cur curInfo, ex clusterModels.ClusterNode) bool {
	status := "offline"
	if cur.healthOK {
		status = "online"
	}

	if ex.Status != status {
		return true
	}

	if ex.API != cur.api {
		return true
	}

	hostname := cur.canonHost
	if hostname == "" {
		hostname = cur.rawHost
	}

	if ex.Hostname != hostname {
		return true
	}

	if len(cur.guestIDs) != len(ex.GuestIDs) {
		return true
	}

	if len(cur.guestIDs) > 0 {
		currentMap := make(map[uint]struct{}, len(cur.guestIDs))
		for _, id := range cur.guestIDs {
			currentMap[id] = struct{}{}
		}

		for _, id := range ex.GuestIDs {
			if _, exists := currentMap[id]; !exists {
				return true
			}
		}
	}

	if cur.cpu > 0 && ex.CPU != cur.cpu {
		return true
	}

	if cur.memory > 0 && ex.Memory != cur.memory {
		return true
	}

	if cur.disk > 0 && ex.Disk != cur.disk {
		return true
	}

	if math.Abs(ex.CPUUsage-cur.cpuUsage) >= usageThreshold {
		return true
	}

	if math.Abs(ex.MemoryUsage-cur.memUsage) >= usageThreshold {
		return true
	}

	if math.Abs(ex.DiskUsage-cur.diskUsage) >= usageThreshold {
		return true
	}

	return false
}

func (s *Service) fetchCPUInfo(host string, port int, clusterToken string) (int, float64, bool) {
	url := fmt.Sprintf("https://%s:%d/api/info/cpu", host, port)

	body, _, err := utils.HTTPGetJSONRead(
		url,
		map[string]string{
			"Accept":          "application/json",
			"Content-Type":    "application/json",
			"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
		},
	)

	if err != nil {
		return 0, 0, false
	}

	var resp internal.APIResponse[infoServiceInterfaces.CPUInfo]
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, 0, false
	}

	if resp.Status != "success" {
		return 0, 0, false
	}

	cores := int(resp.Data.LogicalCores)
	usage := resp.Data.Usage
	return cores, usage, true
}

func (s *Service) fetchRAMInfo(host string, port int, clusterToken string) (uint64, float64, bool) {
	url := fmt.Sprintf("https://%s:%d/api/info/ram", host, port)

	body, _, err := utils.HTTPGetJSONRead(
		url,
		map[string]string{
			"Accept":          "application/json",
			"Content-Type":    "application/json",
			"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
		},
	)
	if err != nil {
		return 0, 0, false
	}

	var resp internal.APIResponse[infoServiceInterfaces.RAMInfo]
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, 0, false
	}
	if resp.Status != "success" {
		return 0, 0, false
	}

	return resp.Data.Total, resp.Data.UsedPercent, true
}

type PoolDisksUsageResponse struct {
	Total float64 `json:"total"`
	Usage float64 `json:"usage"`
}

func (s *Service) fetchDiskInfo(host string, port int, clusterToken string) (uint64, float64, bool) {
	url := fmt.Sprintf("https://%s:%d/api/zfs/pools/disks-usage", host, port)

	body, _, err := utils.HTTPGetJSONRead(
		url,
		map[string]string{
			"Accept":          "application/json",
			"Content-Type":    "application/json",
			"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
		},
	)

	if err != nil {
		return 0, 0, false
	}

	var resp internal.APIResponse[PoolDisksUsageResponse]
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, 0, false
	}

	if resp.Status != "success" {
		return 0, 0, false
	}

	total := uint64(resp.Data.Total)
	used := uint64(resp.Data.Usage)
	pct := float64(0)
	if total > 0 {
		pct = (float64(used) / float64(total)) * 100.0
	}

	return uint64(resp.Data.Total), pct, true
}

func (s *Service) fetchCanonicalHostnameAndCPU(host string, port int, clusterToken, clusterKey, selfHostname string) (string, bool, int, float64, bool) {
	if utils.IsLocalIP(host) {
		hostname := selfHostname
		cpuCores, usage, okCPU := s.fetchCPUInfo(host, port, clusterToken)
		return hostname, true, cpuCores, usage, okCPU
	}
	canon, ok := s.fetchCanonicalHostnameWithToken(host, port, clusterToken, clusterKey)
	cpuCores, usage, okCPU := s.fetchCPUInfo(host, port, clusterToken)
	return canon, ok, cpuCores, usage, okCPU
}

func (s *Service) getClusterToken(hostname string) (string, error) {
	return s.AuthService.CreateClusterJWT(0, hostname, "", "")
}

func (s *Service) fetchCanonicalHostnameWithToken(host string, port int, clusterToken, clusterKey string) (string, bool) {
	url := fmt.Sprintf("https://%s:%d/api/health/basic", host, port)

	body, _, err := utils.HTTPPostJSONRead(
		url,
		map[string]any{"clusterKey": clusterKey},
		map[string]string{
			"Accept":          "application/json",
			"Content-Type":    "application/json",
			"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
		},
	)
	if err != nil {
		return "", false
	}

	var resp internal.APIResponse[basicHealthData]
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", false
	}
	if resp.Status == "success" && resp.Data.Hostname != "" {
		return resp.Data.Hostname, true
	}
	return "", false
}

func (s *Service) fetchResourceIds(host string, port int, clusterToken string) ([]uint, error) {
	var resourceIDs []uint

	endpoints := []string{"vm", "jail"}

	for _, endpoint := range endpoints {
		url := fmt.Sprintf("https://%s:%d/api/%s/simple", host, port, endpoint)

		body, _, err := utils.HTTPGetJSONRead(
			url,
			map[string]string{
				"Accept":          "application/json",
				"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
			},
		)
		if err != nil {
			continue
		}

		var resp struct {
			Status string `json:"status"`
			Data   []struct {
				RID  uint `json:"rid"`
				CTID uint `json:"ctId"`
			} `json:"data"`
		}

		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, err
		}

		for _, item := range resp.Data {
			if item.RID > 0 {
				resourceIDs = append(resourceIDs, item.RID)
			} else if item.CTID > 0 {
				resourceIDs = append(resourceIDs, item.CTID)
			}
		}
	}

	return resourceIDs, nil
}

func (s *Service) PopulateClusterNodes() error {
	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err != nil {
		return err
	}
	if !c.Enabled {
		return nil
	}
	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	selfHostname, err := utils.GetSystemHostname()
	if err != nil {
		return err
	}
	clusterToken, err := s.getClusterToken(selfHostname)
	if err != nil {
		return err
	}
	clusterDetails, err := s.GetClusterDetails()
	if err != nil {
		return err
	}
	clusterKey := clusterDetails.Cluster.Key

	fut := s.Raft.GetConfiguration()
	if err := fut.Error(); err != nil {
		return fmt.Errorf("failed_to_get_raft_configuration: %w", err)
	}
	cfg := fut.Configuration()

	current := make(map[string]curInfo, len(cfg.Servers))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, server := range cfg.Servers {
		wg.Add(1)
		go func(serverID, serverAddr string) {
			defer wg.Done()

			uuid := serverID
			addr := serverAddr

			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}
			api := fmt.Sprintf("%s:%d", host, config.ParsedConfig.Port)
			port := config.ParsedConfig.Port

			// Parallelize fetches within each node
			var nodeWg sync.WaitGroup
			var canon string
			var okHealth bool
			var cores int
			var cpuUsage float64
			var okCPU bool
			var memBytes uint64
			var memUsedPct float64
			var okRAM bool
			var diskBytes uint64
			var diskUsedPct float64
			var okDisk bool
			var guestIDs []uint

			nodeWg.Add(4)

			go func() {
				defer nodeWg.Done()
				canon, okHealth, cores, cpuUsage, okCPU =
					s.fetchCanonicalHostnameAndCPU(host, port, clusterToken, clusterKey, selfHostname)
			}()

			go func() {
				defer nodeWg.Done()
				memBytes, memUsedPct, okRAM = s.fetchRAMInfo(host, port, clusterToken)
			}()

			go func() {
				defer nodeWg.Done()
				diskBytes, diskUsedPct, okDisk = s.fetchDiskInfo(host, port, clusterToken)
			}()

			go func() {
				defer nodeWg.Done()
				ids, err := s.fetchResourceIds(host, port, clusterToken)
				if err == nil {
					guestIDs = ids
				}
			}()

			nodeWg.Wait()

			ci := curInfo{
				nodeUUID:  uuid,
				api:       api,
				canonHost: canon,
				rawHost:   host,
				healthOK:  okHealth,
				guestIDs:  guestIDs,
			}

			if okCPU {
				ci.cpu = cores
				ci.cpuUsage = cpuUsage
			}

			if okRAM {
				ci.memory = memBytes
				ci.memUsage = memUsedPct
			}

			if okDisk {
				ci.disk = diskBytes
				ci.diskUsage = diskUsedPct
			}

			mu.Lock()
			current[uuid] = ci
			mu.Unlock()
		}(string(server.ID), string(server.Address))
	}

	wg.Wait()

	writeOnce := func() error {
		return s.DB.Transaction(func(tx *gorm.DB) error {
			var existing []clusterModels.ClusterNode
			if err := tx.Find(&existing).Error; err != nil {
				return err
			}
			exByUUID := make(map[string]clusterModels.ClusterNode, len(existing))
			for _, n := range existing {
				exByUUID[n.NodeUUID] = n
			}

			for _, cur := range current {
				status := "offline"
				if cur.healthOK {
					status = "online"
				}

				if ex, exists := exByUUID[cur.nodeUUID]; exists {
					if !hasSignificantChange(cur, ex) {
						delete(exByUUID, cur.nodeUUID)
						continue
					}
				}

				insertRow := clusterModels.ClusterNode{
					NodeUUID: cur.nodeUUID,
					Hostname: func() string {
						if cur.canonHost != "" {
							return cur.canonHost
						}
						return cur.rawHost
					}(),
					API:         cur.api,
					Status:      status,
					CPU:         cur.cpu,
					CPUUsage:    cur.cpuUsage,
					Memory:      cur.memory,
					MemoryUsage: cur.memUsage,
					Disk:        cur.disk,
					DiskUsage:   cur.diskUsage,
					GuestIDs:    cur.guestIDs,
				}

				updates := map[string]any{
					"api":        cur.api,
					"status":     status,
					"updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
				}

				safeGuestIDs := cur.guestIDs
				if safeGuestIDs == nil {
					safeGuestIDs = make([]uint, 0)
				}

				if b, err := json.Marshal(safeGuestIDs); err == nil {
					updates["guest_ids"] = string(b)
				} else {
					updates["guest_ids"] = "[]"
				}

				if cur.canonHost != "" {
					updates["hostname"] = cur.canonHost
				}

				if cur.cpu > 0 {
					updates["cpu"] = cur.cpu
				}
				if cur.cpu > 0 || cur.cpuUsage > 0 {
					updates["cpu_usage"] = cur.cpuUsage
				}

				if cur.memory > 0 {
					updates["memory"] = cur.memory
				}
				if cur.memory > 0 || cur.memUsage > 0 {
					updates["memory_usage"] = cur.memUsage
				}

				if cur.disk > 0 {
					updates["disk"] = cur.disk
				}
				if cur.disk > 0 || cur.diskUsage > 0 {
					updates["disk_usage"] = cur.diskUsage
				}

				if err := tx.Clauses(clause.OnConflict{
					Columns:   []clause.Column{{Name: "node_uuid"}},
					DoUpdates: clause.Assignments(updates),
				}).Create(&insertRow).Error; err != nil {
					return err
				}

				delete(exByUUID, cur.nodeUUID)
			}

			if len(exByUUID) > 0 {
				ids := make([]string, 0, len(exByUUID))
				for uuid := range exByUUID {
					ids = append(ids, uuid)
				}

				if err := tx.
					Where("node_uuid IN ?", ids).
					Delete(&clusterModels.ClusterNode{}).Error; err != nil {
					return err
				}
			}

			return nil
		})
	}

	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := writeOnce(); err != nil {
			if strings.Contains(err.Error(), "database is locked") && attempt < maxRetries-1 {
				time.Sleep(time.Duration(100*(attempt+1)) * time.Millisecond)
				continue
			}
			return err
		}
		return nil
	}

	return nil
}
