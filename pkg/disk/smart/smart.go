package smart

type Attribute struct {
	ID        uint32
	Name      string
	Value     int
	Worst     int
	Threshold int
	RawValue  uint64
	RawBytes  []byte
	TextValue string
	IsText    bool
}

type DeviceInfo struct {
	Device          string
	Protocol        string
	Temperature     int
	PowerOnHours    int
	PowerCycleCount int
	Attributes      []Attribute
}
