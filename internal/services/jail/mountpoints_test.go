// SPDX-License-Identifier: BSD-2-Clause

package jail

import (
	"strings"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func newResolvedJailRootTestService(
	t *testing.T,
	ctID uint,
	datasetMountPoint string,
) (*Service, *jailModels.Jail) {
	t.Helper()
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Storage{},
		&jailModels.JailHooks{},
		&jailModels.JailSnapshot{},
		&jailModels.Network{},
	)
	jail := &jailModels.Jail{CTID: ctID, Name: "mountpoint-test"}
	if err := db.Create(jail).Error; err != nil {
		t.Fatalf("seed jail: %v", err)
	}
	service := &Service{DB: db}
	attachJailRootTestFixture(t, service, db, jail.ID, ctID, datasetMountPoint)
	return service, jail
}

func TestGetJailBaseMountPointUsesExactDatasetMountpoint(t *testing.T) {
	const mountPoint = "/custom/jail-root/9301"
	service, _ := newResolvedJailRootTestService(t, 9301, mountPoint)

	got, err := service.GetJailBaseMountPoint(9301)
	if err != nil {
		t.Fatalf("resolve jail root: %v", err)
	}
	if got != mountPoint {
		t.Fatalf("mountpoint = %q, want %q", got, mountPoint)
	}
}

func TestGetJailBaseMountPointRejectsDatasetGUIDMismatch(t *testing.T) {
	const mountPoint = "/custom/jail-root/9304"
	service, jail := newResolvedJailRootTestService(t, 9304, mountPoint)
	if err := service.DB.Model(&jailModels.Storage{}).
		Where("jid = ? AND is_base = ?", jail.ID, true).
		Update("guid", "redirected-guid").Error; err != nil {
		t.Fatalf("redirect storage GUID: %v", err)
	}

	_, err := service.GetJailBaseMountPoint(9304)
	if err == nil || !strings.Contains(err.Error(), "filesystem_dataset_identity_mismatch") {
		t.Fatalf("expected dataset identity mismatch, got %v", err)
	}
}
