// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/hashicorp/raft"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newClusterModelTestDB(t *testing.T, migrateModels ...any) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	if len(migrateModels) > 0 {
		if err := db.AutoMigrate(migrateModels...); err != nil {
			t.Fatalf("failed to migrate test tables: %v", err)
		}
	}

	return db
}

func applyFSMRaftLog(t *testing.T, fsm *FSMDispatcher, cmdBytes []byte) error {
	t.Helper()

	resp := fsm.Apply(&raft.Log{
		Type: raft.LogCommand,
		Data: cmdBytes,
	})

	if resp == nil {
		return nil
	}

	err, ok := resp.(error)
	if !ok {
		return fmt.Errorf("unexpected apply response type %T", resp)
	}

	return err
}

func applyFSMCommand(t *testing.T, fsm *FSMDispatcher, cmd Command) error {
	t.Helper()

	payload, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("failed to marshal command: %v", err)
	}

	return applyFSMRaftLog(t, fsm, payload)
}
