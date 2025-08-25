package datacenter

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	datacenterModels "github.com/alchemillahq/sylve/internal/db/models/datacenter"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb"
	"gorm.io/gorm"
)

func (s *Service) SetupRaft(bootstrap bool, fsm raft.FSM) (*raft.Raft, error) {
	nodeId, err := utils.GetSystemUUID()
	if err != nil {
		return nil, fmt.Errorf("no_node_id")
	}

	cfg := raft.DefaultConfig()
	cfg.LocalID = raft.ServerID(nodeId)
	cfg.SnapshotInterval = 20 * time.Second
	cfg.SnapshotThreshold = 2

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

	snapStore, err := raft.NewFileSnapshotStore(dataDir, 2, os.Stdout)
	if err != nil {
		return nil, fmt.Errorf("failed_to_create_snap_store")
	}

	bindAddr := fmt.Sprintf("%s:%d", config.ParsedConfig.Raft.Address, config.ParsedConfig.Raft.Port)
	transport, err := raft.NewTCPTransport(bindAddr, nil, 3, 10*time.Second, os.Stdout)
	if err != nil {
		return nil, fmt.Errorf("failed_to_create_transport: %v", err)
	}

	r, err := raft.NewRaft(cfg, fsm, logStore, stableStore, snapStore, transport)
	if err != nil {
		return nil, fmt.Errorf("failed_to_create_raft: %v", err)
	}

	if bootstrap {
		cfg := raft.Configuration{
			Servers: []raft.Server{{
				ID:      raft.ServerID(nodeId),
				Address: transport.LocalAddr(),
			}},
		}
		r.BootstrapCluster(cfg)
	}

	s.Raft = r
	return r, nil
}

func (s *Service) InitRaft(fsm raft.FSM) error {
	var dc datacenterModels.DataCenter
	err := s.DB.First(&dc).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		logger.L.Debug().Msg("No cluster found")
		return nil
	}

	if err != nil {
		return err
	}

	bootstrap := dc.RaftBootstrap != nil && *dc.RaftBootstrap
	r, err := s.SetupRaft(bootstrap, fsm)
	if err != nil {
		return err
	}

	s.Raft = r

	if dc.RaftBootstrap == nil {
		logger.L.Info().Msg("Cluster record exists but RaftBootstrap is NULL (invalid state)")
	} else if bootstrap {
		logger.L.Info().Msg("Cluster initialized: bootstrapping node")
	} else {
		logger.L.Info().Msg("Cluster initialized: follower node")
	}

	return nil
}
