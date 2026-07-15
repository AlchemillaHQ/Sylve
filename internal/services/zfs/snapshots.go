// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	"github.com/alchemillahq/sylve/internal/db/replicationguard"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm/clause"
)

type retentionType string

const (
	retentionNone   retentionType = "none"
	retentionSimple retentionType = "simple"
	retentionGFS    retentionType = "gfs"
)

var (
	ErrReservedSnapshotNamespace = errors.New("snapshot_namespace_reserved")
	ErrSnapshotCreationBlocked   = errors.New("snapshot_creation_blocked")
)

var reservedUserSnapshotPrefixes = []string{"ha_", "bk_", "sylve-migrate-"}

func validateUserSnapshotNamespace(name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, prefix := range reservedUserSnapshotPrefixes {
		if strings.HasPrefix(name, prefix) {
			return fmt.Errorf("%w:%s", ErrReservedSnapshotNamespace, prefix)
		}
	}
	return nil
}

func validatePeriodicSnapshotPrefix(prefix string) error {
	if err := validateUserSnapshotNamespace(prefix); err != nil {
		return err
	}
	// Periodic names append a dash. This also reserves the exact
	// "sylve-migrate" prefix, whose generated names enter that namespace.
	return validateUserSnapshotNamespace(prefix + "-")
}

func snapshotScopeContains(dataset, protectedRoot string, recursive bool) bool {
	dataset = normalizedMutationDataset(dataset)
	protectedRoot = normalizedMutationDataset(protectedRoot)
	if dataset == "" || protectedRoot == "" {
		return false
	}
	if dataset == protectedRoot || strings.HasPrefix(dataset, protectedRoot+"/") {
		return true
	}
	return recursive && strings.HasPrefix(protectedRoot, dataset+"/")
}

func (s *Service) requireUserSnapshotCreationAllowed(
	ctx context.Context,
	dataset string,
	recursive bool,
) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("snapshot_creation_guard_unavailable")
	}
	dataset = normalizedMutationDataset(dataset)
	if dataset == "" {
		return fmt.Errorf("snapshot_creation_dataset_required")
	}

	if replicationguard.GuestOperationSchemaReady(s.DB) {
		var operations []clusterModels.ReplicationGuestOperation
		if err := s.DB.Find(&operations).Error; err != nil {
			return fmt.Errorf("snapshot_guest_operation_lookup_failed: %w", err)
		}
		guests := make([]clusterModels.ReplicationPolicy, 0, len(operations))
		for _, operation := range operations {
			guests = append(guests, clusterModels.ReplicationPolicy{
				GuestType: operation.GuestType,
				GuestID:   operation.GuestID,
			})
		}
		roots, err := s.protectedReplicationDatasetRoots(guests)
		if err != nil {
			return err
		}
		for _, root := range roots {
			if snapshotScopeContains(dataset, root, recursive) {
				return fmt.Errorf("%w:guest_operation:%s", ErrSnapshotCreationBlocked, root)
			}
		}
	}

	var restore clusterModels.BackupEvent
	result := s.DB.Select("id").
		Where("mode = ? AND status = ?", "restore", "running").
		Limit(1).Find(&restore)
	if result.Error != nil {
		return fmt.Errorf("snapshot_restore_lookup_failed: %w", result.Error)
	}
	if result.RowsAffected != 0 {
		return fmt.Errorf("%w:restore:%d", ErrSnapshotCreationBlocked, restore.ID)
	}

	args := []string{"get", "-H", "-o", "name,property,value"}
	if recursive {
		args = append(args, "-r")
	}
	args = append(args, "-t", "filesystem,volume", "sylve:replication-role,readonly", dataset)
	output, err := utils.RunCommandWithContext(ctx, "zfs", args...)
	if err != nil {
		return fmt.Errorf("snapshot_replication_provenance_lookup_failed: %w", err)
	}
	type provenanceState struct{ role, readonly string }
	states := make(map[string]provenanceState)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		state := states[fields[0]]
		switch fields[1] {
		case "sylve:replication-role":
			state.role = fields[2]
		case "readonly":
			state.readonly = fields[2]
		}
		states[fields[0]] = state
	}
	for _, state := range states {
		if strings.EqualFold(state.role, "standby") && state.readonly == "on" {
			return fmt.Errorf("%w:ha_standby:%s", ErrSnapshotCreationBlocked, dataset)
		}
	}

	return nil
}

type retentionValues struct {
	KeepLast, MaxAgeDays              int
	KeepHourly, KeepDaily, KeepWeekly int
	KeepMonthly, KeepYearly           int
}

func (s *Service) CreateSnapshot(ctx context.Context, guid string, name string, recursive bool) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	if err := validateUserSnapshotNamespace(name); err != nil {
		return err
	}

	dataset, err := s.GZFS.ZFS.GetByGUID(ctx, guid, false)
	if err != nil {
		return err
	}
	if err := s.requireUserSnapshotCreationAllowed(ctx, dataset.Name, recursive); err != nil {
		return err
	}

	shot, err := dataset.Snapshot(ctx, name, recursive)
	if err != nil {
		return err
	}

	if shot.Name != dataset.Name+"@"+name {
		return fmt.Errorf("snapshot_creation_failed")
	}

	s.SignalDSChange(shot.Pool, shot.Name, "snapshot", "create")

	return nil
}

func (s *Service) DeleteSnapshot(ctx context.Context, guid string, recursive bool) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	dataset, err := s.GZFS.ZFS.GetByGUID(ctx, guid, false)

	if err != nil {
		return err
	}

	err = dataset.Destroy(ctx, recursive, false)

	if err != nil {
		return err
	}

	s.SignalDSChange(dataset.Pool, dataset.Name, "snapshot", "create")

	return nil
}

func (s *Service) GetPeriodicSnapshots() ([]zfsModels.PeriodicSnapshot, error) {
	var snapshots []zfsModels.PeriodicSnapshot

	if err := s.DB.Find(&snapshots).Error; err != nil {
		return nil, err
	}

	return snapshots, nil
}

func validateAndNormalizeRetention(req any, t string) (retentionType, retentionValues, error) {
	var keepLast, maxAgeDays, keepHourly, keepDaily, keepWeekly, keepMonthly, keepYearly *int

	switch t {
	case "create":
		r := req.(zfsServiceInterfaces.CreatePeriodicSnapshotJobRequest)
		keepLast = r.KeepLast
		maxAgeDays = r.MaxAgeDays
		keepHourly = r.KeepHourly
		keepDaily = r.KeepDaily
		keepWeekly = r.KeepWeekly
		keepMonthly = r.KeepMonthly
		keepYearly = r.KeepYearly
	case "modify":
		r := req.(zfsServiceInterfaces.ModifyPeriodicSnapshotRetentionRequest)
		keepLast = r.KeepLast
		maxAgeDays = r.MaxAgeDays
		keepHourly = r.KeepHourly
		keepDaily = r.KeepDaily
		keepWeekly = r.KeepWeekly
		keepMonthly = r.KeepMonthly
		keepYearly = r.KeepYearly
	default:
		return "", retentionValues{}, fmt.Errorf("invalid_request_type")
	}

	simplePresent := keepLast != nil || maxAgeDays != nil
	gfsPresent := keepHourly != nil || keepDaily != nil ||
		keepWeekly != nil || keepMonthly != nil || keepYearly != nil

	if simplePresent && gfsPresent {
		return "", retentionValues{}, fmt.Errorf("retention_conflict: simple and GFS cannot be set together")
	}

	val := retentionValues{
		KeepLast:    utils.IntOrZero(keepLast),
		MaxAgeDays:  utils.IntOrZero(maxAgeDays),
		KeepHourly:  utils.IntOrZero(keepHourly),
		KeepDaily:   utils.IntOrZero(keepDaily),
		KeepWeekly:  utils.IntOrZero(keepWeekly),
		KeepMonthly: utils.IntOrZero(keepMonthly),
		KeepYearly:  utils.IntOrZero(keepYearly),
	}

	for _, v := range []int{
		val.KeepLast, val.MaxAgeDays,
		val.KeepHourly, val.KeepDaily, val.KeepWeekly, val.KeepMonthly, val.KeepYearly,
	} {
		if v < 0 {
			return "", retentionValues{}, fmt.Errorf("invalid_retention: values must be >= 0")
		}
	}

	if simplePresent {
		if val.KeepLast == 0 && val.MaxAgeDays == 0 {
			return retentionNone, val, nil
		}
		return retentionSimple, val, nil
	}
	if gfsPresent {
		if val.KeepHourly == 0 && val.KeepDaily == 0 && val.KeepWeekly == 0 &&
			val.KeepMonthly == 0 && val.KeepYearly == 0 {
			return retentionNone, val, nil
		}
		return retentionGFS, val, nil
	}

	return retentionNone, val, nil
}

func (s *Service) AddPeriodicSnapshot(ctx context.Context, req zfsServiceInterfaces.CreatePeriodicSnapshotJobRequest) error {
	if err := validatePeriodicSnapshotPrefix(req.Prefix); err != nil {
		return err
	}

	var interval int
	if req.Interval != nil {
		interval = *req.Interval
	}
	cronExpr := req.CronExpr

	var recursive bool
	if req.Recursive != nil {
		recursive = *req.Recursive
	}

	if (interval == 0 && cronExpr == "") || (interval != 0 && cronExpr != "") {
		return fmt.Errorf("invalid_schedule: specify either interval or cronExpr")
	}

	_, rvals, err := validateAndNormalizeRetention(req, "create")
	if err != nil {
		return err
	}

	ds, err := s.GZFS.ZFS.GetByGUID(ctx, req.GUID, false)
	if err != nil {
		return fmt.Errorf("dataset_with_guid_not_found")
	}

	snapshot := zfsModels.PeriodicSnapshot{
		GUID:      req.GUID,
		Prefix:    req.Prefix,
		Recursive: recursive,
		Interval:  interval,
		CronExpr:  cronExpr,
		Pool:      ds.Pool,

		KeepLast:   rvals.KeepLast,
		MaxAgeDays: rvals.MaxAgeDays,

		KeepHourly:  rvals.KeepHourly,
		KeepDaily:   rvals.KeepDaily,
		KeepWeekly:  rvals.KeepWeekly,
		KeepMonthly: rvals.KeepMonthly,
		KeepYearly:  rvals.KeepYearly,
	}

	if err := s.DB.Create(&snapshot).Error; err != nil {
		return err
	}

	if interval > 0 && cronExpr == "" {
		seedLocal := utils.ComputeLocalBoundary(interval, time.Now())
		name := req.Prefix + "-" + seedLocal.Format("2006-01-02-15-04")

		if err := s.requireUserSnapshotCreationAllowed(ctx, ds.Name, recursive); err != nil {
			logger.L.Debug().Err(err).Msgf("Skipping initial snapshot for job %s", snapshot.GUID)
			return nil
		}

		full := ds.Name + "@" + name
		exists, _ := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeSnapshot, false, full)

		if exists == nil || len(exists) == 0 {
			isnap, err := ds.Snapshot(ctx, name, recursive)
			if err != nil {
				logger.L.Warn().Err(err).Msgf("Failed to create initial snapshot %s", full)
				return nil
			}

			logger.L.Debug().Msgf("Initial boundary snapshot created: %s", full)
			s.SignalDSChange(isnap.Pool, isnap.Name, "snapshot", "create")
		}

		if err := s.DB.Model(&snapshot).Update("LastRunAt", seedLocal.UTC()).Error; err != nil {
			logger.L.Warn().Err(err).Msgf("Failed to seed LastRunAt for snapshot job %s", snapshot.GUID)
		}
	}

	return nil
}

func (s *Service) ModifyPeriodicSnapshotRetention(req zfsServiceInterfaces.ModifyPeriodicSnapshotRetentionRequest) error {
	var job zfsModels.PeriodicSnapshot
	if err := s.DB.
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", req.ID).
		First(&job).Error; err != nil {
		return err
	}

	rtype, rvals, err := validateAndNormalizeRetention(req, "modify")
	if err != nil {
		return err
	}

	simplePresent := req.KeepLast != nil || req.MaxAgeDays != nil
	gfsPresent := req.KeepHourly != nil || req.KeepDaily != nil ||
		req.KeepWeekly != nil || req.KeepMonthly != nil || req.KeepYearly != nil

	updates := map[string]interface{}{}

	if req.KeepLast != nil {
		updates["KeepLast"] = rvals.KeepLast
	}
	if req.MaxAgeDays != nil {
		updates["MaxAgeDays"] = rvals.MaxAgeDays
	}
	if req.KeepHourly != nil {
		updates["KeepHourly"] = rvals.KeepHourly
	}
	if req.KeepDaily != nil {
		updates["KeepDaily"] = rvals.KeepDaily
	}
	if req.KeepWeekly != nil {
		updates["KeepWeekly"] = rvals.KeepWeekly
	}
	if req.KeepMonthly != nil {
		updates["KeepMonthly"] = rvals.KeepMonthly
	}
	if req.KeepYearly != nil {
		updates["KeepYearly"] = rvals.KeepYearly
	}

	switch rtype {
	case retentionSimple:
		if gfsPresent {
			// no-op; validator should have errored earlier
		} else if simplePresent {
			// They touched simple; zero-out GFS only if they're switching away from it
			if job.KeepHourly != 0 || job.KeepDaily != 0 || job.KeepWeekly != 0 || job.KeepMonthly != 0 || job.KeepYearly != 0 {
				updates["KeepHourly"] = 0
				updates["KeepDaily"] = 0
				updates["KeepWeekly"] = 0
				updates["KeepMonthly"] = 0
				updates["KeepYearly"] = 0
			}
		}
	case retentionGFS:
		if simplePresent {
			// no-op
		} else if gfsPresent {
			if job.KeepLast != 0 || job.MaxAgeDays != 0 {
				updates["KeepLast"] = 0
				updates["MaxAgeDays"] = 0
			}
		}
	case retentionNone:
		return fmt.Errorf("no_retention_values_provided")
	}

	if len(updates) == 0 {
		return nil
	}

	if err := s.DB.Model(&job).Updates(updates).Error; err != nil {
		return err
	}
	return nil
}

func (s *Service) DeletePeriodicSnapshot(guid string) error {
	var snapshot zfsModels.PeriodicSnapshot

	if err := s.DB.Where("guid = ?", guid).First(&snapshot).Error; err != nil {
		return err
	}

	if err := s.DB.Delete(&snapshot).Error; err != nil {
		return err
	}

	return nil
}

func parseSnapshotTime(dsName, prefix, snapName string) (time.Time, bool) {
	const snapTimeLayout = "2006-01-02-15-04"

	p := dsName + "@"
	if !strings.HasPrefix(snapName, p) {
		return time.Time{}, false
	}

	short := strings.TrimPrefix(snapName, p)
	if !strings.HasPrefix(short, prefix+"-") {
		return time.Time{}, false
	}

	ts := strings.TrimPrefix(short, prefix+"-")
	t, err := time.ParseInLocation(snapTimeLayout, ts, time.Local)
	if err != nil {
		return time.Time{}, false
	}

	return t, true
}

func (s *Service) pruneSnapshots(
	ctx context.Context,
	job zfsModels.PeriodicSnapshot,
) {
	dataset, err := s.GZFS.ZFS.GetByGUID(ctx, job.GUID, false)
	if err != nil {
		logger.L.Debug().Err(err).Msgf("Failed to get dataset for pruning %s", job.GUID)
		return
	}

	if job.KeepLast == 0 &&
		job.MaxAgeDays == 0 &&
		job.KeepHourly == 0 &&
		job.KeepDaily == 0 &&
		job.KeepWeekly == 0 &&
		job.KeepMonthly == 0 &&
		job.KeepYearly == 0 {
		return
	}

	now := time.Now()

	var snaps []zfsServiceInterfaces.RetentionSnapInfo

	prefixShots, err := s.GZFS.ZFS.ListWithPrefix(ctx, gzfs.DatasetTypeSnapshot, dataset.Name, false)
	if err != nil {
		logger.L.Debug().Err(err).Msgf("Failed to list snapshots for pruning %s", job.GUID)
		return
	}

	if len(prefixShots) == 0 {
		return
	}

	for _, ds := range prefixShots {
		if t, ok := parseSnapshotTime(dataset.Name, job.Prefix, ds.Name); ok {
			snaps = append(snaps, zfsServiceInterfaces.RetentionSnapInfo{
				Name:    ds.Name,
				Dataset: ds,
				Time:    t,
			})
		}
	}

	sort.Slice(snaps, func(i, j int) bool { return snaps[i].Time.After(snaps[j].Time) })

	type key = *gzfs.Dataset
	keepers := make(map[key]struct{})
	add := func(snap zfsServiceInterfaces.RetentionSnapInfo) {
		if snap.Dataset != nil {
			keepers[snap.Dataset] = struct{}{}
		}
	}

	if job.KeepLast > 0 {
		for i := 0; i < len(snaps) && i < job.KeepLast; i++ {
			add(snaps[i])
		}
	}

	fillBucket := func(n int, makeKey func(time.Time) string) {
		if n <= 0 {
			return
		}
		seen := make(map[string]struct{})
		for _, sn := range snaps {
			k := makeKey(sn.Time.UTC())
			if _, ok := seen[k]; ok {
				continue
			}
			add(sn)
			seen[k] = struct{}{}
			if len(seen) >= n {
				break
			}
		}
	}

	fillBucket(job.KeepHourly, func(t time.Time) string {
		return t.Truncate(time.Hour).Format("2006-01-02-15")
	})

	fillBucket(job.KeepDaily, func(t time.Time) string {
		y, m, d := t.Date()
		return fmt.Sprintf("%04d-%02d-%02d", y, m, d)
	})

	fillBucket(job.KeepWeekly, func(t time.Time) string {
		year, week := t.ISOWeek()
		return fmt.Sprintf("W%04d-%02d", year, week)
	})

	fillBucket(job.KeepMonthly, func(t time.Time) string {
		y, m, _ := t.Date()
		return fmt.Sprintf("%04d-%02d", y, m)
	})

	fillBucket(job.KeepYearly, func(t time.Time) string {
		return fmt.Sprintf("%04d", t.Year())
	})

	useAge := job.MaxAgeDays > 0
	var cutoff time.Time
	if useAge {
		cutoff = now.AddDate(0, 0, -job.MaxAgeDays)
	}

	for i := len(snaps) - 1; i >= 0; i-- {
		sn := snaps[i]

		if sn.Dataset != nil {
			if _, ok := keepers[sn.Dataset]; ok {
				continue
			}
		}

		if useAge && sn.Time.After(cutoff) {
			logger.L.Debug().Msgf("Keep (younger than MaxAgeDays): %s", sn.Name)
			continue
		}

		if sn.Dataset == nil {
			logger.L.Debug().Msgf("Skip prune (nil dataset) %s", sn.Name)
			continue
		}

		if err := sn.Dataset.Destroy(ctx, job.Recursive, false); err != nil {
			logger.L.Debug().Err(err).Msgf("Failed to prune snapshot %s", sn.Name)
			continue
		}

		logger.L.Debug().Msgf("Pruned snapshot %s", sn.Name)
	}

	if len(snaps) > 0 {
		s.SignalDSChange(dataset.Pool, "", "snapshot", "prune")
	}
}

func (s *Service) StartSnapshotScheduler(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)

	go func() {
		for {
			select {
			case <-ticker.C:
				var snapshotJobs []zfsModels.PeriodicSnapshot
				if err := s.DB.Find(&snapshotJobs).Error; err != nil {
					logger.L.Debug().Err(err).Msg("Failed to load snapshotJobs")
					continue
				}

				nowLocal := time.Now()

				for _, job := range snapshotJobs {
					var (
						shouldRun  bool
						runAtLocal time.Time
					)

					if job.CronExpr != "" {
						sched, err := cron.ParseStandard(job.CronExpr)
						if err != nil {
							logger.L.Debug().Err(err).Msgf("Invalid cron expression for job %s", job.GUID)
							continue
						}

						start := nowLocal.Add(-48 * time.Hour)
						t := sched.Next(start)
						var last time.Time
						for !t.After(nowLocal) {
							last = t
							t = sched.Next(t)
						}

						if last.IsZero() {
							continue
						}

						if job.LastRunAt.IsZero() || last.After(job.LastRunAt) {
							shouldRun = true
							runAtLocal = last
						}

					} else if job.Interval > 0 {
						iv := time.Duration(job.Interval) * time.Second
						nowLocal := time.Now().In(time.Local)

						if job.LastRunAt.IsZero() {
							runAtLocal = nowLocal.Truncate(iv)
							shouldRun = true
						} else {
							lastLocal := job.LastRunAt.In(time.Local)
							elapsed := nowLocal.Sub(lastLocal)
							if elapsed >= iv {
								missedIntervals := elapsed / iv
								runAtLocal = lastLocal.Add(missedIntervals * iv)
								shouldRun = true
							}
						}
					} else {
						logger.L.Debug().Msgf("Skipping job %s: no valid interval or cronExpr", job.GUID)
						continue
					}

					if !shouldRun {
						continue
					}
					if err := validatePeriodicSnapshotPrefix(job.Prefix); err != nil {
						logger.L.Debug().Err(err).Msgf("Skipping snapshot job %s with reserved prefix", job.GUID)
						continue
					}

					boundaryLocal := runAtLocal
					persistTime := runAtLocal.UTC()

					if job.CronExpr != "" {
						boundaryLocal = runAtLocal
						persistTime = runAtLocal
					} else {
						boundaryLocal = runAtLocal
						persistTime = runAtLocal.UTC()
					}

					name := job.Prefix + "-" + boundaryLocal.Format("2006-01-02-15-04")
					dataset, err := s.GZFS.ZFS.GetByGUID(ctx, job.GUID, false)
					if err != nil {
						logger.L.Debug().Err(err).Msgf("Failed to get dataset for %s", job.GUID)
						if err := s.DB.Delete(&job).Error; err != nil {
							logger.L.Debug().Err(err).Msgf("Failed to delete job %s", job.GUID)
						}
						logger.L.Debug().Msgf("Deleted job %s due to missing dataset", job.GUID)
						continue
					}
					if err := s.requireUserSnapshotCreationAllowed(ctx, dataset.Name, job.Recursive); err != nil {
						logger.L.Debug().Err(err).Msgf("Skipping snapshot job %s", job.GUID)
						continue
					}

					full := dataset.Name + "@" + name

					exists, _ := s.GZFS.ZFS.Get(ctx, full, false)
					if exists != nil {
						logger.L.Debug().Msgf("Snapshot %s already exists", name)
						if err := s.DB.Model(&job).Update("LastRunAt", persistTime).Error; err != nil {
							logger.L.Debug().Err(err).Msgf("Failed to update LastRunAt for %d", job.ID)
						}
						continue
					}

					if snap, err := dataset.Snapshot(ctx, name, job.Recursive); err != nil {
						logger.L.Debug().Err(err).Msgf("Failed to create snapshot for %s", job.GUID)
						continue
					} else {
						s.SignalDSChange(snap.Pool, snap.Name, "snapshot", "create")
					}

					if err := s.DB.Model(&job).Update("LastRunAt", persistTime).Error; err != nil {
						logger.L.Debug().Err(err).Msgf("Failed to update LastRunAt for %d", job.ID)
					}

					logger.L.Debug().Msgf("Snapshot %s created", full)

					go s.pruneSnapshots(ctx, job)
				}

			case <-ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()
}

func (s *Service) RollbackSnapshot(ctx context.Context, guid string, destroyMoreRecent bool) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	dataset, err := s.GZFS.ZFS.GetByGUID(ctx, guid, false)
	if err != nil {
		return err
	}

	err = dataset.Rollback(ctx, destroyMoreRecent)
	if err != nil {
		return fmt.Errorf("failed_to_rollback_snapshot: %v", err)
	}

	s.SignalDSChange(dataset.Pool, dataset.Name, "snapshot", "rollback")

	return nil
}

func (s *Service) RollbackSnapshotByName(ctx context.Context, snapshotName string, destroyMoreRecent bool) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	dataset, err := s.GZFS.ZFS.Get(ctx, snapshotName, false)
	if err != nil {
		return fmt.Errorf("snapshot_not_found: %v", err)
	}

	err = dataset.Rollback(ctx, destroyMoreRecent)
	if err != nil {
		return fmt.Errorf("failed_to_rollback_snapshot: %v", err)
	}

	s.SignalDSChange(dataset.Pool, dataset.Name, "snapshot", "rollback")

	return nil
}
