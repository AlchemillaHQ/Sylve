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
	"net"
	"strings"
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
}

func (s *Service) fetchCPUInfo(host string, port int, clusterToken, clusterKey string) (int, float64, bool) {
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

	cores := int(resp.Data.PhysicalCores)
	usage := resp.Data.Usage
	return cores, usage, true
}

func (s *Service) fetchRAMInfo(host string, port int, clusterToken, clusterKey string) (uint64, float64, bool) {
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

func (s *Service) fetchDiskInfo(host string, port int, clusterToken, clusterKey string) (uint64, float64, bool) {
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
		cpuCores, usage, okCPU := s.fetchCPUInfo(host, port, clusterToken, clusterKey)
		return hostname, true, cpuCores, usage, okCPU
	}
	canon, ok := s.fetchCanonicalHostnameWithToken(host, port, clusterToken, clusterKey)
	cpuCores, usage, okCPU := s.fetchCPUInfo(host, port, clusterToken, clusterKey)
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
	for _, server := range cfg.Servers {
		uuid := string(server.ID)
		addr := string(server.Address)

		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		api := fmt.Sprintf("%s:%d", host, config.ParsedConfig.Port)

		canon, okHealth, cores, cpuUsage, okCPU :=
			s.fetchCanonicalHostnameAndCPU(host, config.ParsedConfig.Port, clusterToken, clusterKey, selfHostname)

		memBytes, memUsedPct, okRAM := s.fetchRAMInfo(host, config.ParsedConfig.Port, clusterToken, clusterKey)
		diskBytes, diskUsedPct, okDisk := s.fetchDiskInfo(host, config.ParsedConfig.Port, clusterToken, clusterKey)

		ci := curInfo{
			nodeUUID:  uuid,
			api:       api,
			canonHost: canon,
			rawHost:   host,
			healthOK:  okHealth,
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

		current[uuid] = ci
	}

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
				}

				updates := map[string]any{
					"api":        cur.api,
					"status":     status,
					"updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
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
