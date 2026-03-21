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
	"strconv"
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
	sentSizeRegex        = regexp.MustCompile(`(?im)\b([0-9]+(?:\.[0-9]+)?)\s*([KMGTPE]?)(i?B?)\s+sent\b`)
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
	queuedJobs  map[uint]struct{}

	replicationMu      sync.Mutex
	runningReplication map[uint]struct{}
	transitionMu       sync.Mutex
	runningTransitions map[uint]struct{}
	downMisses         map[uint]int

	workloadOpMu      sync.Mutex
	runningWorkloadOp map[string]string
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
		DB:                 db,
		Cluster:            clusterService,
		Jail:               jailService,
		Network:            networkService,
		VM:                 vmService,
		GZFS:               gzfsClient,
		runningJobs:        make(map[uint]struct{}),
		queuedJobs:         make(map[uint]struct{}),
		runningReplication: make(map[uint]struct{}),
		runningTransitions: make(map[uint]struct{}),
		downMisses:         make(map[uint]int),
		runningWorkloadOp:  make(map[string]string),
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
	snapPrefix string,
) (string, error) {
	snapPrefix = strings.TrimSpace(snapPrefix)
	if snapPrefix == "" {
		snapPrefix = "bk"
	}

	return s.backupWithEventProgressSnapshotName(
		ctx,
		target,
		sourceDataset,
		destSuffix,
		eventID,
		zeltaSnapshotName(snapPrefix),
	)
}

func (s *Service) backupWithEventProgressSnapshotName(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset, destSuffix string,
	eventID uint,
	snapshotName string,
) (string, error) {
	zeltaEndpoint := target.ZeltaEndpoint(destSuffix)
	extraEnv := s.buildZeltaEnv(target)
	extraEnv = setEnvValue(extraEnv, "ZELTA_LOG_LEVEL", "3")
	snapshotName = strings.TrimSpace(snapshotName)
	if snapshotName == "" {
		snapshotName = zeltaSnapshotName("bk")
	}

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
		"--incremental",
		"--snapshot",
		"--snap-name",
		snapshotName,
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

func (s *Service) RegisterJobs() {
	db.QueueRegisterJSON(backupJobQueueName, func(ctx context.Context, payload backupJobPayload) error {
		if payload.JobID == 0 {
			logger.L.Warn().Msg("queued_backup_job_invalid_payload_job_id")
			return nil
		}

		var job clusterModels.BackupJob
		if err := s.DB.Preload("Target").First(&job, payload.JobID).Error; err != nil {
			s.releaseReservedJob(payload.JobID)
			logger.L.Warn().Err(err).Uint("job_id", payload.JobID).Msg("queued_backup_job_not_found")
			return nil
		}

		if err := s.runBackupJob(ctx, &job); err != nil {
			if isJobAlreadyRunningErr(err) {
				s.releaseReservedJob(payload.JobID)
				logger.L.Info().Uint("job_id", payload.JobID).Msg("queued_backup_job_already_running_discarded")
				return nil
			}
			logger.L.Warn().Err(err).Uint("job_id", payload.JobID).Msg("queued_backup_job_failed")
			return err
		}

		return nil
	})

	s.registerRestoreJob()
	s.registerRestoreFromTargetJob()
	s.registerReplicationJob()
	s.registerReplicationFailoverJob()
}

func (s *Service) archiveActiveTargetDatasetForReseed(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	destSuffix string,
) (string, string, error) {
	if target == nil {
		return "", "", fmt.Errorf("target_required")
	}

	currentDataset := normalizeDatasetPath(strings.TrimSpace(target.BackupRoot))
	suffix := normalizeDatasetPath(destSuffix)
	if suffix != "" {
		currentDataset = normalizeDatasetPath(currentDataset + "/" + suffix)
	}
	if currentDataset == "" {
		return "", "", fmt.Errorf("target_dataset_required")
	}

	exists, err := s.targetDatasetExists(ctx, target, currentDataset)
	if err != nil {
		return currentDataset, "", err
	}
	if !exists {
		return currentDataset, "", nil
	}

	generationToken := compactNowToken()
	archivedDataset := ""
	for attempt := 0; attempt < 16; attempt++ {
		candidate := targetGenerationDatasetCandidate(currentDataset, generationToken, attempt)
		candidateExists, existsErr := s.targetDatasetExists(ctx, target, candidate)
		if existsErr != nil {
			return currentDataset, "", existsErr
		}
		if candidateExists {
			continue
		}
		archivedDataset = candidate
		break
	}
	if archivedDataset == "" {
		return currentDataset, "", fmt.Errorf("failed_to_allocate_archive_dataset_name")
	}

	if err := s.renameTargetDataset(ctx, target, currentDataset, archivedDataset); err != nil {
		return currentDataset, archivedDataset, fmt.Errorf("target_dataset_archive_failed: %w", err)
	}

	return currentDataset, archivedDataset, nil
}

func (s *Service) syncTargetBackupJobMetadata(
	ctx context.Context,
	job *clusterModels.BackupJob,
	sourceDataset string,
	destSuffix string,
) {
	if job == nil {
		return
	}

	target := &job.Target
	if target == nil || strings.TrimSpace(target.SSHHost) == "" {
		return
	}

	remoteDataset := normalizeDatasetPath(strings.TrimSpace(target.BackupRoot))
	suffix := normalizeDatasetPath(destSuffix)
	if suffix != "" {
		remoteDataset = normalizeDatasetPath(remoteDataset + "/" + suffix)
	}
	if remoteDataset == "" {
		return
	}

	sourceDataset = normalizeDatasetPath(sourceDataset)
	if sourceDataset == "" && strings.TrimSpace(job.Mode) == clusterModels.BackupJobModeJail {
		sourceDataset = normalizeDatasetPath(job.JailRootDataset)
	}
	if sourceDataset == "" {
		sourceDataset = normalizeDatasetPath(job.SourceDataset)
	}

	props := []string{
		fmt.Sprintf("sylve:backup_job_id=%d", job.ID),
		fmt.Sprintf("sylve:backup_mode=%s", strings.TrimSpace(job.Mode)),
		fmt.Sprintf("sylve:backup_source=%s", sourceDataset),
		fmt.Sprintf("sylve:backup_updated_at=%s", time.Now().UTC().Format(time.RFC3339)),
	}

	sshArgs := s.buildSSHArgs(target)
	sshArgs = append(sshArgs, target.SSHHost, "zfs", "set")
	sshArgs = append(sshArgs, props...)
	sshArgs = append(sshArgs, remoteDataset)
	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		logger.L.Warn().
			Err(err).
			Uint("job_id", job.ID).
			Str("dataset", remoteDataset).
			Str("output", strings.TrimSpace(output)).
			Msg("sync_target_backup_job_metadata_failed")
	}
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

		if !s.reserveJob(job.ID) {
			logger.L.Debug().Uint("job_id", job.ID).Msg("scheduled_backup_skip_job_already_queued_or_running")
			continue
		}

		if err := s.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", job.ID).Update("next_run_at", nextAt).Error; err != nil {
			s.releaseReservedJob(job.ID)
			logger.L.Warn().Err(err).Uint("job_id", job.ID).Msg("failed_to_update_next_run_at")
			continue
		}

		enqueueCtx, enqueueCancel := context.WithTimeout(ctx, 5*time.Second)
		if err := db.EnqueueJSON(enqueueCtx, backupJobQueueName, backupJobPayload{JobID: job.ID}); err != nil {
			s.releaseReservedJob(job.ID)
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

	if !s.reserveJob(jobID) {
		return fmt.Errorf("backup_job_already_running")
	}
	if err := db.EnqueueJSON(ctx, backupJobQueueName, backupJobPayload{JobID: jobID}); err != nil {
		s.releaseReservedJob(jobID)
		return err
	}
	return nil
}

func (s *Service) runBackupJob(ctx context.Context, job *clusterModels.BackupJob) error {
	if !s.beginJob(job.ID) {
		return fmt.Errorf("backup_job_already_running")
	}

	defer s.releaseJob(job.ID)

	jobGuestType, jobGuestID := backupJobGuestIdentity(job)
	if jobGuestType != "" && jobGuestID > 0 && s.Cluster != nil {
		localNodeID := s.localNodeID()
		allowed, leaseErr := cluster.CanNodeMutateProtectedGuest(s.DB, jobGuestType, jobGuestID, localNodeID)
		if leaseErr != nil {
			runErr := fmt.Errorf("replication_lease_check_failed: %w", leaseErr)
			s.updateBackupJobResult(job, runErr)
			return runErr
		}
		if !allowed {
			runErr := fmt.Errorf("replication_lease_not_owned")
			s.updateBackupJobResult(job, runErr)
			return runErr
		}
	}
	if ok, holder := s.acquireWorkloadOperation(jobGuestType, jobGuestID, fmt.Sprintf("backup_job:%d", job.ID)); !ok {
		runErr := fmt.Errorf(
			"workload_operation_conflict_with_%s guest_type=%s guest_id=%d",
			holder,
			jobGuestType,
			jobGuestID,
		)
		s.updateBackupJobResult(job, runErr)
		return runErr
	}
	defer s.releaseWorkloadOperation(jobGuestType, jobGuestID)

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

		preferredVMSource := normalizeDatasetPath(sourceDataset)
		validatedSources := make([]string, 0, len(vmSourceDatasets))
		for _, vmSource := range vmSourceDatasets {
			vmSource = normalizeDatasetPath(vmSource)
			if vmSource == "" {
				continue
			}

			exists, err := s.localDatasetExists(ctx, vmSource)
			if err != nil {
				runErr := fmt.Errorf("failed_to_check_vm_backup_source_dataset_%s: %w", vmSource, err)
				s.updateBackupJobResult(job, runErr)
				return runErr
			}
			if !exists {
				if preferredVMSource != "" && vmSource == preferredVMSource {
					logger.L.Warn().
						Uint("job_id", job.ID).
						Uint("vm_rid", vmRID).
						Str("dataset", vmSource).
						Msg("skipping_missing_preferred_vm_backup_source_dataset")
					continue
				}

				runErr := fmt.Errorf("vm_backup_source_dataset_not_found: %s", vmSource)
				s.updateBackupJobResult(job, runErr)
				return runErr
			}

			validatedSources = append(validatedSources, vmSource)
		}

		if len(validatedSources) == 0 {
			runErr := fmt.Errorf("vm_source_datasets_not_found")
			s.updateBackupJobResult(job, runErr)
			return runErr
		}

		vmSourceDatasets = validatedSources
		if preferredVMSource != "" && preferredVMSource != vmSourceDatasets[0] {
			foundPreferred := false
			for _, vmSource := range vmSourceDatasets {
				if vmSource == preferredVMSource {
					foundPreferred = true
					break
				}
			}
			if !foundPreferred {
				logger.L.Info().
					Uint("job_id", job.ID).
					Uint("vm_rid", vmRID).
					Str("missing_source", preferredVMSource).
					Str("fallback_source", vmSourceDatasets[0]).
					Msg("vm_backup_source_fallback_selected")
				sourceDataset = vmSourceDatasets[0]
			}
		}
	}

	event.SourceDataset = sourceDataset

	destSuffix := s.backupDestSuffixForMode(job.Mode, strings.TrimSpace(job.DestSuffix), sourceDataset)
	if job.Mode == clusterModels.BackupJobModeVM {
		destSuffix = s.backupDestSuffixForVMSource(strings.TrimSpace(job.DestSuffix), sourceDataset)
	} else if job.Mode == clusterModels.BackupJobModeJail {
		destSuffix = s.backupDestSuffixForJailSource(strings.TrimSpace(job.DestSuffix), sourceDataset)
	}
	backupSnapPrefix := backupSnapshotPrefixForJob(job.ID)
	event.TargetEndpoint = job.Target.ZeltaEndpoint(destSuffix)
	if err := s.DB.Create(&event).Error; err != nil {
		runErr := fmt.Errorf("create_backup_event_failed: %w", err)
		s.updateBackupJobResult(job, runErr)
		return runErr
	}
	stopHeartbeat := s.startBackupEventHeartbeat(ctx, event.ID, time.Minute)

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
	var lastVMFailedSource string
	var lastVMFailedDestSuffix string

	runVMBackupPass := func() error {
		lastVMFailedSource = ""
		lastVMFailedDestSuffix = ""
		vmSnapshotName := zeltaSnapshotName(backupSnapPrefix)

		for idx, vmSource := range vmSourceDatasets {
			vmDestSuffix := s.backupDestSuffixForVMSource(strings.TrimSpace(job.DestSuffix), vmSource)
			output = appendOutput(output, fmt.Sprintf("vm_dataset_backup_start[%d/%d]: %s -> %s", idx+1, len(vmSourceDatasets), vmSource, job.Target.ZeltaEndpoint(vmDestSuffix)))
			partOutput, partErr := s.backupWithEventProgressSnapshotName(ctx, &job.Target, vmSource, vmDestSuffix, event.ID, vmSnapshotName)
			output = appendOutput(output, partOutput)
			if partErr == nil {
				outcome := classifyBackupOutput(partOutput)
				if code := outcome.errorCode(); code != "" {
					partErr = errors.New(code)
				} else if outcome == backupOutputUpToDate {
					logger.L.Info().
						Uint("job_id", job.ID).
						Str("source", vmSource).
						Str("target", job.Target.ZeltaEndpoint(vmDestSuffix)).
						Msg("backup_up_to_date_noop")
				}
			}
			if partErr != nil {
				lastVMFailedSource = vmSource
				lastVMFailedDestSuffix = vmDestSuffix
				return partErr
			}
		}

		return nil
	}

	defer func() {
		stopHeartbeat()
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
		runErr = runVMBackupPass()
	} else {
		output, runErr = s.backupWithEventProgress(ctx, &job.Target, sourceDataset, destSuffix, event.ID, backupSnapPrefix)
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

	if runErr != nil && isReplicationResumeStateError(runErr) {
		resumeSource := sourceDataset
		resumeDestSuffix := destSuffix
		if job.Mode == clusterModels.BackupJobModeVM {
			if strings.TrimSpace(lastVMFailedSource) != "" && strings.TrimSpace(lastVMFailedDestSuffix) != "" {
				resumeSource = lastVMFailedSource
				resumeDestSuffix = lastVMFailedDestSuffix
			} else if len(vmSourceDatasets) > 0 {
				resumeSource = vmSourceDatasets[0]
				resumeDestSuffix = s.backupDestSuffixForVMSource(strings.TrimSpace(job.DestSuffix), resumeSource)
			}
		}

		logger.L.Info().
			Uint("job_id", job.ID).
			Str("source", resumeSource).
			Str("target", job.Target.ZeltaEndpoint(resumeDestSuffix)).
			Err(runErr).
			Msg("backup_resume_state_abort_starting")

		abortOut, abortErr := s.abortTargetResumableReceiveState(ctx, &job.Target, resumeDestSuffix)
		output = appendOutput(output, abortOut)
		if abortErr != nil {
			runErr = fmt.Errorf("backup_resume_abort_failed: %w (original: %v)", abortErr, runErr)
		} else if job.Mode == clusterModels.BackupJobModeVM {
			runErr = runVMBackupPass()
		} else {
			retryOutput, retryErr := s.backupWithEventProgress(ctx, &job.Target, sourceDataset, destSuffix, event.ID, backupSnapPrefix)
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
						Msg("backup_up_to_date_noop_after_resume_abort")
				}
			}
		}
	}

	if runErr != nil && shouldAutoRotateBackupErrorCode(runErr.Error()) {
		reseedSource := sourceDataset
		reseedDestSuffix := destSuffix
		if job.Mode == clusterModels.BackupJobModeVM {
			if strings.TrimSpace(lastVMFailedSource) != "" && strings.TrimSpace(lastVMFailedDestSuffix) != "" {
				reseedSource = lastVMFailedSource
				reseedDestSuffix = lastVMFailedDestSuffix
			} else if len(vmSourceDatasets) > 0 {
				reseedSource = vmSourceDatasets[0]
				reseedDestSuffix = s.backupDestSuffixForVMSource(strings.TrimSpace(job.DestSuffix), reseedSource)
			}
		}

		logger.L.Info().
			Uint("job_id", job.ID).
			Str("source", reseedSource).
			Str("target", job.Target.ZeltaEndpoint(reseedDestSuffix)).
			Str("reason", runErr.Error()).
			Msg("backup_auto_reseed_starting")

		fromDataset, archivedDataset, archiveErr := s.archiveActiveTargetDatasetForReseed(ctx, &job.Target, reseedDestSuffix)
		if archiveErr != nil {
			runErr = fmt.Errorf("backup_auto_reseed_failed: %w", archiveErr)
		} else {
			logger.L.Info().
				Uint("job_id", job.ID).
				Str("source", reseedSource).
				Str("target", job.Target.ZeltaEndpoint(reseedDestSuffix)).
				Str("archived_from", fromDataset).
				Str("archived_to", archivedDataset).
				Msg("backup_auto_reseed_archive_completed")
			if strings.TrimSpace(archivedDataset) != "" {
				output = appendOutput(output, fmt.Sprintf("auto_archived_target_dataset: %s -> %s", fromDataset, archivedDataset))
			}

			if job.Mode == clusterModels.BackupJobModeVM {
				runErr = runVMBackupPass()
			} else {
				retryOutput, retryErr := s.backupWithEventProgress(ctx, &job.Target, sourceDataset, destSuffix, event.ID, backupSnapPrefix)
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
							Msg("backup_up_to_date_noop_after_reseed")
					}
				}
			}

			if runErr == nil {
				logger.L.Info().
					Uint("job_id", job.ID).
					Str("source", reseedSource).
					Str("target", job.Target.ZeltaEndpoint(reseedDestSuffix)).
					Msg("backup_completed_after_reseed")
			}
		}
	}

	if runErr == nil {
		s.syncTargetBackupJobMetadata(ctx, job, sourceDataset, destSuffix)
	}

	if runErr == nil && job.PruneKeepLast > 0 {
		type pruneScope struct {
			sourceDataset string
			destSuffix    string
		}

		pruneScopes := make([]pruneScope, 0, 1)
		if job.Mode == clusterModels.BackupJobModeVM {
			for _, vmSource := range vmSourceDatasets {
				vmDestSuffix := s.backupDestSuffixForVMSource(strings.TrimSpace(job.DestSuffix), vmSource)
				pruneScopes = append(pruneScopes, pruneScope{
					sourceDataset: vmSource,
					destSuffix:    vmDestSuffix,
				})
			}
		} else {
			pruneScopes = append(pruneScopes, pruneScope{
				sourceDataset: sourceDataset,
				destSuffix:    destSuffix,
			})
		}

		for _, scope := range pruneScopes {
			scopeSource := normalizeDatasetPath(scope.sourceDataset)
			scopeDestSuffix := normalizeDatasetPath(scope.destSuffix)
			if scopeSource == "" {
				logger.L.Warn().Uint("job_id", job.ID).Msg("backup_prune_skipped_invalid_scope_source_dataset")
				continue
			}

			localSnapshots, localListErr := s.listLocalSnapshotsForDataset(ctx, scopeSource)
			if localListErr != nil {
				logger.L.Warn().Err(localListErr).Uint("job_id", job.ID).Str("source", scopeSource).Msg("backup_prune_local_snapshot_list_failed")
			}

			pruneCandidates, pruneOutput, pruneErr := s.PruneCandidatesWithTarget(ctx, &job.Target, scopeSource, scopeDestSuffix, 0)
			output = appendOutput(output, pruneOutput)

			if pruneErr != nil {
				logger.L.Warn().Err(pruneErr).Uint("job_id", job.ID).Str("source", scopeSource).Str("dest_suffix", scopeDestSuffix).Msg("backup_prune_scan_failed")
			} else if localListErr == nil {
				pruneCandidates = buildBKRetentionPruneCandidates(localSnapshots, job.PruneKeepLast, snapshotCandidateSet(pruneCandidates), backupSnapPrefix)
			} else {
				pruneCandidates = []string{}
			}

			if len(pruneCandidates) > 0 {
				if err := s.DestroySnapshots(ctx, pruneCandidates); err != nil {
					logger.L.Warn().Err(err).Uint("job_id", job.ID).Str("source", scopeSource).Int("candidate_count", len(pruneCandidates)).Msg("backup_prune_destroy_failed")
				} else {
					logger.L.Info().Uint("job_id", job.ID).Str("source", scopeSource).Int("pruned", len(pruneCandidates)).Msg("backup_prune_completed")
				}
			} else {
				logger.L.Debug().Uint("job_id", job.ID).Str("source", scopeSource).Int("keep_last", job.PruneKeepLast).Msg("backup_prune_no_candidates")
			}

			if !job.PruneTarget {
				continue
			}

			remoteDataset := normalizeDatasetPath(strings.TrimSpace(job.Target.BackupRoot))
			if scopeDestSuffix != "" {
				if remoteDataset == "" {
					remoteDataset = scopeDestSuffix
				} else {
					remoteDataset = normalizeDatasetPath(remoteDataset + "/" + scopeDestSuffix)
				}
			}
			if remoteDataset == "" {
				logger.L.Warn().Uint("job_id", job.ID).Str("source", scopeSource).Msg("backup_prune_target_skipped_remote_dataset_empty")
				continue
			}

			remoteSnapshots, remoteListErr := s.listRemoteSnapshotsForDataset(ctx, &job.Target, remoteDataset)
			if remoteListErr != nil {
				logger.L.Warn().Err(remoteListErr).Uint("job_id", job.ID).Str("source", scopeSource).Str("remote_dataset", remoteDataset).Msg("backup_prune_target_snapshot_list_failed")
			}

			targetPruneCandidates, targetPruneOutput, targetPruneErr := s.PruneTargetCandidatesWithSource(ctx, &job.Target, scopeSource, scopeDestSuffix, 0)
			output = appendOutput(output, targetPruneOutput)

			if targetPruneErr != nil {
				logger.L.Warn().Err(targetPruneErr).Uint("job_id", job.ID).Str("source", scopeSource).Str("dest_suffix", scopeDestSuffix).Msg("backup_prune_target_scan_failed")
			} else if remoteListErr == nil {
				targetPruneCandidates = buildBKRetentionPruneCandidates(remoteSnapshots, job.PruneKeepLast, snapshotCandidateSet(targetPruneCandidates), backupSnapPrefix)
			} else {
				targetPruneCandidates = []string{}
			}

			if len(targetPruneCandidates) > 0 {
				if err := s.DestroyTargetSnapshotsByName(ctx, &job.Target, targetPruneCandidates); err != nil {
					logger.L.Warn().Err(err).Uint("job_id", job.ID).Str("source", scopeSource).Int("candidate_count", len(targetPruneCandidates)).Msg("backup_prune_target_destroy_failed")
				} else {
					logger.L.Info().Uint("job_id", job.ID).Str("source", scopeSource).Int("pruned", len(targetPruneCandidates)).Msg("backup_prune_target_completed")
				}
			} else {
				fallbackCandidates, fallbackErr := s.buildTargetRetentionPruneCandidatesForDataset(ctx, &job.Target, remoteDataset, job.PruneKeepLast+1, backupSnapPrefix)
				if fallbackErr != nil {
					logger.L.Warn().Err(fallbackErr).Uint("job_id", job.ID).Str("source", scopeSource).Str("remote_dataset", remoteDataset).Msg("backup_prune_target_retention_scan_failed")
				} else if len(fallbackCandidates) > 0 {
					if err := s.DestroyTargetSnapshotsByName(ctx, &job.Target, fallbackCandidates); err != nil {
						logger.L.Warn().Err(err).Uint("job_id", job.ID).Str("source", scopeSource).Int("candidate_count", len(fallbackCandidates)).Msg("backup_prune_target_retention_destroy_failed")
					} else {
						logger.L.Info().Uint("job_id", job.ID).Str("source", scopeSource).Int("pruned", len(fallbackCandidates)).Msg("backup_prune_target_retention_completed")
					}
				} else {
					logger.L.Debug().Uint("job_id", job.ID).Str("source", scopeSource).Int("keep_last", job.PruneKeepLast).Msg("backup_prune_target_no_candidates")
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
		sourceRoot := normalizeDatasetPath(vmDatasetRoot(sourceDataset))
		if sourceRoot != "" {
			sourceRootSuffix := autoDestSuffix(sourceRoot)
			if configuredSuffix == sourceRootSuffix ||
				strings.HasPrefix(configuredSuffix, sourceRootSuffix+"/j-") {
				rel := strings.TrimPrefix(sourceDataset, sourceRoot)
				rel = strings.TrimPrefix(rel, "/")
				if rel == "" {
					return configuredSuffix
				}
				return normalizeDatasetPath(configuredSuffix + "/" + rel)
			}
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

func (s *Service) backupDestSuffixForVMSource(configuredSuffix, vmSourceDataset string) string {
	return vmDestSuffixForSource(configuredSuffix, vmSourceDataset)
}

func (s *Service) backupDestSuffixForJailSource(configuredSuffix, jailSourceDataset string) string {
	return jailDestSuffixForSource(configuredSuffix, jailSourceDataset)
}

func vmDestSuffixForSource(configuredSuffix, vmSourceDataset string) string {
	configuredSuffix = normalizeDatasetPath(configuredSuffix)
	vmSourceDataset = normalizeDatasetPath(vmSourceDataset)

	sourceRoot := normalizeDatasetPath(vmDatasetRoot(vmSourceDataset))
	if sourceRoot == "" {
		return configuredSuffix
	}

	rel := strings.TrimPrefix(vmSourceDataset, sourceRoot)
	rel = strings.TrimPrefix(rel, "/")

	if tail := vmJobLineageTail(configuredSuffix); tail != "" {
		mapped := normalizeDatasetPath(sourceRoot + "/" + tail)
		if rel != "" {
			mapped = normalizeDatasetPath(mapped + "/" + rel)
		}
		return mapped
	}

	if configuredSuffix == "" {
		if rel == "" {
			return sourceRoot
		}
		return normalizeDatasetPath(sourceRoot + "/" + rel)
	}

	mapped := configuredSuffix
	if !strings.HasPrefix(mapped, sourceRoot+"/") && mapped != sourceRoot {
		mapped = normalizeDatasetPath(sourceRoot + "/" + mapped)
	}
	if rel != "" && !strings.HasSuffix(mapped, "/"+rel) {
		mapped = normalizeDatasetPath(mapped + "/" + rel)
	}

	return normalizeDatasetPath(mapped)
}

func vmJobLineageTail(destSuffix string) string {
	destSuffix = normalizeDatasetPath(destSuffix)
	if destSuffix == "" {
		return ""
	}

	if idx := strings.Index(destSuffix, "/j-"); idx >= 0 {
		return strings.TrimLeft(destSuffix[idx+1:], "/")
	}
	if idx := strings.Index(destSuffix, "/job-"); idx >= 0 {
		return strings.TrimLeft(destSuffix[idx+1:], "/")
	}

	return ""
}

func jailDestSuffixForSource(configuredSuffix, jailSourceDataset string) string {
	configuredSuffix = normalizeDatasetPath(configuredSuffix)
	jailSourceDataset = normalizeDatasetPath(jailSourceDataset)
	if jailSourceDataset == "" {
		return configuredSuffix
	}

	if tail := jailJobLineageTail(configuredSuffix); tail != "" {
		return normalizeDatasetPath(jailSourceDataset + "/" + tail)
	}

	if configuredSuffix == "" {
		return jailSourceDataset
	}
	if strings.HasPrefix(configuredSuffix, jailSourceDataset+"/") || configuredSuffix == jailSourceDataset {
		return configuredSuffix
	}

	return normalizeDatasetPath(jailSourceDataset + "/" + configuredSuffix)
}

func jailJobLineageTail(destSuffix string) string {
	destSuffix = normalizeDatasetPath(destSuffix)
	if destSuffix == "" {
		return ""
	}

	if idx := strings.Index(destSuffix, "/j-"); idx >= 0 {
		return strings.TrimLeft(destSuffix[idx+1:], "/")
	}
	if idx := strings.Index(destSuffix, "/job-"); idx >= 0 {
		return strings.TrimLeft(destSuffix[idx+1:], "/")
	}

	return ""
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

	isShutOff, err := s.VM.IsDomainShutOff(rid)
	if err == nil && isShutOff {
		return nil
	}
	if err != nil && isVMDomainNotFoundError(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed_to_check_vm_state_before_stop: %w", err)
	}

	if err := s.VM.LvVMAction(*vm, "stop"); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "not running") || isVMDomainNotFoundError(err) {
			return nil
		}
		return err
	}

	deadline := time.Now().Add(60 * time.Second)
	for {
		isShutOff, err := s.VM.IsDomainShutOff(rid)
		if err == nil && isShutOff {
			return nil
		}
		if err != nil && isVMDomainNotFoundError(err) {
			return nil
		}

		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("vm_failed_to_reach_shutoff_state: %w", err)
			}
			return fmt.Errorf("vm_failed_to_reach_shutoff_state")
		}

		time.Sleep(500 * time.Millisecond)
	}
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

func isVMDomainNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "domain") &&
		(strings.Contains(lower, "not found") || strings.Contains(lower, "no domain"))
}

func (s *Service) buildTargetRetentionPruneCandidatesForDataset(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	remoteDataset string,
	keepCount int,
	snapPrefix string,
) ([]string, error) {
	if keepCount < 1 {
		keepCount = 1
	}

	remoteDataset = normalizeDatasetPath(remoteDataset)
	if remoteDataset == "" {
		return nil, fmt.Errorf("remote_dataset_required")
	}
	if target == nil {
		return nil, fmt.Errorf("backup_target_required")
	}

	snapshots, err := s.listRemoteSnapshotsForDataset(ctx, target, remoteDataset)
	if err != nil {
		return nil, err
	}

	return buildBKRetentionPruneCandidates(snapshots, keepCount, nil, snapPrefix), nil
}

func (s *Service) buildTargetRetentionPruneCandidates(ctx context.Context, job *clusterModels.BackupJob, keepCount int, snapPrefix string) ([]string, error) {
	if job == nil {
		return nil, fmt.Errorf("backup_job_required")
	}

	return s.buildTargetRetentionPruneCandidatesForDataset(
		ctx,
		&job.Target,
		remoteDatasetForJob(job),
		keepCount,
		snapPrefix,
	)
}

func (s *Service) listLocalSnapshotsForDataset(ctx context.Context, dataset string) ([]SnapshotInfo, error) {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return nil, fmt.Errorf("source_dataset_required")
	}

	output, err := utils.RunCommandWithContext(
		ctx,
		"zfs",
		"list",
		"-t", "snapshot",
		"-r",
		"-Hp",
		"-o", "name,creation,used,refer",
		"-s", "creation",
		dataset,
	)
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_local_snapshots: %w", err)
	}

	return parseSnapshotInfoOutput(output), nil
}

func buildBKRetentionPruneCandidates(snapshots []SnapshotInfo, keepCount int, safeSet map[string]struct{}, snapPrefix string) []string {
	if keepCount < 0 {
		keepCount = 0
	}

	grouped := make(map[string][]string)
	for _, snapshot := range snapshots {
		name := strings.TrimSpace(snapshot.Name)
		if !isValidZFSSnapshotName(name) {
			continue
		}
		if !isBKSnapshotShortName(snapshotShortName(snapshot), snapPrefix) {
			continue
		}
		if safeSet != nil {
			if _, ok := safeSet[name]; !ok {
				continue
			}
		}

		dataset := snapshotDatasetName(name)
		if dataset == "" {
			dataset = normalizeDatasetPath(snapshot.Dataset)
		}
		grouped[dataset] = append(grouped[dataset], name)
	}

	if len(grouped) == 0 {
		return []string{}
	}

	datasets := make([]string, 0, len(grouped))
	for dataset := range grouped {
		datasets = append(datasets, dataset)
	}
	sort.Strings(datasets)

	candidates := make([]string, 0)
	for _, dataset := range datasets {
		names := grouped[dataset]
		if len(names) <= keepCount {
			continue
		}
		deleteCount := len(names) - keepCount
		candidates = append(candidates, names[:deleteCount]...)
	}

	return candidates
}

func snapshotCandidateSet(snapshots []string) map[string]struct{} {
	set := make(map[string]struct{}, len(snapshots))
	for _, snapshot := range snapshots {
		name := strings.TrimSpace(snapshot)
		if !isValidZFSSnapshotName(name) {
			continue
		}
		set[name] = struct{}{}
	}
	return set
}

func isBKSnapshotShortName(snapshotName, snapPrefix string) bool {
	snapshotName = strings.TrimSpace(snapshotName)
	snapshotName = strings.TrimPrefix(snapshotName, "@")
	snapPrefix = strings.TrimSpace(snapPrefix)
	if snapPrefix == "" {
		return strings.HasPrefix(snapshotName, "bk_")
	}
	return strings.HasPrefix(snapshotName, snapPrefix+"_")
}

func backupSnapshotPrefixForJob(jobID uint) string {
	if jobID == 0 {
		return "bk"
	}
	return "bk_j" + compactIDToken(jobID)
}

func compactIDToken(id uint) string {
	if id == 0 {
		return "0"
	}
	return strings.ToLower(strconv.FormatUint(uint64(id), 36))
}

func compactNowToken() string {
	return strings.ToLower(strconv.FormatInt(time.Now().UTC().UnixMilli(), 36))
}

func targetGenerationDatasetCandidate(activeDataset, generationToken string, attempt int) string {
	activeDataset = normalizeDatasetPath(activeDataset)
	generationToken = strings.TrimSpace(generationToken)
	if activeDataset == "" {
		return ""
	}
	if generationToken == "" {
		generationToken = compactNowToken()
	}

	candidate := normalizeDatasetPath(activeDataset + "_gen-" + generationToken)
	if attempt > 0 {
		candidate = normalizeDatasetPath(fmt.Sprintf("%s-%d", candidate, attempt))
	}
	return candidate
}

func (s *Service) updateBackupJobResult(job *clusterModels.BackupJob, runErr error) {
	now := time.Now().UTC()
	next := (*time.Time)(nil)

	if job.Enabled {
		if n, err := nextRunTime(job.CronExpr, now); err == nil {
			next = &n
		}
	}

	status := "success"
	lastError := ""
	if runErr != nil {
		status = "failed"
		lastError = runErr.Error()
	}

	update := cluster.BackupJobRuntimeStateUpdate{
		JobID:      job.ID,
		LastRunAt:  &now,
		LastStatus: status,
		LastError:  lastError,
		NextRunAt:  next,
	}

	if s.syncBackupJobRuntimeState(update) {
		return
	}

	updates := map[string]any{
		"last_run_at": update.LastRunAt,
		"last_status": update.LastStatus,
		"last_error":  update.LastError,
		"next_run_at": update.NextRunAt,
	}

	if err := s.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", job.ID).Updates(updates).Error; err != nil {
		logger.L.Warn().Err(err).Uint("job_id", job.ID).Msg("failed_to_update_backup_job_state")
	}
}

func (s *Service) syncBackupJobRuntimeState(update cluster.BackupJobRuntimeStateUpdate) bool {
	if s == nil || s.Cluster == nil {
		return false
	}

	bypassRaft := s.Cluster.Raft == nil
	if err := s.Cluster.UpdateBackupJobRuntimeState(update, bypassRaft); err == nil {
		return true
	} else if !bypassRaft && strings.Contains(strings.ToLower(err.Error()), "not_leader") {
		forwardErr := s.forwardBackupJobStateToLeader(update)
		if forwardErr == nil {
			return true
		}
		logger.L.Warn().Err(forwardErr).Uint("job_id", update.JobID).Msg("failed_to_forward_backup_job_state_to_leader")
	} else {
		logger.L.Warn().Err(err).Uint("job_id", update.JobID).Msg("failed_to_sync_backup_job_state_cluster_wide")
	}

	return false
}

func (s *Service) forwardBackupJobStateToLeader(update cluster.BackupJobRuntimeStateUpdate) error {
	if s == nil || s.Cluster == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}
	if s.Cluster.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	_, leaderID := s.Cluster.Raft.LeaderWithID()
	leaderNodeID := strings.TrimSpace(string(leaderID))
	if leaderNodeID == "" {
		return fmt.Errorf("leader_unknown")
	}

	payload := map[string]any{
		"jobId":      update.JobID,
		"lastRunAt":  update.LastRunAt,
		"lastStatus": update.LastStatus,
		"lastError":  update.LastError,
		"nextRunAt":  update.NextRunAt,
	}

	return s.forwardReplicationPolicyControl(leaderNodeID, "backup-job-state", payload, 5*time.Second)
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

	s.emitLeftPanelRefresh(fmt.Sprintf("backup_event_finalized_%d", event.ID))
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
	out.MovedBytes = parseMovedBytesFromOutput(event.Output)

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
	} else if event.JobID != nil && *event.JobID > 0 {
		var job clusterModels.BackupJob
		if err := s.DB.Preload("Target").First(&job, *event.JobID).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				logger.L.Debug().
					Err(err).
					Uint("event_id", id).
					Msg("backup_event_progress_job_lookup_failed")
			}
		} else if job.Target.ID > 0 {
			progressDataset := datasetFromZeltaEndpoint(event.TargetEndpoint)
			if progressDataset != "" {
				out.ProgressDataset = progressDataset
				movedBytes, movedErr := zfsTargetDatasetUsedBytes(s, ctx, &job.Target, progressDataset)
				if movedErr != nil {
					logger.L.Debug().
						Uint("event_id", id).
						Str("dataset", progressDataset).
						Err(movedErr).
						Msg("backup_progress_dataset_query_failed")
				} else if movedBytes != nil {
					if out.TotalBytes != nil && *out.TotalBytes > 0 && *movedBytes > *out.TotalBytes {
						// Avoid false 100% progress when target dataset already has historical data.
						if out.MovedBytes == nil || *out.MovedBytes > *out.TotalBytes {
							// Keep output-derived progress only.
						}
					} else if out.MovedBytes == nil || *movedBytes > *out.MovedBytes {
						out.MovedBytes = movedBytes
					}
				}
			}
		}
	}

	if out.TotalBytes != nil && out.MovedBytes != nil && *out.TotalBytes > 0 && *out.MovedBytes > *out.TotalBytes {
		capped := *out.TotalBytes
		out.MovedBytes = &capped
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
	query := s.DB.Model(&clusterModels.BackupEvent{}).
		Where("status = ? AND updated_at < ?", "running", cutoff)

	activeJobIDs := s.activeJobIDs()
	if len(activeJobIDs) > 0 {
		query = query.Where("(job_id IS NULL OR job_id NOT IN ?)", activeJobIDs)
	}

	return query.Updates(map[string]any{
		"status":       "interrupted",
		"error":        "process_crashed_or_restarted",
		"completed_at": time.Now().UTC(),
	}).Error
}

func (s *Service) touchBackupEvent(eventID uint) error {
	if s == nil || s.DB == nil || eventID == 0 {
		return nil
	}

	return s.DB.Model(&clusterModels.BackupEvent{}).
		Where("id = ? AND status = ?", eventID, "running").
		Update("updated_at", time.Now().UTC()).
		Error
}

func (s *Service) startBackupEventHeartbeat(ctx context.Context, eventID uint, interval time.Duration) func() {
	if s == nil || s.DB == nil || eventID == 0 {
		return func() {}
	}
	if interval <= 0 {
		interval = time.Minute
	}

	heartbeatCtx, cancel := context.WithCancel(ctx)
	ticker := time.NewTicker(interval)

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if err := s.touchBackupEvent(eventID); err != nil {
					logger.L.Debug().
						Uint("event_id", eventID).
						Err(err).
						Msg("backup_event_heartbeat_failed")
				}
			}
		}
	}()

	return cancel
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

func isJobAlreadyRunningErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "already_running")
}

func (s *Service) reserveJob(jobID uint) bool {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()

	if jobID == 0 {
		return false
	}
	if _, exists := s.runningJobs[jobID]; exists {
		return false
	}
	if _, exists := s.queuedJobs[jobID]; exists {
		return false
	}
	s.queuedJobs[jobID] = struct{}{}
	return true
}

func (s *Service) releaseReservedJob(jobID uint) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	delete(s.queuedJobs, jobID)
}

func (s *Service) beginJob(jobID uint) bool {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()

	if jobID == 0 {
		return false
	}
	if _, exists := s.runningJobs[jobID]; exists {
		return false
	}
	delete(s.queuedJobs, jobID)
	s.runningJobs[jobID] = struct{}{}
	return true
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

func (s *Service) activeJobIDs() []uint {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()

	ids := make([]uint, 0, len(s.runningJobs))
	for jobID := range s.runningJobs {
		if jobID == 0 {
			continue
		}
		ids = append(ids, jobID)
	}

	return ids
}

func workloadOperationKey(guestType string, guestID uint) string {
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	if guestID == 0 {
		return ""
	}
	if guestType != clusterModels.BackupJobModeVM && guestType != clusterModels.BackupJobModeJail {
		return ""
	}
	return fmt.Sprintf("%s:%d", guestType, guestID)
}

func (s *Service) acquireWorkloadOperation(guestType string, guestID uint, operation string) (bool, string) {
	key := workloadOperationKey(guestType, guestID)
	if key == "" {
		return true, ""
	}

	op := strings.TrimSpace(operation)
	if op == "" {
		op = "unknown"
	}

	s.workloadOpMu.Lock()
	defer s.workloadOpMu.Unlock()
	if s.runningWorkloadOp == nil {
		s.runningWorkloadOp = make(map[string]string)
	}

	if existing, exists := s.runningWorkloadOp[key]; exists {
		return false, existing
	}

	s.runningWorkloadOp[key] = op
	return true, ""
}

func (s *Service) releaseWorkloadOperation(guestType string, guestID uint) {
	key := workloadOperationKey(guestType, guestID)
	if key == "" {
		return
	}

	s.workloadOpMu.Lock()
	defer s.workloadOpMu.Unlock()
	delete(s.runningWorkloadOp, key)
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
