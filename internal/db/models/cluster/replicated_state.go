// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
)

// ReplicatedStateTable is the authoritative boundary between Raft-owned state
// and node-local state. The order is safe for destructive clearing: children
// precede parents where foreign keys may exist.
type ReplicatedStateTable struct {
	Table         string
	Model         any
	SnapshotField string
}

var replicatedStateManifest = []ReplicatedStateTable{
	{Table: "scheduled_run_receipts", Model: &ScheduledRunReceipt{}, SnapshotField: "ScheduledRunReceipts"},
	{Table: "replication_run_operations", Model: &ReplicationRunOperation{}, SnapshotField: "ReplicationRunOperations"},
	{Table: "backup_job_runner_rebind_items", Model: &BackupJobRunnerRebindItem{}, SnapshotField: "BackupJobRebindItems"},
	{Table: "backup_job_runner_rebinds", Model: &BackupJobRunnerRebind{}, SnapshotField: "BackupJobRebinds"},
	{Table: "backup_target_restore_operations", Model: &BackupTargetRestoreOperation{}, SnapshotField: "BackupTargetRestoreOperations"},
	{Table: "backup_target_node_readinesses", Model: &BackupTargetNodeReadiness{}, SnapshotField: "BackupTargetReadiness"},
	{Table: "backup_target_provision_operations", Model: &BackupTargetProvisionOperation{}, SnapshotField: "BackupTargetProvisions"},
	{Table: "backup_job_operations", Model: &BackupJobOperation{}, SnapshotField: "BackupJobOperations"},
	{Table: "replication_guest_operation_receipts", Model: &ReplicationGuestOperationReceipt{}, SnapshotField: "GuestOperationReceipts"},
	{Table: "replication_guest_operations", Model: &ReplicationGuestOperation{}, SnapshotField: "GuestOperations"},
	{Table: "replication_transition_events", Model: &ReplicationTransitionEvent{}, SnapshotField: "ReplicationTransitionEvents"},
	{Table: "replication_leases", Model: &ReplicationLease{}, SnapshotField: "ReplicationLeases"},
	{Table: "replication_policy_targets", Model: &ReplicationPolicyTarget{}, SnapshotField: "ReplicationPolicies"},
	{Table: "replication_policies", Model: &ReplicationPolicy{}, SnapshotField: "ReplicationPolicies"},
	{Table: "backup_jobs", Model: &BackupJob{}, SnapshotField: "BackupJobs"},
	{Table: "backup_targets", Model: &BackupTarget{}, SnapshotField: "BackupTargets"},
	{Table: "cluster_ssh_identities", Model: &ClusterSSHIdentity{}, SnapshotField: "SSHIdentities"},
	{Table: "encryption_keys", Model: &EncryptionKey{}, SnapshotField: "EncryptionKeys"},
	{Table: "cluster_notes", Model: &ClusterNote{}, SnapshotField: "Notes"},
	{Table: "cluster_options", Model: &ClusterOption{}, SnapshotField: "Options"},
}

// ReplicatedStateManifest returns a defensive copy so callers and tests cannot
// mutate the process-wide replicated-state boundary.
func ReplicatedStateManifest() []ReplicatedStateTable {
	return append([]ReplicatedStateTable(nil), replicatedStateManifest...)
}

// ClearReplicatedStateTx removes only Raft-owned state. Cluster membership
// metadata, local backup/replication history, and delivery outboxes are
// intentionally outside this manifest.
func ClearReplicatedStateTx(tx *gorm.DB) error {
	if tx == nil {
		return fmt.Errorf("replicated_state_database_required")
	}
	for _, entry := range replicatedStateManifest {
		if !tx.Migrator().HasTable(entry.Model) {
			continue
		}
		if err := tx.Exec("DELETE FROM " + entry.Table).Error; err != nil {
			return fmt.Errorf("clear_replicated_state_%s: %w", entry.Table, err)
		}
	}
	if tx.Dialector != nil && tx.Dialector.Name() == "sqlite" &&
		tx.Migrator().HasTable("sqlite_sequence") {
		tableNames := make([]string, 0, len(replicatedStateManifest))
		for _, entry := range replicatedStateManifest {
			tableNames = append(tableNames, entry.Table)
		}
		if err := tx.Exec(
			"DELETE FROM sqlite_sequence WHERE name IN ?",
			tableNames,
		).Error; err != nil {
			return fmt.Errorf("clear_replicated_state_sequences: %w", err)
		}
	}
	return nil
}

func captureClusterSnapshot(db *gorm.DB) (*ClusterSnapshot, error) {
	if db == nil {
		return nil, fmt.Errorf("replicated_state_database_required")
	}

	snap := &ClusterSnapshot{
		Notes:                         []ClusterNote{},
		Options:                       []ClusterOption{},
		BackupTargets:                 []BackupTargetReplicationPayload{},
		BackupTargetProvisions:        []BackupTargetProvisionOperation{},
		BackupTargetReadiness:         []BackupTargetNodeReadiness{},
		BackupJobs:                    []BackupJob{},
		BackupJobOperations:           []BackupJobOperation{},
		ReplicationRunOperations:      []ReplicationRunOperation{},
		ScheduledRunReceipts:          []ScheduledRunReceipt{},
		BackupTargetRestoreOperations: []BackupTargetRestoreOperation{},
		BackupJobRebinds:              []BackupJobRunnerRebind{},
		BackupJobRebindItems:          []BackupJobRunnerRebindItem{},
		ReplicationPolicies:           []ReplicationPolicyPayload{},
		ReplicationLeases:             []ReplicationLease{},
		GuestOperations:               []ReplicationGuestOperation{},
		GuestOperationReceipts:        []ReplicationGuestOperationReceipt{},
		ReplicationEvents:             []ReplicationEvent{},
		ReplicationTransitionEvents:   []ReplicationTransitionEvent{},
		SSHIdentities:                 []ClusterSSHIdentity{},
		EncryptionKeys:                []EncryptionKey{},
	}

	if err := db.Order("id ASC").Find(&snap.Notes).Error; err != nil {
		return nil, err
	}
	if err := db.Order("id ASC").Find(&snap.Options).Error; err != nil {
		return nil, err
	}
	var targets []BackupTarget
	if err := db.Order("id ASC").Find(&targets).Error; err != nil {
		return nil, err
	}
	for _, target := range targets {
		snap.BackupTargets = append(snap.BackupTargets, BackupTargetToReplicationPayload(target))
	}
	if db.Migrator().HasTable(&BackupTargetProvisionOperation{}) {
		if err := db.Order("token ASC").Find(&snap.BackupTargetProvisions).Error; err != nil {
			return nil, err
		}
	}
	if db.Migrator().HasTable(&BackupTargetNodeReadiness{}) {
		if err := db.Order("target_id ASC, node_id ASC").Find(&snap.BackupTargetReadiness).Error; err != nil {
			return nil, err
		}
	}
	if err := db.Order("id ASC").Find(&snap.BackupJobs).Error; err != nil {
		return nil, err
	}
	if err := db.Order("job_id ASC").Find(&snap.BackupJobOperations).Error; err != nil {
		return nil, err
	}
	if db.Migrator().HasTable(&ReplicationRunOperation{}) {
		if err := db.Order("policy_id ASC").Find(&snap.ReplicationRunOperations).Error; err != nil {
			return nil, err
		}
	}
	if db.Migrator().HasTable(&ScheduledRunReceipt{}) {
		if err := db.Order("token ASC").Find(&snap.ScheduledRunReceipts).Error; err != nil {
			return nil, err
		}
	}
	if db.Migrator().HasTable(&BackupTargetRestoreOperation{}) {
		if err := db.Order("holder_node_id ASC, destination_dataset ASC, token ASC").
			Find(&snap.BackupTargetRestoreOperations).Error; err != nil {
			return nil, err
		}
	}
	if db.Migrator().HasTable(&BackupJobRunnerRebind{}) {
		if err := db.Order("token ASC").Find(&snap.BackupJobRebinds).Error; err != nil {
			return nil, err
		}
	}
	if db.Migrator().HasTable(&BackupJobRunnerRebindItem{}) {
		if err := db.Order("operation_token ASC, job_id ASC").Find(&snap.BackupJobRebindItems).Error; err != nil {
			return nil, err
		}
	}
	var policies []ReplicationPolicy
	if err := db.
		Preload("Targets", func(query *gorm.DB) *gorm.DB {
			return query.Order("id ASC, node_id ASC")
		}).
		Order("id ASC").
		Find(&policies).Error; err != nil {
		return nil, err
	}
	for _, policy := range policies {
		snap.ReplicationPolicies = append(snap.ReplicationPolicies, ReplicationPolicyPayload{
			Policy:  policy,
			Targets: policy.Targets,
		})
	}
	if err := db.Order("id ASC").Find(&snap.ReplicationLeases).Error; err != nil {
		return nil, err
	}
	if err := db.Order("guest_type ASC, guest_id ASC").Find(&snap.GuestOperations).Error; err != nil {
		return nil, err
	}
	if err := db.Order("token ASC").Find(&snap.GuestOperationReceipts).Error; err != nil {
		return nil, err
	}
	if db.Migrator().HasTable(&ReplicationTransitionEvent{}) {
		if err := db.Order("id ASC").Find(&snap.ReplicationTransitionEvents).Error; err != nil {
			return nil, err
		}
	}
	if err := db.Order("node_uuid ASC").Find(&snap.SSHIdentities).Error; err != nil {
		return nil, err
	}
	if err := db.Order("id ASC").Find(&snap.EncryptionKeys).Error; err != nil {
		return nil, err
	}
	return snap, nil
}

// CaptureClusterSnapshot captures the canonical logical Raft-owned state.
// Callers that share a live FSM should prefer FSMDispatcher.StateDigest so the
// capture is serialized with command application.
func CaptureClusterSnapshot(db *gorm.DB) (*ClusterSnapshot, error) {
	return captureClusterSnapshot(db)
}

func ClusterSnapshotDigest(snapshot *ClusterSnapshot) (string, error) {
	if snapshot == nil {
		return "", fmt.Errorf("cluster_snapshot_required")
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("marshal_cluster_snapshot_digest: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func CaptureReplicatedStateDigest(db *gorm.DB) (*ClusterSnapshot, string, error) {
	snapshot, err := captureClusterSnapshot(db)
	if err != nil {
		return nil, "", err
	}
	digest, err := ClusterSnapshotDigest(snapshot)
	if err != nil {
		return nil, "", err
	}
	return snapshot, digest, nil
}
