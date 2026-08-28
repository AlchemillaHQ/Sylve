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
	"path/filepath"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/network"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

func (s *Service) initRaftStores(dataDir string, rw io.Writer) (raft.LogStore, raft.StableStore, raft.SnapshotStore, error) {
	logStore, err := raftboltdb.NewBoltStore(fmt.Sprintf("%s/raft-log.db", dataDir))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed_to_create_log_store")
	}

	stableStore, err := raftboltdb.NewBoltStore(fmt.Sprintf("%s/raft-stable.db", dataDir))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed_to_create_stable_store")
	}

	snapStore, err := raft.NewFileSnapshotStore(dataDir, 2, rw)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed_to_create_snap_store")
	}

	return logStore, stableStore, snapStore, nil
}

func (s *Service) initRaftTransport(raftIP string) (*raft.NetworkTransport, error) {
	bindAddr := RaftServerAddress(raftIP)
	tcpAddr, err := net.ResolveTCPAddr("tcp", bindAddr)
	if err != nil {
		return nil, fmt.Errorf("Could not resolve address: %s", err)
	}

	t, err := raft.NewTCPTransport(bindAddr, tcpAddr, raftTransportMaxPool, raftTransportTimeout, os.Stdout)
	if err != nil {
		return nil, fmt.Errorf("failed_to_create_transport: %v", err)
	}

	return t, nil
}

func (s *Service) SetupRaft(bootstrap bool, fsm raft.FSM) (*raft.Raft, error) {
	return s.setupRaft(bootstrap, fsm)
}

func (s *Service) setupRaft(bootstrap bool, fsm raft.FSM) (*raft.Raft, error) {
	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err != nil {
		return nil, fmt.Errorf("failed_to_get_cluster_info: %v", err)
	}
	return s.setupRaftAtIP(bootstrap, fsm, c.RaftIP)
}

func (s *Service) setupRaftAtIP(bootstrap bool, fsm raft.FSM, raftIP string) (*raft.Raft, error) {
	if fsm == nil {
		return nil, fmt.Errorf("raft_fsm_required")
	}
	s.raftFSM = fsm
	if dispatcher, ok := fsm.(*clusterModels.FSMDispatcher); ok {
		s.stateFSM = dispatcher
	} else {
		s.stateFSM = nil
	}

	if config.ParsedConfig != nil && config.ParsedConfig.Raft.Reset {
		if err := s.CleanRaftDir(); err != nil {
			return nil, fmt.Errorf("failed_to_clean_raft_dir: %w", err)
		}

		err := config.ResetRaftReset()
		if err != nil {
			return nil, fmt.Errorf("failed_to_reset_raft: %w", err)
		}

		bootstrap = true
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

	raftLog := logger.NewZerologHCLog(logger.L, "raft")
	raftLog.SetLevel(hclog.Error)
	cfg.Logger = raftLog

	dataDir, err := config.GetRaftPath()
	if err != nil {
		return nil, fmt.Errorf("no_raft_path")
	}

	rw := logger.StandardWriterAdapter(logger.L)
	logStore, stableStore, snapStore, err := s.initRaftStores(dataDir, rw)
	if err != nil {
		return nil, err
	}

	t, err := s.initRaftTransport(raftIP)
	if err != nil {
		return nil, err
	}

	raftAddr := raft.ServerAddress(RaftServerAddress(raftIP))
	r, err := raft.NewRaft(cfg, fsm, logStore, stableStore, snapStore, t)
	if err != nil {
		_ = t.Close()
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
			return nil, fmt.Errorf("failed_to_bootstrap_raft: %w", err)
		}
	}

	s.RaftID = &raftAddr
	s.NodeID = detail.NodeID
	s.Transport = t
	s.Raft = r

	return r, nil
}

func hasExistingRaftState(dir string) bool {
	paths := []string{
		filepath.Join(dir, "raft-log.db"),
		filepath.Join(dir, "raft-stable.db"),
		filepath.Join(dir, "snapshots"),
	}

	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			if fi.Mode().IsRegular() || fi.IsDir() {
				return true
			}
		}
	}

	return false
}

func (s *Service) InitRaft(fsm raft.FSM) error {
	if fsm != nil {
		s.raftFSM = fsm
		if dispatcher, ok := fsm.(*clusterModels.FSMDispatcher); ok {
			s.stateFSM = dispatcher
		} else {
			s.stateFSM = nil
		}
	}
	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err != nil {
		return err
	}

	if !c.Enabled {
		logger.L.Info().Msg("We're not clustered; skipping Raft init (join-ready will start Raft on demand).")
		return nil
	}

	raftDir, _ := config.GetRaftPath()
	if hasExistingRaftState(raftDir) {
		logger.L.Info().Msg("Found existing Raft state; starting Raft (non-bootstrap restore).")
		_, err := s.setupRaft(false, fsm)
		if err != nil {
			return err
		}
		return nil
	}

	bootstrap := c.RaftBootstrap != nil && *c.RaftBootstrap
	if bootstrap {
		logger.L.Info().Msg("Starting Raft as bootstrap node (first cluster node).")
	} else {
		logger.L.Info().Msg("Starting Raft in non-bootstrap mode (clustered follower).")
	}

	_, err := s.setupRaft(bootstrap, fsm)
	if err != nil {
		return err
	}

	return nil
}

func (s *Service) ClearClusterNode(id string) error {
	return s.DB.Exec("DELETE FROM cluster_nodes WHERE node_uuid = ?", id).Error
}

func (s *Service) CleanRaftDir() error {
	raftDir, err := config.GetRaftPath()
	if err != nil {
		return fmt.Errorf("failed_to_get_raft_dir: %w", err)
	}
	raftDir = filepath.Clean(strings.TrimSpace(raftDir))
	dataPath, err := config.GetDataPath()
	if err != nil {
		return fmt.Errorf("failed_to_get_data_dir: %w", err)
	}
	expected := filepath.Join(filepath.Clean(dataPath), "raft")
	if raftDir == "." || raftDir == string(filepath.Separator) || raftDir != expected {
		return fmt.Errorf("invalid_raft_dir")
	}
	info, err := os.Stat(raftDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed_to_stat_raft_dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("raft_path_not_directory")
	}
	if err := utils.RemoveDirContents(raftDir); err != nil {
		return fmt.Errorf("failed_to_clean_raft_dir: %w", err)
	}
	return nil
}
