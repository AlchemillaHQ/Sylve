package networkServiceInterfaces

type ModifyDHCPConfigRequest struct {
	StandardSwitches []uint   `json:"standardSwitches"`
	ManualSwitches   []uint   `json:"manualSwitches"`
	DNSServers       []string `json:"dnsServers"`
	Domain           string   `json:"domain"`
	ExpandHosts      *bool    `json:"expandHosts"`
}

type CreateDHCPRangeRequest struct {
	StartIP        string `json:"startIp" binding:"required,ip"`
	EndIP          string `json:"endIp" binding:"required,ip"`
	StandardSwitch *uint  `json:"standardSwitch"`
	ManualSwitch   *uint  `json:"manualSwitch"`
	Expiry         *uint  `json:"expiry"`
}

type ModifyDHCPRangeRequest struct {
	ID             uint   `json:"id"`
	StartIP        string `json:"startIp" binding:"required,ip"`
	EndIP          string `json:"endIp" binding:"required,ip"`
	StandardSwitch *uint  `json:"standardSwitch"`
	ManualSwitch   *uint  `json:"manualSwitch"`
	Expiry         *uint  `json:"expiry"`
}
