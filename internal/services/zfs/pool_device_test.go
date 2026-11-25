// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/testutil"
)

const poolDeviceMemberSize = 128 * 1024 * 1024

type poolDeviceTestInspector struct {
	t        *testing.T
	mutex    sync.Mutex
	locked   bool
	reads    int
	releases int
	devices  []diskServiceInterfaces.PoolDevice
	onRead   func(int, []diskServiceInterfaces.PoolDevice) ([]diskServiceInterfaces.PoolDevice, error)
}

func (d *poolDeviceTestInspector) LockPoolDeviceMutation() func() {
	d.mutex.Lock()
	d.locked = true
	return func() {
		d.releases++
		d.locked = false
		d.mutex.Unlock()
	}
}

func (d *poolDeviceTestInspector) ReadPoolDeviceInventory(ctx context.Context) ([]diskServiceInterfaces.PoolDevice, error) {
	d.t.Helper()
	if !d.locked {
		d.t.Fatal("pool mutation read disks without holding the shared operation lock")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.reads++
	result := slices.Clone(d.devices)
	for i := range result {
		result[i].Aliases = slices.Clone(result[i].Aliases)
	}
	if d.onRead != nil {
		return d.onRead(d.reads, result)
	}
	return result, nil
}

type poolDeviceTestRunner struct {
	t           *testing.T
	inspector   *poolDeviceTestInspector
	pool        *gzfs.ZPool
	otherPools  map[string]any
	status      *gzfs.ZPoolStatusPool
	statusReads int
	listReads   int
	mutations   [][]string
	onStatus    func(int)
	onList      func(int)
	statusError error
	commandErr  error
}

func (r *poolDeviceTestRunner) Run(_ context.Context, _ io.Reader, stdout, _ io.Writer, name string, args ...string) error {
	if name != "zpool" || len(args) == 0 {
		return fmt.Errorf("unexpected command: %s %v", name, args)
	}
	switch args[0] {
	case "list":
		r.listReads++
		if r.onList != nil {
			r.onList(r.listReads)
		}
		vdevs := make(map[string]any)
		for key, node := range r.pool.Vdevs {
			vdevs[key] = node
		}
		for key, nodes := range map[string]map[string]*gzfs.ZPoolVDEV{
			"logs": r.pool.Logs, "special": r.pool.Special, "dedup": r.pool.Dedup,
			"l2cache": r.pool.L2Cache, "spares": r.pool.Spares,
		} {
			if nodes != nil {
				vdevs[key] = nodes
			}
		}
		pools := map[string]any{
			r.pool.Name: map[string]any{
				"name": r.pool.Name, "pool_guid": r.pool.PoolGUID, "state": r.pool.State,
				"properties": r.pool.Properties, "vdevs": vdevs,
			},
		}
		for name, pool := range r.otherPools {
			pools[name] = pool
		}
		return json.NewEncoder(stdout).Encode(map[string]any{"pools": pools})
	case "status":
		var names []string
		for _, arg := range args[1:] {
			if !strings.HasPrefix(arg, "-") {
				names = append(names, arg)
			}
		}
		if !slices.Equal(names, []string{"tank"}) {
			return fmt.Errorf("attempted to scan an unrelated pool: %v", args)
		}
		r.statusReads++
		if r.onStatus != nil {
			r.onStatus(r.statusReads)
		}
		if r.statusError != nil {
			return r.statusError
		}
		return json.NewEncoder(stdout).Encode(gzfs.ZPoolStatus{Pools: map[string]*gzfs.ZPoolStatusPool{"tank": r.status}})
	case "replace", "attach", "detach":
		if !r.inspector.locked {
			r.t.Fatal("native pool mutation escaped the disk operation lock")
		}
		r.mutations = append(r.mutations, slices.Clone(args))
		return r.commandErr
	default:
		return fmt.Errorf("unexpected zpool command: %v", args)
	}
}

func newPoolDeviceTestService(t *testing.T) (*Service, *poolDeviceTestRunner, *poolDeviceTestInspector) {
	t.Helper()
	database := testutil.NewSQLiteTestDB(t, &models.BasicSettings{}, &zfsModels.PoolScrubRun{},
		&zfsModels.PoolScrubSchedule{}, &zfsModels.PoolScrubSchedulerLease{})
	if err := database.Create(&models.BasicSettings{Pools: []string{"tank"}}).Error; err != nil {
		t.Fatal(err)
	}
	member := func(name, guid string) *gzfs.ZPoolStatusVDEV {
		return &gzfs.ZPoolStatusVDEV{
			Name: name, Path: "/dev/" + name, GUID: guid, VdevType: "disk", State: "ONLINE",
			RepDevSize: fmt.Sprint(poolDeviceMemberSize), ReadErrors: "0", WriteErrors: "0", ChkErrors: "0",
		}
	}
	status := &gzfs.ZPoolStatusPool{
		Name: "tank", PoolGUID: "101", State: "ONLINE",
		ScanStats: &gzfs.ZPoolStatusScanStats{Function: "NONE", State: "NONE"},
		Vdevs: map[string]*gzfs.ZPoolStatusVDEV{"tank": {
			Name: "tank", GUID: "101", VdevType: "root", State: "ONLINE",
			Vdevs: map[string]*gzfs.ZPoolStatusVDEV{"mirror-0": {
				Name: "mirror-0", GUID: "1001", VdevType: "mirror", State: "ONLINE",
				Vdevs: map[string]*gzfs.ZPoolStatusVDEV{"da0": member("da0", "202"), "da1": member("da1", "203")},
			}},
		}},
	}
	inspector := &poolDeviceTestInspector{t: t, devices: []diskServiceInterfaces.PoolDevice{
		{Path: "/dev/da0", Aliases: []string{"/dev/gptid/source"}, Disk: "da0", Size: poolDeviceMemberSize, Identity: "source", IdentityStable: true, Busy: true},
		{Path: "/dev/da1", Disk: "da1", Size: 2 * poolDeviceMemberSize, Identity: "survivor", IdentityStable: true, Busy: true},
		{Path: "/dev/da7", Disk: "da7", Size: poolDeviceMemberSize, Identity: "replacement", IdentityStable: true},
	}}
	runner := &poolDeviceTestRunner{
		t: t, inspector: inspector, status: status,
		pool: &gzfs.ZPool{
			Name: "tank", PoolGUID: "101", State: gzfs.ZPoolStateOnline,
			Properties: map[string]gzfs.ZFSProperty{"bootfs": {Value: "-"}},
			Vdevs: map[string]*gzfs.ZPoolVDEV{"mirror-0": {
				Name: "mirror-0", GUID: "1001", VdevType: "mirror", State: "ONLINE",
				Vdevs: map[string]*gzfs.ZPoolVDEV{
					"da0": {Name: "da0", Path: "/dev/da0", GUID: "202", VdevType: "disk", Size: poolDeviceMemberSize},
					"da1": {Name: "da1", Path: "/dev/da1", GUID: "203", VdevType: "disk", Size: 2 * poolDeviceMemberSize},
				},
			}},
		},
	}
	client := gzfs.NewClient(gzfs.Options{Runner: runner})
	service := NewZfsService(database, nil, nil, client).(*Service)
	service.PoolDeviceDisks = inspector
	service.poolDeviceRunner = runner
	return service, runner, inspector
}

func poolDeviceTestMirror(r *poolDeviceTestRunner) *gzfs.ZPoolStatusVDEV {
	return r.status.Vdevs["tank"].Vdevs["mirror-0"]
}

func TestPoolAttachCreatesMirrorFromSingleDiskVDEV(t *testing.T) {
	for _, partition := range []bool{false, true} {
		service, runner, inspector := newPoolDeviceTestService(t)
		runner.status.Vdevs["tank"].Vdevs = map[string]*gzfs.ZPoolStatusVDEV{
			"da0": poolDeviceTestMirror(runner).Vdevs["da0"],
		}
		runner.pool.Vdevs = map[string]*gzfs.ZPoolVDEV{"da0": runner.pool.Vdevs["mirror-0"].Vdevs["da0"]}
		path := "/dev/da7"
		if partition {
			inspector.devices[2].Path = "/dev/da7p1"
			inspector.devices[2].Aliases = []string{"/dev/gptid/new-partition"}
			inspector.devices[2].Partition, inspector.devices[2].PartitionType = true, "ZFS"
			path = "/dev/gptid/new-partition"
		}
		checks := zfsServiceInterfaces.PoolDeviceMutationOptions{
			ExpectedParentGUID: "101", ExpectedMemberGUIDs: []string{"202"}, ExpectedNewIdentity: "replacement",
		}
		if err := service.AttachDevice(t.Context(), "101", "gptid/source", path, checks); err != nil {
			t.Fatalf("single disk could not be mirrored: %v", err)
		}
		want := []string{"attach", "tank", "202", inspector.devices[2].Path}
		if len(runner.mutations) != 1 || !slices.Equal(runner.mutations[0], want) || inspector.reads != 2 || runner.listReads != 2 || runner.statusReads != 2 || inspector.locked {
			t.Fatalf("unexpected attachment: commands=%v disks=%d lists=%d statuses=%d", runner.mutations, inspector.reads, runner.listReads, runner.statusReads)
		}
	}
}

func TestPoolAttachRejectsUnsupportedTargetsAndUnhealthySources(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*poolDeviceTestRunner)
	}{
		{"raidz", func(r *poolDeviceTestRunner) { poolDeviceTestMirror(r).VdevType = "raidz2" }},
		{"draid", func(r *poolDeviceTestRunner) { poolDeviceTestMirror(r).VdevType = "draid2" }},
		{"offline", func(r *poolDeviceTestRunner) { poolDeviceTestMirror(r).Vdevs["da0"].State = "OFFLINE" }},
		{"faulted", func(r *poolDeviceTestRunner) { poolDeviceTestMirror(r).Vdevs["da0"].State = "FAULTED" }},
		{"read-errors", func(r *poolDeviceTestRunner) { poolDeviceTestMirror(r).Vdevs["da0"].ReadErrors = "1" }},
		{"write-errors", func(r *poolDeviceTestRunner) { poolDeviceTestMirror(r).Vdevs["da0"].WriteErrors = "1" }},
		{"checksum-errors", func(r *poolDeviceTestRunner) { poolDeviceTestMirror(r).Vdevs["da0"].ChkErrors = "1" }},
		{"unknown-errors", func(r *poolDeviceTestRunner) { poolDeviceTestMirror(r).Vdevs["da0"].ReadErrors = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, runner, inspector := newPoolDeviceTestService(t)
			test.edit(runner)
			if err := service.AttachDevice(t.Context(), "101", "202", "da7"); !errors.Is(err, ErrConflict) || len(runner.mutations) != 0 || inspector.locked {
				t.Fatalf("unsafe attachment admitted: %v commands=%v", err, runner.mutations)
			}
		})
	}
}

func TestPoolAttachBindsFreshMirrorAndDiskSamples(t *testing.T) {
	for _, change := range []string{"membership", "parent-type", "source-errors", "source-offline", "new-identity", "new-path", "new-size", "new-busy"} {
		t.Run(change, func(t *testing.T) {
			service, runner, inspector := newPoolDeviceTestService(t)
			runner.onStatus = func(read int) {
				if read != 2 {
					return
				}
				switch change {
				case "membership":
					poolDeviceTestMirror(runner).Vdevs["da1"].GUID = "204"
				case "parent-type":
					poolDeviceTestMirror(runner).VdevType = "raidz2"
				case "source-errors":
					poolDeviceTestMirror(runner).Vdevs["da0"].ReadErrors = "1"
				case "source-offline":
					poolDeviceTestMirror(runner).Vdevs["da0"].State = "OFFLINE"
				}
			}
			inspector.onRead = func(read int, disks []diskServiceInterfaces.PoolDevice) ([]diskServiceInterfaces.PoolDevice, error) {
				if read == 2 {
					switch change {
					case "new-identity":
						disks[2].Identity = "different-disk"
					case "new-path":
						disks[2].Path, disks[2].Aliases = "/dev/da8", []string{"/dev/da7"}
					case "new-size":
						disks[2].Size++
					case "new-busy":
						disks[2].Busy = true
					}
				}
				return disks, nil
			}
			if err := service.AttachDevice(t.Context(), "101", "202", "da7"); !errors.Is(err, ErrConflict) || len(runner.mutations) != 0 || inspector.locked || inspector.releases != 1 {
				t.Fatalf("changed attachment preflight admitted: %v commands=%v", err, runner.mutations)
			}
		})
	}
}

func TestPoolDeviceMutationsResolveLegacyPathsAndUseGUIDCommands(t *testing.T) {
	for _, source := range []string{"202", "da0", "/dev/da0", "gptid/source", "/dev/gptid/source"} {
		for _, action := range []string{"detach", "replace", "attach"} {
			t.Run(source+"/"+action, func(t *testing.T) {
				service, runner, inspector := newPoolDeviceTestService(t)
				checks := zfsServiceInterfaces.PoolDeviceMutationOptions{
					ExpectedParentGUID: "1001", ExpectedMemberGUIDs: []string{"203", "202"},
				}
				var err error
				want := []string{action, "tank", "202"}
				if action != "detach" {
					checks.ExpectedNewIdentity = "replacement"
					if action == "attach" {
						err = service.AttachDevice(t.Context(), "101", source, "/dev/da7", checks)
					} else {
						err = service.ReplaceDevice(t.Context(), "101", source, "/dev/da7", checks)
					}
					want = append(want, "/dev/da7")
				} else {
					err = service.DetachDevice(t.Context(), "101", source, checks)
				}
				if err != nil {
					t.Fatalf("valid mutation refused: %v", err)
				}
				if len(runner.mutations) != 1 || !slices.Equal(runner.mutations[0], want) || inspector.reads != 2 || runner.statusReads != 2 || runner.listReads != 2 || inspector.locked || inspector.releases != 1 {
					t.Fatalf("unexpected command/ownership: commands=%v disk reads=%d status reads=%d list reads=%d releases=%d", runner.mutations, inspector.reads, runner.statusReads, runner.listReads, inspector.releases)
				}
			})
		}
	}
}

func TestPoolDeviceMutationsBlockUnsafePoolStatesBeforeCommands(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Service, *poolDeviceTestRunner, *poolDeviceTestInspector)
		want error
	}{
		{"boot-pool", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			r.pool.Properties["bootfs"] = gzfs.ZFSProperty{Value: "tank/ROOT/default"}
		}, ErrConflict},
		{"unknown-boot-status", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			delete(r.pool.Properties, "bootfs")
		}, ErrPoolDeviceStatusUnavailable},
		{"boot-disk", func(_ *Service, _ *poolDeviceTestRunner, d *poolDeviceTestInspector) { d.devices[0].Boot = true }, ErrConflict},
		{"readonly", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			r.pool.Properties["readonly"] = gzfs.ZFSProperty{Value: "on"}
		}, ErrConflict},
		{"faulted", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) { r.status.State = "FAULTED" }, ErrConflict},
		{"pool-guid-changed", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) { r.status.PoolGUID = "999" }, ErrConflict},
		{"scan-unknown", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			r.status.ScanStats.State = "UNKNOWN"
		}, ErrScrubStatusUnavailable},
		{"scan-missing-unverified-root", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			r.status.ScanStats = nil
			r.status.Vdevs["tank"].GUID = "999"
		}, ErrConflict},
		{"external-scrub", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			r.status.ScanStats = &gzfs.ZPoolStatusScanStats{Function: "SCRUB", State: "SCANNING"}
		}, ErrConflict},
		{"external-resilver", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			r.status.ScanStats = &gzfs.ZPoolStatusScanStats{Function: "RESILVER", State: "SCANNING"}
		}, ErrConflict},
		{"paused", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			r.status.ScanStats.State = "PAUSED"
		}, ErrConflict},
		{"error-scrub", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			r.status.ScanStats.State = "ERRORSCRUBBING"
		}, ErrConflict},
		{"status-unreadable", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			r.statusError = errors.New("cannot read status")
		}, ErrPoolDeviceStatusUnavailable},
		{"disk-inventory-unreadable", func(_ *Service, _ *poolDeviceTestRunner, d *poolDeviceTestInspector) {
			d.onRead = func(int, []diskServiceInterfaces.PoolDevice) ([]diskServiceInterfaces.PoolDevice, error) {
				return nil, errors.New("cannot read GEOM")
			}
		}, ErrPoolDeviceStatusUnavailable},
		{"no-coordinator", func(s *Service, _ *poolDeviceTestRunner, _ *poolDeviceTestInspector) { s.PoolDeviceDisks = nil }, ErrPoolDeviceStatusUnavailable},
		{"source-missing", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			delete(poolDeviceTestMirror(r).Vdevs, "da0")
		}, ErrConflict},
		{"source-guid-missing", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			poolDeviceTestMirror(r).Vdevs["da0"].GUID = ""
		}, ErrConflict},
		{"source-health-unknown", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			poolDeviceTestMirror(r).Vdevs["da0"].State = "UNKNOWN"
		}, ErrPoolDeviceStatusUnavailable},
		{"parent-health-unknown", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			poolDeviceTestMirror(r).State = ""
		}, ErrPoolDeviceStatusUnavailable},
		{"source-is-auxiliary", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			poolDeviceTestMirror(r).Vdevs["da0"].Class = "spare"
		}, ErrConflict},
		{"source-physical-disk-unknown", func(_ *Service, _ *poolDeviceTestRunner, d *poolDeviceTestInspector) { d.devices = d.devices[1:] }, ErrPoolDeviceStatusUnavailable},
		{"replacement-container", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			poolDeviceTestMirror(r).VdevType = "replacing"
		}, ErrConflict},
		{"incomplete-topology", func(_ *Service, r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			poolDeviceTestMirror(r).Vdevs["broken"] = nil
		}, ErrPoolDeviceStatusUnavailable},
	}
	for _, test := range tests {
		for _, action := range []string{"detach", "replace", "attach"} {
			t.Run(test.name+"/"+action, func(t *testing.T) {
				service, runner, inspector := newPoolDeviceTestService(t)
				test.edit(service, runner, inspector)
				var err error
				switch action {
				case "attach":
					err = service.AttachDevice(t.Context(), "101", "202", "da7")
				case "replace":
					err = service.ReplaceDevice(t.Context(), "101", "202", "da7")
				case "detach":
					err = service.DetachDevice(t.Context(), "101", "202")
				}
				if !errors.Is(err, test.want) || len(runner.mutations) != 0 || inspector.locked {
					t.Fatalf("unsafe mutation admitted: err=%v want=%v commands=%v", err, test.want, runner.mutations)
				}
			})
		}
	}
}

func TestPoolDetachRequiresHealthyMirrorCopies(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*poolDeviceTestRunner)
		want error
	}{
		{"raidz", func(r *poolDeviceTestRunner) { poolDeviceTestMirror(r).VdevType = "raidz2" }, ErrConflict},
		{"stripe", func(r *poolDeviceTestRunner) { r.status.Vdevs["tank"].Vdevs = poolDeviceTestMirror(r).Vdevs }, ErrConflict},
		{"last-copy", func(r *poolDeviceTestRunner) { delete(poolDeviceTestMirror(r).Vdevs, "da1") }, ErrConflict},
		{"offline-survivor", func(r *poolDeviceTestRunner) { poolDeviceTestMirror(r).Vdevs["da1"].State = "OFFLINE" }, ErrConflict},
		{"faulted-survivor", func(r *poolDeviceTestRunner) { poolDeviceTestMirror(r).Vdevs["da1"].State = "FAULTED" }, ErrConflict},
		{"read-errors", func(r *poolDeviceTestRunner) { poolDeviceTestMirror(r).Vdevs["da1"].ReadErrors = "1" }, ErrConflict},
		{"write-errors", func(r *poolDeviceTestRunner) { poolDeviceTestMirror(r).Vdevs["da1"].WriteErrors = "1" }, ErrConflict},
		{"checksum-errors", func(r *poolDeviceTestRunner) { poolDeviceTestMirror(r).Vdevs["da1"].ChkErrors = "1" }, ErrConflict},
		{"unknown-errors", func(r *poolDeviceTestRunner) { poolDeviceTestMirror(r).Vdevs["da1"].ReadErrors = "" }, ErrConflict},
		{"missing-member-guid", func(r *poolDeviceTestRunner) { poolDeviceTestMirror(r).Vdevs["da1"].GUID = "" }, ErrPoolDeviceStatusUnavailable},
		{"duplicate-member-guid", func(r *poolDeviceTestRunner) { poolDeviceTestMirror(r).Vdevs["da1"].GUID = "202" }, ErrConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, runner, inspector := newPoolDeviceTestService(t)
			test.edit(runner)
			if err := service.DetachDevice(t.Context(), "101", "202"); !errors.Is(err, test.want) || len(runner.mutations) != 0 || inspector.locked {
				t.Fatalf("unsafe detach admitted: %v commands=%v", err, runner.mutations)
			}
		})
	}
}

func TestPoolReplacementValidatesDeviceIdentityAvailabilityAndTargetSize(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*poolDeviceTestRunner, *poolDeviceTestInspector)
		want error
	}{
		{"unstable", func(_ *poolDeviceTestRunner, d *poolDeviceTestInspector) { d.devices[2].IdentityStable = false }, ErrConflict},
		{"missing-identity", func(_ *poolDeviceTestRunner, d *poolDeviceTestInspector) { d.devices[2].Identity = "" }, ErrConflict},
		{"too-small", func(_ *poolDeviceTestRunner, d *poolDeviceTestInspector) {
			d.devices[2].Size = poolDeviceMemberSize - 1
		}, ErrConflict},
		{"boot", func(_ *poolDeviceTestRunner, d *poolDeviceTestInspector) { d.devices[2].Boot = true }, ErrConflict},
		{"swap", func(_ *poolDeviceTestRunner, d *poolDeviceTestInspector) { d.devices[2].Swap = true }, ErrConflict},
		{"open-provider", func(_ *poolDeviceTestRunner, d *poolDeviceTestInspector) { d.devices[2].Busy = true }, ErrConflict},
		{"partition-table", func(_ *poolDeviceTestRunner, d *poolDeviceTestInspector) { d.devices[2].Partitioned = true }, ErrConflict},
		{"optical", func(_ *poolDeviceTestRunner, d *poolDeviceTestInspector) { d.devices[2].Disk = "cd0" }, ErrConflict},
		{"other-filesystem", func(_ *poolDeviceTestRunner, d *poolDeviceTestInspector) {
			d.devices[2].Partition = true
			d.devices[2].PartitionType = "UFS"
		}, ErrConflict},
		{"assigned-physical-disk", func(_ *poolDeviceTestRunner, d *poolDeviceTestInspector) { d.devices[2].Disk = "da0" }, ErrConflict},
		{"assigned-spare", func(r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			r.pool.Spares = map[string]*gzfs.ZPoolVDEV{"da7": {GUID: "207", Name: "da7", Path: "/dev/da7", VdevType: "disk"}}
		}, ErrConflict},
		{"assigned-cache", func(r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			r.pool.L2Cache = map[string]*gzfs.ZPoolVDEV{"da7": {GUID: "207", Name: "da7", Path: "/dev/da7", VdevType: "disk"}}
		}, ErrConflict},
		{"assigned-special", func(r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			r.pool.Special = map[string]*gzfs.ZPoolVDEV{"da7": {GUID: "207", Name: "da7", Path: "/dev/da7", VdevType: "disk"}}
		}, ErrConflict},
		{"unknown-minimum", func(r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			poolDeviceTestMirror(r).Vdevs["da0"].RepDevSize = ""
			r.pool.Vdevs["mirror-0"].Vdevs["da0"].Size = 0
		}, ErrPoolDeviceStatusUnavailable},
		{"malformed-minimum", func(r *poolDeviceTestRunner, _ *poolDeviceTestInspector) {
			poolDeviceTestMirror(r).Vdevs["da0"].RepDevSize = "unknown"
		}, ErrPoolDeviceStatusUnavailable},
		{"ambiguous-path", func(_ *poolDeviceTestRunner, d *poolDeviceTestInspector) { d.devices[1].Aliases = []string{"/dev/da7"} }, ErrConflict},
	} {
		for _, action := range []string{"replace", "attach"} {
			t.Run(test.name+"/"+action, func(t *testing.T) {
				service, runner, inspector := newPoolDeviceTestService(t)
				test.edit(runner, inspector)
				var err error
				if action == "attach" {
					err = service.AttachDevice(t.Context(), "101", "202", "da7")
				} else {
					err = service.ReplaceDevice(t.Context(), "101", "202", "da7")
				}
				if !errors.Is(err, test.want) || len(runner.mutations) != 0 || inspector.locked {
					t.Fatalf("unsafe new device admitted: %v commands=%v", err, runner.mutations)
				}
			})
		}
	}
	service, runner, inspector := newPoolDeviceTestService(t)
	inspector.devices[2].Path = "/dev/da7p1"
	inspector.devices[2].Aliases = []string{"/dev/gptid/new-partition"}
	inspector.devices[2].Partition, inspector.devices[2].PartitionType = true, "ZFS"
	if err := service.ReplaceDevice(t.Context(), "101", "202", "/dev/gptid/new-partition"); err != nil || len(runner.mutations) != 1 || runner.mutations[0][3] != "/dev/da7p1" {
		t.Fatalf("eligible GPT partition replacement failed: %v %v", err, runner.mutations)
	}
}

func TestPoolDeviceMutationsBindFreshSnapshotsBeforeNativeCommand(t *testing.T) {
	for _, change := range []string{"resilver", "survivor-fails", "membership", "parent-guid", "parent-type", "boot-property", "replacement-identity", "replacement-busy", "replacement-disappears", "second-disk-read-fails"} {
		t.Run(change, func(t *testing.T) {
			service, runner, inspector := newPoolDeviceTestService(t)
			replace := strings.HasPrefix(change, "replacement-") || change == "parent-type" || change == "second-disk-read-fails"
			runner.onStatus = func(read int) {
				if read != 2 {
					return
				}
				switch change {
				case "resilver":
					runner.status.ScanStats = &gzfs.ZPoolStatusScanStats{Function: "RESILVER", State: "SCANNING"}
				case "survivor-fails":
					poolDeviceTestMirror(runner).Vdevs["da1"].State = "FAULTED"
				case "membership":
					poolDeviceTestMirror(runner).Vdevs["da1"].GUID = "204"
				case "parent-guid":
					poolDeviceTestMirror(runner).GUID = "1002"
				case "parent-type":
					poolDeviceTestMirror(runner).VdevType = "raidz2"
				}
			}
			runner.onList = func(read int) {
				if read == 2 && change == "boot-property" {
					runner.pool.Properties["bootfs"] = gzfs.ZFSProperty{Value: "tank/ROOT/default"}
				}
			}
			inspector.onRead = func(read int, disks []diskServiceInterfaces.PoolDevice) ([]diskServiceInterfaces.PoolDevice, error) {
				if read == 2 {
					switch change {
					case "replacement-identity":
						disks[2].Identity = "different-physical-disk"
					case "replacement-busy":
						disks[2].Busy = true
					case "replacement-disappears":
						return disks[:2], nil
					case "second-disk-read-fails":
						return nil, errors.New("GEOM unavailable")
					}
				}
				return disks, nil
			}
			var err error
			if replace {
				err = service.ReplaceDevice(t.Context(), "101", "202", "da7")
			} else {
				err = service.DetachDevice(t.Context(), "101", "202")
			}
			if err == nil || len(runner.mutations) != 0 || inspector.locked || inspector.releases != 1 {
				t.Fatalf("changed preflight admitted: %v commands=%v", err, runner.mutations)
			}
		})
	}
}

func TestPoolReplacementAllowsRAIDZAndStripeMembers(t *testing.T) {
	for _, layout := range []string{"raidz2", "stripe"} {
		service, runner, _ := newPoolDeviceTestService(t)
		if layout == "stripe" {
			runner.status.Vdevs["tank"].Vdevs = poolDeviceTestMirror(runner).Vdevs
		} else {
			poolDeviceTestMirror(runner).VdevType = layout
		}
		if err := service.ReplaceDevice(t.Context(), "101", "202", "da7"); err != nil || len(runner.mutations) != 1 {
			t.Fatalf("valid %s member replacement refused: %v commands=%v", layout, err, runner.mutations)
		}
	}
}

func TestPoolDeviceMutationsRejectAuxiliaryAndParentTargets(t *testing.T) {
	for _, source := range []string{"1001", "204", "205"} {
		for _, replace := range []bool{false, true} {
			service, runner, _ := newPoolDeviceTestService(t)
			runner.status.Spares = map[string]*gzfs.ZPoolStatusVDEV{"spare": {Name: "da2", GUID: "204", VdevType: "disk", State: "AVAIL"}}
			runner.status.L2Cache = map[string]*gzfs.ZPoolStatusVDEV{"cache": {Name: "da3", GUID: "205", VdevType: "disk", State: "ONLINE"}}
			var err error
			if replace {
				err = service.ReplaceDevice(t.Context(), "101", source, "da7")
			} else {
				err = service.DetachDevice(t.Context(), "101", source)
			}
			if !errors.Is(err, ErrConflict) || len(runner.mutations) != 0 {
				t.Fatalf("auxiliary/parent target %s admitted: %v commands=%v", source, err, runner.mutations)
			}
		}
	}
}

func TestPoolDeviceMutationsRespectClientPreconditions(t *testing.T) {
	for _, checks := range []zfsServiceInterfaces.PoolDeviceMutationOptions{
		{ExpectedNewIdentity: "old-replacement-identity"},
		{ExpectedParentGUID: "1002", ExpectedMemberGUIDs: []string{"202", "203"}},
		{ExpectedParentGUID: "1001", ExpectedMemberGUIDs: []string{"202", "204"}},
	} {
		service, runner, _ := newPoolDeviceTestService(t)
		if err := service.ReplaceDevice(t.Context(), "101", "202", "da7", checks); !errors.Is(err, ErrConflict) || len(runner.mutations) != 0 {
			t.Fatalf("stale confirmation admitted: %v commands=%v", err, runner.mutations)
		}
		if err := service.AttachDevice(t.Context(), "101", "202", "da7", checks); !errors.Is(err, ErrConflict) || len(runner.mutations) != 0 {
			t.Fatalf("stale attachment confirmation admitted: %v commands=%v", err, runner.mutations)
		}
	}
	for _, checks := range []zfsServiceInterfaces.PoolDeviceMutationOptions{
		{ExpectedParentGUID: "1001"},
		{ExpectedMemberGUIDs: []string{"202", "203"}},
		{ExpectedParentGUID: "1001", ExpectedMemberGUIDs: []string{"202", "202"}},
		{ExpectedParentGUID: "invalid", ExpectedMemberGUIDs: []string{"202", "203"}},
	} {
		service, runner, inspector := newPoolDeviceTestService(t)
		if err := service.DetachDevice(t.Context(), "101", "202", checks); !errors.Is(err, ErrInvalidRequest) || len(runner.mutations) != 0 || inspector.reads != 0 {
			t.Fatalf("invalid preconditions reached hardware: %v", err)
		}
	}
}

func TestPoolDeviceMutationsRejectUnmanagedPoolsAndQueuedScrubs(t *testing.T) {
	service, runner, _ := newPoolDeviceTestService(t)
	var settings models.BasicSettings
	if err := service.DB.First(&settings).Error; err != nil {
		t.Fatal(err)
	}
	settings.Pools = []string{}
	if err := service.DB.Save(&settings).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.DetachDevice(t.Context(), "101", "202"); !errors.Is(err, ErrPoolNotFound) || runner.statusReads != 0 || len(runner.mutations) != 0 {
		t.Fatalf("unselected pool was scanned or changed: %v status reads=%d", err, runner.statusReads)
	}
	service, runner, _ = newPoolDeviceTestService(t)
	if err := service.DB.Create(&zfsModels.PoolScrubSchedule{
		PoolGUID: "101", PoolName: "tank", CronExpr: "0 3 1 * *", Enabled: true, LastStatus: "queued",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.ReplaceDevice(t.Context(), "101", "202", "da7"); !errors.Is(err, ErrConflict) || len(runner.mutations) != 0 {
		t.Fatalf("queued scrub was bypassed: %v", err)
	}
	if err := service.AttachDevice(t.Context(), "101", "202", "da7"); !errors.Is(err, ErrConflict) || len(runner.mutations) != 0 {
		t.Fatalf("attachment bypassed a queued scrub: %v", err)
	}
}

func TestPoolReplacementRejectsUnselectedPoolSparesWithoutScanningThatPool(t *testing.T) {
	service, runner, _ := newPoolDeviceTestService(t)
	runner.otherPools = map[string]any{"foreign": map[string]any{
		"name": "foreign", "pool_guid": "909", "state": "ONLINE",
		"vdevs": map[string]any{"spares": map[string]*gzfs.ZPoolVDEV{
			"da7": {Name: "da7", Path: "/dev/da7", GUID: "207", VdevType: "disk", State: "AVAIL"},
		}},
	}}
	if err := service.ReplaceDevice(t.Context(), "101", "202", "da7"); !errors.Is(err, ErrConflict) || len(runner.mutations) != 0 || runner.statusReads != 2 {
		t.Fatalf("foreign spare ownership was ignored or unrelated status scanned: %v commands=%v status=%d", err, runner.mutations, runner.statusReads)
	}
}

func TestPoolDeviceMutationsPermitUnscannedPoolsAndFailedSourceReplacement(t *testing.T) {
	for _, replace := range []bool{false, true} {
		service, runner, inspector := newPoolDeviceTestService(t)
		runner.status.ScanStats = nil
		poolDeviceTestMirror(runner).Vdevs["da0"].State = "UNAVAIL"
		runner.status.State = "DEGRADED"
		inspector.devices = inspector.devices[1:]
		var err error
		if replace {
			err = service.ReplaceDevice(t.Context(), "101", "202", "da7")
		} else {
			err = service.DetachDevice(t.Context(), "101", "202")
		}
		if err != nil || len(runner.mutations) != 1 {
			t.Fatalf("failed source recovery refused: %v commands=%v", err, runner.mutations)
		}
	}
}

func TestPoolDeviceMutationsRejectUnsafeIdentifiersBeforeInspection(t *testing.T) {
	for _, value := range []string{"", "0", "18446744073709551616", "-f", "../da0", "/tmp/disk", "/dev/../da0", "/dev//da0", "da0\x00", " da0", "da0;reboot"} {
		service, runner, inspector := newPoolDeviceTestService(t)
		if err := service.DetachDevice(t.Context(), "101", value); !errors.Is(err, ErrInvalidRequest) || inspector.reads != 0 || len(runner.mutations) != 0 {
			t.Fatalf("unsafe source %q reached inspection: %v", value, err)
		}
	}
	for _, value := range []string{"", "-f", "../da7", "/tmp/disk", "/dev/da7/../da8", "da7\n"} {
		service, runner, inspector := newPoolDeviceTestService(t)
		if err := service.ReplaceDevice(t.Context(), "101", "202", value); !errors.Is(err, ErrInvalidRequest) || inspector.reads != 0 || len(runner.mutations) != 0 {
			t.Fatalf("unsafe replacement %q reached inspection: %v", value, err)
		}
	}
}

func TestPoolDeviceMutationsDoNotRetryUncertainCommands(t *testing.T) {
	for _, action := range []string{"detach", "replace", "attach"} {
		service, runner, inspector := newPoolDeviceTestService(t)
		runner.commandErr = errors.New("command response lost")
		var err error
		switch action {
		case "attach":
			err = service.AttachDevice(t.Context(), "101", "202", "da7")
		case "replace":
			err = service.ReplaceDevice(t.Context(), "101", "202", "da7")
		case "detach":
			err = service.DetachDevice(t.Context(), "101", "202")
		}
		if !errors.Is(err, ErrConflict) || len(runner.mutations) != 1 || inspector.locked || inspector.releases != 1 {
			t.Fatalf("uncertain command retried or reported success: %v commands=%v", err, runner.mutations)
		}
		if runner.listReads != 2 || runner.statusReads != 2 || inspector.reads != 2 {
			t.Fatalf("uncertain command triggered more reads: lists=%d status=%d disks=%d", runner.listReads, runner.statusReads, inspector.reads)
		}
		if slices.Contains(runner.mutations[0], "-f") {
			t.Fatal("device mutation was forced")
		}
	}
}

func TestPoolDiskIndexRejectsAmbiguousAliases(t *testing.T) {
	devices := []diskServiceInterfaces.PoolDevice{
		{Path: "/dev/da0", Aliases: []string{"/dev/da0", "/dev/gpt/source", "/dev/shared"}},
		{Path: "/dev/da1", Aliases: []string{"/dev/shared", "/dev/shared"}},
		{Path: "/dev/da2", Aliases: []string{"/dev/shared"}},
	}
	index := indexPoolDisks(devices)
	for _, path := range []string{"da0", "/dev/da0", "gpt/source", "/dev/gpt/source"} {
		if disk, err := index.find(path); err != nil || disk != &devices[0] {
			t.Fatalf("alias %q resolved incorrectly: %+v %v", path, disk, err)
		}
	}
	if _, err := index.find("shared"); !errors.Is(err, ErrConflict) {
		t.Fatalf("shared alias became unambiguous after repeated entries: %v", err)
	}
	for _, path := range []string{"202", "", "/dev/missing"} {
		if disk, err := index.find(path); disk != nil || err != nil {
			t.Fatalf("unknown status identifier %q resolved as a disk: %+v %v", path, disk, err)
		}
	}
}

func TestPoolDeviceMutationsRejectReusedLegacyAliases(t *testing.T) {
	for _, replace := range []bool{false, true} {
		service, runner, inspector := newPoolDeviceTestService(t)
		inspector.onRead = func(read int, disks []diskServiceInterfaces.PoolDevice) ([]diskServiceInterfaces.PoolDevice, error) {
			if read == 2 {
				disks[0].Aliases = nil
				disks[1].Aliases = []string{"/dev/gptid/source"}
			}
			return disks, nil
		}
		var err error
		if replace {
			err = service.ReplaceDevice(t.Context(), "101", "gptid/source", "da7")
		} else {
			err = service.DetachDevice(t.Context(), "101", "gptid/source")
		}
		if !errors.Is(err, ErrConflict) || len(runner.mutations) != 0 || inspector.locked {
			t.Fatalf("reused legacy alias targeted a different member: %v commands=%v", err, runner.mutations)
		}
	}
}

func TestPoolDeviceMutationsDoNotSubmitAfterCancellation(t *testing.T) {
	for _, action := range []string{"replace", "attach"} {
		service, runner, inspector := newPoolDeviceTestService(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		inspector.onRead = func(read int, disks []diskServiceInterfaces.PoolDevice) ([]diskServiceInterfaces.PoolDevice, error) {
			if read == 2 {
				cancel()
			}
			return disks, nil
		}
		var err error
		if action == "attach" {
			err = service.AttachDevice(ctx, "101", "202", "da7")
		} else {
			err = service.ReplaceDevice(ctx, "101", "202", "da7")
		}
		if !errors.Is(err, context.Canceled) || len(runner.mutations) != 0 || inspector.locked || inspector.releases != 1 {
			t.Fatalf("canceled %s submitted or leaked ownership: %v commands=%v", action, err, runner.mutations)
		}
	}
}
