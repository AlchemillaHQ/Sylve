// SPDX-License-Identifier: BSD-2-Clause

package libvirt

import (
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/db/replicationguard"
)

func TestVMJSONOutputPoolsIgnoreLegacyISOPool(t *testing.T) {
	pools := vmJSONOutputPools([]vmModels.Storage{
		{Type: vmModels.VMStorageTypeRaw, Pool: "tank"},
		{
			Type: vmModels.VMStorageTypeDiskImage, Pool: "stale",
			DownloadUUID: "iso-1", Emulation: vmModels.AHCICDStorageEmulation,
		},
		{Type: vmModels.VMStorageTypeZVol, Dataset: vmModels.VMStorageDataset{Pool: "fast"}},
		{
			Type:    vmModels.VMStorageTypeRaw,
			Dataset: vmModels.VMStorageDataset{Name: "legacy/sylve/virtual-machines/107/raw-9"},
		},
		{Type: vmModels.VMStorageTypeRaw, Pool: "tank"},
	})
	if got := strings.Join(pools, ","); got != "tank,fast,legacy" {
		t.Fatalf("vm.json output pools = %q, want tank,fast,legacy", got)
	}
}

func TestLocalVMMetadataRetirementExplicitlyBypassesTopologyHook(t *testing.T) {
	db := newVMDeleteTestDB(t)
	seed := seedVMDeleteGraph(t, db, 791, "tank", false)
	if err := db.AutoMigrate(&clusterModels.ReplicationPolicy{}); err != nil {
		t.Fatalf("migrate replication policy: %v", err)
	}
	replicationguard.MarkPolicySchemaReady(db)
	if err := db.Create(&clusterModels.ReplicationPolicy{
		Name:      "protected-vm-retirement",
		GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID:   seed.VM.RID,
		Enabled:   true,
	}).Error; err != nil {
		t.Fatalf("seed replication policy: %v", err)
	}

	if err := db.Where("vm_id = ?", seed.VM.ID).Delete(&vmModels.Storage{}).Error; err == nil {
		t.Fatal("normal protected storage deletion unexpectedly bypassed topology guard")
	}
	if err := deleteVMStorageRowsForLocalRetirement(db, []uint{seed.VM.ID}); err != nil {
		t.Fatalf("retire local VM storage metadata: %v", err)
	}

	var count int64
	if err := db.Model(&vmModels.Storage{}).Where("vm_id = ?", seed.VM.ID).Count(&count).Error; err != nil {
		t.Fatalf("count retired storage rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("storage rows remaining = %d, want 0", count)
	}
}
