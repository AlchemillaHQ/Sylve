package datacenterModels

type DataCenter struct {
	ID            uint   `gorm:"primaryKey" json:"id"`
	RaftBootstrap *bool  `json:"raftBootstrap"`
	ClusterKey    string `json:"clusterKey"`
}
