// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package replication

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	replicationServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/replication"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/hashicorp/raft"
	"github.com/robfig/cron/v3"
)

func (s *Service) RegisterJobs() {
	db.QueueRegisterJSON("replication-backup-job-run", func(ctx context.Context, payload replicationServiceInterfaces.BackupJobRunPayload) error {
		if err := s.runBackupJobByID(ctx, payload.JobID, payload.Manual); err != nil {
			logger.L.Warn().Err(err).Uint("job_id", payload.JobID).Msg("queued_backup_job_run_failed")
		}
		return nil
	})
}

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

	now := time.Now().UTC()
	localNodeID := s.localNodeID()
	var jobs []clusterModels.BackupJob
	if err := s.DB.Preload("Target").Where("enabled = ?", true).Find(&jobs).Error; err != nil {
		return err
	}

	for i := range jobs {
		job := jobs[i]
		if !s.isLocalBackupJobRunner(&job, localNodeID) {
			continue
		}

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

		// Update next_run_at immediately before launching to prevent duplicate triggers
		nextRunAt := nextAt
		if err := s.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", job.ID).Update("next_run_at", nextRunAt).Error; err != nil {
			logger.L.Warn().Err(err).Uint("job_id", job.ID).Msg("failed_to_update_next_run_at")
			continue
		}

		enqueueCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = db.EnqueueJSON(enqueueCtx, "replication-backup-job-run", replicationServiceInterfaces.BackupJobRunPayload{
			JobID:  job.ID,
			Manual: false,
		})
		cancel()

		if err != nil {
			logger.L.Warn().Err(err).Uint("job_id", job.ID).Msg("failed_to_enqueue_backup_job")
		}
	}

	return nil
}

// RunBackupJobByID enqueues a manual backup job run via the queue system.
// This is the public API for triggering backup jobs from handlers.
func (s *Service) RunBackupJobByID(ctx context.Context, jobID uint) error {
	if jobID == 0 {
		return fmt.Errorf("invalid_job_id")
	}

	return db.EnqueueJSON(ctx, "replication-backup-job-run", replicationServiceInterfaces.BackupJobRunPayload{
		JobID:  jobID,
		Manual: true,
	})
}

func (s *Service) runBackupJobByID(ctx context.Context, jobID uint, manual bool) error {
	if jobID == 0 {
		return fmt.Errorf("invalid_job_id")
	}

	if !s.acquireJob(jobID) {
		return fmt.Errorf("backup_job_already_running")
	}
	logger.L.Debug().Uint("job_id", jobID).Msg("backup_job_acquired")
	defer func() {
		s.releaseJob(jobID)
		logger.L.Debug().Uint("job_id", jobID).Msg("backup_job_released")
	}()

	var job clusterModels.BackupJob
	if err := s.DB.Preload("Target").First(&job, jobID).Error; err != nil {
		return err
	}

	if !s.isLocalBackupJobRunner(&job, s.localNodeID()) {
		return fmt.Errorf("backup_job_not_assigned_to_this_node")
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
	case clusterModels.BackupJobModeJail:
		runErr = s.runJailBackupJob(ctx, &job)
	default:
		runErr = fmt.Errorf("invalid_backup_job_mode")
	}

	logger.L.Debug().Uint("job_id", job.ID).Err(runErr).Msg("backup_job_completed")
	s.updateBackupJobResult(&job, runErr)
	return runErr
}

func (s *Service) runJailBackupJob(ctx context.Context, job *clusterModels.BackupJob) error {
	src := strings.TrimSpace(job.JailRootDataset)
	if src == "" {
		return fmt.Errorf("jail_root_dataset_required")
	}

	logger.L.Info().Str("source", src).Str("destination", job.DestinationDataset).Msg("backing_up_jail_dataset")

	_, err := s.replicateDatasetToNode(
		ctx,
		src,
		job.DestinationDataset,
		job.Target.Endpoint,
		job.Force,
		job.WithIntermediates,
		&job.ID,
	)
	return err
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

func (s *Service) localNodeID() string {
	if s.Cluster == nil {
		return ""
	}
	detail := s.Cluster.Detail()
	if detail == nil {
		return ""
	}
	return strings.TrimSpace(detail.NodeID)
}

func (s *Service) isLocalBackupJobRunner(job *clusterModels.BackupJob, localNodeID string) bool {
	if job == nil {
		return false
	}

	runner := strings.TrimSpace(job.RunnerNodeID)
	if runner == "" {
		if s.Cluster != nil && s.Cluster.Raft != nil {
			return s.Cluster.Raft.State() == raft.Leader
		}
		return true
	}

	if localNodeID == "" {
		return false
	}

	return localNodeID == runner
}
