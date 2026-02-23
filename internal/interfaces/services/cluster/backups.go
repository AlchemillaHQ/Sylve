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
