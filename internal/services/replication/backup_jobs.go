// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package replication

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/hashicorp/raft"
	"github.com/robfig/cron/v3"
)

func (s *Service) StartBackupScheduler(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.runBackupSchedulerTick(ctx); err != nil {
				logger.L.Warn().Err(err).Msg("backup_scheduler_tick_failed")
			}
		}
	}
}

func (s *Service) runBackupSchedulerTick(ctx context.Context) error {
	if s.DB == nil {
		return nil
	}

	if s.Cluster != nil && s.Cluster.Raft != nil && s.Cluster.Raft.State() != raft.Leader {
		return nil
	}

	now := time.Now().UTC()
	var jobs []clusterModels.BackupJob
	if err := s.DB.Preload("Target").Where("enabled = ?", true).Find(&jobs).Error; err != nil {
		return err
	}

	for i := range jobs {
		job := jobs[i]

		nextAt, err := nextRunTime(job.CronExpr, now)
		if err != nil {
			_ = s.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", job.ID).Updates(map[string]any{
				"last_status": "failed",
				"last_error":  "invalid_cron_expr",
				"next_run_at": nil,
			}).Error
			continue
		}

		if job.NextRunAt == nil {
			_ = s.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", job.ID).Update("next_run_at", nextAt).Error
			continue
		}

		if now.Before(*job.NextRunAt) {
			continue
		}

		jobID := job.ID
		go func() {
			runCtx, cancel := context.WithTimeout(ctx, 2*time.Hour)
			defer cancel()
			if err := s.runBackupJobByID(runCtx, jobID, false); err != nil {
				logger.L.Warn().Err(err).Uint("job_id", jobID).Msg("backup_job_run_failed")
			}
		}()
	}

	return nil
}

func (s *Service) RunBackupJobByID(ctx context.Context, jobID uint) error {
	return s.runBackupJobByID(ctx, jobID, true)
}

func (s *Service) runBackupJobByID(ctx context.Context, jobID uint, manual bool) error {
	if jobID == 0 {
		return fmt.Errorf("invalid_job_id")
	}

	if !s.acquireJob(jobID) {
		return fmt.Errorf("backup_job_already_running")
	}
	defer s.releaseJob(jobID)

	var job clusterModels.BackupJob
	if err := s.DB.Preload("Target").First(&job, jobID).Error; err != nil {
		return err
	}

	if !manual && !job.Enabled {
		return nil
	}

	if !job.Target.Enabled {
		runErr := fmt.Errorf("backup_target_disabled")
		s.updateBackupJobResult(&job, runErr)
		return runErr
	}

	var runErr error
	switch job.Mode {
	case clusterModels.BackupJobModeDataset:
		if strings.TrimSpace(job.SourceDataset) == "" {
			runErr = fmt.Errorf("source_dataset_required")
			break
		}
		_, runErr = s.replicateDatasetToNode(
			ctx,
			job.SourceDataset,
			job.DestinationDataset,
			job.Target.Endpoint,
			job.Force,
			job.WithIntermediates,
			&job.ID,
		)
	case clusterModels.BackupJobModeJails:
		runErr = s.runJailBackupJob(ctx, &job)
	default:
		runErr = fmt.Errorf("invalid_backup_job_mode")
	}

	s.updateBackupJobResult(&job, runErr)
	return runErr
}

func (s *Service) runJailBackupJob(ctx context.Context, job *clusterModels.BackupJob) error {
	root := strings.TrimSpace(job.JailRootDataset)
	if root == "" {
		root = "zroot/sylve/jails"
	}

	datasets, err := s.listChildDatasets(ctx, root)
	if err != nil {
		return err
	}
	if len(datasets) == 0 {
		return fmt.Errorf("no_jail_datasets_found")
	}

	sort.Strings(datasets)

	var errs []string
	for _, src := range datasets {
		rel := strings.TrimPrefix(strings.TrimPrefix(src, root), "/")
		dst := job.DestinationDataset
		if rel != "" {
			dst = strings.TrimSuffix(job.DestinationDataset, "/") + "/" + rel
		}

		if _, err := s.replicateDatasetToNode(
			ctx,
			src,
			dst,
			job.Target.Endpoint,
			job.Force,
			job.WithIntermediates,
			&job.ID,
		); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", src, err))
		}
	}

	if len(errs) == 0 {
		return nil
	}

	if len(errs) == len(datasets) {
		return fmt.Errorf("all_jail_dataset_backups_failed: %s", strings.Join(errs, " | "))
	}

	return fmt.Errorf("partial_jail_dataset_backup_failure: %s", strings.Join(errs, " | "))
}

func (s *Service) listChildDatasets(ctx context.Context, root string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "zfs", "list", "-H", "-o", "name", "-t", "filesystem", "-r", root)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if isDatasetMissingErr(err) || isDatasetMissingErr(fmt.Errorf(stderr.String())) {
			return nil, fmt.Errorf("jail_root_dataset_not_found")
		}
		return nil, fmt.Errorf("list_jail_datasets_failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	res := make([]string, 0, len(lines))
	for _, line := range lines {
		name := strings.TrimSpace(line)
		if name == "" || name == root {
			continue
		}
		res = append(res, name)
	}

	return res, nil
}

func (s *Service) updateBackupJobResult(job *clusterModels.BackupJob, runErr error) {
	now := time.Now().UTC()
	next := (*time.Time)(nil)
	if job.Enabled {
		if n, err := nextRunTime(job.CronExpr, now); err == nil {
			next = &n
		}
	}

	updates := map[string]any{
		"last_run_at": now,
		"last_status": "success",
		"last_error":  "",
		"next_run_at": next,
	}

	if runErr != nil {
		updates["last_status"] = "failed"
		updates["last_error"] = runErr.Error()
	}

	if err := s.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", job.ID).Updates(updates).Error; err != nil {
		logger.L.Warn().Err(err).Uint("job_id", job.ID).Msg("failed_to_update_backup_job_state")
	}
}

func nextRunTime(cronExpr string, now time.Time) (time.Time, error) {
	spec := strings.TrimSpace(cronExpr)
	if spec == "" {
		return time.Time{}, errors.New("cron_expr_required")
	}
	schedule, err := cron.ParseStandard(spec)
	if err != nil {
		return time.Time{}, err
	}
	return schedule.Next(now), nil
}

func (s *Service) ListLocalBackupEvents(limit int, jobID uint) ([]ReplicationEventInfo, error) {
	if jobID == 0 {
		return s.listReplicationEvents(limit, nil)
	}

	jid := jobID
	return s.listReplicationEvents(limit, &jid)
}

func (s *Service) acquireJob(jobID uint) bool {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()

	if _, exists := s.runningJobs[jobID]; exists {
		return false
	}

	s.runningJobs[jobID] = struct{}{}
	return true
}

func (s *Service) releaseJob(jobID uint) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	delete(s.runningJobs, jobID)
}
