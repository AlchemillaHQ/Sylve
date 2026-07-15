// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestRunRestoreJobRejectsPersistedNoncanonicalGuestRootsBeforeReceive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		seed      func(t *testing.T, service *Service)
		job       clusterModels.BackupJob
		wantError string
	}{
		{
			name: "broad jail root",
			seed: func(t *testing.T, service *Service) {
				jail := jailModels.Jail{CTID: 100, Name: "restore-guard-jail", Type: jailModels.JailTypeFreeBSD}
				if err := service.DB.Create(&jail).Error; err != nil {
					t.Fatalf("create jail: %v", err)
				}
				if err := service.DB.Create(&jailModels.Storage{
					JailID: jail.ID,
					Pool:   "zroot",
					GUID:   "restore-guard-jail-guid",
					Name:   "Base Filesystem",
					IsBase: true,
				}).Error; err != nil {
					t.Fatalf("create jail storage: %v", err)
				}
			},
			job: clusterModels.BackupJob{
				ID:              101,
				Mode:            clusterModels.BackupJobModeJail,
				JailRootDataset: "zroot/sylve",
				Recursive:       true,
			},
			wantError: "jail_backup_requires_registered_canonical_root",
		},
		{
			name: "noncanonical VM root",
			seed: func(t *testing.T, service *Service) {
				vm := vmModels.VM{RID: 200, Name: "restore-guard-vm"}
				if err := service.DB.Create(&vm).Error; err != nil {
					t.Fatalf("create VM: %v", err)
				}
				dataset := vmModels.VMStorageDataset{
					Pool: "fast",
					Name: "fast/sylve/virtual-machines/200/zvol-1",
					GUID: "restore-guard-vm-guid",
				}
				if err := service.DB.Create(&dataset).Error; err != nil {
					t.Fatalf("create VM storage dataset: %v", err)
				}
				if err := service.DB.Create(&vmModels.Storage{
					VMID:      vm.ID,
					Type:      vmModels.VMStorageTypeZVol,
					Pool:      "fast",
					Enable:    true,
					DatasetID: &dataset.ID,
				}).Error; err != nil {
					t.Fatalf("create VM storage: %v", err)
				}
			},
			job: clusterModels.BackupJob{
				ID:            102,
				Mode:          clusterModels.BackupJobModeVM,
				SourceDataset: "fast/sylve",
				Recursive:     true,
			},
			wantError: "vm_backup_requires_registered_canonical_root",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database := testutil.NewSQLiteTestDB(
				t,
				&clusterModels.BackupTarget{},
				&clusterModels.BackupJob{},
				&clusterModels.BackupEvent{},
				&jailModels.Jail{},
				&jailModels.Storage{},
				&vmModels.VM{},
				&vmModels.Storage{},
				&vmModels.VMStorageDataset{},
			)
			service := &Service{
				DB:          database,
				runningJobs: make(map[uint]struct{}),
			}
			tt.seed(t, service)

			err := service.runRestoreJob(context.Background(), &tt.job, "@must-not-receive", "")
			if err == nil || !strings.Contains(err.Error(), tt.wantError) ||
				!strings.Contains(err.Error(), "restore_backup_job_safety_check_failed") {
				t.Fatalf("restore safety error = %v, want %q", err, tt.wantError)
			}
			if len(service.runningJobs) != 0 {
				t.Fatalf("restore job lock leaked after rejection: %v", service.runningJobs)
			}
			var eventCount int64
			if err := database.Model(&clusterModels.BackupEvent{}).Count(&eventCount).Error; err != nil {
				t.Fatalf("count restore events: %v", err)
			}
			if eventCount != 0 {
				t.Fatalf("unsafe restore reached event/receive setup: events=%d", eventCount)
			}
		})
	}
}
