// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"fmt"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/raft"
)

func replicatedRetentionDecision(now time.Time) clusterModels.ReplicatedRetentionDecision {
	cutoff := now.UTC().Add(-replicatedRetentionDays * 24 * time.Hour)
	return clusterModels.ReplicatedRetentionDecision{
		ScheduledRunReceiptCutoff:    cutoff,
		ScheduledRunReceiptMaxRows:   replicatedRetentionMaxRows,
		GuestOperationReceiptCutoff:  cutoff,
		GuestOperationReceiptMaxRows: replicatedRetentionMaxRows,
		ReplicationTransitionCutoff:  cutoff,
		ReplicationTransitionMaxRows: replicatedRetentionMaxRows,
	}
}

// EnforceReplicatedRetention prunes standalone state locally and clustered
// state only through a leader-decided Raft command.
func (s *Service) EnforceReplicatedRetention(now time.Time) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}
	if r := s.Raft(); r != nil && r.State() != raft.Leader {
		return nil
	}
	return s.apply("replicated_retention", "apply", replicatedRetentionDecision(now))
}
