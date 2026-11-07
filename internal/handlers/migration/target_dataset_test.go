// SPDX-License-Identifier: BSD-2-Clause

package migrationHandlers

import (
	"context"
	"slices"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	migrationService "github.com/alchemillahq/sylve/internal/services/migration"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestWriteTargetDatasetRequiresMigrationToThisNode(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &clusterModels.ReplicationGuestOperation{})
	node := cluster.NewClusterService(db, nil, nil).(*cluster.Service)
	node.NodeID = "node-b"
	var written []string
	write := writeTargetDataset(&zelta.Service{DB: db, Cluster: node}, func(_ context.Context, dataset string) error {
		written = append(written, dataset)
		return nil
	})
	req := migrationService.TargetDatasetRequest{
		GuestType: "vm", GuestID: 901, OperationToken: "migration:node-a:901",
		Dataset: "tank/sylve/virtual-machines/901.merge-1",
	}

	if _, err := write(t.Context(), "", req); err == nil {
		t.Fatal("write without a migration to this node was allowed")
	}

	if err := db.Create(&clusterModels.ReplicationGuestOperation{
		GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 901,
		Operation: clusterModels.ReplicationGuestOperationMigration,
		State:     clusterModels.ReplicationGuestOperationPreCutover,
		Token:     req.OperationToken, OwnerNodeID: "node-a", TargetNodeID: "node-b",
		TaskID: 901, AcquiredAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := write(t.Context(), "", req); err != nil {
		t.Fatalf("write during the migration was refused: %v", err)
	}

	for name, bad := range map[string]func(*migrationService.TargetDatasetRequest){
		"other guest's root": func(r *migrationService.TargetDatasetRequest) { r.Dataset = "tank/sylve/virtual-machines/902" },
		"not a guest root":   func(r *migrationService.TargetDatasetRequest) { r.Dataset = "tank/data" },
		"guest's child":      func(r *migrationService.TargetDatasetRequest) { r.Dataset = "tank/sylve/virtual-machines/901/raw-1" },
		"stale token":        func(r *migrationService.TargetDatasetRequest) { r.OperationToken = "migration:node-a:900" },
	} {
		request := req
		bad(&request)
		if _, err := write(t.Context(), "", request); err == nil {
			t.Errorf("%s: write was allowed", name)
		}
	}
	if !slices.Equal(written, []string{req.Dataset}) {
		t.Fatalf("written = %v, want only %s", written, req.Dataset)
	}
}
