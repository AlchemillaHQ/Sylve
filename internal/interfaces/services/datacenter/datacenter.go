package datacenterServiceInterfaces

import (
	datacenterModels "github.com/alchemillahq/sylve/internal/db/models/datacenter"
	"github.com/hashicorp/raft"
)

type DatacenterServiceInterface interface {
	AcceptJoin(nodeID string, nodeAddr string, providedKey string) error
	CanJoinCluster() error
	CreateCluster(fsm raft.FSM) (*datacenterModels.DataCenter, error)
	GetCluster() (*datacenterModels.DataCenter, error)
	InitRaft(fsm raft.FSM) error
	SetupRaft(bootstrap bool, fsm raft.FSM) (*raft.Raft, error)
	StartAsJoiner(fsm raft.FSM) error
}
