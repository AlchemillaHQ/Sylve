// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

func newClusterModelTestDB(t *testing.T, migrateModels ...any) *gorm.DB {
	return testutil.NewSQLiteTestDB(t, append(ReplicatedModels(), migrateModels...)...)
}

func applyFSMRaftLog(t *testing.T, fsm *FSMDispatcher, cmdBytes []byte) error {
	t.Helper()

	resp := fsm.Apply(&raft.Log{
		Type:  raft.LogCommand,
		Index: fsm.AppliedIndex() + 1,
		Data:  cmdBytes,
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

func applyFSMRaftLogPanic(t *testing.T, fsm *FSMDispatcher, cmdBytes []byte) (recovered any) {
	t.Helper()
	defer func() { recovered = recover() }()
	_ = applyFSMRaftLog(t, fsm, cmdBytes)
	return nil
}

func applyFSMCommand(t *testing.T, fsm *FSMDispatcher, cmd Command) error {
	t.Helper()

	if err := PrepareCommand(&cmd, time.Now()); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("failed to marshal command: %v", err)
	}

	return applyFSMRaftLog(t, fsm, payload)
}

func snapshotReader(t *testing.T, snapshot *ClusterSnapshot) io.ReadCloser {
	t.Helper()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(snapshot); err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}
	return io.NopCloser(&buf)
}

func ReplicatedStateManifest() []ReplicatedStateTable {
	return append([]ReplicatedStateTable(nil), replicatedStateManifest...)
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
