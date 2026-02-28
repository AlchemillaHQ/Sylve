package clusterServiceInterfaces

type ReplicationPolicyTargetReq struct {
	NodeID string `json:"nodeId" binding:"required"`
	Weight int    `json:"weight"`
}

type ReplicationPolicyReq struct {
	Name         string                       `json:"name" binding:"required,min=2"`
	GuestType    string                       `json:"guestType" binding:"required"`
	GuestID      uint                         `json:"guestId" binding:"required"`
	SourceNodeID string                       `json:"sourceNodeId"`
	SourceMode   string                       `json:"sourceMode"`
	FailbackMode string                       `json:"failbackMode"`
	CronExpr     string                       `json:"cronExpr"`
	Enabled      *bool                        `json:"enabled"`
	Targets      []ReplicationPolicyTargetReq `json:"targets" binding:"required"`
}
