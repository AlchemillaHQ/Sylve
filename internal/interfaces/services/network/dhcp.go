package networkServiceInterfaces

type ModifyDHCPConfigRequest struct {
	StandardSwitches []uint   `json:"standardSwitches"`
	ManualSwitches   []uint   `json:"manualSwitches"`
	DNSServers       []string `json:"dnsServers"`
	Domain           string   `json:"domain"`
	ExpandHosts      *bool    `json:"expandHosts"`
}

type CreateDHCPRangeRequest struct {
	Type           string `json:"type" binding:"required,oneof=ipv4 ipv6"`
	StartIP        string `json:"startIp"`
	EndIP          string `json:"endIp"`
	StandardSwitch *uint  `json:"standardSwitch"`
	ManualSwitch   *uint  `json:"manualSwitch"`
	Expiry         *uint  `json:"expiry"`
	RAOnly         *bool  `json:"raOnly"`
	SLAAC          *bool  `json:"slaac"`
}

type ModifyDHCPRangeRequest struct {
	ID             uint   `json:"id"`
	Type           string `json:"type" binding:"required,oneof=ipv4 ipv6"`
	StartIP        string `json:"startIp"`
	EndIP          string `json:"endIp"`
	StandardSwitch *uint  `json:"standardSwitch"`
	ManualSwitch   *uint  `json:"manualSwitch"`
	Expiry         *uint  `json:"expiry"`
	RAOnly         *bool  `json:"raOnly"`
	SLAAC          *bool  `json:"slaac"`
}
