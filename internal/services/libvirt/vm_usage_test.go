// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"errors"
	"math"
	"strconv"
	"testing"
	"time"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/digitalocean/go-libvirt"
)

type fakeVMUsageSource struct {
	ensureErr error
	lookupErr error
	info      func(domain libvirt.Domain) (vmUsageDomainInfo, error)
	memory    func(domain libvirt.Domain) (uint64, uint64, error)
}

func (f fakeVMUsageSource) EnsureConnection() error {
	return f.ensureErr
}

func (f fakeVMUsageSource) LookupDomain(rid uint) (libvirt.Domain, error) {
	if f.lookupErr != nil {
		return libvirt.Domain{}, f.lookupErr
	}
	return libvirt.Domain{Name: strconv.Itoa(int(rid))}, nil
}

func (f fakeVMUsageSource) DomainInfo(domain libvirt.Domain) (vmUsageDomainInfo, error) {
	if f.info == nil {
		return vmUsageDomainInfo{}, errors.New("domain_info_unavailable")
	}
	return f.info(domain)
}

func (f fakeVMUsageSource) MemoryStats(domain libvirt.Domain) (uint64, uint64, error) {
	if f.memory == nil {
		return 0, 0, errors.New("memory_stats_unavailable")
	}
	return f.memory(domain)
}

func newVMUsageTestService(t *testing.T, rids ...uint) (*Service, []vmModels.VM) {
	t.Helper()

	database := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.VMStats{})
	vms := make([]vmModels.VM, 0, len(rids))
	for _, rid := range rids {
		vms = append(vms, vmModels.VM{RID: rid, Name: "vm-" + strconv.Itoa(int(rid))})
	}
	if len(vms) > 0 {
		if err := database.Create(&vms).Error; err != nil {
			t.Fatalf("seed vms: %v", err)
		}
	}

	service := &Service{DB: database}
	service.lastVMUsageRetention = time.Now()
	return service, vms
}

func stubVMUsageSleep(t *testing.T, waits *int) {
	t.Helper()

	original := vmUsageSleep
	t.Cleanup(func() {
		vmUsageSleep = original
	})
	vmUsageSleep = func(time.Duration) {
		*waits = *waits + 1
	}
}

func twoPassInfo(vcpus uint16, maxMemKB uint64) func(libvirt.Domain) (vmUsageDomainInfo, error) {
	calls := map[string]int{}
	return func(domain libvirt.Domain) (vmUsageDomainInfo, error) {
		calls[domain.Name]++
		cpuTime := uint64(1_000_000_000)
		if calls[domain.Name] > 1 {
			cpuTime += 1_000_000_000
		}
		return vmUsageDomainInfo{vcpus: vcpus, cpuTime: cpuTime, maxMemKB: maxMemKB}, nil
	}
}

func TestStoreVMUsageSamplesEveryGuestWithSingleWait(t *testing.T) {
	service, _ := newVMUsageTestService(t, 101, 102, 103)

	waits := 0
	stubVMUsageSleep(t, &waits)

	service.vmUsageSource = fakeVMUsageSource{
		info: twoPassInfo(2, 4<<20),
		memory: func(libvirt.Domain) (uint64, uint64, error) {
			return 1 << 20, 4 << 20, nil
		},
	}

	if err := service.StoreVMUsage(); err != nil {
		t.Fatalf("store vm usage: %v", err)
	}

	if waits != 1 {
		t.Fatalf("sample waits = %d, want exactly one per pass", waits)
	}

	var stats []vmModels.VMStats
	if err := service.DB.Order("vm_id ASC").Find(&stats).Error; err != nil {
		t.Fatalf("load vm stats: %v", err)
	}
	if len(stats) != 3 {
		t.Fatalf("stats rows = %d, want 3", len(stats))
	}
	for _, row := range stats {
		if math.Abs(row.CPUUsage-50) > 0.001 {
			t.Errorf("vm %d cpu usage = %v, want 50", row.VMID, row.CPUUsage)
		}
		if math.Abs(row.MemoryUsage-25) > 0.001 {
			t.Errorf("vm %d memory usage = %v, want 25", row.VMID, row.MemoryUsage)
		}
	}
}

func TestStoreVMUsageDoesNotHoldVMMutationLock(t *testing.T) {
	service, _ := newVMUsageTestService(t, 107)
	service.vmUsageSource = fakeVMUsageSource{
		info:   twoPassInfo(1, 1<<20),
		memory: func(libvirt.Domain) (uint64, uint64, error) { return 1 << 19, 1 << 20, nil },
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	original := vmUsageSleep
	t.Cleanup(func() {
		vmUsageSleep = original
	})
	vmUsageSleep = func(time.Duration) {
		close(entered)
		<-release
	}

	done := make(chan error, 1)
	go func() {
		done <- service.StoreVMUsage()
	}()

	<-entered
	if !service.crudMutex.TryLock() {
		t.Fatal("StoreVMUsage holds crudMutex while sampling")
	}
	service.crudMutex.Unlock()
	close(release)

	if err := <-done; err != nil {
		t.Fatalf("store vm usage: %v", err)
	}
}

func TestStoreVMUsageSkipsSampleWhenRSSUnavailable(t *testing.T) {
	service, _ := newVMUsageTestService(t, 110)

	waits := 0
	stubVMUsageSleep(t, &waits)

	service.vmUsageSource = fakeVMUsageSource{
		info: twoPassInfo(2, 2<<20),
		memory: func(libvirt.Domain) (uint64, uint64, error) {
			return 0, 2 << 20, errors.New("rss_not_reported")
		},
	}

	if err := service.StoreVMUsage(); err != nil {
		t.Fatalf("store vm usage: %v", err)
	}

	var count int64
	if err := service.DB.Model(&vmModels.VMStats{}).Count(&count).Error; err != nil {
		t.Fatalf("count vm stats: %v", err)
	}
	if count != 0 {
		t.Fatalf("stats rows = %d, want none when RSS is unavailable", count)
	}
}

func TestStoreVMUsageDropsSampleForDeletedGuest(t *testing.T) {
	service, vms := newVMUsageTestService(t, 120)

	original := vmUsageSleep
	t.Cleanup(func() {
		vmUsageSleep = original
	})
	vmUsageSleep = func(time.Duration) {
		if err := service.DB.Delete(&vmModels.VM{}, vms[0].ID).Error; err != nil {
			t.Errorf("delete guest while sampling: %v", err)
		}
	}

	service.vmUsageSource = fakeVMUsageSource{
		info:   twoPassInfo(2, 2<<20),
		memory: func(libvirt.Domain) (uint64, uint64, error) { return 1 << 20, 2 << 20, nil },
	}

	if err := service.StoreVMUsage(); err != nil {
		t.Fatalf("store vm usage: %v", err)
	}

	var count int64
	if err := service.DB.Model(&vmModels.VMStats{}).Count(&count).Error; err != nil {
		t.Fatalf("count vm stats: %v", err)
	}
	if count != 0 {
		t.Fatalf("stats rows = %d, want none for a guest deleted mid-pass", count)
	}
}

func TestStoreVMUsageDropsSampleForReusedRID(t *testing.T) {
	service, vms := newVMUsageTestService(t, 130)
	deletedID := vms[0].ID

	original := vmUsageSleep
	t.Cleanup(func() {
		vmUsageSleep = original
	})
	vmUsageSleep = func(time.Duration) {
		if err := service.DB.Delete(&vmModels.VM{}, deletedID).Error; err != nil {
			t.Errorf("delete guest while sampling: %v", err)
		}
		if err := service.DB.Create(&vmModels.VM{RID: 130, Name: "reused-rid"}).Error; err != nil {
			t.Errorf("recreate guest with the reused RID: %v", err)
		}
	}

	service.vmUsageSource = fakeVMUsageSource{
		info:   twoPassInfo(2, 2<<20),
		memory: func(libvirt.Domain) (uint64, uint64, error) { return 1 << 20, 2 << 20, nil },
	}

	if err := service.StoreVMUsage(); err != nil {
		t.Fatalf("store vm usage: %v", err)
	}

	var count int64
	if err := service.DB.Model(&vmModels.VMStats{}).Count(&count).Error; err != nil {
		t.Fatalf("count vm stats: %v", err)
	}
	if count != 0 {
		t.Fatalf("stats rows = %d, want none for a reused RID", count)
	}
}

func TestApplyVMStatsRetentionIfDueSweepsStaleSamplesOnlyWhenDue(t *testing.T) {
	service, vms := newVMUsageTestService(t, 140)
	service.isDomainShutOffFn = func(uint) (bool, error) {
		return false, nil
	}

	countRows := func(t *testing.T) int64 {
		t.Helper()
		var rows int64
		if err := service.DB.Model(&vmModels.VMStats{}).Count(&rows).Error; err != nil {
			t.Fatalf("count vm stats: %v", err)
		}
		return rows
	}

	if err := service.DB.Create(&vmModels.VMStats{
		VMID:      vms[0].ID,
		CPUUsage:  1,
		CreatedAt: time.Now().Add(-71 * 24 * time.Hour),
	}).Error; err != nil {
		t.Fatalf("seed stale stats: %v", err)
	}

	service.lastVMUsageRetention = time.Now()
	if err := service.applyVMStatsRetentionIfDue(); err != nil {
		t.Fatalf("retention inside cadence: %v", err)
	}
	if got := countRows(t); got != 1 {
		t.Fatalf("rows inside cadence = %d, want 1", got)
	}

	service.lastVMUsageRetention = time.Now().Add(-2 * vmUsageRetentionCadence)
	if err := service.applyVMStatsRetentionIfDue(); err != nil {
		t.Fatalf("retention past cadence: %v", err)
	}
	if got := countRows(t); got != 0 {
		t.Fatalf("rows past cadence = %d, want 0", got)
	}
	if time.Since(service.lastVMUsageRetention) > vmUsageRetentionCadence {
		t.Fatal("retention timestamp was not refreshed")
	}
}

func TestApplyVMStatsRetentionSkipsMissingGuests(t *testing.T) {
	service, _ := newVMUsageTestService(t)

	consultedHypervisor := false
	service.isDomainShutOffFn = func(uint) (bool, error) {
		consultedHypervisor = true
		return false, nil
	}

	if err := service.DB.Create(&vmModels.VMStats{VMID: 4242, CPUUsage: 1}).Error; err != nil {
		t.Fatalf("seed orphan stats: %v", err)
	}

	service.lastVMUsageRetention = time.Time{}
	if err := service.applyVMStatsRetentionIfDue(); err != nil {
		t.Fatalf("retention with orphan stats: %v", err)
	}
	if consultedHypervisor {
		t.Fatal("retention consulted the hypervisor for a guest that no longer exists")
	}
}
