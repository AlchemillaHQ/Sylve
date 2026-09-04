// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
)

type cancellationAwareBackupJailStub struct {
	jailServiceInterfaces.JailServiceInterface
	actions     []string
	startCtxErr error
}

func (s *cancellationAwareBackupJailStub) GetJailCTIDFromDataset(string) (uint, error) {
	return 100, nil
}

func (s *cancellationAwareBackupJailStub) IsJailRunning(uint) (bool, error) {
	return true, nil
}

func (s *cancellationAwareBackupJailStub) JailActionContext(ctx context.Context, _ int, action string) error {
	s.actions = append(s.actions, action)
	if action == "start" {
		s.startCtxErr = ctx.Err()
	}
	return nil
}

func TestQuiescedGuestRestartSurvivesJobCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stub := &cancellationAwareBackupJailStub{}
	job := &clusterModels.BackupJob{
		Mode:             clusterModels.BackupJobModeJail,
		JailRootDataset:  "tank/sylve/jails/100",
		StopBeforeBackup: true,
	}
	restore, stopped, err := (&Service{Jail: stub}).quiesceBackupGuestContext(ctx, job, 0)
	if err != nil || !stopped || restore == nil {
		t.Fatalf("quiesce result: stopped=%t restore=%v err=%v", stopped, restore != nil, err)
	}

	cancel()
	if err := restore(); err != nil {
		t.Fatalf("restore guest state: %v", err)
	}
	if stub.startCtxErr != nil {
		t.Fatalf("restart inherited canceled job context: %v", stub.startCtxErr)
	}
	if len(stub.actions) != 2 || stub.actions[0] != "stop" || stub.actions[1] != "start" {
		t.Fatalf("unexpected guest actions: %v", stub.actions)
	}
}
