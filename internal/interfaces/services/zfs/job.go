package zfsServiceInterfaces

type ZFSHistoryBatchJob struct {
	Pool     string   `json:"pool"`
	Kind     string   `json:"kind"`
	EventIDs []uint   `json:"event_ids"`
	Datasets []string `json:"datasets"`
	Actions  []string `json:"actions"`
	MinTXG   string   `json:"min_txg,omitempty"`
	MaxTXG   string   `json:"max_txg,omitempty"`
}
