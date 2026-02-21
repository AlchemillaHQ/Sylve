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
	"sync"

	"gorm.io/gorm"

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
	Notes         []ClusterNote                    `json:"notes"`
	Options       []ClusterOption                  `json:"options"`
	BackupTargets []BackupTargetReplicationPayload `json:"backupTargets"`
	BackupJobs    []BackupJob                      `json:"backupJobs"`
	BackupEvents  []BackupEvent                    `json:"backupEvents"`
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
	if err := f.DB.Order("id ASC").Find(&snap.BackupEvents).Error; err != nil {
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

		deleteSets := []restoreSet{
			{"backup_events", snap.BackupEvents, 1000},
			{"backup_jobs", snap.BackupJobs, 500},
			{"backup_targets", backupTargets, 200},
			{"cluster_notes", snap.Notes, 500},
			{"cluster_options", snap.Options, 100},
		}

		createSets := []restoreSet{
			{"backup_targets", backupTargets, 200},
			{"backup_jobs", snap.BackupJobs, 500},
			{"backup_events", snap.BackupEvents, 1000},
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
			// Use Updates with map to properly handle boolean false values
			return db.Model(&BackupTarget{}).Where("id = ?", target.ID).Updates(map[string]any{
				"name":         target.Name,
				"ssh_host":     target.SSHHost,
				"ssh_port":     target.SSHPort,
				"ssh_key_path": target.SSHKeyPath,
				"ssh_key":      target.SSHKey,
				"backup_root":  target.BackupRoot,
				"description":  target.Description,
				"enabled":      target.Enabled,
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

			var jobIDs []uint
			if err := db.Model(&BackupJob{}).Where("target_id = ?", payload.ID).Pluck("id", &jobIDs).Error; err != nil {
				return err
			}
			if len(jobIDs) > 0 {
				if err := db.Where("job_id IN ?", jobIDs).Delete(&BackupEvent{}).Error; err != nil {
					return err
				}
			}

			if err := db.Delete(&BackupJob{}, "target_id = ?", payload.ID).Error; err != nil {
				return err
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
				"name":              job.Name,
				"target_id":         job.TargetID,
				"runner_node_id":    job.RunnerNodeID,
				"mode":              job.Mode,
				"source_dataset":    job.SourceDataset,
				"jail_root_dataset": job.JailRootDataset,
				"friendly_src":      job.FriendlySrc,
				"dest_suffix":       job.DestSuffix,
				"prune_keep_last":   job.PruneKeepLast,
				"prune_target":      job.PruneTarget,
				"cron_expr":         job.CronExpr,
				"enabled":           job.Enabled,
				"next_run_at":       job.NextRunAt,
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
			if err := db.Where("job_id = ?", payload.ID).Delete(&BackupEvent{}).Error; err != nil {
				return err
			}
			return db.Delete(&BackupJob{}, payload.ID).Error
		default:
			return nil
		}
	})
}

func validBackupJobMode(mode string) bool {
	return mode == BackupJobModeDataset || mode == BackupJobModeJail
}
