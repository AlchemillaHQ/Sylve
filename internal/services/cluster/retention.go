// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"encoding/json"
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
	decision := replicatedRetentionDecision(now)
	bypassRaft, err := s.RuntimeStateBypassRaft()
	if err != nil {
		return err
	}
	if bypassRaft {
		return clusterModels.ApplyReplicatedRetentionTxn(s.DB, &decision)
	}
	if s.Raft == nil || s.Raft.State() != raft.Leader {
		return nil
	}

	data, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_replicated_retention_decision: %w", err)
	}
	return s.applyRaftCommand(clusterModels.Command{
		Type: "replicated_retention", Action: "apply", Data: data,
	})
}
