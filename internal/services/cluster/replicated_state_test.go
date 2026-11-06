// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"gorm.io/gorm"
)

func replicatedStateTestModels() []any {
	return []any{
		&clusterModels.ClusterNote{},
		&clusterModels.ClusterOption{},
		&clusterModels.BackupTarget{},
		&clusterModels.BackupTargetProvisionOperation{},
		&clusterModels.BackupTargetNodeReadiness{},
		&clusterModels.BackupJob{},
		&clusterModels.BackupJobOperation{},
		&clusterModels.ReplicationRunOperation{},
		&clusterModels.ScheduledRunReceipt{},
		&clusterModels.ScheduledRunResultOutbox{},
		&clusterModels.BackupTargetRestoreOperation{},
		&clusterModels.BackupJobRunnerRebind{},
		&clusterModels.BackupJobRunnerRebindItem{},
		&clusterModels.BackupEvent{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationGuestOperation{},
		&clusterModels.ReplicationGuestOperationReceipt{},
		&clusterModels.ReplicationEvent{},
		&clusterModels.ReplicationTransitionEvent{},
		&clusterModels.ClusterSSHIdentity{},
		&clusterModels.EncryptionKey{},
	}
}

func TestIntegrationReplicatedStateDigestsMatchAcrossMembers(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3, replicatedStateTestModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	if err := leader.service.ProposeNoteCreate(t.Context(), "digest-note", "content"); err != nil {
		t.Fatal(err)
	}
	index := leader.service.AppliedIndex()
	reference, err := leader.service.LocalReplicatedStateDigest(leader.id)
	if err != nil || index == 0 || reference.AppliedIndex != index || reference.Digest == "" {
		t.Fatalf("leader digest = %+v, %v; want one at index %d", reference, err, index)
	}
	for _, node := range nodes {
		if _, err := node.service.WaitForAppliedIndex(t.Context(), index); err != nil {
			t.Fatalf("%s: %v", node.id, err)
		}
		digest, err := node.service.LocalReplicatedStateDigest(node.id)
		if err != nil || digest.AppliedIndex != index || digest.Digest != reference.Digest {
			t.Fatalf("%s digest = %+v, %v; want %+v", node.id, digest, err, reference)
		}
	}

	follower := nodes[0]
	if follower == leader {
		follower = nodes[1]
	}
	if err := follower.service.DB.Model(&clusterModels.ClusterNote{}).
		Where("title = ?", "digest-note").Update("content", "diverged").Error; err != nil {
		t.Fatal(err)
	}
	if digest, err := follower.service.LocalReplicatedStateDigest(follower.id); err != nil ||
		digest.AppliedIndex != index || digest.Digest == reference.Digest {
		t.Fatalf("diverged digest = %+v, %v; want a mismatch at index %d", digest, err, index)
	}
	if _, err := follower.service.LocalReplicatedStateDigest(leader.id); err == nil ||
		!strings.Contains(err.Error(), "replicated_state_node_id_mismatch") {
		t.Fatalf("digest for another node: %v", err)
	}
}

func TestIntegrationWaitForAppliedIndexWaitsForTheDatabase(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, replicatedStateTestModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	started, release := make(chan struct{}), make(chan struct{})
	clusterModels.On(leader.service.fsm, "test_hold_apply", "hold", func(*gorm.DB, *struct{}) error {
		close(started)
		<-release
		return nil
	})
	future := leader.raft.Apply(
		[]byte(`{"version":1,"decidedAt":"2026-09-24T00:00:00Z","type":"test_hold_apply","action":"hold","data":{}}`),
		10*time.Second,
	)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the held entry never reached the FSM")
	}
	index := leader.raft.LastIndex()
	waitForClusterCondition(t, 5*time.Second, "Raft to count the held entry applied", func() bool {
		return leader.raft.AppliedIndex() >= index
	})
	if applied := leader.service.AppliedIndex(); applied >= index {
		close(release)
		t.Fatalf("applied index = %d while the FSM holds entry %d", applied, index)
	}

	waited := make(chan error, 1)
	go func() {
		_, err := leader.service.WaitForAppliedIndex(t.Context(), index)
		waited <- err
	}()
	select {
	case err := <-waited:
		close(release)
		t.Fatalf("wait returned while the FSM held the entry: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := <-waited; err != nil {
		t.Fatal(err)
	}
	if err := future.Error(); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForAppliedIndexNeedsRaft(t *testing.T) {
	service := withFSM(&Service{DB: newClusterServiceTestDB(t)})
	if applied, err := service.WaitForAppliedIndex(t.Context(), 0); err != nil || applied != 0 {
		t.Fatalf("wait for index 0 = %d, %v", applied, err)
	}
	if _, err := service.WaitForAppliedIndex(t.Context(), 5); err == nil ||
		!strings.Contains(err.Error(), "replicated_state_raft_unavailable") {
		t.Fatalf("wait for index 5 without Raft: %v", err)
	}
}
