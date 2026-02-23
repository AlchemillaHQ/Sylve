// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/robfig/cron/v3"
)

var maxSafeJSInt = big.NewInt(9007199254740991)

// BackupJobInput represents the input for creating/updating a backup job.
type BackupJobInput struct {
	Name             string `json:"name"`
	TargetID         uint   `json:"targetId"`
	RunnerNodeID     string `json:"runnerNodeId"`
	Mode             string `json:"mode"`
	SourceDataset    string `json:"sourceDataset"`
	JailRootDataset  string `json:"jailRootDataset"`
	DestSuffix       string `json:"destSuffix"` // appended to target's BackupRoot
	PruneKeepLast    int    `json:"pruneKeepLast"`
	PruneTarget      bool   `json:"pruneTarget"`
	StopBeforeBackup bool   `json:"stopBeforeBackup"`
	CronExpr         string `json:"cronExpr"`
	Enabled          *bool  `json:"enabled"`
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

func (s *Service) ProposeBackupTargetCreate(input clusterServiceInterfaces.BackupTargetReq, bypassRaft bool) error {
	if err := validateBackupTargetInput(input); err != nil {
		return err
	}

	resolvedSSHKey := resolveSSHKeyMaterial(input.SSHKey, input.SSHKeyPath)

	target := clusterModels.BackupTarget{
		Name:        strings.TrimSpace(input.Name),
		SSHHost:     strings.TrimSpace(input.SSHHost),
		SSHPort:     input.SSHPort,
		SSHKeyPath:  strings.TrimSpace(input.SSHKeyPath),
		SSHKey:      resolvedSSHKey,
		BackupRoot:  strings.TrimSpace(input.BackupRoot),
		Description: strings.TrimSpace(input.Description),
		Enabled:     utils.PtrToBool(input.Enabled),
	}

	if target.SSHPort == 0 {
		target.SSHPort = 22
	}

	if bypassRaft {
		return s.DB.Create(&target).Error
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	id, err := s.newRaftObjectID("backup_targets")
	if err != nil {
		return fmt.Errorf("new_backup_target_id_failed: %w", err)
	}
	target.ID = id

	data, err := json.Marshal(clusterModels.BackupTargetToReplicationPayload(target))
	if err != nil {
		return fmt.Errorf("failed_to_marshal_backup_target_payload: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "backup_target",
		Action: "create",
		Data:   data,
	})
}

func (s *Service) ProposeBackupTargetUpdate(input clusterServiceInterfaces.BackupTargetReq, bypassRaft bool) error {
	if input.ID == 0 {
		return fmt.Errorf("invalid_target_id")
	}

	if err := validateBackupTargetInput(input); err != nil {
		return err
	}

	resolvedSSHKey := resolveSSHKeyMaterial(input.SSHKey, input.SSHKeyPath)

	target := clusterModels.BackupTarget{
		ID:          input.ID,
		Name:        strings.TrimSpace(input.Name),
		SSHHost:     strings.TrimSpace(input.SSHHost),
		SSHPort:     input.SSHPort,
		SSHKeyPath:  strings.TrimSpace(input.SSHKeyPath),
		SSHKey:      resolvedSSHKey,
		BackupRoot:  strings.TrimSpace(input.BackupRoot),
		Description: strings.TrimSpace(input.Description),
		Enabled:     utils.PtrToBool(input.Enabled),
	}

	if target.SSHPort == 0 {
		target.SSHPort = 22
	}

	if bypassRaft {
		return s.DB.Model(&clusterModels.BackupTarget{}).Where("id = ?", input.ID).Updates(map[string]any{
			"name":         target.Name,
			"ssh_host":     target.SSHHost,
			"ssh_port":     target.SSHPort,
			"ssh_key_path": target.SSHKeyPath,
			"ssh_key":      target.SSHKey,
			"backup_root":  target.BackupRoot,
			"description":  target.Description,
			"enabled":      target.Enabled,
		}).Error
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	data, err := json.Marshal(clusterModels.BackupTargetToReplicationPayload(target))
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
		var jobIDs []uint
		if err := s.DB.Model(&clusterModels.BackupJob{}).Where("target_id = ?", id).Pluck("id", &jobIDs).Error; err != nil {
			return err
		}
		if len(jobIDs) > 0 {
			if err := s.DB.Where("job_id IN ?", jobIDs).Delete(&clusterModels.BackupEvent{}).Error; err != nil {
				return err
			}
		}
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
	id := uint(0)
	var err error
	if !bypassRaft {
		id, err = s.newRaftObjectID("backup_jobs")
		if err != nil {
			return fmt.Errorf("new_backup_job_id_failed: %w", err)
		}
	}

	job, err := s.buildBackupJob(id, input)
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

	if bypassRaft {
		return s.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", id).Updates(map[string]any{
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
		if err := s.DB.Where("job_id = ?", id).Delete(&clusterModels.BackupEvent{}).Error; err != nil {
			return err
		}
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

	runnerNodeID := strings.TrimSpace(input.RunnerNodeID)
	if runnerNodeID == "" {
		if detail := s.Detail(); detail != nil {
			runnerNodeID = strings.TrimSpace(detail.NodeID)
		}
	}

	if runnerNodeID != "" {
		if !s.backupRunnerNodeExists(runnerNodeID) {
			return nil, fmt.Errorf("backup_runner_node_not_found")
		}
	}

	mode := strings.TrimSpace(strings.ToLower(input.Mode))
	if mode == "" {
		mode = clusterModels.BackupJobModeDataset
	}
	if mode != clusterModels.BackupJobModeDataset && mode != clusterModels.BackupJobModeJail {
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
	enabled := true

	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	if !enabled {
		next = time.Time{}
	}

	job := &clusterModels.BackupJob{
		ID:               id,
		Name:             strings.TrimSpace(input.Name),
		TargetID:         input.TargetID,
		RunnerNodeID:     runnerNodeID,
		Mode:             mode,
		SourceDataset:    strings.TrimSpace(input.SourceDataset),
		JailRootDataset:  strings.TrimSpace(input.JailRootDataset),
		FriendlySrc:      "",
		DestSuffix:       strings.TrimSpace(input.DestSuffix),
		PruneKeepLast:    input.PruneKeepLast,
		PruneTarget:      input.PruneTarget,
		StopBeforeBackup: input.StopBeforeBackup,
		CronExpr:         cronExpr,
		Enabled:          enabled,
	}

	if job.PruneKeepLast < 0 {
		return nil, fmt.Errorf("invalid_prune_keep_last")
	}

	if mode == clusterModels.BackupJobModeDataset {
		if job.SourceDataset == "" {
			return nil, fmt.Errorf("source_dataset_required")
		}
		job.JailRootDataset = ""
	}

	if mode == clusterModels.BackupJobModeJail {
		if job.JailRootDataset == "" {
			job.JailRootDataset = "zroot/sylve/jails"
		}
		job.SourceDataset = ""
	}

	job.FriendlySrc = s.resolveBackupJobFriendlySource(job.Mode, job.SourceDataset, job.JailRootDataset)

	if !next.IsZero() {
		job.NextRunAt = &next
	}

	return job, nil
}

func (s *Service) resolveBackupJobFriendlySource(mode, sourceDataset, jailRootDataset string) string {
	if mode == clusterModels.BackupJobModeDataset {
		return strings.TrimSpace(sourceDataset)
	}

	jailDataset := strings.TrimSpace(jailRootDataset)
	if jailDataset == "" {
		return ""
	}

	ctID, ok := parseJailCTIDFromDataset(jailDataset)
	if !ok {
		return jailDataset
	}

	var jail jailModels.Jail
	if err := s.DB.Select("name").Where("ct_id = ?", ctID).First(&jail).Error; err == nil {
		name := strings.TrimSpace(jail.Name)
		if name != "" {
			return name
		}
	} else {
		logger.L.Err(err).Msg("failed_to_lookup_jail_for_backup_job_friendly_source")
	}

	return jailDataset
}

func parseJailCTIDFromDataset(dataset string) (uint, bool) {
	dataset = strings.TrimSpace(dataset)
	if dataset == "" {
		return 0, false
	}

	idx := strings.LastIndex(dataset, "/")
	if idx < 0 || idx == len(dataset)-1 {
		return 0, false
	}

	ctIDRaw := strings.TrimSpace(dataset[idx+1:])
	ctID, err := strconv.ParseUint(ctIDRaw, 10, 64)
	if err != nil {
		return 0, false
	}

	return uint(ctID), true
}

func validateBackupTargetInput(input clusterServiceInterfaces.BackupTargetReq) error {
	if strings.TrimSpace(input.Name) == "" {
		return fmt.Errorf("name_required")
	}

	if strings.TrimSpace(input.SSHHost) == "" {
		return fmt.Errorf("ssh_host_required")
	}

	if strings.TrimSpace(input.BackupRoot) == "" {
		return fmt.Errorf("backup_root_required")
	}

	sshHost := strings.TrimSpace(input.SSHHost)
	if strings.Contains(sshHost, " ") || strings.Contains(sshHost, ":") {
		return fmt.Errorf("invalid_ssh_host: should be user@host or just hostname")
	}

	return nil
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

func (s *Service) backupRunnerNodeExists(nodeID string) bool {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return false
	}

	if detail := s.Detail(); detail != nil && strings.TrimSpace(detail.NodeID) == nodeID {
		return true
	}

	var count int64
	if err := s.DB.Model(&clusterModels.ClusterNode{}).Where("node_uuid = ?", nodeID).Count(&count).Error; err != nil {
		return false
	}

	return count > 0
}

func (s *Service) newRaftObjectID(table string) (uint, error) {
	for attempts := 0; attempts < 16; attempts++ {
		n, err := rand.Int(rand.Reader, maxSafeJSInt)
		if err != nil {
			return 0, err
		}

		id := uint(n.Uint64())
		if id == 0 {
			continue
		}

		var count int64
		if err := s.DB.Table(table).Where("id = ?", id).Count(&count).Error; err != nil {
			return 0, err
		}
		if count == 0 {
			return id, nil
		}
	}

	return 0, fmt.Errorf("unable_to_allocate_unique_id")
}

func resolveSSHKeyMaterial(sshKey, sshKeyPath string) string {
	trimmedKey := strings.TrimSpace(sshKey)
	if trimmedKey != "" {
		return trimmedKey
	}

	trimmedPath := strings.TrimSpace(sshKeyPath)
	if trimmedPath == "" {
		return ""
	}

	raw, err := os.ReadFile(trimmedPath)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(raw))
}
