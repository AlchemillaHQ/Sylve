// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"errors"
	"fmt"
	"strings"

	"github.com/alchemillahq/sylve/internal/db/replicationguard"
	"gorm.io/gorm"
)

var (
	ErrGuestDeleteRequiresReplicationPolicyRemoved = errors.New("guest_delete_requires_replication_policy_removed")
	ErrGuestDeleteRequiresBackupJobsRemoved        = errors.New("guest_delete_requires_backup_jobs_removed")
)

func guestHasExplicitBackupJob(
	db *gorm.DB,
	guestType string,
	guestID uint,
) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("backup_job_database_unavailable")
	}
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	if (guestType != BackupJobModeVM && guestType != BackupJobModeJail) || guestID == 0 {
		return false, fmt.Errorf("backup_job_guest_identity_invalid")
	}

	var jobs []BackupJob
	if err := db.Select("mode", "source_dataset", "jail_root_dataset").
		Where("LOWER(TRIM(mode)) = ?", guestType).
		Find(&jobs).Error; err != nil {
		return false, fmt.Errorf("failed_to_check_guest_backup_jobs_before_delete: %w", err)
	}

	for i := range jobs {
		kind, id := BackupJobGuestIdentity(&jobs[i])
		if kind == guestType && id == guestID {
			return true, nil
		}
	}
	return false, nil
}

func RequireGuestDeletionDetachedTxn(db *gorm.DB, guestType string, guestID uint) error {
	if db == nil {
		return fmt.Errorf("guest_deletion_database_unavailable")
	}
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	if (guestType != ReplicationGuestTypeVM && guestType != ReplicationGuestTypeJail) || guestID == 0 {
		return fmt.Errorf("guest_deletion_identity_invalid")
	}

	if replicationguard.PolicySchemaReady(db) {
		var count int64
		if err := db.Model(&ReplicationPolicy{}).
			Where("guest_type = ? AND guest_id = ?", guestType, guestID).
			Count(&count).Error; err != nil {
			return fmt.Errorf("failed_to_check_guest_replication_policy_before_delete: %w", err)
		}
		if count > 0 {
			return ErrGuestDeleteRequiresReplicationPolicyRemoved
		}
	}

	hasBackupJob, err := guestHasExplicitBackupJob(db, guestType, guestID)
	if err != nil {
		return err
	}
	if hasBackupJob {
		return ErrGuestDeleteRequiresBackupJobsRemoved
	}
	return nil
}
