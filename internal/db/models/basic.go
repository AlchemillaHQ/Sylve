package models

type AvailableService string

const (
	DHCPServer     AvailableService = "dhcp-server"
	Jails          AvailableService = "jails"
	SambaServer    AvailableService = "samba-server"
	Virtualization AvailableService = "virtualization"
	WoLServer      AvailableService = "wol-server"
)

type BasicSettings struct {
	ID          uint               `json:"id" gorm:"primaryKey;autoIncrement"`
	Pools       []string           `json:"pools" gorm:"serializer:json;type:json"`
	Services    []AvailableService `json:"services" gorm:"serializer:json;type:json"`
	Initialized bool               `json:"initialized"`
	Restarted   bool               `json:"restarted"`
}
