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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
)

type forwardingAuthStub struct {
	serviceInterfaces.AuthServiceInterface
}

func (forwardingAuthStub) CreateInternalClusterJWT(_, _ string) (string, error) {
	return "test-cluster-token", nil
}

func backupTargetsForNode(node *clusterRaftTestNode) ([]clusterModels.BackupTarget, error) {
	var targets []clusterModels.BackupTarget
	err := node.service.DB.Order("id ASC").Find(&targets).Error
	return targets, err
}

func backupJobsForNode(node *clusterRaftTestNode) ([]clusterModels.BackupJob, error) {
	var jobs []clusterModels.BackupJob
	err := node.service.DB.Order("id ASC").Find(&jobs).Error
	return jobs, err
}

func TestRaftBackupTargetsReplicationTwoNodes(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	err := leader.service.ProposeBackupTargetCreate(clusterServiceInterfaces.BackupTargetReq{
		Name:             "target-one",
		SSHHost:          "user@host-one",
		SSHPort:          0,
		SSHKey:           "key-one",
		BackupRoot:       "tank/backups-one",
		CreateBackupRoot: boolPtr(true),
		Description:      "first target",
		Enabled:          boolPtr(true),
	}, false)
	if err != nil {
		t.Fatalf("leader failed to create backup target through raft: %v", err)
	}

	targetID := uint(0)
	waitForClusterCondition(t, 8*time.Second, "backup target create replication to 2 nodes", func() bool {
		for _, node := range nodes {
			targets, queryErr := backupTargetsForNode(node)
			if queryErr != nil || len(targets) != 1 {
				return false
			}
			if targets[0].Name != "target-one" || targets[0].SSHHost != "user@host-one" || targets[0].BackupRoot != "tank/backups-one" {
				return false
			}
			if targets[0].SSHPort != 22 || !targets[0].CreateBackupRoot || !targets[0].Enabled {
				return false
			}
			if targetID == 0 {
				targetID = targets[0].ID
			}
			if targets[0].ID != targetID {
				return false
			}
		}
		return targetID > 0
	})

	leader = waitForClusterRaftLeader(t, nodes, 8*time.Second)
	err = leader.service.ProposeBackupTargetUpdate(clusterServiceInterfaces.BackupTargetReq{
		ID:               targetID,
		Name:             "target-one-updated",
		SSHHost:          "user@host-two",
		SSHPort:          2022,
		SSHKey:           "key-two",
		BackupRoot:       "tank/backups-two",
		CreateBackupRoot: boolPtr(false),
		Description:      "updated target",
		Enabled:          boolPtr(false),
	}, false)
	if err != nil {
		t.Fatalf("leader failed to update backup target through raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "backup target update replication to 2 nodes", func() bool {
		for _, node := range nodes {
			targets, queryErr := backupTargetsForNode(node)
			if queryErr != nil || len(targets) != 1 {
				return false
			}
			target := targets[0]
			if target.Name != "target-one-updated" || target.SSHHost != "user@host-two" || target.SSHPort != 2022 ||
				target.BackupRoot != "tank/backups-two" || target.CreateBackupRoot || target.Enabled {
				return false
			}
		}
		return true
	})

	leader = waitForClusterRaftLeader(t, nodes, 8*time.Second)
	if err := leader.service.ProposeBackupTargetDelete(targetID, false); err != nil {
		t.Fatalf("leader failed to delete backup target through raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "backup target delete replication to 2 nodes", func() bool {
		for _, node := range nodes {
			targets, queryErr := backupTargetsForNode(node)
			if queryErr != nil || len(targets) != 0 {
				return false
			}
		}
		return true
	})
}

func TestRaftBackupTargetsReplicationThreeNodes(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	if err := leader.service.ProposeBackupTargetCreate(clusterServiceInterfaces.BackupTargetReq{
		Name:       "target-three",
		SSHHost:    "user@host-three",
		BackupRoot: "tank/three",
		SSHKey:     "key-three",
		Enabled:    boolPtr(true),
	}, false); err != nil {
		t.Fatalf("leader failed to create backup target through raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "backup target replication to 3 nodes", func() bool {
		for _, node := range nodes {
			targets, queryErr := backupTargetsForNode(node)
			if queryErr != nil || len(targets) != 1 {
				return false
			}
			if targets[0].Name != "target-three" || targets[0].SSHHost != "user@host-three" || targets[0].BackupRoot != "tank/three" {
				return false
			}
		}
		return true
	})
}

func TestRaftBackupTargetsThreeNodeFailover(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	initialLeader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	if err := initialLeader.service.ProposeBackupTargetCreate(clusterServiceInterfaces.BackupTargetReq{
		Name:       "before-failover",
		SSHHost:    "user@host-before",
		BackupRoot: "tank/before",
	}, false); err != nil {
		t.Fatalf("initial leader failed to create backup target: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "initial backup target replication before failover", func() bool {
		for _, node := range nodes {
			targets, queryErr := backupTargetsForNode(node)
			if queryErr != nil || len(targets) != 1 {
				return false
			}
			if targets[0].Name != "before-failover" {
				return false
			}
		}
		return true
	})

	survivors := make([]*clusterRaftTestNode, 0, len(nodes)-1)
	for _, node := range nodes {
		if node.id != initialLeader.id {
			survivors = append(survivors, node)
		}
	}

	for _, node := range survivors {
		node.transport.Disconnect(initialLeader.addr)
	}
	initialLeader.transport.DisconnectAll()
	if err := initialLeader.raft.Shutdown().Error(); err != nil {
		t.Fatalf("failed to shutdown initial leader: %v", err)
	}

	newLeader := waitForClusterRaftLeader(t, survivors, 12*time.Second)
	if err := newLeader.service.ProposeBackupTargetCreate(clusterServiceInterfaces.BackupTargetReq{
		Name:       "after-failover",
		SSHHost:    "user@host-after",
		BackupRoot: "tank/after",
	}, false); err != nil {
		t.Fatalf("new leader failed to create backup target after failover: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "post-failover backup target replication on surviving quorum", func() bool {
		for _, node := range survivors {
			targets, queryErr := backupTargetsForNode(node)
			if queryErr != nil || len(targets) != 2 {
				return false
			}
			nameSet := map[string]struct{}{
				targets[0].Name: {},
				targets[1].Name: {},
			}
			if _, ok := nameSet["before-failover"]; !ok {
				return false
			}
			if _, ok := nameSet["after-failover"]; !ok {
				return false
			}
		}
		return true
	})
}

func TestRaftBackupTargetDeleteBlockedWhenInUse(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	if err := leader.service.ProposeBackupTargetCreate(clusterServiceInterfaces.BackupTargetReq{
		Name:       "delete-blocked",
		SSHHost:    "user@host",
		BackupRoot: "tank/delete-blocked",
	}, false); err != nil {
		t.Fatalf("leader failed to create backup target: %v", err)
	}

	targetID := uint(0)
	waitForClusterCondition(t, 8*time.Second, "backup target create replication before delete-in-use check", func() bool {
		targets, queryErr := backupTargetsForNode(nodes[0])
		if queryErr != nil || len(targets) != 1 {
			return false
		}
		targetID = targets[0].ID
		return targetID > 0
	})

	for _, node := range nodes {
		if err := node.service.DB.Create(&clusterModels.BackupJob{
			ID:            1,
			Name:          "job-1",
			TargetID:      targetID,
			Mode:          clusterModels.BackupJobModeDataset,
			SourceDataset: "tank/source",
			CronExpr:      "* * * * *",
			Enabled:       true,
		}).Error; err != nil {
			t.Fatalf("failed to seed backup job on node %s: %v", node.id, err)
		}
	}

	err := leader.service.ProposeBackupTargetDelete(targetID, false)
	if err == nil {
		t.Fatal("expected delete-in-use raft error, got nil")
	}
	if !strings.Contains(err.Error(), "target_in_use_by_backup_jobs") {
		t.Fatalf("expected target_in_use_by_backup_jobs error, got: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "backup target remains on all nodes after blocked raft delete", func() bool {
		for _, node := range nodes {
			targets, queryErr := backupTargetsForNode(node)
			if queryErr != nil || len(targets) != 1 {
				return false
			}
			if targets[0].ID != targetID {
				return false
			}
		}
		return true
	})
}

func TestRaftBackupJobFriendlySourceSyncReplicatesAcrossNodes(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	job := clusterModels.BackupJob{
		ID:            31,
		Name:          "vm-backup-job",
		TargetID:      1,
		Mode:          clusterModels.BackupJobModeVM,
		SourceDataset: "zroot/sylve/virtual-machines/444",
		FriendlySrc:   "vm-old",
		CronExpr:      "* * * * *",
		Enabled:       true,
	}
	jobRaw, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("failed marshaling backup job: %v", err)
	}

	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type:   "backup_job",
		Action: "create",
		Data:   jobRaw,
	}); err != nil {
		t.Fatalf("failed creating backup job through raft command: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "backup job create replication before friendly source sync", func() bool {
		for _, node := range nodes {
			jobs, queryErr := backupJobsForNode(node)
			if queryErr != nil || len(jobs) != 1 {
				return false
			}
			if jobs[0].FriendlySrc != "vm-old" {
				return false
			}
		}
		return true
	})

	if err := leader.service.SyncBackupJobFriendlySourceByGuest(BackupJobFriendlySourceUpdate{
		GuestType:   clusterModels.ReplicationGuestTypeVM,
		GuestID:     444,
		FriendlySrc: "vm-new",
	}, false); err != nil {
		t.Fatalf("friendly source sync failed: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "backup job friendly source sync replication to 2 nodes", func() bool {
		for _, node := range nodes {
			jobs, queryErr := backupJobsForNode(node)
			if queryErr != nil || len(jobs) != 1 {
				return false
			}
			if jobs[0].FriendlySrc != "vm-new" {
				return false
			}
		}
		return true
	})
}

func TestBackupFriendlySourceSyncForwardsFromFollowerToLeaderEndpoint(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{}, &clusterModels.ClusterNode{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	var follower *clusterRaftTestNode
	for _, node := range nodes {
		if node.id != leader.id {
			follower = node
			break
		}
	}
	if follower == nil {
		t.Fatal("expected follower node")
	}

	job := clusterModels.BackupJob{
		ID:            41,
		Name:          "vm-forward-job",
		TargetID:      1,
		Mode:          clusterModels.BackupJobModeVM,
		SourceDataset: "zroot/sylve/virtual-machines/777",
		FriendlySrc:   "vm-old",
		CronExpr:      "* * * * *",
		Enabled:       true,
	}
	jobRaw, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("failed marshaling backup job: %v", err)
	}
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type:   "backup_job",
		Action: "create",
		Data:   jobRaw,
	}); err != nil {
		t.Fatalf("failed creating backup job through raft command: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "backup job replication before follower forward test", func() bool {
		for _, node := range nodes {
			jobs, queryErr := backupJobsForNode(node)
			if queryErr != nil || len(jobs) != 1 {
				return false
			}
		}
		return true
	})

	var forwardedPayload BackupJobFriendlySourceUpdate
	forwardCalled := false
	forwardServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/intra-cluster/backup-job-friendly-source" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method_not_allowed", http.StatusMethodNotAllowed)
			return
		}

		forwardCalled = true
		if err := json.NewDecoder(r.Body).Decode(&forwardedPayload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer forwardServer.Close()

	leaderAPI := strings.TrimPrefix(forwardServer.URL, "https://")
	if err := follower.service.DB.Create(&clusterModels.ClusterNode{
		NodeUUID: leader.id,
		API:      leaderAPI,
	}).Error; err != nil {
		t.Fatalf("failed seeding cluster node api for forwarding: %v", err)
	}
	follower.service.AuthService = forwardingAuthStub{}

	err = follower.service.SyncBackupJobFriendlySourceByGuestClusterWide(BackupJobFriendlySourceUpdate{
		GuestType:   clusterModels.ReplicationGuestTypeVM,
		GuestID:     777,
		FriendlySrc: "vm-forwarded",
	})
	if err != nil {
		t.Fatalf("expected follower forward to succeed, got %v", err)
	}

	if !forwardCalled {
		t.Fatal("expected follower sync to call leader forwarding endpoint")
	}
	if forwardedPayload.GuestType != clusterModels.ReplicationGuestTypeVM ||
		forwardedPayload.GuestID != 777 ||
		forwardedPayload.FriendlySrc != "vm-forwarded" {
		t.Fatalf("unexpected forwarded payload: %+v", forwardedPayload)
	}
}
