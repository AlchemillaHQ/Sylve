// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	"github.com/alchemillahq/sylve/pkg/disk/smart"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const smartSelfTestRunRetention = 64

const (
	smartSelfTestRunSourceManual    = "manual"
	smartSelfTestRunSourceScheduled = "scheduled"
	smartSelfTestRunRunning         = "running"
	smartSelfTestRunPassed          = "passed"
	smartSelfTestRunFailed          = "failed"
	smartSelfTestRunAborted         = "aborted"
	smartSelfTestRunUnknown         = "unknown"
)

func selfTestRunDiskKey(disk diskServiceInterfaces.DiskInfo) string {
	if key, err := selfTestScheduleDiskKey(disk); err == nil {
		return key
	}
	return strings.ToLower(strings.TrimSpace(disk.Name))
}

func manualSelfTestRunKey(diskKey string, startedAt time.Time) string {
	return fmt.Sprintf("manual|%s|%d", diskKey, startedAt.UnixNano())
}

func scheduledSelfTestRunKey(schedule models.DiskSmartSelfTestSchedule) string {
	anchor := schedule.OccurrenceKey
	if anchor == "" && schedule.LastRunAt != nil {
		anchor = schedule.LastRunAt.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("scheduled|%d|%s", schedule.ID, anchor)
}

func mappedSelfTestResultFingerprint(entry diskServiceInterfaces.DiskSelfTestResult) string {
	lba := entry.LBA
	if !entry.LBAValid {
		lba = 0
	}
	nsid := entry.NSID
	if !entry.NSIDValid {
		nsid = 0
	}
	return strings.Join([]string{
		entry.Type,
		entry.Status,
		strconv.Itoa(entry.RemainingPct),
		strconv.FormatUint(entry.LifetimeHours, 10),
		strconv.FormatUint(lba, 10),
		strconv.FormatBool(entry.LBAValid),
		strconv.FormatUint(uint64(nsid), 10),
		strconv.FormatBool(entry.NSIDValid),
	}, "|")
}

func mappedSelfTestResultTerminal(entry diskServiceInterfaces.DiskSelfTestResult) bool {
	status := strings.ToLower(strings.TrimSpace(entry.Status))
	return status != "" && status != "in_progress" && status != "unused"
}

func mappedSelfTestResultLogFingerprint(results []diskServiceInterfaces.DiskSelfTestResult, testType string) string {
	fingerprints := make([]string, 0, len(results))
	for _, result := range results {
		if !mappedSelfTestResultTerminal(result) || !selfTestTypeMatches(testType, result.Type) {
			continue
		}
		fingerprints = append(fingerprints, mappedSelfTestResultFingerprint(result))
	}
	return strings.Join(fingerprints, "\n")
}

func selfTestFingerprintCounts(value string) map[string]int {
	counts := make(map[string]int)
	for _, fingerprint := range strings.Split(value, "\n") {
		if fingerprint != "" {
			counts[fingerprint]++
		}
	}
	return counts
}

func selfTestFingerprintHasAdditionalOccurrence(current, baseline string) bool {
	counts := selfTestFingerprintCounts(baseline)
	for _, fingerprint := range strings.Split(current, "\n") {
		if fingerprint == "" {
			continue
		}
		if counts[fingerprint] > 0 {
			counts[fingerprint]--
			continue
		}
		return true
	}
	return false
}

func newMappedSelfTestResult(results []diskServiceInterfaces.DiskSelfTestResult, testType, baseline string) *diskServiceInterfaces.DiskSelfTestResult {
	counts := selfTestFingerprintCounts(baseline)
	for i := range results {
		if !mappedSelfTestResultTerminal(results[i]) || !selfTestTypeMatches(testType, results[i].Type) {
			continue
		}
		fingerprint := mappedSelfTestResultFingerprint(results[i])
		if counts[fingerprint] > 0 {
			counts[fingerprint]--
			continue
		}
		return &results[i]
	}
	return nil
}

func mappedSelfTestRunStatus(entry diskServiceInterfaces.DiskSelfTestResult) string {
	switch strings.ToLower(strings.TrimSpace(entry.Outcome)) {
	case "passed":
		return smartSelfTestRunPassed
	case "failed":
		return smartSelfTestRunFailed
	case "aborted":
		return smartSelfTestRunAborted
	}
	status := strings.ToLower(strings.TrimSpace(entry.Status))
	switch {
	case status == "completed":
		return smartSelfTestRunPassed
	case strings.HasPrefix(status, "failed"), status == "fatal", status == "unknown_error", status == "completed_segment_failed":
		return smartSelfTestRunFailed
	case strings.HasPrefix(status, "aborted"), status == "interrupted", status == "cancelled":
		return smartSelfTestRunAborted
	default:
		return smartSelfTestRunUnknown
	}
}

func encodeSelfTestRunResult(entry diskServiceInterfaces.DiskSelfTestResult) (string, error) {
	entry.StartedAt = nil
	data, err := json.Marshal(entry)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func selfTestRunOutcome(status string) string {
	switch status {
	case smartSelfTestRunPassed:
		return "passed"
	case smartSelfTestRunFailed:
		return "failed"
	case smartSelfTestRunAborted:
		return "aborted"
	case smartSelfTestRunRunning:
		return "in_progress"
	default:
		return "unknown"
	}
}

func selfTestRunResultStatus(status string) string {
	switch status {
	case smartSelfTestRunPassed:
		return "completed"
	case smartSelfTestRunFailed:
		return "failed"
	case smartSelfTestRunAborted:
		return "aborted_by_host"
	case smartSelfTestRunRunning:
		return "in_progress"
	default:
		return "unknown"
	}
}

func decodeSelfTestRunFingerprint(value string, run models.DiskSmartSelfTestRun, protocol string) (diskServiceInterfaces.DiskSelfTestResult, bool) {
	parts := strings.Split(value, "|")
	if len(parts) != 8 {
		return diskServiceInterfaces.DiskSelfTestResult{}, false
	}
	remaining, err := strconv.Atoi(parts[2])
	if err != nil {
		return diskServiceInterfaces.DiskSelfTestResult{}, false
	}
	hours, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil {
		return diskServiceInterfaces.DiskSelfTestResult{}, false
	}
	lba, err := strconv.ParseUint(parts[4], 10, 64)
	if err != nil {
		return diskServiceInterfaces.DiskSelfTestResult{}, false
	}
	lbaValid, err := strconv.ParseBool(parts[5])
	if err != nil {
		return diskServiceInterfaces.DiskSelfTestResult{}, false
	}
	nsidValue, err := strconv.ParseUint(parts[6], 10, 32)
	if err != nil {
		return diskServiceInterfaces.DiskSelfTestResult{}, false
	}
	nsidValid, err := strconv.ParseBool(parts[7])
	if err != nil {
		return diskServiceInterfaces.DiskSelfTestResult{}, false
	}
	return diskServiceInterfaces.DiskSelfTestResult{
		Protocol:           protocol,
		Type:               parts[0],
		Status:             parts[1],
		Outcome:            selfTestRunOutcome(run.Status),
		RemainingPct:       remaining,
		LifetimeHours:      hours,
		LifetimeHoursExact: parts[3],
		LBA:                lba,
		LBAExact:           parts[4],
		LBAValid:           lbaValid,
		NSID:               uint32(nsidValue),
		NSIDValid:          nsidValid,
	}, true
}

func selfTestRunResult(run models.DiskSmartSelfTestRun, protocol string) (diskServiceInterfaces.DiskSelfTestResult, bool, bool) {
	var result diskServiceInterfaces.DiskSelfTestResult
	if run.ResultData != "" && json.Unmarshal([]byte(run.ResultData), &result) == nil {
		if result.Protocol == "" {
			result.Protocol = protocol
		}
		if result.Type == "" {
			result.Type = run.TestType
		}
		if result.Status == "" {
			result.Status = selfTestRunResultStatus(run.Status)
		}
		if result.Outcome == "" {
			result.Outcome = selfTestRunOutcome(run.Status)
		}
		return result, true, true
	}
	if run.ResultFingerprint != "" {
		if result, ok := decodeSelfTestRunFingerprint(run.ResultFingerprint, run, protocol); ok {
			return result, true, false
		}
	}
	if run.Status == "" {
		return diskServiceInterfaces.DiskSelfTestResult{}, false, false
	}
	return diskServiceInterfaces.DiskSelfTestResult{
		Protocol:     protocol,
		Type:         run.TestType,
		Status:       selfTestRunResultStatus(run.Status),
		Outcome:      selfTestRunOutcome(run.Status),
		RemainingPct: -1,
	}, true, false
}

func selfTestResultSlotMatches(left, right diskServiceInterfaces.DiskSelfTestResult) bool {
	if !selfTestTypeMatches(left.Type, right.Type) || left.RemainingPct != right.RemainingPct || left.LifetimeHours != right.LifetimeHours || left.LBAValid != right.LBAValid || left.NSIDValid != right.NSIDValid {
		return false
	}
	if left.LBAValid && left.LBA != right.LBA || left.NSIDValid && left.NSID != right.NSID {
		return false
	}
	if left.Protocol != "" && right.Protocol != "" && !strings.EqualFold(left.Protocol, right.Protocol) {
		return false
	}
	if left.Mode != "" && right.Mode != "" && !strings.EqualFold(left.Mode, right.Mode) {
		return false
	}
	return true
}

func mergeSelfTestRunResults(info *diskServiceInterfaces.DiskSelfTestInfo, runs []models.DiskSmartSelfTestRun) {
	if info == nil {
		return
	}
	results := append([]diskServiceInterfaces.DiskSelfTestResult(nil), info.Status.Results...)
	matched := make([]bool, len(results))
	missing := make([]diskServiceInterfaces.DiskSelfTestResult, 0, len(runs))
	for i := range runs {
		run := runs[i]
		startedAt := run.StartedAt.UTC()
		if run.Status == smartSelfTestRunRunning {
			found := false
			for j := range results {
				if matched[j] || !selfTestTypeMatches(run.TestType, results[j].Type) || strings.ToLower(strings.TrimSpace(results[j].Outcome)) != "in_progress" && strings.ToLower(strings.TrimSpace(results[j].Status)) != "in_progress" {
					continue
				}
				results[j].StartedAt = &startedAt
				matched[j] = true
				found = true
				break
			}
			if found || !info.Status.Running || info.Status.Type != "" && !selfTestTypeMatches(run.TestType, info.Status.Type) {
				continue
			}
		}
		result, ok, canonical := selfTestRunResult(run, info.Status.Protocol)
		if !ok {
			continue
		}
		fingerprint := run.ResultFingerprint
		if fingerprint == "" {
			fingerprint = mappedSelfTestResultFingerprint(result)
		}
		found := false
		for j := range results {
			if matched[j] || mappedSelfTestResultFingerprint(results[j]) != fingerprint {
				continue
			}
			if canonical {
				result.StartedAt = &startedAt
				results[j] = result
			} else {
				results[j].StartedAt = &startedAt
			}
			matched[j] = true
			found = true
			break
		}
		if !found && canonical && run.Status == smartSelfTestRunAborted && !info.Status.Running && info.Status.State != smart.SelfTestStateAmbiguous {
			for j := range results {
				status := strings.ToLower(strings.TrimSpace(results[j].Status))
				outcome := strings.ToLower(strings.TrimSpace(results[j].Outcome))
				if matched[j] || status != "in_progress" && outcome != "in_progress" || !selfTestResultSlotMatches(result, results[j]) {
					continue
				}
				result.StartedAt = &startedAt
				results[j] = result
				matched[j] = true
				found = true
				break
			}
		}
		if found {
			continue
		}
		result.StartedAt = &startedAt
		missing = append(missing, result)
	}
	results = append(results, missing...)
	sort.SliceStable(results, func(i, j int) bool {
		left := results[i].StartedAt
		right := results[j].StartedAt
		if left == nil {
			return false
		}
		if right == nil {
			return true
		}
		return left.After(*right)
	})
	info.Status.Results = results
}

func (s *Service) pruneSelfTestRuns(tx *gorm.DB, diskKey string) error {
	var ids []uint
	if err := tx.Model(&models.DiskSmartSelfTestRun{}).
		Where("disk_key = ?", diskKey).
		Order("started_at DESC, id DESC").
		Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) <= smartSelfTestRunRetention {
		return nil
	}
	return tx.Where("id IN ?", ids[smartSelfTestRunRetention:]).Delete(&models.DiskSmartSelfTestRun{}).Error
}

func (s *Service) recordSelfTestRun(ctx context.Context, disk diskServiceInterfaces.DiskInfo, run models.DiskSmartSelfTestRun) error {
	if s == nil || s.DB == nil {
		return nil
	}
	run.DiskKey = selfTestRunDiskKey(disk)
	run.Device = disk.Name
	run.Model = disk.Description
	run.Serial = disk.Serial
	run.StartedAt = run.StartedAt.UTC()
	run.Status = smartSelfTestRunRunning
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var stored models.DiskSmartSelfTestRun
		if err := tx.Where("run_key = ?", run.RunKey).Attrs(run).FirstOrCreate(&stored).Error; err != nil {
			return err
		}
		return s.pruneSelfTestRuns(tx, run.DiskKey)
	})
	if err == nil {
		s.selfTestTrackingActive.Store(true)
	}
	return err
}

func (s *Service) supersedeUnfinishedSelfTestRuns(ctx context.Context, diskKey, runKey string, completedAt time.Time) error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.WithContext(ctx).Model(&models.DiskSmartSelfTestRun{}).
		Where("disk_key = ? AND run_key <> ? AND result_fingerprint = ? AND status IN ?", diskKey, runKey, "", []string{smartSelfTestRunRunning, smartSelfTestRunAborted}).
		Updates(map[string]any{"status": smartSelfTestRunUnknown, "completed_at": completedAt.UTC()}).Error
}

func (s *Service) recordManualSelfTestRun(ctx context.Context, disk diskServiceInterfaces.DiskInfo, testType string, info *diskServiceInterfaces.DiskSelfTestInfo, baselineValid bool, startedAt time.Time) error {
	baseline := ""
	if info != nil {
		baseline = mappedSelfTestResultLogFingerprint(info.Status.Results, testType)
	}
	diskKey := selfTestRunDiskKey(disk)
	return s.recordSelfTestRun(ctx, disk, models.DiskSmartSelfTestRun{
		RunKey:              manualSelfTestRunKey(diskKey, startedAt),
		TestType:            testType,
		Source:              smartSelfTestRunSourceManual,
		StartedAt:           startedAt,
		BaselineFingerprint: baseline,
		BaselineValid:       baselineValid,
	})
}

func (s *Service) recordScheduledSelfTestRun(ctx context.Context, disk diskServiceInterfaces.DiskInfo, schedule models.DiskSmartSelfTestSchedule, startedAt time.Time) error {
	scheduleID := schedule.ID
	return s.recordSelfTestRun(ctx, disk, models.DiskSmartSelfTestRun{
		RunKey:              scheduledSelfTestRunKey(schedule),
		TestType:            schedule.TestType,
		Source:              smartSelfTestRunSourceScheduled,
		ScheduleID:          &scheduleID,
		StartedAt:           startedAt,
		BaselineFingerprint: schedule.BaselineFingerprint,
		BaselineValid:       schedule.BaselineValid,
	})
}

func (s *Service) discardSelfTestRun(ctx context.Context, runKey string) error {
	if s == nil || s.DB == nil || runKey == "" {
		return nil
	}
	return s.DB.WithContext(ctx).
		Where("run_key = ? AND status = ? AND result_fingerprint = ?", runKey, smartSelfTestRunRunning, "").
		Delete(&models.DiskSmartSelfTestRun{}).Error
}

func (s *Service) newActiveSelfTestResult(ctx context.Context, disk diskServiceInterfaces.DiskInfo, info *diskServiceInterfaces.DiskSelfTestInfo) (*diskServiceInterfaces.DiskSelfTestResult, error) {
	if s == nil || s.DB == nil || info == nil {
		return nil, nil
	}
	var run models.DiskSmartSelfTestRun
	err := s.DB.WithContext(ctx).
		Where("disk_key = ? AND result_fingerprint = ? AND status = ?", selfTestRunDiskKey(disk), "", smartSelfTestRunRunning).
		Order("started_at DESC, id DESC").
		First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !run.BaselineValid || strings.EqualFold(info.Status.Protocol, "ATA") && !info.Status.ChecksumValid {
		return nil, nil
	}
	current := mappedSelfTestResultLogFingerprint(info.Status.Results, run.TestType)
	if current == run.BaselineFingerprint {
		return nil, nil
	}
	return newMappedSelfTestResult(info.Status.Results, run.TestType, run.BaselineFingerprint), nil
}

func (s *Service) finishScheduledSelfTestRun(ctx context.Context, schedule models.DiskSmartSelfTestSchedule, entry *diskServiceInterfaces.DiskSelfTestResult, status string, completedAt time.Time) error {
	if s == nil || s.DB == nil {
		return nil
	}
	updates := map[string]any{
		"status":       status,
		"completed_at": completedAt.UTC(),
	}
	if entry != nil {
		updates["result_fingerprint"] = mappedSelfTestResultFingerprint(*entry)
		data, err := encodeSelfTestRunResult(*entry)
		if err != nil {
			return err
		}
		updates["result_data"] = data
	}
	return s.DB.WithContext(ctx).Model(&models.DiskSmartSelfTestRun{}).
		Where("run_key = ? AND status = ? AND result_fingerprint = ?", scheduledSelfTestRunKey(schedule), smartSelfTestRunRunning, "").
		Updates(updates).Error
}

func manualSelfTestTerminalEvent(run models.DiskSmartSelfTestRun, status string) models.DiskSmartSelfTestEvent {
	condition := "self_test_completed_unknown"
	severity := "warning"
	title := fmt.Sprintf("Disk %s self-test result is unknown", run.Device)
	body := fmt.Sprintf("The manual %s self-test on disk %s finished without a recognized result.", run.TestType, run.Device)
	switch status {
	case smartSelfTestRunPassed:
		condition = "self_test_passed"
		severity = "info"
		title = fmt.Sprintf("Disk %s self-test passed", run.Device)
		body = fmt.Sprintf("The manual %s self-test on disk %s completed successfully.", run.TestType, run.Device)
	case smartSelfTestRunFailed:
		condition = "self_test_failed"
		severity = "critical"
		title = fmt.Sprintf("Disk %s self-test failed", run.Device)
		body = fmt.Sprintf("The manual %s self-test on disk %s reported a failure.", run.TestType, run.Device)
	case smartSelfTestRunAborted:
		condition = "self_test_aborted"
		severity = "warning"
		title = fmt.Sprintf("Disk %s self-test was aborted", run.Device)
		body = fmt.Sprintf("The manual %s self-test on disk %s was aborted.", run.TestType, run.Device)
	}
	return models.DiskSmartSelfTestEvent{
		EventKey:  run.RunKey + "|" + condition,
		RunKey:    run.RunKey,
		Source:    smartSelfTestRunSourceManual,
		DiskKey:   run.DiskKey,
		Device:    run.Device,
		TestType:  run.TestType,
		Condition: condition,
		Severity:  severity,
		Title:     title,
		Body:      body,
	}
}

func (s *Service) transitionSelfTestRun(ctx context.Context, run models.DiskSmartSelfTestRun, expectedStatus, status string, completedAt time.Time, result diskServiceInterfaces.DiskSelfTestResult) (bool, error) {
	if s == nil || s.DB == nil {
		return false, nil
	}
	result.StartedAt = nil
	if result.Type == "" {
		result.Type = run.TestType
	}
	if result.Status == "" {
		result.Status = selfTestRunResultStatus(status)
	}
	if result.Outcome == "" {
		result.Outcome = selfTestRunOutcome(status)
	}
	data, err := encodeSelfTestRunResult(result)
	if err != nil {
		return false, err
	}
	transitioned := false
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updated := tx.Model(&models.DiskSmartSelfTestRun{}).
			Where("id = ? AND status = ? AND result_fingerprint = ?", run.ID, expectedStatus, "").
			Updates(map[string]any{
				"status":             status,
				"completed_at":       completedAt.UTC(),
				"result_fingerprint": mappedSelfTestResultFingerprint(result),
				"result_data":        data,
			})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 0 {
			return nil
		}
		transitioned = true
		if run.Source != smartSelfTestRunSourceManual {
			return nil
		}
		event := manualSelfTestTerminalEvent(run, status)
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&event).Error
	})
	if err == nil && transitioned && run.Source == smartSelfTestRunSourceManual {
		s.signalSelfTestEventRelay()
	}
	return transitioned, err
}

func (s *Service) finishActiveSelfTestRun(ctx context.Context, disk diskServiceInterfaces.DiskInfo, status string, completedAt time.Time, entry *diskServiceInterfaces.DiskSelfTestResult) error {
	if s == nil || s.DB == nil {
		return nil
	}
	var run models.DiskSmartSelfTestRun
	err := s.DB.WithContext(ctx).
		Where("disk_key = ? AND result_fingerprint = ? AND status = ?", selfTestRunDiskKey(disk), "", smartSelfTestRunRunning).
		Order("started_at DESC, id DESC").
		First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	result := diskServiceInterfaces.DiskSelfTestResult{
		Type:         run.TestType,
		Status:       selfTestRunResultStatus(status),
		Outcome:      selfTestRunOutcome(status),
		RemainingPct: -1,
	}
	if entry != nil {
		result = *entry
		result.StartedAt = nil
		if result.Type == "" {
			result.Type = run.TestType
		}
		if result.Status == "" {
			result.Status = selfTestRunResultStatus(status)
		}
		if result.Outcome == "" {
			result.Outcome = selfTestRunOutcome(status)
		}
	}
	_, err = s.transitionSelfTestRun(ctx, run, smartSelfTestRunRunning, status, completedAt, result)
	return err
}

func (s *Service) attachSelfTestRunTimes(ctx context.Context, disk diskServiceInterfaces.DiskInfo, info *diskServiceInterfaces.DiskSelfTestInfo) error {
	if s == nil || s.DB == nil || info == nil {
		return nil
	}
	var runs []models.DiskSmartSelfTestRun
	if err := s.DB.WithContext(ctx).
		Where("disk_key = ?", selfTestRunDiskKey(disk)).
		Order("started_at DESC, id DESC").
		Limit(smartSelfTestRunRetention).
		Find(&runs).Error; err != nil {
		return err
	}
	if !info.Status.Running && info.Status.State != "ambiguous" && isAbortedSelfTestExecution(info.Status.ExecutionStatus) {
		for i := range info.Status.Results {
			result := info.Status.Results[i]
			if strings.ToLower(strings.TrimSpace(result.Status)) != "in_progress" && strings.ToLower(strings.TrimSpace(result.Outcome)) != "in_progress" {
				continue
			}
			reconcile := true
			if kind, ok := s.loadActiveSelfTestKind(disk.Name); ok && selfTestTypeMatches(string(kind), result.Type) {
				reconcile = false
			}
			if reconcile {
				reconcileAbortedSelfTestInfo(info, result.Type)
			}
			break
		}
	}
	now := time.Now().UTC()
	for i := range runs {
		run := &runs[i]
		if run.ResultFingerprint != "" || run.Status != smartSelfTestRunRunning && run.Status != smartSelfTestRunAborted {
			continue
		}
		current := mappedSelfTestResultLogFingerprint(info.Status.Results, run.TestType)
		if !run.BaselineValid {
			if info.Status.Running && (!strings.EqualFold(info.Status.Protocol, "ATA") || info.Status.ChecksumValid) {
				updated := s.DB.WithContext(ctx).Model(&models.DiskSmartSelfTestRun{}).
					Where("id = ? AND status = ? AND result_fingerprint = ?", run.ID, run.Status, "").
					Updates(map[string]any{
						"baseline_fingerprint": current,
						"baseline_valid":       true,
					})
				if updated.Error != nil {
					return updated.Error
				}
				if updated.RowsAffected == 0 {
					if err := s.DB.WithContext(ctx).First(run, run.ID).Error; err != nil {
						return err
					}
				} else {
					run.BaselineFingerprint = current
					run.BaselineValid = true
				}
			}
			continue
		}
		if current == run.BaselineFingerprint {
			continue
		}
		entry := newMappedSelfTestResult(info.Status.Results, run.TestType, run.BaselineFingerprint)
		if entry == nil {
			if info.Status.Running {
				updated := s.DB.WithContext(ctx).Model(&models.DiskSmartSelfTestRun{}).
					Where("id = ? AND status = ? AND result_fingerprint = ?", run.ID, run.Status, "").
					Update("baseline_fingerprint", current)
				if updated.Error != nil {
					return updated.Error
				}
				if updated.RowsAffected == 0 {
					if err := s.DB.WithContext(ctx).First(run, run.ID).Error; err != nil {
						return err
					}
				} else {
					run.BaselineFingerprint = current
				}
			}
			continue
		}
		completedAt := now
		if run.CompletedAt != nil {
			completedAt = run.CompletedAt.UTC()
		}
		fingerprint := mappedSelfTestResultFingerprint(*entry)
		status := mappedSelfTestRunStatus(*entry)
		transitioned, err := s.transitionSelfTestRun(ctx, *run, run.Status, status, completedAt, *entry)
		if err != nil {
			mergeSelfTestRunResults(info, runs)
			return err
		}
		if !transitioned {
			if err := s.DB.WithContext(ctx).First(run, run.ID).Error; err != nil {
				return err
			}
		} else {
			data, err := encodeSelfTestRunResult(*entry)
			if err != nil {
				return err
			}
			run.ResultFingerprint = fingerprint
			run.ResultData = data
			run.Status = status
			run.CompletedAt = &completedAt
		}
	}
	mergeSelfTestRunResults(info, runs)
	return nil
}

func manualSelfTestRunExpired(run models.DiskSmartSelfTestRun, now time.Time) bool {
	duration := 6 * time.Hour
	testType := strings.ToLower(strings.TrimSpace(run.TestType))
	if testType == "extended" || testType == "long" || testType == "extended_captive" {
		duration = 48 * time.Hour
	}
	return !now.Before(run.StartedAt.Add(duration))
}

func resolveSelfTestRunDisk(run models.DiskSmartSelfTestRun, disks []diskServiceInterfaces.DiskInfo) (diskServiceInterfaces.DiskInfo, bool) {
	var resolved diskServiceInterfaces.DiskInfo
	matches := 0
	for _, disk := range disks {
		if selfTestRunDiskKey(disk) == run.DiskKey {
			resolved = disk
			matches++
		}
	}
	if matches == 1 {
		return resolved, true
	}
	for _, disk := range disks {
		if disk.Name == run.Device {
			return disk, true
		}
	}
	return diskServiceInterfaces.DiskInfo{}, false
}

func (s *Service) finishManualSelfTestRunUnknown(ctx context.Context, run models.DiskSmartSelfTestRun, now time.Time) error {
	result := diskServiceInterfaces.DiskSelfTestResult{
		Type:         run.TestType,
		Status:       selfTestRunResultStatus(smartSelfTestRunUnknown),
		Outcome:      selfTestRunOutcome(smartSelfTestRunUnknown),
		RemainingPct: -1,
	}
	_, err := s.transitionSelfTestRun(ctx, run, smartSelfTestRunRunning, smartSelfTestRunUnknown, now, result)
	return err
}

func (s *Service) reconcileManualSelfTestRuns(ctx context.Context, now time.Time) error {
	if s == nil || s.DB == nil {
		return nil
	}
	var runs []models.DiskSmartSelfTestRun
	if err := s.DB.WithContext(ctx).
		Where("source = ? AND status = ? AND result_fingerprint = ?", smartSelfTestRunSourceManual, smartSelfTestRunRunning, "").
		Order("started_at ASC, id ASC").
		Find(&runs).Error; err != nil {
		return err
	}
	if len(runs) == 0 {
		return nil
	}
	disks, err := s.physicalDisks()
	if err != nil {
		return err
	}
	var firstErr error
	for i := range runs {
		run := runs[i]
		disk, found := resolveSelfTestRunDisk(run, disks)
		if !found {
			if manualSelfTestRunExpired(run, now) {
				if err := s.finishManualSelfTestRunUnknown(ctx, run, now); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			continue
		}
		lock := s.selfTestDeviceMutex(disk.Name)
		lock.Lock()
		info, readErr := s.readSelfTestInfo(disk.Name)
		if readErr == nil {
			s.storeSelfTestInfo(disk.Name, *info)
			if err := s.attachSelfTestRunTimes(ctx, disk, info); err != nil && firstErr == nil {
				firstErr = err
			}
		} else if firstErr == nil {
			firstErr = readErr
		}
		lock.Unlock()
		if manualSelfTestRunExpired(run, now) {
			if err := s.finishManualSelfTestRunUnknown(ctx, run, now); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (s *Service) hasRunningSelfTestRuns(ctx context.Context) (bool, error) {
	if s == nil || s.DB == nil {
		return false, nil
	}
	var runs []models.DiskSmartSelfTestRun
	result := s.DB.WithContext(ctx).Select("id").
		Where("status = ? AND result_fingerprint = ?", smartSelfTestRunRunning, "").
		Limit(1).
		Find(&runs)
	return len(runs) != 0, result.Error
}
