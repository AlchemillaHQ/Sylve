package clusterServiceInterfaces

type BackupTargetReq struct {
	ID          uint   `json:"id,omitempty"`
	Name        string `json:"name" binding:"required,min=2"`
	SSHHost     string `json:"sshHost" binding:"required,min=3"`
	SSHPort     int    `json:"sshPort"`
	SSHKey      string `json:"sshKey"`
	SSHKeyPath  string `json:"-"`
	BackupRoot  string `json:"backupRoot" binding:"required,min=2"`
	Description string `json:"description"`
	Enabled     *bool  `json:"enabled"`
}

type BackupJobReq struct {
	Name             string `json:"name" binding:"required,min=2"`
	TargetID         uint   `json:"targetId" binding:"required"`
	RunnerNodeID     string `json:"runnerNodeId"`
	Mode             string `json:"mode" binding:"required"`
	SourceDataset    string `json:"sourceDataset"`
	JailRootDataset  string `json:"jailRootDataset"`
	DestSuffix       string `json:"destSuffix"`
	PruneKeepLast    int    `json:"pruneKeepLast"`
	PruneTarget      bool   `json:"pruneTarget"`
	StopBeforeBackup bool   `json:"stopBeforeBackup"`
	CronExpr         string `json:"cronExpr"`
	Enabled          *bool  `json:"enabled"`
}
