// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
)

type restoreJailLifecycleStub struct {
	jailServiceInterfaces.JailServiceInterface

	ctID             uint
	lookupErr        error
	running          bool
	stateErr         error
	stopErr          error
	stopKeepsRunning bool
	restartErr       error
	steps            []string
}

func (s *restoreJailLifecycleStub) GetJailCTIDFromDataset(string) (uint, error) {
	s.steps = append(s.steps, "lookup")
	return s.ctID, s.lookupErr
}

func (s *restoreJailLifecycleStub) IsJailRunning(uint) (bool, error) {
	s.steps = append(s.steps, "state")
	return s.running, s.stateErr
}

func (s *restoreJailLifecycleStub) JailAction(_ int, action string) error {
	s.steps = append(s.steps, action)
	if action == "stop" {
		if s.stopErr == nil && !s.stopKeepsRunning {
			s.running = false
		}
		return s.stopErr
	}
	if s.restartErr == nil {
		s.running = true
	}
	return s.restartErr
}

func (s *restoreJailLifecycleStub) ForceStopJail(uint) error {
	s.steps = append(s.steps, "force-stop")
	if s.stopErr == nil && !s.stopKeepsRunning {
		s.running = false
	}
	return s.stopErr
}

func newRestoreJailLifecycleService(stub *restoreJailLifecycleStub) *Service {
	return &Service{
		Jail: stub,
		localDatasetUnmounter: func(_ context.Context, _ string, force bool) error {
			if force {
				return errors.New("restore_cutover_must_not_force_unmount")
			}
			return nil
		},
		localDatasetMounter: func(context.Context, string) error {
			return nil
		},
	}
}

func TestJailRestoreFenceStopsAndBlocksMutations(t *testing.T) {
	stub := &restoreJailLifecycleStub{ctID: 42, running: true}
	svc := newRestoreJailLifecycleService(stub)
	svc.DB = testutil.NewSQLiteTestDB(t, &clusterModels.ReplicationGuestOperation{})
	svc.Cluster = newTestClusterService(svc.DB, "")

	fence, err := svc.acquireJailRestoreFence(t.Context(), "tank/sylve/jails/42", 9)
	if err != nil {
		t.Fatalf("acquire jail restore fence: %v", err)
	}
	if fence == nil || !fence.wasRunning || fence.guestID != 42 {
		t.Fatalf("expected running jail fence, got %+v", fence)
	}
	if stub.running {
		t.Fatal("restore fence did not stop the jail")
	}

	allowed, err := clusterService.CanNodeMutateProtectedGuest(
		svc.DB,
		clusterModels.ReplicationGuestTypeJail,
		42,
		"node-a",
	)
	if err != nil {
		t.Fatalf("check mutation guard: %v", err)
	}
	if allowed {
		t.Fatal("restore fence allowed a concurrent mutation")
	}
	if err := fence.release(); err != nil {
		t.Fatalf("release jail restore fence: %v", err)
	}

	var count int64
	if err := svc.DB.Model(&clusterModels.ReplicationGuestOperation{}).Count(&count).Error; err != nil {
		t.Fatalf("count restore fences: %v", err)
	}
	if count != 0 {
		t.Fatalf("released restore fence was retained: %d", count)
	}
}

func TestJailRestoreRuntimeGuardRemountsOnce(t *testing.T) {
	t.Parallel()

	mounts := 0
	guard := &restoreRuntimeGuard{
		guestID: 42,
		dataset: "tank/sylve/jails/42",
		remount: func() error {
			mounts++
			return nil
		},
	}
	primary := errors.New("remote activation failed")
	joined, remountErr := guard.restoreAfterFailure(primary)
	if remountErr != nil || !errors.Is(joined, primary) {
		t.Fatalf("restore after failure: joined=%v remount=%v", joined, remountErr)
	}
	// A second deferred/error-reporting pass must not mount twice.
	if _, remountErr = guard.restoreAfterFailure(joined); remountErr != nil || mounts != 1 {
		t.Fatalf("second pass: remount=%v mounts=%d", remountErr, mounts)
	}
}

func TestJailRestoreRuntimeGuardJoinsRemountFailure(t *testing.T) {
	t.Parallel()

	mountFailure := errors.New("mount failed")
	guard := &restoreRuntimeGuard{
		guestID: 42,
		dataset: "tank/sylve/jails/42",
		remount: func() error { return mountFailure },
	}
	primary := errors.New("cutover failed")
	joined, remountErr := guard.restoreAfterFailure(primary)
	if !errors.Is(joined, primary) || !errors.Is(joined, mountFailure) || !errors.Is(remountErr, mountFailure) ||
		!strings.Contains(joined.Error(), "restore_jail_remount_failed") {
		t.Fatalf("joined=%v remount=%v", joined, remountErr)
	}
}

func TestPrepareInPlaceJailRestoreUsesNormalUnmount(t *testing.T) {
	t.Parallel()

	stub := &restoreJailLifecycleStub{ctID: 42}
	var unmountForces []bool
	service := newRestoreJailLifecycleService(stub)
	service.localDatasetUnmounter = func(_ context.Context, _ string, force bool) error {
		unmountForces = append(unmountForces, force)
		return nil
	}

	guard, err := service.prepareInPlaceJailRestore(t.Context(), 42, "tank/sylve/jails/42")
	if err != nil || guard == nil {
		t.Fatalf("prepareInPlaceJailRestore: guard=%v err=%v", guard, err)
	}
	if !reflect.DeepEqual(unmountForces, []bool{false}) {
		t.Fatalf("unmount force values = %v, want [false]", unmountForces)
	}
	if len(stub.steps) != 0 {
		t.Fatalf("the fenced jail was touched: %v", stub.steps)
	}
}

func TestPrepareInPlaceJailRestoreFailsWhileDatasetBusy(t *testing.T) {
	t.Parallel()

	service := newRestoreJailLifecycleService(&restoreJailLifecycleStub{ctID: 42})
	service.localDatasetUnmounter = func(context.Context, string, bool) error {
		return errors.New("pool or dataset is busy")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()

	guard, err := service.prepareInPlaceJailRestore(ctx, 42, "tank/sylve/jails/42")
	if guard != nil {
		t.Fatal("busy unmount must not return a restore guard")
	}
	if err == nil || !strings.Contains(err.Error(), "restore_jail_dataset_busy") {
		t.Fatalf("error = %v, want restore_jail_dataset_busy", err)
	}
}

type restoreVMLifecycleStub struct {
	libvirtServiceInterfaces.LibvirtServiceInterface

	shutOffStates []bool
	stateErr      error
	actionErr     map[string]error
	actions       []string
	stateCalls    int
}

func (s *restoreVMLifecycleStub) IsDomainShutOff(uint) (bool, error) {
	s.stateCalls++
	if s.stateErr != nil {
		return false, s.stateErr
	}
	if len(s.shutOffStates) == 0 {
		return true, nil
	}
	idx := s.stateCalls - 1
	if idx >= len(s.shutOffStates) {
		idx = len(s.shutOffStates) - 1
	}
	return s.shutOffStates[idx], nil
}

func (s *restoreVMLifecycleStub) LvVMAction(_ vmModels.VM, action string) error {
	s.actions = append(s.actions, action)
	return s.actionErr[action]
}

func (s *restoreVMLifecycleStub) ForceStopVM(uint) error {
	s.actions = append(s.actions, "force-stop")
	return s.actionErr["force-stop"]
}

func TestVMRestoreFenceHoldsThroughFinalPlacementCheckAndBlocksMutations(t *testing.T) {
	vmStub := &restoreVMLifecycleStub{
		shutOffStates: []bool{false, true},
		actionErr:     make(map[string]error),
	}
	database := newZeltaServiceTestDB(
		t,
		&clusterModels.Cluster{},
		&clusterModels.ReplicationGuestOperation{},
		&vmModels.VM{},
		&jailModels.Jail{},
	)
	if err := database.Create(&clusterModels.Cluster{Enabled: false}).Error; err != nil {
		t.Fatalf("seed standalone cluster state: %v", err)
	}
	if err := database.Create(&vmModels.VM{RID: 42, Name: "restore-vm-42"}).Error; err != nil {
		t.Fatalf("seed restore VM: %v", err)
	}
	clusterSvc := newTestClusterService(database, "node-a")
	svc := &Service{
		DB:      database,
		Cluster: clusterSvc,
		VM:      vmStub,
	}

	fence, err := svc.acquireVMRestoreFence(t.Context(), 42, 9)
	if err != nil {
		t.Fatalf("acquire VM restore fence: %v", err)
	}
	if fence == nil || !fence.wasRunning || fence.guestID != 42 {
		t.Fatalf("expected running VM fence, got %+v", fence)
	}
	if want := []string{"force-stop"}; !reflect.DeepEqual(vmStub.actions, want) {
		t.Fatalf("VM actions = %v, want %v", vmStub.actions, want)
	}
	if err := svc.requireInPlaceGuestRestorePlacement(
		t.Context(),
		clusterModels.ReplicationGuestTypeVM,
		42,
	); err != nil {
		t.Fatalf("final placement check under restore fence: %v", err)
	}

	allowed, err := clusterService.CanNodeMutateProtectedGuest(
		svc.DB,
		clusterModels.ReplicationGuestTypeVM,
		42,
		"node-a",
	)
	if err != nil {
		t.Fatalf("check mutation guard: %v", err)
	}
	if allowed {
		t.Fatal("restore fence allowed a concurrent VM mutation")
	}
	if err := fence.release(); err != nil {
		t.Fatalf("release VM restore fence: %v", err)
	}
}
