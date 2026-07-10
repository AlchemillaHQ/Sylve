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
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	"github.com/alchemillahq/sylve/internal/logger"
	notifier "github.com/alchemillahq/sylve/internal/notifications"

	"github.com/rs/zerolog"
)

const (
	diskSmartMonitorInterval        = 30 * time.Minute
	diskSmartConsecutiveTrigger     = 2
	diskSmartConsecutiveClear       = 3
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
	passed                      bool
	healthInitialized           bool
	healthAlerted               bool
	temperature                 int
	wearoutPct                  float64
	reallocatedSectors          int64
	pendingSectors              int64
	uncorrectableSectors        int64
	sectorAlerted               bool
	temperatureAlert            string
	wearoutAlert                string
	sectorNotifiedReallocated   int64
	sectorNotifiedPending       int64
	sectorNotifiedUncorrectable int64

	nvmeAvailableSpare  int
	nvmeSpareThreshold  int
	nvmeMediaErrors     string
	nvmeCriticalWarning bool
	nvmeAlerted         bool

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

	unavailableCount   int
	unavailableAlerted bool
}

type diskSmartMonitorSource interface {
	GetDiskDevicesForSMARTMonitor(context.Context) ([]diskServiceInterfaces.Disk, error)
}

func diskSmartTargetKey(disk diskServiceInterfaces.Disk) string {
	if disk.IdentityStable && strings.TrimSpace(disk.UUID) != "" {
		return strings.TrimSpace(strings.ToLower(disk.UUID))
	}
	return strings.TrimSpace(strings.ToLower(disk.Device))
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

		disks, err := s.diskSmartMonitorDevices(ctx)
		if err != nil {
			logger.L.Warn().Err(err).Msg("disk_smart_monitor_failed_to_get_disks")
			tickAndSleep()
			continue
		}
		s.refreshDiskSmartConfigs(disks)

		seenTargets := make(map[string]struct{}, len(disks))
		for _, disk := range disks {
			seenTargets[diskSmartTargetKey(disk)] = struct{}{}
			s.processDiskSmartSample(ctx, &mu, stateByDevice, disk, warmup)
		}
		mu.Lock()
		for target := range stateByDevice {
			if _, ok := seenTargets[target]; !ok {
				delete(stateByDevice, target)
			}
		}
		mu.Unlock()

		if warmup {
			warmup = false
			logger.L.Debug().Msg("disk_smart_monitor_warmup_complete")
		}

		tickAndSleep()
	}
}

func (s *Service) diskSmartMonitorDevices(ctx context.Context) ([]diskServiceInterfaces.Disk, error) {
	if source, ok := s.DiskService.(diskSmartMonitorSource); ok {
		return source.GetDiskDevicesForSMARTMonitor(ctx)
	}
	return s.DiskService.GetDiskDevices(ctx)
}

func (s *Service) processDiskSmartSample(ctx context.Context, mu *sync.Mutex, stateByDevice map[string]*diskSmartState, disk diskServiceInterfaces.Disk, warmup bool) {
	if disk.SmartReadPowerSkipped {
		return
	}
	target := diskSmartTargetKey(disk)
	if disk.SmartData == nil {
		s.handleMissingSmart(ctx, mu, stateByDevice, target, disk.Device)
		return
	}

	mu.Lock()
	st, exists := stateByDevice[target]
	if !exists {
		st = &diskSmartState{}
		stateByDevice[target] = st
	}
	mu.Unlock()

	s.evaluateSmartData(ctx, disk, st, warmup)
}

func (s *Service) handleMissingSmart(ctx context.Context, mu *sync.Mutex, stateByDevice map[string]*diskSmartState, target, device string) {
	mu.Lock()
	st, exists := stateByDevice[target]
	if !exists {
		mu.Unlock()
		return
	}

	st.unavailableCount++
	count := st.unavailableCount
	mu.Unlock()

	if count >= diskSmartConsecutiveUnavailable && !st.unavailableAlerted {
		if s.emitDiskSmartNotification(ctx, target, device, notifier.DiskSmartHealthKindPrefix,
			"smart_unavailable", "warning",
			fmt.Sprintf("SMART data unavailable for disk %s", device),
			fmt.Sprintf("The disk %s previously returned valid S.M.A.R.T data but is now unavailable.", device),
			map[string]string{
				"condition": "smart_unavailable",
			}) {
			mu.Lock()
			st.unavailableAlerted = true
			mu.Unlock()
		}
	}
}

func (s *Service) evaluateSmartData(ctx context.Context, disk diskServiceInterfaces.Disk, st *diskSmartState, warmup bool) {
	if st.unavailableAlerted {
		target := diskSmartTargetKey(disk)
		if s.emitDiskSmartNotification(ctx, target, disk.Device, notifier.DiskSmartHealthKindPrefix,
			"smart_available", "info",
			fmt.Sprintf("SMART data available for disk %s", disk.Device),
			fmt.Sprintf("S.M.A.R.T data for disk %s is available again.", disk.Device),
			map[string]string{"condition": "smart_available"}) {
			st.unavailableAlerted = false
		}
	}
	st.unavailableCount = 0

	s.evaluateHealth(ctx, disk, st, warmup)
	attributesValid := true
	if data, ok := disk.SmartData.(diskServiceInterfaces.SmartData); ok && strings.EqualFold(data.Device.Protocol, "ATA") && !data.ChecksumValid {
		attributesValid = false
	}
	if attributesValid {
		s.evaluateTemperature(ctx, disk, st, warmup)
		s.evaluateReallocated(ctx, disk, st, warmup)

		if disk.Type == "NVMe" || disk.Type == "SSD" {
			s.evaluateWearout(ctx, disk, st, warmup)
		}

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

	target := diskSmartTargetKey(disk)
	cfg := s.loadDiskSmartConfig(target, notifier.DiskSmartTemperatureKindPrefix)
	warnC := cfg.WarningCelsius
	critC := cfg.CriticalCelsius

	st.temperature = temperature
	if warmup {
		st.tempCriticalCount = 0
		st.tempWarningCount = 0
		st.tempNormalCount = 0
		return
	}

	if float64(temperature) >= critC {
		st.tempCriticalCount++
		st.tempWarningCount = 0
		st.tempNormalCount = 0

		if st.tempCriticalCount >= diskSmartConsecutiveTrigger && st.temperatureAlert != "critical" {
			if s.emitDiskSmartNotification(ctx, target, disk.Device, notifier.DiskSmartTemperatureKindPrefix,
				"temperature_critical", "critical",
				fmt.Sprintf("Disk %s temperature critical: %d C", disk.Device, temperature),
				fmt.Sprintf("Temperature %d C exceeds critical threshold of %g C.", temperature, critC),
				map[string]string{
					"condition":   "temperature_critical",
					"temperature": fmt.Sprintf("%d", temperature),
					"threshold":   fmt.Sprintf("%g", critC),
				}) {
				st.temperatureAlert = "critical"
			}
		}
		return
	}

	if float64(temperature) >= warnC {
		st.tempWarningCount++
		st.tempCriticalCount = 0
		st.tempNormalCount = 0

		if st.tempWarningCount >= diskSmartConsecutiveTrigger && st.temperatureAlert != "warning" {
			if s.emitDiskSmartNotification(ctx, target, disk.Device, notifier.DiskSmartTemperatureKindPrefix,
				"temperature_warning", "warning",
				fmt.Sprintf("Disk %s temperature high: %d C", disk.Device, temperature),
				fmt.Sprintf("Temperature %d C exceeds warning threshold of %g C.", temperature, warnC),
				map[string]string{
					"condition":   "temperature_warning",
					"temperature": fmt.Sprintf("%d", temperature),
					"threshold":   fmt.Sprintf("%g", warnC),
				}) {
				st.temperatureAlert = "warning"
			}
		}
		return
	}

	st.tempNormalCount++
	st.tempWarningCount = 0
	st.tempCriticalCount = 0

	if st.tempNormalCount >= diskSmartConsecutiveClear && st.temperatureAlert != "" {
		if s.emitDiskSmartNotification(ctx, target, disk.Device, notifier.DiskSmartTemperatureKindPrefix,
			"temperature_normal", "info",
			fmt.Sprintf("Disk %s temperature normal: %d C", disk.Device, temperature),
			fmt.Sprintf("Temperature returned to %d C, below warning threshold of %g C.", temperature, warnC),
			map[string]string{
				"condition":   "temperature_normal",
				"temperature": fmt.Sprintf("%d", temperature),
			}) {
			st.temperatureAlert = ""
		}
	}
}

func (s *Service) evaluateHealth(ctx context.Context, disk diskServiceInterfaces.Disk, st *diskSmartState, warmup bool) {
	target := diskSmartTargetKey(disk)
	known, passed := s.getSMARTHealth(disk.SmartData)
	if !known {
		st.healthFailCount = 0
		st.healthNormalCount = 0
		return
	}
	if !st.healthInitialized {
		st.healthInitialized = true
	}
	st.passed = passed
	if warmup {
		st.healthFailCount = 0
		st.healthNormalCount = 0
		return
	}

	if !passed {
		st.healthFailCount++
		st.healthNormalCount = 0

		if st.healthFailCount >= diskSmartConsecutiveTrigger && !st.healthAlerted {
			if s.emitDiskSmartNotification(ctx, target, disk.Device, notifier.DiskSmartHealthKindPrefix,
				"health_failed", "critical",
				fmt.Sprintf("Disk %s S.M.A.R.T health check FAILED", disk.Device),
				fmt.Sprintf("The overall S.M.A.R.T health assessment for disk %s indicates failure.", disk.Device),
				map[string]string{
					"condition": "health_failed",
				}) {
				st.healthAlerted = true
			}
		}
		return
	}

	st.healthNormalCount++
	st.healthFailCount = 0

	if st.healthNormalCount >= diskSmartConsecutiveClear && st.healthAlerted {
		if s.emitDiskSmartNotification(ctx, target, disk.Device, notifier.DiskSmartHealthKindPrefix,
			"health_recovered", "info",
			fmt.Sprintf("Disk %s S.M.A.R.T health check recovered", disk.Device),
			fmt.Sprintf("The S.M.A.R.T health assessment for disk %s now passes.", disk.Device),
			map[string]string{
				"condition": "health_recovered",
			}) {
			st.healthAlerted = false
		}
	}
}

func (s *Service) evaluateWearout(ctx context.Context, disk diskServiceInterfaces.Disk, st *diskSmartState, warmup bool) {
	wearout, err := s.DiskService.GetWearOut(disk.SmartData)
	if err != nil {
		return
	}

	target := diskSmartTargetKey(disk)
	cfg := s.loadDiskSmartConfig(target, notifier.DiskSmartWearoutKindPrefix)
	warnPct := cfg.WarningPercent
	critPct := cfg.CriticalPercent

	st.wearoutPct = wearout
	if warmup {
		st.wearCriticalCount = 0
		st.wearWarningCount = 0
		st.wearNormalCount = 0
		return
	}

	if wearout >= critPct {
		st.wearCriticalCount++
		st.wearWarningCount = 0
		st.wearNormalCount = 0

		if st.wearCriticalCount >= diskSmartConsecutiveTrigger && st.wearoutAlert != "critical" {
			if s.emitDiskSmartNotification(ctx, target, disk.Device, notifier.DiskSmartWearoutKindPrefix,
				"wearout_critical", "critical",
				fmt.Sprintf("Disk %s wear-out critical: %.1f%%", disk.Device, wearout),
				fmt.Sprintf("Wear-out of %.1f%% exceeds critical threshold of %.0f%%.", wearout, critPct),
				map[string]string{
					"condition": "wearout_critical",
					"wearout":   fmt.Sprintf("%.1f", wearout),
					"threshold": fmt.Sprintf("%.0f", critPct),
				}) {
				st.wearoutAlert = "critical"
			}
		}
		return
	}

	if wearout >= warnPct {
		st.wearWarningCount++
		st.wearCriticalCount = 0
		st.wearNormalCount = 0

		if st.wearWarningCount >= diskSmartConsecutiveTrigger && st.wearoutAlert != "warning" {
			if s.emitDiskSmartNotification(ctx, target, disk.Device, notifier.DiskSmartWearoutKindPrefix,
				"wearout_warning", "warning",
				fmt.Sprintf("Disk %s wear-out high: %.1f%%", disk.Device, wearout),
				fmt.Sprintf("Wear-out of %.1f%% exceeds warning threshold of %.0f%%.", wearout, warnPct),
				map[string]string{
					"condition": "wearout_warning",
					"wearout":   fmt.Sprintf("%.1f", wearout),
					"threshold": fmt.Sprintf("%.0f", warnPct),
				}) {
				st.wearoutAlert = "warning"
			}
		}
		return
	}

	st.wearNormalCount++
	st.wearWarningCount = 0
	st.wearCriticalCount = 0

	if st.wearNormalCount >= diskSmartConsecutiveClear && st.wearoutAlert != "" {
		if s.emitDiskSmartNotification(ctx, target, disk.Device, notifier.DiskSmartWearoutKindPrefix,
			"wearout_normal", "info",
			fmt.Sprintf("Disk %s wear-out returned to normal: %.1f%%", disk.Device, wearout),
			fmt.Sprintf("Wear-out of %.1f%% is below warning threshold of %.0f%%.", wearout, warnPct),
			map[string]string{
				"condition": "wearout_normal",
				"wearout":   fmt.Sprintf("%.1f", wearout),
			}) {
			st.wearoutAlert = ""
		}
	}
}

func (s *Service) evaluateReallocated(ctx context.Context, disk diskServiceInterfaces.Disk, st *diskSmartState, warmup bool) {
	target := diskSmartTargetKey(disk)
	realloc, pending, uncorrect := s.getReallocatedSectors(disk.SmartData)
	st.reallocatedSectors = realloc
	st.pendingSectors = pending
	st.uncorrectableSectors = uncorrect
	if warmup {
		st.reallocCount = 0
		st.reallocNormalCount = 0
		return
	}

	if realloc > 0 || pending > 0 || uncorrect > 0 {
		st.reallocCount++
		st.reallocNormalCount = 0

		changed := realloc != st.sectorNotifiedReallocated || pending != st.sectorNotifiedPending || uncorrect != st.sectorNotifiedUncorrectable
		if st.reallocCount >= diskSmartConsecutiveTrigger && (!st.sectorAlerted || changed) {
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

			if s.emitDiskSmartNotification(ctx, target, disk.Device, notifier.DiskSmartHealthKindPrefix,
				"sector_issues", "warning",
				fmt.Sprintf("Disk %s has sector issues", disk.Device),
				fmt.Sprintf("Sector anomalies detected on disk %s: %s.", disk.Device, strings.Join(parts, ", ")),
				map[string]string{
					"condition":     "sector_issues",
					"reallocated":   fmt.Sprintf("%d", realloc),
					"pending":       fmt.Sprintf("%d", pending),
					"uncorrectable": fmt.Sprintf("%d", uncorrect),
				}) {
				st.sectorAlerted = true
				st.sectorNotifiedReallocated = realloc
				st.sectorNotifiedPending = pending
				st.sectorNotifiedUncorrectable = uncorrect
			}
		}
		return
	}

	st.reallocNormalCount++
	st.reallocCount = 0

	if st.reallocNormalCount >= diskSmartConsecutiveClear && st.sectorAlerted {
		if s.emitDiskSmartNotification(ctx, target, disk.Device, notifier.DiskSmartHealthKindPrefix,
			"sector_issues_cleared", "info",
			fmt.Sprintf("Disk %s sector issues cleared", disk.Device),
			fmt.Sprintf("Previously reported sector anomalies on disk %s have cleared.", disk.Device),
			map[string]string{
				"condition": "sector_issues_cleared",
			}) {
			st.sectorAlerted = false
			st.sectorNotifiedReallocated = 0
			st.sectorNotifiedPending = 0
			st.sectorNotifiedUncorrectable = 0
		}
	}
}

func (s *Service) evaluateNvme(ctx context.Context, disk diskServiceInterfaces.Disk, nvme *diskServiceInterfaces.SMARTNvme, st *diskSmartState, warmup bool) {
	target := diskSmartTargetKey(disk)
	hasCriticalWarning := nvme.CriticalWarning != "" && nvme.CriticalWarning != "0x00" && nvme.CriticalWarning != "0x0"
	spareLow := nvme.AvailableSpareThreshold > 0 && nvme.AvailableSpare < nvme.AvailableSpareThreshold
	mediaErrors := strings.TrimSpace(nvme.MediaErrorsExact)
	if mediaErrors == "" {
		mediaErrors = strconv.Itoa(nvme.MediaErrors)
	}
	hasMediaErrors := strings.TrimLeft(mediaErrors, "0") != ""

	prevSpare := st.nvmeAvailableSpare
	prevMediaErrors := st.nvmeMediaErrors
	prevCritWarn := st.nvmeCriticalWarning
	st.nvmeAvailableSpare = nvme.AvailableSpare
	st.nvmeSpareThreshold = nvme.AvailableSpareThreshold
	st.nvmeMediaErrors = mediaErrors
	st.nvmeCriticalWarning = hasCriticalWarning
	if warmup {
		st.nvmeWarnCount = 0
		st.nvmeNormalCount = 0
		return
	}

	warn := hasCriticalWarning || spareLow || hasMediaErrors
	if warn {
		st.nvmeWarnCount++
		st.nvmeNormalCount = 0

		changed := prevCritWarn != hasCriticalWarning || prevSpare != nvme.AvailableSpare || prevMediaErrors != mediaErrors
		if st.nvmeWarnCount >= diskSmartConsecutiveTrigger && (!st.nvmeAlerted || changed) {
			parts := []string{}
			if hasCriticalWarning {
				parts = append(parts, fmt.Sprintf("critical_warning=%s", nvme.CriticalWarning))
			}
			if spareLow {
				parts = append(parts, fmt.Sprintf("available_spare=%d%%, threshold=%d%%", nvme.AvailableSpare, nvme.AvailableSpareThreshold))
			}
			if hasMediaErrors {
				parts = append(parts, "media_errors="+mediaErrors)
			}

			if s.emitDiskSmartNotification(ctx, target, disk.Device, notifier.DiskSmartNvmeKindPrefix,
				"nvme_warning", "warning",
				fmt.Sprintf("Disk %s NVMe S.M.A.R.T warning", disk.Device),
				fmt.Sprintf("NVMe S.M.A.R.T issues on disk %s: %s.", disk.Device, strings.Join(parts, "; ")),
				map[string]string{
					"condition":        "nvme_warning",
					"critical_warning": nvme.CriticalWarning,
					"available_spare":  fmt.Sprintf("%d", nvme.AvailableSpare),
					"spare_threshold":  fmt.Sprintf("%d", nvme.AvailableSpareThreshold),
					"media_errors":     mediaErrors,
				}) {
				st.nvmeAlerted = true
			}
		}
		return
	}

	st.nvmeNormalCount++
	st.nvmeWarnCount = 0

	if st.nvmeNormalCount >= diskSmartConsecutiveClear && st.nvmeAlerted {
		if s.emitDiskSmartNotification(ctx, target, disk.Device, notifier.DiskSmartNvmeKindPrefix,
			"nvme_recovered", "info",
			fmt.Sprintf("Disk %s NVMe S.M.A.R.T recovered", disk.Device),
			fmt.Sprintf("Previously reported NVMe S.M.A.R.T issues on disk %s have cleared.", disk.Device),
			map[string]string{
				"condition": "nvme_recovered",
			}) {
			st.nvmeAlerted = false
		}
	}
}

func (s *Service) emitDiskSmartNotification(ctx context.Context, target, device, prefix, condition, severity, title, body string, metadata map[string]string) bool {
	kind := notifier.KindForDiskSmart(prefix, target)
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata["device"] = device
	metadata["disk_key"] = target
	metadata["condition"] = condition

	input := notifier.EventInput{
		Kind:        kind,
		Title:       title,
		Body:        body,
		Severity:    severity,
		Source:      "system.disk.smart",
		Fingerprint: fmt.Sprintf("%s|%s", strings.ToLower(target), diskSmartFingerprintCategory(prefix, condition)),
		Metadata:    metadata,
	}

	_, err := notifier.Emit(ctx, input)
	if err == nil {
		return true
	}
	if !errors.Is(err, notifier.ErrEmitterNotConfigured) {
		logger.L.Error().Err(err).Str("kind", kind).Str("device", device).Str("condition", condition).Msg("failed_to_emit_disk_smart_notification")
	}
	return false
}

func diskSmartFingerprintCategory(prefix, condition string) string {
	switch prefix {
	case notifier.DiskSmartTemperatureKindPrefix:
		return "temperature"
	case notifier.DiskSmartWearoutKindPrefix:
		return "wearout"
	case notifier.DiskSmartNvmeKindPrefix:
		return "nvme"
	case notifier.DiskSmartSelfTestKindPrefix:
		return "selftest"
	default:
		if condition == "sector_issues" || condition == "sector_issues_cleared" {
			return "sectors"
		}
		if condition == "smart_unavailable" || condition == "smart_available" {
			return "availability"
		}
		return "health"
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
		protocol := strings.ToUpper(ata.Device.Protocol)
		if protocol == "SCSI" {
			for _, attr := range ata.Attributes {
				if attr.Page == 0x0D && attr.ID == 0 {
					return int(attr.RawValue)
				}
			}
			return ata.Temperature
		}
		return ata.Temperature
	}

	return 0
}

func (s *Service) getSMARTHealth(smartData any) (bool, bool) {
	if smartData == nil {
		return false, false
	}

	if nvme, ok := smartData.(diskServiceInterfaces.SMARTNvme); ok {
		if !nvme.HealthKnown {
			return false, false
		}
		return true, nvme.Passed
	}

	if ata, ok := smartData.(diskServiceInterfaces.SmartData); ok {
		if !ata.HealthKnown {
			return false, false
		}
		return true, ata.Passed
	}

	return false, false
}

func (s *Service) getSMARTPassed(smartData any) bool {
	known, passed := s.getSMARTHealth(smartData)
	return !known || passed
}

func (s *Service) getReallocatedSectors(smartData any) (reallocated, pending, uncorrectable int64) {
	ata, ok := smartData.(diskServiceInterfaces.SmartData)
	if !ok {
		return 0, 0, 0
	}

	if strings.ToUpper(ata.Device.Protocol) == "SCSI" {
		for _, attr := range ata.Attributes {
			if attr.ID != 6 || attr.RawValue <= 0 {
				continue
			}
			switch attr.Page {
			case 0x02, 0x03, 0x05, 0x06:
				if attr.RawValue > math.MaxInt64-uncorrectable {
					uncorrectable = math.MaxInt64
				} else {
					uncorrectable += attr.RawValue
				}
			}
		}
		return 0, 0, uncorrectable
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
	cfg := defaultDiskSmartConfig(prefix)
	if s == nil {
		return cfg
	}
	kind := notifier.KindForDiskSmart(prefix, device)
	s.diskSmartConfigMu.RLock()
	cached, cachedOK := s.diskSmartConfigs[kind]
	snapshot := s.diskSmartConfigSnapshot
	s.diskSmartConfigMu.RUnlock()
	if cachedOK {
		return cached
	}
	if snapshot {
		return cfg
	}
	if s.DB == nil {
		return cfg
	}

	var configJSON string
	if err := s.DB.Raw("SELECT config FROM notification_kind_rules WHERE kind = ? LIMIT 1", kind).Scan(&configJSON).Error; err != nil {
		logger.LogWithDeduplication(zerolog.DebugLevel,
			fmt.Sprintf("disk_smart_config_load_failed: %v", err))
		return cfg
	}

	if configJSON == "" {
		return cfg
	}

	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		logger.LogWithDeduplication(zerolog.DebugLevel,
			fmt.Sprintf("disk_smart_config_parse_failed: %v", err))
		return cfg
	}

	return cfg
}

func defaultDiskSmartConfig(prefix string) diskSmartConfig {
	cfg := diskSmartConfig{}
	switch prefix {
	case notifier.DiskSmartTemperatureKindPrefix:
		cfg.WarningCelsius = defaultTemperatureWarningCelsius
		cfg.CriticalCelsius = defaultTemperatureCriticalCelsius
	case notifier.DiskSmartWearoutKindPrefix:
		cfg.WarningPercent = defaultWearoutWarningPercent
		cfg.CriticalPercent = defaultWearoutCriticalPercent
	}
	return cfg
}

func (s *Service) refreshDiskSmartConfigs(disks []diskServiceInterfaces.Disk) {
	if s == nil || s.DB == nil {
		return
	}
	kinds := make([]string, 0, len(disks)*2)
	configs := make(map[string]diskSmartConfig, len(disks)*2)
	for _, disk := range disks {
		for _, prefix := range []string{notifier.DiskSmartTemperatureKindPrefix, notifier.DiskSmartWearoutKindPrefix} {
			kind := notifier.KindForDiskSmart(prefix, diskSmartTargetKey(disk))
			kinds = append(kinds, kind)
			configs[kind] = defaultDiskSmartConfig(prefix)
		}
	}
	if len(kinds) > 0 {
		var rows []struct {
			Kind   string
			Config string
		}
		if err := s.DB.Model(&models.NotificationKindRule{}).Select("kind", "config").Where("kind IN ?", kinds).Find(&rows).Error; err != nil {
			logger.LogWithDeduplication(zerolog.DebugLevel, fmt.Sprintf("disk_smart_config_load_failed: %v", err))
			return
		}
		for _, row := range rows {
			cfg := configs[row.Kind]
			if row.Config != "" {
				if err := json.Unmarshal([]byte(row.Config), &cfg); err != nil {
					logger.LogWithDeduplication(zerolog.DebugLevel, fmt.Sprintf("disk_smart_config_parse_failed: %v", err))
					continue
				}
			}
			configs[row.Kind] = cfg
		}
	}
	s.diskSmartConfigMu.Lock()
	s.diskSmartConfigs = configs
	s.diskSmartConfigSnapshot = true
	s.diskSmartConfigMu.Unlock()
}
