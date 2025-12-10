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
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
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

type retentionValues struct {
	KeepLast, MaxAgeDays              int
	KeepHourly, KeepDaily, KeepWeekly int
	KeepMonthly, KeepYearly           int
}

func (s *Service) CreateSnapshot(ctx context.Context, guid string, name string, recursive bool) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	dataset, err := s.GZFS.ZFS.GetByGUID(ctx, guid, false)
	if err != nil {
		return err
	}

	shot, err := dataset.Snapshot(ctx, name, recursive)
	if err != nil {
		return err
	}

	if shot.Name == dataset.Name+"@"+name {
		return nil
	}

	return fmt.Errorf("snapshot_creation_failed")
}

func (s *Service) DeleteSnapshot(ctx context.Context, guid string, recursive bool) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	dataset, err := s.GZFS.ZFS.GetByGUID(ctx, guid, false)

	if err != nil {
		return err
	}

	return dataset.Destroy(ctx, recursive, false)
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

	_, err = s.GZFS.ZFS.GetByGUID(ctx, req.GUID, false)
	if err != nil {
		return fmt.Errorf("dataset_with_guid_not_found")
	}

	snapshot := zfsModels.PeriodicSnapshot{
		GUID:      req.GUID,
		Prefix:    req.Prefix,
		Recursive: recursive,
		Interval:  interval,
		CronExpr:  cronExpr,

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

		ds, err := s.GZFS.ZFS.GetByGUID(ctx, req.GUID, false)
		if err != nil {
			return err
		}

		full := ds.Name + "@" + name
		exists, _ := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeSnapshot, false, full)

		if exists == nil || len(exists) == 0 {
			_, err := ds.Snapshot(ctx, name, recursive)
			if err != nil {
				logger.L.Warn().Err(err).Msgf("Failed to create initial snapshot %s", full)
			} else {
				logger.L.Debug().Msgf("Initial boundary snapshot created: %s", full)
			}
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
		if gfsPresent { // client sent some GFS fields in this request (invalid), already blocked by validator
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
		if simplePresent { // invalid mix; validator should have errored already
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
						runAtLocal time.Time // for cron path (local time boundary)
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
							runAtLocal = nowLocal.Truncate(iv) // local boundary
							shouldRun = true
						} else {
							lastLocal := job.LastRunAt.In(time.Local)
							dueLocal := lastLocal.Add(iv) // step in LOCAL time
							if !nowLocal.Before(dueLocal) {
								runAtLocal = dueLocal
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

					// Decide the boundary stamp and the value to persist in LastRunAt.
					boundaryLocal := runAtLocal
					persistTime := runAtLocal.UTC()

					if job.CronExpr != "" {
						boundaryLocal = runAtLocal
						persistTime = runAtLocal // cron can stay local if you prefer
					} else {
						boundaryLocal = runAtLocal     // interval: we computed it in local
						persistTime = runAtLocal.UTC() // but persist UTC
					}

					// Name with boundary time (predictable, aligned).
					name := job.Prefix + "-" + boundaryLocal.Format("2006-01-02-15-04")
					dataset, err := s.GZFS.ZFS.GetByGUID(ctx, job.GUID, false)
					if err != nil {
						logger.L.Debug().Err(err).Msgf("Failed to get dataset for %s", job.GUID)
						// Remove the job if the dataset vanished.
						if err := s.DB.Delete(&job).Error; err != nil {
							logger.L.Debug().Err(err).Msgf("Failed to delete job %s", job.GUID)
						}
						logger.L.Debug().Msgf("Deleted job %s due to missing dataset", job.GUID)
						continue
					}

					full := dataset.Name + "@" + name

					exists, _ := s.GZFS.ZFS.Get(ctx, full, false)
					if exists != nil {
						logger.L.Debug().Msgf("Snapshot %s already exists", name)
						// Still move LastRunAt forward to the boundary we processed.
						if err := s.DB.Model(&job).Update("LastRunAt", persistTime).Error; err != nil {
							logger.L.Debug().Err(err).Msgf("Failed to update LastRunAt for %d", job.ID)
						}
						continue
					}

					if _, err := dataset.Snapshot(ctx, name, job.Recursive); err != nil {
						logger.L.Debug().Err(err).Msgf("Failed to create snapshot for %s", job.GUID)
						continue
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

	return nil
}
