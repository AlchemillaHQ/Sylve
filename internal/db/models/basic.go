package models

type AvailableService string

const (
	Virtualization AvailableService = "virtualization"
	Jails          AvailableService = "jails"
	DHCPServer     AvailableService = "dhcp-server"
	SambaServer    AvailableService = "samba-server"
	WoLServer      AvailableService = "wol-server"
)

type BasicSettings struct {
	Pools       []string           `json:"pools" gorm:"serializer:json;type:json"`
	Services    []AvailableService `json:"services" gorm:"serializer:json;type:json"`
	Initialized bool               `json:"initialized"`
}
