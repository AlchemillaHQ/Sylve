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
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	infoServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/info"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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

	if cur.healthOK {
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
	}

	return false
}

func (s *Service) getClusterToken(hostname string) (string, error) {
	return s.AuthService.CreateClusterJWT(0, hostname, "", "")
}

func (s *Service) GetNodeInfo(host string, port int, clusterToken string) (infoServiceInterfaces.NodeInfo, error) {
	var nodeInfo infoServiceInterfaces.NodeInfo

	url := fmt.Sprintf("https://%s:%d/api/info/node", host, port)
	body, _, err := utils.HTTPGetJSONRead(
		url,
		map[string]string{
			"Accept":          "application/json",
			"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
		},
	)

	if err != nil {
		return nodeInfo, err
	}

	var resp internal.APIResponse[infoServiceInterfaces.NodeInfo]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nodeInfo, err
	}

	if resp.Status != "success" {
		return nodeInfo, fmt.Errorf("failed_to_fetch_node_info")
	}

	return resp.Data, nil
}

func (s *Service) PopulateClusterNodes() error {
	if s.Raft == nil {
		return nil
	}

	state := s.Raft.State()
	if state != raft.Leader {
		return nil
	}

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

			ci := curInfo{
				nodeUUID: uuid,
				api:      api,
				rawHost:  host,
				healthOK: false,
			}

			nodeInfo, err := s.GetNodeInfo(host, port, clusterToken)
			if err == nil {
				ci.healthOK = true
				ci.canonHost = nodeInfo.Hostname
				ci.cpu = int(nodeInfo.LogicalCores)
				ci.cpuUsage = nodeInfo.CPUUsage
				ci.memory = nodeInfo.RAMTotal
				ci.memUsage = nodeInfo.RAMUsage
				ci.disk = nodeInfo.DiskTotal
				ci.diskUsage = nodeInfo.DiskUsage
				ci.guestIDs = nodeInfo.Guests
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
	for attempt := range maxRetries {
		err := writeOnce()
		if err == nil {
			break
		}

		if strings.Contains(err.Error(), "database is locked") && attempt < maxRetries-1 {
			time.Sleep(time.Duration(100*(attempt+1)) * time.Millisecond)
			continue
		}

		return err
	}

	var syncPayload []clusterServiceInterfaces.NodeHealthSync
	for _, cur := range current {
		status := "offline"
		if cur.healthOK {
			status = "online"
		}

		hostname := cur.canonHost
		if hostname == "" {
			hostname = cur.rawHost
		}

		syncPayload = append(syncPayload, clusterServiceInterfaces.NodeHealthSync{
			NodeUUID:    cur.nodeUUID,
			Hostname:    hostname,
			API:         cur.api,
			Status:      status,
			CPU:         cur.cpu,
			CPUUsage:    cur.cpuUsage,
			Memory:      cur.memory,
			MemoryUsage: cur.memUsage,
			Disk:        cur.disk,
			DiskUsage:   cur.diskUsage,
			GuestIDs:    cur.guestIDs,
		})
	}

	payloadBytes, _ := json.Marshal(syncPayload)

	for _, server := range cfg.Servers {
		if server.Address == s.Raft.Leader() {
			continue
		}

		go func(addr string) {
			host, _, _ := net.SplitHostPort(addr)
			if host == "" {
				host = addr
			}

			url := fmt.Sprintf("https://%s:%d/api/internal/cluster/sync-health", host, config.ParsedConfig.Port)

			utils.HTTPPostJSONWithTimeout(
				url,
				payloadBytes,
				map[string]string{
					"Content-Type":    "application/json",
					"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
				},
				5*time.Second,
			)
		}(string(server.Address))
	}

	return nil
}

func (s *Service) FastStatusCheck() {
	if s.Raft == nil {
		return
	}

	state := s.Raft.State()
	_, leaderID := s.Raft.LeaderWithID()
	now := time.Now()

	if err := s.DB.Model(&clusterModels.ClusterNode{}).
		Where("node_uuid = ?", s.NodeID).
		Updates(map[string]any{"status": "online", "updated_at": now}).Error; err != nil {
		logger.L.Debug().Err(err).Msg("FastStatusCheck: failed to update local node status")
	}

	fut := s.Raft.GetConfiguration()
	if err := fut.Error(); err != nil {
		return
	}

	peerIDs := make([]string, 0, len(fut.Configuration().Servers))
	peerAddrs := make(map[string]string, len(fut.Configuration().Servers))
	for _, server := range fut.Configuration().Servers {
		id := string(server.ID)
		if id == s.NodeID {
			continue
		}
		peerIDs = append(peerIDs, id)
		peerAddrs[id] = string(server.Address)
	}

	if len(peerIDs) == 0 {
		return
	}

	setPeersStatus := func(status string) {
		if err := s.DB.Model(&clusterModels.ClusterNode{}).
			Where("node_uuid IN ?", peerIDs).
			Updates(map[string]any{"status": status, "updated_at": now}).Error; err != nil {
			logger.L.Debug().Err(err).Str("status", status).Msg("FastStatusCheck: failed to update peer statuses")
		}
	}

	if state != raft.Leader {
		if leaderID == "" {
			setPeersStatus("offline")
			return
		}

		if err := s.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&clusterModels.ClusterNode{}).
				Where("node_uuid IN ?", peerIDs).
				Updates(map[string]any{"status": "offline", "updated_at": now}).Error; err != nil {
				return err
			}

			if err := tx.Model(&clusterModels.ClusterNode{}).
				Where("node_uuid = ?", string(leaderID)).
				Updates(map[string]any{"status": "online", "updated_at": now}).Error; err != nil {
				return err
			}

			return nil
		}); err != nil {
			logger.L.Debug().Err(err).Msg("FastStatusCheck: failed to apply non-leader status view")
		}
		return
	}

	if state == raft.Leader {
		if err := s.Raft.VerifyLeader().Error(); err != nil {
			setPeersStatus("offline")
			return
		}

		selfHostname, err := utils.GetSystemHostname()
		if err != nil {
			logger.L.Debug().Err(err).Msg("FastStatusCheck: failed to get system hostname")
			setPeersStatus("offline")
			return
		}

		clusterToken, err := s.getClusterToken(selfHostname)
		if err != nil {
			logger.L.Debug().Err(err).Msg("FastStatusCheck: failed to get cluster token")
			setPeersStatus("offline")
			return
		}

		headers := map[string]string{
			"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
		}

		results := make(map[string]string, len(peerIDs))
		var wg sync.WaitGroup
		var mu sync.Mutex

		for _, id := range peerIDs {
			addr := peerAddrs[id]
			wg.Add(1)
			go func(nodeID, raftAddr string) {
				defer wg.Done()

				host, _, err := net.SplitHostPort(raftAddr)
				if err != nil || host == "" {
					host = raftAddr
				}

				status := "offline"
				url := fmt.Sprintf("https://%s:%d/api/health/http", host, config.ParsedConfig.Port)
				if _, err := utils.HTTPGetStatus(url, headers); err == nil {
					status = "online"
				}

				mu.Lock()
				results[nodeID] = status
				mu.Unlock()
			}(id, addr)
		}

		wg.Wait()

		if err := s.DB.Transaction(func(tx *gorm.DB) error {
			for id, status := range results {
				if err := tx.Model(&clusterModels.ClusterNode{}).
					Where("node_uuid = ?", id).
					Updates(map[string]any{"status": status, "updated_at": now}).Error; err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			logger.L.Debug().Err(err).Msg("FastStatusCheck: failed to apply per-node leader checks")
		}
	}
}

func (s *Service) StartClusterMonitors() {
	s.monitorOnce.Do(func() {
		if err := s.PopulateClusterNodes(); err != nil {
			if !strings.Contains(err.Error(), "raft_not_initialized") {
				logger.L.Error().Err(err).Msg("Failed to populate cluster nodes")
			}
		}

		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()

			for range ticker.C {
				s.FastStatusCheck()
			}
		}()

		go func() {
			ticker := time.NewTicker(60 * time.Second)
			defer ticker.Stop()

			for range ticker.C {
				if err := s.PopulateClusterNodes(); err != nil {
					if !strings.Contains(err.Error(), "raft_not_initialized") {
						logger.L.Error().Err(err).Msg("Failed to populate cluster nodes")
					}
				}
			}
		}()
	})
}
