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
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/network"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

var _ clusterServiceInterfaces.ClusterServiceInterface = (*Service)(nil)

type Service struct {
	DB          *gorm.DB
	Raft        *raft.Raft
	RaftID      *raft.ServerAddress
	NodeID      string
	Transport   *raft.NetworkTransport
	AuthService serviceInterfaces.AuthServiceInterface
	JailService jailServiceInterfaces.JailServiceInterface

	clusterJoinMu sync.Mutex

	peerProbeMu            sync.Mutex
	peerProbeFailureStreak map[string]int

	embeddedSSHOnce sync.Once
	monitorOnce     sync.Once

	clusterStartHook func(ip string) error

	guestIdentityInventoryAPIForNode func(string, raft.ServerAddress) (string, error)
}

func (s *Service) SetClusterStartHook(fn func(ip string) error) {
	s.clusterStartHook = fn
}

func (s *Service) triggerClusterStart(ip string) error {
	if s.clusterStartHook == nil {
		return nil
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := s.clusterStartHook(ip); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
		}
	}
	return lastErr
}

func NewClusterService(db *gorm.DB, authService serviceInterfaces.AuthServiceInterface, jailService jailServiceInterfaces.JailServiceInterface) clusterServiceInterfaces.ClusterServiceInterface {
	return &Service{
		DB:          db,
		Raft:        nil,
		RaftID:      nil,
		NodeID:      "",
		AuthService: authService,
		JailService: jailService,

		peerProbeFailureStreak: make(map[string]int),
	}
}

func (s *Service) GetClusterDetails() (*clusterServiceInterfaces.ClusterDetails, error) {
	out := &clusterServiceInterfaces.ClusterDetails{
		Cluster:  nil,
		Nodes:    []clusterServiceInterfaces.RaftNode{},
		LeaderID: "",
		Partial:  false,
	}

	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err == nil {
		out.Cluster = &c
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	detail := s.Detail()
	if detail == nil {
		return out, fmt.Errorf("failed to get cluster detail")
	}

	out.NodeID = detail.NodeID

	if s.Raft == nil || c.Enabled == false {
		return out, nil
	}

	leaderAddr, leaderID := s.Raft.LeaderWithID()
	out.LeaderID = string(leaderID)
	out.LeaderAddress = string(leaderAddr)

	fut := s.Raft.GetConfiguration()
	if err := fut.Error(); err != nil {
		out.Partial = true
		return out, nil
	}
	conf := fut.Configuration()

	suffrageStr := func(sf raft.ServerSuffrage) string {
		switch sf {
		case raft.Voter:
			return "voter"
		case raft.Nonvoter:
			return "nonvoter"
		case raft.Staging:
			return "staging"
		default:
			return "unknown"
		}
	}

	for _, srv := range conf.Servers {
		id := string(srv.ID)
		addr := string(srv.Address)

		var node clusterModels.ClusterNode
		err := s.DB.Select("guest_ids").Where("node_uuid = ?", id).First(&node).Error

		if err != nil && err != gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("failed to query guest ids for node %s: %w", id, err)
		}

		out.Nodes = append(out.Nodes, clusterServiceInterfaces.RaftNode{
			ID:       id,
			Address:  addr,
			Suffrage: suffrageStr(srv.Suffrage),
			IsLeader: id == string(leaderID) || addr == string(leaderAddr),
			GuestIDs: node.GuestIDs,
		})
	}

	return out, nil
}

func (s *Service) waitUntilLeader(timeout time.Duration) (bool, raft.ServerAddress, error) {
	deadline := time.Now().Add(timeout)
	var lastKnownLeader raft.ServerAddress

	for time.Now().Before(deadline) {
		if s.Raft.State() == raft.Leader {
			return true, s.Raft.Leader(), nil
		}
		if addr := s.Raft.Leader(); addr != "" {
			lastKnownLeader = addr
		}
		time.Sleep(raftLeaderPollInterval)
	}

	if lastKnownLeader != "" {
		return false, lastKnownLeader, fmt.Errorf("timeout waiting to become leader")
	}

	return false, "", fmt.Errorf("timeout waiting for leader election")
}

func (s *Service) backfillPreClusterState() error {
	{
		var notes []clusterModels.ClusterNote
		if err := s.DB.Order("id ASC").Find(&notes).Error; err != nil {
			return fmt.Errorf("scan_existing_notes: %w", err)
		}
		for _, n := range notes {
			payloadStruct := struct {
				ID      uint   `json:"id"`
				Title   string `json:"title"`
				Content string `json:"content"`
			}{ID: n.ID, Title: n.Title, Content: n.Content}

			data, _ := json.Marshal(payloadStruct)
			cmd := clusterModels.Command{Type: "note", Action: "create", Data: data}
			if err := s.Raft.Apply(utils.MustJSON(cmd), 5*time.Second).Error(); err != nil {
				return fmt.Errorf("apply_synth_create_note id=%d: %w", n.ID, err)
			}
		}
	}

	{
		var opts []clusterModels.ClusterOption
		if err := s.DB.Order("id ASC").Find(&opts).Error; err != nil {
			return fmt.Errorf("scan_existing_options: %w", err)
		}

		for _, o := range opts {
			payloadStruct := struct {
				ID             uint   `json:"id"`
				KeyboardLayout string `json:"keyboardLayout"`
			}{ID: o.ID, KeyboardLayout: o.KeyboardLayout}

			data, _ := json.Marshal(payloadStruct)
			cmd := clusterModels.Command{Type: "options", Action: "set", Data: data}
			if err := s.Raft.Apply(utils.MustJSON(cmd), 5*time.Second).Error(); err != nil {
				return fmt.Errorf("apply_synth_set_options id=%d: %w", o.ID, err)
			}
		}
	}

	{
		var targets []clusterModels.BackupTarget
		if err := s.DB.Order("id ASC").Find(&targets).Error; err != nil {
			return fmt.Errorf("scan_existing_backup_targets: %w", err)
		}

		for _, t := range targets {
			data, _ := json.Marshal(clusterModels.BackupTargetToReplicationPayload(t))
			cmd := clusterModels.Command{Type: "backup_target", Action: "create", Data: data}
			if err := s.Raft.Apply(utils.MustJSON(cmd), 5*time.Second).Error(); err != nil {
				return fmt.Errorf("apply_synth_create_backup_target id=%d: %w", t.ID, err)
			}
		}
	}

	{
		var jobs []clusterModels.BackupJob
		if err := s.DB.Order("id ASC").Find(&jobs).Error; err != nil {
			return fmt.Errorf("scan_existing_backup_jobs: %w", err)
		}

		for _, j := range jobs {
			payloadStruct := struct {
				ID               uint       `json:"id"`
				Name             string     `json:"name"`
				TargetID         uint       `json:"targetId"`
				RunnerNodeID     string     `json:"runnerNodeId"`
				Mode             string     `json:"mode"`
				SourceDataset    string     `json:"sourceDataset"`
				JailRootDataset  string     `json:"jailRootDataset"`
				FriendlySrc      string     `json:"friendlySrc"`
				DestSuffix       string     `json:"destSuffix"`
				PruneKeepLast    int        `json:"pruneKeepLast"`
				PruneTarget      bool       `json:"pruneTarget"`
				StopBeforeBackup bool       `json:"stopBeforeBackup"`
				Recursive        bool       `json:"recursive"`
				CronExpr         string     `json:"cronExpr"`
				Enabled          bool       `json:"enabled"`
				NextRunAt        *time.Time `json:"nextRunAt"`
			}{
				ID:               j.ID,
				Name:             j.Name,
				TargetID:         j.TargetID,
				RunnerNodeID:     j.RunnerNodeID,
				Mode:             j.Mode,
				SourceDataset:    j.SourceDataset,
				JailRootDataset:  j.JailRootDataset,
				FriendlySrc:      j.FriendlySrc,
				DestSuffix:       j.DestSuffix,
				PruneKeepLast:    j.PruneKeepLast,
				PruneTarget:      j.PruneTarget,
				StopBeforeBackup: j.StopBeforeBackup,
				Recursive:        j.Recursive,
				CronExpr:         j.CronExpr,
				Enabled:          j.Enabled,
				NextRunAt:        j.NextRunAt,
			}

			data, _ := json.Marshal(payloadStruct)
			cmd := clusterModels.Command{Type: "backup_job", Action: "create", Data: data}
			if err := s.Raft.Apply(utils.MustJSON(cmd), 5*time.Second).Error(); err != nil {
				return fmt.Errorf("apply_synth_create_backup_job id=%d: %w", j.ID, err)
			}
		}
	}

	{
		var policies []clusterModels.ReplicationPolicy
		if err := s.DB.Preload("Targets").Order("id ASC").Find(&policies).Error; err != nil {
			return fmt.Errorf("scan_existing_replication_policies: %w", err)
		}

		for _, p := range policies {
			data, _ := json.Marshal(clusterModels.ReplicationPolicyPayload{
				Policy:  p,
				Targets: p.Targets,
			})
			cmd := clusterModels.Command{Type: "replication_policy", Action: "create", Data: data}
			if err := s.Raft.Apply(utils.MustJSON(cmd), 5*time.Second).Error(); err != nil {
				return fmt.Errorf("apply_synth_create_replication_policy id=%d: %w", p.ID, err)
			}
		}
	}

	{
		var leases []clusterModels.ReplicationLease
		if err := s.DB.Order("id ASC").Find(&leases).Error; err != nil {
			return fmt.Errorf("scan_existing_replication_leases: %w", err)
		}

		for _, l := range leases {
			data, _ := json.Marshal(l)
			cmd := clusterModels.Command{Type: "replication_lease", Action: "upsert", Data: data}
			if err := s.Raft.Apply(utils.MustJSON(cmd), 5*time.Second).Error(); err != nil {
				return fmt.Errorf("apply_synth_upsert_replication_lease id=%d: %w", l.ID, err)
			}
		}
	}

	{
		var identities []clusterModels.ClusterSSHIdentity
		if err := s.DB.Order("id ASC").Find(&identities).Error; err != nil {
			return fmt.Errorf("scan_existing_cluster_ssh_identities: %w", err)
		}

		for _, i := range identities {
			data, _ := json.Marshal(i)
			cmd := clusterModels.Command{Type: "cluster_ssh_identity", Action: "upsert", Data: data}
			if err := s.Raft.Apply(utils.MustJSON(cmd), 5*time.Second).Error(); err != nil {
				return fmt.Errorf("apply_synth_upsert_cluster_ssh_identity id=%d: %w", i.ID, err)
			}
		}
	}

	{
		var keys []clusterModels.EncryptionKey
		if err := s.DB.Order("id ASC").Find(&keys).Error; err != nil {
			return fmt.Errorf("scan_existing_encryption_keys: %w", err)
		}

		for _, k := range keys {
			data, _ := json.Marshal(k)
			cmd := clusterModels.Command{Type: "encryption_key", Action: "upsert", Data: data}
			if err := s.Raft.Apply(utils.MustJSON(cmd), 5*time.Second).Error(); err != nil {
				return fmt.Errorf("apply_synth_upsert_encryption_key id=%d: %w", k.ID, err)
			}
		}
	}

	{
		var events []clusterModels.ReplicationEvent
		if err := s.DB.Order("id ASC").Find(&events).Error; err != nil {
			return fmt.Errorf("scan_existing_replication_events: %w", err)
		}

		for _, e := range events {
			data, _ := json.Marshal(e)
			cmd := clusterModels.Command{Type: "replication_event", Action: "create", Data: data}
			if err := s.Raft.Apply(utils.MustJSON(cmd), 5*time.Second).Error(); err != nil {
				return fmt.Errorf("apply_synth_create_replication_event id=%d: %w", e.ID, err)
			}
		}
	}

	if err := s.Raft.Barrier(10 * time.Second).Error(); err != nil {
		return fmt.Errorf("barrier_after_backfill: %w", err)
	}

	return nil
}

func (s *Service) ResyncClusterState() error {
	if s.Raft == nil {
		return errors.New("raft_not_initialized")
	}

	if s.Raft.State() != raft.Leader {
		addr, id := s.Raft.LeaderWithID()
		return fmt.Errorf("not_leader; leader_addr=%s; leader_id=%s", string(addr), string(id))
	}

	if err := s.backfillPreClusterState(); err != nil {
		return fmt.Errorf("state_backfill_failed: %w", err)
	}

	if err := s.Raft.Snapshot().Error(); err != nil && !errors.Is(err, raft.ErrNothingNewToSnapshot) {
		return fmt.Errorf("raft_snapshot_failed: %w", err)
	}

	return nil
}

func (s *Service) stopRaftRuntime() error {
	stopErrors := make([]error, 0, 2)
	if s.Raft != nil {
		if err := s.Raft.Shutdown().Error(); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("shutdown_raft: %w", err))
		}
		s.Raft = nil
	}
	if s.Transport != nil {
		if err := s.Transport.Close(); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("close_raft_transport: %w", err))
		}
		s.Transport = nil
	}
	s.RaftID = nil
	return errors.Join(stopErrors...)
}

func (s *Service) CreateCluster(ip string, fsm raft.FSM) error {
	if s.Raft != nil {
		return errors.New("raft_already_initialized")
	}
	localNodeID := s.guestIdentityInventoryLocalNodeID()
	if localNodeID == "" {
		return errors.New("local_node_id_unavailable")
	}
	localInventory, err := ScanLocalGuestIdentityInventory(s.DB, localNodeID)
	if err != nil {
		return fmt.Errorf("scan_local_guest_identity_inventory: %w", err)
	}
	if err := requireCleanGuestIdentityInventory(localInventory); err != nil {
		return err
	}
	port := ClusterRaftPort

	if err := network.TryBindToPort(ip, port, "tcp"); err != nil {
		return err
	}

	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err != nil {
		return err
	}

	if c.Enabled {
		return errors.New("cluster already exists")
	}
	if dir, _ := config.GetRaftPath(); hasExistingRaftState(dir) {
		return errors.New("raft_state_already_exists")
	}

	bootstrap := true
	newKey := c.Key
	if newKey == "" {
		newKey = utils.GenerateRandomString(32)
	}

	if _, err := s.setupRaftAtIP(true, fsm, ip); err != nil {
		return err
	}

	becameLeader, leaderAddr, err := s.waitUntilLeader(raftLeaderWaitTimeout)
	if err != nil {
		return fmt.Errorf("bootstrap_leader_election_failed: %w", err)
	}

	if becameLeader {
		if err := s.backfillPreClusterState(); err != nil {
			return err
		}

		if err := s.Raft.Snapshot().Error(); err != nil && !errors.Is(err, raft.ErrNothingNewToSnapshot) {
			return fmt.Errorf("raft_snapshot_failed: %w", err)
		}

	} else {
		return fmt.Errorf("bootstrap_node_not_leader: leader=%s", string(leaderAddr))
	}

	// Persist clustered state only after Raft bootstrap, backfill, and snapshot
	// have succeeded.
	if err := s.DB.Model(&c).Updates(map[string]any{
		"enabled":        true,
		"key":            newKey,
		"raft_bootstrap": &bootstrap,
		"raft_ip":        ip,
		"raft_port":      port,
	}).Error; err != nil {
		return err
	}

	if err := s.EnsureAndPublishLocalSSHIdentity(); err != nil {
		logger.L.Warn().Err(err).Msg("Cluster SSH identity publish deferred during cluster creation")
	}

	if err := s.triggerClusterStart(ip); err != nil {
		logger.L.Error().Err(err).Str("ip", ip).Msg("cluster_listener_start_failed")
	}

	return nil
}

func (s *Service) rollbackJoinPreparation(
	originalCluster clusterModels.Cluster,
	originalNodeID string,
) error {
	rollbackErrors := make([]error, 0, 3)
	if err := s.stopRaftRuntime(); err != nil {
		rollbackErrors = append(rollbackErrors, err)
	}
	s.NodeID = originalNodeID
	if err := s.DB.Save(&originalCluster).Error; err != nil {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("restore_cluster_record: %w", err))
	}
	if err := s.CleanRaftDir(); err != nil {
		rollbackErrors = append(rollbackErrors, err)
	}
	return errors.Join(rollbackErrors...)
}

func (s *Service) StartAsJoiner(fsm raft.FSM, ip, clusterKey string) error {
	if !utils.IsValidIP(ip) {
		return errors.New("invalid_ip_address")
	}

	port := ClusterRaftPort
	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err != nil {
		return err
	}
	if c.Enabled {
		if c.RaftIP == ip &&
			c.RaftPort == port &&
			c.Key == clusterKey &&
			s.Raft != nil &&
			s.Raft.State() != raft.Shutdown {
			if err := s.triggerClusterStart(ip); err != nil {
				return fmt.Errorf("cluster_listener_start_failed: %w", err)
			}
			return nil
		}
		return fmt.Errorf("clustered_already")
	}

	if s.Raft != nil && s.Raft.State() != raft.Shutdown {
		return errors.New("raft_already_initialized")
	}
	if s.Raft != nil || s.Transport != nil {
		if err := s.stopRaftRuntime(); err != nil {
			return fmt.Errorf("failed_to_stop_stale_raft_runtime: %w", err)
		}
	}

	if err := network.TryBindToPort(ip, port, "tcp"); err != nil {
		return fmt.Errorf("failed_to_bind_to_port: %v", err)
	}

	if err := s.CleanRaftDir(); err != nil {
		return err
	}

	originalCluster := c
	originalNodeID := s.NodeID
	if _, err := s.setupRaftAtIP(false, fsm, ip); err != nil {
		if rollbackErr := s.rollbackJoinPreparation(originalCluster, originalNodeID); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("join_preparation_rollback_failed: %w", rollbackErr))
		}
		return err
	}

	c.RaftIP = ip
	c.RaftPort = port
	c.Enabled = true
	c.Key = clusterKey
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&c).Error; err != nil {
			return err
		}
		return clearClusteredDataTx(tx)
	}); err != nil {
		if rollbackErr := s.rollbackJoinPreparation(originalCluster, originalNodeID); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("join_preparation_rollback_failed: %w", rollbackErr))
		}
		return err
	}

	if err := s.EnsureAndPublishLocalSSHIdentity(); err != nil {
		logger.L.Warn().Err(err).Msg("Cluster SSH identity publish deferred during joiner startup")
	}

	if err := s.triggerClusterStart(ip); err != nil {
		return fmt.Errorf("cluster_listener_start_failed: %w", err)
	}

	return nil
}

func clearClusteredDataTx(tx *gorm.DB) error {
	if err := tx.Exec("DELETE FROM cluster_notes").Error; err != nil {
		return fmt.Errorf("failed_to_clean_cluster_notes: %w", err)
	}

	if err := tx.Exec("DELETE FROM cluster_options").Error; err != nil {
		return fmt.Errorf("failed_to_clean_cluster_options: %w", err)
	}

	if err := tx.Exec("DELETE FROM backup_events").Error; err != nil {
		return fmt.Errorf("failed_to_clean_backup_events: %w", err)
	}

	if err := tx.Exec("DELETE FROM backup_jobs").Error; err != nil {
		return fmt.Errorf("failed_to_clean_backup_jobs: %w", err)
	}

	if err := tx.Exec("DELETE FROM backup_targets").Error; err != nil {
		return fmt.Errorf("failed_to_clean_backup_targets: %w", err)
	}

	if err := tx.Exec("DELETE FROM replication_events").Error; err != nil {
		return fmt.Errorf("failed_to_clean_replication_events: %w", err)
	}

	if err := tx.Exec("DELETE FROM replication_guest_operation_receipts").Error; err != nil {
		return fmt.Errorf("failed_to_clean_replication_guest_operation_receipts: %w", err)
	}

	if err := tx.Exec("DELETE FROM replication_guest_operations").Error; err != nil {
		return fmt.Errorf("failed_to_clean_replication_guest_operations: %w", err)
	}

	if err := tx.Exec("DELETE FROM replication_leases").Error; err != nil {
		return fmt.Errorf("failed_to_clean_replication_leases: %w", err)
	}

	if err := tx.Exec("DELETE FROM replication_policy_targets").Error; err != nil {
		return fmt.Errorf("failed_to_clean_replication_policy_targets: %w", err)
	}

	if err := tx.Exec("DELETE FROM replication_policies").Error; err != nil {
		return fmt.Errorf("failed_to_clean_replication_policies: %w", err)
	}

	if err := tx.Exec("DELETE FROM cluster_ssh_identities").Error; err != nil {
		return fmt.Errorf("failed_to_clean_cluster_ssh_identities: %w", err)
	}

	return nil
}

func (s *Service) ClearClusteredData() error {
	return s.DB.Transaction(clearClusteredDataTx)
}

func (s *Service) MarkClustered() error {
	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err != nil {
		return err
	}

	c.Enabled = true
	if err := s.DB.Save(&c).Error; err != nil {
		return err
	}

	return nil
}

func (s *Service) MarkDeclustered() error {
	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err != nil {
		return err
	}

	c.Enabled = false
	c.Key = ""
	c.RaftBootstrap = nil
	c.RaftIP = ""
	c.RaftPort = ClusterRaftPort

	if err := s.DB.Save(&c).Error; err != nil {
		return err
	}

	return nil
}

func (s *Service) ListBackupTargetsForSync() ([]clusterModels.BackupTarget, error) {
	var targets []clusterModels.BackupTarget
	err := s.DB.Order("id ASC").Find(&targets).Error
	return targets, err
}
