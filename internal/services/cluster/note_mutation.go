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
	"fmt"
	"unicode/utf8"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

type NoteMutation struct {
	Action  string `json:"action"`
	ID      int    `json:"id,omitempty"`
	Title   string `json:"title,omitempty"`
	Content string `json:"content,omitempty"`
}

func validateNoteMutation(request NoteMutation) error {
	switch request.Action {
	case "create":
	case "update":
		if request.ID <= 0 {
			return fmt.Errorf("note_id_required")
		}
	case "delete":
		if request.ID <= 0 {
			return fmt.Errorf("note_id_required")
		}
		return nil
	default:
		return fmt.Errorf("note_action_invalid")
	}
	titleLength := utf8.RuneCountInString(request.Title)
	if titleLength < 3 || titleLength > 128 {
		return fmt.Errorf("note_title_invalid")
	}
	if utf8.RuneCountInString(request.Content) < 3 {
		return fmt.Errorf("note_content_invalid")
	}
	return nil
}

func (s *Service) GetNote(id int) (clusterModels.ClusterNote, error) {
	var note clusterModels.ClusterNote
	if id <= 0 {
		return note, fmt.Errorf("note_id_required")
	}
	return note, s.DB.First(&note, id).Error
}

func (s *Service) ApplyNoteMutation(ctx context.Context, request NoteMutation) error {
	if err := validateNoteMutation(request); err != nil {
		return err
	}
	switch request.Action {
	case "create":
		return s.ProposeNoteCreate(ctx, request.Title, request.Content)
	case "update":
		return s.ProposeNoteUpdate(ctx, request.ID, request.Title, request.Content)
	default:
		return s.ProposeNoteDelete(ctx, request.ID)
	}
}
