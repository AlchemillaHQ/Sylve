// SPDX-License-Identifier: BSD-2-Clause

package migration

import (
	"context"
	"fmt"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

const replicationPolicyMustBeDisabledReason = "replication_policy_must_be_disabled_before_migration"

func (s *Service) requireReplicationDisabledForMigration(
	ctx context.Context,
	guestType string,
	guestID uint,
) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("replication_policy_lookup_failed: database_not_initialized")
	}
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	if guestType == "" || guestID == 0 {
		return fmt.Errorf("replication_policy_lookup_failed: invalid_guest_identity")
	}

	var count int64
	if err := s.DB.WithContext(ctx).
		Model(&clusterModels.ReplicationPolicy{}).
		Where("guest_type = ? AND guest_id = ? AND enabled = ?", guestType, guestID, true).
		Count(&count).Error; err != nil {
		return fmt.Errorf("replication_policy_lookup_failed: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("%s", replicationPolicyMustBeDisabledReason)
	}
	return nil
}
