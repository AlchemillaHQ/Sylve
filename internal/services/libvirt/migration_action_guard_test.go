// SPDX-License-Identifier: BSD-2-Clause

package libvirt

import (
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestMigrationVMActionAuthorizationSurvivesInnerActionRecheck(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &clusterModels.ReplicationGuestOperation{})
	const rid = uint(731)
	if err := db.Create(&clusterModels.ReplicationGuestOperation{
		GuestType:    clusterModels.ReplicationGuestTypeVM,
		GuestID:      rid,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		State:        clusterModels.ReplicationGuestOperationCutover,
		Token:        "migration:source-node:731",
		OwnerNodeID:  "source-node",
		TargetNodeID: "target-node",
	}).Error; err != nil {
		t.Fatalf("seed migration guard: %v", err)
	}

	tests := []struct {
		name   string
		nodeID string
		action string
		want   bool
	}{
		{name: "source may stop", nodeID: "source-node", action: "stop", want: true},
		{name: "source may not restart", nodeID: "source-node", action: "start", want: false},
		{name: "source may not reboot", nodeID: "source-node", action: "reboot", want: false},
		{name: "target may start", nodeID: "target-node", action: "start", want: true},
		{name: "target may not stop source", nodeID: "target-node", action: "stop", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			allowed, err := canNodePerformVMAction(db, rid, test.action, test.nodeID)
			if err != nil {
				t.Fatalf("authorize %s: %v", test.action, err)
			}
			if allowed != test.want {
				t.Fatalf("allowed = %t, want %t", allowed, test.want)
			}
		})
	}
}
