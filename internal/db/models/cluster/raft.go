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
	Notes   []ClusterNote   `json:"notes"`
	Options []ClusterOption `json:"options"`
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

		sets := []restoreSet{
			{"cluster_notes", snap.Notes, 500},
			{"cluster_options", snap.Options, 100},
			// We can add more tables here as needed
		}

		for _, s := range sets {
			if err := tx.Exec("DELETE FROM " + s.table).Error; err != nil {
				return err
			}

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
}
