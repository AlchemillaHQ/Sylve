// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"context"
	"errors"
	"testing"

	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func periodicSnapshotJobs(t *testing.T) (*Service, zfsModels.PeriodicSnapshot, zfsModels.PeriodicSnapshot) {
	t.Helper()
	database := testutil.NewSQLiteTestDB(t, &zfsModels.PeriodicSnapshot{})
	first := zfsModels.PeriodicSnapshot{GUID: "dataset-guid", Interval: 60, Prefix: "minute"}
	second := zfsModels.PeriodicSnapshot{GUID: "dataset-guid", Interval: 3600, Prefix: "hour"}
	if err := database.Create(&first).Error; err != nil {
		t.Fatalf("create first periodic snapshot job: %v", err)
	}
	if err := database.Create(&second).Error; err != nil {
		t.Fatalf("create second periodic snapshot job: %v", err)
	}
	return &Service{DB: database}, first, second
}

func TestDeletePeriodicSnapshotTargetsJobID(t *testing.T) {
	service, first, second := periodicSnapshotJobs(t)
	ctx := context.Background()

	if err := service.DeletePeriodicSnapshot(ctx, second.ID); err != nil {
		t.Fatalf("delete periodic snapshot job: %v", err)
	}

	var remaining zfsModels.PeriodicSnapshot
	if err := service.DB.First(&remaining, first.ID).Error; err != nil {
		t.Fatalf("the other job for the same dataset was deleted: %v", err)
	}
	if err := service.DB.First(&zfsModels.PeriodicSnapshot{}, second.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("deleted job lookup error = %v, want record not found", err)
	}
}

func TestModifyPeriodicSnapshotTargetsJobID(t *testing.T) {
	service, first, second := periodicSnapshotJobs(t)
	keepLast := 7
	request := zfsServiceInterfaces.ModifyPeriodicSnapshotRetentionRequest{KeepLast: &keepLast}

	if err := service.ModifyPeriodicSnapshotRetention(context.Background(), second.ID, request); err != nil {
		t.Fatalf("modify periodic snapshot retention: %v", err)
	}

	var jobs []zfsModels.PeriodicSnapshot
	if err := service.DB.Order("id").Find(&jobs).Error; err != nil {
		t.Fatalf("load periodic snapshot jobs: %v", err)
	}
	if len(jobs) != 2 || jobs[0].ID != first.ID || jobs[0].KeepLast != 0 || jobs[1].ID != second.ID || jobs[1].KeepLast != keepLast {
		t.Fatalf("unexpected jobs after update: %#v", jobs)
	}
}

func TestPeriodicSnapshotMissingJobErrors(t *testing.T) {
	service, _, _ := periodicSnapshotJobs(t)
	ctx := context.Background()
	keepLast := 1
	request := zfsServiceInterfaces.ModifyPeriodicSnapshotRetentionRequest{KeepLast: &keepLast}

	if err := service.DeletePeriodicSnapshot(ctx, 9999); !errors.Is(err, ErrSnapshotJobNotFound) {
		t.Fatalf("delete error = %v, want ErrSnapshotJobNotFound", err)
	}
	if err := service.ModifyPeriodicSnapshotRetention(ctx, 9999, request); !errors.Is(err, ErrSnapshotJobNotFound) {
		t.Fatalf("modify error = %v, want ErrSnapshotJobNotFound", err)
	}
}

func TestClassifiedErrorPreservesDetailAndCategory(t *testing.T) {
	detail := errors.New("dataset_in_use_by_vm")
	err := classifyError(ErrConflict, "%w", detail)
	if err.Error() != detail.Error() {
		t.Fatalf("error detail = %q, want %q", err.Error(), detail.Error())
	}
	if !errors.Is(err, ErrConflict) || !errors.Is(err, detail) {
		t.Fatalf("classified error did not unwrap category and detail: %v", err)
	}
}
