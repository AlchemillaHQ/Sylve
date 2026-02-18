package replicationServiceInterfaces

import (
	"context"
	"time"
)

type ReplicationServiceInterface interface {
	Run(ctx context.Context)
	RegisterJobs()
}

type BackupEventInfo struct {
	ID                 uint       `json:"id"`
	JobID              *uint      `json:"jobId,omitempty"`
	Direction          string     `json:"direction"`
	RemoteAddress      string     `json:"remoteAddress"`
	SourceDataset      string     `json:"sourceDataset"`
	DestinationDataset string     `json:"destinationDataset"`
	BaseSnapshot       string     `json:"baseSnapshot"`
	TargetSnapshot     string     `json:"targetSnapshot"`
	Mode               string     `json:"mode"`
	Status             string     `json:"status"`
	Error              string     `json:"error"`
	StartedAt          time.Time  `json:"startedAt"`
	CompletedAt        *time.Time `json:"completedAt"`
}

type BackupEventsResponse struct {
	LastPage int               `json:"last_page"`
	Data     []BackupEventInfo `json:"data"`
}
