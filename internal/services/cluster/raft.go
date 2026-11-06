// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/cluster/raftdir"
	"github.com/alchemillahq/sylve/pkg/network"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

func (s *Service) initRaftTransport(raftIP string) (*raft.NetworkTransport, error) {
	if s.clusterCert == nil {
		return nil, fmt.Errorf("cluster_cert_unset")
	}
	listener, err := net.Listen("tcp", RaftServerAddress(raftIP))
	if err != nil {
		return nil, fmt.Errorf("failed_to_create_transport: %v", err)
	}
	if s.addressProvider == nil {
		s.addressProvider = newRaftAddressProvider()
	}
	return raft.NewNetworkTransportWithConfig(&raft.NetworkTransportConfig{
		Stream:                s.newRaftStream(listener),
		ServerAddressProvider: s.addressProvider,
		MaxPool:               raftTransportMaxPool,
		Timeout:               raftTransportTimeout,
		Logger: hclog.New(&hclog.LoggerOptions{
			Name:   "raft-net",
			Output: os.Stdout,
			Level:  hclog.Error,
		}),
	}), nil
}

func (s *Service) setupRaft(bootstrap bool) (*raft.Raft, error) {
	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err != nil {
		return nil, fmt.Errorf("failed_to_get_cluster_info: %v", err)
	}
	return s.setupRaftAtIP(bootstrap, c.RaftIP)
}

func (s *Service) setupRaftAtIP(bootstrap bool, raftIP string) (*raft.Raft, error) {
	var err error
	raftIP, err = normalizeClusterIPv4(raftIP, "invalid_ip_address")
	if err != nil {
		return nil, err
	}

	detail := s.Detail()
	if detail == nil {
		return nil, fmt.Errorf("unable_to_get_node_detail")
	}

	if err := network.TryBindToPort(raftIP, ClusterRaftPort, "tcp"); err != nil {
		return nil, fmt.Errorf("failed_to_bind_raft_port: %v", err)
	}

	cfg := raft.DefaultConfig()
	cfg.LocalID = raft.ServerID(detail.NodeID)
	cfg.SnapshotThreshold = raftSnapshotThreshold
	// A database that records its applied index already holds everything up to
	// it; restoring the last snapshot on start would rewind it until a leader
	// replays the rest. Nodes upgraded from before the index existed restore
	// once, then record it.
	persisted, err := s.fsm.HasPersistedAppliedIndex()
	if err != nil {
		return nil, fmt.Errorf("raft_fsm_state_unavailable: %w", err)
	}
	cfg.NoSnapshotRestoreOnStart = persisted

	raftLog := logger.NewZerologHCLog(logger.L, "raft")
	raftLog.SetLevel(hclog.Error)
	cfg.Logger = raftLog

	dataDir, err := config.GetRaftPath()
	if err != nil {
		return nil, fmt.Errorf("no_raft_path")
	}

	rw := logger.StandardWriterAdapter(logger.L)
	logStore, stableStore, snapStore, err := raftdir.OpenStores(dataDir, rw)
	if err != nil {
		return nil, err
	}
	closeStores := func() {
		_ = logStore.Close()
		_ = stableStore.Close()
	}
	if err := raftdir.WriteFormat(dataDir); err != nil {
		closeStores()
		return nil, fmt.Errorf("raft_format_write_failed: %w", err)
	}

	t, err := s.initRaftTransport(raftIP)
	if err != nil {
		closeStores()
		return nil, err
	}

	raftAddr := raft.ServerAddress(RaftServerAddress(raftIP))
	r, err := raft.NewRaft(cfg, s.fsm, logStore, stableStore, snapStore, t)
	if err != nil {
		_ = t.Close()
		closeStores()
		return nil, fmt.Errorf("failed_to_create_raft: %v", err)
	}

	if bootstrap {
		bootstrapConfig := raft.Configuration{
			Servers: []raft.Server{{
				ID:      raft.ServerID(detail.NodeID),
				Address: t.LocalAddr(),
			}},
		}
		if err := r.BootstrapCluster(bootstrapConfig).Error(); err != nil {
			_ = r.Shutdown().Error()
			_ = t.Close()
			closeStores()
			return nil, fmt.Errorf("failed_to_bootstrap_raft: %w", err)
		}
	}

	s.RaftID = &raftAddr
	s.Transport = t
	s.raftStores = []io.Closer{logStore, stableStore}
	s.SetRaft(r)

	return r, nil
}

func (s *Service) InitRaft() error {
	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err != nil {
		return err
	}

	reset := config.ParsedConfig != nil && config.ParsedConfig.Raft.Reset
	if reset {
		if err := config.ResetRaftReset(); err != nil {
			return fmt.Errorf("failed_to_reset_raft: %w", err)
		}
	}

	raftDir, err := config.GetRaftPath()
	if err != nil {
		return fmt.Errorf("no_raft_path")
	}
	// A rebuild this node hasn't finished goes on, whatever the cluster row
	// says: a rejoining node isn't clustered until it has joined.
	if !reset {
		if leaveRaft, err := s.resumeRebuild(raftDir); err != nil || leaveRaft {
			return err
		}
	}

	if !c.Enabled {
		if reset {
			logger.L.Warn().Msg("Ignoring raft.reset: this node isn't clustered.")
		}
		logger.L.Info().Msg("We're not clustered; skipping Raft init (join-ready will start Raft on demand).")
		return nil
	}
	if strings.TrimSpace(c.LeavePhase) == LeavePhaseCleaning {
		logger.L.Warn().Msg("Skipping Raft init: leave cleanup is still pending.")
		return nil
	}

	if reset {
		if err := s.CleanRaftDir(); err != nil {
			return fmt.Errorf("failed_to_clean_raft_dir: %w", err)
		}
		logger.L.Warn().Msg("raft.reset set; bootstrapping a single-node cluster from local state.")
		if _, err := s.setupRaft(true); err != nil {
			return err
		}
		if _, _, err := s.waitUntilLeader(raftLeaderWaitTimeout); err != nil {
			return err
		}
		// Snapshot right away so restarts restore this state instead of
		// replaying the new log onto a database that already holds it.
		return s.snapshotPreClusterState()
	}

	if raftdir.HasState(raftDir) {
		format, err := raftdir.ReadFormat(raftDir)
		if err != nil {
			return fmt.Errorf("raft_format_unreadable: %w", err)
		}
		if format > raftdir.Format {
			return fmt.Errorf("raft_format_newer: this build reads format %d, the Raft state is format %d", raftdir.Format, format)
		}
		if format < raftdir.Format {
			state, err := raftdir.NewRebuild(raftDir, s.LocalNodeID(), c.Key)
			if err != nil {
				return fmt.Errorf("cluster_rebuild_failed: %w", err)
			}
			s.startRebuild(&rebuild{dir: raftDir, state: *state})
			return nil
		}
	}
	if !raftdir.HasState(raftDir) && c.RaftBootstrap != nil && *c.RaftBootstrap {
		// Bootstrapping again would start a second cluster beside the one the
		// other members still run.
		return fmt.Errorf("raft_state_missing: restore %s or reset this node", raftDir)
	}

	_, err = s.setupRaft(false)
	return err
}

func (s *Service) ClearClusterNode(id string) error {
	return s.DB.Exec("DELETE FROM cluster_nodes WHERE node_uuid = ?", id).Error
}

func (s *Service) CleanRaftDir() error {
	raftDir, err := config.GetRaftPath()
	if err != nil {
		return fmt.Errorf("failed_to_get_raft_dir: %w", err)
	}
	if err := utils.RemoveDirContents(raftDir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed_to_clean_raft_dir: %w", err)
	}
	// The next log starts again at index 1; a recorded index from the old one
	// would make this node skip it.
	return s.fsm.ResetAppliedIndex()
}
