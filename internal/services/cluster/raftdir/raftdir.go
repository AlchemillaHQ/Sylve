// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package raftdir

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

// Bump Format on any change to commands, snapshots or transport an older build
// couldn't read. The next bump needs another rebuild credential: this format
// clears the cluster key once a node is clustered, but pins the members' certs.
const Format = 1

const formatFile = "format"

// Older builds wrote no marker: their state is format 0.
func ReadFormat(dir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(dir, formatFile))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func WriteFormat(dir string) error {
	return os.WriteFile(filepath.Join(dir, formatFile), []byte(strconv.Itoa(Format)+"\n"), 0o600)
}

func HasState(dir string) bool {
	for _, name := range []string{"raft-log.db", "raft-stable.db", "snapshots"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && (info.Mode().IsRegular() || info.IsDir()) {
			return true
		}
	}
	return false
}

func OpenStores(dir string, logOutput io.Writer) (*raftboltdb.BoltStore, *raftboltdb.BoltStore, raft.SnapshotStore, error) {
	logs, err := raftboltdb.NewBoltStore(filepath.Join(dir, "raft-log.db"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed_to_create_log_store: %w", err)
	}
	stable, err := raftboltdb.NewBoltStore(filepath.Join(dir, "raft-stable.db"))
	if err != nil {
		_ = logs.Close()
		return nil, nil, nil, fmt.Errorf("failed_to_create_stable_store: %w", err)
	}
	snapshots, err := raft.NewFileSnapshotStore(dir, 2, logOutput)
	if err != nil {
		_ = logs.Close()
		_ = stable.Close()
		return nil, nil, nil, fmt.Errorf("failed_to_create_snap_store: %w", err)
	}
	return logs, stable, snapshots, nil
}

type State struct {
	Configuration raft.Configuration
	LastTerm      uint64
	LastIndex     uint64
}

// Read reads no snapshot payload, so it works on any format.
func Read(dir string) (State, error) {
	var state State
	logs, stable, snapshots, err := OpenStores(dir, io.Discard)
	if err != nil {
		return state, err
	}
	defer logs.Close()
	defer stable.Close()
	if state.Configuration, err = LatestConfiguration(logs, snapshots); err != nil {
		return state, err
	}
	state.LastTerm, state.LastIndex, err = LastEntry(logs, snapshots)
	return state, err
}

func LatestConfiguration(logs raft.LogStore, snapshots raft.SnapshotStore) (raft.Configuration, error) {
	var configuration raft.Configuration
	after := uint64(0)
	metas, err := snapshots.List()
	if err != nil {
		return configuration, fmt.Errorf("raft_snapshot_list_failed: %w", err)
	}
	if len(metas) > 0 {
		configuration, after = metas[0].Configuration, metas[0].Index
	}
	last, err := logs.LastIndex()
	if err != nil {
		return configuration, fmt.Errorf("raft_log_index_failed: %w", err)
	}
	for index := after + 1; index <= last; index++ {
		var entry raft.Log
		if err := logs.GetLog(index, &entry); err != nil {
			if errors.Is(err, raft.ErrLogNotFound) {
				continue
			}
			return configuration, fmt.Errorf("raft_log_read_failed: %w", err)
		}
		if entry.Type == raft.LogConfiguration {
			configuration = raft.DecodeConfiguration(entry.Data)
		}
	}
	return configuration, nil
}

func LastEntry(logs raft.LogStore, snapshots raft.SnapshotStore) (uint64, uint64, error) {
	var term, index uint64
	metas, err := snapshots.List()
	if err != nil {
		return 0, 0, fmt.Errorf("raft_snapshot_list_failed: %w", err)
	}
	if len(metas) > 0 {
		term, index = metas[0].Term, metas[0].Index
	}
	last, err := logs.LastIndex()
	if err != nil {
		return 0, 0, fmt.Errorf("raft_log_index_failed: %w", err)
	}
	if last > index {
		var entry raft.Log
		if err := logs.GetLog(last, &entry); err != nil {
			return 0, 0, fmt.Errorf("raft_log_read_failed: %w", err)
		}
		term, index = entry.Term, entry.Index
	}
	return term, index, nil
}

const rebuildFile = "rebuild.json"

// A rebuild goes through these phases; the electing one has none.
const (
	RebuildFounding  = "founding"
	RebuildFounded   = "founded"
	RebuildRejoining = "rejoining"
)

type Rebuild struct {
	Key       string        `json:"key"`
	Members   []raft.Server `json:"members"`
	LastTerm  uint64        `json:"lastTerm"`
	LastIndex uint64        `json:"lastIndex"`
	Vote      string        `json:"vote,omitempty"`
	Phase     string        `json:"phase,omitempty"`
	Force     bool          `json:"force,omitempty"`
}

func PreDir(raftDir string) string {
	return raftDir + ".pre-" + strconv.Itoa(Format)
}

func FindRebuild(raftDir string) (string, *Rebuild, error) {
	for _, dir := range []string{PreDir(raftDir), raftDir} {
		data, err := os.ReadFile(filepath.Join(dir, rebuildFile))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		var rebuild Rebuild
		if err := json.Unmarshal(data, &rebuild); err != nil {
			return "", nil, fmt.Errorf("rebuild_state_unreadable: %w", err)
		}
		return dir, &rebuild, nil
	}
	return "", nil, nil
}

func NewRebuild(dir, nodeID, key string) (*Rebuild, error) {
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("rebuild_cluster_key_missing")
	}
	old, err := Read(dir)
	if err != nil {
		return nil, err
	}
	member := false
	for _, server := range old.Configuration.Servers {
		member = member || string(server.ID) == nodeID
	}
	if !member {
		return nil, fmt.Errorf("rebuild_node_not_member: %s", nodeID)
	}
	rebuild := &Rebuild{
		Key: strings.TrimSpace(key), Members: old.Configuration.Servers,
		LastTerm: old.LastTerm, LastIndex: old.LastIndex,
	}
	return rebuild, rebuild.Save(dir)
}

func (r *Rebuild) Save(dir string) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, rebuildFile)
	file, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func RemoveRebuild(dir string) error {
	err := os.Remove(filepath.Join(dir, rebuildFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func ForceRebuild(raftDir, nodeID, key string) (bool, error) {
	dir, rebuild, err := FindRebuild(raftDir)
	if err != nil {
		return false, err
	}
	if rebuild != nil && rebuild.Phase == RebuildFounded {
		// This node already runs the rebuilt cluster: its quorum is recovered like any.
		return false, nil
	}
	if rebuild == nil {
		format, err := ReadFormat(raftDir)
		if err != nil || format >= Format {
			return false, err
		}
		dir = raftDir
		if rebuild, err = NewRebuild(dir, nodeID, key); err != nil {
			return false, err
		}
	}
	rebuild.Force = true
	return true, rebuild.Save(dir)
}
