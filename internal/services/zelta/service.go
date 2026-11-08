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
	"slices"
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
	"github.com/alchemillahq/sylve/internal/downloadstorage"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/remoteexec"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/google/uuid"
	"github.com/hashicorp/raft"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

var (
	replicationSizeRegex = regexp.MustCompile(`"replicationSize"\s*:\s*"?([0-9]+)"?`)
	syncingSizeRegex     = regexp.MustCompile(`syncing:\s*([0-9]+(?:\.[0-9]+)?)\s*([KMGTPE]?)(i?B?)\b`)
	sentSizeRegex        = regexp.MustCompile(`(?im)\b([0-9]+(?:\.[0-9]+)?)\s*([KMGTPE]?)(i?B?)\s+sent\b`)
	receivedSizeRegex    = regexp.MustCompile(`(?im)\b([0-9]+(?:\.[0-9]+)?)\s*([KMGTPE]?)(i?B?)\s+(?:full|incremental)\s+stream\s+received\b`)
)

// backupJobPayload is the goqite queue payload for a backup job
type backupJobPayload struct {
	JobID          uint   `json:"job_id"`
	OperationToken string `json:"operation_token,omitempty"`
	HolderNodeID   string `json:"holder_node_id,omitempty"`
}

type backupEncryptionObservation struct {
	known     bool
	encrypted bool
}

func (o backupEncryptionObservation) optional() *bool {
	if !o.known {
		return nil
	}
	value := o.encrypted
	return &value
}

func (s *Service) detectBackupSourcesEncryption(
	ctx context.Context,
	sources []string,
	recursive bool,
) (bool, error) {
	if len(sources) == 0 {
		return false, fmt.Errorf("backup_source_dataset_required")
	}

	for _, source := range sources {
		source = normalizeDatasetPath(source)
		if source == "" {
			return false, fmt.Errorf("backup_source_dataset_required")
		}

		if recursive {
			encrypted, err := s.isDatasetEncrypted(ctx, source)
			if err != nil {
				return false, fmt.Errorf("backup_encryption_detection_failed_for_%s: %w", source, err)
			}
			if encrypted {
				return true, nil
			}
			continue
		}

		dataset, err := s.getLocalDataset(ctx, source)
		if err != nil {
			return false, fmt.Errorf("backup_encryption_detection_failed_for_%s: %w", source, err)
		}
		if dataset == nil {
			return false, fmt.Errorf("backup_encryption_detection_failed_for_%s: dataset_not_found", source)
		}
		if dataset.IsEncrypted() {
			return true, nil
		}
	}

	return false, nil
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
	DB           *gorm.DB
	TelemetryDB  *gorm.DB
	Cluster      *cluster.Service
	Jail         jailServiceInterfaces.JailServiceInterface
	Network      networkServiceInterfaces.NetworkServiceInterface
	VM           libvirtServiceInterfaces.LibvirtServiceInterface
	GZFS         *gzfs.Client
	startedAt    time.Time
	mutationGate interface {
		EnterMutation(context.Context) (context.Context, func(), error)
	}

	DownloadStorage *downloadstorage.Resolver

	jobMu       sync.Mutex
	runningJobs map[uint]struct{}
	queuedJobs  map[uint]struct{}

	scheduledRunOutboxMu sync.Mutex

	migrationVMImportMu sync.Mutex

	replicationMu      sync.Mutex
	runningReplication map[uint]struct{}
	transitionMu       sync.Mutex
	runningTransitions map[uint]struct{}
	poolDownMisses     map[string]int
	failoverWarningMu  sync.Mutex
	failoverWarnings   map[uint]map[string]struct{}
	forcedPromotionMu  sync.Mutex
	forcedPromotions   map[uint]replicationForcedPromotionObservation

	crashRecoveryWarnings map[uint]string

	replicationFenceMu           sync.Mutex
	replicationFenceObservations map[uint]replicationFenceObservation
	replicationLeaseAuthorities  map[uint]replicationLeaseAuthority
	replicationAuthorityEpochs   map[uint]uint64
	replicationStartupReady      bool

	workloadOpMu      sync.Mutex
	runningWorkloadOp map[string]string

	restoreDestinationMu       sync.Mutex
	runningRestoreDestination  map[string]struct{}
	targetRestoreOperationMu   sync.Mutex
	activeTargetRestoreTokens  map[string]struct{}
	startedTargetRestoreTokens map[string]struct{}
	queuedTargetRestoreTokens  map[string]struct{}

	backupTargetKeyMu    sync.Mutex
	backupTargetKeyUsers map[string]uint

	backupTargetProvisionMu     sync.Mutex
	activeTargetProvisionTokens map[string]struct{}

	runtimeMu    sync.RWMutex
	runtimeClock replicationRuntimeClock

	// Local dataset seams keep host-level ZFS tests scoped to disposable pools.
	// Production leaves them nil and uses gzfs directly.
	localFilesystemDatasetLister      func(context.Context) ([]string, error)
	localVolumeDatasetLister          func(context.Context) ([]string, error)
	localDatasetUnmounter             func(context.Context, string, bool) error
	localDatasetMounter               func(context.Context, string) error
	replicationSnapshotDatasetState   func(context.Context, string) (string, string, error)
	replicationJailMountCleaner       func(context.Context, uint) error
	replicationSnapshotMetadataReader func(context.Context, string, string, string) ([]byte, bool, error)

	backupOperationEnqueue            func(context.Context, string, any) error
	replicationOperationEnqueue       func(context.Context, string, any) error
	replicationGuestDriverFactory     func(string) (replicationGuestDriver, error)
	restoreJobRun                     func(context.Context, *clusterModels.BackupJob, string, string) error
	restoreFromTargetOperationEnqueue func(context.Context, string, any) error
	restoreFromTargetRun              func(context.Context, *clusterModels.BackupTarget, restoreFromTargetPayload) error
}

func (s *Service) SetMutationAdmission(gate interface {
	EnterMutation(context.Context) (context.Context, func(), error)
}) {
	s.mutationGate = gate
}

func (s *Service) enterMutation(ctx context.Context) (context.Context, func(), error) {
	if s == nil || s.mutationGate == nil {
		return ctx, func() {}, nil
	}
	return s.mutationGate.EnterMutation(ctx)
}

func (s *Service) withMutation(ctx context.Context, operation func(context.Context) error) error {
	admittedCtx, release, err := s.enterMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	return operation(admittedCtx)
}

func (s *Service) jailActionContext(ctx context.Context, ctID int, action string) error {
	if contextual, ok := s.Jail.(interface {
		JailActionContext(context.Context, int, string) error
	}); ok {
		return contextual.JailActionContext(ctx, ctID, action)
	}
	return s.Jail.JailAction(ctID, action)
}

func (s *Service) jailActionForReplicationContext(
	ctx context.Context,
	ctID int,
	action string,
	transitionRunID string,
) error {
	if contextual, ok := s.Jail.(interface {
		JailActionForReplicationContext(context.Context, int, string, string) error
	}); ok {
		return contextual.JailActionForReplicationContext(ctx, ctID, action, transitionRunID)
	}
	if transition, ok := s.Jail.(interface {
		JailActionForReplication(int, string, string) error
	}); ok {
		return transition.JailActionForReplication(ctID, action, transitionRunID)
	}
	return fmt.Errorf("jail_replication_transition_action_unavailable")
}

func (s *Service) vmActionContext(ctx context.Context, vm vmModels.VM, action string) error {
	if contextual, ok := s.VM.(interface {
		LvVMActionContext(context.Context, vmModels.VM, string) error
	}); ok {
		return contextual.LvVMActionContext(ctx, vm, action)
	}
	return s.VM.LvVMAction(vm, action)
}

func (s *Service) vmActionForReplicationContext(
	ctx context.Context,
	vm vmModels.VM,
	action string,
	transitionRunID string,
) error {
	if contextual, ok := s.VM.(interface {
		LvVMActionForReplicationContext(context.Context, vmModels.VM, string, string) error
	}); ok {
		return contextual.LvVMActionForReplicationContext(ctx, vm, action, transitionRunID)
	}
	if transition, ok := s.VM.(interface {
		LvVMActionForReplication(vmModels.VM, string, string) error
	}); ok {
		return transition.LvVMActionForReplication(vm, action, transitionRunID)
	}
	return fmt.Errorf("vm_replication_transition_action_unavailable")
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
	service := &Service{
		DownloadStorage:           downloadstorage.New(db, gzfsClient),
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
		forcedPromotions:          make(map[uint]replicationForcedPromotionObservation),
		runningWorkloadOp:         make(map[string]string),
		runningRestoreDestination: make(map[string]struct{}),
		activeTargetRestoreTokens: make(map[string]struct{}),
		runtimeClock:              realReplicationRuntimeClock{},

		replicationFenceObservations: make(map[uint]replicationFenceObservation),
		replicationLeaseAuthorities:  make(map[uint]replicationLeaseAuthority),
	}
	if clusterService != nil {
		clusterService.SetBackupTargetValidator(service.ValidateTargetReadiness)
		clusterService.SetBackupTargetHostKeyEnroller(service.EnsureBackupTargetHostKey)
		service.registerCalls(clusterService)
	}
	return service
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
	args := []string{"backup", "--json", "--incremental", "--no-snapshot"}
	if !recursive {
		args = append(args, "--depth", "1")
	}
	return append(args, sourceDataset+"@"+snapshotName, zeltaEndpoint)
}

func (s *Service) backupWithEventProgressSnapshotNameRecursive(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset, destSuffix string,
	eventID uint,
	snapshotName string,
	recursive bool,
) (string, error) {
	parsedSource, err := remoteexec.ParseZFSDataset(sourceDataset)
	if err != nil {
		return "", fmt.Errorf("invalid_backup_source_dataset: %w", err)
	}
	zeltaEndpoint, err := canonicalZeltaEndpoint(target, destSuffix)
	if err != nil {
		return "", err
	}
	parsedSnapshot, err := remoteexec.ParseZFSSnapshotName(snapshotName)
	if err != nil {
		return "", fmt.Errorf("invalid_backup_snapshot_name: %w", err)
	}
	sourceDataset = parsedSource.String()
	snapshotName = parsedSnapshot.String()
	extraEnv, err := s.buildZeltaEnv(target)
	if err != nil {
		return "", err
	}
	extraEnv = setEnvValue(extraEnv, "ZELTA_LOG_LEVEL", "3")

	output, runErr := runZeltaWithEnvStreaming(
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
	return output, targetSSHHostKeyError(target, output, runErr)
}

func (s *Service) RegisterJobs() {
	db.QueueRegisterJSON(backupJobQueueName, s.handleBackupJobQueue)
	s.registerRestoreJob()
	s.registerRestoreFromTargetJob()
	s.registerReplicationJob()
	s.registerReplicationFailoverJob()
}

func (s *Service) handleBackupJobQueue(ctx context.Context, payload backupJobPayload) (retErr error) {
	admittedCtx, release, err := s.enterMutation(ctx)
	if err != nil {
		// Give the reservation back, or the scheduler won't queue the job again until a restart.
		s.releaseReservedJob(payload.JobID)
		return err
	}
	defer release()
	ctx = admittedCtx
	if payload.JobID == 0 {
		logger.L.Warn().Msg("queued_backup_job_invalid_payload_job_id")
		return nil
	}

	handle, execute, err := s.prepareQueuedBackupJobOperation(
		ctx,
		payload.JobID,
		clusterModels.BackupJobOperationBackup,
		payload.OperationToken,
		payload.HolderNodeID,
		"",
	)
	if err != nil {
		s.releaseReservedJob(payload.JobID)
		return err
	}
	if !execute {
		s.releaseReservedJob(payload.JobID)
		return nil
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			s.releaseReservedJob(payload.JobID)
			panicErr := fmt.Errorf("queued_backup_job_panicked: %v", recovered)
			logger.L.Error().
				Interface("panic", recovered).
				Uint("job_id", payload.JobID).
				Err(panicErr).
				Msg("queued_backup_job_panicked")
			// Keep the durable running token. The queue retry or restart
			// reconciler can resume it, while a terminal outbox (if one was
			// already written) prevents the data plane from running again.
			retErr = panicErr
		}
	}()

	var job clusterModels.BackupJob
	if err := s.DB.Preload("Target").First(&job, payload.JobID).Error; err != nil {
		s.releaseReservedJob(payload.JobID)
		logger.L.Warn().Err(err).Uint("job_id", payload.JobID).Msg("queued_backup_job_not_found")
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if cleanupErr := s.finishDurableBackupJobOperation(handle); cleanupErr != nil {
			return cleanupErr
		}
		return nil
	}

	runErr := s.runBackupJobWithToken(ctx, &job, payload.OperationToken)
	if errors.Is(runErr, errScheduledRunOperationRevalidation) {
		s.releaseReservedJob(payload.JobID)
		return runErr
	}
	if isJobAlreadyRunningErr(runErr) {
		// A duplicate message for the same operation token lost the local
		// execution race. The winning worker still owns the durable row.
		s.releaseReservedJob(payload.JobID)
		logger.L.Info().Uint("job_id", payload.JobID).Msg("queued_backup_job_duplicate_discarded")
		return nil
	}
	if runErr != nil {
		logger.L.Warn().Err(runErr).Uint("job_id", payload.JobID).Msg("queued_backup_job_failed")
	}
	// Terminal-result delivery owns deletion of the durable operation. If
	// delivery is deferred, the operation and node-local outbox must remain
	// paired so a later retry can finalize without rerunning the backup.
	return nil
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
	_, root, err := canonicalizeBackupTarget(target)
	if err != nil {
		return err
	}
	parsedRemoteDataset, err := remoteexec.JoinZFSDataset(root, destSuffix)
	if err != nil {
		return fmt.Errorf("backup_target_dataset_invalid: %w", err)
	}
	remoteDataset := parsedRemoteDataset.String()

	sourceDataset = strings.TrimSpace(sourceDataset)
	if sourceDataset == "" && strings.TrimSpace(job.Mode) == clusterModels.BackupJobModeJail {
		sourceDataset = strings.TrimSpace(job.JailRootDataset)
	}
	if sourceDataset == "" {
		sourceDataset = strings.TrimSpace(job.SourceDataset)
	}
	parsedSource, err := remoteexec.ParseZFSDataset(sourceDataset)
	if err != nil {
		return fmt.Errorf("backup_source_dataset_invalid: %w", err)
	}
	sourceDataset = parsedSource.String()

	props := []string{
		fmt.Sprintf("sylve:backup_job_id=%d", job.ID),
		fmt.Sprintf("sylve:backup_mode=%s", strings.TrimSpace(job.Mode)),
		fmt.Sprintf("sylve:backup_source=%s", sourceDataset),
	}
	props, err = canonicalZFSPropertyAssignments(props)
	if err != nil {
		return err
	}
	// A zfs set waits for a transaction group even when it changes nothing.
	current, err := s.runTargetSSH(
		ctx, target, "zfs", "get", "-H", "-p", "-s", "local", "-o", "property,value",
		"sylve:backup_job_id,sylve:backup_mode,sylve:backup_source", remoteDataset,
	)
	if err == nil {
		var have []string
		for _, line := range strings.Split(strings.TrimSpace(current), "\n") {
			if property, value, ok := strings.Cut(line, "\t"); ok {
				have = append(have, property+"="+value)
			}
		}
		if slices.Equal(have, props) {
			return nil
		}
	}

	argv := append([]string{"zfs", "set"}, props...)
	argv = append(argv, remoteDataset)
	output, err := s.runTargetSSH(ctx, target, argv...)
	if err != nil {
		return fmt.Errorf("sync_target_metadata_failed: %w (output: %s)", err, strings.TrimSpace(output))
	}
	return nil
}

func (s *Service) Run(ctx context.Context) {
	<-ctx.Done()
}

func (s *Service) StartBackupScheduler(ctx context.Context) {
	_ = s.withMutation(ctx, func(ctx context.Context) error {
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
		if err := s.ReconcileRestoreObservabilityAfterRestart(); err != nil {
			logger.L.Warn().Err(err).Msg("failed_to_reconcile_restore_observability")
		}
		return nil
	})

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
			_ = s.withMutation(ctx, func(ctx context.Context) error {
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
				if err := s.ReconcileRestoreObservabilityAfterRestart(); err != nil {
					logger.L.Warn().Err(err).Msg("periodic_restore_observability_reconcile_failed")
				}
				return nil
			})
		}
	}
}

func (s *Service) runBackupSchedulerTick(ctx context.Context) error {
	admittedCtx, release, err := s.enterMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	ctx = admittedCtx
	if s.DB == nil {
		return nil
	}

	bypassRaft, err := s.runtimeStateBypassRaft()
	if err != nil {
		return err
	}
	if err := s.requireCurrentRuntimeVoter(s.localNodeID(), bypassRaft); err != nil {
		return err
	}
	if err := s.RepublishQueuedBackupJobOperations(ctx); err != nil {
		logger.L.Warn().Err(err).Msg("backup_operation_republish_failed")
	}
	if err := s.drainScheduledRunResultOutbox(); err != nil {
		logger.L.Debug().Err(err).Msg("scheduled_run_result_outbox_pending")
	}
	if !bypassRaft && (s.Cluster.Raft() == nil || s.Cluster.Raft().State() != raft.Leader) {
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
		if bypassRaft && !s.isLocalBackupJobRunner(&job, localNodeID) {
			continue
		}

		nextAt, err := nextRunTime(job.CronExpr, now)
		if err != nil {
			if job.NextRunAt == nil && job.LastStatus == "failed" && job.LastError == "invalid_cron_expr" {
				continue
			}
			decision := clusterModels.BackupJobScheduleDecision{
				JobID: job.ID, ExpectedScheduleRevision: job.ScheduleRevision,
				ExpectedNextRunAt: job.NextRunAt, DecidedAt: now,
				SetRuntime: true, LastStatus: "failed", LastError: "invalid_cron_expr",
			}
			if applyErr := s.Cluster.ApplyBackupJobScheduleDecision(decision); applyErr != nil {
				logger.L.Debug().Err(applyErr).Uint("job_id", job.ID).Msg("backup_invalid_cron_decision_rejected")
			}
			continue
		}

		if job.NextRunAt == nil {
			decision := clusterModels.BackupJobScheduleDecision{
				JobID: job.ID, ExpectedScheduleRevision: job.ScheduleRevision,
				ExpectedNextRunAt: nil, NextRunAt: &nextAt, DecidedAt: now,
			}
			if applyErr := s.Cluster.ApplyBackupJobScheduleDecision(decision); applyErr != nil {
				logger.L.Debug().Err(applyErr).Uint("job_id", job.ID).Msg("backup_initial_schedule_decision_rejected")
			}
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
			decision := clusterModels.BackupJobScheduleDecision{
				JobID: job.ID, ExpectedScheduleRevision: job.ScheduleRevision,
				ExpectedNextRunAt: job.NextRunAt, NextRunAt: &nextAt, DecidedAt: now,
			}
			if applyErr := s.Cluster.ApplyBackupJobScheduleDecision(decision); applyErr != nil {
				logger.L.Debug().Err(applyErr).Uint("job_id", job.ID).Msg("backup_catchup_skip_decision_rejected")
			}
			continue
		}

		var operationCount int64
		if countErr := s.DB.Model(&clusterModels.BackupJobOperation{}).
			Where("job_id = ?", job.ID).Count(&operationCount).Error; countErr != nil {
			return countErr
		}
		if operationCount != 0 {
			// A long run owns the one reservation. Advancing directly to the
			// first future occurrence coalesces missed intervals.
			decision := clusterModels.BackupJobScheduleDecision{
				JobID: job.ID, ExpectedScheduleRevision: job.ScheduleRevision,
				ExpectedNextRunAt: job.NextRunAt, NextRunAt: &nextAt, DecidedAt: now,
			}
			if applyErr := s.Cluster.ApplyBackupJobScheduleDecision(decision); applyErr != nil {
				logger.L.Debug().Err(applyErr).Uint("job_id", job.ID).Msg("backup_coalesce_decision_rejected")
			}
			continue
		}

		holderNodeID := strings.TrimSpace(job.RunnerNodeID)
		if holderNodeID == "" {
			holderNodeID = strings.TrimSpace(localNodeID)
		}
		if holderNodeID == "" {
			logger.L.Warn().Uint("job_id", job.ID).Msg("scheduled_backup_holder_unavailable")
			continue
		}

		publishAfter := now
		if maxBackupEnqueueJitter > 0 {
			jitter := time.Duration(rand.Int63n(int64(maxBackupEnqueueJitter)))
			publishAfter = publishAfter.Add(jitter)
		}
		occurrenceAt := job.NextRunAt.UTC()
		hostRevision, err := s.backupJobHostKeyRevision(ctx, job.ID)
		if err != nil {
			logger.L.Warn().Err(err).Uint("job_id", job.ID).Msg("backup_target_host_key_unavailable")
			continue
		}
		decision := clusterModels.BackupJobScheduleDecision{
			HostKeyRevision: hostRevision,
			JobID:           job.ID, ExpectedScheduleRevision: job.ScheduleRevision,
			ExpectedNextRunAt: job.NextRunAt, NextRunAt: &nextAt, DecidedAt: now,
			ClaimToken:   fmt.Sprintf("backup:%s:%s", holderNodeID, uuid.NewString()),
			HolderNodeID: holderNodeID, OccurrenceAt: &occurrenceAt, PublishAfter: &publishAfter,
		}
		if applyErr := s.Cluster.ApplyBackupJobScheduleDecision(decision); applyErr != nil {
			logger.L.Debug().Err(applyErr).Uint("job_id", job.ID).Msg("scheduled_backup_claim_rejected")
		}
	}

	return s.RepublishQueuedBackupJobOperations(ctx)
}

func (s *Service) EnqueueBackupJob(ctx context.Context, jobID uint) error {
	if jobID == 0 {
		return fmt.Errorf("invalid_job_id")
	}

	var job clusterModels.BackupJob
	if err := s.DB.Preload("Target").First(&job, jobID).Error; err != nil {
		return err
	}
	repairRequired, err := clusterModels.BackupJobRepairRequired(s.DB, jobID)
	if err != nil {
		return err
	}
	if repairRequired {
		return fmt.Errorf("backup_job_repair_required")
	}

	if !s.reserveJob(jobID) {
		return fmt.Errorf("backup_job_already_running")
	}
	handle, err := s.acquireDurableBackupJobOperation(
		ctx,
		jobID,
		clusterModels.BackupJobOperationBackup,
		"",
	)
	if err != nil {
		s.releaseReservedJob(jobID)
		if strings.Contains(strings.ToLower(err.Error()), "backup_job_running") {
			return fmt.Errorf("backup_job_already_running")
		}
		return err
	}
	if err := db.EnqueueJSON(ctx, backupJobQueueName, backupJobPayload{
		JobID: jobID, OperationToken: handle.Token, HolderNodeID: handle.HolderNodeID,
	}); err != nil {
		s.releaseReservedJob(jobID)
		logger.L.Warn().Err(err).Uint("job_id", jobID).Msg("backup_queue_publish_deferred_to_reconciler")
	}
	return nil
}

func (s *Service) runBackupJob(ctx context.Context, job *clusterModels.BackupJob) error {
	return s.runBackupJobWithToken(ctx, job, "")
}

func (s *Service) runBackupJobWithToken(
	ctx context.Context,
	job *clusterModels.BackupJob,
	operationToken string,
) error {
	admittedCtx, release, err := s.enterMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	ctx = admittedCtx
	if job == nil || job.ID == 0 {
		return fmt.Errorf("backup_job_required")
	}
	if !s.beginJob(job.ID) {
		return fmt.Errorf("backup_job_already_running")
	}
	defer s.releaseJob(job.ID)

	if strings.TrimSpace(operationToken) != "" {
		executable, err := s.backupRunTokenExecutable(job.ID, operationToken)
		if err != nil {
			return fmt.Errorf("%w: backup job %d: %v", errScheduledRunOperationRevalidation, job.ID, err)
		}
		if !executable {
			return fmt.Errorf("backup_job_already_running")
		}
	}

	var encryption backupEncryptionObservation
	runErr := s.runBackupJobCore(ctx, job, &encryption)
	if !isJobAlreadyRunningErr(runErr) {
		s.updateBackupJobResult(job, runErr, encryption.optional())
	}
	return runErr
}

func datasetDestSuffix(configuredSuffix, sourceDataset string) string {
	if configuredSuffix = normalizeDatasetPath(configuredSuffix); configuredSuffix != "" {
		return configuredSuffix
	}
	return autoDestSuffix(normalizeDatasetPath(sourceDataset))
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

	if tail := jobLineageTail(configuredSuffix); tail != "" {
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

func jobLineageTail(destSuffix string) string {
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

	if tail := jobLineageTail(configuredSuffix); tail != "" {
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

func (s *Service) resolveVMBackupSourceDatasets(ctx context.Context, vmRID uint, preferred string) ([]string, error) {
	if vmRID == 0 {
		return nil, fmt.Errorf("invalid_vm_rid")
	}

	preferred = normalizeDatasetPath(preferred)
	backupRoots := s.listEnabledBackupRoots()

	sources := make([]string, 0)
	seen := make(map[string]struct{})
	allowedPools := make(map[string]struct{})
	restrictToAllowedPools := false
	sourcePoolAllowed := func(dataset string) bool {
		if !restrictToAllowedPools {
			return true
		}
		pool := dataset
		if slash := strings.Index(pool, "/"); slash > 0 {
			pool = pool[:slash]
		}
		_, allowed := allowedPools[pool]
		return allowed
	}
	addSource := func(dataset string) {
		dataset = normalizeDatasetPath(dataset)
		if dataset == "" {
			return
		}
		if !sourcePoolAllowed(dataset) {
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
		restrictToAllowedPools = true
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

			allowedPools[pool] = struct{}{}
			addSource(fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, vmRID))
		}
	}

	localDatasets, err := s.listLocalFilesystemDatasets(ctx)
	if err != nil {
		if len(sources) > 0 {
			sort.Strings(sources)
			if preferred != "" && sourcePoolAllowed(preferred) && !datasetWithinAnyRoot(preferred, backupRoots) {
				if _, ok := seen[preferred]; !ok {
					sources = append([]string{preferred}, sources...)
				}
			}
			return sources, nil
		}
		if preferred == "" || !sourcePoolAllowed(preferred) || datasetWithinAnyRoot(preferred, backupRoots) {
			return nil, err
		}
		return []string{preferred}, nil
	}

	for _, dataset := range localDatasets {
		if isReplicationLineageDatasetPath(dataset) {
			continue
		}
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
		if !sourcePoolAllowed(preferred) {
			logger.L.Warn().
				Uint("rid", vmRID).
				Str("dataset", preferred).
				Msg("ignoring_vm_backup_source_outside_registered_storage_pools")
		} else if datasetWithinAnyRoot(preferred, backupRoots) {
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
	if len(sources) == 0 && preferred != "" && sourcePoolAllowed(preferred) && !datasetWithinAnyRoot(preferred, backupRoots) {
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

func (s *Service) stopVMIfPresentContext(ctx context.Context, rid uint) error {
	return s.stopVMIfPresentForTransitionContext(ctx, rid, "")
}

func (s *Service) stopVMIfPresentForTransitionContext(
	ctx context.Context,
	rid uint,
	transitionRunID string,
) error {
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

	transitionRunID = strings.TrimSpace(transitionRunID)
	var stopErr error
	if transitionRunID == "" {
		stopErr = s.vmActionContext(ctx, *vm, "stop")
	} else {
		stopErr = s.vmActionForReplicationContext(ctx, *vm, "stop", transitionRunID)
	}
	if stopErr != nil {
		lower := strings.ToLower(stopErr.Error())
		if strings.Contains(lower, "not running") || isVMDomainNotFoundError(stopErr) {
			return nil
		}
		return stopErr
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

		select {
		case <-ctx.Done():
			return fmt.Errorf("vm_failed_to_reach_shutoff_state: %w", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (s *Service) startVMIfPresentContext(ctx context.Context, rid uint) error {
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

	return s.vmActionContext(ctx, *vm, "start")
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

func (s *Service) updateBackupJobResult(job *clusterModels.BackupJob, runErr error, encrypted *bool) {
	if job == nil || job.ID == 0 {
		return
	}
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
	handled, completionErr := s.completeBackupJobOperation(
		job, status, lastError, now, next, encrypted,
	)
	if handled {
		s.logScheduledResultDeliveryFailure(clusterModels.ScheduledRunKindBackup, job.ID, completionErr)
		return
	}

	update := cluster.BackupJobRuntimeStateUpdate{
		Version:    cluster.BackupJobRuntimeStateVersion,
		JobID:      job.ID,
		LastRunAt:  &now,
		LastStatus: status,
		LastError:  lastError,
		NextRunAt:  next,
		Encrypted:  encrypted,
	}

	bypassRaft, authorityErr := s.runtimeStateBypassRaft()
	if authorityErr != nil {
		logger.L.Warn().Err(authorityErr).Uint("job_id", job.ID).Msg("backup_runtime_authority_unavailable")
		return
	}
	if !bypassRaft {
		logger.L.Warn().Uint("job_id", job.ID).Msg("unfenced_cluster_backup_result_ignored")
		return
	}
	if err := s.Cluster.UpdateBackupJobRuntimeState(update); err != nil {
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
					// Avoid false progress when the target already holds history.
					targetFitsRun := out.TotalBytes == nil || *out.TotalBytes == 0 || *movedBytes <= *out.TotalBytes
					if targetFitsRun && (out.MovedBytes == nil || *movedBytes > *out.MovedBytes) {
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
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}

	orderClause := "started_at DESC"
	if sortField != "" {
		dir := "ASC"
		if strings.EqualFold(sortDir, "desc") {
			dir = "DESC"
		}
		allowed := map[string]bool{
			"id": true, "source_dataset": true, "target_endpoint": true,
			"mode": true, "status": true, "started_at": true, "completed_at": true,
			"error": true,
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
	// Exact restore operations are reconciled against their durable outbox
	// state; age alone must not interrupt a saturated but valid queued restore.
	query := s.DB.Model(&clusterModels.BackupEvent{}).
		Where("status = ? AND updated_at < ? AND operation_id IS NULL", "running", cutoff)

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

func (s *Service) buildZeltaEnv(target *clusterModels.BackupTarget) ([]string, error) {
	args, err := s.buildSSHArgs(target)
	if err != nil {
		return nil, err
	}
	base := []string{"ssh"}
	for _, arg := range args {
		if arg != "-n" {
			base = append(base, arg)
		}
	}
	sshBase := remoteexec.ShellCommandString(base...)
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
	}, nil
}

func isJobAlreadyRunningErr(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, sentinel := range []string{
		"backup_job_already_running",
		"replication_policy_already_running",
		"replication_policy_transition_already_running",
	} {
		if strings.Contains(message, sentinel) {
			return true
		}
	}
	return false
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
	return s.Cluster.LocalNodeID()
}

func (s *Service) isLocalBackupJobRunner(job *clusterModels.BackupJob, localNodeID string) bool {
	if job == nil {
		return false
	}

	runner := strings.TrimSpace(job.RunnerNodeID)
	if runner == "" {
		if s.Cluster.Raft() != nil {
			return s.Cluster.Raft().State() == raft.Leader
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
