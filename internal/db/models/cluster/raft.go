// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/hashicorp/raft"
)

type Command struct {
	Type   string          `json:"type"`
	Action string          `json:"action"`
	Data   json.RawMessage `json:"data"`
}

type HandlerFn func(db *gorm.DB, action string, raw json.RawMessage) error

type FSMDispatcher struct {
	DB       *gorm.DB
	mu       sync.RWMutex
	sm       sync.Mutex
	handlers map[string]HandlerFn
}

func NewFSMDispatcher(db *gorm.DB) *FSMDispatcher {
	return &FSMDispatcher{
		DB:       db,
		handlers: make(map[string]HandlerFn),
	}
}

func (f *FSMDispatcher) Register(t string, fn HandlerFn) {
	f.mu.Lock()
	f.handlers[t] = fn
	f.mu.Unlock()
}

func (f *FSMDispatcher) Apply(l *raft.Log) any {
	if l.Type != raft.LogCommand {
		return nil
	}
	var cmd Command
	if err := json.Unmarshal(l.Data, &cmd); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}

	f.mu.RLock()
	h, ok := f.handlers[cmd.Type]
	f.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no handler for %s", cmd.Type)
	}

	f.sm.Lock()
	defer f.sm.Unlock()

	if err := h(f.DB, cmd.Action, cmd.Data); err != nil {
		return fmt.Errorf("handler: %w", err)
	}
	return nil
}

// ClusterSnapshot represents the state that will be snapshotted/restored.
type ClusterSnapshot struct {
	Notes                         []ClusterNote                      `json:"notes"`
	Options                       []ClusterOption                    `json:"options"`
	BackupTargets                 []BackupTargetReplicationPayload   `json:"backupTargets"`
	BackupTargetProvisions        []BackupTargetProvisionOperation   `json:"backupTargetProvisions,omitempty"`
	BackupTargetReadiness         []BackupTargetNodeReadiness        `json:"backupTargetReadiness,omitempty"`
	BackupJobs                    []BackupJob                        `json:"backupJobs"`
	BackupJobOperations           []BackupJobOperation               `json:"backupJobOperations"`
	BackupTargetRestoreOperations []BackupTargetRestoreOperation     `json:"backupTargetRestoreOperations,omitempty"`
	BackupJobRebinds              []BackupJobRunnerRebind            `json:"backupJobRebinds,omitempty"`
	BackupJobRebindItems          []BackupJobRunnerRebindItem        `json:"backupJobRebindItems,omitempty"`
	ReplicationPolicies           []ReplicationPolicyPayload         `json:"replicationPolicies"`
	ReplicationLeases             []ReplicationLease                 `json:"replicationLeases"`
	GuestOperations               []ReplicationGuestOperation        `json:"guestOperations"`
	GuestOperationReceipts        []ReplicationGuestOperationReceipt `json:"guestOperationReceipts"`
	ReplicationEvents             []ReplicationEvent                 `json:"replicationEvents"`
	SSHIdentities                 []ClusterSSHIdentity               `json:"sshIdentities"`
	EncryptionKeys                []EncryptionKey                    `json:"encryptionKeys"`
	// We can add more tables here as needed
}

func (f *FSMDispatcher) Snapshot() (raft.FSMSnapshot, error) {
	f.sm.Lock()
	defer f.sm.Unlock()
	var snap ClusterSnapshot
	if err := f.DB.Find(&snap.Notes).Error; err != nil {
		return nil, err
	}
	if err := f.DB.Find(&snap.Options).Error; err != nil {
		return nil, err
	}
	var targets []BackupTarget
	if err := f.DB.Order("id ASC").Find(&targets).Error; err != nil {
		return nil, err
	}
	snap.BackupTargets = make([]BackupTargetReplicationPayload, 0, len(targets))
	for _, t := range targets {
		snap.BackupTargets = append(snap.BackupTargets, BackupTargetToReplicationPayload(t))
	}
	if f.DB.Migrator().HasTable(&BackupTargetProvisionOperation{}) {
		if err := f.DB.Order("token ASC").Find(&snap.BackupTargetProvisions).Error; err != nil {
			return nil, err
		}
	}
	if f.DB.Migrator().HasTable(&BackupTargetNodeReadiness{}) {
		if err := f.DB.Order("target_id ASC, node_id ASC").Find(&snap.BackupTargetReadiness).Error; err != nil {
			return nil, err
		}
	}
	if err := f.DB.Order("id ASC").Find(&snap.BackupJobs).Error; err != nil {
		return nil, err
	}
	if err := f.DB.Order("job_id ASC").Find(&snap.BackupJobOperations).Error; err != nil {
		return nil, err
	}
	if f.DB.Migrator().HasTable(&BackupTargetRestoreOperation{}) {
		if err := f.DB.Order("holder_node_id ASC, destination_dataset ASC, token ASC").
			Find(&snap.BackupTargetRestoreOperations).Error; err != nil {
			return nil, err
		}
	}
	if f.DB.Migrator().HasTable(&BackupJobRunnerRebind{}) {
		if err := f.DB.Order("token ASC").Find(&snap.BackupJobRebinds).Error; err != nil {
			return nil, err
		}
	}
	if f.DB.Migrator().HasTable(&BackupJobRunnerRebindItem{}) {
		if err := f.DB.Order("operation_token ASC, job_id ASC").Find(&snap.BackupJobRebindItems).Error; err != nil {
			return nil, err
		}
	}
	var replicationPolicies []ReplicationPolicy
	if err := f.DB.Preload("Targets").Order("id ASC").Find(&replicationPolicies).Error; err != nil {
		return nil, err
	}
	snap.ReplicationPolicies = make([]ReplicationPolicyPayload, 0, len(replicationPolicies))
	for _, policy := range replicationPolicies {
		snap.ReplicationPolicies = append(snap.ReplicationPolicies, ReplicationPolicyPayload{
			Policy:  policy,
			Targets: policy.Targets,
		})
	}
	if err := f.DB.Order("id ASC").Find(&snap.ReplicationLeases).Error; err != nil {
		return nil, err
	}
	if err := f.DB.Order("guest_type ASC, guest_id ASC").Find(&snap.GuestOperations).Error; err != nil {
		return nil, err
	}
	if err := f.DB.Order("token ASC").Find(&snap.GuestOperationReceipts).Error; err != nil {
		return nil, err
	}
	if err := f.DB.Order("id ASC").Find(&snap.ReplicationEvents).Error; err != nil {
		return nil, err
	}
	if err := f.DB.Order("id ASC").Find(&snap.SSHIdentities).Error; err != nil {
		return nil, err
	}
	if err := f.DB.Order("id ASC").Find(&snap.EncryptionKeys).Error; err != nil {
		return nil, err
	}
	return &snap, nil
}

func dedupReplicationTargets(payloads []ReplicationPolicyPayload) ([]ReplicationPolicy, []ReplicationPolicyTarget) {
	replicationPolicies := make([]ReplicationPolicy, 0, len(payloads))
	replicationTargets := make([]ReplicationPolicyTarget, 0)
	seenID := make(map[uint]struct{})
	seenPair := make(map[string]struct{})

	for _, payload := range payloads {
		replicationPolicies = append(replicationPolicies, payload.Policy)
		for _, t := range payload.Targets {
			t.PolicyID = payload.Policy.ID
			if t.ID != 0 {
				if _, exists := seenID[t.ID]; exists {
					continue
				}
				seenID[t.ID] = struct{}{}
			} else {
				k := fmt.Sprintf("%d|%s", t.PolicyID, strings.TrimSpace(t.NodeID))
				if _, exists := seenPair[k]; exists {
					continue
				}
				seenPair[k] = struct{}{}
			}
			replicationTargets = append(replicationTargets, t)
		}
	}

	return replicationPolicies, replicationTargets
}

func (f *FSMDispatcher) Restore(rc io.ReadCloser) error {
	defer rc.Close()
	var snap ClusterSnapshot
	if err := json.NewDecoder(rc).Decode(&snap); err != nil {
		return err
	}
	return f.DB.Transaction(func(tx *gorm.DB) error {
		type restoreSet struct {
			table string
			data  any
			batch int
		}

		backupTargets := make([]BackupTarget, 0, len(snap.BackupTargets))
		for _, t := range snap.BackupTargets {
			backupTargets = append(backupTargets, t.ToModel())
		}
		replicationPolicies, replicationTargets := dedupReplicationTargets(snap.ReplicationPolicies)
		deleteSets := make([]restoreSet, 0, 19)
		if tx.Migrator().HasTable(&BackupJobRunnerRebindItem{}) {
			deleteSets = append(deleteSets, restoreSet{"backup_job_runner_rebind_items", snap.BackupJobRebindItems, 500})
		}
		if tx.Migrator().HasTable(&BackupJobRunnerRebind{}) {
			deleteSets = append(deleteSets, restoreSet{"backup_job_runner_rebinds", snap.BackupJobRebinds, 500})
		}
		if tx.Migrator().HasTable(&BackupTargetRestoreOperation{}) {
			deleteSets = append(deleteSets, restoreSet{"backup_target_restore_operations", snap.BackupTargetRestoreOperations, 500})
		}
		if tx.Migrator().HasTable(&BackupTargetNodeReadiness{}) {
			deleteSets = append(deleteSets, restoreSet{"backup_target_node_readinesses", snap.BackupTargetReadiness, 500})
		}
		if tx.Migrator().HasTable(&BackupTargetProvisionOperation{}) {
			deleteSets = append(deleteSets, restoreSet{"backup_target_provision_operations", snap.BackupTargetProvisions, 500})
		}
		deleteSets = append(deleteSets,
			restoreSet{"backup_job_operations", snap.BackupJobOperations, 500},
			restoreSet{"replication_events", snap.ReplicationEvents, 500},
			restoreSet{"replication_guest_operation_receipts", snap.GuestOperationReceipts, 500},
			restoreSet{"replication_guest_operations", snap.GuestOperations, 500},
			restoreSet{"replication_leases", snap.ReplicationLeases, 500},
			restoreSet{"replication_policy_targets", replicationTargets, 500},
			restoreSet{"replication_policies", replicationPolicies, 500},
		)
		deleteSets = append(deleteSets,
			restoreSet{"cluster_ssh_identities", snap.SSHIdentities, 200},
			restoreSet{"encryption_keys", snap.EncryptionKeys, 200},
			restoreSet{"backup_jobs", snap.BackupJobs, 500},
			restoreSet{"backup_targets", backupTargets, 200},
			restoreSet{"cluster_notes", snap.Notes, 500},
			restoreSet{"cluster_options", snap.Options, 100},
		)

		createSets := []restoreSet{
			{"cluster_ssh_identities", snap.SSHIdentities, 200},
			{"encryption_keys", snap.EncryptionKeys, 200},
		}
		createSets = append(createSets,
			restoreSet{"replication_policies", replicationPolicies, 500},
			restoreSet{"replication_policy_targets", replicationTargets, 500},
			restoreSet{"replication_leases", snap.ReplicationLeases, 500},
			restoreSet{"replication_guest_operations", snap.GuestOperations, 500},
			restoreSet{"replication_guest_operation_receipts", snap.GuestOperationReceipts, 500},
			restoreSet{"replication_events", snap.ReplicationEvents, 500},
			restoreSet{"backup_targets", backupTargets, 200},
		)
		if tx.Migrator().HasTable(&BackupTargetProvisionOperation{}) {
			createSets = append(createSets, restoreSet{"backup_target_provision_operations", snap.BackupTargetProvisions, 500})
		}
		if tx.Migrator().HasTable(&BackupTargetNodeReadiness{}) {
			createSets = append(createSets, restoreSet{"backup_target_node_readinesses", snap.BackupTargetReadiness, 500})
		}
		createSets = append(createSets,
			restoreSet{"backup_jobs", snap.BackupJobs, 500},
			restoreSet{"backup_job_operations", snap.BackupJobOperations, 500},
		)
		if tx.Migrator().HasTable(&BackupTargetRestoreOperation{}) {
			createSets = append(createSets, restoreSet{"backup_target_restore_operations", snap.BackupTargetRestoreOperations, 500})
		}
		if tx.Migrator().HasTable(&BackupJobRunnerRebind{}) {
			createSets = append(createSets, restoreSet{"backup_job_runner_rebinds", snap.BackupJobRebinds, 500})
		}
		if tx.Migrator().HasTable(&BackupJobRunnerRebindItem{}) {
			createSets = append(createSets, restoreSet{"backup_job_runner_rebind_items", snap.BackupJobRebindItems, 500})
		}
		createSets = append(createSets,
			restoreSet{"cluster_notes", snap.Notes, 500},
			restoreSet{"cluster_options", snap.Options, 100},
		)

		for _, s := range deleteSets {
			if err := tx.Exec("DELETE FROM " + s.table).Error; err != nil {
				return err
			}
		}

		for _, s := range createSets {
			val := reflect.ValueOf(s.data)
			if val.Kind() == reflect.Slice && val.Len() > 0 {
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(s.data, s.batch).Error; err != nil {
					return err
				}
			}
		}

		return nil
	})
}

func (s *ClusterSnapshot) Persist(sink raft.SnapshotSink) error {
	defer sink.Close()
	enc := json.NewEncoder(sink)
	return enc.Encode(s)
}

func (s *ClusterSnapshot) Release() {}

func RegisterDefaultHandlers(fsm *FSMDispatcher) {
	fsm.Register("note", func(db *gorm.DB, action string, raw json.RawMessage) error {
		var note ClusterNote
		switch action {
		case "create":
			if err := json.Unmarshal(raw, &note); err != nil {
				return err
			}
			return upsertNote(db, &note)
		case "update":
			if err := json.Unmarshal(raw, &note); err != nil {
				return err
			}
			return db.Model(&ClusterNote{}).
				Where("id = ?", note.ID).
				Updates(note).Error
		case "delete":
			var payload struct{ ID int }
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return db.Delete(&ClusterNote{}, payload.ID).Error
		case "bulk_delete":
			var payload struct{ IDs []int }
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			if len(payload.IDs) > 0 {
				return db.Delete(&ClusterNote{}, payload.IDs).Error
			}
			return nil
		default:
			return nil
		}
	})

	fsm.Register("options", func(db *gorm.DB, action string, raw json.RawMessage) error {
		var opt ClusterOption
		if err := json.Unmarshal(raw, &opt); err != nil {
			return err
		}
		opt.ID = 1
		if action == "set" {
			return upsertOption(db, &opt)
		}
		return nil
	})

	fsm.Register("backup_target", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "create_v2":
			var command BackupTargetCreateV2
			if err := json.Unmarshal(raw, &command); err != nil {
				return err
			}
			return ApplyBackupTargetCreateV2Txn(db, &command)
		case "update_v2":
			var command BackupTargetUpdateV2
			if err := json.Unmarshal(raw, &command); err != nil {
				return err
			}
			return ApplyBackupTargetUpdateV2Txn(db, &command)
		case "create":
			var payload BackupTargetReplicationPayload
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			target := payload.ToModel()
			return upsertBackupTarget(db, &target)
		case "update":
			var payload BackupTargetReplicationPayload
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			target := payload.ToModel()
			return upsertBackupTarget(db, &target)
		case "delete", "delete_v2":
			var payload struct {
				ID uint `json:"id"`
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}

			if payload.ID == 0 {
				return nil
			}
			if action == "delete_v2" {
				return DeleteBackupTargetTxn(db, payload.ID)
			}
			return DeleteBackupTargetLegacyTxn(db, payload.ID)
		default:
			return nil
		}
	})

	fsm.Register("backup_target_provision", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "prepare":
			var prepare BackupTargetProvisionPrepare
			if err := json.Unmarshal(raw, &prepare); err != nil {
				return err
			}
			return PrepareBackupTargetProvisionOperationTxn(db, &prepare)
		case "complete":
			var transition BackupTargetProvisionTransition
			if err := json.Unmarshal(raw, &transition); err != nil {
				return err
			}
			return CompleteBackupTargetProvisionOperationTxn(db, &transition)
		case "fail":
			var transition BackupTargetProvisionTransition
			if err := json.Unmarshal(raw, &transition); err != nil {
				return err
			}
			return FailBackupTargetProvisionOperationTxn(db, &transition)
		default:
			return nil
		}
	})

	fsm.Register("backup_target_readiness", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "update":
			var update BackupTargetNodeReadinessUpdate
			if err := json.Unmarshal(raw, &update); err != nil {
				return err
			}
			return ApplyBackupTargetNodeReadinessUpdateTxn(db, &update)
		case "backfill":
			var row BackupTargetNodeReadiness
			if err := json.Unmarshal(raw, &row); err != nil {
				return err
			}
			return UpsertBackupTargetNodeReadinessBackfillTxn(db, &row)
		default:
			return nil
		}
	})

	fsm.Register("backup_job", func(db *gorm.DB, action string, raw json.RawMessage) error {
		decodeMutation := func() (*BackupJob, *BackupJobPlacementFence, *BackupJobPlacementFence, *BackupTargetNodeReadinessUpdate, error) {
			var envelope struct {
				Job                    *BackupJob                       `json:"job"`
				PlacementFence         *BackupJobPlacementFence         `json:"placementFence"`
				PreviousPlacementFence *BackupJobPlacementFence         `json:"previousPlacementFence"`
				TargetReadiness        *BackupTargetNodeReadinessUpdate `json:"targetReadiness"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil {
				return nil, nil, nil, nil, err
			}
			if envelope.Job != nil {
				return envelope.Job, envelope.PlacementFence, envelope.PreviousPlacementFence, envelope.TargetReadiness, nil
			}

			// Compatibility with raw BackupJob entries written before the
			// placement-fence and target-readiness envelope was introduced.
			var legacy BackupJob
			if err := json.Unmarshal(raw, &legacy); err != nil {
				return nil, nil, nil, nil, err
			}
			return &legacy, nil, nil, nil, nil
		}

		switch action {
		case "create":
			job, fence, _, targetReadiness, err := decodeMutation()
			if err != nil {
				return err
			}
			if !validBackupJobMode(job.Mode) {
				return fmt.Errorf("invalid_backup_job_mode")
			}
			return db.Transaction(func(tx *gorm.DB) error {
				if targetReadiness != nil {
					if err := ApplyBackupTargetNodeReadinessForJobTxn(tx, job, targetReadiness); err != nil {
						return err
					}
				}
				if err := ValidateBackupJobPlacementFenceTxn(tx, job, fence); err != nil {
					return err
				}
				return upsertBackupJob(tx, job)
			})
		case "update":
			job, fence, previousFence, targetReadiness, err := decodeMutation()
			if err != nil {
				return err
			}
			if !validBackupJobMode(job.Mode) {
				return fmt.Errorf("invalid_backup_job_mode")
			}
			return db.Transaction(func(tx *gorm.DB) error {
				rebindPending, err := BackupJobRunnerRebindPendingForJob(tx, job.ID)
				if err != nil {
					return err
				}
				if rebindPending {
					return fmt.Errorf("backup_job_runner_rebind_pending")
				}
				if previousFence != nil {
					var existing BackupJob
					result := tx.Where("id = ?", job.ID).Limit(1).Find(&existing)
					if result.Error != nil {
						return result.Error
					}
					if result.RowsAffected == 0 {
						return fmt.Errorf("backup_job_not_found")
					}
					if err := ValidateBackupJobPlacementFenceTxn(tx, &existing, previousFence); err != nil {
						return err
					}
				}
				if err := ValidateBackupJobPlacementFenceTxn(tx, job, fence); err != nil {
					return err
				}
				if targetReadiness != nil {
					if err := ApplyBackupTargetNodeReadinessForJobTxn(tx, job, targetReadiness); err != nil {
						return err
					}
				}
				// Use Updates with map to properly handle boolean false values.
				if err := tx.Model(&BackupJob{}).Where("id = ?", job.ID).Updates(map[string]any{
					"name":               job.Name,
					"target_id":          job.TargetID,
					"runner_node_id":     job.RunnerNodeID,
					"mode":               job.Mode,
					"source_dataset":     job.SourceDataset,
					"jail_root_dataset":  job.JailRootDataset,
					"friendly_src":       job.FriendlySrc,
					"dest_suffix":        job.DestSuffix,
					"prune_keep_last":    job.PruneKeepLast,
					"prune_target":       job.PruneTarget,
					"stop_before_backup": job.StopBeforeBackup,
					"recursive":          job.Recursive,
					"cron_expr":          job.CronExpr,
					"enabled":            job.Enabled,
					"next_run_at":        job.NextRunAt,
				}).Error; err != nil {
					return err
				}
				// A normal update has already passed runner-local validation and
				// therefore acts as the explicit repair acknowledgement.
				return ClearBackupJobRepairRequiredTxn(tx, job.ID)
			})
		case "delete":
			var payload struct {
				ID uint `json:"id"`
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return DeleteBackupJobTxn(db, payload.ID)
		default:
			return nil
		}
	})

	fsm.Register("backup_job_runner_rebind", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "prepare":
			var payload BackupJobRunnerRebindPlan
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return PrepareBackupJobRunnerRebindTxn(db, &payload)
		case "ready":
			var payload BackupJobRunnerRebindReady
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return ReadyBackupJobRunnerRebindTxn(db, &payload)
		case "apply":
			var payload BackupJobRunnerRebindApply
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return ApplyBackupJobRunnerRebindTxn(db, &payload)
		case "repair":
			var payload BackupJobRunnerRebindRepair
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return RepairBackupJobRunnerRebindTxn(db, &payload)
		case "pending":
			var payload BackupJobRunnerRebindPending
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return PendingBackupJobRunnerRebindTxn(db, &payload)
		case "abort":
			var payload BackupJobRunnerRebindAbort
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return AbortFailedFailoverBackupJobRunnerRebindTxn(db, &payload)
		default:
			return nil
		}
	})

	fsm.Register("backup_job_operation", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "acquire":
			var payload BackupJobOperationAcquire
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return AcquireBackupJobOperationTxn(db, &payload)
		case "start", "finish", "abort", "release":
			var payload BackupJobOperationTransition
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			switch action {
			case "start":
				return StartBackupJobOperationTxn(db, &payload)
			case "finish":
				return FinishBackupJobOperationTxn(db, &payload)
			case "abort":
				return AbortBackupJobOperationTxn(db, &payload)
			default:
				return ReleaseBackupJobOperationTxn(db, &payload)
			}
		default:
			return nil
		}
	})

	fsm.Register("backup_target_restore_operation", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "acquire":
			var payload BackupTargetRestoreOperationAcquire
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return AcquireBackupTargetRestoreOperationTxn(db, &payload)
		case "start", "finish", "requeue", "abort", "release":
			var payload BackupTargetRestoreOperationTransition
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			switch action {
			case "start":
				return StartBackupTargetRestoreOperationTxn(db, &payload)
			case "finish":
				return FinishBackupTargetRestoreOperationTxn(db, &payload)
			case "requeue":
				return RequeueBackupTargetRestoreOperationTxn(db, &payload)
			case "abort":
				return AbortBackupTargetRestoreOperationTxn(db, &payload)
			default:
				return ReleaseBackupTargetRestoreOperationTxn(db, &payload)
			}
		default:
			return nil
		}
	})

	fsm.Register("backup_job_state", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "update":
			var payload struct {
				JobID       uint       `json:"jobId"`
				LastRunAt   *time.Time `json:"lastRunAt"`
				LastStatus  string     `json:"lastStatus"`
				LastError   string     `json:"lastError"`
				NextRunAt   *time.Time `json:"nextRunAt"`
				Encrypted   bool       `json:"encrypted"`
				NextRunOnly bool       `json:"nextRunOnly"`
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			if payload.JobID == 0 {
				return fmt.Errorf("invalid_job_id")
			}
			if payload.NextRunOnly {
				return db.Model(&BackupJob{}).Where("id = ?", payload.JobID).Update("next_run_at", payload.NextRunAt).Error
			}

			status := strings.TrimSpace(strings.ToLower(payload.LastStatus))
			if status == "" {
				return fmt.Errorf("last_status_required")
			}
			if status != "success" && status != "failed" && status != "running" && status != "blocked" {
				return fmt.Errorf("invalid_last_status")
			}

			return db.Model(&BackupJob{}).Where("id = ?", payload.JobID).Updates(map[string]any{
				"last_run_at": payload.LastRunAt,
				"last_status": status,
				"last_error":  strings.TrimSpace(payload.LastError),
				"next_run_at": payload.NextRunAt,
				"encrypted":   payload.Encrypted,
			}).Error
		default:
			return nil
		}
	})

	fsm.Register("backup_job_friendly_source", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "update":
			var payload struct {
				JobIDs      []uint `json:"jobIds"`
				FriendlySrc string `json:"friendlySrc"`
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}

			payload.FriendlySrc = strings.TrimSpace(payload.FriendlySrc)
			if payload.FriendlySrc == "" {
				return fmt.Errorf("friendly_src_required")
			}

			if len(payload.JobIDs) == 0 {
				return nil
			}

			seen := make(map[uint]struct{}, len(payload.JobIDs))
			jobIDs := make([]uint, 0, len(payload.JobIDs))
			for _, id := range payload.JobIDs {
				if id == 0 {
					continue
				}
				if _, exists := seen[id]; exists {
					continue
				}
				seen[id] = struct{}{}
				jobIDs = append(jobIDs, id)
			}
			if len(jobIDs) == 0 {
				return nil
			}

			return db.Model(&BackupJob{}).Where("id IN ?", jobIDs).Update("friendly_src", payload.FriendlySrc).Error
		default:
			return nil
		}
	})

	fsm.Register("replication_policy", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "create", "update":
			var payload ReplicationPolicyPayload
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return db.Transaction(func(tx *gorm.DB) error {
				var err error
				if action == "create" {
					err = upsertReplicationPolicy(tx, &payload.Policy, payload.Targets)
				} else {
					err = updateReplicationPolicy(tx, &payload)
				}
				if err != nil {
					return err
				}
				if !payload.Policy.Enabled && tx.Migrator().HasTable(&ReplicationLease{}) {
					return tx.Where("policy_id = ?", payload.Policy.ID).Delete(&ReplicationLease{}).Error
				}
				return nil
			})
		case "delete":
			var payload struct {
				ID uint `json:"id"`
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			if payload.ID == 0 {
				return nil
			}
			return DeleteReplicationPolicyTxn(db, payload.ID)
		case "state_update":
			var payload struct {
				ID         uint       `json:"id"`
				LastRunAt  *time.Time `json:"lastRunAt"`
				LastStatus string     `json:"lastStatus"`
				LastError  string     `json:"lastError"`
				NextRunAt  *time.Time `json:"nextRunAt"`
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			if payload.ID == 0 {
				return nil
			}
			updates := map[string]any{
				"last_run_at": payload.LastRunAt,
				"last_status": payload.LastStatus,
				"last_error":  payload.LastError,
				"next_run_at": payload.NextRunAt,
			}
			return db.Model(&ReplicationPolicy{}).Where("id = ?", payload.ID).Updates(updates).Error
		default:
			return nil
		}
	})

	fsm.Register("replication_lease", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "upsert":
			var lease ReplicationLease
			if err := json.Unmarshal(raw, &lease); err != nil {
				return err
			}
			return upsertReplicationLease(db, &lease)
		case "upsert_batch":
			var leases []ReplicationLease
			if err := json.Unmarshal(raw, &leases); err != nil {
				return err
			}
			return db.Transaction(func(tx *gorm.DB) error {
				for i := range leases {
					if err := upsertReplicationLease(tx, &leases[i]); err != nil {
						return err
					}
				}
				return nil
			})
		case "delete":
			var payload struct {
				PolicyID uint `json:"policyId"`
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			if payload.PolicyID == 0 {
				return nil
			}
			return db.Where("policy_id = ?", payload.PolicyID).Delete(&ReplicationLease{}).Error
		default:
			return nil
		}
	})

	fsm.Register("replication_guest_operation", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "acquire":
			var payload ReplicationGuestOperationAcquire
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return acquireReplicationGuestOperation(db, &payload)
		case "seal":
			var payload ReplicationGuestOperationTransition
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return sealReplicationGuestOperation(db, &payload)
		case "abort":
			var payload ReplicationGuestOperationTransition
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return abortReplicationGuestOperation(db, &payload)
		case "complete":
			var payload ReplicationGuestOperationTransition
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return completeReplicationGuestOperation(db, &payload)
		default:
			return nil
		}
	})

	fsm.Register("replication_policy_transition", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "begin":
			var payload ReplicationPolicyTransitionBegin
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return beginReplicationPolicyTransition(db, &payload)
		case "update":
			var payload struct {
				PolicyID   uint                        `json:"policyId"`
				Transition ReplicationPolicyTransition `json:"transition"`
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return upsertReplicationPolicyTransition(db, payload.PolicyID, &payload.Transition)
		default:
			return nil
		}
	})

	fsm.Register("replication_ownership_transition", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "commit":
			var payload ReplicationOwnershipTransitionPayload
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return applyReplicationOwnershipTransition(db, &payload)
		case "reassign_disabled":
			var payload ReplicationDisabledOwnerReassignment
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return reassignDisabledReplicationPolicyOwner(db, &payload)
		default:
			return nil
		}
	})

	fsm.Register("replication_target_readiness", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "update":
			var payload ReplicationTargetReadinessUpdate
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return updateReplicationTargetReadiness(db, &payload)
		default:
			return nil
		}
	})

	fsm.Register("replication_policy_protection_state", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "update":
			var payload ReplicationPolicyProtectionStateUpdate
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			return updateReplicationPolicyProtectionState(db, &payload)
		default:
			return nil
		}
	})

	fsm.Register("cluster_ssh_identity", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "upsert":
			var identity ClusterSSHIdentity
			if err := json.Unmarshal(raw, &identity); err != nil {
				return err
			}
			return upsertClusterSSHIdentity(db, &identity)
		case "delete":
			var payload struct {
				NodeUUID string `json:"nodeUUID"`
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			payload.NodeUUID = strings.TrimSpace(payload.NodeUUID)
			if payload.NodeUUID == "" {
				return nil
			}
			return db.Where("node_uuid = ?", payload.NodeUUID).Delete(&ClusterSSHIdentity{}).Error
		default:
			return nil
		}
	})

	fsm.Register("encryption_key", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "upsert":
			var key EncryptionKey
			if err := json.Unmarshal(raw, &key); err != nil {
				return err
			}
			return upsertEncryptionKey(db, &key)
		case "delete":
			var payload struct {
				UUID string `json:"uuid"`
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			payload.UUID = strings.TrimSpace(payload.UUID)
			if payload.UUID == "" {
				return nil
			}
			return db.Where("uuid = ?", payload.UUID).Delete(&EncryptionKey{}).Error
		default:
			return nil
		}
	})

	fsm.Register("replication_event", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "create", "update":
			var event ReplicationEvent
			if err := json.Unmarshal(raw, &event); err != nil {
				return err
			}
			if event.ID == 0 {
				return fmt.Errorf("replication_event_id_required")
			}
			return db.Clauses(clause.OnConflict{
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
			}).Create(&event).Error
		default:
			return nil
		}
	})
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
