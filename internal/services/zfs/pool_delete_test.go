// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"errors"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestIntegrationDeletePoolRefusesGuestsRealZFS(t *testing.T) {
	pool, client := zfstest.DedicatedPool(t)
	database := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{},
		&zfsModels.PoolScrubRun{}, &zfsModels.PoolScrubSchedulerLease{}, &jailModels.Storage{},
		&models.ZFSCacheInvalidation{}, &iscsiModels.ISCSITarget{}, &iscsiModels.ISCSITargetLUN{})
	if err := containerModels.Migrate(database); err != nil {
		t.Fatal(err)
	}
	service := NewZfsService(database, nil, nil, client).(*Service)
	service.ReleasePool = func(string) { t.Fatal("pool released for a refused delete") }
	rootPool, err := client.Zpool.Get(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ZFS.CreateVolume(t.Context(), pool+"/lun", 16<<20, map[string]string{"sparse": "on"}); err != nil {
		t.Fatal(err)
	}
	target := iscsiModels.ISCSITarget{TargetName: "iqn.2026-11.test:pool"}
	if err := database.Create(&target).Error; err != nil {
		t.Fatal(err)
	}

	for _, guest := range []any{
		&jailModels.Storage{JailID: 1, Pool: pool, GUID: "jail-root", Name: "root", IsBase: true},
		&containerModels.Container{OCID: 1, Name: "web", Pool: pool, Image: "busybox"},
		&iscsiModels.ISCSITargetLUN{TargetID: target.ID, ZVol: pool + "/lun"},
	} {
		if err := database.Create(guest).Error; err != nil {
			t.Fatal(err)
		}
		if err := service.DeletePool(t.Context(), rootPool.PoolGUID); !errors.Is(err, ErrConflict) {
			t.Fatalf("pool with %T deleted: %v", guest, err)
		}
		if err := database.Delete(guest).Error; err != nil {
			t.Fatal(err)
		}
	}
	if after, err := client.Zpool.Get(t.Context(), pool); err != nil || after == nil {
		t.Fatalf("pool destroyed: %v", err)
	}
}
