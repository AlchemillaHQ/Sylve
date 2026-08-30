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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

const noteMutationTimeout = 30 * time.Second

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

func (s *Service) ApplyNoteMutation(ctx context.Context, request NoteMutation, forward bool) error {
	if err := validateNoteMutation(request); err != nil {
		return err
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("cluster_service_not_initialized")
	}
	if s.Raft == nil {
		return s.applyNoteMutation(request, true)
	}
	if s.Raft.State() == raft.Leader {
		return s.applyNoteMutation(request, false)
	}
	if !forward {
		return raft.ErrNotLeader
	}
	leaderAddress := strings.TrimSpace(string(s.Raft.Leader()))
	if leaderAddress == "" {
		return fmt.Errorf("cluster_consensus_unavailable")
	}
	return s.forwardNoteMutation(ctx, raftAddressHost(leaderAddress), request)
}

func (s *Service) applyNoteMutation(request NoteMutation, bypassRaft bool) error {
	switch request.Action {
	case "create":
		return s.ProposeNoteCreate(request.Title, request.Content, bypassRaft)
	case "update":
		return s.ProposeNoteUpdate(request.ID, request.Title, request.Content, bypassRaft)
	case "delete":
		return s.ProposeNoteDelete(request.ID, bypassRaft)
	default:
		return fmt.Errorf("note_action_invalid")
	}
}

func (s *Service) forwardNoteMutation(ctx context.Context, leaderIP string, request NoteMutation) error {
	if ctx == nil {
		ctx = context.Background()
	}
	token, err := s.AuthService.CreateInternalClusterJWT(s.LocalNodeID())
	if err != nil {
		return fmt.Errorf("note_forward_token_failed: %w", err)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	response, err := utils.HTTPRequestReadContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("https://%s/api/intra-cluster/note-mutation", ClusterAPIHost(leaderIP)),
		payload,
		map[string]string{
			"Accept":          "application/json",
			"Content-Type":    "application/json",
			"X-Cluster-Token": "Bearer " + token,
		},
		noteMutationTimeout,
		1<<20,
	)
	if err != nil {
		return fmt.Errorf("note_forward_failed: %w", err)
	}
	var apiResponse internal.APIResponse[any]
	if err := json.Unmarshal(response.Body, &apiResponse); err != nil {
		return fmt.Errorf("note_forward_response_invalid: %w", err)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 && apiResponse.Status == "success" {
		return nil
	}
	switch apiResponse.Message {
	case "note_not_found":
		return gorm.ErrRecordNotFound
	case "cluster_leadership_changed":
		return raft.ErrLeadershipLost
	case "cluster_consensus_unavailable":
		return fmt.Errorf("cluster_consensus_unavailable")
	}
	if strings.TrimSpace(apiResponse.Error) != "" {
		return errors.New(apiResponse.Error)
	}
	return fmt.Errorf("note_forward_rejected")
}
