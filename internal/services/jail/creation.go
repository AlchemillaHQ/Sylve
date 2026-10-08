// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const creationOwnerProperty = "sylve:create-operation"

type creationDataset struct {
	Name         string `json:"name"`
	GUID         string `json:"guid"`
	Snapshot     string `json:"snapshot,omitempty"`
	SnapshotGUID string `json:"snapshotGuid,omitempty"`
}

type creationState struct {
	Reservation   *clusterServiceInterfaces.GuestIdentityReservation `json:"reservation,omitempty"`
	LocalToken    string                                             `json:"localToken,omitempty"`
	Datasets      []creationDataset                                  `json:"datasets,omitempty"`
	Snapshots     []creationDataset                                  `json:"snapshots,omitempty"`
	ConfigDir     string                                             `json:"configDir,omitempty"`
	JailID        uint                                               `json:"jailId,omitempty"`
	AutoObjectIDs []uint                                             `json:"autoObjectIds,omitempty"`
	DevFSRules    bool                                               `json:"devfsRules,omitempty"`
	StagingDir    string                                             `json:"stagingDir,omitempty"`
}

type creationCompletionPendingError struct{ cause error }

func (e *creationCompletionPendingError) Error() string {
	return "jail_creation_completion_pending: " + e.cause.Error()
}
func (e *creationCompletionPendingError) Unwrap() error               { return e.cause }
func (e *creationCompletionPendingError) LifecycleRetryPending() bool { return true }

func (s *Service) requireCreationAvailable(ctx context.Context, ctID uint, except string) error {
	if !s.DB.Migrator().HasTable(&jailModels.JailCreation{}) {
		return nil
	}
	var count int64
	if err := s.DB.WithContext(ctx).Model(&jailModels.JailCreation{}).
		Where("id <> ? AND active_ct_id = ?", except, ctID).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("jail_creation_in_progress")
	}
	return nil
}

func (s *Service) CreateJail(ctx context.Context, req jailServiceInterfaces.CreateJailRequest) error {
	s.createMutex.Lock()
	defer s.createMutex.Unlock()
	op, state, err := s.prepareCreation(ctx, nil, req)
	if err != nil {
		return err
	}
	return s.runCreation(ctx, op, state, req)
}

func (s *Service) PrepareCreateJail(ctx context.Context, taskID uint, req jailServiceInterfaces.CreateJailRequest) error {
	_, _, err := s.prepareCreation(ctx, &taskID, req)
	return err
}

func (s *Service) prepareCreation(ctx context.Context, taskID *uint, req jailServiceInterfaces.CreateJailRequest) (*jailModels.JailCreation, *creationState, error) {
	s.creationAdmissionMutex.Lock()
	defer s.creationAdmissionMutex.Unlock()
	if err := s.ValidateCreate(ctx, req); err != nil {
		return nil, nil, err
	}
	request, err := json.Marshal(req)
	if err != nil {
		return nil, nil, err
	}
	op := &jailModels.JailCreation{
		ID: uuid.NewString(), TaskID: taskID, CTID: *req.CTID, ActiveCTID: req.CTID,
		Request: string(request), State: "{}", Phase: "preparing",
	}
	state := &creationState{}
	if s.guestIdentityCoordinator != nil {
		coordinator, ok := s.guestIdentityCoordinator.(clusterServiceInterfaces.GuestIdentityCreationCoordinator)
		if !ok {
			return nil, nil, fmt.Errorf("guest_identity_durable_reservation_unavailable")
		}
		reservation, err := coordinator.PrepareGuestIdentityReservation(ctx, clusterModels.ReplicationGuestTypeJail, []uint{op.CTID}, op.ID)
		if err != nil {
			return nil, nil, err
		}
		state.Reservation = &reservation
		state.LocalToken = reservation.LocalOperationToken
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return nil, nil, err
	}
	op.State = string(encoded)
	if err := s.DB.WithContext(ctx).Create(op).Error; err != nil {
		return nil, nil, fmt.Errorf("failed_to_prepare_jail_creation: %w", err)
	}
	if state.Reservation != nil {
		coordinator := s.guestIdentityCoordinator.(clusterServiceInterfaces.GuestIdentityCreationCoordinator)
		if err := coordinator.AcquireGuestIdentityReservation(ctx, *state.Reservation); err != nil {
			if errors.Is(err, clusterModels.ErrGuestIdentityAlreadyInUse) {
				// Admission lost a race before acquiring this token. Do not try
				// to release the other creator's local reservation.
				state.Reservation = nil
			}
			return nil, nil, errors.Join(err, s.failCreation(ctx, op, state, err))
		}
	}
	if err := s.saveCreation(s.DB.WithContext(ctx), op, state, "queued"); err != nil {
		return nil, nil, errors.Join(err, s.failCreation(ctx, op, state, err))
	}
	return op, state, nil
}

func (s *Service) loadCreation(taskID uint) (*jailModels.JailCreation, *creationState, jailServiceInterfaces.CreateJailRequest, error) {
	var op jailModels.JailCreation
	var state creationState
	var req jailServiceInterfaces.CreateJailRequest
	if err := s.DB.Where("task_id = ?", taskID).First(&op).Error; err != nil {
		return nil, nil, req, err
	}
	if err := json.Unmarshal([]byte(op.State), &state); err != nil {
		return nil, nil, req, err
	}
	if state.Reservation != nil {
		state.Reservation.LocalOperationToken = state.LocalToken
	}
	if err := json.Unmarshal([]byte(op.Request), &req); err != nil {
		return nil, nil, req, err
	}
	return &op, &state, req, nil
}

func (s *Service) ExecuteCreateJail(ctx context.Context, taskID uint) error {
	s.createMutex.Lock()
	defer s.createMutex.Unlock()
	op, state, req, err := s.loadCreation(taskID)
	if err != nil {
		return err
	}
	if op.Phase == "committed" {
		return nil
	}
	if op.Phase == "ready" {
		if err := s.commitCreation(ctx, op, state); err != nil {
			return &creationCompletionPendingError{cause: err}
		}
		return nil
	}
	if op.Phase != "queued" {
		return fmt.Errorf("jail_creation_not_queued: %s", op.Phase)
	}
	return s.runCreation(ctx, op, state, req)
}

func (s *Service) CancelCreateJail(ctx context.Context, taskID uint, cause error) error {
	s.createMutex.Lock()
	defer s.createMutex.Unlock()
	op, state, _, err := s.loadCreation(taskID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.failCreation(ctx, op, state, cause)
}

func (s *Service) saveCreation(db *gorm.DB, op *jailModels.JailCreation, state *creationState, phase string) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := db.Model(op).Updates(map[string]any{"state": string(encoded), "phase": phase}).Error; err != nil {
		return fmt.Errorf("failed_to_journal_jail_creation: %w", err)
	}
	op.State, op.Phase = string(encoded), phase
	if op.TaskID != nil {
		// Progress is best effort; the ownership journal is not.
		db.Model(&taskModels.GuestLifecycleTask{}).Where("id = ? AND status IN ?", *op.TaskID, []string{"queued", "running"}).Update("message", phase)
	}
	return nil
}

func (s *Service) runCreation(ctx context.Context, op *jailModels.JailCreation, state *creationState, req jailServiceInterfaces.CreateJailRequest) (err error) {
	defer func() {
		if err != nil && op.Phase != "ready" && op.Phase != "committed" {
			err = errors.Join(err, s.failCreation(ctx, op, state, err))
		}
	}()
	if state.Reservation != nil {
		if err := s.guestIdentityCoordinator.ValidateGuestIdentityClaim(ctx, *state.Reservation); err != nil {
			return err
		}
	}
	if err := s.saveCreation(s.DB.WithContext(ctx), op, state, "preparing"); err != nil {
		return err
	}
	if err := s.createJail(ctx, req, op, state); err != nil {
		return err
	}
	// Configuration and metadata are complete. A crash from here must finish
	// the operation, not roll back a usable jail whose task wasn't published.
	if err := s.saveCreation(s.DB.WithContext(ctx), op, state, "ready"); err != nil {
		return err
	}
	completionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := s.commitCreation(completionCtx, op, state); err != nil {
		return &creationCompletionPendingError{cause: err}
	}
	return nil
}

func (s *Service) commitCreation(ctx context.Context, op *jailModels.JailCreation, state *creationState) error {
	if err := s.verifyReadyCreation(ctx, op, state); err != nil {
		return err
	}
	if state.Reservation != nil {
		coordinator := s.guestIdentityCoordinator.(clusterServiceInterfaces.GuestIdentityCreationCoordinator)
		if err := coordinator.RestoreGuestIdentityReservation(ctx, *state.Reservation); err != nil {
			return err
		}
		if err := s.guestIdentityCoordinator.ValidateGuestIdentityClaim(ctx, *state.Reservation); err != nil {
			return err
		}
		if err := s.guestIdentityCoordinator.FinalizeGuestIdentities(ctx, *state.Reservation); err != nil {
			return err
		}
	}
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&jailModels.Jail{}).Where("id = ? AND ct_id = ?", state.JailID, op.CTID).Update("creation_pending", false)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("jail_creation_registration_missing")
		}
		if err := tx.Model(op).Updates(map[string]any{"phase": "committed", "active_ct_id": nil, "error": ""}).Error; err != nil {
			return err
		}
		if op.TaskID != nil {
			if err := tx.Model(&taskModels.GuestLifecycleTask{}).Where("id = ?", *op.TaskID).
				Updates(map[string]any{"status": taskModels.LifecycleTaskStatusSuccess, "message": "completed", "error": "", "finished_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		op.Phase = "committed"
		s.emitLeftPanelRefresh(fmt.Sprintf("jail_create_%d", op.CTID))
	}
	return err
}

func (s *Service) verifyReadyCreation(ctx context.Context, op *jailModels.JailCreation, state *creationState) error {
	var req jailServiceInterfaces.CreateJailRequest
	if err := json.Unmarshal([]byte(op.Request), &req); err != nil {
		return err
	}
	root := fmt.Sprintf("%s/sylve/jails/%d", req.Pool, op.CTID)
	known := map[string]creationDataset{}
	for _, resource := range state.Datasets {
		if resource.Name == root || strings.HasPrefix(resource.Name, root+"/") {
			if resource.GUID == "" {
				return fmt.Errorf("jail_creation_ready_identity_missing")
			}
			ds, err := s.ownedCreationDataset(ctx, op.ID, resource)
			if err != nil {
				return err
			}
			if ds == nil {
				return fmt.Errorf("jail_creation_ready_dataset_missing: %s", resource.Name)
			}
			known[resource.Name] = resource
		}
	}
	if _, ok := known[root]; !ok {
		return fmt.Errorf("jail_creation_ready_identity_missing")
	}
	datasets, err := s.GZFS.ZFS.List(ctx, true, root)
	if err != nil {
		return err
	}
	for _, dataset := range datasets {
		if dataset.Type == gzfs.DatasetTypeSnapshot {
			continue
		}
		if _, owned := known[dataset.Name]; !owned {
			return fmt.Errorf("jail_creation_ready_unknown_dataset: %s", dataset.Name)
		}
		if origin := dataset.Properties["origin"].Value; origin != "" && origin != "-" {
			return fmt.Errorf("jail_copy_has_clone_dependency")
		}
	}
	var storageCount int64
	if err := s.DB.WithContext(ctx).Model(&jailModels.Storage{}).Where("jid = ? AND is_base = ? AND guid = ?", state.JailID, true, known[root].GUID).Count(&storageCount).Error; err != nil {
		return err
	}
	if storageCount != 1 {
		return fmt.Errorf("jail_creation_registration_changed")
	}
	configInfo, err := os.Lstat(filepath.Join(state.ConfigDir, fmt.Sprintf("%d.conf", op.CTID)))
	if err != nil || !configInfo.Mode().IsRegular() || configInfo.Size() == 0 {
		return fmt.Errorf("jail_creation_ready_config_missing")
	}
	marker, err := os.ReadFile(filepath.Join(state.ConfigDir, ".creation-operation"))
	if err != nil || string(marker) != op.ID {
		return fmt.Errorf("jail_creation_config_ownership_unverified")
	}
	return nil
}

func (s *Service) ownedCreationDataset(ctx context.Context, opID string, resource creationDataset) (*gzfs.Dataset, error) {
	ds, err := s.GZFS.ZFS.Get(ctx, resource.Name, false)
	if isZFSDatasetMissingError(err) || err == nil && ds == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	owner, err := s.GZFS.ZFS.GetProperty(ctx, ds.Name, creationOwnerProperty)
	if err != nil {
		return nil, err
	}
	if (resource.GUID != "" && ds.GUID != resource.GUID) || owner.Value != opID || !strings.EqualFold(owner.Source.Type, "local") {
		return nil, fmt.Errorf("jail_creation_cleanup_ownership_unverified: %s", resource.Name)
	}
	return ds, nil
}

func (s *Service) failCreation(ctx context.Context, op *jailModels.JailCreation, state *creationState, cause error) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	var durable jailModels.JailCreation
	if err := s.DB.WithContext(cleanupCtx).Select("phase").First(&durable, "id = ?", op.ID).Error; err != nil {
		return fmt.Errorf("jail_creation_cleanup_state_unverified: %w", err)
	}
	if durable.Phase == "committed" || durable.Phase == "ready" {
		op.Phase = durable.Phase
		return &creationCompletionPendingError{cause: fmt.Errorf("durable_result_requires_finalization: %s", op.ID)}
	}
	var cleanupErr error
	for i := len(state.Datasets) - 1; i >= 0; i-- {
		resource := state.Datasets[i]
		ds, err := s.ownedCreationDataset(cleanupCtx, op.ID, resource)
		if err == nil && ds != nil {
			children, listErr := s.GZFS.ZFS.List(cleanupCtx, true, ds.Name)
			if listErr != nil {
				err = listErr
			} else {
				for _, child := range children {
					if child.Name != ds.Name && child.Type != gzfs.DatasetTypeSnapshot {
						err = fmt.Errorf("jail_creation_cleanup_unknown_child: %s", child.Name)
						break
					}
				}
				if err == nil {
					err = s.removeCreationReceivedSnapshot(cleanupCtx, ds, resource)
				}
				if err == nil {
					err = ds.Destroy(cleanupCtx, false, false)
				}
			}
			if isZFSDatasetMissingError(err) {
				err = nil
			}
		}
		cleanupErr = errors.Join(cleanupErr, err)
	}
	if state.StagingDir != "" {
		cleanupErr = errors.Join(cleanupErr, removeCreationStagingDir(op.ID, state.StagingDir))
	}
	cleanupErr = errors.Join(cleanupErr, s.cleanupCreationSnapshots(cleanupCtx, op, state))
	if state.ConfigDir != "" {
		marker, err := os.ReadFile(filepath.Join(state.ConfigDir, ".creation-operation"))
		if err == nil && string(marker) == op.ID {
			err = os.RemoveAll(state.ConfigDir)
		} else if errors.Is(err, os.ErrNotExist) {
			if _, statErr := os.Lstat(state.ConfigDir); errors.Is(statErr, os.ErrNotExist) {
				err = nil
			} else {
				err = fmt.Errorf("jail_creation_config_ownership_unverified: %s", state.ConfigDir)
			}
		} else if err == nil {
			err = fmt.Errorf("jail_creation_config_ownership_unverified: %s", state.ConfigDir)
		}
		cleanupErr = errors.Join(cleanupErr, err)
	}
	if state.DevFSRules {
		cleanupErr = errors.Join(cleanupErr, s.RemoveDevfsRulesForCTID(op.CTID))
	}
	if state.JailID != 0 {
		cleanupErr = errors.Join(cleanupErr, s.DB.Transaction(func(tx *gorm.DB) error {
			var jail jailModels.Jail
			if err := tx.Where("id = ? AND ct_id = ? AND creation_pending = ?", state.JailID, op.CTID, true).First(&jail).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			} else if err != nil {
				return err
			}
			for _, model := range []any{&jailModels.Network{}, &jailModels.JailHooks{}, &jailModels.Storage{}, &jailModels.JailStats{}, &jailModels.JailSnapshot{}} {
				if err := tx.Where("jid = ?", state.JailID).Delete(model).Error; err != nil {
					return err
				}
			}
			return tx.Delete(&jail).Error
		}))
	}
	warnings := []string{}
	s.cleanupAutoCreatedJailCreateObjects(op.CTID, state.AutoObjectIDs, &warnings)
	if len(warnings) > 0 {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("jail_creation_cleanup_warnings: %s", strings.Join(warnings, "; ")))
	}
	if cleanupErr == nil && state.Reservation != nil {
		cleanupErr = s.guestIdentityCoordinator.ReleaseGuestIdentities(cleanupCtx, *state.Reservation)
	}
	failure := errors.Join(cause, cleanupErr)
	if failure == nil {
		failure = fmt.Errorf("jail_creation_failed")
	}
	updates := map[string]any{"phase": "failed", "error": failure.Error()}
	if cleanupErr == nil {
		updates["active_ct_id"] = nil
	}
	if cleanupErr != nil {
		cleanupErr = fmt.Errorf("jail_creation_cleanup_blocked: %w\nCleanup is blocked; the destination ID remains reserved.", cleanupErr)
		updates["error"] = errors.Join(cause, cleanupErr).Error()
	}
	return errors.Join(cleanupErr, s.DB.Model(op).Updates(updates).Error)
}

func removeCreationStagingDir(operationID, dir string) error {
	markerPath := filepath.Join(dir, ".creation-operation")
	marker, err := os.ReadFile(markerPath)
	if errors.Is(err, os.ErrNotExist) {
		if _, statErr := os.Lstat(dir); errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
	}
	if err != nil || string(marker) != operationID {
		return fmt.Errorf("jail_creation_staging_ownership_unverified: %s", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != "root" && entry.Name() != ".creation-operation" {
			return fmt.Errorf("jail_creation_staging_unknown_file: %s", filepath.Join(dir, entry.Name()))
		}
	}
	if err := os.Remove(filepath.Join(dir, "root")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(markerPath); err != nil {
		return err
	}
	return os.Remove(dir)
}

func (s *Service) removeCreationReceivedSnapshot(ctx context.Context, ds *gzfs.Dataset, resource creationDataset) error {
	if resource.Snapshot == "" {
		return nil
	}
	snapshot, err := s.GZFS.ZFS.Get(ctx, ds.Name+"@"+resource.Snapshot, false)
	if isZFSDatasetMissingError(err) || err == nil && snapshot == nil {
		return nil
	}
	if err != nil {
		return err
	}
	if snapshot.GUID != resource.SnapshotGUID {
		return fmt.Errorf("jail_creation_received_snapshot_identity_changed: %s", snapshot.Name)
	}
	return snapshot.Destroy(ctx, false, false)
}

func (s *Service) RecoverCreations(ctx context.Context) error {
	s.createMutex.Lock()
	defer s.createMutex.Unlock()
	if !s.DB.Migrator().HasTable(&jailModels.JailCreation{}) {
		return nil
	}
	var operations []jailModels.JailCreation
	if err := s.DB.Where("active_ct_id IS NOT NULL").Find(&operations).Error; err != nil {
		return err
	}
	var result error
	for i := range operations {
		op := &operations[i]
		var state creationState
		if err := json.Unmarshal([]byte(op.State), &state); err != nil {
			result = errors.Join(result, s.blockCreationRecovery(op, err))
			continue
		}
		if state.Reservation != nil {
			state.Reservation.LocalOperationToken = state.LocalToken
			coordinator, ok := s.guestIdentityCoordinator.(clusterServiceInterfaces.GuestIdentityCreationCoordinator)
			if !ok {
				result = errors.Join(result, s.blockCreationRecovery(op, fmt.Errorf("guest_identity_durable_reservation_unavailable")))
				continue
			}
			if err := coordinator.RestoreGuestIdentityReservation(ctx, *state.Reservation); err != nil {
				result = errors.Join(result, s.blockCreationRecovery(op, err))
				continue
			}
		}
		var err error
		if op.Phase == "ready" {
			err = s.commitCreation(ctx, op, &state)
		} else {
			err = s.failCreation(ctx, op, &state, fmt.Errorf("jail_creation_interrupted_by_server_restart"))
		}
		result = errors.Join(result, err)
	}
	return result
}

func (s *Service) blockCreationRecovery(op *jailModels.JailCreation, cause error) error {
	updates := map[string]any{"error": "jail_creation_recovery_blocked: " + cause.Error() + "; the destination ID remains reserved"}
	if op.Phase != "ready" {
		updates["phase"] = "failed"
	}
	return errors.Join(cause, s.DB.Model(op).Updates(updates).Error)
}

func (s *Service) prepareCreationConfigDir(ctx context.Context, op *jailModels.JailCreation, state *creationState, parent string) error {
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	state.ConfigDir = filepath.Join(parent, fmt.Sprint(op.CTID))
	if err := s.saveCreation(s.DB.WithContext(ctx), op, state, "configuring"); err != nil {
		return err
	}
	if err := os.Mkdir(state.ConfigDir, 0755); err != nil {
		return fmt.Errorf("failed_to_create_jail_directory: %w", err)
	}
	return os.WriteFile(filepath.Join(state.ConfigDir, ".creation-operation"), []byte(op.ID), 0600)
}

func (s *Service) provisionJailRoot(ctx context.Context, req jailServiceInterfaces.CreateJailRequest, op *jailModels.JailCreation, state *creationState) (*gzfs.Dataset, string, error) {
	if req.ZFSSource != nil {
		return s.copyZFSSource(ctx, req, op, state)
	}
	name := fmt.Sprintf("%s/sylve/jails/%d", req.Pool, *req.CTID)
	state.Datasets = append(state.Datasets, creationDataset{Name: name})
	if err := s.saveCreation(s.DB.WithContext(ctx), op, state, "provisioning"); err != nil {
		return nil, "", err
	}
	ds, err := s.GZFS.ZFS.CreateFilesystem(ctx, name, map[string]string{creationOwnerProperty: op.ID})
	if err != nil || ds == nil {
		return nil, "", fmt.Errorf("failed_to_create_jail_dataset: %v", err)
	}
	state.Datasets[len(state.Datasets)-1].GUID = ds.GUID
	if err := s.saveCreation(s.DB.WithContext(ctx), op, state, "provisioning"); err != nil {
		return nil, "", err
	}
	mountpoint, err := validateFilesystemDatasetMountpoint(ds, name, ds.GUID)
	if err != nil {
		return nil, "", fmt.Errorf("jail_dataset_mountpoint_not_usable: %w", err)
	}
	var source string
	if req.BootstrapName != "" {
		identity, identityErr := canonicalBootstrapIdentity(req.Pool, req.BootstrapName)
		if identityErr != nil {
			return nil, "", identityErr
		}
		source, err = s.resolveBootstrapMountpoint(ctx, identity)
	} else {
		source, err = s.FindBaseByUUID(req.Base)
	}
	if err != nil {
		return nil, "", err
	}
	if err := utils.CopyDirContents(source, mountpoint); err != nil {
		return nil, "", fmt.Errorf("failed_to_copy_base: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	return ds, mountpoint, nil
}
