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
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/metrics"
	"github.com/digitalocean/go-libvirt"
	"github.com/rs/zerolog"
	"gorm.io/gorm"
)

func (s *Service) PruneOrphanedVMStats() error {
	if err := s.DB.
		Where(
			"vm_id NOT IN (?)",
			s.DB.
				Model(&vmModels.VM{}).
				Select("id"),
		).
		Delete(&vmModels.VMStats{}).
		Error; err != nil {
		return fmt.Errorf("failed to prune orphaned VMStats: %w", err)
	}
	return nil
}

func (s *Service) ApplyVMStatsRetention() error {
	var vmIDs []uint
	if err := s.DB.
		Model(&vmModels.VMStats{}).
		Select("DISTINCT vm_id").
		Pluck("vm_id", &vmIDs).Error; err != nil {
		return fmt.Errorf("failed_to_get_vm_ids_for_retention: %w", err)
	}

	now := time.Now()

	for _, vmID := range vmIDs {
		var stats []vmModels.VMStats
		if err := s.DB.
			Where("vm_id = ?", vmID).
			Order("created_at ASC").
			Find(&stats).Error; err != nil {
			return fmt.Errorf("failed_to_get_vm_stats_for_retention: %w", err)
		}

		if len(stats) == 0 {
			continue
		}

		var vm vmModels.VM
		if err := s.DB.Model(&vmModels.VM{}).
			Select("rid").
			Where("id = ?", vmID).
			First(&vm).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			logger.L.Error().Err(err).Uint("vm_id", vmID).Msg("failed_to_resolve_vm_for_retention")
			continue
		}

		isOff, err := s.isDomainShutOff(vm.RID)
		if err != nil {
			logger.L.Error().Err(err).Uint("vm_id", vmID).Msg("failed_to_check_if_domain_is_shutoff_for_retention")
			continue
		}

		if !isOff {
			_, deleteIDs := db.ApplyGFS(now, stats)
			if len(deleteIDs) == 0 {
				continue
			}

			if err := s.DB.
				Where("id IN ?", deleteIDs).
				Delete(&vmModels.VMStats{}).Error; err != nil {
				return fmt.Errorf("failed_to_delete_old_vm_stats: %w", err)
			}
		}
	}

	if err := s.PruneOrphanedVMStats(); err != nil {
		return err
	}

	return nil
}

const (
	vmUsageSampleInterval   = time.Second
	vmUsageRetentionCadence = time.Hour
)

type vmUsageDomainInfo struct {
	state    libvirt.DomainState
	vcpus    uint16
	cpuTime  uint64
	maxMemKB uint64
}

type vmUsageDomainSource interface {
	EnsureConnection() error
	LookupDomain(rid uint) (libvirt.Domain, error)
	DomainInfo(domain libvirt.Domain) (vmUsageDomainInfo, error)
	MemoryStats(domain libvirt.Domain) (rssKB uint64, availableKB uint64, err error)
}

type libvirtVMUsageSource struct {
	service *Service
}

func (src libvirtVMUsageSource) EnsureConnection() error {
	return src.service.requireConnection()
}

func (src libvirtVMUsageSource) LookupDomain(rid uint) (libvirt.Domain, error) {
	return src.service.conn().DomainLookupByName(strconv.Itoa(int(rid)))
}

func (src libvirtVMUsageSource) DomainInfo(domain libvirt.Domain) (vmUsageDomainInfo, error) {
	state, maxMemKB, _, vcpus, cpuTime, err := src.service.conn().DomainGetInfo(domain)
	if err != nil {
		return vmUsageDomainInfo{}, err
	}
	return vmUsageDomainInfo{state: libvirt.DomainState(state), vcpus: vcpus, cpuTime: cpuTime, maxMemKB: maxMemKB}, nil
}

func (src libvirtVMUsageSource) MemoryStats(domain libvirt.Domain) (uint64, uint64, error) {
	stats, err := src.service.conn().DomainMemoryStats(domain, 8, 0)
	if err != nil {
		return 0, 0, err
	}

	var rssKB, availableKB uint64
	for _, stat := range stats {
		switch libvirt.DomainMemoryStatTags(stat.Tag) {
		case libvirt.DomainMemoryStatRss:
			rssKB = stat.Val
		case libvirt.DomainMemoryStatAvailable:
			availableKB = stat.Val
		}
	}
	return rssKB, availableKB, nil
}

var vmUsageSleep = time.Sleep

type vmUsageSample struct {
	vmID     uint
	rid      uint
	domain   libvirt.Domain
	vcpus    uint16
	cpuTime1 uint64
	guest    metrics.VM
}

func (s *Service) vmUsageDomains() vmUsageDomainSource {
	if s.vmUsageSource != nil {
		return s.vmUsageSource
	}
	return libvirtVMUsageSource{service: s}
}

// StoreVMUsage samples CPU and memory usage without taking crudMutex.
func (s *Service) StoreVMUsage() error {
	if err := s.sampleVMUsage(); err != nil {
		return err
	}
	return s.applyVMStatsRetentionIfDue()
}

func (s *Service) sampleVMUsage() error {
	var vms []vmModels.VM
	if err := s.DB.Model(&vmModels.VM{}).Select("id", "rid", "name", "ram", "cpu_sockets", "cpu_cores", "cpu_threads").Find(&vms).Error; err != nil {
		s.metricsCache.Fail()
		return fmt.Errorf("failed_to_get_vm_usage_targets: %w", err)
	}

	source := s.vmUsageDomains()
	if len(vms) > 0 {
		if err := source.EnsureConnection(); err != nil {
			s.metricsCache.Fail()
			return err
		}
	}

	// Per-guest failures are reported by vm_collection_success, not collector health.
	var guests []metrics.VM
	success := true
	samples := make([]vmUsageSample, 0, len(vms))
	for _, vm := range vms {
		if vm.RID == 0 {
			continue
		}

		guest := metrics.VM{
			RID: vm.RID, Name: vm.Name, MemoryConfigured: vm.RAM,
			VCPUs: vm.CPUSockets * vm.CPUCores * vm.CPUThreads,
		}
		domain, err := source.LookupDomain(vm.RID)
		if err != nil {
			guests = append(guests, guest)
			continue
		}

		info, err := source.DomainInfo(domain)
		if err != nil || info.vcpus == 0 {
			guests = append(guests, guest)
			continue
		}

		samples = append(samples, vmUsageSample{
			vmID:     vm.ID,
			rid:      vm.RID,
			domain:   domain,
			vcpus:    info.vcpus,
			cpuTime1: info.cpuTime,
			guest:    guest,
		})
	}

	if len(samples) > 0 {
		vmUsageSleep(vmUsageSampleInterval)

		for i := range samples {
			guest, ok := s.storeVMUsageSample(source, samples[i])
			if guest != nil {
				guests = append(guests, *guest)
			}
			success = success && ok
		}
	}

	s.metricsCache.Store(guests, success)
	return nil
}

func (s *Service) MetricsSnapshot() metrics.Snapshot[metrics.VM] {
	return s.metricsCache.Snapshot()
}

func (s *Service) storeVMUsageSample(source vmUsageDomainSource, sample vmUsageSample) (*metrics.VM, bool) {
	guest := sample.guest
	info, err := source.DomainInfo(sample.domain)
	var rssKB, availableKB uint64
	if err == nil {
		guest.State = metrics.VMStateName(int(info.state))
		guest.Success = guest.State != ""
		switch info.state {
		case libvirt.DomainRunning, libvirt.DomainBlocked, libvirt.DomainPaused, libvirt.DomainShutdown, libvirt.DomainPmsuspended:
			guest.CPUSeconds = metrics.OptionalValue{Value: float64(info.cpuTime) / 1e9, Available: true}
			rssKB, availableKB, err = source.MemoryStats(sample.domain)
			if err != nil || rssKB == 0 {
				guest.Success = false
				reason := "rss_not_reported"
				if err != nil {
					reason = err.Error()
				}
				logger.LogWithDeduplication(zerolog.WarnLevel, fmt.Sprintf(
					"vm_usage_memory_stats_unavailable: rid=%d reason=%s", sample.rid, reason,
				))
			} else {
				guest.MemoryRSS = metrics.OptionalValue{Value: float64(rssKB * 1024), Available: true}
			}
		}
	}

	// The guest may have been deleted, and its RID reused, while sampling.
	var currentID uint
	err = s.DB.Model(&vmModels.VM{}).
		Where("id = ? AND rid = ?", sample.vmID, sample.rid).
		Select("id").
		Scan(&currentID).Error
	if err != nil {
		logger.LogWithDeduplication(zerolog.WarnLevel, fmt.Sprintf(
			"vm_usage_identity_check_failed: rid=%d err=%v", sample.rid, err,
		))
		return nil, false
	}
	if currentID == 0 {
		return nil, true
	}

	if !guest.MemoryRSS.Available || sample.vcpus == 0 || info.cpuTime <= sample.cpuTime1 {
		return &guest, true
	}
	cpuUsage := (float64(info.cpuTime-sample.cpuTime1) / 1e9) / float64(sample.vcpus) * 100
	maxMemMB := float64(info.maxMemKB) / 1024
	if availableKB > 0 {
		maxMemMB = float64(availableKB) / 1024
	}
	usedMemMB := float64(rssKB) / 1024
	memUsagePercent := 0.0
	if maxMemMB > 0 {
		memUsagePercent = usedMemMB / maxMemMB * 100
	}

	stats := &vmModels.VMStats{
		VMID:        sample.vmID,
		CPUUsage:    math.Max(0, math.Min(100, cpuUsage)),
		MemoryUsage: math.Max(0, math.Min(100, memUsagePercent)),
		MemoryUsed:  usedMemMB,
	}
	if err := s.DB.Create(stats).Error; err != nil {
		logger.LogWithDeduplication(zerolog.WarnLevel, fmt.Sprintf(
			"failed_to_store_vm_usage: rid=%d err=%v", sample.rid, err,
		))
	}
	return &guest, true
}

func (s *Service) applyVMStatsRetentionIfDue() error {
	now := time.Now()

	s.vmUsageRetentionMu.Lock()
	due := s.lastVMUsageRetention.IsZero() ||
		now.Sub(s.lastVMUsageRetention) >= vmUsageRetentionCadence
	s.vmUsageRetentionMu.Unlock()

	if !due {
		return nil
	}

	if err := s.ApplyVMStatsRetention(); err != nil {
		return err
	}

	s.vmUsageRetentionMu.Lock()
	s.lastVMUsageRetention = now
	s.vmUsageRetentionMu.Unlock()
	return nil
}

func (s *Service) GetVMUsage(rid int, step db.GFSStep) ([]vmModels.VMStats, error) {
	vmId, err := s.GetVMIDByRID(uint(rid))
	if err != nil {
		return nil, err
	}

	if vmId == 0 {
		return nil, fmt.Errorf("vm_not_found")
	}

	window, err := step.Window()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	from := now.Add(-window)

	var vmStats []vmModels.VMStats
	if err := s.DB.
		Where("vm_id = ? AND created_at >= ?", vmId, from).
		Order("created_at ASC").
		Find(&vmStats).Error; err != nil {
		return nil, fmt.Errorf("failed_to_get_vm_usage: %w", err)
	}

	return vmStats, nil
}

func (s *Service) GetVMUsageBootstrap(rid int) (db.StatsBootstrap[vmModels.VMStats], error) {
	return s.getVMUsageBootstrapAt(rid, time.Now())
}

func (s *Service) getVMUsageBootstrapAt(
	rid int,
	now time.Time,
) (db.StatsBootstrap[vmModels.VMStats], error) {
	empty := db.BuildStatsBootstrap[vmModels.VMStats](now, nil, nil)

	vmID, err := s.GetVMIDByRID(uint(rid))
	if err != nil {
		return empty, err
	}
	if vmID == 0 {
		return empty, fmt.Errorf("vm_not_found")
	}

	var latestAt *time.Time
	var latest vmModels.VMStats
	result := s.DB.
		Select("created_at").
		Where("vm_id = ?", vmID).
		Order("created_at DESC").
		Limit(1).
		Find(&latest)
	if result.Error != nil {
		return empty, fmt.Errorf("failed_to_get_latest_vm_usage: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		latestAt = &latest.CreatedAt
	}

	availability := db.ResolveStatsAvailability(now, latestAt)
	if availability.ResolvedStep == nil {
		return db.BuildStatsBootstrap[vmModels.VMStats](now, nil, latestAt), nil
	}

	window, err := availability.ResolvedStep.Window()
	if err != nil {
		return empty, err
	}

	var points []vmModels.VMStats
	if err := s.DB.
		Where("vm_id = ? AND created_at >= ?", vmID, now.Add(-window)).
		Order("created_at ASC").
		Find(&points).Error; err != nil {
		return empty, fmt.Errorf("failed_to_get_vm_usage_bootstrap: %w", err)
	}

	return db.BuildStatsBootstrap(now, points, latestAt), nil
}
