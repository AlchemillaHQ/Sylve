// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/network"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

func (s *Service) SetupRaft(bootstrap bool, fsm raft.FSM) (*raft.Raft, error) {
	if config.ParsedConfig.Raft.Reset {
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

	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err != nil {
		return nil, fmt.Errorf("failed_to_get_cluster_info: %v", err)
	}

	err := network.TryBindToPort(c.RaftIP, c.RaftPort, "tcp")
	if err != nil {
		return nil, fmt.Errorf("failed_to_bind_raft_port: %v", err)
	}

	cfg := raft.DefaultConfig()
	cfg.LocalID = raft.ServerID(detail.NodeID)

	raftLog := logger.NewZerologHCLog(logger.L, "raft")
	raftLog.SetLevel(hclog.Error)
	cfg.Logger = raftLog

	rw := logger.StandardWriterAdapter(logger.L)

	dataDir, err := config.GetRaftPath()
	if err != nil {
		return nil, fmt.Errorf("no_raft_path")
	}

	logStore, err := raftboltdb.NewBoltStore(fmt.Sprintf("%s/raft-log.db", dataDir))
	if err != nil {
		return nil, fmt.Errorf("failed_to_create_log_store")
	}

	stableStore, err := raftboltdb.NewBoltStore(fmt.Sprintf("%s/raft-stable.db", dataDir))
	if err != nil {
		return nil, fmt.Errorf("failed_to_create_stable_store")
	}

	snapStore, err := raft.NewFileSnapshotStore(dataDir, 2, rw)
	if err != nil {
		return nil, fmt.Errorf("failed_to_create_snap_store")
	}

	bindAddr := fmt.Sprintf("%s:%d", c.RaftIP, c.RaftPort)
	tcpAddr, err := net.ResolveTCPAddr("tcp", bindAddr)
	if err != nil {
		return nil, fmt.Errorf("Could not resolve address: %s", err)
	}

	t, err := raft.NewTCPTransport(bindAddr, tcpAddr, 3, 10*time.Second, os.Stdout)

	if err != nil {
		return nil, fmt.Errorf("failed_to_create_transport: %v", err)
	}

	s.Transport = t

	r, err := raft.NewRaft(cfg, fsm, logStore, stableStore, snapStore, s.Transport)
	if err != nil {
		return nil, fmt.Errorf("failed_to_create_raft: %v", err)
	}

	if bootstrap {
		cfg := raft.Configuration{
			Servers: []raft.Server{{
				ID:      raft.ServerID(detail.NodeID),
				Address: s.Transport.LocalAddr(),
			}},
		}
		r.BootstrapCluster(cfg)
	}

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
		_, err := s.SetupRaft(false, fsm)
		return err
	}

	bootstrap := c.RaftBootstrap != nil && *c.RaftBootstrap
	if bootstrap {
		logger.L.Info().Msg("Starting Raft as bootstrap node (first cluster node).")
	} else {
		logger.L.Info().Msg("Starting Raft in non-bootstrap mode (clustered follower).")
	}

	_, err := s.SetupRaft(bootstrap, fsm)

	return err
}

func (s *Service) RemovePeer(id raft.ServerID) error {
	if s.Raft.State() != raft.Leader {
		return fmt.Errorf("not_leader")
	}

	fut := s.Raft.RemoveServer(id, 0, 8000)

	if fut.Error() != nil {
		return fmt.Errorf("failed_to_remove_peer: %v", fut.Error())
	}

	return nil
}

func (s *Service) ClearClusterNode(id string) error {
	return s.DB.Exec("DELETE FROM cluster_nodes WHERE node_uuid = ?", id).Error
}

func (s *Service) ResetRaftNode() error {
	if s.Raft.State() != raft.Leader {
		nodeId := s.Detail().NodeID

		leaderAddr := s.Raft.Leader()

		if leaderAddr == "" {
			return fmt.Errorf("no_leader_found_manual_reset_required")
		}

		host, _, err := net.SplitHostPort(string(leaderAddr))
		if err != nil {
			return fmt.Errorf("failed_to_split_leader_address: %v", err)
		}

		hostname, err := utils.GetSystemHostname()
		if err != nil {
			return fmt.Errorf("failed_to_get_system_hostname: %v", err)
		}

		clusterToken, err := s.getClusterToken(hostname)
		if err != nil {
			return fmt.Errorf("failed_to_get_cluster_token: %v", err)
		}

		payload := map[string]interface{}{
			"nodeId": nodeId,
		}

		headers := map[string]string{
			"Accept":          "application/json",
			"Content-Type":    "application/json",
			"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
		}

		err = utils.HTTPPostJSON(
			fmt.Sprintf("https://%s:%d/api/cluster/remove-peer", host, config.ParsedConfig.Port), payload, headers)

		if err != nil {
			return fmt.Errorf("failed_to_remove_peer_from_leader: %v", err)
		}
	}

	if r := s.Raft; r != nil {
		_ = r.Shutdown().Error()
		s.Raft = nil
	}

	if t := s.Transport; t != nil {
		_ = t.Close()
		s.Transport = nil
	}

	if err := s.MarkDeclustered(); err != nil {
		return err
	}

	if err := s.DB.Exec("DELETE FROM cluster_nodes").Error; err != nil {
		return fmt.Errorf("failed_to_clean_cluster_nodes: %v", err)
	}

	return s.CleanRaftDir()
}

func (s *Service) CleanRaftDir() error {
	raftDir, _ := config.GetRaftPath()
	err := utils.RemoveDirContents(raftDir)

	if err != nil {
		return fmt.Errorf("failed_to_clean_raft_dir: %w", err)
	}

	return nil
}
