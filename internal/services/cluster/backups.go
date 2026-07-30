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
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

var maxSafeJSInt = big.NewInt(9007199254740991)
var maxBackupJobIDRange = big.NewInt(1000000000)

func boolPtrDefaultTrue(v *bool) bool {
	if v == nil {
		return true
	}

	return *v
}

// BackupJobInput represents the input for creating/updating a backup job.
type BackupJobInput struct {
	Name             string `json:"name"`
	TargetID         uint   `json:"targetId"`
	RunnerNodeID     string `json:"runnerNodeId"`
	Mode             string `json:"mode"`
	SourceDataset    string `json:"sourceDataset"`
	JailRootDataset  string `json:"jailRootDataset"`
	PruneKeepLast    int    `json:"pruneKeepLast"`
	PruneTarget      bool   `json:"pruneTarget"`
	StopBeforeBackup bool   `json:"stopBeforeBackup"`
	Recursive        bool   `json:"recursive"`
	CronExpr         string `json:"cronExpr"`
	Enabled          *bool  `json:"enabled"`
}

// BackupJobRuntimeStateUpdate carries runtime-only fields that should be
// synchronized cluster-wide after a backup run finishes.
type BackupJobRuntimeStateUpdate struct {
	JobID       uint       `json:"jobId"`
	LastRunAt   *time.Time `json:"lastRunAt"`
	LastStatus  string     `json:"lastStatus"`
	LastError   string     `json:"lastError"`
	NextRunAt   *time.Time `json:"nextRunAt"`
	Encrypted   bool       `json:"encrypted"`
	NextRunOnly bool       `json:"nextRunOnly,omitempty"`
}

type BackupJobFriendlySourceUpdate struct {
	GuestType   string `json:"guestType"`
	GuestID     uint   `json:"guestId"`
	FriendlySrc string `json:"friendlySrc"`
}

type backupJobFriendlySourceCommandPayload struct {
	JobIDs      []uint `json:"jobIds"`
	FriendlySrc string `json:"friendlySrc"`
}

func (s *Service) ListBackupTargets() ([]clusterModels.BackupTarget, error) {
	if err := s.requireBackupTargetReadinessBarrier(); err != nil {
		return nil, err
	}
	var targets []clusterModels.BackupTarget
	if err := s.DB.Order("name ASC").Find(&targets).Error; err != nil {
		return nil, err
	}
	if err := s.attachBackupTargetReadiness(targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func (s *Service) GetBackupTargetByID(id uint) (*clusterModels.BackupTarget, error) {
	if id == 0 {
		return nil, fmt.Errorf("invalid_target_id")
	}
	if err := s.requireBackupTargetReadinessBarrier(); err != nil {
		return nil, err
	}

	var target clusterModels.BackupTarget
	if err := s.DB.First(&target, id).Error; err != nil {
		return nil, err
	}
	return &target, nil
}

func (s *Service) ProposeBackupTargetCreate(input clusterServiceInterfaces.BackupTargetReq, bypassRaft bool) error {
	resolvedSSHKey, err := resolveSSHKeyMaterial(input.SSHKey, input.SSHKeyPath)
	if err != nil {
		return err
	}
	input.SSHKey = resolvedSSHKey
	candidate, err := s.BuildBackupTargetCreateCandidate(input)
	if err != nil {
		return err
	}
	_, err = s.ProposeBackupTargetCreateCandidate(candidate, bypassRaft)
	return err
}

func (s *Service) ProposeBackupTargetUpdate(input clusterServiceInterfaces.BackupTargetReq, bypassRaft bool) error {
	if input.ID == 0 {
		return fmt.Errorf("invalid_target_id")
	}
	if !bypassRaft && s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}
	existing, err := s.GetBackupTargetByID(input.ID)
	if err != nil {
		return err
	}
	plan, err := s.BuildBackupTargetUpdatePlan(existing, input)
	if err != nil {
		return err
	}
	return s.ProposeBackupTargetUpdatePlan(plan, bypassRaft)
}

func (s *Service) ProposeBackupTargetDelete(id uint, bypassRaft bool) error {
	if id == 0 {
		return fmt.Errorf("invalid_target_id")
	}

	if bypassRaft {
		return clusterModels.DeleteBackupTargetTxn(s.DB, id)
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
		Action: "delete_v2",
		Data:   data,
	})
}

func (s *Service) ListBackupJobs(targetID uint) ([]clusterModels.BackupJob, error) {
	var jobs []clusterModels.BackupJob
	query := s.DB.
		Preload("Target").
		Order("target_id ASC").
		Order("name ASC")
	if targetID > 0 {
		query = query.Where("target_id = ?", targetID)
	}
	err := query.Find(&jobs).Error
	return jobs, err
}

// ListBackupJobsForGuest returns jobs that belong to one guest. Guest identity
// is derived from the canonical source dataset so this also works for jobs
// created before a dedicated guest filter was exposed by the API.
func (s *Service) ListBackupJobsForGuest(targetID uint, guestType string, guestID uint) ([]clusterModels.BackupJob, error) {
	guestType = strings.TrimSpace(strings.ToLower(guestType))
	if guestType != clusterModels.ReplicationGuestTypeVM {
		return nil, fmt.Errorf("invalid_guest_type")
	}
	if guestID == 0 {
		return nil, fmt.Errorf("guest_id_required")
	}

	jobs, err := s.ListBackupJobs(targetID)
	if err != nil {
		return nil, err
	}

	filtered := make([]clusterModels.BackupJob, 0)
	for _, job := range jobs {
		if job.Mode != clusterModels.BackupJobModeVM {
			continue
		}
		rid, ok := parseVMRIDFromDataset(job.SourceDataset)
		if ok && rid == guestID {
			filtered = append(filtered, job)
		}
	}

	return filtered, nil
}

// RunningJobIDsForTarget returns the IDs of jobs belonging to targetID that
// have a durable queued, running, or finishing operation. Backup events are
// node-local telemetry and must not determine cluster-visible active state.
//
// Raft-backed callers must read on the leader after a barrier so a lagging API
// ingress node cannot incorrectly report that no operation is active.
func (s *Service) RunningJobIDsForTarget(targetID uint) ([]uint, error) {
	if targetID == 0 {
		return nil, fmt.Errorf("invalid_target_id")
	}
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("backup_job_database_unavailable")
	}
	if s.Raft != nil {
		if s.Raft.State() != raft.Leader {
			return nil, fmt.Errorf("not_leader")
		}
		if err := s.Raft.Barrier(raftApplyTimeout).Error(); err != nil {
			return nil, fmt.Errorf("backup_job_operation_status_barrier_failed: %w", err)
		}
	}

	var jobIDs []uint
	err := s.DB.
		Table("backup_job_operations").
		Select("DISTINCT backup_job_operations.job_id").
		Joins("JOIN backup_jobs ON backup_jobs.id = backup_job_operations.job_id").
		Where("backup_jobs.target_id = ?", targetID).
		Order("backup_job_operations.job_id ASC").
		Pluck("backup_job_operations.job_id", &jobIDs).Error
	return jobIDs, err
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

func (s *Service) UpdateBackupJobRuntimeState(update BackupJobRuntimeStateUpdate, bypassRaft bool) error {
	if update.JobID == 0 {
		return fmt.Errorf("invalid_job_id")
	}
	if update.NextRunOnly {
		if bypassRaft {
			return s.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", update.JobID).Update("next_run_at", update.NextRunAt).Error
		}
		if s.Raft == nil {
			return fmt.Errorf("raft_not_initialized")
		}
		if s.Raft.State() != raft.Leader {
			return fmt.Errorf("not_leader")
		}

		data, err := json.Marshal(update)
		if err != nil {
			return fmt.Errorf("failed_to_marshal_backup_job_state_payload: %w", err)
		}
		return s.applyRaftCommand(clusterModels.Command{
			Type:   "backup_job_state",
			Action: "update",
			Data:   data,
		})
	}

	status := strings.TrimSpace(strings.ToLower(update.LastStatus))
	if status == "" {
		return fmt.Errorf("last_status_required")
	}
	if status != "success" && status != "failed" && status != "running" && status != "blocked" {
		return fmt.Errorf("invalid_last_status")
	}
	update.LastStatus = status

	if bypassRaft {
		return s.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", update.JobID).Updates(map[string]any{
			"last_run_at": update.LastRunAt,
			"last_status": update.LastStatus,
			"last_error":  strings.TrimSpace(update.LastError),
			"next_run_at": update.NextRunAt,
			"encrypted":   update.Encrypted,
		}).Error
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}
	if s.Raft.State() != raft.Leader {
		return fmt.Errorf("not_leader")
	}

	data, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_backup_job_state_payload: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "backup_job_state",
		Action: "update",
		Data:   data,
	})
}

func (s *Service) SyncBackupJobFriendlySourceByGuest(update BackupJobFriendlySourceUpdate, bypassRaft bool) error {
	update.GuestType = strings.TrimSpace(strings.ToLower(update.GuestType))
	update.FriendlySrc = strings.TrimSpace(update.FriendlySrc)
	if update.GuestType != clusterModels.ReplicationGuestTypeVM && update.GuestType != clusterModels.ReplicationGuestTypeJail {
		return fmt.Errorf("invalid_guest_type")
	}
	if update.GuestID == 0 {
		return fmt.Errorf("guest_id_required")
	}
	if update.FriendlySrc == "" {
		return fmt.Errorf("friendly_src_required")
	}

	jobIDs, err := s.matchBackupJobIDsForGuest(update.GuestType, update.GuestID)
	if err != nil {
		return err
	}
	if len(jobIDs) == 0 {
		return nil
	}

	if bypassRaft {
		return s.DB.Model(&clusterModels.BackupJob{}).
			Where("id IN ?", jobIDs).
			Update("friendly_src", update.FriendlySrc).Error
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}
	if s.Raft.State() != raft.Leader {
		return fmt.Errorf("not_leader")
	}

	data, err := json.Marshal(backupJobFriendlySourceCommandPayload{
		JobIDs:      jobIDs,
		FriendlySrc: update.FriendlySrc,
	})
	if err != nil {
		return fmt.Errorf("failed_to_marshal_backup_job_friendly_source_payload: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "backup_job_friendly_source",
		Action: "update",
		Data:   data,
	})
}

func (s *Service) SyncBackupJobFriendlySourceByGuestClusterWide(update BackupJobFriendlySourceUpdate) error {
	err := s.SyncBackupJobFriendlySourceByGuest(update, s.Raft == nil)
	if err == nil {
		return nil
	}

	if s.Raft != nil && strings.Contains(strings.ToLower(err.Error()), "not_leader") {
		return s.forwardBackupJobFriendlySourceToLeader(update)
	}

	return err
}

func (s *Service) matchBackupJobIDsForGuest(guestType string, guestID uint) ([]uint, error) {
	type jobRow struct {
		ID              uint
		SourceDataset   string
		JailRootDataset string
	}

	rows := []jobRow{}
	query := s.DB.Model(&clusterModels.BackupJob{}).
		Select("id", "source_dataset", "jail_root_dataset")

	switch guestType {
	case clusterModels.ReplicationGuestTypeVM:
		query = query.Where("mode = ?", clusterModels.BackupJobModeVM)
	case clusterModels.ReplicationGuestTypeJail:
		query = query.Where("mode = ?", clusterModels.BackupJobModeJail)
	default:
		return nil, fmt.Errorf("invalid_guest_type")
	}

	if err := query.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_backup_jobs_for_guest: %w", err)
	}

	jobIDs := make([]uint, 0, len(rows))
	for _, row := range rows {
		switch guestType {
		case clusterModels.ReplicationGuestTypeVM:
			rid, ok := parseVMRIDFromDataset(row.SourceDataset)
			if ok && rid == guestID {
				jobIDs = append(jobIDs, row.ID)
			}
		case clusterModels.ReplicationGuestTypeJail:
			ctid, ok := parseJailCTIDFromDataset(row.JailRootDataset)
			if (!ok || ctid == 0) && strings.TrimSpace(row.SourceDataset) != "" {
				ctid, ok = parseJailCTIDFromDataset(row.SourceDataset)
			}
			if ok && ctid == guestID {
				jobIDs = append(jobIDs, row.ID)
			}
		}
	}

	return jobIDs, nil
}

func (s *Service) forwardBackupJobFriendlySourceToLeader(update BackupJobFriendlySourceUpdate) error {
	if s == nil {
		return fmt.Errorf("cluster_service_unavailable")
	}
	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	_, leaderID := s.Raft.LeaderWithID()
	leaderNodeID := strings.TrimSpace(string(leaderID))
	if leaderNodeID == "" {
		return fmt.Errorf("leader_unknown")
	}

	targetAPI, err := s.resolveClusterNodeAPIByNodeID(leaderNodeID)
	if err != nil {
		return err
	}

	hostname, err := utils.GetSystemHostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "cluster"
	}

	if s.AuthService == nil {
		return fmt.Errorf("auth_service_unavailable")
	}

	clusterToken, err := s.AuthService.CreateInternalClusterJWT(hostname, "")
	if err != nil {
		return fmt.Errorf("create_cluster_token_failed: %w", err)
	}

	body, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("marshal_backup_job_friendly_source_payload_failed: %w", err)
	}

	_, statusCode, err := utils.HTTPPostJSONWithTimeout(
		fmt.Sprintf("https://%s/api/intra-cluster/backup-job-friendly-source", targetAPI),
		body,
		map[string]string{
			"Accept":          "application/json",
			"Content-Type":    "application/json",
			"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
		},
		5*time.Second,
	)
	if err != nil {
		return fmt.Errorf("forward_backup_job_friendly_source_failed_status_%d: %w", statusCode, err)
	}

	return nil
}

func (s *Service) resolveClusterNodeAPIByNodeID(nodeID string) (string, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return "", fmt.Errorf("cluster_node_not_found")
	}

	nodes, err := s.Nodes()
	if err == nil {
		for _, node := range nodes {
			if strings.TrimSpace(node.NodeUUID) == nodeID {
				api := strings.TrimSpace(node.API)
				if api != "" {
					return api, nil
				}
			}
		}
	}

	if s.Raft != nil {
		fut := s.Raft.GetConfiguration()
		if err := fut.Error(); err == nil {
			for _, server := range fut.Configuration().Servers {
				if strings.TrimSpace(string(server.ID)) != nodeID {
					continue
				}

				host, _, splitErr := net.SplitHostPort(string(server.Address))
				if splitErr != nil {
					host = string(server.Address)
				}

				if strings.TrimSpace(host) == "" {
					continue
				}

				return net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(ClusterEmbeddedHTTPSPort)), nil
			}
		}
	}

	return "", fmt.Errorf("cluster_node_not_found")
}

/*
backupJobRequest
type BackupJobReq struct {
	Name             string `json:"name" binding:"required,min=2"`
	TargetID         uint   `json:"targetId" binding:"required"`
	RunnerNodeID     string `json:"runnerNodeId"`
	Mode             string `json:"mode" binding:"required"`
	SourceDataset    string `json:"sourceDataset"`
	JailRootDataset  string `json:"jailRootDataset"`
	PruneKeepLast    int    `json:"pruneKeepLast"`
	PruneTarget      bool   `json:"pruneTarget"`
	StopBeforeBackup bool   `json:"stopBeforeBackup"`
	CronExpr         string `json:"cronExpr"`
	Enabled          *bool  `json:"enabled"`
}

		err := cS.ProposeBackupJobCreate(cluster.BackupJobInput{
			Name:             req.Name,
			TargetID:         req.TargetID,
			RunnerNodeID:     req.RunnerNodeID,
			Mode:             req.Mode,
			SourceDataset:    req.SourceDataset,
			JailRootDataset:  req.JailRootDataset,
			PruneKeepLast:    req.PruneKeepLast,
			PruneTarget:      req.PruneTarget,
			StopBeforeBackup: req.StopBeforeBackup,
			CronExpr:         req.CronExpr,
			Enabled:          req.Enabled,
		}, cS.Raft == nil)
*/

func (s *Service) ProposeBackupJobCreate(input clusterServiceInterfaces.BackupJobReq, bypassRaft bool) error {
	return s.ProposeBackupJobCreateContext(context.Background(), input, bypassRaft)
}

func (s *Service) ProposeBackupJobCreateContext(
	ctx context.Context,
	input clusterServiceInterfaces.BackupJobReq,
	bypassRaft bool,
) error {
	id, err := s.newRaftObjectID("backup_jobs")
	if err != nil {
		return fmt.Errorf("new_backup_job_id_failed: %w", err)
	}

	job, fence, err := s.buildBackupJob(ctx, id, input, bypassRaft, BackupJobPlacementAuthorization{})
	if err != nil {
		return err
	}
	targetReadiness := job.TargetReadinessReceipt
	if targetReadiness == nil {
		return fmt.Errorf("backup_target_readiness_job_receipt_missing")
	}

	if bypassRaft {
		return s.DB.Transaction(func(tx *gorm.DB) error {
			if err := clusterModels.ApplyBackupTargetNodeReadinessForJobTxn(tx, job, targetReadiness); err != nil {
				return err
			}
			return tx.Create(job).Error
		})
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	data, err := json.Marshal(clusterModels.BackupJobCommandPayload{
		Job:             *job,
		PlacementFence:  fence,
		TargetReadiness: targetReadiness,
	})
	if err != nil {
		return fmt.Errorf("failed_to_marshal_backup_job_payload: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "backup_job",
		Action: "create",
		Data:   data,
	})
}

func (s *Service) ProposeBackupJobUpdate(id uint, input clusterServiceInterfaces.BackupJobReq, bypassRaft bool) error {
	return s.ProposeBackupJobUpdateContext(
		context.Background(), id, input, bypassRaft, BackupJobPlacementAuthorization{},
	)
}

func (s *Service) ProposeBackupJobUpdateContext(
	ctx context.Context,
	id uint,
	input clusterServiceInterfaces.BackupJobReq,
	bypassRaft bool,
	authorization BackupJobPlacementAuthorization,
) error {
	if id == 0 {
		return fmt.Errorf("invalid_job_id")
	}

	previousFence, err := s.backupJobPreviousPlacementFence(ctx, id, authorization)
	if err != nil {
		return err
	}
	job, fence, err := s.buildBackupJob(ctx, id, input, bypassRaft, authorization)
	if err != nil {
		return err
	}
	targetReadiness := job.TargetReadinessReceipt
	if targetReadiness == nil {
		return fmt.Errorf("backup_target_readiness_job_receipt_missing")
	}

	if bypassRaft {
		return s.DB.Transaction(func(tx *gorm.DB) error {
			if err := clusterModels.ApplyBackupTargetNodeReadinessForJobTxn(tx, job, targetReadiness); err != nil {
				return err
			}
			if err := tx.Model(&clusterModels.BackupJob{}).Where("id = ?", id).Updates(map[string]any{
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
				"recursive":          job.Recursive,
				"cron_expr":          job.CronExpr,
				"enabled":            job.Enabled,
				"next_run_at":        job.NextRunAt,
			}).Error; err != nil {
				return err
			}
			return clusterModels.ClearBackupJobRepairRequiredTxn(tx, id)
		})
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	data, err := json.Marshal(clusterModels.BackupJobCommandPayload{
		Job:                    *job,
		PlacementFence:         fence,
		PreviousPlacementFence: previousFence,
		TargetReadiness:        targetReadiness,
	})
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
		return clusterModels.DeleteBackupJobTxn(s.DB, id)
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

func (s *Service) buildBackupJob(
	ctx context.Context,
	id uint,
	input clusterServiceInterfaces.BackupJobReq,
	bypassRaft bool,
	authorization BackupJobPlacementAuthorization,
) (*clusterModels.BackupJob, *clusterModels.BackupJobPlacementFence, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if input.TargetID == 0 {
		return nil, nil, fmt.Errorf("target_id_required")
	}

	var target clusterModels.BackupTarget
	if err := s.DB.WithContext(ctx).First(&target, input.TargetID).Error; err != nil {
		return nil, nil, fmt.Errorf("backup_target_not_found")
	}
	if strings.TrimSpace(input.Name) == "" {
		return nil, nil, fmt.Errorf("name_required")
	}

	runnerNodeID := strings.TrimSpace(input.RunnerNodeID)
	if runnerNodeID == "" {
		if detail := s.Detail(); detail != nil {
			runnerNodeID = strings.TrimSpace(detail.NodeID)
		}
	}
	if runnerNodeID == "" {
		return nil, nil, fmt.Errorf("backup_runner_node_id_required")
	}

	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode == "" {
		mode = clusterModels.BackupJobModeDataset
	}
	if mode != clusterModels.BackupJobModeDataset &&
		mode != clusterModels.BackupJobModeJail &&
		mode != clusterModels.BackupJobModeVM {
		return nil, nil, fmt.Errorf("invalid_mode")
	}

	var schedule cron.Schedule
	cronExpr := strings.TrimSpace(input.CronExpr)
	if cronExpr != "" {
		var err error
		schedule, err = cron.ParseStandard(cronExpr)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid_cron_expr")
		}
	}

	now := time.Now().UTC()
	var next time.Time
	if schedule != nil {
		next = schedule.Next(now)
	}
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
		SourceDataset:    normalizeManagedGuestDatasetPath(input.SourceDataset),
		JailRootDataset:  normalizeManagedGuestDatasetPath(input.JailRootDataset),
		PruneKeepLast:    input.PruneKeepLast,
		PruneTarget:      input.PruneTarget,
		StopBeforeBackup: input.StopBeforeBackup,
		Recursive:        input.Recursive,
		CronExpr:         cronExpr,
		Enabled:          enabled,
	}
	if job.PruneKeepLast < 0 {
		return nil, nil, fmt.Errorf("invalid_prune_keep_last")
	}

	switch mode {
	case clusterModels.BackupJobModeDataset:
		if job.SourceDataset == "" {
			return nil, nil, fmt.Errorf("source_dataset_required")
		}
		job.JailRootDataset = ""
	case clusterModels.BackupJobModeJail:
		if job.JailRootDataset == "" {
			return nil, nil, fmt.Errorf("jail_root_dataset_required")
		}
		job.SourceDataset = ""
	case clusterModels.BackupJobModeVM:
		if job.SourceDataset == "" {
			return nil, nil, fmt.Errorf("source_dataset_required")
		}
		job.JailRootDataset = ""
	}

	validation, placementFence, err := s.validateBackupJobOnRunner(ctx, job, bypassRaft, authorization)
	if err != nil {
		return nil, nil, err
	}
	if job.StopBeforeBackup && mode == clusterModels.BackupJobModeDataset {
		return nil, nil, fmt.Errorf("stop_before_backup_not_supported_for_dataset_mode")
	}

	job.DestSuffix = autoBackupJobDestSuffix(job.ID, job.Mode, job.SourceDataset, job.JailRootDataset)
	if job.DestSuffix != "" {
		var conflictCount int64
		if err := s.DB.WithContext(ctx).Model(&clusterModels.BackupJob{}).
			Where("target_id = ? AND dest_suffix = ? AND id != ?", job.TargetID, job.DestSuffix, job.ID).
			Count(&conflictCount).Error; err != nil {
			return nil, nil, fmt.Errorf("dest_suffix_uniqueness_check_failed: %w", err)
		}
		if conflictCount > 0 {
			return nil, nil, fmt.Errorf(
				"dest_suffix_already_in_use: target_id=%d dest_suffix=%s",
				job.TargetID,
				job.DestSuffix,
			)
		}
	}

	job.FriendlySrc = strings.TrimSpace(validation.FriendlySource)
	job.TargetReadinessReceipt = validation.TargetReadiness
	if !next.IsZero() {
		job.NextRunAt = &next
	}
	return job, placementFence, nil
}

func autoBackupJobDestSuffix(jobID uint, mode, sourceDataset, jailRootDataset string) string {
	source := strings.TrimSpace(sourceDataset)
	if strings.TrimSpace(mode) == clusterModels.BackupJobModeJail {
		source = strings.TrimSpace(jailRootDataset)
	}

	base := autoBackupDestBase(source)
	if base == "" {
		base = "backups"
	}

	if jobID == 0 {
		return fmt.Sprintf("%s/j-pending/active", base)
	}

	return fmt.Sprintf("%s/j-%s/active", base, compactBackupJobToken(jobID))
}

func compactBackupJobToken(jobID uint) string {
	if jobID == 0 {
		return "0"
	}
	return strings.ToLower(strconv.FormatUint(uint64(jobID), 36))
}

func autoBackupDestBase(source string) string {
	source = normalizeBackupDatasetPath(source)
	if source == "" {
		return ""
	}

	parts := strings.Split(source, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		switch parts[i] {
		case "jails", "virtual-machines":
			return strings.Join(parts[i:], "/")
		}
	}

	return strings.ReplaceAll(source, "/", "-")
}

func normalizeBackupDatasetPath(dataset string) string {
	dataset = strings.TrimSpace(dataset)
	for strings.HasSuffix(dataset, "/") {
		dataset = strings.TrimSuffix(dataset, "/")
	}
	return dataset
}

func (s *Service) resolveBackupJobFriendlySource(mode, sourceDataset, jailRootDataset string) string {
	if mode == clusterModels.BackupJobModeDataset {
		return strings.TrimSpace(sourceDataset)
	}

	if mode == clusterModels.BackupJobModeVM {
		vmDataset := strings.TrimSpace(sourceDataset)
		if vmDataset == "" {
			return ""
		}

		rid, ok := parseVMRIDFromDataset(vmDataset)
		if !ok {
			return vmDataset
		}

		var vm vmModels.VM
		if err := s.DB.Select("name").Where("rid = ?", rid).First(&vm).Error; err == nil {
			name := strings.TrimSpace(vm.Name)
			if name != "" {
				return name
			}
		} else {
			logger.L.Err(err).Msg("failed_to_lookup_vm_for_backup_job_friendly_source")
		}

		return vmDataset
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

func parseVMRIDFromDataset(dataset string) (uint, bool) {
	dataset = strings.TrimSpace(dataset)
	if dataset == "" {
		return 0, false
	}

	parts := strings.Split(strings.Trim(dataset, "/"), "/")
	for idx := 0; idx+1 < len(parts); idx++ {
		if parts[idx] != "virtual-machines" {
			continue
		}

		ridRaw := strings.TrimSpace(parts[idx+1])
		if ridRaw == "" {
			continue
		}

		cutAt := len(ridRaw)
		if split := strings.IndexAny(ridRaw, "._"); split > 0 && split < cutAt {
			cutAt = split
		}
		ridRaw = strings.TrimSpace(ridRaw[:cutAt])
		if ridRaw == "" {
			continue
		}

		rid, err := strconv.ParseUint(ridRaw, 10, 64)
		if err == nil && rid > 0 {
			return uint(rid), true
		}
	}

	return 0, false
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

	applyFuture := s.Raft.Apply(payload, raftApplyTimeout)
	if err := applyFuture.Error(); err != nil {
		return fmt.Errorf("raft_apply_failed: %w", err)
	}

	if resp, ok := applyFuture.Response().(error); ok && resp != nil {
		return fmt.Errorf("fsm_apply_failed: %w", resp)
	}

	return nil
}

// backupRunnerNodeExists is retained for replication-policy compatibility.
// Backup-job validation resolves its runner directly from Raft membership.
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
	idRangeMax := raftObjectIDRangeForTable(table)
	n, err := rand.Int(rand.Reader, idRangeMax)
	if err != nil {
		return 0, err
	}
	id := uint(n.Uint64())
	if id == 0 {
		return 0, fmt.Errorf("generated_zero_id")
	}
	return id, nil
}

func raftObjectIDRangeForTable(table string) *big.Int {
	switch strings.ToLower(strings.TrimSpace(table)) {
	case "backup_jobs":
		return maxBackupJobIDRange
	default:
		return maxSafeJSInt
	}
}

func resolveSSHKeyMaterial(sshKey, sshKeyPath string) (string, error) {
	trimmedKey := strings.TrimSpace(sshKey)
	if trimmedKey != "" {
		return trimmedKey, nil
	}

	trimmedPath := strings.TrimSpace(sshKeyPath)
	if trimmedPath == "" {
		return "", nil
	}

	raw, err := os.ReadFile(trimmedPath)
	if err != nil {
		return "", fmt.Errorf("ssh_key_file_not_found: %s: %w", trimmedPath, err)
	}

	return strings.TrimSpace(string(raw)), nil
}
