// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	hub "github.com/alchemillahq/sylve/internal/events"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	infoServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/info"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
	"github.com/rs/zerolog"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type curInfo struct {
	nodeUUID     string
	api          string
	canonHost    string
	rawHost      string
	healthOK     bool
	sylveVersion string
	sylveCommit  string
	protocol     int

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
const (
	// Keep CPU updates more sensitive so low non-zero usage doesn't appear stuck at 0%.
	cpuUsageThreshold = 1.0
	// Memory and disk can keep a wider threshold to avoid unnecessary write churn.
	resourceUsageThreshold = 5.0
)

const (
	nodeStatusOnline  = "online"
	nodeStatusOffline = "offline"

	peerOfflineConsecutiveFailures = 2
)

func statusFromHealth(healthOK bool) string {
	if healthOK {
		return nodeStatusOnline
	}
	return nodeStatusOffline
}

func preferredHostname(cur curInfo) string {
	if cur.canonHost != "" {
		return cur.canonHost
	}
	return cur.rawHost
}

func raftAddressHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return addr
	}
	return host
}

func publishLeftPanelRefresh() {
	hub.SSE.Publish(hub.Event{
		Type:      "left-panel-refresh",
		Timestamp: time.Now(),
	})
}

func (s *Service) applyProbeHysteresis(peerKey, observedStatus string) string {
	peerKey = strings.TrimSpace(peerKey)
	if peerKey == "" {
		return observedStatus
	}

	s.peerProbeMu.Lock()
	defer s.peerProbeMu.Unlock()

	if observedStatus == nodeStatusOnline {
		delete(s.peerProbeFailureStreak, peerKey)
		return nodeStatusOnline
	}

	s.peerProbeFailureStreak[peerKey]++
	if s.peerProbeFailureStreak[peerKey] < peerOfflineConsecutiveFailures {
		return nodeStatusOnline
	}

	return nodeStatusOffline
}

func currentToClusterNode(cur curInfo) clusterModels.ClusterNode {
	return clusterModels.ClusterNode{
		NodeUUID:     cur.nodeUUID,
		Hostname:     preferredHostname(cur),
		SylveVersion: cur.sylveVersion,
		SylveCommit:  cur.sylveCommit,
		Protocol:     cur.protocol,
		API:          cur.api,
		Status:       statusFromHealth(cur.healthOK),
		CPU:          cur.cpu,
		CPUUsage:     cur.cpuUsage,
		Memory:       cur.memory,
		MemoryUsage:  cur.memUsage,
		Disk:         cur.disk,
		DiskUsage:    cur.diskUsage,
		GuestIDs:     cur.guestIDs,
	}
}

func currentToClusterNodeUpdates(cur curInfo) map[string]any {
	updates := map[string]any{
		"api":        cur.api,
		"updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
	}

	// A failed node-info call says nothing about liveness: keep the status the
	// health check (with its retry) last set, and the last known inventory.
	if cur.healthOK {
		updates["status"] = nodeStatusOnline
		guestIDs := cur.guestIDs
		if guestIDs == nil {
			guestIDs = []uint{}
		}
		if b, err := json.Marshal(guestIDs); err == nil {
			updates["guest_ids"] = string(b)
		}
	}

	if cur.canonHost != "" {
		updates["hostname"] = cur.canonHost
	}
	if cur.sylveVersion != "" {
		updates["sylve_version"] = cur.sylveVersion
	}
	if cur.healthOK {
		updates["sylve_commit"] = cur.sylveCommit
		updates["protocol"] = cur.protocol
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

	return updates
}

func hasSignificantChange(cur curInfo, ex clusterModels.ClusterNode) bool {
	if cur.healthOK && ex.Status != nodeStatusOnline {
		return true
	}

	if ex.API != cur.api {
		return true
	}

	hostname := preferredHostname(cur)

	if ex.Hostname != hostname {
		return true
	}
	if cur.sylveVersion != "" && ex.SylveVersion != cur.sylveVersion {
		return true
	}
	if cur.healthOK && ex.SylveCommit != cur.sylveCommit {
		return true
	}
	if cur.healthOK && ex.Protocol != cur.protocol {
		return true
	}

	if cur.healthOK && len(cur.guestIDs) != len(ex.GuestIDs) {
		return true
	}

	if cur.healthOK && len(cur.guestIDs) > 0 {
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

		if math.Abs(ex.CPUUsage-cur.cpuUsage) >= cpuUsageThreshold {
			return true
		}

		if math.Abs(ex.MemoryUsage-cur.memUsage) >= resourceUsageThreshold {
			return true
		}

		if math.Abs(ex.DiskUsage-cur.diskUsage) >= resourceUsageThreshold {
			return true
		}
	}

	return false
}

func (s *Service) GetNodeInfo(host string, port int) (infoServiceInterfaces.NodeInfo, error) {
	var nodeInfo infoServiceInterfaces.NodeInfo

	url := fmt.Sprintf("https://%s:%d/api/info/node", host, port)
	body, _, err := utils.HTTPGetJSONRead(
		url,
		map[string]string{"Accept": "application/json"},
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

func (s *Service) collectCurrentClusterInfo(cfg raft.Configuration) map[string]curInfo {
	current := make(map[string]curInfo, len(cfg.Servers))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, server := range cfg.Servers {
		wg.Add(1)
		go func(serverID, serverAddr string) {
			defer wg.Done()

			uuid := serverID
			host := raftAddressHost(serverAddr)
			api := fmt.Sprintf("%s:%d", host, ClusterEmbeddedHTTPSPort)

			ci := curInfo{
				nodeUUID: uuid,
				api:      api,
				rawHost:  host,
				healthOK: false,
			}

			nodeInfo, err := s.GetNodeInfo(host, ClusterEmbeddedHTTPSPort)
			if err == nil {
				ci.healthOK = true
				ci.canonHost = nodeInfo.Hostname
				ci.sylveVersion = nodeInfo.SylveVersion
				ci.sylveCommit = nodeInfo.SylveCommit
				ci.protocol = nodeInfo.ClusterProtocol
				ci.cpu = int(nodeInfo.LogicalCores)
				ci.cpuUsage = nodeInfo.CPUUsage
				ci.memory = nodeInfo.RAMTotal
				ci.memUsage = nodeInfo.RAMUsage
				ci.disk = nodeInfo.DiskTotal
				ci.diskUsage = nodeInfo.DiskUsage
				ci.guestIDs = nodeInfo.Guests
			} else {
				logger.L.Debug().
					Str("node_uuid", uuid).
					Str("host", host).
					Err(err).
					Msg("PopulateClusterNodes: node info probe failed, keeping node offline")
			}

			mu.Lock()
			current[uuid] = ci
			mu.Unlock()
		}(string(server.ID), string(server.Address))
	}

	wg.Wait()
	return current
}

func (s *Service) persistCurrentClusterNodesOnce(current map[string]curInfo) (bool, error) {
	changed := false

	err := s.DB.Transaction(func(tx *gorm.DB) error {
		var existing []clusterModels.ClusterNode
		if err := tx.Find(&existing).Error; err != nil {
			return err
		}

		exByUUID := make(map[string]clusterModels.ClusterNode, len(existing))
		for _, n := range existing {
			exByUUID[n.NodeUUID] = n
		}

		for _, cur := range current {
			if ex, exists := exByUUID[cur.nodeUUID]; exists {
				if !hasSignificantChange(cur, ex) {
					delete(exByUUID, cur.nodeUUID)
					continue
				}
			}

			insertRow := currentToClusterNode(cur)
			updates := currentToClusterNodeUpdates(cur)

			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "node_uuid"}},
				DoUpdates: clause.Assignments(updates),
			}).Create(&insertRow).Error; err != nil {
				return err
			}
			changed = true

			delete(exByUUID, cur.nodeUUID)
		}

		if len(exByUUID) == 0 {
			return nil
		}

		ids := make([]string, 0, len(exByUUID))
		for uuid := range exByUUID {
			ids = append(ids, uuid)
		}

		if err := tx.Where("node_uuid IN ?", ids).Delete(&clusterModels.ClusterNode{}).Error; err != nil {
			return err
		}
		changed = true
		return nil
	})
	return changed, err
}

func (s *Service) persistCurrentClusterNodes(current map[string]curInfo) (bool, error) {
	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		changed, err := s.persistCurrentClusterNodesOnce(current)
		if err == nil {
			return changed, nil
		}
		if strings.Contains(err.Error(), "database is locked") && attempt < maxRetries-1 {
			time.Sleep(time.Duration(100*(attempt+1)) * time.Millisecond)
			continue
		}
		return false, err
	}
	return false, nil
}

func (s *Service) PopulateClusterNodes() error {
	if s.Raft() == nil {
		return nil
	}

	if s.Raft().State() != raft.Leader {
		return nil
	}
	if err := s.Raft().VerifyLeader().Error(); err != nil {
		return nil
	}

	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err != nil {
		return err
	}

	if !c.Enabled {
		return nil
	}

	cfgFuture := s.Raft().GetConfiguration()
	if err := cfgFuture.Error(); err != nil {
		return fmt.Errorf("failed_to_get_raft_configuration: %w", err)
	}
	cfg := cfgFuture.Configuration()

	current := s.collectCurrentClusterInfo(cfg)

	changed, err := s.persistCurrentClusterNodes(current)
	if err != nil {
		return err
	}
	if changed {
		publishLeftPanelRefresh()
	}

	syncPayload := make([]clusterServiceInterfaces.NodeHealthSync, 0, len(current))
	ids := make([]string, 0, len(current))
	for nodeUUID := range current {
		ids = append(ids, nodeUUID)
	}

	if len(ids) > 0 {
		var nodes []clusterModels.ClusterNode
		err = s.DB.Where("node_uuid IN ?", ids).Find(&nodes).Error
		if err == nil {
			syncPayload = make([]clusterServiceInterfaces.NodeHealthSync, 0, len(nodes))
			for _, node := range nodes {
				syncPayload = append(syncPayload, clusterServiceInterfaces.NodeHealthSync{
					NodeUUID:     node.NodeUUID,
					Hostname:     node.Hostname,
					SylveVersion: node.SylveVersion,
					SylveCommit:  node.SylveCommit,
					Protocol:     node.Protocol,
					API:          node.API,
					Status:       node.Status,
					CPU:          node.CPU,
					CPUUsage:     node.CPUUsage,
					Memory:       node.Memory,
					MemoryUsage:  node.MemoryUsage,
					Disk:         node.Disk,
					DiskUsage:    node.DiskUsage,
					GuestIDs:     node.GuestIDs,
				})
			}
		}
	}

	if err != nil {
		logger.L.Debug().
			Err(err).
			Msg("PopulateClusterNodes: failed to build DB-backed sync payload, falling back to probe payload")
		syncPayload = make([]clusterServiceInterfaces.NodeHealthSync, 0, len(current))
		for _, cur := range current {
			syncPayload = append(syncPayload, clusterServiceInterfaces.NodeHealthSync{
				NodeUUID:     cur.nodeUUID,
				Hostname:     preferredHostname(cur),
				SylveVersion: cur.sylveVersion,
				SylveCommit:  cur.sylveCommit,
				Protocol:     cur.protocol,
				API:          cur.api,
				Status:       statusFromHealth(cur.healthOK),
				CPU:          cur.cpu,
				CPUUsage:     cur.cpuUsage,
				Memory:       cur.memory,
				MemoryUsage:  cur.memUsage,
				Disk:         cur.disk,
				DiskUsage:    cur.diskUsage,
				GuestIDs:     cur.guestIDs,
			})
		}
	}

	s.fanOutHealthSync(syncPayload, cfg)

	return nil
}

func (s *Service) updateNodeStatus(nodeID, status string, now time.Time) (int64, error) {
	result := s.DB.Model(&clusterModels.ClusterNode{}).
		Where("node_uuid = ? AND status <> ?", nodeID, status).
		Updates(map[string]any{"status": status, "updated_at": now})

	return result.RowsAffected, result.Error
}

func (s *Service) classifyPeerStatuses(results map[string]string) ([]string, []string) {
	onlinePeerIDs := make([]string, 0, len(results))
	offlinePeerIDs := make([]string, 0, len(results))
	for id, status := range results {
		if status == nodeStatusOnline {
			onlinePeerIDs = append(onlinePeerIDs, id)
		} else {
			offlinePeerIDs = append(offlinePeerIDs, id)
		}
	}
	return onlinePeerIDs, offlinePeerIDs
}

func (s *Service) probePeerStatus(raftAddr string) string {
	host := raftAddressHost(raftAddr)
	url := fmt.Sprintf("https://%s:%d/api/health/http", host, ClusterEmbeddedHTTPSPort)
	if _, err := utils.HTTPGetStatus(url, nil); err == nil {
		return nodeStatusOnline
	}
	return nodeStatusOffline
}

func (s *Service) probePeerStatusWithHysteresis(peerKey, raftAddr string) string {
	firstObserved := s.probePeerStatus(raftAddr)
	status := s.applyProbeHysteresis(peerKey, firstObserved)

	// Confirm a first failure immediately so offline can converge within one 5s cycle.
	if firstObserved == nodeStatusOffline && status != nodeStatusOffline {
		secondObserved := s.probePeerStatus(raftAddr)
		status = s.applyProbeHysteresis(peerKey, secondObserved)
	}

	return status
}

func (s *Service) probePeerStatuses(peerIDs []string, peerAddrs map[string]string) map[string]string {
	results := make(map[string]string, len(peerIDs))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, id := range peerIDs {
		addr := peerAddrs[id]
		wg.Add(1)
		go func(nodeID, raftAddr string) {
			defer wg.Done()

			status := s.probePeerStatusWithHysteresis(nodeID, raftAddr)

			mu.Lock()
			results[nodeID] = status
			mu.Unlock()
		}(id, addr)
	}

	wg.Wait()
	return results
}

func (s *Service) applyLeaderPeerStatuses(onlinePeerIDs, offlinePeerIDs []string, now time.Time) (bool, int64, int64, error) {
	changed := false
	onlineRows := int64(0)
	offlineRows := int64(0)

	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if len(onlinePeerIDs) > 0 {
			result := tx.Model(&clusterModels.ClusterNode{}).
				Where("node_uuid IN ? AND status <> ?", onlinePeerIDs, nodeStatusOnline).
				Updates(map[string]any{"status": nodeStatusOnline, "updated_at": now})
			if result.Error != nil {
				return result.Error
			}
			onlineRows = result.RowsAffected
			if result.RowsAffected > 0 {
				changed = true
			}
		}

		if len(offlinePeerIDs) > 0 {
			result := tx.Model(&clusterModels.ClusterNode{}).
				Where("node_uuid IN ? AND status <> ?", offlinePeerIDs, nodeStatusOffline).
				Updates(map[string]any{"status": nodeStatusOffline, "updated_at": now})
			if result.Error != nil {
				return result.Error
			}
			offlineRows = result.RowsAffected
			if result.RowsAffected > 0 {
				changed = true
			}
		}

		return nil
	})
	if err != nil {
		return false, 0, 0, err
	}

	return changed, onlineRows, offlineRows, nil
}

func (s *Service) fastStatusCheckLeader(peerIDs []string, peerAddrs map[string]string, now time.Time) {
	results := s.probePeerStatuses(peerIDs, peerAddrs)
	onlinePeerIDs, offlinePeerIDs := s.classifyPeerStatuses(results)

	changed, onlineRows, offlineRows, err := s.applyLeaderPeerStatuses(onlinePeerIDs, offlinePeerIDs, now)
	if err != nil {
		logger.L.Debug().Err(err).Msg("FastStatusCheck: failed to apply per-node leader checks")
		return
	}

	if !changed {
		return
	}

	logger.L.Debug().
		Int64("online_rows", onlineRows).
		Int64("offline_rows", offlineRows).
		Msg("FastStatusCheck: applied leader peer status updates")
	publishLeftPanelRefresh()

	go s.syncClusterHealthToFollowers()
}

func (s *Service) FastStatusCheck() {
	if s.Raft() == nil {
		return
	}
	if s.Raft().State() != raft.Leader {
		return
	}
	if err := s.Raft().VerifyLeader().Error(); err != nil {
		return
	}

	now := time.Now()

	localRows, err := s.updateNodeStatus(s.NodeID, nodeStatusOnline, now)
	if err != nil {
		logger.L.Debug().Err(err).Msg("FastStatusCheck: failed to update local node status")
	} else if localRows > 0 {
		publishLeftPanelRefresh()
	}

	cfgFuture := s.Raft().GetConfiguration()
	if err := cfgFuture.Error(); err != nil {
		logger.L.Debug().Err(err).Msg("FastStatusCheck: failed to get raft configuration")
		return
	}
	cfg := cfgFuture.Configuration()

	peerIDs := make([]string, 0, len(cfg.Servers))
	peerAddrs := make(map[string]string, len(cfg.Servers))
	for _, server := range cfg.Servers {
		id := string(server.ID)
		if id == s.NodeID {
			continue
		}
		peerIDs = append(peerIDs, id)
		peerAddrs[id] = string(server.Address)
	}

	s.peerProbeMu.Lock()
	for id := range s.peerProbeFailureStreak {
		if _, member := peerAddrs[id]; !member {
			delete(s.peerProbeFailureStreak, id)
		}
	}
	s.peerProbeMu.Unlock()

	if len(peerIDs) == 0 {
		logger.LogWithDeduplication(zerolog.DebugLevel, "FastStatusCheck: no peers in raft configuration")
		return
	}

	s.fastStatusCheckLeader(peerIDs, peerAddrs, now)
}

func (s *Service) syncClusterHealthToFollowers() {
	if s.Raft() == nil || s.Raft().State() != raft.Leader || s.AuthService == nil {
		return
	}

	var nodes []clusterModels.ClusterNode
	if err := s.DB.Find(&nodes).Error; err != nil || len(nodes) == 0 {
		return
	}

	syncPayload := make([]clusterServiceInterfaces.NodeHealthSync, 0, len(nodes))
	for _, node := range nodes {
		syncPayload = append(syncPayload, clusterServiceInterfaces.NodeHealthSync{
			NodeUUID:     node.NodeUUID,
			Hostname:     node.Hostname,
			SylveVersion: node.SylveVersion,
			SylveCommit:  node.SylveCommit,
			Protocol:     node.Protocol,
			API:          node.API,
			Status:       node.Status,
			CPU:          node.CPU,
			CPUUsage:     node.CPUUsage,
			Memory:       node.Memory,
			MemoryUsage:  node.MemoryUsage,
			Disk:         node.Disk,
			DiskUsage:    node.DiskUsage,
			GuestIDs:     node.GuestIDs,
		})
	}

	cfgFuture := s.Raft().GetConfiguration()
	if err := cfgFuture.Error(); err != nil {
		return
	}
	cfg := cfgFuture.Configuration()

	s.fanOutHealthSync(syncPayload, cfg)
}

func (s *Service) fanOutHealthSync(payload []clusterServiceInterfaces.NodeHealthSync, cfg raft.Configuration) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return
	}
	for _, server := range cfg.Servers {
		if string(server.ID) == s.LocalNodeID() {
			continue
		}
		go func(server raft.Server) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			host := raftAddressHost(string(server.Address))
			if _, err := callNodeAt[struct{}](ctx, s, host, string(server.ID), "sync-health", payloadBytes, 0); err != nil {
				logger.L.Debug().Err(err).Str("node_id", string(server.ID)).Msg("cluster_health_sync_failed")
			}
		}(server)
	}
}

func (s *Service) StartClusterMonitors(ctx context.Context) {
	s.monitorOnce.Do(func() {
		runPopulateClusterNodes := func() {
			if err := s.PopulateClusterNodes(); err != nil {
				if !strings.Contains(err.Error(), "raft_not_initialized") {
					logger.L.Error().Err(err).Msg("Failed to populate cluster nodes")
				}
			}
			if err := s.forgetDepartedMembers(ctx); err != nil && !errors.Is(err, ErrNodeLeaveFenced) {
				logger.L.Warn().Err(err).Msg("cluster_departed_member_cleanup_failed")
			}
		}

		runPopulateClusterNodes()

		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.FastStatusCheck()
				}
			}
		}()

		go func() {
			ticker := time.NewTicker(clusterNodePopulateInterval)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					runPopulateClusterNodes()
				}
			}
		}()

		go func() {
			timer := time.NewTimer(replicatedRetentionInitialDelay)
			defer timer.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
					_, release, err := s.EnterMutation(ctx)
					if err == nil {
						if err := s.EnforceReplicatedRetention(time.Now().UTC()); err != nil {
							logger.L.Error().Err(err).Msg("failed_to_enforce_replicated_retention")
						}
						release()
					} else if !errors.Is(err, ErrNodeLeaveFenced) {
						logger.L.Error().Err(err).Msg("failed_to_admit_replicated_retention")
					}
					timer.Reset(replicatedRetentionInterval)
				}
			}
		}()
	})
}

func (s *Service) StartMembershipReconcilers(ctx context.Context) {
	s.reconcilerOnce.Do(func() {
		s.startLeaveReconciler(ctx)
		s.startJoinReconciler(ctx)
		s.startGuestIdentityReconciler(ctx)
	})
}
