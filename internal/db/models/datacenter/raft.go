package datacenterModels

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/hashicorp/raft"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Command struct {
	Type   string          `json:"type"`
	Action string          `json:"action"`
	Data   json.RawMessage `json:"data"`
}

type FSMDispatcher struct {
	DB       *gorm.DB
	handlers map[string]func(db *gorm.DB, action string, raw json.RawMessage) error
	mu       sync.RWMutex
}

func NewFSMDispatcher(db *gorm.DB) *FSMDispatcher {
	return &FSMDispatcher{
		DB:       db,
		handlers: make(map[string]func(db *gorm.DB, action string, raw json.RawMessage) error),
	}
}

func (f *FSMDispatcher) Register(t string, fn func(db *gorm.DB, action string, raw json.RawMessage) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[t] = fn
}

func (f *FSMDispatcher) Apply(l *raft.Log) interface{} {
	if l.Type != raft.LogCommand {
		return nil // ignore internal Raft housekeeping logs
	}

	var cmd Command
	if err := json.Unmarshal(l.Data, &cmd); err != nil {
		fmt.Printf("[FSM] Failed to unmarshal command: %v\n", err)
		return nil
	}

	f.mu.RLock()
	handler, ok := f.handlers[cmd.Type]
	f.mu.RUnlock()
	if !ok {
		fmt.Printf("[FSM] No handler for type=%s\n", cmd.Type)
		return nil
	}

	if err := handler(f.DB, cmd.Action, cmd.Data); err != nil {
		fmt.Printf("[FSM] Handler error (type=%s, action=%s): %v\n", cmd.Type, cmd.Action, err)
		// DO NOT return the error to Raft, just log it
		return nil
	}

	return nil
}

func (f *FSMDispatcher) Snapshot() (raft.FSMSnapshot, error) {
	var notes []DataCenterNote
	f.DB.Find(&notes)

	var opts []DataCenterOptions
	f.DB.Find(&opts)

	return &GenericSnapshot{
		Notes:   notes,
		Options: opts,
	}, nil
}

func (f *FSMDispatcher) Restore(rc io.ReadCloser) error {
	var snap GenericSnapshot
	if err := json.NewDecoder(rc).Decode(&snap); err != nil {
		return err
	}

	f.DB.Exec("DELETE FROM data_center_notes")
	for _, n := range snap.Notes {
		f.DB.Create(&n)
	}

	f.DB.Exec("DELETE FROM data_center_options")
	for _, o := range snap.Options {
		f.DB.Create(&o)
	}

	return nil
}

type GenericSnapshot struct {
	Notes   []DataCenterNote    `json:"notes"`
	Options []DataCenterOptions `json:"options"`
}

func (s *GenericSnapshot) Persist(sink raft.SnapshotSink) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if _, err := sink.Write(data); err != nil {
		return err
	}
	return sink.Close()
}

func (s *GenericSnapshot) Release() {}

func RegisterDefaultHandlers(fsm *FSMDispatcher) {
	// Notes handler
	fsm.Register("note", func(db *gorm.DB, action string, raw json.RawMessage) error {
		var note DataCenterNote
		if err := json.Unmarshal(raw, &note); err != nil {
			return err
		}

		switch action {
		case "create":
			// Upsert (create if not exists, update if conflict)
			return db.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "id"}},
				DoUpdates: clause.AssignmentColumns([]string{"title", "content", "created_at", "updated_at"}),
			}).Create(&note).Error

		case "update":
			return db.Model(&DataCenterNote{}).
				Where("id = ?", note.ID).
				Updates(map[string]any{
					"title":      note.Title,
					"content":    note.Content,
					"updated_at": note.UpdatedAt,
				}).Error

		case "delete":
			return db.Delete(&DataCenterNote{}, note.ID).Error
		}

		return nil
	})

	// Options handler
	fsm.Register("options", func(db *gorm.DB, action string, raw json.RawMessage) error {
		var opt DataCenterOptions
		if err := json.Unmarshal(raw, &opt); err != nil {
			return err
		}

		opt.ID = 1 // enforce singleton row

		switch action {
		case "set":
			// Try to update, insert if missing
			var existing DataCenterOptions
			if err := db.First(&existing, 1).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return db.Create(&opt).Error
			}
			return db.Model(&existing).Updates(map[string]any{
				"keyboard_layout": opt.KeyboardLayout,
				"updated_at":      opt.UpdatedAt,
			}).Error
		}

		return nil
	})
}
