package db

import (
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

func CleanupOrphanBackupEvents(db *gorm.DB) error {
	deleteResult := db.Where(
		"job_id IS NOT NULL AND job_id NOT IN (?)",
		db.Model(&clusterModels.BackupJob{}).Select("id"),
	).Delete(&clusterModels.BackupEvent{})
	if deleteResult.Error != nil {
		return deleteResult.Error
	}

	if deleteResult.RowsAffected > 0 {
		logger.L.Info().Int64("count", deleteResult.RowsAffected).Msg("Removed orphan backup events")
	}

	return nil
}

func PruneJobs(db *gorm.DB) error {
	if err := CleanupOrphanBackupEvents(db); err != nil {
		return err
	}

	if err := EnforceAuditRecordRetention(db, time.Now()); err != nil {
		return err
	}

	return nil
}
