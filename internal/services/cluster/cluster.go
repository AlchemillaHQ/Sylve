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
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
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
	Transport   *raft.NetworkTransport
	AuthService serviceInterfaces.AuthServiceInterface
}

func NewClusterService(db *gorm.DB, authService serviceInterfaces.AuthServiceInterface) clusterServiceInterfaces.ClusterServiceInterface {
	return &Service{
		DB:          db,
		Raft:        nil,
		AuthService: authService,
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

		out.Nodes = append(out.Nodes, clusterServiceInterfaces.RaftNode{
			ID:       id,
			Address:  addr,
			Suffrage: suffrageStr(srv.Suffrage),
			IsLeader: id == string(leaderID) || addr == string(leaderAddr),
		})
	}

	return out, nil
}

func (s *Service) waitUntilLeader(timeout time.Duration) (bool, raft.ServerAddress, error) {
	deadline := time.Now().Add(timeout)

	if s.Raft.State() == raft.Leader {
		return true, s.Raft.Leader(), nil
	}
	if addr := s.Raft.Leader(); addr != "" {
		return false, addr, nil
	}

	for time.Now().Before(deadline) {
		if s.Raft.State() == raft.Leader {
			return true, s.Raft.Leader(), nil
		}
		if addr := s.Raft.Leader(); addr != "" {
			return false, addr, nil
		}
		time.Sleep(50 * time.Millisecond)
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

			break
		}
	}

	{
		var targets []clusterModels.BackupTarget
		if err := s.DB.Order("id ASC").Find(&targets).Error; err != nil {
			return fmt.Errorf("scan_existing_backup_targets: %w", err)
		}

		for _, t := range targets {
			payloadStruct := struct {
				ID          uint   `json:"id"`
				Name        string `json:"name"`
				Endpoint    string `json:"endpoint"`
				Description string `json:"description"`
				Enabled     bool   `json:"enabled"`
			}{
				ID:          t.ID,
				Name:        t.Name,
				Endpoint:    t.Endpoint,
				Description: t.Description,
				Enabled:     t.Enabled,
			}

			data, _ := json.Marshal(payloadStruct)
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
				ID                 uint       `json:"id"`
				Name               string     `json:"name"`
				TargetID           uint       `json:"targetId"`
				RunnerNodeID       string     `json:"runnerNodeId"`
				Mode               string     `json:"mode"`
				SourceDataset      string     `json:"sourceDataset"`
				JailRootDataset    string     `json:"jailRootDataset"`
				DestinationDataset string     `json:"destinationDataset"`
				CronExpr           string     `json:"cronExpr"`
				Force              bool       `json:"force"`
				WithIntermediates  bool       `json:"withIntermediates"`
				Enabled            bool       `json:"enabled"`
				NextRunAt          *time.Time `json:"nextRunAt"`
			}{
				ID:                 j.ID,
				Name:               j.Name,
				TargetID:           j.TargetID,
				RunnerNodeID:       j.RunnerNodeID,
				Mode:               j.Mode,
				SourceDataset:      j.SourceDataset,
				JailRootDataset:    j.JailRootDataset,
				DestinationDataset: j.DestinationDataset,
				CronExpr:           j.CronExpr,
				Force:              j.Force,
				WithIntermediates:  j.WithIntermediates,
				Enabled:            j.Enabled,
				NextRunAt:          j.NextRunAt,
			}

			data, _ := json.Marshal(payloadStruct)
			cmd := clusterModels.Command{Type: "backup_job", Action: "create", Data: data}
			if err := s.Raft.Apply(utils.MustJSON(cmd), 5*time.Second).Error(); err != nil {
				return fmt.Errorf("apply_synth_create_backup_job id=%d: %w", j.ID, err)
			}
		}
	}

	if err := s.Raft.Barrier(10 * time.Second).Error(); err != nil {
		return fmt.Errorf("barrier_after_backfill: %w", err)
	}

	return nil
}

func (s *Service) CreateCluster(ip string, port int, fsm raft.FSM) error {
	if s.Raft != nil {
		return errors.New("raft_already_initialized")
	}

	if err := network.TryBindToPort(ip, port, "tcp"); err != nil {
		return err
	}

	if dir, _ := config.GetRaftPath(); hasExistingRaftState(dir) {
		return errors.New("raft_state_already_exists")
	}

	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err != nil {
		return err
	}

	if c.Enabled {
		return errors.New("cluster already exists")
	}

	bootstrap := true
	newKey := c.Key
	if newKey == "" {
		newKey = utils.GenerateRandomString(32)
	}

	if err := s.DB.Model(&c).Updates(map[string]any{
		"enabled":        true,
		"key":            newKey,
		"raft_bootstrap": &bootstrap,
		"raft_ip":        ip,
		"raft_port":      port,
	}).Error; err != nil {
		return err
	}

	if _, err := s.SetupRaft(true, fsm); err != nil {
		return err
	}

	c.Enabled = true
	c.Key = newKey
	c.RaftBootstrap = &bootstrap
	c.RaftIP = ip
	c.RaftPort = port

	becameLeader, leaderAddr, err := s.waitUntilLeader(10 * time.Second)
	if err != nil {
		logger.L.Warn().Err(err).Msg("Leader not elected yet; skipping immediate snapshot")
		return nil
	}

	if becameLeader {
		if err := s.backfillPreClusterState(); err != nil {
			return err
		}

		if err := s.Raft.Snapshot().Error(); err != nil && !errors.Is(err, raft.ErrNothingNewToSnapshot) {
			return fmt.Errorf("raft_snapshot_failed: %w", err)
		}
	} else {
		logger.L.Info().Str("leader", string(leaderAddr)).Msg("not leader after bootstrap; skipping local snapshot")
	}

	return nil
}

func (s *Service) StartAsJoiner(fsm raft.FSM, ip string, port int, clusterKey string) error {
	if !utils.IsValidIP(ip) {
		return errors.New("invalid_ip_address")
	}

	if !utils.IsValidPort(port) {
		return errors.New("invalid_port_number")
	}

	if err := network.TryBindToPort(ip, port, "tcp"); err != nil {
		return fmt.Errorf("failed_to_bind_to_port: %v", err)
	}

	details, err := s.GetClusterDetails()
	if err != nil {
		return err
	}

	if details.Cluster.Enabled {
		return fmt.Errorf("clustered_already")
	}

	if s.Raft != nil && s.Raft.State() != raft.Shutdown {
		return errors.New("raft_already_initialized")
	}

	err = s.CleanRaftDir()
	if err != nil {
		return err
	}

	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err != nil {
		return err
	}

	c.RaftIP = ip
	c.RaftPort = port
	c.Enabled = true
	c.Key = clusterKey

	if err := s.DB.Save(&c).Error; err != nil {
		return err
	}

	if err := s.DB.Exec("DELETE FROM cluster_notes").Error; err != nil {
		return err
	}

	if err := s.DB.Exec("DELETE FROM cluster_options").Error; err != nil {
		return err
	}

	_, err = s.SetupRaft(false, fsm)
	if err != nil {
		c.RaftIP = ""
		c.RaftPort = 0
		c.Enabled = false
		c.Key = ""

		if err := s.DB.Save(&c).Error; err != nil {
			return err
		}

		return err
	}

	return nil
}

func (s *Service) AcceptJoin(nodeID, nodeIp string, nodePort int, providedKey string) error {
	details, err := s.GetClusterDetails()
	if err != nil {
		return err
	}

	if details.Cluster == nil {
		return errors.New("cluster_not_found")
	}

	if details.Cluster.Key != providedKey {
		return errors.New("invalid_cluster_key")
	}

	if s.Raft == nil {
		return errors.New("raft_not_initialized")
	}

	if s.Raft.State() != raft.Leader {
		addr, id := s.Raft.LeaderWithID()
		return fmt.Errorf("not_leader; leader_addr=%s; leader_id=%s", string(addr), string(id))
	}

	fut := s.Raft.GetConfiguration()
	if err := fut.Error(); err != nil {
		return fmt.Errorf("get_config_failed: %w", err)
	}

	conf := fut.Configuration()
	sid := raft.ServerID(nodeID)
	saddr := raft.ServerAddress(fmt.Sprintf("%s:%d", nodeIp, nodePort))

	for _, srv := range conf.Servers {
		if srv.ID == sid {
			if srv.Address == saddr && srv.Suffrage == raft.Voter {
				return nil
			}

			rf := s.Raft.RemoveServer(srv.ID, 0, 0)
			if err := rf.Error(); err != nil {
				return fmt.Errorf("remove_existing_failed: %w", err)
			}
			break
		}
		if srv.Address == saddr && srv.ID != sid {
			rf := s.Raft.RemoveServer(srv.ID, 0, 0)
			if err := rf.Error(); err != nil {
				return fmt.Errorf("remove_conflicting_addr_failed: %w", err)
			}
		}
	}

	af := s.Raft.AddVoter(sid, saddr, 0, 0)
	if err := af.Error(); err != nil {
		return fmt.Errorf("add_voter_failed: %w", err)
	}

	return nil
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
	c.RaftPort = 0

	if err := s.DB.Save(&c).Error; err != nil {
		return err
	}

	return nil
}
