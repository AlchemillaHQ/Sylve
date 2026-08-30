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
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
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

	clusterJoinMu         sync.Mutex
	membershipLifecycleMu sync.Mutex
	leaveInitiationMu     sync.Mutex
	backupJobRebindMu     sync.Mutex
	replicatedStateMu     sync.RWMutex
	mutationGate          *MutationGate
	addressProvider       *raftAddressProvider

	raftFSM            raft.FSM
	stateFSM           *clusterModels.FSMDispatcher
	stateRepair        atomic.Bool
	stateDigestForNode func(
		context.Context,
		string,
		raft.ServerAddress,
		uint64,
	) (ReplicatedStateDigest, error)
	stateRepairForNode func(
		context.Context,
		string,
		raft.ServerAddress,
		ReplicatedStateRepairRequest,
	) error

	peerProbeMu            sync.Mutex
	peerProbeFailureStreak map[string]int

	embeddedSSHOnce   sync.Once
	monitorOnce       sync.Once
	reconcilerOnce    sync.Once
	readdressOnce     sync.Once
	joinComplete      atomic.Bool
	leaveComplete     atomic.Bool
	readdressRestart  atomic.Bool
	joinCompleteHook  func()
	leaveCompleteHook func()
	readdressHook     func()

	clusterStartHook func(ip string) error

	guestIdentityInventoryAPIForNode func(string, raft.ServerAddress) (string, error)
	joinVersionForNode               func(context.Context, raft.Server, string) (string, error)
	joinProgressForNode              func(context.Context, string, raft.ServerAddress, uint64) (ClusterJoinProgress, error)
	backupJobValidationAPIForNode    func(string, raft.ServerAddress) (string, error)
	backupTargetValidationAPIForNode func(string, raft.ServerAddress) (string, error)
	leaveMembershipForNode           func(context.Context, clusterModels.Cluster, string) (MembershipStatus, error)
	leaveRemovalForNode              func(context.Context, string, RemoveMembershipRequest) error
	backupTargetValidator            func(context.Context, *clusterModels.BackupTarget) error
	backupJobIDGenerator             func() (uint, error)
	raftMembershipForNode            func(string) (RaftMembership, error)
	readdressIdentityForNode         func(context.Context, string, raft.ServerAddress) (ReaddressIdentity, error)
}

func (s *Service) SetClusterStartHook(fn func(ip string) error) {
	s.clusterStartHook = fn
}

func (s *Service) SetJoinCompleteHook(fn func()) {
	s.joinCompleteHook = fn
}

func (s *Service) SetLeaveCompleteHook(fn func()) {
	s.leaveCompleteHook = fn
}

func (s *Service) SetReaddressRestartHook(fn func()) {
	s.readdressHook = fn
}

func (s *Service) notifyLeaveComplete() {
	if s == nil || s.leaveComplete.Load() {
		return
	}
	hook := s.leaveCompleteHook
	if hook == nil || !s.leaveComplete.CompareAndSwap(false, true) {
		return
	}
	go hook()
}

func (s *Service) notifyJoinComplete() {
	if s == nil || s.joinComplete.Load() {
		return
	}
	hook := s.joinCompleteHook
	if hook == nil || !s.joinComplete.CompareAndSwap(false, true) {
		return
	}
	go hook()
}

func (s *Service) notifyReaddressRestart() {
	if s == nil || s.readdressRestart.Load() {
		return
	}
	hook := s.readdressHook
	if hook == nil || !s.readdressRestart.CompareAndSwap(false, true) {
		return
	}
	go hook()
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
		DB:              db,
		Raft:            nil,
		RaftID:          nil,
		NodeID:          "",
		AuthService:     authService,
		JailService:     jailService,
		mutationGate:    NewMutationGate(),
		addressProvider: newRaftAddressProvider(),

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

	guestIDsByNode := make(map[string][]uint, len(conf.Servers))
	for _, srv := range conf.Servers {
		id := string(srv.ID)
		var node clusterModels.ClusterNode
		err := s.DB.Select("guest_ids").Where("node_uuid = ?", id).First(&node).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("failed to query guest ids for node %s: %w", id, err)
		}
		guestIDsByNode[id] = node.GuestIDs
	}
	out.Nodes = clusterRaftNodeDetails(conf, leaderAddr, leaderID, guestIDsByNode)

	return out, nil
}

func clusterRaftNodeDetails(
	configuration raft.Configuration,
	leaderAddress raft.ServerAddress,
	leaderID raft.ServerID,
	guestIDsByNode map[string][]uint,
) []clusterServiceInterfaces.RaftNode {
	nodes := make([]clusterServiceInterfaces.RaftNode, 0, len(configuration.Servers))
	for _, server := range configuration.Servers {
		id := string(server.ID)
		address := string(server.Address)
		suffrage := "unknown"
		switch server.Suffrage {
		case raft.Voter:
			suffrage = "voter"
		case raft.Nonvoter:
			suffrage = "nonvoter"
		case raft.Staging:
			suffrage = "staging"
		}
		nodes = append(nodes, clusterServiceInterfaces.RaftNode{
			ID:       id,
			Address:  address,
			Suffrage: suffrage,
			IsLeader: id == string(leaderID) || address == string(leaderAddress),
			GuestIDs: guestIDsByNode[id],
		})
	}
	return nodes
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

func (s *Service) snapshotPreClusterState() error {
	// The first node already owns the authoritative standalone database. A
	// single replicated checkpoint gives Raft a snapshot index without
	// maintaining a second row-by-row definition of replicated state.
	s.replicatedStateMu.Lock()
	defer s.replicatedStateMu.Unlock()
	return s.checkpointAndSnapshotLocked()
}

func (s *Service) ResyncClusterState() error {
	_, err := s.ResyncClusterStateWithResult(context.Background())
	return err
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
	var err error
	ip, err = normalizeClusterIPv4(ip, "invalid_ip_address")
	if err != nil {
		return err
	}
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
		if err := s.snapshotPreClusterState(); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("bootstrap_node_not_leader: leader=%s", string(leaderAddr))
	}

	// Persist clustered state only after Raft bootstrap, backfill, and snapshot
	// have succeeded.
	if err := s.DB.Model(&c).Updates(map[string]any{
		"enabled":           true,
		"key":               newKey,
		"raft_bootstrap":    &bootstrap,
		"raft_ip":           ip,
		"raft_port":         port,
		"join_leader_ip":    "",
		"join_node_id":      "",
		"join_node_ip":      "",
		"join_node_version": "",
		"join_inventory":    nil,
		"join_phase":        "",
		"join_last_error":   "",
		"join_attempts":     0,
	}).Error; err != nil {
		return err
	}
	s.joinComplete.Store(true)

	if err := s.EnsureAndPublishLocalSSHIdentity(); err != nil {
		logger.L.Warn().Err(err).Msg("Cluster SSH identity publish deferred during cluster creation")
	}

	if err := s.triggerClusterStart(ip); err != nil {
		logger.L.Error().Err(err).Str("ip", ip).Msg("cluster_listener_start_failed")
	}

	if err := s.PopulateClusterNodes(); err != nil {
		logger.L.Warn().Err(err).Msg("cluster_node_population_deferred_after_create")
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
	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()

	var err error
	ip, err = normalizeClusterIPv4(ip, "invalid_ip_address")
	if err != nil {
		return err
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
	return clusterModels.ClearReplicatedStateTx(tx)
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
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		return markDeclusteredTx(tx)
	})
	if err == nil {
		s.joinComplete.Store(false)
	}
	return err
}

func markDeclusteredTx(tx *gorm.DB) error {
	var c clusterModels.Cluster
	if err := tx.First(&c).Error; err != nil {
		return err
	}
	c.Enabled = false
	c.Key = ""
	c.RaftBootstrap = nil
	c.RaftIP = ""
	c.RaftPort = ClusterRaftPort
	c.JoinLeaderIP = ""
	c.JoinNodeID = ""
	c.JoinNodeIP = ""
	c.JoinNodeVersion = ""
	c.JoinInventory = nil
	c.JoinPhase = ""
	c.JoinLastError = ""
	c.JoinAttempts = 0
	c.LeaveID = ""
	c.LeavePhase = ""
	c.LeaveLeaderIP = ""
	c.LeavePeerAddrs = nil
	c.LeaveLastError = ""
	c.LeaveAttempts = 0
	c.ReaddressOldIP = ""
	c.ReaddressNewIP = ""
	c.ReaddressPhase = ""
	c.ReaddressLastError = ""
	return tx.Save(&c).Error
}

func (s *Service) ListBackupTargetsForSync() ([]clusterModels.BackupTarget, error) {
	var targets []clusterModels.BackupTarget
	err := s.DB.Order("id ASC").Find(&targets).Error
	return targets, err
}
