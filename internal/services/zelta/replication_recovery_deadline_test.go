// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestReplicationPromotionRecoveryUsesExplicitDeadline(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	futureDeadline := now.Add(time.Minute)
	pastDeadline := now.Add(-time.Minute)

	policy := &clusterModels.ReplicationPolicy{
		UpdatedAt:                    now.Add(-24 * time.Hour),
		TransitionRecoveryDeadlineAt: &futureDeadline,
	}
	if replicationPromotionRecoveryExpired(policy, now) {
		t.Fatal("old UpdatedAt incorrectly expired a future recovery deadline")
	}

	policy.UpdatedAt = now.Add(24 * time.Hour)
	policy.TransitionRecoveryDeadlineAt = &pastDeadline
	if !replicationPromotionRecoveryExpired(policy, now) {
		t.Fatal("future UpdatedAt incorrectly extended an expired recovery deadline")
	}
}
