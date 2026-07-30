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
)

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
		return s.DB.Model(&clusterModels.ClusterNote{}).Where("id = ?", id).
			Updates(clusterModels.ClusterNote{
				Title:   title,
				Content: content,
			}).Error
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
		return s.DB.Delete(&clusterModels.ClusterNote{}, id).Error
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
		return s.DB.Delete(&clusterModels.ClusterNote{}, ids).Error
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
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
