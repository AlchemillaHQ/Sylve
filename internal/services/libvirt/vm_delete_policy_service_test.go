// SPDX-License-Identifier: BSD-2-Clause

package libvirt

import (
	"context"
	"fmt"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/db/replicationguard"
)

func enableVMDeletePolicySchema(t *testing.T, service *Service) {
	t.Helper()
	if err := service.DB.AutoMigrate(&clusterModels.ReplicationPolicy{}); err != nil {
		t.Fatalf("migrate replication policy: %v", err)
	}
	replicationguard.MarkPolicySchemaReady(service.DB)
}

func seedVMDeleteBackupJob(t *testing.T, service *Service, rid uint, enabled bool) {
	t.Helper()
	if err := service.DB.Create(&clusterModels.BackupJob{
		Name: "vm-delete-backup", TargetID: 1, Mode: clusterModels.BackupJobModeVM,
		SourceDataset: "tank/sylve/virtual-machines/" + fmt.Sprint(rid),
		CronExpr:      "0 * * * *", Enabled: enabled,
	}).Error; err != nil {
		t.Fatalf("seed backup job: %v", err)
	}
}

func seedVMDeletePolicy(t *testing.T, service *Service, rid uint) {
	t.Helper()
	if err := service.DB.Create(&clusterModels.ReplicationPolicy{
		Name:      "vm-delete-policy",
		GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID:   rid,
		Enabled:   false,
	}).Error; err != nil {
		t.Fatalf("seed replication policy: %v", err)
	}
}

func TestRemoveVMServiceBlocksDisabledPolicyBeforeRuntime(t *testing.T) {
	db := newVMDeleteTestDB(t)
	seed := seedVMDeleteGraph(t, db, 780, "tank", false)
	service := &Service{DB: db}
	enableVMDeletePolicySchema(t, service)
	seedVMDeletePolicy(t, service, seed.VM.RID)

	runtimeCalled := false
	_, err := service.removeVMWithWarnings(
		seed.VM.RID, false, false, false, t.Context(),
		func(uint) error {
			runtimeCalled = true
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "guest_delete_requires_replication_policy_removed") {
		t.Fatalf("delete error = %v", err)
	}
	if runtimeCalled {
		t.Fatal("runtime was touched before replication-policy rejection")
	}
}

func TestRemoveVMTransactionRevalidatesPolicy(t *testing.T) {
	db := newVMDeleteTestDB(t)
	seed := seedVMDeleteGraph(t, db, 781, "tank", false)
	service := &Service{DB: db}
	enableVMDeletePolicySchema(t, service)

	_, err := service.removeVMWithWarnings(
		seed.VM.RID, false, false, false, t.Context(),
		func(uint) error {
			seedVMDeletePolicy(t, service, seed.VM.RID)
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "guest_delete_requires_replication_policy_removed") {
		t.Fatalf("delete error = %v", err)
	}
	assertVMDeleteGraphCounts(t, db, seed, 1)
}

func TestRemoveVMServiceBlocksExplicitBackupJobsBeforeRuntime(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("enabled_%t", enabled), func(t *testing.T) {
			db := newVMDeleteTestDB(t)
			seed := seedVMDeleteGraph(t, db, 782, "tank", false)
			service := &Service{DB: db}
			seedVMDeleteBackupJob(t, service, seed.VM.RID, enabled)

			runtimeCalled := false
			_, err := service.removeVMWithWarnings(
				seed.VM.RID, false, false, false, t.Context(),
				func(uint) error {
					runtimeCalled = true
					return nil
				},
			)
			if err == nil || !strings.Contains(err.Error(), "guest_delete_requires_backup_jobs_removed") {
				t.Fatalf("delete error = %v", err)
			}
			if runtimeCalled {
				t.Fatal("runtime was touched before backup-job rejection")
			}
		})
	}
}

func TestRemoveVMTransactionRevalidatesBackupJobs(t *testing.T) {
	db := newVMDeleteTestDB(t)
	seed := seedVMDeleteGraph(t, db, 783, "tank", false)
	service := &Service{DB: db}

	_, err := service.removeVMWithWarnings(
		seed.VM.RID, false, false, false, t.Context(),
		func(uint) error {
			seedVMDeleteBackupJob(t, service, seed.VM.RID, false)
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "guest_delete_requires_backup_jobs_removed") {
		t.Fatalf("delete error = %v", err)
	}
	assertVMDeleteGraphCounts(t, db, seed, 1)
}

func TestEveryVMDeleteEntryPointBlocksExplicitBackupJob(t *testing.T) {
	entryPoints := []struct {
		name string
		run  func(*Service, uint) error
	}{
		{
			name: "normal",
			run: func(service *Service, rid uint) error {
				_, err := service.RemoveVMWithWarnings(rid, false, false, false, context.Background())
				return err
			},
		},
		{
			name: "force",
			run: func(service *Service, rid uint) error {
				_, err := service.ForceRemoveVM(rid, false, context.Background())
				return err
			},
		},
		{
			name: "registration purge",
			run: func(service *Service, rid uint) error {
				_, err := service.PurgeVMRegistration(rid, false)
				return err
			},
		},
	}

	for index, entryPoint := range entryPoints {
		t.Run(entryPoint.name, func(t *testing.T) {
			db := newVMDeleteTestDB(t)
			rid := uint(790 + index)
			seed := seedVMDeleteGraph(t, db, rid, "tank", false)
			service := &Service{DB: db, uri: "://"}
			seedVMDeleteBackupJob(t, service, rid, false)

			err := entryPoint.run(service, rid)
			if err == nil || !strings.Contains(err.Error(), "guest_delete_requires_backup_jobs_removed") {
				t.Fatalf("delete error = %v", err)
			}
			assertVMDeleteGraphCounts(t, db, seed, 1)
		})
	}
}
