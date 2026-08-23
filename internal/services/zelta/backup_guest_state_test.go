// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"errors"
	"slices"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
)

type backupGuestStateJailStub struct {
	jailServiceInterfaces.JailServiceInterface
	ctID        uint
	running     bool
	stateErr    error
	stopErr     error
	startErr    error
	actions     []string
	lookupCalls int
}

func (s *backupGuestStateJailStub) GetJailCTIDFromDataset(string) (uint, error) {
	s.lookupCalls++
	return s.ctID, nil
}

func (s *backupGuestStateJailStub) IsJailRunning(uint) (bool, error) {
	return s.running, s.stateErr
}

func (s *backupGuestStateJailStub) JailAction(_ int, action string) error {
	s.actions = append(s.actions, action)
	if action == "stop" {
		return s.stopErr
	}
	return s.startErr
}

func TestQuiesceBackupGuestPreservesJailState(t *testing.T) {
	t.Parallel()

	job := &clusterModels.BackupJob{
		Mode:             clusterModels.BackupJobModeJail,
		JailRootDataset:  "tank/sylve/jails/100",
		StopBeforeBackup: true,
	}

	t.Run("already stopped remains stopped", func(t *testing.T) {
		stub := &backupGuestStateJailStub{ctID: 100}
		restore, stopped, err := (&Service{Jail: stub}).quiesceBackupGuest(job, 0)
		if err != nil || stopped || restore != nil {
			t.Fatalf("unexpected result: restore=%v stopped=%t err=%v", restore != nil, stopped, err)
		}
		if len(stub.actions) != 0 {
			t.Fatalf("already-stopped jail was mutated: %v", stub.actions)
		}
	})

	t.Run("running jail is stopped then restored", func(t *testing.T) {
		stub := &backupGuestStateJailStub{ctID: 100, running: true}
		restore, stopped, err := (&Service{Jail: stub}).quiesceBackupGuest(job, 0)
		if err != nil || !stopped || restore == nil {
			t.Fatalf("unexpected result: restore=%v stopped=%t err=%v", restore != nil, stopped, err)
		}
		if !slices.Equal(stub.actions, []string{"stop"}) {
			t.Fatalf("unexpected stop actions: %v", stub.actions)
		}
		if err := restore(); err != nil {
			t.Fatalf("restore running state: %v", err)
		}
		if !slices.Equal(stub.actions, []string{"stop", "start"}) {
			t.Fatalf("unexpected lifecycle actions: %v", stub.actions)
		}
	})

	t.Run("stop failure has no inverse", func(t *testing.T) {
		stub := &backupGuestStateJailStub{ctID: 100, running: true, stopErr: errors.New("stop failed")}
		restore, stopped, err := (&Service{Jail: stub}).quiesceBackupGuest(job, 0)
		if err == nil || stopped || restore != nil {
			t.Fatalf("unexpected result: restore=%v stopped=%t err=%v", restore != nil, stopped, err)
		}
		if !slices.Equal(stub.actions, []string{"stop"}) {
			t.Fatalf("unexpected lifecycle actions: %v", stub.actions)
		}
	})

	t.Run("restart failure is returned", func(t *testing.T) {
		stub := &backupGuestStateJailStub{ctID: 100, running: true, startErr: errors.New("start failed")}
		restore, stopped, err := (&Service{Jail: stub}).quiesceBackupGuest(job, 0)
		if err != nil || !stopped || restore == nil {
			t.Fatalf("unexpected result: restore=%v stopped=%t err=%v", restore != nil, stopped, err)
		}
		if err := restore(); err == nil || err.Error() != "start failed" {
			t.Fatalf("expected restart error, got %v", err)
		}
	})
}

func TestQuiesceBackupGuestDisabledDoesNotInspectGuest(t *testing.T) {
	t.Parallel()

	stub := &backupGuestStateJailStub{ctID: 100, running: true}
	job := &clusterModels.BackupJob{
		Mode:             clusterModels.BackupJobModeJail,
		JailRootDataset:  "tank/sylve/jails/100",
		StopBeforeBackup: false,
	}
	restore, stopped, err := (&Service{Jail: stub}).quiesceBackupGuest(job, 0)
	if err != nil || stopped || restore != nil || stub.lookupCalls != 0 || len(stub.actions) != 0 {
		t.Fatalf(
			"disabled quiesce touched guest: restore=%v stopped=%t err=%v lookups=%d actions=%v",
			restore != nil,
			stopped,
			err,
			stub.lookupCalls,
			stub.actions,
		)
	}
}
