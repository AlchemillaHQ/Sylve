// SPDX-License-Identifier: BSD-2-Clause
//go:build freebsd

package jail

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
	"github.com/alchemillahq/sylve/pkg/pkgmanager"
)

func TestIntegrationJailPackagesOfflineTargetResolution(t *testing.T) {
	if testing.Short() {
		t.Skip("requires an isolated ZFS filesystem on FreeBSD")
	}
	if err := pkgmanager.Probe(""); err != nil {
		t.Skip(err)
	}
	pool, client := zfstest.DedicatedPool(t)
	s, jail := newPackageTestService(t)
	if err := s.DB.AutoMigrate(&clusterModels.ReplicationPolicy{}); err != nil {
		t.Fatal(err)
	}
	datasetName := fmt.Sprintf("%s/sylve/jails/%d", pool, jail.CTID)
	zfstest.EnsureDataset(t, client, datasetName)
	dataset, err := client.ZFS.Get(t.Context(), datasetName, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DB.Model(&jailModels.Storage{}).Where("jid = ?", jail.ID).Updates(map[string]any{"pool": pool, "guid": dataset.GUID}).Error; err != nil {
		t.Fatal(err)
	}
	s.GZFS = client
	s.ctidHashByCTID = map[uint]string{jail.CTID: fmt.Sprintf("spkg-resolve-%d", os.Getpid())}
	current, _, err := s.packageJail(t.Context(), jail.CTID)
	if err != nil {
		t.Fatal(err)
	}
	target, snapshotSafe, err := s.resolvePackageTarget(t.Context(), current)
	if err != nil || !target.Offline || target.JID != 0 || target.Root != dataset.Mountpoint || target.GUID != dataset.GUID || snapshotSafe {
		t.Fatalf("offline target %+v, snapshot=%v: %v", target, snapshotSafe, err)
	}
	policy := clusterModels.ReplicationPolicy{GuestType: "jail", GuestID: jail.CTID, Enabled: true, SourceNodeID: "another-node", ActiveNodeID: "another-node", OwnerEpoch: 1}
	if err = s.DB.Create(&policy).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.resolvePackageTarget(t.Context(), current); err == nil || err.Error() != "replication_lease_not_owned" {
		t.Fatalf("offline inspection bypassed ownership: %v", err)
	}
	if err = s.DB.Delete(&policy).Error; err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/sbin/zfs", "unmount", datasetName).CombinedOutput(); err != nil {
		t.Fatalf("unmount fixture: %v %s", err, out)
	}
	if _, _, err = s.resolvePackageTarget(t.Context(), current); err == nil || err.Error() != "package_root_not_mounted" {
		t.Fatalf("unmounted jail root not rejected: %v", err)
	}
}
