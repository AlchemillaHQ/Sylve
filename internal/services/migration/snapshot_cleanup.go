// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migration

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/alchemillahq/sylve/internal/logger"
)

const (
	migrationStaleSnapshotAge = 1 * time.Hour
)

func (s *Service) StartSnapshotCleanupTicker(ctx context.Context) {
	if s == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return
	}

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.cleanupStaleMigrationSnapshots(ctx); err != nil {
				logger.L.Warn().Err(err).Msg("migration_snapshot_cleanup_tick_failed")
			}
		}
	}
}

func (s *Service) cleanupStaleMigrationSnapshots(ctx context.Context) error {
	protected, err := s.buildActiveMigrationGuestSet()
	if err != nil {
		return err
	}

	sets, err := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeFilesystem, false)
	if err != nil {
		return err
	}

	for _, ds := range sets {
		if ds == nil {
			continue
		}
		datasetName := ds.Name
		if datasetName == "" {
			continue
		}

		snapshots, snapErr := s.GZFS.ZFS.ListWithPrefix(ctx, gzfs.DatasetTypeSnapshot, datasetName, true)
		if snapErr != nil {
			continue
		}

		for _, snap := range snapshots {
			if snap == nil {
				continue
			}
			fullName := snap.Name
			atIdx := strings.LastIndex(fullName, "@")
			if atIdx < 0 {
				continue
			}
			shortName := fullName[atIdx+1:]
			if shortName == "" || !strings.HasPrefix(shortName, migrationSnapPrefix) {
				continue
			}

			snapTime, ok := parseMigrationSnapshotTimestamp(shortName)
			if !ok || time.Since(snapTime) < migrationStaleSnapshotAge {
				continue
			}

			guestType, guestID := extractGuestFromDatasetPath(datasetName)
			if guestType == "" || guestID == 0 {
				continue
			}

			key := guestKey(guestType, guestID)
			if _, active := protected[key]; active {
				continue
			}

			if destroyErr := snap.Destroy(ctx, false, false); destroyErr != nil {
				if !isDatasetNotFound(destroyErr) {
					logger.L.Warn().
						Str("snapshot", fullName).
						Err(destroyErr).
						Msg("migration_snapshot_cleanup_destroy_failed")
				}
				continue
			}

			logger.L.Info().
				Str("snapshot", fullName).
				Str("guest_type", guestType).
				Uint("guest_id", guestID).
				Msg("migration_snapshot_cleaned")
		}
	}

	return nil
}

func (s *Service) buildActiveMigrationGuestSet() (map[string]struct{}, error) {
	out := make(map[string]struct{})

	var tasks []taskModels.GuestLifecycleTask
	if err := s.DB.
		Where("status IN ?", []string{
			taskModels.LifecycleTaskStatusQueued,
			taskModels.LifecycleTaskStatusRunning,
		}).
		Find(&tasks).Error; err != nil {
		return nil, err
	}

	for _, t := range tasks {
		key := guestKey(t.GuestType, t.GuestID)
		if key != "" {
			out[key] = struct{}{}
		}
	}

	return out, nil
}

func guestKey(guestType string, guestID uint) string {
	guestType = strings.TrimSpace(guestType)
	if guestType == "" || guestID == 0 {
		return ""
	}
	return guestType + "-" + strconv.FormatUint(uint64(guestID), 10)
}

func parseMigrationSnapshotTimestamp(snapShortName string) (time.Time, bool) {
	idx := strings.LastIndex(snapShortName, "-")
	if idx < 0 {
		return time.Time{}, false
	}
	ts, err := strconv.ParseInt(snapShortName[idx+1:], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(ts, 0), true
}

func extractGuestFromDatasetPath(dataset string) (guestType string, guestID uint) {
	parts := strings.Split(dataset, "/")
	for i, part := range parts {
		if part == "virtual-machines" && i+1 < len(parts) {
			if id, err := strconv.ParseUint(parts[i+1], 10, 64); err == nil {
				return "vm", uint(id)
			}
		}
		if part == "jails" && i+1 < len(parts) {
			if id, err := strconv.ParseUint(parts[i+1], 10, 64); err == nil {
				return "jail", uint(id)
			}
		}
	}
	return "", 0
}
