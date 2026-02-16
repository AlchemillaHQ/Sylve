package replicationServiceInterfaces

import "context"

type ReplicationServiceInterface interface {
	Run(ctx context.Context)
}
