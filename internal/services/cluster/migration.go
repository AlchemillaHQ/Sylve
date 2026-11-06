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

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

// SSH identities with a legacy port are replicated, so they're left alone:
// each node republishes its own through Raft once it votes.
func (s *Service) MigrateLegacyPorts() error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}
	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err != nil {
		return fmt.Errorf("failed_to_load_cluster_record: %w", err)
	}
	if c.RaftPort == ClusterRaftPort {
		return nil
	}
	if err := s.DB.Model(&c).Update("raft_port", ClusterRaftPort).Error; err != nil {
		return fmt.Errorf("failed_to_migrate_raft_port: %w", err)
	}
	return nil
}
