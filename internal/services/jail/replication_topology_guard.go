// SPDX-License-Identifier: BSD-2-Clause

package jail

import (
	"errors"
	"fmt"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
)

func (s *Service) RequireJailStorageTopologyMutable(ctID uint) error {
	allowed, err := clusterService.CanMutateProtectedGuestStorageTopology(
		s.DB,
		clusterModels.ReplicationGuestTypeJail,
		ctID,
	)
	if err != nil {
		if errors.Is(err, clusterService.ErrReplicationRunInProgress) {
			return err
		}
		return fmt.Errorf("replication_topology_check_failed: %w", err)
	}
	if !allowed {
		return fmt.Errorf("replication_storage_topology_change_requires_policy_disabled")
	}
	return nil
}
