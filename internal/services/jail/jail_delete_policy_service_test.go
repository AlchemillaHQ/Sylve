// SPDX-License-Identifier: BSD-2-Clause

package jail

import (
	"fmt"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/db/replicationguard"
)

func enableJailDeletePolicySchema(t *testing.T, service *Service) {
	t.Helper()
	if err := service.DB.AutoMigrate(&clusterModels.ReplicationPolicy{}); err != nil {
		t.Fatalf("migrate replication policy: %v", err)
	}
	replicationguard.MarkPolicySchemaReady(service.DB)
}

func seedJailDeleteBackupJob(t *testing.T, service *Service, ctID uint, enabled bool) {
	t.Helper()
	if err := service.DB.Create(&clusterModels.BackupJob{
		Name: "jail-delete-backup", TargetID: 1, Mode: clusterModels.BackupJobModeJail,
		JailRootDataset: fmt.Sprintf("tank/sylve/jails/%d", ctID),
		CronExpr:        "0 * * * *", Enabled: enabled,
	}).Error; err != nil {
		t.Fatalf("seed backup job: %v", err)
	}
}

func seedJailDeletePolicy(t *testing.T, service *Service, ctID uint, enabled bool) {
	t.Helper()
	if err := service.DB.Create(&clusterModels.ReplicationPolicy{
		Name:      "jail-delete-policy",
		GuestType: clusterModels.ReplicationGuestTypeJail,
		GuestID:   ctID,
		Enabled:   enabled,
	}).Error; err != nil {
		t.Fatalf("seed replication policy: %v", err)
	}
}

func TestDeleteJailServiceBlocksDisabledPolicyBeforeRuntime(t *testing.T) {
	db := newJailDeleteTestDB(t)
	const ctID uint = 681
	seedJailDeleteGraph(t, db, ctID, "tank", false)
	service := &Service{DB: db}
	enableJailDeletePolicySchema(t, service)
	seedJailDeletePolicy(t, service, ctID, false)

	runtimeCalled := false
	runtime := inactiveJailDeleteRuntime()
	runtime.isRunning = func(uint) (bool, error) {
		runtimeCalled = true
		return false, nil
	}
	_, err := service.deleteJailWithRuntime(t.Context(), ctID, false, false, runtime)
	if err == nil || !strings.Contains(err.Error(), "guest_delete_requires_replication_policy_removed") {
		t.Fatalf("delete error = %v", err)
	}
	if runtimeCalled {
		t.Fatal("runtime was touched before replication-policy rejection")
	}
}

func TestDeleteJailTransactionRevalidatesPolicy(t *testing.T) {
	db := newJailDeleteTestDB(t)
	const ctID uint = 682
	jailID, _ := seedJailDeleteGraph(t, db, ctID, "tank", false)
	service := &Service{DB: db}
	enableJailDeletePolicySchema(t, service)

	runtime := inactiveJailDeleteRuntime()
	runtime.removeConfig = func(string) error {
		seedJailDeletePolicy(t, service, ctID, false)
		return nil
	}
	_, err := service.deleteJailWithRuntime(t.Context(), ctID, false, false, runtime)
	if err == nil || !strings.Contains(err.Error(), "guest_delete_requires_replication_policy_removed") {
		t.Fatalf("delete error = %v", err)
	}
	if count := countJailDeleteRows(t, db, &clusterModels.ReplicationPolicy{}, "guest_id = ?", ctID); count != 1 {
		t.Fatalf("policy count = %d, want 1", count)
	}
	var jailCount int64
	if err := db.Table("jails").Where("id = ? AND ct_id = ?", jailID, ctID).Count(&jailCount).Error; err != nil {
		t.Fatalf("count jail identity: %v", err)
	}
	if jailCount != 1 {
		t.Fatalf("jail identity count = %d, want 1", jailCount)
	}
}

func TestDeleteJailServiceBlocksExplicitBackupJobsBeforeRuntime(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("enabled_%t", enabled), func(t *testing.T) {
			db := newJailDeleteTestDB(t)
			const ctID uint = 684
			seedJailDeleteGraph(t, db, ctID, "tank", false)
			service := &Service{DB: db}
			seedJailDeleteBackupJob(t, service, ctID, enabled)

			runtimeCalled := false
			runtime := inactiveJailDeleteRuntime()
			runtime.isRunning = func(uint) (bool, error) {
				runtimeCalled = true
				return false, nil
			}
			_, err := service.deleteJailWithRuntime(t.Context(), ctID, false, false, runtime)
			if err == nil || !strings.Contains(err.Error(), "guest_delete_requires_backup_jobs_removed") {
				t.Fatalf("delete error = %v", err)
			}
			if runtimeCalled {
				t.Fatal("runtime was touched before backup-job rejection")
			}
		})
	}
}

func TestDeleteJailTransactionRevalidatesBackupJobs(t *testing.T) {
	db := newJailDeleteTestDB(t)
	const ctID uint = 685
	jailID, _ := seedJailDeleteGraph(t, db, ctID, "tank", false)
	service := &Service{DB: db}

	runtime := inactiveJailDeleteRuntime()
	runtime.removeConfig = func(string) error {
		seedJailDeleteBackupJob(t, service, ctID, false)
		return nil
	}
	_, err := service.deleteJailWithRuntime(t.Context(), ctID, false, false, runtime)
	if err == nil || !strings.Contains(err.Error(), "guest_delete_requires_backup_jobs_removed") {
		t.Fatalf("delete error = %v", err)
	}
	var jailCount int64
	if err := db.Table("jails").Where("id = ? AND ct_id = ?", jailID, ctID).Count(&jailCount).Error; err != nil {
		t.Fatalf("count jail identity: %v", err)
	}
	if jailCount != 1 {
		t.Fatalf("jail identity count = %d, want 1", jailCount)
	}
}

func TestPublicJailDeleteBlocksExplicitBackupJob(t *testing.T) {
	db := newJailDeleteTestDB(t)
	const ctID uint = 686
	jailID, _ := seedJailDeleteGraph(t, db, ctID, "tank", false)
	service := &Service{DB: db}
	seedJailDeleteBackupJob(t, service, ctID, false)

	_, err := service.DeleteJailWithWarnings(t.Context(), ctID, false, false)
	if err == nil || !strings.Contains(err.Error(), "guest_delete_requires_backup_jobs_removed") {
		t.Fatalf("delete error = %v", err)
	}
	var jailCount int64
	if err := db.Table("jails").Where("id = ? AND ct_id = ?", jailID, ctID).Count(&jailCount).Error; err != nil {
		t.Fatalf("count jail identity: %v", err)
	}
	if jailCount != 1 {
		t.Fatalf("jail identity count = %d, want 1", jailCount)
	}
}

func TestRetireJailLocalMetadataBypassesGuestReferencesOnlyExplicitly(t *testing.T) {
	db := newJailDeleteTestDB(t)
	const ctID uint = 683
	jailID, _ := seedJailDeleteGraph(t, db, ctID, "tank", false)
	service := &Service{DB: db}
	enableJailDeletePolicySchema(t, service)
	seedJailDeletePolicy(t, service, ctID, true)
	seedJailDeleteBackupJob(t, service, ctID, false)

	result, err := service.deleteJailWithRuntimeOptions(
		t.Context(), ctID, false, false, inactiveJailDeleteRuntime(), true,
	)
	if err != nil {
		t.Fatalf("retire jail metadata: %v", err)
	}
	if len(result.RetainedDatasets) != 1 || result.RetainedDatasets[0] != "tank/sylve/jails/683" {
		t.Fatalf("retained datasets = %v", result.RetainedDatasets)
	}
	assertJailDeleteGraphAbsent(t, db, jailID, ctID)
	if count := countJailDeleteRows(t, db, &clusterModels.ReplicationPolicy{}, "guest_id = ?", ctID); count != 1 {
		t.Fatalf("policy count = %d, want 1", count)
	}
	if count := countJailDeleteRows(t, db, &clusterModels.BackupJob{}, "mode = ?", clusterModels.BackupJobModeJail); count != 1 {
		t.Fatalf("backup job count = %d, want 1", count)
	}
}
