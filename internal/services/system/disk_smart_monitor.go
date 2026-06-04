// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	"github.com/alchemillahq/sylve/internal/logger"
	notifier "github.com/alchemillahq/sylve/internal/notifications"

	"github.com/rs/zerolog"
)

const (
	diskSmartMonitorInterval        = 30 * time.Minute
	diskSmartConsecutiveTrigger     = 2
	diskSmartConsecutiveClear       = 1
	diskSmartConsecutiveUnavailable = 3
)

const (
	diskSmartConfigTemperatureWarningCelsius  = "warningCelsius"
	diskSmartConfigTemperatureCriticalCelsius = "criticalCelsius"
	diskSmartConfigWearoutWarningPercent      = "warningPercent"
	diskSmartConfigWearoutCriticalPercent     = "criticalPercent"
)

const (
	defaultTemperatureWarningCelsius  = 55.0
	defaultTemperatureCriticalCelsius = 65.0
	defaultWearoutWarningPercent      = 80.0
	defaultWearoutCriticalPercent     = 90.0
)

type diskSmartConfig struct {
	WarningCelsius  float64 `json:"warningCelsius"`
	CriticalCelsius float64 `json:"criticalCelsius"`
	WarningPercent  float64 `json:"warningPercent"`
	CriticalPercent float64 `json:"criticalPercent"`
}

type diskSmartState struct {
	passed               bool
	temperature          int
	wearoutPct           float64
	reallocatedSectors   int64
	pendingSectors       int64
	uncorrectableSectors int64

	nvmeAvailableSpare  int
	nvmeSpareThreshold  int
	nvmeMediaErrors     int
	nvmeCriticalWarning bool

	tempWarningCount   int
	tempCriticalCount  int
	tempNormalCount    int
	wearWarningCount   int
	wearCriticalCount  int
	wearNormalCount    int
	healthFailCount    int
	healthNormalCount  int
	reallocCount       int
	reallocNormalCount int
	nvmeWarnCount      int
	nvmeNormalCount    int

	unavailableCount int
}

func (s *Service) StartDiskSmartMonitor(ctx context.Context) {
	if s.DiskService == nil {
		logger.L.Warn().Msg("disk_smart_monitor_skipped_no_disk_service")
		return
	}

	go s.runDiskSmartMonitor(ctx)
}

func (s *Service) runDiskSmartMonitor(ctx context.Context) {
	logger.L.Info().Msg("starting_disk_smart_monitor")

	warmup := true
	stateByDevice := map[string]*diskSmartState{}
	var mu sync.Mutex

	tickAndSleep := func() {
		timer := time.NewTimer(diskSmartMonitorInterval)
		defer timer.Stop()

		select {
		case <-ctx.Done():
		case <-timer.C:
		}
	}

	for {
		select {
		case <-ctx.Done():
			logger.L.Debug().Msg("stopped_disk_smart_monitor")
			return
		default:
		}

		disks, err := s.DiskService.GetDiskDevices(ctx)
		if err != nil {
			logger.L.Warn().Err(err).Msg("disk_smart_monitor_failed_to_get_disks")
			tickAndSleep()
			continue
		}

		for _, disk := range disks {
			if disk.SmartData == nil {
				s.handleMissingSmart(ctx, &mu, stateByDevice, disk.Device)
				continue
			}

			mu.Lock()
			st, exists := stateByDevice[disk.Device]
			if !exists {
				st = &diskSmartState{}
				stateByDevice[disk.Device] = st
			}
			mu.Unlock()

			s.evaluateSmartData(ctx, disk, st, warmup)
		}

		if warmup {
			warmup = false
			logger.L.Debug().Msg("disk_smart_monitor_warmup_complete")
		}

		tickAndSleep()
	}
}

func (s *Service) handleMissingSmart(ctx context.Context, mu *sync.Mutex, stateByDevice map[string]*diskSmartState, device string) {
	mu.Lock()
	st, exists := stateByDevice[device]
	if !exists {
		mu.Unlock()
		return
	}

	st.unavailableCount++
	count := st.unavailableCount
	mu.Unlock()

	if count == diskSmartConsecutiveUnavailable {
		s.emitDiskSmartNotification(ctx, device, notifier.DiskSmartHealthKindPrefix,
			"smart unavailable", "warning",
			fmt.Sprintf("SMART data unavailable for disk %s", device),
			fmt.Sprintf("The disk %s previously returned valid S.M.A.R.T data but is now unavailable.", device),
			map[string]string{
				"condition": "smart_unavailable",
			})
	}
}

func (s *Service) evaluateSmartData(ctx context.Context, disk diskServiceInterfaces.Disk, st *diskSmartState, warmup bool) {
	st.unavailableCount = 0

	s.evaluateTemperature(ctx, disk, st, warmup)
	s.evaluateHealth(ctx, disk, st, warmup)
	s.evaluateWearout(ctx, disk, st, warmup)
	s.evaluateReallocated(ctx, disk, st, warmup)

	if disk.Type == "NVMe" {
		if nvmeData, ok := disk.SmartData.(diskServiceInterfaces.SMARTNvme); ok {
			s.evaluateNvme(ctx, disk, &nvmeData, st, warmup)
		}
	}
}

func (s *Service) evaluateTemperature(ctx context.Context, disk diskServiceInterfaces.Disk, st *diskSmartState, warmup bool) {
	temperature := s.getTemperature(disk.SmartData)
	if temperature <= 0 {
		return
	}

	cfg := s.loadDiskSmartConfig(disk.Device, notifier.DiskSmartTemperatureKindPrefix)
	warnC := cfg.WarningCelsius
	critC := cfg.CriticalCelsius
	if warnC <= 0 {
		warnC = defaultTemperatureWarningCelsius
	}
	if critC <= 0 {
		critC = defaultTemperatureCriticalCelsius
	}

	prevTemp := st.temperature
	st.temperature = temperature

	if temperature >= int(critC) {
		st.tempCriticalCount++
		st.tempWarningCount = 0
		st.tempNormalCount = 0

		if !warmup && st.tempCriticalCount == diskSmartConsecutiveTrigger {
			s.emitDiskSmartNotification(ctx, disk.Device, notifier.DiskSmartTemperatureKindPrefix,
				"critical temperature", "critical",
				fmt.Sprintf("Disk %s temperature critical: %d C", disk.Device, temperature),
				fmt.Sprintf("Temperature %d C exceeds critical threshold of %.0f C.", temperature, critC),
				map[string]string{
					"condition":   "temperature_critical",
					"temperature": fmt.Sprintf("%d", temperature),
					"threshold":   fmt.Sprintf("%.0f", critC),
				})
		}
		return
	}

	if temperature >= int(warnC) {
		st.tempWarningCount++
		st.tempCriticalCount = 0
		st.tempNormalCount = 0

		if !warmup && st.tempWarningCount == diskSmartConsecutiveTrigger {
			s.emitDiskSmartNotification(ctx, disk.Device, notifier.DiskSmartTemperatureKindPrefix,
				"high temperature", "warning",
				fmt.Sprintf("Disk %s temperature high: %d C", disk.Device, temperature),
				fmt.Sprintf("Temperature %d C exceeds warning threshold of %.0f C.", temperature, warnC),
				map[string]string{
					"condition":   "temperature_warning",
					"temperature": fmt.Sprintf("%d", temperature),
					"threshold":   fmt.Sprintf("%.0f", warnC),
				})
		}
		return
	}

	st.tempNormalCount++
	st.tempWarningCount = 0
	st.tempCriticalCount = 0

	if !warmup && st.tempNormalCount == diskSmartConsecutiveClear && (prevTemp >= int(warnC)) {
		s.emitDiskSmartNotification(ctx, disk.Device, notifier.DiskSmartTemperatureKindPrefix,
			"temperature normal", "info",
			fmt.Sprintf("Disk %s temperature normal: %d C", disk.Device, temperature),
			fmt.Sprintf("Temperature returned to %d C, below warning threshold of %.0f C.", temperature, warnC),
			map[string]string{
				"condition":   "temperature_normal",
				"temperature": fmt.Sprintf("%d", temperature),
			})
	}
}

func (s *Service) evaluateHealth(ctx context.Context, disk diskServiceInterfaces.Disk, st *diskSmartState, warmup bool) {
	passed := s.getSMARTPassed(disk.SmartData)
	prevPassed := st.passed
	st.passed = passed

	if !passed {
		st.healthFailCount++
		st.healthNormalCount = 0

		if !warmup && st.healthFailCount == diskSmartConsecutiveTrigger {
			s.emitDiskSmartNotification(ctx, disk.Device, notifier.DiskSmartHealthKindPrefix,
				"SMART health failed", "critical",
				fmt.Sprintf("Disk %s S.M.A.R.T health check FAILED", disk.Device),
				fmt.Sprintf("The overall S.M.A.R.T health assessment for disk %s indicates failure.", disk.Device),
				map[string]string{
					"condition": "health_failed",
				})
		}
		return
	}

	st.healthNormalCount++
	st.healthFailCount = 0

	if !warmup && !prevPassed && st.healthNormalCount == diskSmartConsecutiveClear {
		s.emitDiskSmartNotification(ctx, disk.Device, notifier.DiskSmartHealthKindPrefix,
			"SMART health recovered", "info",
			fmt.Sprintf("Disk %s S.M.A.R.T health check recovered", disk.Device),
			fmt.Sprintf("The S.M.A.R.T health assessment for disk %s now passes.", disk.Device),
			map[string]string{
				"condition": "health_recovered",
			})
	}
}

func (s *Service) evaluateWearout(ctx context.Context, disk diskServiceInterfaces.Disk, st *diskSmartState, warmup bool) {
	wearout, err := s.DiskService.GetWearOut(disk.SmartData)
	if err != nil {
		return
	}

	cfg := s.loadDiskSmartConfig(disk.Device, notifier.DiskSmartWearoutKindPrefix)
	warnPct := cfg.WarningPercent
	critPct := cfg.CriticalPercent
	if warnPct <= 0 {
		warnPct = defaultWearoutWarningPercent
	}
	if critPct <= 0 {
		critPct = defaultWearoutCriticalPercent
	}

	prevWearout := st.wearoutPct
	st.wearoutPct = wearout

	if wearout >= critPct {
		st.wearCriticalCount++
		st.wearWarningCount = 0
		st.wearNormalCount = 0

		if !warmup && st.wearCriticalCount == diskSmartConsecutiveTrigger {
			s.emitDiskSmartNotification(ctx, disk.Device, notifier.DiskSmartWearoutKindPrefix,
				"critical wear-out", "critical",
				fmt.Sprintf("Disk %s wear-out critical: %.1f%%", disk.Device, wearout),
				fmt.Sprintf("Wear-out of %.1f%% exceeds critical threshold of %.0f%%.", wearout, critPct),
				map[string]string{
					"condition": "wearout_critical",
					"wearout":   fmt.Sprintf("%.1f", wearout),
					"threshold": fmt.Sprintf("%.0f", critPct),
				})
		}
		return
	}

	if wearout >= warnPct {
		st.wearWarningCount++
		st.wearCriticalCount = 0
		st.wearNormalCount = 0

		if !warmup && st.wearWarningCount == diskSmartConsecutiveTrigger {
			s.emitDiskSmartNotification(ctx, disk.Device, notifier.DiskSmartWearoutKindPrefix,
				"high wear-out", "warning",
				fmt.Sprintf("Disk %s wear-out high: %.1f%%", disk.Device, wearout),
				fmt.Sprintf("Wear-out of %.1f%% exceeds warning threshold of %.0f%%.", wearout, warnPct),
				map[string]string{
					"condition": "wearout_warning",
					"wearout":   fmt.Sprintf("%.1f", wearout),
					"threshold": fmt.Sprintf("%.0f", warnPct),
				})
		}
		return
	}

	st.wearNormalCount++
	st.wearWarningCount = 0
	st.wearCriticalCount = 0

	if !warmup && st.wearNormalCount == diskSmartConsecutiveClear && prevWearout >= warnPct {
		s.emitDiskSmartNotification(ctx, disk.Device, notifier.DiskSmartWearoutKindPrefix,
			"wear-out normal", "info",
			fmt.Sprintf("Disk %s wear-out returned to normal: %.1f%%", disk.Device, wearout),
			fmt.Sprintf("Wear-out of %.1f%% is below warning threshold of %.0f%%.", wearout, warnPct),
			map[string]string{
				"condition": "wearout_normal",
				"wearout":   fmt.Sprintf("%.1f", wearout),
			})
	}
}

func (s *Service) evaluateReallocated(ctx context.Context, disk diskServiceInterfaces.Disk, st *diskSmartState, warmup bool) {
	realloc, pending, uncorrect := s.getReallocatedSectors(disk.SmartData)
	prevRealloc := st.reallocatedSectors
	st.reallocatedSectors = realloc
	st.pendingSectors = pending
	st.uncorrectableSectors = uncorrect

	if realloc > 0 || pending > 0 || uncorrect > 0 {
		st.reallocCount++
		st.reallocNormalCount = 0

		if !warmup && st.reallocCount == diskSmartConsecutiveTrigger && realloc != prevRealloc {
			parts := []string{}
			if realloc > 0 {
				parts = append(parts, fmt.Sprintf("reallocated=%d", realloc))
			}
			if pending > 0 {
				parts = append(parts, fmt.Sprintf("pending=%d", pending))
			}
			if uncorrect > 0 {
				parts = append(parts, fmt.Sprintf("uncorrectable=%d", uncorrect))
			}

			s.emitDiskSmartNotification(ctx, disk.Device, notifier.DiskSmartHealthKindPrefix,
				"sector issues", "warning",
				fmt.Sprintf("Disk %s has sector issues", disk.Device),
				fmt.Sprintf("Sector anomalies detected on disk %s: %s.", disk.Device, strings.Join(parts, ", ")),
				map[string]string{
					"condition":    "sector_issues",
					"reallocated":  fmt.Sprintf("%d", realloc),
					"pending":      fmt.Sprintf("%d", pending),
					"uncorrectable": fmt.Sprintf("%d", uncorrect),
				})
		}
		return
	}

	st.reallocNormalCount++
	st.reallocCount = 0

	if !warmup && st.reallocNormalCount == diskSmartConsecutiveClear && prevRealloc > 0 {
		s.emitDiskSmartNotification(ctx, disk.Device, notifier.DiskSmartHealthKindPrefix,
			"sector issues cleared", "info",
			fmt.Sprintf("Disk %s sector issues cleared", disk.Device),
			fmt.Sprintf("Previously reported sector anomalies on disk %s have cleared.", disk.Device),
			map[string]string{
				"condition": "sector_issues_cleared",
			})
	}
}

func (s *Service) evaluateNvme(ctx context.Context, disk diskServiceInterfaces.Disk, nvme *diskServiceInterfaces.SMARTNvme, st *diskSmartState, warmup bool) {
	hasCriticalWarning := nvme.CriticalWarning != "" && nvme.CriticalWarning != "0x00" && nvme.CriticalWarning != "0x0"
	spareLow := nvme.AvailableSpareThreshold > 0 && nvme.AvailableSpare < nvme.AvailableSpareThreshold
	hasMediaErrors := nvme.MediaErrors > 0

	prevSpare := st.nvmeAvailableSpare
	prevMediaErrors := st.nvmeMediaErrors
	prevCritWarn := st.nvmeCriticalWarning
	prevHadWarning := prevCritWarn ||
		(st.nvmeSpareThreshold > 0 && prevSpare < st.nvmeSpareThreshold) ||
		prevMediaErrors > 0

	st.nvmeAvailableSpare = nvme.AvailableSpare
	st.nvmeSpareThreshold = nvme.AvailableSpareThreshold
	st.nvmeMediaErrors = nvme.MediaErrors
	st.nvmeCriticalWarning = hasCriticalWarning

	warn := hasCriticalWarning || spareLow || hasMediaErrors
	if warn {
		st.nvmeWarnCount++
		st.nvmeNormalCount = 0

		if !warmup && st.nvmeWarnCount == diskSmartConsecutiveTrigger {
			parts := []string{}
			if hasCriticalWarning {
				parts = append(parts, fmt.Sprintf("critical_warning=%s", nvme.CriticalWarning))
			}
			if spareLow {
				parts = append(parts, fmt.Sprintf("available_spare=%d%%, threshold=%d%%", nvme.AvailableSpare, nvme.AvailableSpareThreshold))
			}
			if hasMediaErrors {
				parts = append(parts, fmt.Sprintf("media_errors=%d", nvme.MediaErrors))
			}

			s.emitDiskSmartNotification(ctx, disk.Device, notifier.DiskSmartNvmeKindPrefix,
				"NVMe warning", "warning",
				fmt.Sprintf("Disk %s NVMe S.M.A.R.T warning", disk.Device),
				fmt.Sprintf("NVMe S.M.A.R.T issues on disk %s: %s.", disk.Device, strings.Join(parts, "; ")),
				map[string]string{
					"condition":          "nvme_warning",
					"critical_warning":   nvme.CriticalWarning,
					"available_spare":    fmt.Sprintf("%d", nvme.AvailableSpare),
					"spare_threshold":    fmt.Sprintf("%d", nvme.AvailableSpareThreshold),
					"media_errors":       fmt.Sprintf("%d", nvme.MediaErrors),
				})
		}
		return
	}

	st.nvmeNormalCount++
	st.nvmeWarnCount = 0

	if !warmup && st.nvmeNormalCount == diskSmartConsecutiveClear && prevHadWarning {
		recovered := false
		if prevCritWarn && !hasCriticalWarning {
			recovered = true
		}
		if prevSpare < nvme.AvailableSpareThreshold && nvme.AvailableSpare >= nvme.AvailableSpareThreshold {
			recovered = true
		}
		if prevMediaErrors > 0 && nvme.MediaErrors == 0 {
			recovered = true
		}
		if recovered {
			s.emitDiskSmartNotification(ctx, disk.Device, notifier.DiskSmartNvmeKindPrefix,
				"NVMe recovered", "info",
				fmt.Sprintf("Disk %s NVMe S.M.A.R.T recovered", disk.Device),
				fmt.Sprintf("Previously reported NVMe S.M.A.R.T issues on disk %s have cleared.", disk.Device),
				map[string]string{
					"condition": "nvme_recovered",
				})
		}
	}
}

func (s *Service) emitDiskSmartNotification(ctx context.Context, device, prefix, condition, severity, title, body string, metadata map[string]string) {
	kind := notifier.KindForDiskSmart(prefix, device)
	metadata["device"] = device
	metadata["condition"] = condition

	input := notifier.EventInput{
		Kind:        kind,
		Title:       title,
		Body:        body,
		Severity:    severity,
		Source:      "system.disk.smart",
		Fingerprint: fmt.Sprintf("%s|%s", strings.ToLower(device), condition),
		Metadata:    metadata,
	}

	_, err := notifier.Emit(ctx, input)
	if err != nil && !errors.Is(err, notifier.ErrEmitterNotConfigured) {
		logger.L.Error().
			Err(err).
			Str("kind", kind).
			Str("device", device).
			Str("condition", condition).
			Msg("failed_to_emit_disk_smart_notification")
	}
}

func (s *Service) getTemperature(smartData any) int {
	if smartData == nil {
		return 0
	}

	if nvme, ok := smartData.(diskServiceInterfaces.SMARTNvme); ok {
		return nvme.Temperature
	}

	if ata, ok := smartData.(diskServiceInterfaces.SmartData); ok {
		return ata.Temperature
	}

	return 0
}

func (s *Service) getSMARTPassed(smartData any) bool {
	if smartData == nil {
		return true
	}

	if nvme, ok := smartData.(diskServiceInterfaces.SMARTNvme); ok {
		return nvme.Passed
	}

	if ata, ok := smartData.(diskServiceInterfaces.SmartData); ok {
		return ata.Passed
	}

	return true
}

func (s *Service) getReallocatedSectors(smartData any) (reallocated, pending, uncorrectable int64) {
	ata, ok := smartData.(diskServiceInterfaces.SmartData)
	if !ok {
		return 0, 0, 0
	}

	for _, attr := range ata.Attributes {
		switch attr.ID {
		case 5:
			reallocated = attr.RawValue
		case 197:
			pending = attr.RawValue
		case 198:
			uncorrectable = attr.RawValue
		}
	}

	return
}

func (s *Service) loadDiskSmartConfig(device, prefix string) diskSmartConfig {
	if s == nil || s.DB == nil {
		return diskSmartConfig{}
	}

	kind := notifier.KindForDiskSmart(prefix, device)
	var configJSON string
	if err := s.DB.Raw("SELECT config FROM notification_kind_rules WHERE kind = ? LIMIT 1", kind).Scan(&configJSON).Error; err != nil {
		logger.LogWithDeduplication(zerolog.DebugLevel,
			fmt.Sprintf("disk_smart_config_load_failed: %v", err))
		return diskSmartConfig{}
	}

	if configJSON == "" {
		return diskSmartConfig{}
	}

	var cfg diskSmartConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		logger.LogWithDeduplication(zerolog.DebugLevel,
			fmt.Sprintf("disk_smart_config_parse_failed: %v", err))
		return diskSmartConfig{}
	}

	return cfg
}
