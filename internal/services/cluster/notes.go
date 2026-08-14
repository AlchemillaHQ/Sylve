// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"encoding/json"
	"fmt"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"gorm.io/gorm"
)

func requireAffectedNoteRows(result *gorm.DB, expected int64) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != expected {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func requireExactNoteIDs(db *gorm.DB, ids []int) error {
	var count int64
	if err := db.Model(&clusterModels.ClusterNote{}).Where("id IN ?", ids).Count(&count).Error; err != nil {
		return err
	}
	if count != int64(len(ids)) {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func deleteNotesExact(db *gorm.DB, ids []int) error {
	return db.Transaction(func(tx *gorm.DB) error {
		return requireAffectedNoteRows(
			tx.Delete(&clusterModels.ClusterNote{}, ids),
			int64(len(ids)),
		)
	})
}

func (s *Service) ListNotes() ([]clusterModels.ClusterNote, error) {
	var notes []clusterModels.ClusterNote
	err := s.DB.Order("id ASC").Find(&notes).Error
	return notes, err
}

func (s *Service) ProposeNoteCreate(title, content string, bypassRaft bool) error {
	if bypassRaft {
		note := clusterModels.ClusterNote{
			Title:   title,
			Content: content,
		}

		return s.DB.Create(&note).Error
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	payloadStruct := struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}{
		Title:   title,
		Content: content,
	}

	data, err := json.Marshal(payloadStruct)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_note_payload: %w", err)
	}

	cmd := clusterModels.Command{
		Type:   "note",
		Action: "create",
		Data:   data,
	}

	return s.applyRaftCommand(cmd)
}

func (s *Service) ProposeNoteUpdate(id int, title, content string, bypassRaft bool) error {
	if bypassRaft {
		return requireAffectedNoteRows(
			s.DB.Model(&clusterModels.ClusterNote{}).Where("id = ?", id).
				Updates(clusterModels.ClusterNote{
					Title:   title,
					Content: content,
				}),
			1,
		)
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	payloadStruct := struct {
		ID      int    `json:"id"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}{
		ID:      id,
		Title:   title,
		Content: content,
	}

	data, err := json.Marshal(payloadStruct)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_note_payload: %w", err)
	}

	cmd := clusterModels.Command{
		Type:   "note",
		Action: "update",
		Data:   data,
	}

	return s.applyRaftCommand(cmd)
}

func (s *Service) ProposeNoteDelete(id int, bypassRaft bool) error {
	if bypassRaft {
		return requireAffectedNoteRows(s.DB.Delete(&clusterModels.ClusterNote{}, id), 1)
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	payloadStruct := struct {
		ID int `json:"id"`
	}{ID: id}

	data, err := json.Marshal(payloadStruct)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_delete_payload: %w", err)
	}

	cmd := clusterModels.Command{
		Type:   "note",
		Action: "delete",
		Data:   data,
	}

	return s.applyRaftCommand(cmd)
}

func (s *Service) ProposeNoteBulkDelete(ids []int, bypassRaft bool) error {
	if bypassRaft {
		return deleteNotesExact(s.DB, ids)
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}
	if err := requireExactNoteIDs(s.DB, ids); err != nil {
		return err
	}

	payloadStruct := struct {
		IDs []int `json:"ids"`
	}{IDs: ids}

	data, err := json.Marshal(payloadStruct)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_bulk_delete_payload: %w", err)
	}

	cmd := clusterModels.Command{
		Type:   "note",
		Action: "bulk_delete",
		Data:   data,
	}

	return s.applyRaftCommand(cmd)
}
