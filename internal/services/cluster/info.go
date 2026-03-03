package cluster

import (
	"fmt"

	"github.com/hashicorp/raft"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
)

func (s *Service) SyncClusterHealth(payload []clusterServiceInterfaces.NodeHealthSync) error {
	if s.Raft != nil && s.Raft.State() == raft.Leader {
		return fmt.Errorf("leader_should_not_receive_syncs")
	}

	return s.DB.Transaction(func(tx *gorm.DB) error {
		var incomingUUIDs []string
		var insertRows []clusterModels.ClusterNode

		for _, node := range payload {
			incomingUUIDs = append(incomingUUIDs, node.NodeUUID)

			insertRows = append(insertRows, clusterModels.ClusterNode{
				NodeUUID:    node.NodeUUID,
				Hostname:    node.Hostname,
				API:         node.API,
				Status:      node.Status,
				CPU:         node.CPU,
				CPUUsage:    node.CPUUsage,
				Memory:      node.Memory,
				MemoryUsage: node.MemoryUsage,
				Disk:        node.Disk,
				DiskUsage:   node.DiskUsage,
				GuestIDs:    node.GuestIDs,
			})
		}

		if len(insertRows) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "node_uuid"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"hostname", "api", "status", "cpu", "cpu_usage",
					"memory", "memory_usage", "disk", "disk_usage", "guest_ids", "updated_at",
				}),
			}).Create(&insertRows).Error; err != nil {
				return err
			}
		}

		deleteQuery := tx.Where("node_uuid NOT IN ?", incomingUUIDs)
		if s.NodeID != "" {
			deleteQuery = deleteQuery.Where("node_uuid != ?", s.NodeID)
		}

		if err := deleteQuery.Delete(&clusterModels.ClusterNode{}).Error; err != nil {
			return err
		}

		return nil
	})
}
