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
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func init() {
	// Column values travel as interfaces; time is the only non-basic type the
	// SQLite driver returns.
	gob.Register(time.Time{})
}

type ReplicatedStateTable struct {
	Table string
	Model any
}

// Children precede parents: clearing walks it forwards and restore backwards.
var replicatedStateManifest = []ReplicatedStateTable{
	{Table: "guest_identity_claims", Model: &GuestIdentityClaim{}},
	{Table: "scheduled_run_receipts", Model: &ScheduledRunReceipt{}},
	{Table: "replication_run_operations", Model: &ReplicationRunOperation{}},
	{Table: "backup_job_runner_rebind_items", Model: &BackupJobRunnerRebindItem{}},
	{Table: "backup_job_runner_rebinds", Model: &BackupJobRunnerRebind{}},
	{Table: "backup_target_restore_operations", Model: &BackupTargetRestoreOperation{}},
	{Table: "backup_target_node_readinesses", Model: &BackupTargetNodeReadiness{}},
	{Table: "backup_target_ssh_host_trusts", Model: &BackupTargetSSHHostTrust{}},
	{Table: "backup_target_provision_operations", Model: &BackupTargetProvisionOperation{}},
	{Table: "backup_job_operations", Model: &BackupJobOperation{}},
	{Table: "replication_guest_operation_receipts", Model: &ReplicationGuestOperationReceipt{}},
	{Table: "replication_guest_operations", Model: &ReplicationGuestOperation{}},
	{Table: "replication_transition_events", Model: &ReplicationTransitionEvent{}},
	{Table: "replication_leases", Model: &ReplicationLease{}},
	{Table: "replication_policy_targets", Model: &ReplicationPolicyTarget{}},
	{Table: "replication_policies", Model: &ReplicationPolicy{}},
	{Table: "backup_jobs", Model: &BackupJob{}},
	{Table: "backup_targets", Model: &BackupTarget{}},
	{Table: "cluster_ssh_identities", Model: &ClusterSSHIdentity{}},
	{Table: "encryption_keys", Model: &EncryptionKey{}},
	{Table: "cluster_notes", Model: &ClusterNote{}},
	{Table: "cluster_options", Model: &ClusterOption{}},
}

func ReplicatedModels() []any {
	models := make([]any, len(replicatedStateManifest))
	for i, entry := range replicatedStateManifest {
		models[i] = entry.Model
	}
	return models
}

type ClusterSnapshot struct {
	AppliedIndex uint64
	Tables       []SnapshotTable
}

type SnapshotTable struct {
	Name     string
	Columns  []string
	Rows     [][]any
	Sequence int64
}

func ClearReplicatedStateTx(tx *gorm.DB) error {
	if tx == nil {
		return fmt.Errorf("replicated_state_database_required")
	}
	tableNames := make([]string, 0, len(replicatedStateManifest))
	for _, entry := range replicatedStateManifest {
		if err := tx.Exec("DELETE FROM " + quoteIdentifier(entry.Table)).Error; err != nil {
			return fmt.Errorf("clear_replicated_state_%s: %w", entry.Table, err)
		}
		tableNames = append(tableNames, entry.Table)
	}
	if err := tx.Exec("DELETE FROM sqlite_sequence WHERE name IN ?", tableNames).Error; err != nil {
		return fmt.Errorf("clear_replicated_state_sequences: %w", err)
	}
	return nil
}

func captureClusterSnapshot(db *gorm.DB) (*ClusterSnapshot, error) {
	if db == nil {
		return nil, fmt.Errorf("replicated_state_database_required")
	}
	snap := &ClusterSnapshot{Tables: make([]SnapshotTable, 0, len(replicatedStateManifest))}
	for _, entry := range replicatedStateManifest {
		table, err := captureSnapshotTable(db, entry)
		if err != nil {
			return nil, fmt.Errorf("capture_replicated_state_%s: %w", entry.Table, err)
		}
		snap.Tables = append(snap.Tables, table)
	}
	return snap, nil
}

var snapshotSchemas sync.Map

// Use the model's columns sorted by name: an upgraded database has later columns
// at the end and may keep removed ones, so table layouts differ between nodes.
func captureSnapshotTable(db *gorm.DB, entry ReplicatedStateTable) (SnapshotTable, error) {
	table := SnapshotTable{Name: entry.Table}
	model, err := schema.Parse(entry.Model, &snapshotSchemas, db.NamingStrategy)
	if err != nil {
		return table, err
	}
	if len(model.PrimaryFieldDBNames) == 0 {
		return table, fmt.Errorf("primary_key_missing")
	}
	table.Columns = slices.Sorted(slices.Values(model.DBNames))
	quoted := make([]string, len(table.Columns))
	for i, column := range table.Columns {
		quoted[i] = quoteIdentifier(column)
	}
	keys := make([]string, len(model.PrimaryFieldDBNames))
	for i, key := range model.PrimaryFieldDBNames {
		keys[i] = quoteIdentifier(key)
	}
	rows, err := db.Raw("SELECT " + strings.Join(quoted, ", ") + " FROM " + quoteIdentifier(entry.Table) +
		" ORDER BY " + strings.Join(keys, ", ")).Rows()
	if err != nil {
		return table, err
	}
	defer rows.Close()
	for rows.Next() {
		values := make([]any, len(table.Columns))
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return table, err
		}
		table.Rows = append(table.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return table, err
	}
	if err := db.Raw("SELECT COALESCE(MAX(seq), 0) FROM sqlite_sequence WHERE name = ?", entry.Table).Scan(&table.Sequence).Error; err != nil {
		return table, err
	}
	return table, nil
}

func restoreReplicatedStateTx(tx *gorm.DB, snap *ClusterSnapshot) error {
	byName := make(map[string]SnapshotTable, len(snap.Tables))
	for _, table := range snap.Tables {
		byName[table.Name] = table
	}
	if len(byName) != len(replicatedStateManifest) {
		return fmt.Errorf("cluster_snapshot_table_set_mismatch")
	}
	if err := ClearReplicatedStateTx(tx); err != nil {
		return err
	}
	for i := len(replicatedStateManifest) - 1; i >= 0; i-- {
		name := replicatedStateManifest[i].Table
		table, ok := byName[name]
		if !ok {
			return fmt.Errorf("cluster_snapshot_table_missing: %s", name)
		}
		if err := restoreSnapshotTable(tx, table); err != nil {
			return fmt.Errorf("restore_replicated_state_%s: %w", name, err)
		}
	}
	return nil
}

func restoreSnapshotTable(tx *gorm.DB, table SnapshotTable) error {
	if len(table.Columns) == 0 {
		return fmt.Errorf("cluster_snapshot_table_columns_missing")
	}
	quoted := make([]string, len(table.Columns))
	for i, column := range table.Columns {
		quoted[i] = quoteIdentifier(column)
	}
	prefix := "INSERT INTO " + quoteIdentifier(table.Name) + " (" + strings.Join(quoted, ", ") + ") VALUES "
	placeholders := "(" + strings.TrimSuffix(strings.Repeat("?, ", len(table.Columns)), ", ") + ")"
	// Stay well under SQLite's bound-parameter limit.
	batch := max(1, 900/len(table.Columns))
	for start := 0; start < len(table.Rows); start += batch {
		end := min(start+batch, len(table.Rows))
		values := make([]any, 0, (end-start)*len(table.Columns))
		groups := make([]string, 0, end-start)
		for _, row := range table.Rows[start:end] {
			if len(row) != len(table.Columns) {
				return fmt.Errorf("cluster_snapshot_row_width_mismatch")
			}
			values = append(values, row...)
			groups = append(groups, placeholders)
		}
		// Straight to the connection: gorm's Exec would expand a []byte value
		// into a list of bytes.
		if _, err := tx.Statement.ConnPool.ExecContext(tx.Statement.Context, prefix+strings.Join(groups, ", "), values...); err != nil {
			return err
		}
	}
	if err := tx.Exec("DELETE FROM sqlite_sequence WHERE name = ?", table.Name).Error; err != nil {
		return err
	}
	if table.Sequence > 0 {
		return tx.Exec("INSERT INTO sqlite_sequence (name, seq) VALUES (?, ?)", table.Name, table.Sequence).Error
	}
	return nil
}

// JSON, not gob: gob numbers types per process in first-use order, so the same
// state would hash differently on a leader and a joiner.
func ClusterSnapshotDigest(snapshot *ClusterSnapshot) (string, error) {
	if snapshot == nil {
		return "", fmt.Errorf("cluster_snapshot_required")
	}
	sum := sha256.New()
	encoder := json.NewEncoder(sum)
	for _, table := range snapshot.Tables {
		rows := make([][]any, len(table.Rows))
		for i, row := range table.Rows {
			rows[i] = make([]any, len(row))
			for j, value := range row {
				if t, ok := value.(time.Time); ok {
					value = t.UTC()
				}
				rows[i][j] = value
			}
		}
		table.Rows = rows
		if err := encoder.Encode(table); err != nil {
			return "", fmt.Errorf("encode_cluster_snapshot_digest: %w", err)
		}
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func dropRetiredReplicatedState(db *gorm.DB) error {
	migrator := db.Migrator()
	for _, table := range []string{"guest_identity_registries", "guest_identity_enrollments", "guest_identity_departures"} {
		if err := migrator.DropTable(table); err != nil {
			return err
		}
	}
	for _, column := range []struct {
		model any
		name  string
	}{
		{&GuestIdentityClaim{}, "token"},
		{&BackupTargetNodeReadiness{}, "raft_applied_index"},
	} {
		if !migrator.HasColumn(column.model, column.name) {
			continue
		}
		if err := migrator.DropColumn(column.model, column.name); err != nil {
			return err
		}
		if migrator.HasColumn(column.model, column.name) {
			return fmt.Errorf("retired_column_not_dropped: %s", column.name)
		}
	}
	return nil
}
