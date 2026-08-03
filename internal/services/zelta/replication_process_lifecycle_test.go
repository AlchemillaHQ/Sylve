// SPDX-License-Identifier: BSD-2-Clause

package zelta

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"gorm.io/gorm"
)

type processLifecycleVMStub struct {
	libvirtServiceInterfaces.LibvirtServiceInterface
	stopped []uint
}

func (s *processLifecycleVMStub) ForceStopVM(rid uint) error {
	s.stopped = append(s.stopped, rid)
	return nil
}

func seedReplicationProcessPolicy(t *testing.T, db *gorm.DB, policyID uint, nodeID string) {
	t.Helper()
	if err := db.AutoMigrate(&clusterModels.ReplicationGuestOperation{}); err != nil {
		t.Fatal(err)
	}
	policy := &clusterModels.ReplicationPolicy{
		ID: policyID, Name: "process-lifecycle",
		GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: policyID,
		SourceNodeID: nodeID, ActiveNodeID: nodeID, OwnerEpoch: 1,
		SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		Enabled:      true, ProtectionState: clusterModels.ReplicationProtectionStateInitializing,
		CronExpr: "*/5 * * * *",
	}
	if err := clusterModels.UpsertReplicationPolicyTxn(db, policy, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&clusterModels.ReplicationLease{
		PolicyID: policyID, GuestType: policy.GuestType, GuestID: policy.GuestID,
		OwnerNodeID: nodeID, OwnerEpoch: 1, Version: 1,
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestPrepareReplicationStartupRenewsBeforeProtectedAutostart(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	clusterSvc, localNodeID, cleanup := setupRaftClusterService(t)
	defer cleanup()
	seedReplicationProcessPolicy(t, clusterSvc.DB, 9201, localNodeID)

	service := NewService(clusterSvc.DB, nil, clusterSvc, nil, nil, nil, nil)
	service.localFilesystemDatasetLister = func(context.Context) ([]string, error) { return nil, nil }
	if err := service.PrepareReplicationStartup(t.Context()); err != nil {
		t.Fatal(err)
	}
	ready, err := service.CanAutostartReplicationGuest(clusterModels.ReplicationGuestTypeVM, 9201)
	if err != nil || !ready {
		t.Fatalf("protected guest readiness = %t, error = %v", ready, err)
	}
	ready, err = service.CanAutostartReplicationGuest(clusterModels.ReplicationGuestTypeVM, 9202)
	if err != nil || !ready {
		t.Fatalf("unprotected guest readiness = %t, error = %v", ready, err)
	}

	var lease clusterModels.ReplicationLease
	if err := clusterSvc.DB.Where("policy_id = ?", 9201).First(&lease).Error; err != nil {
		t.Fatal(err)
	}
	if lease.Version <= 1 {
		t.Fatalf("lease version = %d, want a post-start renewal", lease.Version)
	}

	vm := &processLifecycleVMStub{}
	service.VM = vm
	if err := service.FenceReplicationShutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(vm.stopped) != 1 || vm.stopped[0] != 9201 {
		t.Fatalf("shutdown stopped VMs = %v, want [9201]", vm.stopped)
	}
	ready, err = service.CanAutostartReplicationGuest(clusterModels.ReplicationGuestTypeVM, 9201)
	if err != nil || ready {
		t.Fatalf("shutdown left protected guest ready = %t, error = %v", ready, err)
	}
}

func TestPrepareReplicationStartupWithoutLeaderTimesOutFenced(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	database := newZeltaServiceTestDB(t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
	)
	const localNodeID = "partitioned-node"
	seedReplicationProcessPolicy(t, database, 9203, localNodeID)
	service := NewService(database, nil, &clusterService.Service{DB: database, NodeID: localNodeID}, nil, nil, nil, nil)
	service.localFilesystemDatasetLister = func(context.Context) ([]string, error) { return nil, nil }

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	err := service.PrepareReplicationStartup(ctx)
	if err == nil || !strings.Contains(err.Error(), "replication_startup_authority_timeout") {
		t.Fatalf("partitioned startup error = %v", err)
	}
	ready, readyErr := service.CanAutostartReplicationGuest(clusterModels.ReplicationGuestTypeVM, 9203)
	if readyErr != nil || ready {
		t.Fatalf("partitioned protected guest readiness = %t, error = %v", ready, readyErr)
	}
	ready, readyErr = service.CanAutostartReplicationGuest(clusterModels.ReplicationGuestTypeVM, 9204)
	if readyErr != nil || !ready {
		t.Fatalf("partitioned unprotected guest readiness = %t, error = %v", ready, readyErr)
	}
}

func TestPrepareReplicationStartupFenceFailureStaysClosed(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	database := newZeltaServiceTestDB(t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
	)
	const localNodeID = "fence-failure-node"
	seedReplicationProcessPolicy(t, database, 9205, localNodeID)
	service := NewService(database, nil, &clusterService.Service{DB: database, NodeID: localNodeID}, nil, nil, nil, nil)
	service.localFilesystemDatasetLister = func(context.Context) ([]string, error) {
		return nil, errors.New("zfs unavailable")
	}

	err := service.PrepareReplicationStartup(t.Context())
	if err == nil || !strings.Contains(err.Error(), "replication_startup_cold_fence_failed") {
		t.Fatalf("cold-fence error = %v", err)
	}
	ready, readyErr := service.CanAutostartReplicationGuest(clusterModels.ReplicationGuestTypeVM, 9205)
	if readyErr != nil || ready {
		t.Fatalf("fence failure left protected guest ready = %t, error = %v", ready, readyErr)
	}
}

func TestPrepareReplicationStartupFencesPolicyWithoutOwner(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	database := newZeltaServiceTestDB(t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
	)
	const localNodeID = "ownerless-policy-node"
	seedReplicationProcessPolicy(t, database, 9206, localNodeID)
	if err := database.Model(&clusterModels.ReplicationPolicy{}).Where("id = ?", 9206).
		Updates(map[string]any{"source_node_id": "", "active_node_id": ""}).Error; err != nil {
		t.Fatal(err)
	}
	vm := &processLifecycleVMStub{}
	service := NewService(database, nil, &clusterService.Service{DB: database, NodeID: localNodeID}, nil, nil, vm, nil)
	service.localFilesystemDatasetLister = func(context.Context) ([]string, error) { return nil, nil }

	if err := service.PrepareReplicationStartup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(vm.stopped) != 1 || vm.stopped[0] != 9206 {
		t.Fatalf("cold fence stopped VMs = %v, want [9206]", vm.stopped)
	}
	ready, err := service.CanAutostartReplicationGuest(clusterModels.ReplicationGuestTypeVM, 9206)
	if err != nil || ready {
		t.Fatalf("ownerless protected guest readiness = %t, error = %v", ready, err)
	}
}
