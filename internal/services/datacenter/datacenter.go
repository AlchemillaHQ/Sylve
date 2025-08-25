package datacenter

import (
	"errors"

	datacenterModels "github.com/alchemillahq/sylve/internal/db/models/datacenter"
	datacenterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/datacenter"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

var _ datacenterServiceInterfaces.DatacenterServiceInterface = (*Service)(nil)

type Service struct {
	DB   *gorm.DB
	Raft *raft.Raft
}

func NewDatacenterService(db *gorm.DB) datacenterServiceInterfaces.DatacenterServiceInterface {
	return &Service{
		DB: db,
	}
}

func (s *Service) CreateCluster(fsm raft.FSM) (*datacenterModels.DataCenter, error) {
	var dc datacenterModels.DataCenter
	err := s.DB.First(&dc).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		bootstrap := true
		dc = datacenterModels.DataCenter{
			RaftBootstrap: &bootstrap,
			ClusterKey:    utils.GenerateRandomString(32),
		}
		if err := s.DB.Create(&dc).Error; err != nil {
			return nil, err
		}

		_, err := s.SetupRaft(true, fsm)
		if err != nil {
			return nil, err
		}

		return &dc, nil
	}

	if err != nil {
		return nil, err
	}

	if dc.RaftBootstrap != nil && *dc.RaftBootstrap {
		return nil, errors.New("cluster already exists")
	}

	return nil, errors.New("invalid cluster state")
}

func (s *Service) CanJoinCluster() error {
	var dc datacenterModels.DataCenter
	err := s.DB.First(&dc).Error

	if err == nil {
		return errors.New("node_already_part_of_cluster")
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	if s.Raft != nil {
		return errors.New("raft_already_initialized")
	}

	return nil
}

func (s *Service) StartAsJoiner(fsm raft.FSM) error {
	if err := s.CanJoinCluster(); err != nil {
		return err
	}

	_, err := s.SetupRaft(false, fsm)
	return err
}

func (s *Service) AcceptJoin(nodeID, nodeAddr, providedKey string) error {
	dc, err := s.GetCluster()
	if err != nil {
		return err
	}

	if dc.ClusterKey != providedKey {
		return errors.New("invalid cluster key")
	}

	if s.Raft == nil {
		return errors.New("raft not initialized on leader")
	}

	future := s.Raft.AddVoter(
		raft.ServerID(nodeID),
		raft.ServerAddress(nodeAddr),
		0, 0,
	)
	return future.Error()
}

func (s *Service) GetCluster() (*datacenterModels.DataCenter, error) {
	var dc datacenterModels.DataCenter
	if err := s.DB.First(&dc).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("cluster_not_found")
		}

		return nil, err
	}

	return &dc, nil
}
