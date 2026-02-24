// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

type Service struct {
	DB      *gorm.DB
	Cluster *cluster.Service
	Jail    jailServiceInterfaces.JailServiceInterface

	jobMu       sync.Mutex
	runningJobs map[uint]struct{}
}

var SSHKeyDirectory string

func NewService(db *gorm.DB, clusterService *cluster.Service, jailService jailServiceInterfaces.JailServiceInterface) *Service {
	return &Service{
		DB:          db,
		Cluster:     clusterService,
		Jail:        jailService,
		runningJobs: make(map[uint]struct{}),
	}
}

func GetSSHKeyDir() (string, error) {
	if SSHKeyDirectory != "" {
		return SSHKeyDirectory, nil
	}

	data, err := config.GetDataPath()
	if err != nil {
		return "", fmt.Errorf("get_data_path_failed: %w", err)
	}

	if data != "" {
		SSHKeyDirectory = filepath.Join(data, "ssh")
	}

	if err := os.MkdirAll(SSHKeyDirectory, 0700); err != nil {
		return "", fmt.Errorf("create_ssh_key_dir: %w", err)
	}

	return SSHKeyDirectory, nil
}

func SaveSSHKey(targetID uint, keyData string) (string, error) {
	sshDir, err := GetSSHKeyDir()
	if err != nil {
		return "", err
	}

	keyPath := filepath.Join(sshDir, fmt.Sprintf("target-%d_id", targetID))
	content := strings.TrimSpace(keyData) + "\n"
	if err := os.WriteFile(keyPath, []byte(content), 0600); err != nil {
		return "", fmt.Errorf("write_ssh_key: %w", err)
	}

	return keyPath, nil
}

func ensureSSHKeyFileAtPath(keyPath, keyData string) error {
	trimmed := strings.TrimSpace(keyData)
	if keyPath == "" || trimmed == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		return fmt.Errorf("create_ssh_key_parent_dir: %w", err)
	}

	if err := os.WriteFile(keyPath, []byte(trimmed+"\n"), 0600); err != nil {
		return fmt.Errorf("write_ssh_key: %w", err)
	}

	return nil
}

func recoverDefaultSSHKeyPath(targetID uint) (string, error) {
	if targetID == 0 {
		return "", nil
	}

	sshDir, err := GetSSHKeyDir()
	if err != nil {
		return "", fmt.Errorf("resolve_ssh_key_dir: %w", err)
	}

	path := filepath.Join(sshDir, fmt.Sprintf("target-%d_id", targetID))
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", nil
	}

	return path, nil
}

func recoverSingleSSHKeyPathCandidate() (string, error) {
	sshDir, err := GetSSHKeyDir()
	if err != nil {
		return "", fmt.Errorf("resolve_ssh_key_dir: %w", err)
	}

	entries, err := os.ReadDir(sshDir)
	if err != nil {
		return "", nil
	}

	candidates := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if !strings.HasPrefix(name, "target-") || !strings.HasSuffix(name, "_id") {
			continue
		}
		candidates = append(candidates, filepath.Join(sshDir, name))
	}

	if len(candidates) != 1 {
		return "", nil
	}

	return candidates[0], nil
}

func (s *Service) ensureBackupTargetSSHKeyMaterialized(target *clusterModels.BackupTarget) error {
	if target == nil {
		return fmt.Errorf("backup_target_required")
	}

	target.SSHKeyPath = strings.TrimSpace(target.SSHKeyPath)
	keyData := strings.TrimSpace(target.SSHKey)

	if keyData == "" {
		if target.SSHKeyPath == "" {
			recoveredPath, err := recoverDefaultSSHKeyPath(target.ID)
			if err != nil {
				return fmt.Errorf("recover_target_ssh_key_path id=%d: %w", target.ID, err)
			}
			if recoveredPath != "" {
				target.SSHKeyPath = recoveredPath
				if s != nil && s.DB != nil && target.ID != 0 {
					if err := s.DB.Model(&clusterModels.BackupTarget{}).Where("id = ?", target.ID).Update("ssh_key_path", recoveredPath).Error; err != nil {
						return fmt.Errorf("persist_target_ssh_key_path id=%d: %w", target.ID, err)
					}
				}
			}

			if target.SSHKeyPath == "" && s != nil && s.DB != nil {
				var targetCount int64
				if err := s.DB.Model(&clusterModels.BackupTarget{}).Count(&targetCount).Error; err == nil && targetCount == 1 {
					recoveredSinglePath, recErr := recoverSingleSSHKeyPathCandidate()
					if recErr != nil {
						return fmt.Errorf("recover_target_ssh_key_path id=%d: %w", target.ID, recErr)
					}
					if recoveredSinglePath != "" {
						target.SSHKeyPath = recoveredSinglePath
						if target.ID != 0 {
							if err := s.DB.Model(&clusterModels.BackupTarget{}).Where("id = ?", target.ID).Update("ssh_key_path", recoveredSinglePath).Error; err != nil {
								return fmt.Errorf("persist_target_ssh_key_path id=%d: %w", target.ID, err)
							}
						}
					}
				}
			}
		}

		return nil
	}

	if target.SSHKeyPath == "" {
		generatedPath, err := SaveSSHKey(target.ID, keyData)
		if err != nil {
			return fmt.Errorf("materialize_target_ssh_key id=%d: %w", target.ID, err)
		}

		target.SSHKeyPath = generatedPath
		if s != nil && s.DB != nil && target.ID != 0 {
			if err := s.DB.Model(&clusterModels.BackupTarget{}).Where("id = ?", target.ID).Update("ssh_key_path", generatedPath).Error; err != nil {
				return fmt.Errorf("persist_target_ssh_key_path id=%d: %w", target.ID, err)
			}
		}

		return nil
	}

	if err := ensureSSHKeyFileAtPath(target.SSHKeyPath, keyData); err != nil {
		return fmt.Errorf("materialize_target_ssh_key id=%d: %w", target.ID, err)
	}

	return nil
}

func (s *Service) ReconcileBackupTargetSSHKeys() error {
	if s.Cluster == nil {
		return nil
	}

	targets, err := s.Cluster.ListBackupTargetsForSync()
	if err != nil {
		return err
	}

	for i := range targets {
		if err := s.ensureBackupTargetSSHKeyMaterialized(&targets[i]); err != nil {
			return err
		}
	}

	return nil
}

func RemoveSSHKey(targetID uint) {
	sshDir, err := GetSSHKeyDir()
	if err != nil {
		logger.L.Warn().Err(err).Uint("target_id", targetID).Msg("failed_to_get_ssh_key_dir_for_removal")
		return
	}

	keyPath := filepath.Join(sshDir, fmt.Sprintf("target-%d_id", targetID))
	_ = os.Remove(keyPath)
}

func (s *Service) ValidateTarget(ctx context.Context, target *clusterModels.BackupTarget) error {
	sshArgs := s.buildSSHArgs(target)
	sshArgs = append(sshArgs, target.SSHHost, "zfs", "list", "-H", "-o", "name", "-t", "filesystem", "-d", "0", target.BackupRoot)

	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		sshArgs2 := s.buildSSHArgs(target)
		sshArgs2 = append(sshArgs2, target.SSHHost, "zfs", "version")
		output2, err2 := utils.RunCommandWithContext(ctx, "ssh", sshArgs2...)
		if err2 != nil {
			return fmt.Errorf("ssh_connection_failed: %s: %s", err2, output2)
		}
		return fmt.Errorf("backup_root_not_found: dataset '%s' does not exist on target (but SSH connection works): %s", target.BackupRoot, output)
	}

	_ = output
	return nil
}

// Backup runs a zelta backup from source to the target endpoint.
func (s *Service) Backup(ctx context.Context, target *clusterModels.BackupTarget, sourceDataset, destSuffix string) (string, error) {
	return s.BackupWithTarget(ctx, target, sourceDataset, destSuffix)
}

func (s *Service) backupWithEventProgress(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset, destSuffix string,
	eventID uint,
) (string, error) {
	zeltaEndpoint := target.ZeltaEndpoint(destSuffix)
	extraEnv := s.buildZeltaEnv(target)
	extraEnv = setEnvValue(extraEnv, "ZELTA_LOG_LEVEL", "3")

	return runZeltaWithEnvStreaming(
		ctx,
		extraEnv,
		func(line string) {
			if err := s.AppendBackupEventOutput(eventID, line); err != nil {
				logger.L.Warn().
					Uint("event_id", eventID).
					Err(err).
					Msg("append_backup_event_output_failed")
			}
		},
		"backup",
		"--json",
		sourceDataset,
		zeltaEndpoint,
	)
}

// Match runs zelta match to compare source and target datasets.
func (s *Service) Match(ctx context.Context, target *clusterModels.BackupTarget, sourceDataset, destSuffix string) (string, error) {
	return s.MatchWithTarget(ctx, target, sourceDataset, destSuffix)
}

// Rotate runs zelta rotate to preserve diverged target history and rebase backups.
func (s *Service) Rotate(ctx context.Context, target *clusterModels.BackupTarget, sourceDataset, destSuffix string) (string, error) {
	return s.RotateWithTarget(ctx, target, sourceDataset, destSuffix)
}

// RenameTargetDatasetForBootstrap preserves an existing non-replica target dataset
// by renaming it out of the way so a fresh full backup can proceed.
func (s *Service) RenameTargetDatasetForBootstrap(ctx context.Context, target *clusterModels.BackupTarget, destSuffix string) (string, string, error) {
	currentDataset := strings.TrimSpace(target.BackupRoot)
	suffix := strings.TrimSpace(destSuffix)
	if suffix != "" {
		currentDataset = currentDataset + "/" + suffix
	}
	if currentDataset == "" {
		return "", "", fmt.Errorf("target_dataset_required")
	}

	renamedDataset := fmt.Sprintf("%s.pre_sylve_%d", currentDataset, time.Now().UTC().UnixNano())

	sshArgs := s.buildSSHArgs(target)
	sshArgs = append(sshArgs, target.SSHHost, "zfs", "rename", currentDataset, renamedDataset)

	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		return currentDataset, renamedDataset, fmt.Errorf("target_dataset_rename_failed: %s: %w", strings.TrimSpace(output), err)
	}

	return currentDataset, renamedDataset, nil
}

// backupJobPayload is the goqite queue payload for a backup job.
type backupJobPayload struct {
	JobID uint `json:"job_id"`
}

const backupJobQueueName = "zelta-backup-run"

// RegisterJobs registers the backup job queue handler with goqite.
func (s *Service) RegisterJobs() {
	db.QueueRegisterJSON(backupJobQueueName, func(ctx context.Context, payload backupJobPayload) error {
		if payload.JobID == 0 {
			return fmt.Errorf("invalid_job_id_in_queue_payload")
		}

		var job clusterModels.BackupJob
		if err := s.DB.Preload("Target").First(&job, payload.JobID).Error; err != nil {
			logger.L.Warn().Err(err).Uint("job_id", payload.JobID).Msg("queued_backup_job_not_found")
			return fmt.Errorf("backup_job_not_found: %w", err)
		}

		if err := s.runBackupJob(ctx, &job); err != nil {
			logger.L.Warn().Err(err).Uint("job_id", payload.JobID).Msg("queued_backup_job_failed")
			return err
		}

		return nil
	})

	s.registerRestoreJob()
	s.registerRestoreFromTargetJob()
}

// Run is a no-op for interface compatibility (Zelta doesn't need a listener).
func (s *Service) Run(ctx context.Context) {
	<-ctx.Done()
}

// StartBackupScheduler runs the cron-based backup scheduler.
func (s *Service) StartBackupScheduler(ctx context.Context) {
	if err := s.ReconcileBackupTargetSSHKeys(); err != nil {
		logger.L.Warn().Err(err).Msg("failed_to_reconcile_backup_target_ssh_keys")
	}

	if err := s.CleanupStaleEvents(ctx, 15*time.Minute); err != nil {
		logger.L.Warn().Err(err).Msg("failed_to_cleanup_stale_backup_events")
	}

	ticker := time.NewTicker(30 * time.Second)
	cleanupTicker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.runBackupSchedulerTick(ctx); err != nil {
				logger.L.Warn().Err(err).Msg("backup_scheduler_tick_failed")
			}
		case <-cleanupTicker.C:
			if err := s.ReconcileBackupTargetSSHKeys(); err != nil {
				logger.L.Warn().Err(err).Msg("periodic_backup_target_ssh_key_reconcile_failed")
			}
			if err := s.CleanupStaleEvents(ctx, 15*time.Minute); err != nil {
				logger.L.Warn().Err(err).Msg("periodic_stale_event_cleanup_failed")
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
	if err := s.DB.Preload("Target").Where("enabled = ? AND COALESCE(cron_expr, '') != ''", true).Find(&jobs).Error; err != nil {
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

		if err := s.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", job.ID).Update("next_run_at", nextAt).Error; err != nil {
			logger.L.Warn().Err(err).Uint("job_id", job.ID).Msg("failed_to_update_next_run_at")
			continue
		}

		enqueueCtx, enqueueCancel := context.WithTimeout(ctx, 5*time.Second)
		if err := db.EnqueueJSON(enqueueCtx, backupJobQueueName, backupJobPayload{JobID: job.ID}); err != nil {
			logger.L.Warn().Err(err).Uint("job_id", job.ID).Msg("failed_to_enqueue_scheduled_backup")
		}
		enqueueCancel()
	}

	return nil
}

// EnqueueBackupJob enqueues a backup job for async execution via goqite.
func (s *Service) EnqueueBackupJob(ctx context.Context, jobID uint) error {
	if jobID == 0 {
		return fmt.Errorf("invalid_job_id")
	}

	// Verify job exists before enqueuing.
	var job clusterModels.BackupJob
	if err := s.DB.Preload("Target").First(&job, jobID).Error; err != nil {
		return err
	}

	if !s.acquireJob(jobID) {
		return fmt.Errorf("backup_job_already_running")
	}
	s.releaseJob(jobID) // Release immediately; the queue handler will re-acquire.

	return db.EnqueueJSON(ctx, backupJobQueueName, backupJobPayload{JobID: jobID})
}

func (s *Service) runBackupJob(ctx context.Context, job *clusterModels.BackupJob) error {
	if !s.acquireJob(job.ID) {
		return fmt.Errorf("backup_job_already_running")
	}
	defer s.releaseJob(job.ID)

	if job.StopBeforeBackup {
		fmt.Println("StopBeforeBackup is checked for job", job.ID)
	}

	if err := s.ensureBackupTargetSSHKeyMaterialized(&job.Target); err != nil {
		runErr := fmt.Errorf("backup_target_ssh_key_materialize_failed: %w", err)
		s.updateBackupJobResult(job, runErr)
		return runErr
	}

	if !job.Target.Enabled {
		runErr := fmt.Errorf("backup_target_disabled")
		s.updateBackupJobResult(job, runErr)
		return runErr
	}

	event := clusterModels.BackupEvent{
		JobID:     &job.ID,
		Mode:      job.Mode,
		Status:    "running",
		StartedAt: time.Now().UTC(),
	}

	var sourceDataset string
	switch job.Mode {
	case clusterModels.BackupJobModeDataset:
		sourceDataset = strings.TrimSpace(job.SourceDataset)
		if sourceDataset == "" {
			runErr := fmt.Errorf("source_dataset_required")
			s.updateBackupJobResult(job, runErr)
			return runErr
		}
	case clusterModels.BackupJobModeJail:
		sourceDataset = strings.TrimSpace(job.JailRootDataset)
		if sourceDataset == "" {
			runErr := fmt.Errorf("jail_root_dataset_required")
			s.updateBackupJobResult(job, runErr)
			return runErr
		}
	default:
		runErr := fmt.Errorf("invalid_backup_job_mode")
		s.updateBackupJobResult(job, runErr)
		return runErr
	}

	event.SourceDataset = sourceDataset

	destSuffix := strings.TrimSpace(job.DestSuffix)
	if destSuffix == "" {
		destSuffix = autoDestSuffix(sourceDataset)
	}

	event.TargetEndpoint = job.Target.ZeltaEndpoint(destSuffix)
	s.DB.Create(&event)

	logger.L.Info().
		Uint("job_id", job.ID).
		Str("source", sourceDataset).
		Str("target", event.TargetEndpoint).
		Msg("starting_zelta_backup")

	appendOutput := func(current, next string) string {
		next = strings.TrimSpace(next)
		if next == "" {
			return current
		}
		if strings.TrimSpace(current) == "" {
			return next
		}
		return strings.TrimSpace(current) + "\n" + next
	}

	var ctId uint

	if job.StopBeforeBackup {
		if job.Mode == clusterModels.BackupJobModeJail {
			var err error

			ctId, err = s.Jail.GetJailCTIDFromDataset(job.JailRootDataset)
			if err != nil {
				runErr := fmt.Errorf("failed_to_get_jail_ctid: %w", err)
				s.updateBackupJobResult(job, runErr)
				return runErr
			}

			err = s.Jail.JailAction(int(ctId), "stop")
			if err != nil {
				runErr := fmt.Errorf("failed_to_stop_jail: %w", err)
				s.updateBackupJobResult(job, runErr)
				return runErr
			}
		}
	}

	output, runErr := s.backupWithEventProgress(ctx, &job.Target, sourceDataset, destSuffix, event.ID)
	if runErr == nil {
		outcome := classifyBackupOutput(output)
		if code := outcome.errorCode(); code != "" {
			runErr = errors.New(code)
		} else if outcome == backupOutputUpToDate {
			logger.L.Info().
				Uint("job_id", job.ID).
				Str("source", sourceDataset).
				Str("target", event.TargetEndpoint).
				Msg("backup_up_to_date_noop")
		}
	}

	if runErr != nil && shouldAutoRotateBackupErrorCode(runErr.Error()) {
		logger.L.Info().
			Uint("job_id", job.ID).
			Str("source", sourceDataset).
			Str("target", event.TargetEndpoint).
			Str("reason", runErr.Error()).
			Msg("backup_auto_rotate_starting")

		rotateOutput, rotateErr := s.Rotate(ctx, &job.Target, sourceDataset, destSuffix)
		output = appendOutput(output, rotateOutput)
		if rotateErr != nil {
			if shouldRenameTargetAfterRotateFailure(rotateOutput) {
				fromDataset, renamedDataset, renameErr := s.RenameTargetDatasetForBootstrap(ctx, &job.Target, destSuffix)
				if renameErr != nil {
					output = appendOutput(output, fmt.Sprintf("auto_rename_target_failed: %s", renameErr))
					runErr = fmt.Errorf("backup_auto_rotate_failed: %w", rotateErr)
				} else {
					logger.L.Info().
						Uint("job_id", job.ID).
						Str("source", sourceDataset).
						Str("from_dataset", fromDataset).
						Str("renamed_dataset", renamedDataset).
						Msg("backup_auto_target_renamed_for_bootstrap")
					output = appendOutput(output, fmt.Sprintf("auto_renamed_target_dataset: %s -> %s", fromDataset, renamedDataset))

					retryOutput, retryErr := s.backupWithEventProgress(ctx, &job.Target, sourceDataset, destSuffix, event.ID)
					output = appendOutput(output, retryOutput)
					runErr = retryErr
					if runErr == nil {
						retryOutcome := classifyBackupOutput(retryOutput)
						if code := retryOutcome.errorCode(); code != "" {
							runErr = errors.New(code)
						} else if retryOutcome == backupOutputUpToDate {
							logger.L.Info().
								Uint("job_id", job.ID).
								Str("source", sourceDataset).
								Str("target", event.TargetEndpoint).
								Msg("backup_up_to_date_noop_after_target_rename")
						}
					}
					if runErr == nil {
						logger.L.Info().
							Uint("job_id", job.ID).
							Str("source", sourceDataset).
							Str("target", event.TargetEndpoint).
							Msg("backup_completed_after_target_rename")
					}
				}
			} else {
				runErr = fmt.Errorf("backup_auto_rotate_failed: %w", rotateErr)
			}
		} else {
			logger.L.Info().
				Uint("job_id", job.ID).
				Str("source", sourceDataset).
				Str("target", event.TargetEndpoint).
				Msg("backup_auto_rotate_completed")

			retryOutput, retryErr := s.backupWithEventProgress(ctx, &job.Target, sourceDataset, destSuffix, event.ID)
			output = appendOutput(output, retryOutput)
			runErr = retryErr
			if runErr == nil {
				retryOutcome := classifyBackupOutput(retryOutput)
				if code := retryOutcome.errorCode(); code != "" {
					runErr = errors.New(code)
				} else if retryOutcome == backupOutputUpToDate {
					logger.L.Info().
						Uint("job_id", job.ID).
						Str("source", sourceDataset).
						Str("target", event.TargetEndpoint).
						Msg("backup_up_to_date_noop_after_rotate")
				}
			}
			if runErr == nil {
				logger.L.Info().
					Uint("job_id", job.ID).
					Str("source", sourceDataset).
					Str("target", event.TargetEndpoint).
					Msg("backup_completed_after_rotate")
			}
		}
	}

	if runErr == nil && job.PruneKeepLast > 0 {
		pruneCandidates, pruneOutput, pruneErr := s.PruneCandidatesWithTarget(ctx, &job.Target, sourceDataset, destSuffix, job.PruneKeepLast)
		output = appendOutput(output, pruneOutput)

		if pruneErr != nil {
			logger.L.Warn().Err(pruneErr).Uint("job_id", job.ID).Msg("backup_prune_scan_failed")
		} else if len(pruneCandidates) > 0 {
			if err := s.DestroySnapshots(ctx, pruneCandidates); err != nil {
				logger.L.Warn().Err(err).Uint("job_id", job.ID).Int("candidate_count", len(pruneCandidates)).Msg("backup_prune_destroy_failed")
			} else {
				logger.L.Info().Uint("job_id", job.ID).Int("pruned", len(pruneCandidates)).Msg("backup_prune_completed")
			}
		} else {
			logger.L.Debug().Uint("job_id", job.ID).Int("keep_last", job.PruneKeepLast).Msg("backup_prune_no_candidates")
		}

		if job.PruneTarget {
			targetPruneCandidates, targetPruneOutput, targetPruneErr := s.PruneTargetCandidatesWithSource(ctx, &job.Target, sourceDataset, destSuffix, job.PruneKeepLast)
			output = appendOutput(output, targetPruneOutput)

			if targetPruneErr != nil {
				logger.L.Warn().Err(targetPruneErr).Uint("job_id", job.ID).Msg("backup_prune_target_scan_failed")
			} else if len(targetPruneCandidates) > 0 {
				if err := s.DestroyTargetSnapshotsByName(ctx, &job.Target, targetPruneCandidates); err != nil {
					logger.L.Warn().Err(err).Uint("job_id", job.ID).Int("candidate_count", len(targetPruneCandidates)).Msg("backup_prune_target_destroy_failed")
				} else {
					logger.L.Info().Uint("job_id", job.ID).Int("pruned", len(targetPruneCandidates)).Msg("backup_prune_target_completed")
				}
			} else {
				fallbackCandidates, fallbackErr := s.buildTargetRetentionPruneCandidates(ctx, job, job.PruneKeepLast+1)
				if fallbackErr != nil {
					logger.L.Warn().Err(fallbackErr).Uint("job_id", job.ID).Msg("backup_prune_target_retention_scan_failed")
				} else if len(fallbackCandidates) > 0 {
					if err := s.DestroyTargetSnapshotsByName(ctx, &job.Target, fallbackCandidates); err != nil {
						logger.L.Warn().Err(err).Uint("job_id", job.ID).Int("candidate_count", len(fallbackCandidates)).Msg("backup_prune_target_retention_destroy_failed")
					} else {
						logger.L.Info().Uint("job_id", job.ID).Int("pruned", len(fallbackCandidates)).Msg("backup_prune_target_retention_completed")
					}
				} else {
					logger.L.Debug().Uint("job_id", job.ID).Int("keep_last", job.PruneKeepLast).Msg("backup_prune_target_no_candidates")
				}
			}
		}
	}

	if job.StopBeforeBackup {
		if job.Mode == clusterModels.BackupJobModeJail {
			if ctId == 0 {
				runErr = fmt.Errorf("invalid_jail_ctid_for_restart")
				s.updateBackupJobResult(job, runErr)
				return runErr
			}

			if err := s.Jail.JailAction(int(ctId), "start"); err != nil {
				logger.L.Warn().Err(err).Uint("job_id", job.ID).Msg("failed_to_restart_jail_after_backup")
				output = appendOutput(output, fmt.Sprintf("failed_to_restart_jail: %s", err))
			}
		}
	}

	now := time.Now().UTC()
	event.CompletedAt = &now
	event.Output = output
	if runErr != nil {
		event.Status = "failed"
		event.Error = runErr.Error()
	} else {
		event.Status = "success"
	}

	s.DB.Save(&event)
	s.updateBackupJobResult(job, runErr)

	logger.L.Info().
		Uint("job_id", job.ID).
		Str("status", event.Status).
		Err(runErr).
		Msg("zelta_backup_completed")

	return runErr
}

func (s *Service) buildTargetRetentionPruneCandidates(ctx context.Context, job *clusterModels.BackupJob, keepCount int) ([]string, error) {
	if keepCount < 1 {
		keepCount = 1
	}

	remoteDataset := remoteDatasetForJob(job)
	snapshots, err := s.listRemoteSnapshotsForDataset(ctx, &job.Target, remoteDataset)
	if err != nil {
		return nil, err
	}

	if len(snapshots) <= keepCount {
		return []string{}, nil
	}

	deleteCount := len(snapshots) - keepCount
	candidates := make([]string, 0, deleteCount)
	for i := 0; i < deleteCount; i++ {
		name := strings.TrimSpace(snapshots[i].Name)
		if !isValidZFSSnapshotName(name) {
			continue
		}
		candidates = append(candidates, name)
	}

	return candidates, nil
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

// ListLocalBackupEvents returns recent backup events.
func (s *Service) ListLocalBackupEvents(limit int, jobID uint) ([]clusterModels.BackupEvent, error) {
	if limit <= 0 {
		limit = 200
	}

	query := s.DB.Order("started_at DESC").Limit(limit)
	if jobID > 0 {
		query = query.Where("job_id = ?", jobID)
	}

	var events []clusterModels.BackupEvent
	if err := query.Find(&events).Error; err != nil {
		return nil, err
	}

	return events, nil
}

func (s *Service) GetLocalBackupEvent(id uint) (*clusterModels.BackupEvent, error) {
	if id == 0 {
		return nil, fmt.Errorf("invalid_event_id")
	}

	var event clusterModels.BackupEvent
	if err := s.DB.First(&event, id).Error; err != nil {
		return nil, err
	}

	return &event, nil
}

func (s *Service) AppendBackupEventOutput(eventID uint, chunk string) error {
	trimmed := strings.TrimSpace(chunk)
	if eventID == 0 || trimmed == "" {
		return nil
	}

	appendChunk := trimmed + "\n"
	return s.DB.Model(&clusterModels.BackupEvent{}).
		Where("id = ?", eventID).
		Update("output", gorm.Expr("COALESCE(output, '') || ?", appendChunk)).Error
}

// ListLocalBackupEventsPaginated returns paginated backup events.
func (s *Service) ListLocalBackupEventsPaginated(page, size int, sortField, sortDir string, jobID uint, search string) (*BackupEventsResponse, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 25
	}

	query := s.DB.Model(&clusterModels.BackupEvent{})
	if jobID > 0 {
		query = query.Where("job_id = ?", jobID)
	}
	if search != "" {
		like := "%" + search + "%"
		query = query.Where("source_dataset LIKE ? OR target_endpoint LIKE ? OR status LIKE ? OR error LIKE ?", like, like, like, like)
	}

	var total int64
	query.Count(&total)

	orderClause := "started_at DESC"
	if sortField != "" {
		dir := "ASC"
		if strings.EqualFold(sortDir, "desc") {
			dir = "DESC"
		}
		allowed := map[string]bool{
			"id": true, "source_dataset": true, "target_endpoint": true,
			"mode": true, "status": true, "started_at": true, "completed_at": true,
		}
		if allowed[sortField] {
			orderClause = sortField + " " + dir
		}
	}

	var events []clusterModels.BackupEvent
	offset := (page - 1) * size
	if err := query.Order(orderClause).Offset(offset).Limit(size).Find(&events).Error; err != nil {
		return nil, err
	}

	lastPage := int(total) / size
	if int(total)%size > 0 {
		lastPage++
	}
	if lastPage < 1 {
		lastPage = 1
	}

	return &BackupEventsResponse{
		LastPage: lastPage,
		Data:     events,
	}, nil
}

// CleanupStaleEvents marks old "running" events as interrupted.
func (s *Service) CleanupStaleEvents(_ context.Context, maxAge time.Duration) error {
	cutoff := time.Now().UTC().Add(-maxAge)
	return s.DB.Model(&clusterModels.BackupEvent{}).
		Where("status = ? AND started_at < ?", "running", cutoff).
		Updates(map[string]any{
			"status":       "interrupted",
			"error":        "process_crashed_or_restarted",
			"completed_at": time.Now().UTC(),
		}).Error
}

// buildSSHArgs builds the common SSH arguments for a target.
func (s *Service) buildSSHArgs(target *clusterModels.BackupTarget) []string {
	args := []string{"-n", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new"}
	if target.SSHPort != 0 && target.SSHPort != 22 {
		args = append(args, "-p", fmt.Sprintf("%d", target.SSHPort))
	}
	if target.SSHKeyPath != "" {
		args = append(args, "-i", target.SSHKeyPath)
	}
	return args
}

// buildZeltaEnv returns the environment variables for a Zelta invocation targeting a specific host.
func (s *Service) buildZeltaEnv(target *clusterModels.BackupTarget) []string {
	sshBase := "ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new"
	if target.SSHPort != 0 && target.SSHPort != 22 {
		portArg := fmt.Sprintf(" -p %d", target.SSHPort)
		sshBase += portArg
	}
	if target.SSHKeyPath != "" {
		keyArg := fmt.Sprintf(" -i %s", target.SSHKeyPath)
		sshBase += keyArg
	}
	sshDefault := sshBase + " -n"
	sshSend := sshDefault
	sshRecv := sshBase

	return []string{
		"ZELTA_REMOTE_COMMAND=" + sshBase,
		"ZELTA_REMOTE_DEFAULT=" + sshDefault,
		"ZELTA_REMOTE_SEND=" + sshSend,
		"ZELTA_REMOTE_RECV=" + sshRecv,
		"ZELTA_LOG_MODE=json",
		"ZELTA_LOG_LEVEL=2",
	}
}

func setEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		out = append(out, entry)
	}
	out = append(out, prefix+value)
	return out
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

// BackupEventsResponse is the paginated response for backup events.
type BackupEventsResponse struct {
	LastPage int                         `json:"last_page"`
	Data     []clusterModels.BackupEvent `json:"data"`
}

// autoDestSuffix derives a destination suffix from the source dataset when the user hasn't set one.
//
//   - Jails:    ".../jails/105"             → "jails/105"
//   - VMs:      ".../virtual-machines/100"  → "virtual-machines/100"
//   - Other:    "zroot/sylve/mydata"        → "zroot-sylve-mydata"
func autoDestSuffix(source string) string {
	parts := strings.Split(source, "/")

	// Walk backwards looking for a known prefix segment.
	for i := len(parts) - 1; i >= 0; i-- {
		switch parts[i] {
		case "jails", "virtual-machines":
			// Return from this segment onward: jails/105, virtual-machines/100, etc.
			return strings.Join(parts[i:], "/")
		}
	}

	// Fallback: full source path with "/" replaced by "-".
	return strings.ReplaceAll(source, "/", "-")
}
