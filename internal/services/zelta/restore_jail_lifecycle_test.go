// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
)

type restoreJailLifecycleStub struct {
	jailServiceInterfaces.JailServiceInterface

	ctID       uint
	lookupErr  error
	running    bool
	stateErr   error
	stopErr    error
	restartErr error
	steps      []string
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
		return s.stopErr
	}
	return s.restartErr
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
			_, err := (&Service{Jail: tt.stub}).runInPlaceJailRestoreCutover(
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
		_, err := (&Service{Jail: stub}).runInPlaceJailRestoreCutover(
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
		_, err := (&Service{Jail: stub}).runInPlaceJailRestoreCutover(
			"tank/sylve/jails/42",
			func() (string, error) {
				stub.steps = append(stub.steps, "cutover")
				return "", primary
			},
		)
		if !errors.Is(err, primary) {
			t.Fatalf("cutover error was not preserved: %v", err)
		}
		want := []string{"lookup", "state", "stop", "cutover", "start"}
		if !reflect.DeepEqual(stub.steps, want) {
			t.Fatalf("steps = %v, want %v", stub.steps, want)
		}
	})

	t.Run("restart failure is joined", func(t *testing.T) {
		primary := errors.New("cutover failed")
		restart := errors.New("restart failed")
		stub := &restoreJailLifecycleStub{ctID: 42, running: true, restartErr: restart}
		_, err := (&Service{Jail: stub}).runInPlaceJailRestoreCutover(
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
		want := []string{"lookup", "state", "stop", "cutover", "start"}
		if !reflect.DeepEqual(stub.steps, want) {
			t.Fatalf("steps = %v, want %v", stub.steps, want)
		}
	})
}

func TestRunInPlaceJailRestoreCutoverSuccessLeavesJailStopped(t *testing.T) {
	t.Parallel()

	stub := &restoreJailLifecycleStub{ctID: 42, running: true}
	backup, err := (&Service{Jail: stub}).runInPlaceJailRestoreCutover(
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
	want := []string{"lookup", "state", "stop", "cutover"}
	if !reflect.DeepEqual(stub.steps, want) {
		t.Fatalf("steps = %v, want %v", stub.steps, want)
	}
}

func TestJailRestoreRuntimeGuardSpansFailuresAfterCutover(t *testing.T) {
	t.Parallel()

	stub := &restoreJailLifecycleStub{ctID: 42, running: true}
	guard, err := (&Service{Jail: stub}).prepareInPlaceJailRestore("tank/sylve/jails/42")
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
	want := []string{"lookup", "state", "stop", "cutover", "properties", "activation-failed", "start"}
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
