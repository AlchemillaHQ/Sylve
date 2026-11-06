// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/raft"
)

type ReplicatedStateDigest struct {
	NodeID       string `json:"nodeId"`
	AppliedIndex uint64 `json:"appliedIndex"`
	Digest       string `json:"digest"`
}

func raftSuffrageName(suffrage raft.ServerSuffrage) string {
	switch suffrage {
	case raft.Voter:
		return "voter"
	case raft.Nonvoter:
		return "nonvoter"
	default:
		return fmt.Sprintf("unknown_%d", suffrage)
	}
}

// Raft's own applied index runs ahead of this: Raft counts an entry as applied
// once it hands it to the FSM, before the FSM commits it.
func (s *Service) AppliedIndex() uint64 {
	if s == nil {
		return 0
	}
	return s.fsm.AppliedIndex()
}

func (s *Service) WaitForAppliedIndex(ctx context.Context, index uint64) (uint64, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		applied := s.AppliedIndex()
		if applied >= index {
			return applied, nil
		}
		if r := s.Raft(); r == nil || r.State() == raft.Shutdown {
			return applied, fmt.Errorf("replicated_state_raft_unavailable: index=%d applied=%d", index, applied)
		}
		select {
		case <-ctx.Done():
			return applied, fmt.Errorf("replicated_state_catchup_failed: index=%d applied=%d: %w", index, applied, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (s *Service) LocalReplicatedStateDigest(expectedNodeID string) (ReplicatedStateDigest, error) {
	result := ReplicatedStateDigest{NodeID: strings.TrimSpace(s.LocalNodeID())}
	if expected := strings.TrimSpace(expectedNodeID); expected != "" && expected != result.NodeID {
		return result, fmt.Errorf("replicated_state_node_id_mismatch: expected=%s actual=%s", expected, result.NodeID)
	}
	digest, index, err := s.fsm.StateDigest()
	if err != nil {
		return result, fmt.Errorf("capture_replicated_state_digest: %w", err)
	}
	result.AppliedIndex, result.Digest = index, digest
	return result, nil
}
