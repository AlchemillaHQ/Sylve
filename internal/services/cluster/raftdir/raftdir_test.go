// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package raftdir

import (
	"io"
	"os"
	"slices"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

var oldMembers = []raft.Server{
	{ID: "node-a", Address: "192.0.2.1:8180", Suffrage: raft.Voter},
	{ID: "node-b", Address: "192.0.2.2:8180", Suffrage: raft.Voter},
	{ID: "node-c", Address: "192.0.2.3:8180", Suffrage: raft.Nonvoter},
}

func oldRaftDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	logs, stable, snapshots, err := OpenStores(dir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	defer stable.Close()
	config := raft.DefaultConfig()
	config.LocalID = "node-a"
	config.Logger = hclog.NewNullLogger()
	_, transport := raft.NewInmemTransport("")
	if err := raft.BootstrapCluster(config, logs, stable, snapshots, transport, raft.Configuration{Servers: oldMembers}); err != nil {
		t.Fatal(err)
	}
	var entries []*raft.Log
	for index := uint64(2); index <= 5; index++ {
		entries = append(entries, &raft.Log{Index: index, Term: 3, Type: raft.LogCommand, Data: []byte(`{}`)})
	}
	if err := logs.StoreLogs(entries); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReadOldRaftState(t *testing.T) {
	dir := oldRaftDir(t)
	if !HasState(dir) || HasState(t.TempDir()) {
		t.Fatal("HasState is wrong")
	}
	if format, err := ReadFormat(dir); err != nil || format != 0 {
		t.Fatalf("old format = %d, %v", format, err)
	}
	state, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(state.Configuration.Servers, oldMembers) || state.LastTerm != 3 || state.LastIndex != 5 {
		t.Fatalf("state = %+v", state)
	}
	if err := WriteFormat(dir); err != nil {
		t.Fatal(err)
	}
	if format, err := ReadFormat(dir); err != nil || format != Format {
		t.Fatalf("marked format = %d, %v", format, err)
	}
}

func TestRebuildRecordFollowsTheOldState(t *testing.T) {
	dir := oldRaftDir(t)
	if _, err := NewRebuild(dir, "node-x", "old-key"); err == nil {
		t.Fatal("a node outside the old cluster started a rebuild")
	}
	if _, err := NewRebuild(dir, "node-a", " "); err == nil {
		t.Fatal("a rebuild started without the old cluster key")
	}
	rebuild, err := NewRebuild(dir, "node-a", "old-key")
	if err != nil {
		t.Fatal(err)
	}
	if rebuild.LastTerm != 3 || rebuild.LastIndex != 5 || len(rebuild.Members) != 3 {
		t.Fatalf("rebuild = %+v", rebuild)
	}
	rebuild.Vote = "node-b"
	if err := rebuild.Save(dir); err != nil {
		t.Fatal(err)
	}

	if found, record, err := FindRebuild(dir); err != nil || found != dir || record.Vote != "node-b" {
		t.Fatalf("in the Raft dir: %q %+v %v", found, record, err)
	}
	if err := os.Rename(dir, PreDir(dir)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if found, record, err := FindRebuild(dir); err != nil || found != PreDir(dir) || record.Vote != "node-b" {
		t.Fatalf("moved aside: %q %+v %v", found, record, err)
	}
	if err := RemoveRebuild(PreDir(dir)); err != nil {
		t.Fatal(err)
	}
	if found, record, err := FindRebuild(dir); err != nil || record != nil {
		t.Fatalf("after removal: %q %+v %v", found, record, err)
	}
}

func TestForceRebuild(t *testing.T) {
	dir := oldRaftDir(t)
	if forced, err := ForceRebuild(dir, "node-a", "old-key"); err != nil || !forced {
		t.Fatalf("old state: %t, %v", forced, err)
	}
	if _, record, err := FindRebuild(dir); err != nil || record == nil || !record.Force {
		t.Fatalf("record = %+v, %v", record, err)
	}

	current := oldRaftDir(t)
	if err := WriteFormat(current); err != nil {
		t.Fatal(err)
	}
	if forced, err := ForceRebuild(current, "node-a", "old-key"); err != nil || forced {
		t.Fatalf("this build's state: %t, %v", forced, err)
	}
	if _, record, err := FindRebuild(current); err != nil || record != nil {
		t.Fatalf("record on this build's state = %+v, %v", record, err)
	}

	founded := oldRaftDir(t)
	record, err := NewRebuild(founded, "node-a", "old-key")
	if err != nil {
		t.Fatal(err)
	}
	record.Phase = RebuildFounded
	if err := record.Save(founded); err != nil {
		t.Fatal(err)
	}
	if forced, err := ForceRebuild(founded, "node-a", "old-key"); err != nil || forced {
		t.Fatalf("founded: %t, %v", forced, err)
	}
	if _, record, err := FindRebuild(founded); err != nil || record.Force {
		t.Fatalf("founded record = %+v, %v", record, err)
	}
}
