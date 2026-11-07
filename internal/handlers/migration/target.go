// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migrationHandlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/logger"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	migrationService "github.com/alchemillahq/sylve/internal/services/migration"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/alchemillahq/sylve/pkg/utils"
	goLibvirt "github.com/digitalocean/go-libvirt"
	"gorm.io/gorm"
)

func RegisterCalls(
	clusterService *cluster.Service,
	zeltaService *zelta.Service,
	libvirtService *libvirt.Service,
	jailService *jail.Service,
	systemService systemServiceInterfaces.SystemServiceInterface,
) {
	cluster.OnNode(clusterService, "migration-import-vm", targetMigrationImport(vmImportOperations(zeltaService, libvirtService)))
	cluster.OnNode(clusterService, "migration-import-jail", targetMigrationImport(jailImportOperations(zeltaService, jailService)))
	cluster.OnNode(clusterService, "migration-check-vm-target", checkVMTarget(libvirtService))
	cluster.OnNode(clusterService, "migration-check-jail-target", checkJailTarget(jailService))
	cluster.OnNode(clusterService, "migration-target-pools", targetPools(systemService))
	cluster.OnNode(clusterService, "migration-target-dataset", targetDataset(zeltaService))
	cluster.OnNode(clusterService, "migration-target-prepare", writeTargetDataset(zeltaService, zeltaService.PrepareMigrationReceive))
	cluster.OnNode(clusterService, "migration-target-cleanup", writeTargetDataset(zeltaService, zeltaService.CleanupMigrationSnapshots))
	cluster.OnNodeStream(clusterService, "migration-target-receive", receiveTargetDataset(zeltaService))
}

func targetDataset(
	zeltaService *zelta.Service,
) func(context.Context, string, migrationService.TargetDatasetRequest) (migrationService.TargetDatasetInfo, error) {
	return func(ctx context.Context, _ string, req migrationService.TargetDatasetRequest) (migrationService.TargetDatasetInfo, error) {
		var info migrationService.TargetDatasetInfo
		if !req.Valid() {
			return info, fmt.Errorf("migration_target_dataset_invalid: %s", req.Dataset)
		}
		var err error
		info.Exists, info.MigrationSnapshots, err = zeltaService.MigrationTargetDataset(ctx, req.Dataset)
		return info, err
	}
}

func writeTargetDataset(
	zeltaService *zelta.Service,
	write func(context.Context, string) error,
) func(context.Context, string, migrationService.TargetDatasetRequest) (struct{}, error) {
	return func(ctx context.Context, _ string, req migrationService.TargetDatasetRequest) (struct{}, error) {
		if err := authorizeTargetDatasetWrite(ctx, zeltaService, req); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, write(ctx, req.Dataset)
	}
}

func receiveTargetDataset(
	zeltaService *zelta.Service,
) func(context.Context, string, migrationService.TargetDatasetRequest, io.Reader) (struct{}, error) {
	return func(ctx context.Context, _ string, req migrationService.TargetDatasetRequest, stream io.Reader) (struct{}, error) {
		if err := authorizeTargetDatasetWrite(ctx, zeltaService, req); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, zeltaService.ReceiveMigrationStream(ctx, req.Dataset, stream)
	}
}

// A write to a target dataset is only allowed for a guest root of the guest
// being migrated, while its migration targets this node.
func authorizeTargetDatasetWrite(ctx context.Context, zeltaService *zelta.Service, req migrationService.TargetDatasetRequest) error {
	if !req.Valid() {
		return fmt.Errorf("migration_target_dataset_invalid: %s", req.Dataset)
	}
	return requireMigrationToThisNode(
		ctx, zeltaService.DB, zeltaService.Cluster.LocalNodeID(), req.GuestType, req.GuestID, req.OperationToken,
		clusterModels.ReplicationGuestOperationPreCutover, clusterModels.ReplicationGuestOperationCutover,
	)
}

// targetPools lists the pools this node lets guests live on, which are the
// only pools a migration may receive datasets into.
func targetPools(
	systemService systemServiceInterfaces.SystemServiceInterface,
) func(context.Context, string, struct{}) ([]string, error) {
	return func(ctx context.Context, _ string, _ struct{}) ([]string, error) {
		pools, err := systemService.GetUsablePools(ctx)
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(pools))
		for _, pool := range pools {
			if pool != nil {
				names = append(names, pool.Name)
			}
		}
		slices.Sort(names)
		return names, nil
	}
}

type targetMigrationImportRequest struct {
	GuestID            uint              `json:"guestId"`
	OperationToken     string            `json:"operationToken"`
	StartGuest         *bool             `json:"startGuest"`
	SourceDatasetRoots []string          `json:"sourceDatasetRoots"`
	TargetPools        map[string]string `json:"targetPools,omitempty"`
}

type targetMigrationImportReceipt struct {
	Message            string            `json:"message"`
	Warnings           []string          `json:"warnings"`
	GuestID            uint              `json:"guestId"`
	OperationToken     string            `json:"operationToken"`
	StartGuest         *bool             `json:"startGuest"`
	SourceDatasetRoots []string          `json:"sourceDatasetRoots"`
	TargetPools        map[string]string `json:"targetPools,omitempty"`
}

type targetMigrationRuntimeState string

const (
	targetMigrationRuntimeInactive targetMigrationRuntimeState = "inactive"
	targetMigrationRuntimeActive   targetMigrationRuntimeState = "active"
	targetMigrationRuntimeUnsafe   targetMigrationRuntimeState = "unsafe"
)

type targetMigrationImportOperations struct {
	GuestType              string
	UnavailableReason      string
	ImportedMessage        string
	ImportedStoppedMessage string
	AlreadyActiveMessage   string
	Authorize              func(context.Context, uint, string) error
	ValidateRoots          func(context.Context, uint, []string) ([]string, error)
	// MergeRoots moves datasets received into staging beside a guest root into
	// that root. Only VMs can span pools, so jails leave it nil.
	MergeRoots         func(context.Context, uint, map[string][]string) error
	RuntimeState       func(uint) (targetMigrationRuntimeState, error)
	Import             func(context.Context, uint, []string, map[string]string) ([]string, error)
	SetIntentionalStop func(uint, bool) error
	Start              func(context.Context, uint) error
	CleanupSnapshots   func(context.Context, []string) error
}

type targetMigrationGuestLockEntry struct {
	mu   sync.Mutex
	refs uint
}

type targetMigrationGuestLockSet struct {
	mu    sync.Mutex
	locks map[uint]*targetMigrationGuestLockEntry
}

func (s *targetMigrationGuestLockSet) acquire(guestID uint) func() {
	s.mu.Lock()
	if s.locks == nil {
		s.locks = make(map[uint]*targetMigrationGuestLockEntry)
	}
	entry := s.locks[guestID]
	if entry == nil {
		entry = &targetMigrationGuestLockEntry{}
		s.locks[guestID] = entry
	}
	entry.refs++
	s.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		s.mu.Lock()
		entry.refs--
		if entry.refs == 0 && s.locks[guestID] == entry {
			delete(s.locks, guestID)
		}
		s.mu.Unlock()
	}
}

func requireExactMigrationTargetCutover(
	ctx context.Context,
	db *gorm.DB,
	localNodeID string,
	guestType string,
	guestID uint,
	operationToken string,
) error {
	return requireMigrationToThisNode(ctx, db, localNodeID, guestType, guestID, operationToken,
		clusterModels.ReplicationGuestOperationCutover)
}

// requireMigrationToThisNode checks that the guest's migration with this token
// targets this node and is in one of states.
func requireMigrationToThisNode(
	ctx context.Context,
	db *gorm.DB,
	localNodeID string,
	guestType string,
	guestID uint,
	operationToken string,
	states ...string,
) error {
	localNodeID = strings.TrimSpace(localNodeID)
	guestType = strings.ToLower(strings.TrimSpace(guestType))
	operationToken = strings.TrimSpace(operationToken)
	if db == nil {
		return fmt.Errorf("migration_target_guard_database_unavailable")
	}
	if localNodeID == "" {
		return fmt.Errorf("migration_target_node_id_unavailable")
	}
	if guestID == 0 || operationToken == "" ||
		!clusterModels.ValidReplicationGuestType(guestType) {
		return fmt.Errorf("migration_target_guard_input_invalid")
	}

	var operation clusterModels.ReplicationGuestOperation
	result := db.WithContext(ctx).
		Where("guest_type = ? AND guest_id = ?", guestType, guestID).
		Limit(1).
		Find(&operation)
	if result.Error != nil {
		return fmt.Errorf("migration_target_guard_lookup_failed: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("migration_target_cutover_guard_not_applied")
	}
	if operation.Operation != clusterModels.ReplicationGuestOperationMigration ||
		!slices.Contains(states, operation.State) ||
		strings.TrimSpace(operation.Token) != operationToken ||
		strings.TrimSpace(operation.TargetNodeID) != localNodeID ||
		strings.TrimSpace(operation.OwnerNodeID) == "" || operation.TaskID == 0 {
		return fmt.Errorf("migration_target_cutover_guard_mismatch")
	}
	return nil
}

func withWarnings(err error, warnings []string) error {
	if len(warnings) == 0 {
		return err
	}
	return fmt.Errorf("%w (warnings: %s)", err, strings.Join(warnings, "; "))
}

func targetMigrationImport(
	ops targetMigrationImportOperations,
) func(context.Context, string, targetMigrationImportRequest) (targetMigrationImportReceipt, error) {
	var guestLocks targetMigrationGuestLockSet

	return func(ctx context.Context, _ string, req targetMigrationImportRequest) (targetMigrationImportReceipt, error) {
		var none targetMigrationImportReceipt
		req.OperationToken = strings.TrimSpace(req.OperationToken)
		if req.GuestID == 0 || req.OperationToken == "" || req.StartGuest == nil || len(req.SourceDatasetRoots) == 0 {
			return none, fmt.Errorf("guest_id_operation_token_start_state_and_dataset_roots_required")
		}
		if ops.UnavailableReason != "" || ops.Authorize == nil || ops.ValidateRoots == nil ||
			ops.RuntimeState == nil || ops.Import == nil || ops.SetIntentionalStop == nil || ops.Start == nil ||
			ops.CleanupSnapshots == nil {
			reason := strings.TrimSpace(ops.UnavailableReason)
			if reason == "" {
				reason = "migration_target_import_not_configured"
			}
			return none, errors.New(reason)
		}

		releaseGuestLock := guestLocks.acquire(req.GuestID)
		defer releaseGuestLock()

		if err := ops.Authorize(ctx, req.GuestID, req.OperationToken); err != nil {
			return none, fmt.Errorf("migration_target_cutover_guard_rejected: %w", err)
		}
		var roots []string
		staged := make(map[string][]string)
		for _, target := range migrationService.TargetDatasetRoots(req.SourceDatasetRoots, req.TargetPools) {
			if target.Receive == target.Root {
				roots = append(roots, target.Root)
			} else {
				staged[target.Root] = append(staged[target.Root], target.Receive)
			}
		}
		if len(staged) > 0 {
			if ops.MergeRoots == nil {
				return none, fmt.Errorf("migration_target_merge_not_supported")
			}
			if err := ops.MergeRoots(ctx, req.GuestID, staged); err != nil {
				return none, fmt.Errorf("migration_target_merge_failed: %w", err)
			}
		}
		roots, err := ops.ValidateRoots(ctx, req.GuestID, roots)
		if err != nil {
			return none, fmt.Errorf("migration_target_dataset_manifest_rejected: %w", err)
		}
		// Every receipt answers a guest in its final state, so the migration
		// snapshots go here instead of in the import: each destroy waits for a
		// txg sync, which shouldn't add to the guest's downtime.
		receipt := func(message string, warnings []string) targetMigrationImportReceipt {
			if err := ops.CleanupSnapshots(ctx, roots); err != nil {
				logger.L.Warn().Err(err).
					Str("guest_type", ops.GuestType).
					Uint("guest_id", req.GuestID).
					Msg("migration_target_snapshot_cleanup_failed")
				warnings = append(warnings, "warning_migration_snapshot_cleanup_failed: "+err.Error())
			}
			if warnings == nil {
				warnings = []string{}
			}
			return targetMigrationImportReceipt{
				Message: message, Warnings: warnings, GuestID: req.GuestID, OperationToken: req.OperationToken,
				StartGuest: req.StartGuest, SourceDatasetRoots: append([]string(nil), req.SourceDatasetRoots...),
				TargetPools: req.TargetPools,
			}
		}

		runtimeState, err := ops.RuntimeState(req.GuestID)
		if err != nil {
			return none, fmt.Errorf("migration_target_active_check_failed: %w", err)
		}
		if runtimeState == targetMigrationRuntimeUnsafe ||
			runtimeState == targetMigrationRuntimeActive && !*req.StartGuest {
			return none, fmt.Errorf("migration_target_runtime_state_not_safe_for_import")
		}
		if runtimeState == targetMigrationRuntimeActive {
			return receipt(ops.AlreadyActiveMessage, nil), nil
		}

		warnings, err := ops.Import(ctx, req.GuestID, roots, req.TargetPools)
		if err != nil {
			logger.L.Warn().Err(err).
				Str("guest_type", ops.GuestType).
				Uint("guest_id", req.GuestID).
				Msg("intra_cluster_guest_import_failed")
			return none, withWarnings(err, warnings)
		}

		// Raft state can advance during the import, so re-read the guard right
		// before starting: a successful import isn't authority to start later.
		if err := ops.Authorize(ctx, req.GuestID, req.OperationToken); err != nil {
			return none, withWarnings(fmt.Errorf("migration_target_cutover_guard_rejected: %w", err), warnings)
		}
		if err := ops.SetIntentionalStop(req.GuestID, !*req.StartGuest); err != nil {
			return none, withWarnings(fmt.Errorf("migration_target_runtime_intent_update_failed: %w", err), warnings)
		}
		if !*req.StartGuest {
			stateAfterImport, stateErr := ops.RuntimeState(req.GuestID)
			if stateErr != nil || stateAfterImport != targetMigrationRuntimeInactive {
				err := errors.New("migration_target_stopped_state_unverified")
				if stateErr != nil {
					err = fmt.Errorf("%w: %w", err, stateErr)
				}
				return none, withWarnings(err, warnings)
			}
			return receipt(ops.ImportedStoppedMessage, warnings), nil
		}
		if err := ops.Authorize(ctx, req.GuestID, req.OperationToken); err != nil {
			return none, withWarnings(fmt.Errorf("migration_target_cutover_guard_rejected: %w", err), warnings)
		}

		if err := ops.Start(ctx, req.GuestID); err != nil {
			// Starting and answering aren't atomic: if the guest is active under
			// the same cutover guard, a retry is complete, not a second import.
			stateAfterError, activeErr := ops.RuntimeState(req.GuestID)
			if activeErr == nil && stateAfterError == targetMigrationRuntimeActive &&
				ops.Authorize(ctx, req.GuestID, req.OperationToken) == nil {
				return receipt(ops.AlreadyActiveMessage, warnings), nil
			}
			logger.L.Warn().Err(err).
				Str("guest_type", ops.GuestType).
				Uint("guest_id", req.GuestID).
				Msg("intra_cluster_guest_start_failed")
			if activeErr != nil {
				err = fmt.Errorf("%w; active_recheck_failed: %w", err, activeErr)
			}
			return none, withWarnings(err, warnings)
		}

		stateAfterStart, stateErr := ops.RuntimeState(req.GuestID)
		if stateErr != nil || stateAfterStart != targetMigrationRuntimeActive {
			err := errors.New("migration_target_start_unverified")
			if stateErr != nil {
				err = fmt.Errorf("%w: %w", err, stateErr)
			}
			return none, withWarnings(err, warnings)
		}
		return receipt(ops.ImportedMessage, warnings), nil
	}
}

func migratedVMRuntimeState(libvirtService *libvirt.Service, guestID uint) (targetMigrationRuntimeState, error) {
	state, err := libvirtService.GetDomainState(int(guestID))
	if err != nil {
		if libvirtServiceInterfaces.IsDomainNotFoundError(err) {
			return targetMigrationRuntimeInactive, nil
		}
		return targetMigrationRuntimeUnsafe, err
	}
	return classifyMigratedVMRuntimeState(state), nil
}

func classifyMigratedVMRuntimeState(state goLibvirt.DomainState) targetMigrationRuntimeState {
	switch state {
	case goLibvirt.DomainRunning:
		return targetMigrationRuntimeActive
	case goLibvirt.DomainShutoff:
		return targetMigrationRuntimeInactive
	default:
		return targetMigrationRuntimeUnsafe
	}
}

func vmImportOperations(
	zeltaService *zelta.Service,
	libvirtService *libvirt.Service,
) targetMigrationImportOperations {
	ops := targetMigrationImportOperations{
		GuestType:              clusterModels.ReplicationGuestTypeVM,
		ImportedMessage:        "vm_imported_and_started",
		ImportedStoppedMessage: "vm_imported_and_left_stopped",
		AlreadyActiveMessage:   "vm_already_imported_and_active",
	}
	if zeltaService == nil {
		ops.UnavailableReason = "zelta_not_configured"
	} else if zeltaService.Cluster == nil {
		ops.UnavailableReason = "cluster_not_configured"
	} else {
		ops.Authorize = func(ctx context.Context, guestID uint, operationToken string) error {
			return requireExactMigrationTargetCutover(
				ctx,
				zeltaService.DB,
				zeltaService.Cluster.LocalNodeID(),
				clusterModels.ReplicationGuestTypeVM,
				guestID,
				operationToken,
			)
		}
		ops.ValidateRoots = zeltaService.ValidateMigratedVMRoots
		ops.MergeRoots = zeltaService.MergeMigratedVMRoots
		ops.Import = zeltaService.ImportMigratedVMWithRoots
		ops.CleanupSnapshots = zeltaService.CleanupMigratedSnapshots
	}
	if libvirtService == nil {
		if ops.UnavailableReason == "" {
			ops.UnavailableReason = "libvirt_not_configured"
		}
	} else {
		ops.RuntimeState = func(guestID uint) (targetMigrationRuntimeState, error) {
			return migratedVMRuntimeState(libvirtService, guestID)
		}
		ops.SetIntentionalStop = func(guestID uint, stopped bool) error {
			result := libvirtService.DB.Model(&vmModels.VM{}).
				Where("rid = ?", guestID).
				Update("intentionally_stopped", stopped)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("migrated_vm_record_not_found")
			}
			return libvirtService.WriteVMJson(guestID)
		}
		ops.Start = func(ctx context.Context, guestID uint) error {
			return libvirtService.PerformActionContext(ctx, guestID, "start")
		}
	}
	return ops
}

func jailImportOperations(
	zeltaService *zelta.Service,
	jailService *jail.Service,
) targetMigrationImportOperations {
	ops := targetMigrationImportOperations{
		GuestType:              clusterModels.ReplicationGuestTypeJail,
		ImportedMessage:        "jail_imported_and_started",
		ImportedStoppedMessage: "jail_imported_and_left_stopped",
		AlreadyActiveMessage:   "jail_already_imported_and_active",
	}
	if zeltaService == nil {
		ops.UnavailableReason = "zelta_not_configured"
	} else if zeltaService.Cluster == nil {
		ops.UnavailableReason = "cluster_not_configured"
	} else {
		ops.Authorize = func(ctx context.Context, guestID uint, operationToken string) error {
			return requireExactMigrationTargetCutover(
				ctx,
				zeltaService.DB,
				zeltaService.Cluster.LocalNodeID(),
				clusterModels.ReplicationGuestTypeJail,
				guestID,
				operationToken,
			)
		}
		ops.ValidateRoots = zeltaService.ValidateMigratedJailRoots
		ops.Import = func(ctx context.Context, ctID uint, roots []string, _ map[string]string) ([]string, error) {
			return zeltaService.ImportMigratedJailWithRoots(ctx, ctID, roots)
		}
		ops.CleanupSnapshots = zeltaService.CleanupMigratedSnapshots
	}
	if jailService == nil {
		if ops.UnavailableReason == "" {
			ops.UnavailableReason = "jail_not_configured"
		}
	} else {
		ops.RuntimeState = func(guestID uint) (targetMigrationRuntimeState, error) {
			active, err := jailService.IsJailRunning(guestID)
			if err != nil {
				return targetMigrationRuntimeUnsafe, err
			}
			if active {
				return targetMigrationRuntimeActive, nil
			}
			return targetMigrationRuntimeInactive, nil
		}
		ops.SetIntentionalStop = func(guestID uint, stopped bool) error {
			result := jailService.DB.Model(&jailModels.Jail{}).
				Where("ct_id = ?", guestID).
				Update("intentionally_stopped", stopped)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("migrated_jail_record_not_found")
			}
			return jailService.WriteJailJSON(guestID)
		}
		ops.Start = func(ctx context.Context, guestID uint) error {
			return jailService.JailActionContext(ctx, int(guestID), "start")
		}
	}
	return ops
}

type CheckVMTargetRequest struct {
	BootRequirement *libvirtServiceInterfaces.VMBootRequirement `json:"bootRequirement,omitempty"`
	RID             uint                                        `json:"rid"`
	MediaUUIDs      []string                                    `json:"mediaUuids"`
	VNCPort         int                                         `json:"vncPort"`
	Switches        []networkAttachment.NamedContract           `json:"switches"`
	FsDatasets      []string                                    `json:"fsDatasets"`
}

type CheckVMTargetResult struct {
	networkAttachment.TargetResult
	MissingMedia           []string `json:"missingMedia"`
	VNCPortInUse           bool     `json:"vncPortInUse"`
	MissingFsDatasets      []string `json:"missingFsDatasets"`
	BootErrors             []string `json:"bootErrors"`
	VirtualizationDisabled bool     `json:"virtualizationDisabled"`
}

var validateTargetNetworkAttachments = func(
	db *gorm.DB,
	attachments []networkAttachment.NamedContract,
	expectedKind networkAttachment.Kind,
) networkAttachment.TargetResult {
	return networkAttachment.NewResolver(db).ValidateEffectiveTargets(expectedKind, attachments)
}

func checkVMTarget(
	libvirtService *libvirt.Service,
) func(context.Context, string, CheckVMTargetRequest) (CheckVMTargetResult, error) {
	return func(ctx context.Context, _ string, req CheckVMTargetRequest) (CheckVMTargetResult, error) {
		if libvirtService == nil {
			return CheckVMTargetResult{}, fmt.Errorf("libvirt_not_configured")
		}
		db := libvirtService.DB
		if len(req.Switches) > 0 && db == nil {
			return CheckVMTargetResult{}, fmt.Errorf("libvirt_database_not_configured")
		}

		result := CheckVMTargetResult{
			TargetResult: networkAttachment.TargetResult{
				MissingSwitches:      []string{},
				IncompatibleSwitches: []string{},
			},
			MissingMedia:           make([]string, 0, len(req.MediaUUIDs)),
			MissingFsDatasets:      make([]string, 0, len(req.FsDatasets)),
			BootErrors:             []string{},
			VirtualizationDisabled: !libvirtService.IsVirtualizationEnabled(),
		}
		seenMedia := make(map[string]struct{}, len(req.MediaUUIDs))
		for _, uuid := range req.MediaUUIDs {
			uuid = strings.TrimSpace(uuid)
			if uuid == "" {
				continue
			}
			if _, ok := seenMedia[uuid]; ok {
				continue
			}
			seenMedia[uuid] = struct{}{}

			if _, err := libvirtService.FindISOByUUID(uuid, true); err != nil {
				result.MissingMedia = append(result.MissingMedia, uuid)
			}
		}

		if db != nil {
			result.TargetResult = validateTargetNetworkAttachments(db, req.Switches, networkAttachment.KindVM)
		}

		if libvirtService.GZFS != nil && libvirtService.GZFS.ZFS != nil {
			for _, ds := range req.FsDatasets {
				ds = strings.TrimSpace(ds)
				if ds == "" {
					continue
				}
				datasets, err := libvirtService.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeFilesystem, false, ds)
				if err != nil || len(datasets) == 0 {
					result.MissingFsDatasets = append(result.MissingFsDatasets, ds)
				}
			}
		}

		if db != nil && req.VNCPort > 0 {
			var count int64
			if err := db.Model(&vmModels.VM{}).
				Where("vnc_port = ? AND rid <> ?", req.VNCPort, req.RID).
				Count(&count).Error; err == nil && count > 0 {
				result.VNCPortInUse = true
			}
			if utils.IsTCPPortInUse(req.VNCPort) {
				result.VNCPortInUse = true
			}
		}

		if err := libvirt.CheckVMBootRequirement(req.BootRequirement); err != nil {
			result.BootErrors = append(result.BootErrors, err.Error())
		}
		return result, nil
	}
}

type CheckJailTargetRequest struct {
	Networks []networkAttachment.NamedContract `json:"networks"`
}

func checkJailTarget(
	jailService *jail.Service,
) func(context.Context, string, CheckJailTargetRequest) (networkAttachment.TargetResult, error) {
	return func(_ context.Context, _ string, req CheckJailTargetRequest) (networkAttachment.TargetResult, error) {
		if jailService == nil || jailService.DB == nil {
			return networkAttachment.TargetResult{}, fmt.Errorf("jail_not_configured")
		}
		return validateTargetNetworkAttachments(jailService.DB, req.Networks, networkAttachment.KindJail), nil
	}
}
