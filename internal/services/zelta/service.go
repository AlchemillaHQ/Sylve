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
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

var (
	replicationSizeRegex = regexp.MustCompile(`"replicationSize"\s*:\s*"?([0-9]+)"?`)
	syncingSizeRegex     = regexp.MustCompile(`syncing:\s*([0-9]+(?:\.[0-9]+)?)\s*([KMGTPE]?)(i?B?)\b`)
)

// backupJobPayload is the goqite queue payload for a backup job
type backupJobPayload struct {
	JobID uint `json:"job_id"`
}

const backupJobQueueName = "zelta-backup-run"

type Service struct {
	DB      *gorm.DB
	Cluster *cluster.Service
	Jail    jailServiceInterfaces.JailServiceInterface
	Network networkServiceInterfaces.NetworkServiceInterface
	VM      libvirtServiceInterfaces.LibvirtServiceInterface
	GZFS    *gzfs.Client

	jobMu       sync.Mutex
	runningJobs map[uint]struct{}
}

type BackupEventProgress struct {
	Event           *clusterModels.BackupEvent `json:"event"`
	ProgressDataset string                     `json:"progressDataset"`
	MovedBytes      *uint64                    `json:"movedBytes"`
	TotalBytes      *uint64                    `json:"totalBytes"`
	ProgressPercent *float64                   `json:"progressPercent"`
}

type BackupEventsResponse struct {
	LastPage int                         `json:"last_page"`
	Data     []clusterModels.BackupEvent `json:"data"`
}

func NewService(
	db *gorm.DB,
	clusterService *cluster.Service,
	jailService jailServiceInterfaces.JailServiceInterface,
	networkService networkServiceInterfaces.NetworkServiceInterface,
	vmService libvirtServiceInterfaces.LibvirtServiceInterface,
	gzfsClient *gzfs.Client,
) *Service {
	return &Service{
		DB:          db,
		Cluster:     clusterService,
		Jail:        jailService,
		Network:     networkService,
		VM:          vmService,
		GZFS:        gzfsClient,
		runningJobs: make(map[uint]struct{}),
	}
}

func (s *Service) findVMByRID(rid uint) (*vmModels.VM, error) {
	if s == nil || s.DB == nil || rid == 0 {
		return nil, nil
	}

	var vm vmModels.VM
	if err := s.DB.
		Preload("Storages").
		Preload("Storages.Dataset").
		Preload("Networks").
		Preload("CPUPinning").
		Where("rid = ?", rid).
		First(&vm).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &vm, nil
}

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

func (s *Service) Match(ctx context.Context, target *clusterModels.BackupTarget, sourceDataset, destSuffix string) (string, error) {
	return s.MatchWithTarget(ctx, target, sourceDataset, destSuffix)
}

func (s *Service) Rotate(ctx context.Context, target *clusterModels.BackupTarget, sourceDataset, destSuffix string) (string, error) {
	return s.RotateWithTarget(ctx, target, sourceDataset, destSuffix)
}

// RenameTargetDatasetForBootstrap preserves an existing non-replica target dataset
// by renaming it out of the way so a fresh full backup can proceed
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

func (s *Service) Run(ctx context.Context) {
	<-ctx.Done()
}

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

func (s *Service) EnqueueBackupJob(ctx context.Context, jobID uint) error {
	if jobID == 0 {
		return fmt.Errorf("invalid_job_id")
	}

	var job clusterModels.BackupJob
	if err := s.DB.Preload("Target").First(&job, jobID).Error; err != nil {
		return err
	}

	if !s.acquireJob(jobID) {
		return fmt.Errorf("backup_job_already_running")
	}

	s.releaseJob(jobID)

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
	case clusterModels.BackupJobModeVM:
		sourceDataset = strings.TrimSpace(job.SourceDataset)
		if sourceDataset == "" {
			runErr := fmt.Errorf("source_dataset_required")
			s.updateBackupJobResult(job, runErr)
			return runErr
		}
	default:
		runErr := fmt.Errorf("invalid_backup_job_mode")
		s.updateBackupJobResult(job, runErr)
		return runErr
	}

	event.SourceDataset = sourceDataset

	vmRID := uint(0)
	vmSourceDatasets := []string{}
	if job.Mode == clusterModels.BackupJobModeVM {
		_, parsedRID := inferRestoreDatasetKind(sourceDataset)
		vmRID = parsedRID
		if vmRID == 0 {
			runErr := fmt.Errorf("invalid_vm_source_dataset")
			s.updateBackupJobResult(job, runErr)
			return runErr
		}

		sources, err := s.resolveVMBackupSourceDatasets(ctx, vmRID, sourceDataset)
		if err != nil {
			runErr := fmt.Errorf("resolve_vm_backup_sources_failed: %w", err)
			s.updateBackupJobResult(job, runErr)
			return runErr
		}
		vmSourceDatasets = sources
	}

	destSuffix := s.backupDestSuffixForMode(job.Mode, strings.TrimSpace(job.DestSuffix), sourceDataset)
	event.TargetEndpoint = job.Target.ZeltaEndpoint(destSuffix)
	if err := s.DB.Create(&event).Error; err != nil {
		runErr := fmt.Errorf("create_backup_event_failed: %w", err)
		s.updateBackupJobResult(job, runErr)
		return runErr
	}

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
	var output string
	var runErr error

	defer func() {
		s.finalizeBackupEvent(&event, runErr, output)
		s.updateBackupJobResult(job, runErr)

		logger.L.Info().
			Uint("job_id", job.ID).
			Str("status", event.Status).
			Err(runErr).
			Msg("zelta_backup_completed")
	}()

	if job.StopBeforeBackup {
		if job.Mode == clusterModels.BackupJobModeJail {
			var err error

			ctId, err = s.Jail.GetJailCTIDFromDataset(job.JailRootDataset)
			if err != nil {
				runErr = fmt.Errorf("failed_to_get_jail_ctid: %w", err)
				output = appendOutput(output, runErr.Error())
				return runErr
			}

			err = s.Jail.JailAction(int(ctId), "stop")
			if err != nil {
				runErr = fmt.Errorf("failed_to_stop_jail: %w", err)
				output = appendOutput(output, runErr.Error())
				return runErr
			}
		} else if job.Mode == clusterModels.BackupJobModeVM {
			if vmRID == 0 {
				runErr = fmt.Errorf("invalid_vm_rid_for_stop")
				output = appendOutput(output, runErr.Error())
				return runErr
			}
			if err := s.stopVMIfPresent(vmRID); err != nil {
				runErr = fmt.Errorf("failed_to_stop_vm: %w", err)
				if !strings.Contains(runErr.Error(), "domain is not running") {
					output = appendOutput(output, runErr.Error())
					return runErr
				}
			}
		}
	}

	if job.Mode == clusterModels.BackupJobModeVM {
		for idx, vmSource := range vmSourceDatasets {
			vmDestSuffix := s.backupDestSuffixForMode(job.Mode, strings.TrimSpace(job.DestSuffix), vmSource)
			output = appendOutput(output, fmt.Sprintf("vm_dataset_backup_start[%d/%d]: %s -> %s", idx+1, len(vmSourceDatasets), vmSource, job.Target.ZeltaEndpoint(vmDestSuffix)))
			partOutput, partErr := s.backupWithEventProgress(ctx, &job.Target, vmSource, vmDestSuffix, event.ID)
			output = appendOutput(output, partOutput)
			if partErr != nil {
				runErr = partErr
				break
			}
		}
	} else {
		output, runErr = s.backupWithEventProgress(ctx, &job.Target, sourceDataset, destSuffix, event.ID)
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
	}

	if job.Mode != clusterModels.BackupJobModeVM && runErr != nil && shouldAutoRotateBackupErrorCode(runErr.Error()) {
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

	if job.Mode != clusterModels.BackupJobModeVM && runErr == nil && job.PruneKeepLast > 0 {
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
				output = appendOutput(output, runErr.Error())
				return runErr
			}

			if err := s.Jail.JailAction(int(ctId), "start"); err != nil {
				logger.L.Warn().Err(err).Uint("job_id", job.ID).Msg("failed_to_restart_jail_after_backup")
				output = appendOutput(output, fmt.Sprintf("failed_to_restart_jail: %s", err))
			}
		} else if job.Mode == clusterModels.BackupJobModeVM {
			if vmRID == 0 {
				runErr = fmt.Errorf("invalid_vm_rid_for_restart")
				output = appendOutput(output, runErr.Error())
				return runErr
			}
			if err := s.startVMIfPresent(vmRID); err != nil {
				logger.L.Warn().Err(err).Uint("job_id", job.ID).Msg("failed_to_restart_vm_after_backup")
				output = appendOutput(output, fmt.Sprintf("failed_to_restart_vm: %s", err))
			}
		}
	}

	return runErr
}

func (s *Service) backupDestSuffixForMode(mode, configuredSuffix, sourceDataset string) string {
	configuredSuffix = normalizeDatasetPath(configuredSuffix)
	sourceDataset = normalizeDatasetPath(sourceDataset)

	if mode == clusterModels.BackupJobModeVM {
		if sourceDataset == "" {
			return configuredSuffix
		}
		if configuredSuffix == "" {
			return sourceDataset
		}
		if configuredSuffix == sourceDataset || strings.HasSuffix(configuredSuffix, "/"+sourceDataset) {
			return configuredSuffix
		}
		return configuredSuffix + "/" + sourceDataset
	}

	if configuredSuffix != "" {
		return configuredSuffix
	}

	return autoDestSuffix(sourceDataset)
}

func (s *Service) resolveVMBackupSourceDatasets(ctx context.Context, vmRID uint, preferred string) ([]string, error) {
	if vmRID == 0 {
		return nil, fmt.Errorf("invalid_vm_rid")
	}

	preferred = normalizeDatasetPath(preferred)
	backupRoots := s.listEnabledBackupRoots()

	sources := make([]string, 0)
	seen := make(map[string]struct{})
	addSource := func(dataset string) {
		dataset = normalizeDatasetPath(dataset)
		if dataset == "" {
			return
		}
		if datasetWithinAnyRoot(dataset, backupRoots) {
			return
		}
		if _, ok := seen[dataset]; ok {
			return
		}
		seen[dataset] = struct{}{}
		sources = append(sources, dataset)
	}

	vm, vmErr := s.findVMByRID(vmRID)
	if vmErr != nil {
		logger.L.Warn().
			Uint("rid", vmRID).
			Err(vmErr).
			Msg("failed_to_lookup_vm_for_backup_source_resolution")
	} else if vm != nil {
		for _, storage := range vm.Storages {
			pool := strings.TrimSpace(storage.Pool)
			if pool == "" {
				pool = strings.TrimSpace(storage.Dataset.Pool)
			}
			if pool == "" {
				datasetName := normalizeDatasetPath(storage.Dataset.Name)
				if idx := strings.Index(datasetName, "/"); idx > 0 {
					pool = datasetName[:idx]
				}
			}
			if pool == "" {
				continue
			}

			addSource(fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, vmRID))
		}
	}

	localDatasets, err := s.listLocalFilesystemDatasets(ctx)
	if err != nil {
		if len(sources) > 0 {
			sort.Strings(sources)
			if preferred != "" && !datasetWithinAnyRoot(preferred, backupRoots) {
				if _, ok := seen[preferred]; !ok {
					sources = append([]string{preferred}, sources...)
				}
			}
			return sources, nil
		}
		if preferred == "" || datasetWithinAnyRoot(preferred, backupRoots) {
			return nil, err
		}
		return []string{preferred}, nil
	}

	for _, dataset := range localDatasets {
		if dataset == "" {
			continue
		}

		kind, rid := inferRestoreDatasetKind(dataset)
		if kind != clusterModels.BackupJobModeVM || rid != vmRID {
			continue
		}
		if vmDatasetRoot(dataset) != dataset {
			continue
		}

		addSource(dataset)
	}

	if preferred != "" {
		if datasetWithinAnyRoot(preferred, backupRoots) {
			logger.L.Warn().
				Uint("rid", vmRID).
				Str("dataset", preferred).
				Msg("ignoring_vm_backup_source_inside_backup_root")
		} else if _, ok := seen[preferred]; !ok {
			sources = append([]string{preferred}, sources...)
		} else {
			sort.SliceStable(sources, func(i, j int) bool {
				if sources[i] == preferred {
					return true
				}
				if sources[j] == preferred {
					return false
				}
				return sources[i] < sources[j]
			})
			return sources, nil
		}
	}

	sort.Strings(sources)
	if len(sources) == 0 && preferred != "" && !datasetWithinAnyRoot(preferred, backupRoots) {
		sources = append(sources, preferred)
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("vm_source_datasets_not_found")
	}

	return sources, nil
}

func (s *Service) listEnabledBackupRoots() []string {
	if s == nil || s.DB == nil {
		return []string{}
	}

	var rawRoots []string
	if err := s.DB.
		Model(&clusterModels.BackupTarget{}).
		Where("enabled = ?", true).
		Pluck("backup_root", &rawRoots).Error; err != nil {
		logger.L.Warn().Err(err).Msg("failed_to_list_backup_roots_for_vm_source_resolution")
		return []string{}
	}

	seen := make(map[string]struct{}, len(rawRoots))
	roots := make([]string, 0, len(rawRoots))
	for _, root := range rawRoots {
		normalized := normalizeDatasetPath(root)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		roots = append(roots, normalized)
	}

	sort.Strings(roots)
	return roots
}

func datasetWithinAnyRoot(dataset string, roots []string) bool {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" || len(roots) == 0 {
		return false
	}

	for _, root := range roots {
		if datasetWithinRoot(root, dataset) {
			return true
		}
	}

	return false
}

func (s *Service) stopVMIfPresent(rid uint) error {
	if rid == 0 || s.VM == nil {
		return nil
	}

	vm, err := s.findVMByRID(rid)
	if err != nil {
		return err
	}
	if vm == nil {
		return nil
	}

	return s.VM.LvVMAction(*vm, "stop")
}

func (s *Service) startVMIfPresent(rid uint) error {
	if rid == 0 || s.VM == nil {
		return nil
	}

	vm, err := s.findVMByRID(rid)
	if err != nil {
		return err
	}
	if vm == nil {
		return nil
	}

	return s.VM.LvVMAction(*vm, "start")
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
	for i := range deleteCount {
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

func (s *Service) finalizeBackupEvent(event *clusterModels.BackupEvent, runErr error, output string) {
	if event == nil || event.ID == 0 {
		return
	}

	now := time.Now().UTC()
	event.CompletedAt = &now
	event.Output = output
	if runErr != nil {
		event.Status = "failed"
		event.Error = runErr.Error()
	} else {
		event.Status = "success"
		event.Error = ""
	}

	if err := s.DB.Save(event).Error; err != nil {
		logger.L.Warn().Err(err).Uint("event_id", event.ID).Msg("failed_to_finalize_backup_event")
	}
}

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

func (s *Service) GetBackupEventProgress(ctx context.Context, id uint) (*BackupEventProgress, error) {
	event, err := s.GetLocalBackupEvent(id)
	if err != nil {
		return nil, err
	}

	out := &BackupEventProgress{
		Event:      event,
		TotalBytes: parseTotalBytesFromOutput(event.Output),
	}

	if strings.EqualFold(event.Mode, "restore") {
		progressDataset := strings.TrimSpace(event.TargetEndpoint)
		if progressDataset != "" {
			progressDataset += ".restoring"
			out.ProgressDataset = progressDataset
			movedBytes, movedErr := zfsDatasetUsedBytes(s, ctx, progressDataset)
			if movedErr != nil {
				logger.L.Debug().
					Uint("event_id", id).
					Str("dataset", progressDataset).
					Err(movedErr).
					Msg("restore_progress_dataset_query_failed")
			} else {
				out.MovedBytes = movedBytes
			}
		}
	}

	if out.TotalBytes != nil && out.MovedBytes != nil && *out.TotalBytes > 0 {
		pct := (float64(*out.MovedBytes) / float64(*out.TotalBytes)) * 100
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		rounded := math.Round(pct*100) / 100
		out.ProgressPercent = &rounded
	}

	return out, nil
}

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
