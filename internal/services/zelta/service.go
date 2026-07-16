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
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
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

const (
	backupJobQueueName = "zelta-backup-run"

	// maxBackupStallAge is the maximum time since LastRunAt before
	// the scheduler forces a run regardless of NextRunAt (clock-skew
	// or stalled-job safety net).
	maxBackupStallAge = 25 * time.Hour

	// maxBackupCatchUpWindow is the maximum time a job can be past
	// its NextRunAt before being skipped. Prevents thundering-herd
	// on node restart after extended downtime.
	maxBackupCatchUpWindow = 2 * time.Hour

	// maxBackupEnqueueJitter is the maximum random delay before
	// enqueuing a job to spread load across the tick window.
	maxBackupEnqueueJitter = 30 * time.Second
)

type Service struct {
	DB          *gorm.DB
	TelemetryDB *gorm.DB
	Cluster     *cluster.Service
	Jail        jailServiceInterfaces.JailServiceInterface
	Network     networkServiceInterfaces.NetworkServiceInterface
	VM          libvirtServiceInterfaces.LibvirtServiceInterface
	GZFS        *gzfs.Client
	startedAt   time.Time

	jobMu       sync.Mutex
	runningJobs map[uint]struct{}
	queuedJobs  map[uint]struct{}

	migrationVMImportMu sync.Mutex

	replicationMu      sync.Mutex
	runningReplication map[uint]struct{}
	transitionMu       sync.Mutex
	runningTransitions map[uint]struct{}
	poolDownMisses     map[string]int
	failoverWarningMu  sync.Mutex
	failoverWarnings   map[uint]map[string]struct{}

	workloadOpMu      sync.Mutex
	runningWorkloadOp map[string]string

	restoreDestinationMu      sync.Mutex
	runningRestoreDestination map[string]struct{}

	runtimeMu    sync.RWMutex
	runtimeClock replicationRuntimeClock

	// Local dataset seams keep host-level ZFS tests scoped to disposable pools.
	// Production leaves them nil and uses gzfs directly.
	localFilesystemDatasetLister func(context.Context) ([]string, error)
	localDatasetUnmounter        func(context.Context, string, bool) error
	localDatasetMounter          func(context.Context, string) error
}

type BackupEventProgress struct {
	Event           *clusterModels.BackupEvent `json:"event"`
	ProgressDataset string                     `json:"progressDataset"`
	Phase           string                     `json:"phase"`
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
	telemetryDB *gorm.DB,
	clusterService *cluster.Service,
	jailService jailServiceInterfaces.JailServiceInterface,
	networkService networkServiceInterfaces.NetworkServiceInterface,
	vmService libvirtServiceInterfaces.LibvirtServiceInterface,
	gzfsClient *gzfs.Client,
) *Service {
	return &Service{
		DB:                        db,
		TelemetryDB:               telemetryDB,
		Cluster:                   clusterService,
		Jail:                      jailService,
		Network:                   networkService,
		VM:                        vmService,
		GZFS:                      gzfsClient,
		startedAt:                 time.Now().UTC(),
		runningJobs:               make(map[uint]struct{}),
		queuedJobs:                make(map[uint]struct{}),
		runningReplication:        make(map[uint]struct{}),
		runningTransitions:        make(map[uint]struct{}),
		poolDownMisses:            make(map[string]int),
		failoverWarnings:          make(map[uint]map[string]struct{}),
		runningWorkloadOp:         make(map[string]string),
		runningRestoreDestination: make(map[string]struct{}),
		runtimeClock:              realReplicationRuntimeClock{},
	}
}

func (s *Service) replicationGuestExistsLocally(guestType string, guestID uint) bool {
	if s == nil || s.DB == nil || guestID == 0 {
		return false
	}

	switch strings.TrimSpace(guestType) {
	case clusterModels.ReplicationGuestTypeVM:
		var count int64
		if err := s.DB.Model(&vmModels.VM{}).Where("rid = ?", guestID).Limit(1).Count(&count).Error; err != nil {
			return false
		}
		return count > 0
	case clusterModels.ReplicationGuestTypeJail:
		var count int64
		if err := s.DB.Model(&jailModels.Jail{}).Where("ct_id = ?", guestID).Limit(1).Count(&count).Error; err != nil {
			return false
		}
		return count > 0
	default:
		return false
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

func backupZeltaArgs(sourceDataset, zeltaEndpoint, snapshotName string, recursive bool) []string {
	args := []string{
		"backup",
		"--json",
		"--incremental",
		"--snapshot",
		"--snap-name",
		snapshotName,
	}
	if !recursive {
		args = append(args, "--depth", "1")
	}
	return append(args, sourceDataset, zeltaEndpoint)
}

func (s *Service) backupWithEventProgressSnapshotNameRecursive(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset, destSuffix string,
	eventID uint,
	snapshotName string,
	recursive bool,
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
		backupZeltaArgs(sourceDataset, zeltaEndpoint, snapshotName, recursive)...,
	)
}

func (s *Service) RegisterJobs() {
	db.QueueRegisterJSONWithPolicy(backupJobQueueName, db.QueueHandlerErrorConsume, func(ctx context.Context, payload backupJobPayload) error {
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
) error {
	if job == nil {
		return nil
	}

	target := &job.Target
	if target == nil || strings.TrimSpace(target.SSHHost) == "" {
		return nil
	}

	remoteDataset := normalizeDatasetPath(strings.TrimSpace(target.BackupRoot))
	suffix := normalizeDatasetPath(destSuffix)
	if suffix != "" {
		remoteDataset = normalizeDatasetPath(remoteDataset + "/" + suffix)
	}
	if remoteDataset == "" {
		return nil
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
		return fmt.Errorf("sync_target_metadata_failed: %w (output: %s)", err, strings.TrimSpace(output))
	}
	return nil
}

func (s *Service) Run(ctx context.Context) {
	<-ctx.Done()
}

func (s *Service) StartBackupScheduler(ctx context.Context) {
	if err := s.ReconcileBackupTargetSSHKeys(); err != nil {
		logger.L.Warn().Err(err).Msg("failed_to_reconcile_backup_target_ssh_keys")
	}

	if err := s.ReconcileEncryptionKeys(); err != nil {
		logger.L.Warn().Err(err).Msg("failed_to_reconcile_encryption_keys")
	}

	s.AutoDiscoverAndRegisterKeys(ctx)

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
			if err := s.ReconcileEncryptionKeys(); err != nil {
				logger.L.Warn().Err(err).Msg("periodic_encryption_key_reconcile_failed")
			}
			s.AutoDiscoverAndRegisterKeys(ctx)
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
			// Safety net: if the job hasn't run in over 25 hours
			// despite NextRunAt being in the future, the clock likely
			// jumped backward — force the run.
			if job.LastRunAt != nil && now.Sub(*job.LastRunAt) > maxBackupStallAge {
				logger.L.Warn().
					Uint("job_id", job.ID).
					Time("last_run", *job.LastRunAt).
					Time("next_run", *job.NextRunAt).
					Msg("scheduled_backup_stalled_forcing_run")
			} else {
				continue
			}
		}

		// Catch-up guard: if the job is more than the catch-up window
		// past due (node was down for hours/days), skip this run and
		// just advance NextRunAt. Prevents thundering herd on restart.
		if now.Sub(*job.NextRunAt) > maxBackupCatchUpWindow {
			logger.L.Warn().
				Uint("job_id", job.ID).
				Time("next_run", *job.NextRunAt).
				Msg("scheduled_backup_too_far_past_due_skipping")
			_ = s.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", job.ID).Update("next_run_at", nextAt).Error
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

		// Spread jobs across the tick window to avoid thundering herd.
		if maxBackupEnqueueJitter > 0 {
			jitter := time.Duration(rand.Int63n(int64(maxBackupEnqueueJitter)))
			select {
			case <-ctx.Done():
				s.releaseReservedJob(job.ID)
				return ctx.Err()
			case <-time.After(jitter):
			}
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

func (s *Service) runBackupJob(ctx context.Context, job *clusterModels.BackupJob) (resultErr error) {
	if !s.beginJob(job.ID) {
		return fmt.Errorf("backup_job_already_running")
	}

	defer s.releaseJob(job.ID)
	backupEventCreated := false
	defer func() {
		if backupEventCreated || s.TelemetryDB == nil {
			return
		}

		auditStatus := "success"
		errMsg := ""
		if resultErr != nil {
			auditStatus = "failed"
			errMsg = resultErr.Error()
		}
		db.FinalizeAsyncAuditRecord(s.TelemetryDB, "backup_job_run", job.ID, auditStatus, errMsg, map[string]any{
			"status": auditStatus,
			"error":  errMsg,
		})
	}()

	jobGuestType, jobGuestID := backupJobGuestIdentity(job)

	// Dataset-mode jobs don't have a guest identity, so use the
	// source dataset path as the lock key to prevent concurrent
	// operations on the same dataset.
	if jobGuestType == "" && jobGuestID == 0 &&
		job.Mode == clusterModels.BackupJobModeDataset &&
		strings.TrimSpace(job.SourceDataset) != "" {
		jobGuestType = clusterModels.BackupJobModeDataset
		jobGuestID = datasetHash(strings.TrimSpace(job.SourceDataset))
	}

	if jobGuestType != "" && jobGuestID > 0 && s.Cluster != nil {
		localNodeID := s.localNodeID()
		allowed, leaseErr := cluster.CanNodeMutateProtectedGuest(s.DB, jobGuestType, jobGuestID, localNodeID)
		if leaseErr != nil {
			runErr := fmt.Errorf("replication_lease_check_failed: %w", leaseErr)
			s.updateBackupJobResult(job, runErr, false)
			return runErr
		}
		if !allowed {
			runErr := fmt.Errorf("replication_lease_not_owned")
			s.updateBackupJobResult(job, runErr, false)
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
		s.updateBackupJobResult(job, runErr, false)
		return runErr
	}
	defer s.releaseWorkloadOperation(jobGuestType, jobGuestID)

	if job.StopBeforeBackup {
		logger.L.Debug().Uint("job_id", job.ID).Msg("stop_before_backup_enabled")
	}

	if err := s.ensureBackupTargetSSHKeyMaterialized(&job.Target); err != nil {
		runErr := fmt.Errorf("backup_target_ssh_key_materialize_failed: %w", err)
		s.updateBackupJobResult(job, runErr, false)
		return runErr
	}

	if !job.Target.Enabled {
		runErr := fmt.Errorf("backup_target_disabled")
		s.updateBackupJobResult(job, runErr, false)
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
			s.updateBackupJobResult(job, runErr, false)
			return runErr
		}
	case clusterModels.BackupJobModeJail:
		sourceDataset = strings.TrimSpace(job.JailRootDataset)
		if sourceDataset == "" {
			runErr := fmt.Errorf("jail_root_dataset_required")
			s.updateBackupJobResult(job, runErr, false)
			return runErr
		}
	case clusterModels.BackupJobModeVM:
		sourceDataset = strings.TrimSpace(job.SourceDataset)
		if sourceDataset == "" {
			runErr := fmt.Errorf("source_dataset_required")
			s.updateBackupJobResult(job, runErr, false)
			return runErr
		}
	default:
		runErr := fmt.Errorf("invalid_backup_job_mode")
		s.updateBackupJobResult(job, runErr, false)
		return runErr
	}
	if err := cluster.ValidateBackupJobSafetyWithDB(ctx, s.DB, job); err != nil {
		runErr := fmt.Errorf("backup_job_safety_validation_failed: %w", err)
		s.updateBackupJobResult(job, runErr, false)
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
			s.updateBackupJobResult(job, runErr, false)
			return runErr
		}

		sources, err := s.resolveVMBackupSourceDatasets(ctx, vmRID, sourceDataset)
		if err != nil {
			runErr := fmt.Errorf("resolve_vm_backup_sources_failed: %w", err)
			s.updateBackupJobResult(job, runErr, false)
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
				s.updateBackupJobResult(job, runErr, false)
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
				s.updateBackupJobResult(job, runErr, false)
				return runErr
			}

			validatedSources = append(validatedSources, vmSource)
		}

		if len(validatedSources) == 0 {
			runErr := fmt.Errorf("vm_source_datasets_not_found")
			s.updateBackupJobResult(job, runErr, false)
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

	// Detect whether any source dataset is encrypted.
	encrypted := false
	if job.Mode == clusterModels.BackupJobModeVM {
		for _, vmSource := range vmSourceDatasets {
			ds, dsErr := s.getLocalDataset(ctx, vmSource)
			if dsErr == nil && ds != nil && ds.IsEncrypted() {
				encrypted = true
				break
			}
		}
	} else {
		ds, dsErr := s.getLocalDataset(ctx, sourceDataset)
		if dsErr == nil && ds != nil {
			encrypted = ds.IsEncrypted()
		}
	}

	// Verify encryption keys are loaded before starting the backup.
	// Without this check the zelta send would fail with a cryptic ZFS error.
	if encrypted {
		if job.Mode == clusterModels.BackupJobModeVM {
			for _, vmSource := range vmSourceDatasets {
				ds, dsErr := s.getLocalDataset(ctx, vmSource)
				if dsErr != nil || ds == nil || !ds.IsEncrypted() {
					continue
				}
				keyLoaded, keyErr := s.ensureEncryptionKeyForDataset(ctx, ds)
				if keyErr != nil {
					runErr := fmt.Errorf("encryption_key_load_failed_for_%s: %w", vmSource, keyErr)
					s.updateBackupJobResult(job, runErr, encrypted)
					return runErr
				}
				if !keyLoaded {
					runErr := fmt.Errorf("encryption_key_not_available_for_%s: run 'zfs load-key %s' first", vmSource, vmSource)
					s.updateBackupJobResult(job, runErr, encrypted)
					return runErr
				}
			}
		} else {
			ds, dsErr := s.getLocalDataset(ctx, sourceDataset)
			if dsErr == nil && ds != nil {
				keyLoaded, keyErr := s.ensureEncryptionKeyForDataset(ctx, ds)
				if keyErr != nil {
					runErr := fmt.Errorf("encryption_key_load_failed_for_%s: %w", sourceDataset, keyErr)
					s.updateBackupJobResult(job, runErr, encrypted)
					return runErr
				}
				if !keyLoaded {
					runErr := fmt.Errorf("encryption_key_not_available_for_%s: run 'zfs load-key %s' first", sourceDataset, sourceDataset)
					s.updateBackupJobResult(job, runErr, encrypted)
					return runErr
				}
			}
		}
	}

	destSuffix := s.backupDestSuffixForMode(job.Mode, strings.TrimSpace(job.DestSuffix), sourceDataset)
	if job.Mode == clusterModels.BackupJobModeVM {
		destSuffix = s.backupDestSuffixForVMSource(strings.TrimSpace(job.DestSuffix), sourceDataset)
	} else if job.Mode == clusterModels.BackupJobModeJail {
		destSuffix = s.backupDestSuffixForJailSource(strings.TrimSpace(job.DestSuffix), sourceDataset)
	}
	backupSnapPrefix := backupSnapshotPrefixForJob(job.ID)
	backupScopes := s.backupRunScopes(job, sourceDataset, destSuffix, vmSourceDatasets)
	operationRoots := make([]string, 0, len(backupScopes))
	for _, scope := range backupScopes {
		operationRoots = append(operationRoots, scope.sourceDataset)
	}
	acquired, holder, heldRoots := s.acquireDatasetOperations(operationRoots)
	if !acquired {
		runErr := fmt.Errorf("backup_dataset_operation_conflict: holder=%s", holder)
		s.updateBackupJobResult(job, runErr, encrypted)
		return runErr
	}
	defer s.releaseDatasetOperations(heldRoots)

	if err := s.validateBackupScopesDoNotOverlapTarget(ctx, job, backupScopes); err != nil {
		runErr := fmt.Errorf("backup_scope_validation_failed: %w", err)
		s.updateBackupJobResult(job, runErr, encrypted)
		return runErr
	}
	event.TargetEndpoint = job.Target.ZeltaEndpoint(destSuffix)
	if err := s.DB.Create(&event).Error; err != nil {
		runErr := fmt.Errorf("create_backup_event_failed: %w", err)
		s.updateBackupJobResult(job, runErr, false)
		return runErr
	}
	backupEventCreated = true
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

	var output string
	var runErr error
	var lastVMFailedSource string
	var lastVMFailedDestSuffix string
	var successfulSnapshotName string

	runDatasetBackupPass := func(datasetSource, datasetDestSuffix string) (string, backupOutputKind, error) {
		successfulSnapshotName = ""
		snapshotName := backupSnapshotNameForJob(job.ID)
		partOutput, partErr := s.backupWithEventProgressSnapshotNameRecursive(
			ctx,
			&job.Target,
			datasetSource,
			datasetDestSuffix,
			event.ID,
			snapshotName,
			job.Recursive,
		)
		outcome := classifyBackupOutput(partOutput)
		if partErr == nil {
			if code := outcome.errorCode(); code != "" {
				partErr = errors.New(code)
			} else {
				successfulSnapshotName = snapshotName
			}
		}
		return partOutput, outcome, partErr
	}

	runVMBackupPass := func() error {
		lastVMFailedSource = ""
		lastVMFailedDestSuffix = ""
		successfulSnapshotName = ""
		vmSnapshotName := backupSnapshotNameForJob(job.ID)

		for idx, vmSource := range vmSourceDatasets {
			vmDestSuffix := s.backupDestSuffixForVMSource(strings.TrimSpace(job.DestSuffix), vmSource)
			output = appendOutput(output, fmt.Sprintf("vm_dataset_backup_start[%d/%d]: %s -> %s", idx+1, len(vmSourceDatasets), vmSource, job.Target.ZeltaEndpoint(vmDestSuffix)))
			partOutput, partErr := s.backupWithEventProgressSnapshotNameRecursive(ctx, &job.Target, vmSource, vmDestSuffix, event.ID, vmSnapshotName, job.Recursive)
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

		successfulSnapshotName = vmSnapshotName
		return nil
	}

	defer func() {
		stopHeartbeat()
		s.finalizeBackupEvent(&event, runErr, output)
		s.updateBackupJobResult(job, runErr, encrypted)

		logger.L.Info().
			Uint("job_id", job.ID).
			Str("status", event.Status).
			Err(runErr).
			Msg("zelta_backup_completed")
	}()
	defer func() {
		resultErr = runErr
	}()
	defer recoverOperationPanic("backup_job", &runErr)

	// Re-verify the replication lease immediately before the
	// transfer starts. The lease may have expired or been
	// transferred to another node since the initial check.
	if jobGuestType != "" && jobGuestID > 0 && s.Cluster != nil {
		allowed, leaseErr := cluster.CanNodeMutateProtectedGuest(s.DB, jobGuestType, jobGuestID, s.localNodeID())
		if leaseErr != nil {
			runErr = fmt.Errorf("pre_transfer_lease_check_failed: %w", leaseErr)
			s.updateBackupJobResult(job, runErr, encrypted)
			return runErr
		}
		if !allowed {
			runErr = fmt.Errorf("lease_lost_before_transfer: ownership transferred to another node")
			s.updateBackupJobResult(job, runErr, encrypted)
			return runErr
		}
	}

	guestRestore, guestStoppedByBackup, quiesceErr := s.quiesceBackupGuest(job, vmRID)
	if quiesceErr != nil {
		runErr = quiesceErr
		output = appendOutput(output, runErr.Error())
		return runErr
	}
	defer func() {
		if !guestStoppedByBackup || guestRestore == nil {
			return
		}

		restartErr := guestRestore()
		if restartErr == nil {
			return
		}

		restartErr = fmt.Errorf("failed_to_restore_guest_running_state: %w", restartErr)
		output = appendOutput(output, restartErr.Error())
		runErr = errors.Join(runErr, restartErr)
		logger.L.Warn().Err(restartErr).Uint("job_id", job.ID).Msg("failed_to_restart_guest_after_backup")
	}()
	var topologyArchives []archivedBackupTopology
	backupTransferStarted := false
	defer func() {
		if backupTransferStarted || len(topologyArchives) == 0 {
			return
		}
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		s.rollbackBackupTopologyArchives(rollbackCtx, &job.Target, topologyArchives)
	}()

	if strings.TrimSpace(job.Target.SSHHost) != "" {
		// Inspect the currently active tree before topology preflight is allowed
		// to rename it. Otherwise an unowned snapshot could be moved into a
		// generation dataset, disappear from this check, and later be mistaken
		// for job-owned cleanup state.
		for _, sc := range backupScopes {
			activeDataset := remoteActiveDatasetForSuffix(job.Target.BackupRoot, sc.destSuffix)
			if activeDataset == "" {
				continue
			}
			foreign, listErr := s.findForeignTargetSnapshots(ctx, job, sc.sourceDataset, activeDataset, backupScopes)
			if listErr != nil {
				runErr = fmt.Errorf("pre_backup_foreign_snapshot_check_failed_%s: %w", activeDataset, listErr)
				output = appendOutput(output, runErr.Error())
				return runErr
			}
			if len(foreign) > 0 {
				runErr = fmt.Errorf("backup_target_foreign_snapshots_present:%s", strings.Join(foreign, ","))
				output = appendOutput(output, runErr.Error())
				return runErr
			}
		}

		archives, archiveErr := s.archiveChangedBackupTopologies(ctx, job, backupScopes)
		if archiveErr != nil {
			runErr = fmt.Errorf("backup_topology_preflight_failed: %w", archiveErr)
			output = appendOutput(output, runErr.Error())
			return runErr
		}
		topologyArchives = archives
		for _, archive := range archives {
			output = appendOutput(output, fmt.Sprintf(
				"backup_topology_rotated: %s -> %s",
				archive.Active,
				archive.Archived,
			))
		}
	}

	// Stopping a guest can take long enough for an HA lease to change hands.
	// Check once more at the actual transfer boundary and let the restart defer
	// restore the original running state on failure.
	if jobGuestType != "" && jobGuestID > 0 && s.Cluster != nil {
		allowed, leaseErr := cluster.CanNodeMutateProtectedGuest(s.DB, jobGuestType, jobGuestID, s.localNodeID())
		if leaseErr != nil {
			runErr = fmt.Errorf("pre_transfer_lease_check_failed: %w", leaseErr)
			output = appendOutput(output, runErr.Error())
			return runErr
		}
		if !allowed {
			runErr = fmt.Errorf("lease_lost_before_transfer: ownership transferred to another node")
			output = appendOutput(output, runErr.Error())
			return runErr
		}
	}

	backupTransferStarted = true
	if job.Mode == clusterModels.BackupJobModeVM {
		runErr = runVMBackupPass()
	} else {
		attemptOutput, outcome, attemptErr := runDatasetBackupPass(sourceDataset, destSuffix)
		output = appendOutput(output, attemptOutput)
		runErr = attemptErr
		if runErr == nil && outcome == backupOutputUpToDate {
			logger.L.Info().
				Uint("job_id", job.ID).
				Str("source", sourceDataset).
				Str("target", event.TargetEndpoint).
				Msg("backup_up_to_date_noop")
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
			retryOutput, retryOutcome, retryErr := runDatasetBackupPass(sourceDataset, destSuffix)
			output = appendOutput(output, retryOutput)
			runErr = retryErr
			if runErr == nil && retryOutcome == backupOutputUpToDate {
				logger.L.Info().
					Uint("job_id", job.ID).
					Str("source", sourceDataset).
					Str("target", event.TargetEndpoint).
					Msg("backup_up_to_date_noop_after_resume_abort")
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

		recovered := false
		recoveryActive := remoteActiveDatasetForSuffix(job.Target.BackupRoot, reseedDestSuffix)
		if recoveryActive != "" {
			localSnaps, localErr := s.listLocalSnapshotsForDataset(ctx, reseedSource)
			remoteSnaps, remoteErr := s.listRemoteSnapshotsForDataset(ctx, &job.Target, recoveryActive)
			if localErr != nil {
				logger.L.Warn().Err(localErr).Uint("job_id", job.ID).Str("source", reseedSource).Msg("backup_recovery_local_snapshot_list_failed")
			}
			if remoteErr != nil {
				logger.L.Warn().Err(remoteErr).Uint("job_id", job.ID).Str("target", recoveryActive).Msg("backup_recovery_remote_snapshot_list_failed")
			}
			if localErr == nil && remoteErr == nil {
				if _, ok := latestCommonBackupSnapshot(localSnaps, remoteSnaps, backupSnapPrefix); ok {
					foreign, listErr := s.findForeignTargetSnapshots(ctx, job, reseedSource, recoveryActive, backupScopes)
					if listErr != nil {
						runErr = fmt.Errorf("backup_recovery_foreign_snapshot_check_failed_%s: %w", recoveryActive, listErr)
						output = appendOutput(output, runErr.Error())
						return runErr
					}
					if len(foreign) > 0 {
						runErr = fmt.Errorf("backup_target_foreign_snapshots_present:%s", strings.Join(foreign, ","))
						output = appendOutput(output, runErr.Error())
						return runErr
					}

					logger.L.Info().
						Uint("job_id", job.ID).
						Str("source", reseedSource).
						Str("target", job.Target.ZeltaEndpoint(reseedDestSuffix)).
						Str("reason", runErr.Error()).
						Msg("backup_diverged_recovery_starting")

					var recoverErr error
					if job.Mode == clusterModels.BackupJobModeVM {
						recoverErr = runVMBackupPass()
					} else {
						recoverOutput, _, rErr := runDatasetBackupPass(reseedSource, reseedDestSuffix)
						output = appendOutput(output, recoverOutput)
						recoverErr = rErr
					}

					if recoverErr == nil {
						runErr = nil
						recovered = true
						logger.L.Info().
							Uint("job_id", job.ID).
							Str("source", reseedSource).
							Str("target", job.Target.ZeltaEndpoint(reseedDestSuffix)).
							Msg("backup_recovered_without_reseed")
					} else {
						logger.L.Warn().Err(recoverErr).Uint("job_id", job.ID).Str("source", reseedSource).Msg("backup_diverged_recovery_failed_falling_back_to_reseed")
					}
				}
			}
		}

		if !recovered {
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
					retryOutput, retryOutcome, retryErr := runDatasetBackupPass(sourceDataset, destSuffix)
					output = appendOutput(output, retryOutput)
					runErr = retryErr
					if runErr == nil && retryOutcome == backupOutputUpToDate {
						logger.L.Info().
							Uint("job_id", job.ID).
							Str("source", sourceDataset).
							Str("target", event.TargetEndpoint).
							Msg("backup_up_to_date_noop_after_reseed")
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
	}

	if runErr == nil {
		const phase = "backup_phase: finalizing"
		output = appendOutput(output, phase)
		if appendErr := s.AppendBackupEventOutput(event.ID, phase); appendErr != nil {
			logger.L.Warn().Uint("event_id", event.ID).Err(appendErr).Msg("append_backup_event_phase_failed")
		}

		if successfulSnapshotName == "" {
			runErr = fmt.Errorf("backup_completed_without_verified_snapshot")
		} else if _, commitErr := s.commitBackupSnapshot(ctx, job, successfulSnapshotName, backupScopes); commitErr != nil {
			runErr = fmt.Errorf("backup_commit_failed: %w", commitErr)
		}
		if runErr != nil {
			output = appendOutput(output, runErr.Error())
		}
	}

	if runErr == nil {
		for _, scope := range backupScopes {
			if metaErr := s.syncTargetBackupJobMetadata(ctx, job, scope.sourceDataset, scope.destSuffix); metaErr != nil {
				runErr = fmt.Errorf(
					"backup_target_metadata_sync_failed: source=%s: %w",
					scope.sourceDataset,
					metaErr,
				)
				output = appendOutput(output, runErr.Error())
				break
			}
		}
	}

	if runErr == nil && job.PruneKeepLast > 0 {
		commitCoordinator, coordinatorErr := backupCommitCoordinatorScope(job, backupScopes)
		if coordinatorErr != nil {
			runErr = fmt.Errorf("backup_prune_commit_coordinator_failed: %w", coordinatorErr)
			output = appendOutput(output, runErr.Error())
		}
		retentionCommitRoot := ""
		if runErr == nil {
			retentionCommitRoot = remoteActiveDatasetForSuffix(
				job.Target.BackupRoot,
				backupScopes[commitCoordinator].destSuffix,
			)
		}
		for _, scope := range backupScopes {
			if runErr != nil {
				break
			}
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
			remoteActiveDataset := remoteActiveDatasetForSuffix(job.Target.BackupRoot, scopeDestSuffix)
			remoteSnapshots, remoteListErr := s.listRemoteSnapshotsForDatasetRecursive(ctx, &job.Target, remoteActiveDataset)
			if remoteListErr != nil {
				output = appendOutput(output, fmt.Sprintf(
					"backup_prune_skipped_commit_inventory_failed: source=%s error=%v",
					scopeSource,
					remoteListErr,
				))
				logger.L.Warn().Err(remoteListErr).Uint("job_id", job.ID).Str("source", scopeSource).Msg("backup_prune_commit_inventory_failed")
				continue
			}
			retentionProofs, retentionErr := s.backupRetentionEligibleSnapshotProofs(
				ctx,
				job,
				retentionCommitRoot,
				remoteSnapshots,
				backupScopes,
			)
			if retentionErr != nil {
				output = appendOutput(output, fmt.Sprintf(
					"backup_prune_skipped_commit_validation_failed: source=%s error=%v",
					scopeSource,
					retentionErr,
				))
				logger.L.Warn().Err(retentionErr).Uint("job_id", job.ID).Str("source", scopeSource).Msg("backup_prune_commit_validation_failed")
				continue
			}
			retentionRemoteSnapshots := filterBackupSnapshotsByProof(remoteSnapshots, retentionProofs.Target)
			retentionLocalSnapshots := filterBackupSnapshotsByProof(localSnapshots, retentionProofs.Source)

			_, pruneOutput, pruneErr := s.PruneCandidatesWithTarget(ctx, &job.Target, scopeSource, scopeDestSuffix, 0)
			output = appendOutput(output, pruneOutput)
			if pruneErr != nil {
				logger.L.Warn().Err(pruneErr).Uint("job_id", job.ID).Str("source", scopeSource).Str("dest_suffix", scopeDestSuffix).Msg("backup_prune_scan_failed")
			}

			var pruneCandidates []string
			if localListErr == nil {
				protect, protectErr := s.localRetentionProtectSet(
					ctx,
					&job.Target,
					scopeSource,
					remoteActiveDataset,
					backupSnapPrefix,
					localSnapshots,
				)
				if protectErr != nil {
					output = appendOutput(output, fmt.Sprintf(
						"backup_prune_local_skipped_base_protection_failed: source=%s error=%v",
						scopeSource,
						protectErr,
					))
					logger.L.Warn().
						Err(protectErr).
						Uint("job_id", job.ID).
						Str("source", scopeSource).
						Msg("backup_prune_local_skipped_base_protection_failed")
				} else {
					pruneCandidates = buildLocalRetentionPruneCandidates(retentionLocalSnapshots, job.PruneKeepLast, protect, backupSnapPrefix)
				}
			}

			if len(pruneCandidates) > 0 {
				if err := s.destroyLocalBackupSnapshotsWithProof(ctx, pruneCandidates, retentionProofs.Source); err != nil {
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

			remoteDataset := remoteActiveDataset
			if remoteDataset == "" {
				logger.L.Warn().Uint("job_id", job.ID).Str("source", scopeSource).Msg("backup_prune_target_skipped_remote_dataset_empty")
				continue
			}

			targetPruneCandidates, targetPruneOutput, targetPruneErr := s.PruneTargetCandidatesWithSource(ctx, &job.Target, scopeSource, scopeDestSuffix, 0)
			output = appendOutput(output, targetPruneOutput)

			if targetPruneErr != nil {
				logger.L.Warn().Err(targetPruneErr).Uint("job_id", job.ID).Str("source", scopeSource).Str("dest_suffix", scopeDestSuffix).Msg("backup_prune_target_scan_failed")
			} else {
				targetPruneCandidates = buildBKRetentionPruneCandidates(
					retentionRemoteSnapshots,
					job.PruneKeepLast,
					snapshotCandidateSet(snapshotNames(retentionRemoteSnapshots)),
					backupSnapPrefix,
				)
			}

			if len(targetPruneCandidates) > 0 {
				if err := s.destroyTargetBackupSnapshotsWithProof(ctx, &job.Target, targetPruneCandidates, retentionProofs.Target); err != nil {
					logger.L.Warn().Err(err).Uint("job_id", job.ID).Str("source", scopeSource).Int("candidate_count", len(targetPruneCandidates)).Msg("backup_prune_target_destroy_failed")
				} else {
					logger.L.Info().Uint("job_id", job.ID).Str("source", scopeSource).Int("pruned", len(targetPruneCandidates)).Msg("backup_prune_target_completed")
				}
			} else {
				logger.L.Debug().Uint("job_id", job.ID).Str("source", scopeSource).Int("keep_last", job.PruneKeepLast).Msg("backup_prune_target_no_candidates")
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
	return libvirtServiceInterfaces.IsDomainNotFoundError(err)
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
		"-o", "name,creation,used,refer,guid",
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
		for _, name := range names[:deleteCount] {
			if safeSet != nil {
				if _, ok := safeSet[name]; !ok {
					continue
				}
			}
			candidates = append(candidates, name)
		}
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

func (s *Service) updateBackupJobResult(job *clusterModels.BackupJob, runErr error, encrypted bool) {
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
		Encrypted:  encrypted,
	}

	if s.syncBackupJobRuntimeState(update) {
		return
	}

	updates := map[string]any{
		"last_run_at": update.LastRunAt,
		"last_status": update.LastStatus,
		"last_error":  update.LastError,
		"next_run_at": update.NextRunAt,
		"encrypted":   update.Encrypted,
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
		"jobId":       update.JobID,
		"lastRunAt":   update.LastRunAt,
		"lastStatus":  update.LastStatus,
		"lastError":   update.LastError,
		"nextRunAt":   update.NextRunAt,
		"nextRunOnly": update.NextRunOnly,
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

	if event.JobID != nil && s.TelemetryDB != nil {
		auditStatus := "success"
		errMsg := ""
		if runErr != nil {
			auditStatus = "failed"
			errMsg = runErr.Error()
		}
		db.FinalizeAsyncAuditRecord(s.TelemetryDB, "backup_job_run", *event.JobID, auditStatus, errMsg, map[string]any{
			"eventId": event.ID,
			"status":  auditStatus,
			"error":   errMsg,
		})
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
		Phase:      backupEventProgressPhase(event.Output),
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
	if out.Phase == "finalizing" && out.TotalBytes != nil {
		// Zelta emits its replication-size JSON only after send/receive exits.
		// Later verification and commit work can still keep the event running.
		out.MovedBytes = out.TotalBytes
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

func (s *Service) ReconcileBackupRunAudits() error {
	if s == nil || s.DB == nil || s.TelemetryDB == nil {
		return nil
	}

	var pendingAudits []infoModels.AuditRecord
	if err := s.TelemetryDB.
		Where("async_job_type = ? AND status = ? AND async_job_id IS NOT NULL", "backup_job_run", "pending").
		Find(&pendingAudits).Error; err != nil {
		return err
	}

	jobIDs := make([]uint, 0, len(pendingAudits))
	seenJobIDs := make(map[uint]struct{}, len(pendingAudits))
	for _, audit := range pendingAudits {
		if audit.AsyncJobID == nil || *audit.AsyncJobID == 0 {
			continue
		}
		if _, exists := seenJobIDs[*audit.AsyncJobID]; exists {
			continue
		}
		seenJobIDs[*audit.AsyncJobID] = struct{}{}
		jobIDs = append(jobIDs, *audit.AsyncJobID)
	}
	if len(jobIDs) == 0 {
		return nil
	}

	var jobs []clusterModels.BackupJob
	if err := s.DB.Where("id IN ?", jobIDs).Find(&jobs).Error; err != nil {
		return err
	}

	for _, job := range jobs {
		status := strings.ToLower(strings.TrimSpace(job.LastStatus))
		if job.LastRunAt == nil || (status != "success" && status != "failed") {
			continue
		}

		db.FinalizeAsyncAuditRecordsBefore(
			s.TelemetryDB,
			"backup_job_run",
			job.ID,
			status,
			job.LastError,
			map[string]any{
				"status": status,
				"error":  job.LastError,
			},
			*job.LastRunAt,
		)
	}

	return nil
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
	if guestType == clusterModels.BackupJobModeDataset {
		return fmt.Sprintf("dataset:%s", strconv.FormatUint(uint64(guestID), 16))
	}
	if guestType != clusterModels.BackupJobModeVM && guestType != clusterModels.BackupJobModeJail {
		return ""
	}
	return fmt.Sprintf("%s:%d", guestType, guestID)
}

func datasetHash(s string) uint {
	h := uint(0)
	for i := 0; i < len(s); i++ {
		h = h*31 + uint(s[i])
	}
	return h
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

func (s *Service) AcquireGuestLock(guestType string, guestID uint, operation string) (bool, string) {
	return s.acquireWorkloadOperation(guestType, guestID, operation)
}

func (s *Service) ReleaseGuestLock(guestType string, guestID uint) {
	s.releaseWorkloadOperation(guestType, guestID)
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
