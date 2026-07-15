// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"context"
	"strings"
)

type replicationTransitionRunContextKey struct{}

type ReplicationTransitionAuthority struct {
	RunID      string
	OwnerEpoch uint64
}

// WithReplicationTransitionRun marks database work performed by the durable
// transition engine. Storage model hooks only accept the bypass when this run
// ID exactly matches the policy checkpoint currently in progress.
func WithReplicationTransitionRun(ctx context.Context, runID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	authority := ReplicationTransitionAuthority{RunID: strings.TrimSpace(runID)}
	if existing := ReplicationTransitionAuthorityFromContext(ctx); existing.RunID == authority.RunID {
		authority.OwnerEpoch = existing.OwnerEpoch
	}
	return context.WithValue(ctx, replicationTransitionRunContextKey{}, authority)
}

func ReplicationTransitionRunFromContext(ctx context.Context) string {
	return ReplicationTransitionAuthorityFromContext(ctx).RunID
}

func WithReplicationTransitionAuthority(ctx context.Context, runID string, ownerEpoch uint64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, replicationTransitionRunContextKey{}, ReplicationTransitionAuthority{
		RunID:      strings.TrimSpace(runID),
		OwnerEpoch: ownerEpoch,
	})
}

func ReplicationTransitionAuthorityFromContext(ctx context.Context) ReplicationTransitionAuthority {
	if ctx == nil {
		return ReplicationTransitionAuthority{}
	}
	value := ctx.Value(replicationTransitionRunContextKey{})
	if authority, ok := value.(ReplicationTransitionAuthority); ok {
		authority.RunID = strings.TrimSpace(authority.RunID)
		return authority
	}
	// Compatibility for contexts constructed before authority carried epoch.
	runID, _ := value.(string)
	return ReplicationTransitionAuthority{RunID: strings.TrimSpace(runID)}
}
