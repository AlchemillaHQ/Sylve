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

	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/alchemillahq/sylve/pkg/zfs"

	"github.com/robfig/cron/v3"
)

func (s *Service) DeleteSnapshot(guid string, recursive bool) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	datasets, err := zfs.Snapshots("")

	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		properties, err := dataset.GetAllProperties()
		if err != nil {
			return err
		}

		for _, v := range properties {
			if v == guid {
				var err error

				if recursive {
					err = dataset.Destroy(zfs.DestroyRecursive)
				} else {
					err = dataset.Destroy(zfs.DestroyDefault)
				}

				if err != nil {
					return err
				}

				return nil
			}
		}
	}

	return fmt.Errorf("snapshot with guid %s not found", guid)
}

func (s *Service) CreateSnapshot(guid string, name string, recursive bool) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	datasets, err := zfs.Datasets("")
	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		if dataset.Name == dataset.Name+"@"+name {
			return fmt.Errorf("snapshot with name %s already exists", name)
		}

		properties, err := dataset.GetAllProperties()
		if err != nil {
			return err
		}

		for k, v := range properties {
			if k == "guid" {
				if v == guid {
					shot, err := dataset.Snapshot(name, recursive)
					if err != nil {
						return err
					}

					if shot.Name == dataset.Name+"@"+name {
						return nil
					}
				}
			}
		}
	}

	return fmt.Errorf("dataset with guid %s not found", guid)
}

func (s *Service) GetPeriodicSnapshots() ([]zfsModels.PeriodicSnapshot, error) {
	var snapshots []zfsModels.PeriodicSnapshot

	if err := s.DB.Find(&snapshots).Error; err != nil {
		return nil, err
	}

	return snapshots, nil
}

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

func validateAndNormalizeRetention(req zfsServiceInterfaces.CreatePeriodicSnapshotJobRequest) (retentionType, retentionValues, error) {
	simplePresent := req.KeepLast != nil || req.MaxAgeDays != nil
	gfsPresent := req.KeepHourly != nil || req.KeepDaily != nil ||
		req.KeepWeekly != nil || req.KeepMonthly != nil || req.KeepYearly != nil

	if simplePresent && gfsPresent {
		return "", retentionValues{}, fmt.Errorf("retention_conflict: simple and GFS cannot be set together")
	}

	val := retentionValues{
		KeepLast:    utils.IntOrZero(req.KeepLast),
		MaxAgeDays:  utils.IntOrZero(req.MaxAgeDays),
		KeepHourly:  utils.IntOrZero(req.KeepHourly),
		KeepDaily:   utils.IntOrZero(req.KeepDaily),
		KeepWeekly:  utils.IntOrZero(req.KeepWeekly),
		KeepMonthly: utils.IntOrZero(req.KeepMonthly),
		KeepYearly:  utils.IntOrZero(req.KeepYearly),
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

func (s *Service) AddPeriodicSnapshot(req zfsServiceInterfaces.CreatePeriodicSnapshotJobRequest) error {
	var interval int
	if req.Interval != nil {
		interval = *req.Interval
	}
	cronExpr := req.CronExpr

	var recursive bool
	if req.Recursive != nil {
		recursive = *req.Recursive
	}

	_, rvals, err := validateAndNormalizeRetention(req)
	if err != nil {
		return err
	}

	dataset, err := s.GetFsOrVolByGUID(req.GUID)
	if err != nil {
		return err
	}

	properties, err := dataset.GetAllProperties()
	if err != nil {
		return err
	}

	for k, v := range properties {
		if k == "guid" && v == req.GUID {
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
			return nil
		}
	}

	return fmt.Errorf("dataset with guid %s not found", req.GUID)
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
	datasetName string,
	allSets []*zfs.Dataset,
) {
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
	pfx := job.Prefix + "-"
	prefixWithDataset := datasetName + "@"

	for _, ds := range allSets {
		if !strings.HasPrefix(ds.Name, prefixWithDataset) {
			continue
		}
		if !strings.Contains(ds.Name, "@"+pfx) {
			continue
		}
		if t, ok := parseSnapshotTime(datasetName, job.Prefix, ds.Name); ok {
			snaps = append(snaps, zfsServiceInterfaces.RetentionSnapInfo{
				Name:    ds.Name,
				Dataset: ds,
				Time:    t,
			})
		}
	}

	if len(snaps) == 0 {
		return
	}

	sort.Slice(snaps, func(i, j int) bool { return snaps[i].Time.After(snaps[j].Time) })

	type key = *zfs.Dataset
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

		if err := sn.Dataset.Destroy(zfs.DestroyDefault); err != nil {
			logger.L.Debug().Err(err).Msgf("Failed to prune snapshot %s", sn.Name)
			continue
		}
		logger.L.Debug().Msgf("Pruned snapshot %s", sn.Name)
	}
}

func (s *Service) StartSnapshotScheduler(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)

	go func() {
		for {
			select {
			case <-ticker.C:
				var snapshotJobs []zfsModels.PeriodicSnapshot
				if err := s.DB.Find(&snapshotJobs).Error; err != nil {
					logger.L.Debug().Err(err).Msg("Failed to load snapshotJobs")
					continue
				}

				now := time.Now()

				for _, job := range snapshotJobs {
					shouldRun := false

					if job.CronExpr != "" {
						sched, err := cron.ParseStandard(job.CronExpr)
						if err != nil {
							logger.L.Debug().Err(err).Msgf("Invalid cron expression for job %s", job.GUID)
							continue
						}

						nextRun := sched.Next(job.LastRunAt)
						if job.LastRunAt.IsZero() || now.After(nextRun) {
							shouldRun = true
						}
					} else if job.Interval > 0 {
						if job.LastRunAt.IsZero() || now.Sub(job.LastRunAt).Seconds() >= float64(job.Interval) {
							shouldRun = true
						}
					} else {
						logger.L.Debug().Msgf("Skipping job %s: no valid interval or cronExpr", job.GUID)
						continue
					}

					if !shouldRun {
						continue
					}

					allSets, err := zfs.Snapshots("")
					if err != nil {
						logger.L.Debug().Err(err).Msgf("Failed to get snapshots for %s", job.GUID)
						continue
					}

					name := job.Prefix + "-" + now.Format("2006-01-02-15-04")
					dataset, err := s.GetDatasetByGUID(job.GUID)
					if err != nil {
						logger.L.Debug().Err(err).Msgf("Failed to get dataset for %s", job.GUID)
						if err := s.DB.Delete(&job).Error; err != nil {
							logger.L.Debug().Err(err).Msgf("Failed to delete job %s", job.GUID)
						}

						logger.L.Debug().Msgf("Deleted job %s due to missing dataset", job.GUID)
						continue
					}

					snapshotExists := false
					for _, v := range allSets {
						if v.Name == dataset.Name+"@"+name {
							snapshotExists = true
							break
						}
					}

					if snapshotExists {
						logger.L.Debug().Msgf("Snapshot %s already exists", name)
						continue
					}

					if err := s.CreateSnapshot(job.GUID, name, job.Recursive); err != nil {
						logger.L.Debug().Err(err).Msgf("Failed to create snapshot for %s", job.GUID)
						continue
					}

					if err := s.DB.Model(&job).Update("LastRunAt", now).Error; err != nil {
						logger.L.Debug().Err(err).Msgf("Failed to update LastRunAt for %d", job.ID)
					}

					logger.L.Debug().Msgf("Snapshot %s created successfully", name)

					go s.pruneSnapshots(ctx, job, dataset.Name, allSets)
				}
			case <-ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()
}

func (s *Service) RollbackSnapshot(guid string, destroyMoreRecent bool) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	datasets, err := zfs.Snapshots("")
	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		properties, err := dataset.GetAllProperties()
		if err != nil {
			return err
		}

		for _, v := range properties {
			if v == guid {
				err := dataset.Rollback(destroyMoreRecent)
				if err != nil {
					return err
				}
				return nil
			}
		}
	}

	return fmt.Errorf("snapshot with guid %s not found", guid)
}
