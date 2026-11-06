// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func (s *Service) ListNotes() ([]clusterModels.ClusterNote, error) {
	var notes []clusterModels.ClusterNote
	err := s.DB.Order("id ASC").Find(&notes).Error
	return notes, err
}

func (s *Service) ProposeNoteCreate(ctx context.Context, title, content string) error {
	return s.Propose(ctx, "note", "create", struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}{Title: title, Content: content})
}

func (s *Service) ProposeNoteUpdate(ctx context.Context, id int, title, content string) error {
	return s.Propose(ctx, "note", "update", struct {
		ID      int    `json:"id"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}{ID: id, Title: title, Content: content})
}

func (s *Service) ProposeNoteDelete(ctx context.Context, id int) error {
	return s.Propose(ctx, "note", "delete", struct {
		ID int `json:"id"`
	}{ID: id})
}

func (s *Service) ProposeNoteBulkDelete(ctx context.Context, ids []int) error {
	return s.Propose(ctx, "note", "bulk_delete", struct {
		IDs []int `json:"ids"`
	}{IDs: ids})
}
