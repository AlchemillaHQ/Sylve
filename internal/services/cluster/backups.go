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
	"net"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/robfig/cron/v3"
)

type BackupTargetInput struct {
	Name        string `json:"name"`
	Endpoint    string `json:"endpoint"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type BackupJobInput struct {
	Name               string `json:"name"`
	TargetID           uint   `json:"targetId"`
	Mode               string `json:"mode"`
	SourceDataset      string `json:"sourceDataset"`
	JailRootDataset    string `json:"jailRootDataset"`
	DestinationDataset string `json:"destinationDataset"`
	CronExpr           string `json:"cronExpr"`
	Force              bool   `json:"force"`
	WithIntermediates  bool   `json:"withIntermediates"`
	Enabled            *bool  `json:"enabled"`
}

func (s *Service) ListBackupTargets() ([]clusterModels.BackupTarget, error) {
	var targets []clusterModels.BackupTarget
	err := s.DB.Order("name ASC").Find(&targets).Error
	return targets, err
}

func (s *Service) GetBackupTargetByID(id uint) (*clusterModels.BackupTarget, error) {
	if id == 0 {
		return nil, fmt.Errorf("invalid_target_id")
	}

	var target clusterModels.BackupTarget
	if err := s.DB.First(&target, id).Error; err != nil {
		return nil, err
	}
	return &target, nil
}

func (s *Service) ProposeBackupTargetCreate(input BackupTargetInput, bypassRaft bool) error {
	if err := validateBackupTargetInput(input); err != nil {
		return err
	}

	target := clusterModels.BackupTarget{
		Name:        strings.TrimSpace(input.Name),
		Endpoint:    normalizeEndpoint(input.Endpoint),
		Description: strings.TrimSpace(input.Description),
		Enabled:     input.Enabled,
	}

	if bypassRaft {
		return s.DB.Create(&target).Error
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	data, err := json.Marshal(target)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_backup_target_payload: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "backup_target",
		Action: "create",
		Data:   data,
	})
}

func (s *Service) ProposeBackupTargetUpdate(id uint, input BackupTargetInput, bypassRaft bool) error {
	if id == 0 {
		return fmt.Errorf("invalid_target_id")
	}
	if err := validateBackupTargetInput(input); err != nil {
		return err
	}

	target := clusterModels.BackupTarget{
		ID:          id,
		Name:        strings.TrimSpace(input.Name),
		Endpoint:    normalizeEndpoint(input.Endpoint),
		Description: strings.TrimSpace(input.Description),
		Enabled:     input.Enabled,
	}

	if bypassRaft {
		return s.DB.Model(&clusterModels.BackupTarget{}).Where("id = ?", id).Updates(map[string]any{
			"name":        target.Name,
			"endpoint":    target.Endpoint,
			"description": target.Description,
			"enabled":     target.Enabled,
		}).Error
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	data, err := json.Marshal(target)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_backup_target_payload: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "backup_target",
		Action: "update",
		Data:   data,
	})
}

func (s *Service) ProposeBackupTargetDelete(id uint, bypassRaft bool) error {
	if id == 0 {
		return fmt.Errorf("invalid_target_id")
	}

	if bypassRaft {
		if err := s.DB.Delete(&clusterModels.BackupJob{}, "target_id = ?", id).Error; err != nil {
			return err
		}
		return s.DB.Delete(&clusterModels.BackupTarget{}, id).Error
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	data, err := json.Marshal(struct {
		ID uint `json:"id"`
	}{ID: id})
	if err != nil {
		return fmt.Errorf("failed_to_marshal_backup_target_delete_payload: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "backup_target",
		Action: "delete",
		Data:   data,
	})
}

func (s *Service) ListBackupJobs() ([]clusterModels.BackupJob, error) {
	var jobs []clusterModels.BackupJob
	err := s.DB.
		Preload("Target").
		Order("target_id ASC").
		Order("name ASC").
		Find(&jobs).Error
	return jobs, err
}

func (s *Service) GetBackupJobByID(id uint) (*clusterModels.BackupJob, error) {
	if id == 0 {
		return nil, fmt.Errorf("invalid_job_id")
	}

	var job clusterModels.BackupJob
	if err := s.DB.Preload("Target").First(&job, id).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *Service) ProposeBackupJobCreate(input BackupJobInput, bypassRaft bool) error {
	job, err := s.buildBackupJob(0, input)
	if err != nil {
		return err
	}

	if bypassRaft {
		return s.DB.Create(job).Error
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_backup_job_payload: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "backup_job",
		Action: "create",
		Data:   data,
	})
}

func (s *Service) ProposeBackupJobUpdate(id uint, input BackupJobInput, bypassRaft bool) error {
	if id == 0 {
		return fmt.Errorf("invalid_job_id")
	}

	job, err := s.buildBackupJob(id, input)
	if err != nil {
		return err
	}

	enabled := false
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	if bypassRaft {
		return s.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", id).Updates(map[string]any{
			"name":                job.Name,
			"target_id":           job.TargetID,
			"mode":                job.Mode,
			"source_dataset":      job.SourceDataset,
			"jail_root_dataset":   job.JailRootDataset,
			"destination_dataset": job.DestinationDataset,
			"cron_expr":           job.CronExpr,
			"force":               job.Force,
			"with_intermediates":  job.WithIntermediates,
			"enabled":             enabled,
			"next_run_at":         job.NextRunAt,
		}).Error
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_backup_job_payload: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "backup_job",
		Action: "update",
		Data:   data,
	})
}

func (s *Service) ProposeBackupJobDelete(id uint, bypassRaft bool) error {
	if id == 0 {
		return fmt.Errorf("invalid_job_id")
	}

	if bypassRaft {
		return s.DB.Delete(&clusterModels.BackupJob{}, id).Error
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	data, err := json.Marshal(struct {
		ID uint `json:"id"`
	}{ID: id})
	if err != nil {
		return fmt.Errorf("failed_to_marshal_backup_job_delete_payload: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "backup_job",
		Action: "delete",
		Data:   data,
	})
}

func (s *Service) buildBackupJob(id uint, input BackupJobInput) (*clusterModels.BackupJob, error) {
	if input.TargetID == 0 {
		return nil, fmt.Errorf("target_id_required")
	}

	var target clusterModels.BackupTarget
	if err := s.DB.First(&target, input.TargetID).Error; err != nil {
		return nil, fmt.Errorf("backup_target_not_found")
	}

	if strings.TrimSpace(input.Name) == "" {
		return nil, fmt.Errorf("name_required")
	}

	if strings.TrimSpace(input.DestinationDataset) == "" {
		return nil, fmt.Errorf("destination_dataset_required")
	}

	mode := strings.TrimSpace(strings.ToLower(input.Mode))
	if mode == "" {
		mode = clusterModels.BackupJobModeDataset
	}
	if mode != clusterModels.BackupJobModeDataset && mode != clusterModels.BackupJobModeJails {
		return nil, fmt.Errorf("invalid_mode")
	}

	cronExpr := strings.TrimSpace(input.CronExpr)
	if cronExpr == "" {
		return nil, fmt.Errorf("cron_expr_required")
	}

	schedule, err := cron.ParseStandard(cronExpr)
	if err != nil {
		return nil, fmt.Errorf("invalid_cron_expr")
	}

	now := time.Now().UTC()
	next := schedule.Next(now)
	enabled := false

	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	if !enabled {
		next = time.Time{}
	}

	job := &clusterModels.BackupJob{
		ID:                 id,
		Name:               strings.TrimSpace(input.Name),
		TargetID:           input.TargetID,
		Mode:               mode,
		SourceDataset:      strings.TrimSpace(input.SourceDataset),
		JailRootDataset:    strings.TrimSpace(input.JailRootDataset),
		DestinationDataset: strings.TrimSpace(input.DestinationDataset),
		CronExpr:           cronExpr,
		Force:              input.Force,
		WithIntermediates:  input.WithIntermediates,
		Enabled:            enabled,
	}

	if mode == clusterModels.BackupJobModeDataset {
		if job.SourceDataset == "" {
			return nil, fmt.Errorf("source_dataset_required")
		}
		job.JailRootDataset = ""
	}

	if mode == clusterModels.BackupJobModeJails {
		if job.JailRootDataset == "" {
			job.JailRootDataset = "zroot/sylve/jails"
		}
		job.SourceDataset = ""
	}

	if !next.IsZero() {
		job.NextRunAt = &next
	}

	return job, nil
}

func validateBackupTargetInput(input BackupTargetInput) error {
	if strings.TrimSpace(input.Name) == "" {
		return fmt.Errorf("name_required")
	}

	endpoint := normalizeEndpoint(input.Endpoint)
	if endpoint == "" {
		return fmt.Errorf("endpoint_required")
	}

	if _, _, err := net.SplitHostPort(endpoint); err != nil {
		return fmt.Errorf("invalid_endpoint")
	}

	return nil
}

func normalizeEndpoint(endpoint string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://"))
}

func (s *Service) applyRaftCommand(cmd clusterModels.Command) error {
	payload, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_command: %w", err)
	}

	applyFuture := s.Raft.Apply(payload, 5*time.Second)
	if err := applyFuture.Error(); err != nil {
		return fmt.Errorf("raft_apply_failed: %w", err)
	}

	if resp, ok := applyFuture.Response().(error); ok && resp != nil {
		return fmt.Errorf("fsm_apply_failed: %w", resp)
	}

	return nil
}
