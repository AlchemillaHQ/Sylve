// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/hashicorp/raft"
)

type Command struct {
	Version   uint            `json:"version,omitempty"`
	DecidedAt time.Time       `json:"decidedAt,omitempty"`
	Type      string          `json:"type"`
	Action    string          `json:"action"`
	Data      json.RawMessage `json:"data"`
}

const CurrentCommandVersion uint = 1

// NormalizeCommandTime keeps replicated timestamps portable across database
// drivers while retaining more precision than any control-plane deadline
// needs.
func NormalizeCommandTime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

// PrepareCommand records the leader's time in the bytes replicated by Raft.
// Followers must never derive persistent state from their local clocks.
func PrepareCommand(command *Command, decidedAt time.Time) error {
	if command == nil {
		return fmt.Errorf("raft_command_required")
	}
	if decidedAt.IsZero() {
		return fmt.Errorf("raft_command_decided_at_required")
	}
	command.Version = CurrentCommandVersion
	command.DecidedAt = NormalizeCommandTime(decidedAt)
	return nil
}

func commandApplyTime(command Command) (time.Time, error) {
	if command.Version != CurrentCommandVersion {
		return time.Time{}, fmt.Errorf("%w: version %d", errUnknownCommand, command.Version)
	}
	if command.DecidedAt.IsZero() {
		return time.Time{}, fmt.Errorf("raft_command_decided_at_required")
	}
	return NormalizeCommandTime(command.DecidedAt), nil
}

func replicatedCommandTime(db *gorm.DB) time.Time {
	return NormalizeCommandTime(db.NowFunc())
}

type handler func(db *gorm.DB, raw json.RawMessage) error

// FSMState records the last Raft index this node's database reflects. It's
// node-local and written in the same transaction as each entry, so a restart
// resumes after it instead of restoring the last snapshot over newer state.
type FSMState struct {
	ID           uint   `gorm:"primaryKey;autoIncrement:false"`
	AppliedIndex uint64 `gorm:"not null"`
}

func (FSMState) TableName() string { return "raft_fsm_state" }

const fsmStateID = 1

var errEntryAlreadyApplied = errors.New("raft_entry_already_applied")

// A node that meets a command it can't apply would diverge, so it stops (Apply
// panics) without recording the entry, and replays it once it's upgraded.
var errUnknownCommand = errors.New("raft_command_unknown")

// Adding a command bumps ClusterProtocol and registers it with OnSince at the
// new value (a changed command gets a new action), so a leader proposes it
// only once every member runs a build that knows it.
const ClusterProtocol = 1

type FSMDispatcher struct {
	DB           *gorm.DB
	mu           sync.RWMutex
	sm           sync.Mutex
	handlers     map[string]handler
	protocols    map[string]int
	types        map[string]bool
	afterApply   map[string][]func()
	appliedIndex atomic.Uint64
}

func NewFSMDispatcher(db *gorm.DB) *FSMDispatcher {
	f := &FSMDispatcher{
		DB:        db,
		handlers:  make(map[string]handler),
		protocols: make(map[string]int),
		types:     make(map[string]bool),
	}
	if err := dropRetiredReplicatedState(db); err != nil {
		logger.L.Error().Err(err).Msg("raft_fsm_retired_state_cleanup_failed")
	}
	if err := db.AutoMigrate(append(ReplicatedModels(), &FSMState{})...); err != nil {
		logger.L.Error().Err(err).Msg("raft_fsm_state_migration_failed")
	}
	var state FSMState
	if err := db.Where("id = ?", fsmStateID).Limit(1).Find(&state).Error; err == nil && state.ID != 0 {
		f.appliedIndex.Store(state.AppliedIndex)
	}
	return f
}

func On[T any](f *FSMDispatcher, commandType, action string, fn func(*gorm.DB, *T) error) {
	OnSince(f, 1, commandType, action, fn)
}

func OnSince[T any](f *FSMDispatcher, protocol int, commandType, action string, fn func(*gorm.DB, *T) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.types[commandType] = true
	f.protocols[commandType+"/"+action] = protocol
	f.handlers[commandType+"/"+action] = func(db *gorm.DB, raw json.RawMessage) error {
		var payload T
		if err := json.Unmarshal(raw, &payload); err != nil {
			return err
		}
		return fn(db, &payload)
	}
}

// fn also runs after a restore or reset, with applies held off, so it may read
// the database but must not apply commands.
func (f *FSMDispatcher) AfterApply(commandType string, fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.afterApply == nil {
		f.afterApply = make(map[string][]func())
	}
	f.afterApply[commandType] = append(f.afterApply[commandType], fn)
}

func (f *FSMDispatcher) runAfterApply(commandType string) {
	f.mu.RLock()
	var hooks []func()
	for hookType, fns := range f.afterApply {
		if commandType == "" || hookType == commandType {
			hooks = append(hooks, fns...)
		}
	}
	f.mu.RUnlock()
	for _, fn := range hooks {
		fn()
	}
}

func (f *FSMDispatcher) Protocol(commandType, action string) int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return max(f.protocols[commandType+"/"+action], 1)
}

func (f *FSMDispatcher) Apply(l *raft.Log) any {
	if l.Type != raft.LogCommand {
		return nil
	}
	f.sm.Lock()
	defer f.sm.Unlock()

	var cmd Command
	var rejected error
	err := f.DB.Transaction(func(tx *gorm.DB) error {
		var state FSMState
		if err := tx.Where("id = ?", fsmStateID).Limit(1).Find(&state).Error; err != nil {
			return err
		}
		if state.ID != 0 && l.Index <= state.AppliedIndex {
			return errEntryAlreadyApplied
		}
		cmd, rejected = f.apply(tx, l)
		if errors.Is(rejected, errUnknownCommand) {
			return rejected
		}
		return tx.Save(&FSMState{ID: fsmStateID, AppliedIndex: l.Index}).Error
	})
	switch {
	case errors.Is(err, errEntryAlreadyApplied):
		return nil
	case errors.Is(err, errUnknownCommand):
		panic(fmt.Sprintf("raft entry %d: %v: upgrade this node", l.Index, err))
	case err != nil:
		logger.L.Error().Err(err).Uint64("index", l.Index).Msg("raft_apply_failed")
		return err
	}
	f.recordAppliedIndex(l.Index)
	if rejected != nil {
		// Followers drop Apply's result, so log it: every node runs the same
		// entry, and a rejection is either the same everywhere or the first
		// sign this node has diverged.
		logger.L.Warn().Err(rejected).
			Str("type", cmd.Type).
			Str("action", cmd.Action).
			Uint64("index", l.Index).
			Msg("raft_command_rejected")
		return rejected
	}
	f.runAfterApply(cmd.Type)
	return nil
}

// HasPersistedAppliedIndex reports whether this node's database records the
// index it reflects, in which case Raft needn't restore a snapshot on start.
func (f *FSMDispatcher) HasPersistedAppliedIndex() (bool, error) {
	var count int64
	err := f.DB.Model(&FSMState{}).Where("id = ?", fsmStateID).Count(&count).Error
	return count > 0, err
}

// ResetAppliedIndex forgets the recorded index. Call it whenever the Raft log
// this node follows is wiped: the next one restarts from index 1.
func (f *FSMDispatcher) ResetAppliedIndex() error {
	f.sm.Lock()
	defer f.sm.Unlock()
	if err := f.DB.Where("id = ?", fsmStateID).Delete(&FSMState{}).Error; err != nil {
		return err
	}
	f.resetAppliedIndexLocked()
	return nil
}

// apply runs one entry's handler in a savepoint of tx: a handler that fails
// part-way through leaves nothing behind, while the caller still records the
// entry as applied, as every other node does.
func (f *FSMDispatcher) apply(tx *gorm.DB, l *raft.Log) (Command, error) {
	var cmd Command
	if err := json.Unmarshal(l.Data, &cmd); err != nil {
		return cmd, fmt.Errorf("unmarshal: %w", err)
	}
	decidedAt, err := commandApplyTime(cmd)
	if err != nil {
		return cmd, err
	}
	return cmd, f.run(tx, cmd, decidedAt)
}

func (f *FSMDispatcher) run(tx *gorm.DB, cmd Command, decidedAt time.Time) error {
	f.mu.RLock()
	h, ok := f.handlers[cmd.Type+"/"+cmd.Action]
	known := f.types[cmd.Type]
	f.mu.RUnlock()
	if !ok {
		if known {
			return unsupportedCommandAction(cmd.Action)
		}
		return fmt.Errorf("%w: no handler for %s", errUnknownCommand, cmd.Type)
	}
	commandDB := tx.Session(&gorm.Session{
		NowFunc: func() time.Time { return decidedAt },
	})
	if err := commandDB.Transaction(func(tx *gorm.DB) error {
		return h(tx, cmd.Data)
	}); err != nil {
		return fmt.Errorf("handler: %w", err)
	}
	return nil
}

func (f *FSMDispatcher) ApplyLocal(cmd Command) error {
	decidedAt, err := commandApplyTime(cmd)
	if err != nil {
		return err
	}
	f.sm.Lock()
	defer f.sm.Unlock()
	if err := f.DB.Transaction(func(tx *gorm.DB) error {
		return f.run(tx, cmd, decidedAt)
	}); err != nil {
		return err
	}
	f.runAfterApply(cmd.Type)
	return nil
}

func unsupportedCommandAction(action string) error {
	return fmt.Errorf("%w: unsupported_raft_command_action_%s", errUnknownCommand, action)
}

func (f *FSMDispatcher) recordAppliedIndex(index uint64) {
	if f == nil || index == 0 {
		return
	}
	f.appliedIndex.Store(index)
}

func (f *FSMDispatcher) AppliedIndex() uint64 {
	if f == nil {
		return 0
	}
	return f.appliedIndex.Load()
}

func (f *FSMDispatcher) resetAppliedIndexLocked() {
	f.appliedIndex.Store(0)
}

func (f *FSMDispatcher) ClearReplicatedState() error {
	if f == nil || f.DB == nil {
		return fmt.Errorf("raft_fsm_unavailable")
	}
	f.sm.Lock()
	defer f.sm.Unlock()
	if err := f.DB.Transaction(func(tx *gorm.DB) error {
		if err := ClearReplicatedStateTx(tx); err != nil {
			return err
		}
		return tx.Where("id = ?", fsmStateID).Delete(&FSMState{}).Error
	}); err != nil {
		return err
	}
	f.resetAppliedIndexLocked()
	f.runAfterApply("")
	return nil
}

func (f *FSMDispatcher) Snapshot() (raft.FSMSnapshot, error) {
	f.sm.Lock()
	defer f.sm.Unlock()
	snapshot, err := captureClusterSnapshot(f.DB)
	if err != nil {
		return nil, err
	}
	snapshot.AppliedIndex = f.AppliedIndex()
	return snapshot, nil
}

func (f *FSMDispatcher) StateDigest() (string, uint64, error) {
	if f == nil {
		return "", 0, fmt.Errorf("raft_fsm_unavailable")
	}
	f.sm.Lock()
	defer f.sm.Unlock()

	snapshot, err := captureClusterSnapshot(f.DB)
	if err != nil {
		return "", 0, err
	}
	digest, err := ClusterSnapshotDigest(snapshot)
	if err != nil {
		return "", 0, err
	}
	return digest, f.AppliedIndex(), nil
}

func (f *FSMDispatcher) Restore(rc io.ReadCloser) error {
	defer rc.Close()
	f.sm.Lock()
	defer f.sm.Unlock()

	var snap ClusterSnapshot
	if err := gob.NewDecoder(rc).Decode(&snap); err != nil {
		return err
	}
	if err := f.DB.Transaction(func(tx *gorm.DB) error {
		if err := restoreReplicatedStateTx(tx, &snap); err != nil {
			return err
		}
		// Raft resumes after the snapshot's index, so record what this
		// database now reflects in the same transaction as the restore.
		return tx.Save(&FSMState{ID: fsmStateID, AppliedIndex: snap.AppliedIndex}).Error
	}); err != nil {
		return err
	}
	f.appliedIndex.Store(snap.AppliedIndex)
	f.runAfterApply("")
	return nil
}

// Persist cancels the sink on a failed encode. Closing it instead would
// commit a truncated snapshot as the latest one.
func (s *ClusterSnapshot) Persist(sink raft.SnapshotSink) error {
	if digest, err := ClusterSnapshotDigest(s); err == nil {
		logger.L.Info().Uint64("applied_index", s.AppliedIndex).Str("digest", digest).Msg("replicated_state_snapshot")
	}
	if err := gob.NewEncoder(sink).Encode(s); err != nil {
		_ = sink.Cancel()
		return err
	}
	return sink.Close()
}

func (s *ClusterSnapshot) Release() {}

type (
	commandID struct {
		ID uint `json:"id"`
	}
	noteID struct {
		ID int `json:"id"`
	}
	noteIDs struct {
		IDs []int `json:"ids"`
	}
	backupJobState struct {
		Version     uint       `json:"version"`
		JobID       uint       `json:"jobId"`
		LastRunAt   *time.Time `json:"lastRunAt"`
		LastStatus  string     `json:"lastStatus"`
		LastError   string     `json:"lastError"`
		NextRunAt   *time.Time `json:"nextRunAt"`
		Encrypted   *bool      `json:"encrypted"`
		NextRunOnly bool       `json:"nextRunOnly"`
	}
	backupJobFriendlySource struct {
		JobIDs      []uint `json:"jobIds"`
		FriendlySrc string `json:"friendlySrc"`
	}
	replicationPolicyState struct {
		ID         uint       `json:"id"`
		LastRunAt  *time.Time `json:"lastRunAt"`
		LastStatus string     `json:"lastStatus"`
		LastError  string     `json:"lastError"`
		NextRunAt  *time.Time `json:"nextRunAt"`
	}
	replicationPolicyTransitionUpdate struct {
		PolicyID   uint                        `json:"policyId"`
		Transition ReplicationPolicyTransition `json:"transition"`
	}
)

func RegisterDefaultHandlers(f *FSMDispatcher) {
	RegisterGuestIdentityHandlers(f)

	On(f, "cluster_state", "checkpoint", func(*gorm.DB, *struct{}) error { return nil })

	On(f, "note", "create", upsertNote)
	On(f, "note", "update", func(db *gorm.DB, note *ClusterNote) error {
		result := db.Model(&ClusterNote{}).Where("id = ?", note.ID).Updates(*note)
		return requireRowsAffected(result, 1)
	})
	On(f, "note", "delete", func(db *gorm.DB, p *noteID) error {
		return requireRowsAffected(db.Delete(&ClusterNote{}, p.ID), 1)
	})
	On(f, "note", "bulk_delete", func(db *gorm.DB, p *noteIDs) error {
		return requireRowsAffected(db.Delete(&ClusterNote{}, p.IDs), int64(len(p.IDs)))
	})

	On(f, "backup_target", "create_v3", ApplyBackupTargetCreateV3Txn)
	On(f, "backup_target", "update_v2", ApplyBackupTargetUpdateV2Txn)
	On(f, "backup_target", "delete_v2", func(db *gorm.DB, p *commandID) error {
		if p.ID == 0 {
			return nil
		}
		return DeleteBackupTargetTxn(db, p.ID)
	})

	On(f, "backup_target_ssh_host_trust", "initialize_all_v1", func(db *gorm.DB, change *BackupTargetSSHHostTrustChange) error {
		return InitializeBackupTargetSSHHostTrustTxn(db, change.OccurredAt)
	})
	for _, action := range []string{"initialize_v1", "install_v1", "reset_v1"} {
		On(f, "backup_target_ssh_host_trust", action, func(db *gorm.DB, change *BackupTargetSSHHostTrustChange) error {
			return ApplyBackupTargetSSHHostTrustTxn(db, action, change)
		})
	}

	On(f, "backup_target_provision", "prepare_v2", PrepareBackupTargetProvisionOperationV2Txn)
	On(f, "backup_target_provision", "complete", CompleteBackupTargetProvisionOperationTxn)
	On(f, "backup_target_provision", "fail", FailBackupTargetProvisionOperationTxn)

	On(f, "backup_target_readiness", "update_v2", ApplyBackupTargetNodeReadinessUpdateV2Txn)

	On(f, "backup_job", "create", func(db *gorm.DB, p *BackupJobCommandPayload) error {
		if !validBackupJobMode(p.Job.Mode) {
			return fmt.Errorf("invalid_backup_job_mode")
		}
		if p.TargetReadiness != nil {
			if err := ApplyBackupTargetNodeReadinessForJobTxn(db, &p.Job, p.TargetReadiness); err != nil {
				return err
			}
		}
		if err := ValidateBackupJobPlacementFenceTxn(db, &p.Job, p.PlacementFence); err != nil {
			return err
		}
		return upsertBackupJob(db, &p.Job)
	})
	On(f, "backup_job", "update", func(db *gorm.DB, p *BackupJobCommandPayload) error {
		if !validBackupJobMode(p.Job.Mode) {
			return fmt.Errorf("invalid_backup_job_mode")
		}
		rebindPending, err := BackupJobRunnerRebindPendingForJob(db, p.Job.ID)
		if err != nil {
			return err
		}
		if rebindPending {
			return fmt.Errorf("backup_job_runner_rebind_pending")
		}
		if p.PreviousPlacementFence != nil {
			var existing BackupJob
			result := db.Where("id = ?", p.Job.ID).Limit(1).Find(&existing)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return fmt.Errorf("backup_job_not_found")
			}
			if err := ValidateBackupJobPlacementFenceTxn(db, &existing, p.PreviousPlacementFence); err != nil {
				return err
			}
		}
		if err := ValidateBackupJobPlacementFenceTxn(db, &p.Job, p.PlacementFence); err != nil {
			return err
		}
		if p.TargetReadiness != nil {
			if err := ApplyBackupTargetNodeReadinessForJobTxn(db, &p.Job, p.TargetReadiness); err != nil {
				return err
			}
		}
		return ApplyBackupJobUpdateTxn(db, &p.Job)
	})
	On(f, "backup_job", "delete", func(db *gorm.DB, p *commandID) error {
		return DeleteBackupJobTxn(db, p.ID)
	})

	On(f, "backup_job_runner_rebind", "prepare", PrepareBackupJobRunnerRebindTxn)
	On(f, "backup_job_runner_rebind", "ready", ReadyBackupJobRunnerRebindTxn)
	On(f, "backup_job_runner_rebind", "apply", ApplyBackupJobRunnerRebindTxn)
	On(f, "backup_job_runner_rebind", "repair", RepairBackupJobRunnerRebindTxn)
	On(f, "backup_job_runner_rebind", "pending", PendingBackupJobRunnerRebindTxn)
	On(f, "backup_job_runner_rebind", "abort", AbortFailedFailoverBackupJobRunnerRebindTxn)

	On(f, "backup_job_operation", "acquire_v2", AcquireBackupJobOperationV2Txn)
	On(f, "backup_job_operation", "start_v2", StartBackupJobOperationV2Txn)
	On(f, "backup_job_operation", "finish", FinishBackupJobOperationTxn)
	On(f, "backup_job_operation", "abort", AbortBackupJobOperationTxn)
	On(f, "backup_job_operation", "release", ReleaseBackupJobOperationTxn)

	On(f, "backup_target_restore_operation", "acquire_v2", AcquireBackupTargetRestoreOperationV2Txn)
	On(f, "backup_target_restore_operation", "start_v2", StartBackupTargetRestoreOperationV2Txn)
	On(f, "backup_target_restore_operation", "finish", FinishBackupTargetRestoreOperationTxn)
	On(f, "backup_target_restore_operation", "requeue", RequeueBackupTargetRestoreOperationTxn)
	On(f, "backup_target_restore_operation", "abort", AbortBackupTargetRestoreOperationTxn)
	On(f, "backup_target_restore_operation", "release", ReleaseBackupTargetRestoreOperationTxn)

	On(f, "backup_job_schedule", "decide_v2", ApplyBackupJobScheduleDecisionV2Txn)
	On(f, "backup_job_schedule", "complete", CompleteBackupJobRunTxn)

	On(f, "replication_policy_schedule", "decide", ApplyReplicationPolicyScheduleDecisionTxn)
	On(f, "replication_policy_schedule", "start", StartReplicationRunOperationTxn)
	On(f, "replication_policy_schedule", "complete", CompleteReplicationPolicyRunTxn)

	On(f, "backup_job_state", "update", func(db *gorm.DB, p *backupJobState) error {
		if p.JobID == 0 {
			return fmt.Errorf("invalid_job_id")
		}
		job := db.Model(&BackupJob{}).Where("id = ?", p.JobID)
		if p.NextRunOnly {
			return job.Update("next_run_at", p.NextRunAt).Error
		}
		status := strings.TrimSpace(strings.ToLower(p.LastStatus))
		if status == "" {
			return fmt.Errorf("last_status_required")
		}
		if status != "success" && status != "failed" && status != "running" && status != "blocked" {
			return fmt.Errorf("invalid_last_status")
		}
		updates := map[string]any{
			"last_run_at": p.LastRunAt,
			"last_status": status,
			"last_error":  strings.TrimSpace(p.LastError),
			"next_run_at": p.NextRunAt,
		}
		if p.Encrypted != nil {
			updates["encrypted"] = *p.Encrypted
		}
		return job.Updates(updates).Error
	})

	On(f, "backup_job_friendly_source", "update", func(db *gorm.DB, p *backupJobFriendlySource) error {
		friendlySrc := strings.TrimSpace(p.FriendlySrc)
		if friendlySrc == "" {
			return fmt.Errorf("friendly_src_required")
		}
		seen := make(map[uint]struct{}, len(p.JobIDs))
		jobIDs := make([]uint, 0, len(p.JobIDs))
		for _, id := range p.JobIDs {
			if _, exists := seen[id]; id == 0 || exists {
				continue
			}
			seen[id] = struct{}{}
			jobIDs = append(jobIDs, id)
		}
		if len(jobIDs) == 0 {
			return nil
		}
		return db.Model(&BackupJob{}).Where("id IN ?", jobIDs).Update("friendly_src", friendlySrc).Error
	})

	On(f, "replication_policy", "create", func(db *gorm.DB, p *ReplicationPolicyPayload) error {
		if err := upsertReplicationPolicy(db, &p.Policy, p.Targets); err != nil {
			return err
		}
		return dropDisabledReplicationPolicyLeases(db, &p.Policy)
	})
	On(f, "replication_policy", "update", func(db *gorm.DB, p *ReplicationPolicyPayload) error {
		if err := updateReplicationPolicy(db, p); err != nil {
			return err
		}
		return dropDisabledReplicationPolicyLeases(db, &p.Policy)
	})
	On(f, "replication_policy", "delete", func(db *gorm.DB, p *commandID) error {
		if p.ID == 0 {
			return nil
		}
		return DeleteReplicationPolicyTxn(db, p.ID)
	})
	On(f, "replication_policy", "state_update", func(db *gorm.DB, p *replicationPolicyState) error {
		if p.ID == 0 {
			return nil
		}
		return db.Model(&ReplicationPolicy{}).Where("id = ?", p.ID).Updates(map[string]any{
			"last_run_at": p.LastRunAt,
			"last_status": p.LastStatus,
			"last_error":  p.LastError,
			"next_run_at": p.NextRunAt,
		}).Error
	})

	On(f, "replication_lease", "upsert", upsertReplicationLease)
	On(f, "replication_lease", "upsert_batch", func(db *gorm.DB, leases *[]ReplicationLease) error {
		for i := range *leases {
			if err := upsertReplicationLease(db, &(*leases)[i]); err != nil {
				return err
			}
		}
		return nil
	})

	On(f, "replication_guest_operation", "acquire", acquireReplicationGuestOperation)
	On(f, "replication_guest_operation", "seal", sealReplicationGuestOperation)
	On(f, "replication_guest_operation", "abort", abortReplicationGuestOperation)
	On(f, "replication_guest_operation", "complete", completeReplicationGuestOperation)

	On(f, "replication_policy_transition", "begin", beginReplicationPolicyTransition)
	On(f, "replication_policy_transition", "update", func(db *gorm.DB, p *replicationPolicyTransitionUpdate) error {
		return upsertReplicationPolicyTransition(db, p.PolicyID, &p.Transition)
	})

	On(f, "replication_ownership_transition", "commit", applyReplicationOwnershipTransition)
	On(f, "replication_ownership_transition", "reassign_disabled", reassignDisabledReplicationPolicyOwner)

	On(f, "replication_target_readiness", "update", updateReplicationTargetReadiness)
	On(f, "replication_policy_protection_state", "update", updateReplicationPolicyProtectionState)

	On(f, "cluster_ssh_identity", "upsert", upsertClusterSSHIdentity)
	On(f, "cluster_member", "remove", removeClusterMemberTxn)
	On(f, "cluster_join_key", "set", setClusterJoinKeyTxn)

	On(f, "encryption_key", "upsert", upsertEncryptionKey)

	for _, action := range []string{"create", "update"} {
		On(f, "replication_transition_event", action, func(db *gorm.DB, event *ReplicationTransitionEvent) error {
			if event.ID == 0 {
				return fmt.Errorf("replication_transition_event_id_required")
			}
			return upsertReplicationTransitionEvent(db, event)
		})
	}

	On(f, "replicated_retention", "apply", ApplyReplicatedRetentionTxn)
}

func requireRowsAffected(result *gorm.DB, expected int64) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != expected {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func dropDisabledReplicationPolicyLeases(db *gorm.DB, policy *ReplicationPolicy) error {
	if policy.Enabled {
		return nil
	}
	return db.Where("policy_id = ?", policy.ID).Delete(&ReplicationLease{}).Error
}

func replicationEventConflictClause() clause.OnConflict {
	return clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"policy_id",
			"transition_run_id",
			"event_type",
			"status",
			"message",
			"error",
			"output",
			"source_node_id",
			"target_node_id",
			"guest_type",
			"guest_id",
			"started_at",
			"completed_at",
			"updated_at",
		}),
	}
}

func upsertReplicationTransitionEvent(db *gorm.DB, event *ReplicationTransitionEvent) error {
	return db.Clauses(replicationEventConflictClause()).Create(event).Error
}

func validBackupJobMode(mode string) bool {
	return mode == BackupJobModeDataset || mode == BackupJobModeJail || mode == BackupJobModeVM
}

func upsertEncryptionKey(db *gorm.DB, key *EncryptionKey) error {
	if strings.TrimSpace(key.UUID) == "" {
		return fmt.Errorf("encryption_key_uuid_required")
	}
	if strings.TrimSpace(key.KeyData) == "" {
		return fmt.Errorf("encryption_key_data_required")
	}

	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "uuid"}},
		DoUpdates: clause.AssignmentColumns([]string{"key_data", "key_format"}),
	}).Create(key).Error
}
