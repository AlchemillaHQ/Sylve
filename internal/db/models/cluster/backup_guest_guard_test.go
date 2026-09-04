// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"errors"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestGuestDeletionGuardMatchesAllExplicitJobsOnly(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &BackupJob{})
	jobs := []BackupJob{
		{
			ID: 1, Name: "enabled-vm", TargetID: 1, Mode: BackupJobModeVM,
			SourceDataset: "tank/sylve/virtual-machines/101", CronExpr: "0 * * * *", Enabled: true,
		},
		{
			ID: 2, Name: "disabled-vm", TargetID: 1, Mode: BackupJobModeVM,
			SourceDataset: "tank/sylve/virtual-machines/101", CronExpr: "0 * * * *", Enabled: false,
		},
		{
			ID: 3, Name: "dataset-mode", TargetID: 1, Mode: BackupJobModeDataset,
			SourceDataset: "tank/sylve/virtual-machines/101", CronExpr: "0 * * * *", Enabled: true,
		},
		{
			ID: 4, Name: "other-vm", TargetID: 1, Mode: BackupJobModeVM,
			SourceDataset: "tank/sylve/virtual-machines/102", CronExpr: "0 * * * *", Enabled: true,
		},
		{
			ID: 5, Name: "disabled-jail", TargetID: 1, Mode: BackupJobModeJail,
			JailRootDataset: "tank/sylve/jails/201", CronExpr: "0 * * * *", Enabled: false,
		},
		{
			ID: 6, Name: "legacy-mode-casing", TargetID: 1, Mode: " VM ",
			SourceDataset: "tank/sylve/virtual-machines/101", CronExpr: "0 * * * *", Enabled: false,
		},
	}
	if err := db.Create(&jobs).Error; err != nil {
		t.Fatalf("seed backup jobs: %v", err)
	}

	for _, test := range []struct {
		name      string
		guestType string
		guestID   uint
		blocked   bool
	}{
		{name: "vm with enabled and disabled jobs", guestType: BackupJobModeVM, guestID: 101, blocked: true},
		{name: "other vm", guestType: BackupJobModeVM, guestID: 102, blocked: true},
		{name: "jail with disabled job", guestType: BackupJobModeJail, guestID: 201, blocked: true},
		{name: "unreferenced vm", guestType: BackupJobModeVM, guestID: 999, blocked: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := RequireGuestDeletionDetachedTxn(db, test.guestType, test.guestID)
			if test.blocked && !errors.Is(err, ErrGuestDeleteRequiresBackupJobsRemoved) {
				t.Fatalf("deletion guard error = %v", err)
			}
			if !test.blocked && err != nil {
				t.Fatalf("unexpected deletion guard error = %v", err)
			}
		})
	}

	if err := db.Where("LOWER(TRIM(mode)) IN ?", []string{BackupJobModeVM, BackupJobModeJail}).Delete(&BackupJob{}).Error; err != nil {
		t.Fatalf("delete explicit jobs: %v", err)
	}
	if err := RequireGuestDeletionDetachedTxn(db, BackupJobModeVM, 101); err != nil {
		t.Fatalf("dataset-mode job must not block VM deletion: %v", err)
	}
}

func TestGuestDeletionGuardFailsClosedWithoutBackupSchema(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t)
	err := RequireGuestDeletionDetachedTxn(db, BackupJobModeVM, 101)
	if err == nil || !strings.Contains(err.Error(), "failed_to_check_guest_backup_jobs_before_delete") {
		t.Fatalf("missing-schema deletion guard error = %v", err)
	}
}
