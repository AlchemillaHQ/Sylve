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

// ClusterSnapshot represents the state that will be snapshotted/restored
type ClusterSnapshot struct {
	Notes               []ClusterNote                    `json:"notes"`
	Options             []ClusterOption                  `json:"options"`
	BackupTargets       []BackupTargetReplicationPayload `json:"backupTargets"`
	BackupJobs          []BackupJob                      `json:"backupJobs"`
	ReplicationPolicies []ReplicationPolicyPayload       `json:"replicationPolicies"`
	ReplicationLeases   []ReplicationLease               `json:"replicationLeases"`
	ReplicationEvents   []ReplicationEvent               `json:"replicationEvents"`
	SSHIdentities       []ClusterSSHIdentity             `json:"sshIdentities"`
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
	if err := f.DB.Order("id ASC").Find(&snap.BackupJobs).Error; err != nil {
		return nil, err
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
	if err := f.DB.Order("id ASC").Find(&snap.ReplicationEvents).Error; err != nil {
		return nil, err
	}
	if err := f.DB.Order("id ASC").Find(&snap.SSHIdentities).Error; err != nil {
		return nil, err
	}
	return &snap, nil
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
		replicationPolicies := make([]ReplicationPolicy, 0, len(snap.ReplicationPolicies))
		replicationTargets := make([]ReplicationPolicyTarget, 0)
		for _, payload := range snap.ReplicationPolicies {
			replicationPolicies = append(replicationPolicies, payload.Policy)
			for _, t := range payload.Targets {
				t.PolicyID = payload.Policy.ID
				replicationTargets = append(replicationTargets, t)
			}
		}

		deleteSets := []restoreSet{
			{"replication_events", snap.ReplicationEvents, 500},
			{"replication_leases", snap.ReplicationLeases, 500},
			{"replication_policy_targets", replicationTargets, 500},
			{"replication_policies", replicationPolicies, 500},
			{"cluster_ssh_identities", snap.SSHIdentities, 200},
			{"backup_jobs", snap.BackupJobs, 500},
			{"backup_targets", backupTargets, 200},
			{"cluster_notes", snap.Notes, 500},
			{"cluster_options", snap.Options, 100},
		}

		createSets := []restoreSet{
			{"cluster_ssh_identities", snap.SSHIdentities, 200},
			{"replication_policies", replicationPolicies, 500},
			{"replication_policy_targets", replicationTargets, 500},
			{"replication_leases", snap.ReplicationLeases, 500},
			{"replication_events", snap.ReplicationEvents, 500},
			{"backup_targets", backupTargets, 200},
			{"backup_jobs", snap.BackupJobs, 500},
			{"cluster_notes", snap.Notes, 500},
			{"cluster_options", snap.Options, 100},
		}

		for _, s := range deleteSets {
			if err := tx.Exec("DELETE FROM " + s.table).Error; err != nil {
				return err
			}
		}

		for _, s := range createSets {
			val := reflect.ValueOf(s.data)
			if val.Kind() == reflect.Slice && val.Len() > 0 {
				if err := tx.CreateInBatches(s.data, s.batch).Error; err != nil {
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

			var jobCount int64
			if err := db.Model(&BackupJob{}).Where("target_id = ?", payload.ID).Count(&jobCount).Error; err != nil {
				return err
			}
			if jobCount > 0 {
				return fmt.Errorf("target_in_use_by_backup_jobs: %d", jobCount)
			}

			return db.Delete(&BackupTarget{}, payload.ID).Error
		default:
			return nil
		}
	})

	fsm.Register("backup_job", func(db *gorm.DB, action string, raw json.RawMessage) error {
		switch action {
		case "create":
			var job BackupJob
			if err := json.Unmarshal(raw, &job); err != nil {
				return err
			}
			if job.Mode == "" {
				job.Mode = BackupJobModeDataset
			}
			if !validBackupJobMode(job.Mode) {
				return fmt.Errorf("invalid_backup_job_mode")
			}
			return upsertBackupJob(db, &job)
		case "update":
			var job BackupJob
			if err := json.Unmarshal(raw, &job); err != nil {
				return err
			}
			if job.Mode == "" {
				job.Mode = BackupJobModeDataset
			}
			if !validBackupJobMode(job.Mode) {
				return fmt.Errorf("invalid_backup_job_mode")
			}
			// Use Updates with map to properly handle boolean false values
			return db.Model(&BackupJob{}).Where("id = ?", job.ID).Updates(map[string]any{
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
				"cron_expr":          job.CronExpr,
				"enabled":            job.Enabled,
				"next_run_at":        job.NextRunAt,
			}).Error
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
			var runningCount int64
			if err := db.Model(&BackupEvent{}).
				Where("job_id = ? AND status = ?", payload.ID, "running").
				Count(&runningCount).Error; err != nil {
				return err
			}
			if runningCount > 0 {
				return fmt.Errorf("backup_job_running")
			}
			if err := db.Where("job_id = ?", payload.ID).Delete(&BackupEvent{}).Error; err != nil {
				return err
			}
			return db.Delete(&BackupJob{}, payload.ID).Error
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
			return upsertReplicationPolicy(db, &payload.Policy, payload.Targets)
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
			return db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Where("policy_id = ?", payload.ID).Delete(&ReplicationPolicyTarget{}).Error; err != nil {
					return err
				}
				if err := tx.Where("policy_id = ?", payload.ID).Delete(&ReplicationLease{}).Error; err != nil {
					return err
				}
				return tx.Delete(&ReplicationPolicy{}, payload.ID).Error
			})
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
