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
	"gorm.io/gorm"
)

const legacyClusterEmbeddedSSHPort = 8122

func (s *Service) MigrateLegacyPorts() error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}

	return s.DB.Transaction(func(tx *gorm.DB) error {
		var c clusterModels.Cluster
		if err := tx.First(&c).Error; err != nil {
			return fmt.Errorf("failed_to_load_cluster_record: %w", err)
		}

		if c.RaftPort != ClusterRaftPort {
			if err := tx.Model(&c).Update("raft_port", ClusterRaftPort).Error; err != nil {
				return fmt.Errorf("failed_to_migrate_raft_port: %w", err)
			}
		}

		if err := tx.Model(&clusterModels.ClusterSSHIdentity{}).
			Where("ssh_port IN ?", []int{0, legacyClusterEmbeddedSSHPort}).
			Update("ssh_port", ClusterEmbeddedSSHPort).Error; err != nil {
			return fmt.Errorf("failed_to_migrate_cluster_ssh_identity_ports: %w", err)
		}

		return nil
	})
}
