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

func TestRunInPlaceJailRestoreCutoverBlocksUnsafeLifecycleStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		stub      *restoreJailLifecycleStub
		wantError string
		wantSteps []string
	}{
		{
			name:      "identity lookup fails",
			stub:      &restoreJailLifecycleStub{lookupErr: errors.New("identity unavailable")},
			wantError: "restore_jail_identity_lookup_failed",
			wantSteps: []string{"lookup"},
		},
		{
			name:      "identity is invalid",
			stub:      &restoreJailLifecycleStub{},
			wantError: "restore_jail_identity_invalid",
			wantSteps: []string{"lookup"},
		},
		{
			name:      "runtime state lookup fails",
			stub:      &restoreJailLifecycleStub{ctID: 42, stateErr: errors.New("state unavailable")},
			wantError: "restore_jail_state_check_failed",
			wantSteps: []string{"lookup", "state"},
		},
		{
			name:      "stop fails",
			stub:      &restoreJailLifecycleStub{ctID: 42, running: true, stopErr: errors.New("stop failed")},
			wantError: "restore_jail_stop_failed",
			wantSteps: []string{"lookup", "state", "stop"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cutoverCalls := 0
			_, err := newRestoreJailLifecycleService(tt.stub).runInPlaceJailRestoreCutover(
				"tank/sylve/jails/42",
				func() (string, error) {
					cutoverCalls++
					return "tank/sylve/jails/42.old", nil
				},
			)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want %q", err, tt.wantError)
			}
			if cutoverCalls != 0 {
				t.Fatalf("unsafe lifecycle state reached cutover: calls=%d", cutoverCalls)
			}
			if !reflect.DeepEqual(tt.stub.steps, tt.wantSteps) {
				t.Fatalf("steps = %v, want %v", tt.stub.steps, tt.wantSteps)
			}
		})
	}
}

func TestRunInPlaceJailRestoreCutoverPreservesOriginalRunningStateOnFailure(t *testing.T) {
	t.Parallel()

	t.Run("initially stopped remains stopped", func(t *testing.T) {
		stub := &restoreJailLifecycleStub{ctID: 42}
		primary := errors.New("cutover failed")
		_, err := newRestoreJailLifecycleService(stub).runInPlaceJailRestoreCutover(
			"tank/sylve/jails/42",
			func() (string, error) {
				stub.steps = append(stub.steps, "cutover")
				return "", primary
			},
		)
		if !errors.Is(err, primary) {
			t.Fatalf("cutover error was not preserved: %v", err)
		}
		want := []string{"lookup", "state", "cutover"}
		if !reflect.DeepEqual(stub.steps, want) {
			t.Fatalf("steps = %v, want %v", stub.steps, want)
		}
	})

	t.Run("running jail is restarted", func(t *testing.T) {
		stub := &restoreJailLifecycleStub{ctID: 42, running: true}
		primary := errors.New("cutover failed")
		_, err := newRestoreJailLifecycleService(stub).runInPlaceJailRestoreCutover(
			"tank/sylve/jails/42",
			func() (string, error) {
				stub.steps = append(stub.steps, "cutover")
				return "", primary
			},
		)
		if !errors.Is(err, primary) {
			t.Fatalf("cutover error was not preserved: %v", err)
		}
		want := []string{"lookup", "state", "stop", "state", "cutover", "start"}
		if !reflect.DeepEqual(stub.steps, want) {
			t.Fatalf("steps = %v, want %v", stub.steps, want)
		}
	})

	t.Run("restart failure is joined", func(t *testing.T) {
		primary := errors.New("cutover failed")
		restart := errors.New("restart failed")
		stub := &restoreJailLifecycleStub{ctID: 42, running: true, restartErr: restart}
		_, err := newRestoreJailLifecycleService(stub).runInPlaceJailRestoreCutover(
			"tank/sylve/jails/42",
			func() (string, error) {
				stub.steps = append(stub.steps, "cutover")
				return "", primary
			},
		)
		if !errors.Is(err, primary) || !errors.Is(err, restart) ||
			!strings.Contains(err.Error(), "restore_jail_restart_failed") {
			t.Fatalf("joined error = %v", err)
		}
		want := []string{"lookup", "state", "stop", "state", "cutover", "start"}
		if !reflect.DeepEqual(stub.steps, want) {
			t.Fatalf("steps = %v, want %v", stub.steps, want)
		}
	})
}

func TestRunInPlaceJailRestoreCutoverSuccessLeavesJailStopped(t *testing.T) {
	t.Parallel()

	stub := &restoreJailLifecycleStub{ctID: 42, running: true}
	backup, err := newRestoreJailLifecycleService(stub).runInPlaceJailRestoreCutover(
		"tank/sylve/jails/42",
		func() (string, error) {
			stub.steps = append(stub.steps, "cutover")
			return "tank/sylve/jails/42_restore-backup-test", nil
		},
	)
	if err != nil {
		t.Fatalf("cutover: %v", err)
	}
	if backup != "tank/sylve/jails/42_restore-backup-test" {
		t.Fatalf("backup dataset = %q", backup)
	}
	want := []string{"lookup", "state", "stop", "state", "cutover"}
	if !reflect.DeepEqual(stub.steps, want) {
		t.Fatalf("steps = %v, want %v", stub.steps, want)
	}
}

func TestJailRestoreRuntimeGuardSpansFailuresAfterCutover(t *testing.T) {
	t.Parallel()

	stub := &restoreJailLifecycleStub{ctID: 42, running: true}
	guard, err := newRestoreJailLifecycleService(stub).prepareInPlaceJailRestore(t.Context(), "tank/sylve/jails/42")
	if err != nil {
		t.Fatalf("prepare jail restore: %v", err)
	}
	stub.steps = append(stub.steps, "cutover", "properties", "activation-failed")
	primary := errors.New("remote activation failed")
	joined, restartErr := guard.restoreAfterFailure(primary)
	if restartErr != nil || !errors.Is(joined, primary) {
		t.Fatalf("restore running state: joined=%v restart=%v", joined, restartErr)
	}
	// A second deferred/error-reporting pass must not start the guest twice.
	joined, restartErr = guard.restoreAfterFailure(joined)
	if restartErr != nil || !errors.Is(joined, primary) {
		t.Fatalf("second restore running state: joined=%v restart=%v", joined, restartErr)
	}
	want := []string{"lookup", "state", "stop", "state", "cutover", "properties", "activation-failed", "start"}
	if !reflect.DeepEqual(stub.steps, want) {
		t.Fatalf("steps = %v, want %v", stub.steps, want)
	}
}

func TestJailRestoreRuntimeGuardRemountsBeforeRestart(t *testing.T) {
	t.Parallel()

	var steps []string
	guard := &restoreRuntimeGuard{
		guestType: "jail",
		guestID:   42,
		dataset:   "tank/sylve/jails/42",
		remount: func() error {
			steps = append(steps, "mount")
			return nil
		},
		restart: func() error {
			steps = append(steps, "start")
			return nil
		},
	}

	primary := errors.New("cutover failed")
	joined, restartErr := guard.restoreAfterFailure(primary)
	if restartErr != nil || !errors.Is(joined, primary) {
		t.Fatalf("restore result: joined=%v restart=%v", joined, restartErr)
	}
	if want := []string{"mount", "start"}; !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %v, want %v", steps, want)
	}
}

func TestPrepareInPlaceJailRestoreWaitsForStopAndUsesNormalUnmount(t *testing.T) {
	t.Parallel()

	stub := &restoreJailLifecycleStub{ctID: 42, running: true}
	var unmountForces []bool
	service := newRestoreJailLifecycleService(stub)
	service.localDatasetUnmounter = func(_ context.Context, _ string, force bool) error {
		unmountForces = append(unmountForces, force)
		return nil
	}

	guard, err := service.prepareInPlaceJailRestore(t.Context(), "tank/sylve/jails/42")
	if err != nil {
		t.Fatalf("prepareInPlaceJailRestore: %v", err)
	}
	if guard == nil {
		t.Fatal("expected restore guard")
	}
	if !reflect.DeepEqual(unmountForces, []bool{false}) {
		t.Fatalf("unmount force values = %v, want [false]", unmountForces)
	}
	want := []string{"lookup", "state", "stop", "state"}
	if !reflect.DeepEqual(stub.steps, want) {
		t.Fatalf("steps = %v, want %v", stub.steps, want)
	}
}

func TestPrepareInPlaceJailRestoreRestartsAfterBusyUnmount(t *testing.T) {
	t.Parallel()

	stub := &restoreJailLifecycleStub{ctID: 42, running: true}
	service := newRestoreJailLifecycleService(stub)
	service.localDatasetUnmounter = func(context.Context, string, bool) error {
		return errors.New("pool or dataset is busy")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()

	guard, err := service.prepareInPlaceJailRestore(ctx, "tank/sylve/jails/42")
	if guard != nil {
		t.Fatal("busy unmount must not return a restore guard")
	}
	if err == nil || !strings.Contains(err.Error(), "restore_jail_dataset_busy") {
		t.Fatalf("error = %v, want restore_jail_dataset_busy", err)
	}
	want := []string{"lookup", "state", "stop", "state", "start"}
	if !reflect.DeepEqual(stub.steps, want) {
		t.Fatalf("steps = %v, want %v", stub.steps, want)
	}
}

func TestPrepareInPlaceJailRestoreRestartsAfterStopTimeout(t *testing.T) {
	t.Parallel()

	stub := &restoreJailLifecycleStub{ctID: 42, running: true, stopKeepsRunning: true}
	service := newRestoreJailLifecycleService(stub)
	service.localDatasetUnmounter = func(context.Context, string, bool) error {
		t.Fatal("unmount must not run while jail remains active")
		return nil
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()

	guard, err := service.prepareInPlaceJailRestore(ctx, "tank/sylve/jails/42")
	if guard != nil {
		t.Fatal("stop failure must not return a restore guard")
	}
	if err == nil || !strings.Contains(err.Error(), "restore_jail_stop_timeout") {
		t.Fatalf("error = %v, want restore_jail_stop_timeout", err)
	}
	want := []string{"lookup", "state", "stop", "state", "start"}
	if !reflect.DeepEqual(stub.steps, want) {
		t.Fatalf("steps = %v, want %v", stub.steps, want)
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

func TestVMRestoreFenceStopsAndBlocksMutations(t *testing.T) {
	vmStub := &restoreVMLifecycleStub{
		shutOffStates: []bool{false, true},
		actionErr:     make(map[string]error),
	}
	svc := &Service{
		DB: testutil.NewSQLiteTestDB(t, &clusterModels.ReplicationGuestOperation{}),
		VM: vmStub,
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

func TestVMRestoreRuntimeGuardRestoresRunningStateAfterLaterFailure(t *testing.T) {
	t.Parallel()

	database := testutil.NewSQLiteTestDB(
		t,
		&vmModels.VM{},
		&vmModels.Storage{},
		&vmModels.VMStorageDataset{},
		&vmModels.Network{},
		&vmModels.VMCPUPinning{},
	)
	if err := database.Create(&vmModels.VM{RID: 42, Name: "restore-runtime-vm"}).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}
	vmStub := &restoreVMLifecycleStub{
		// prepare checks once, stopVMIfPresent checks again, then its wait loop
		// observes the domain as shut off after the stop action.
		shutOffStates: []bool{false, false, true},
		actionErr:     make(map[string]error),
	}
	service := &Service{DB: database, VM: vmStub}
	guard, err := service.prepareInPlaceVMRestore(42, "tank/sylve/virtual-machines/42")
	if err != nil {
		t.Fatalf("prepare VM restore: %v", err)
	}
	if !reflect.DeepEqual(vmStub.actions, []string{"stop"}) {
		t.Fatalf("actions after prepare = %v", vmStub.actions)
	}

	primary := errors.New("second VM root failed")
	joined, restartErr := guard.restoreAfterFailure(primary)
	if restartErr != nil || !errors.Is(joined, primary) {
		t.Fatalf("restore running state: joined=%v restart=%v", joined, restartErr)
	}
	if !reflect.DeepEqual(vmStub.actions, []string{"stop", "start"}) {
		t.Fatalf("actions after failure = %v", vmStub.actions)
	}
}

func TestVMRestoreRuntimeGuardJoinsRestartFailure(t *testing.T) {
	t.Parallel()

	database := testutil.NewSQLiteTestDB(
		t,
		&vmModels.VM{},
		&vmModels.Storage{},
		&vmModels.VMStorageDataset{},
		&vmModels.Network{},
		&vmModels.VMCPUPinning{},
	)
	if err := database.Create(&vmModels.VM{RID: 43, Name: "restore-runtime-vm"}).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}
	restartFailure := errors.New("start failed")
	vmStub := &restoreVMLifecycleStub{
		shutOffStates: []bool{false, false, true},
		actionErr:     map[string]error{"start": restartFailure},
	}
	service := &Service{DB: database, VM: vmStub}
	guard, err := service.prepareInPlaceVMRestore(43, "tank/sylve/virtual-machines/43")
	if err != nil {
		t.Fatalf("prepare VM restore: %v", err)
	}
	primary := errors.New("restore failed")
	joined, restartErr := guard.restoreAfterFailure(primary)
	if !errors.Is(joined, primary) || !errors.Is(joined, restartFailure) ||
		restartErr == nil || !strings.Contains(joined.Error(), "restore_vm_restart_failed") {
		t.Fatalf("joined restart failure = %v (restart=%v)", joined, restartErr)
	}
}
