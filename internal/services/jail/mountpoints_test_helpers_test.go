// SPDX-License-Identifier: BSD-2-Clause

package jail

import (
	"fmt"
	"testing"

	"github.com/alchemillahq/gzfs"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"gorm.io/gorm"
)

func attachJailRootTestFixture(
	t *testing.T,
	service *Service,
	db *gorm.DB,
	jailID uint,
	ctID uint,
	mountPoint string,
) {
	t.Helper()
	pool := fmt.Sprintf("jail-test-%d", ctID)
	datasetName := fmt.Sprintf("%s/sylve/jails/%d", pool, ctID)
	guid := fmt.Sprintf("guid-%d", ctID)
	storage := jailModels.Storage{
		JailID: jailID,
		Pool:   pool,
		GUID:   guid,
		Name:   "Base Filesystem",
		IsBase: true,
	}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatalf("seed jail root storage: %v", err)
	}

	runner := newJailCreateTestZFSRunner(t, nil)
	runner.datasets[datasetName] = jailCreateTestZFSDataset{
		guid:       guid,
		mountpoint: mountPoint,
	}
	service.GZFS = gzfs.NewClient(gzfs.Options{
		Runner:   runner,
		ZFSBin:   "zfs",
		ZpoolBin: "zpool",
		ZDBBin:   "zdb",
	})
}

func writeJailRootConfigTestFixture(
	t *testing.T,
	service *Service,
	ctID uint,
	name string,
	mountPoint string,
) {
	t.Helper()
	config := fmt.Sprintf(
		"%s%s {\n\tpath = %q;\n\tpersist;\n}\n",
		JAIL_CONF_PREAMBLE,
		name,
		mountPoint,
	)
	if err := service.SaveJailConfig(ctID, config); err != nil {
		t.Fatalf("write jail root config: %v", err)
	}
}
