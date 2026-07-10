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
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	"github.com/alchemillahq/sylve/internal/logger"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
	"github.com/alchemillahq/sylve/pkg/disk/smart"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const smartSelfTestScheduleInterval = time.Minute
const smartSelfTestTrackInterval = 15 * time.Second
const smartSelfTestScheduleCatchupWindow = 15 * time.Minute
const smartSelfTestEventDispatchInterval = 5 * time.Second
const smartSelfTestEventClaimLease = 10 * time.Minute
const smartSelfTestEventBatchSize = 10
const smartSelfTestSchedulerLeaseDuration = 10 * time.Minute
const smartSelfTestEventMaxAttempts = 48
const smartSelfTestEventMaxAge = 7 * 24 * time.Hour
const smartSelfTestDispatchLease = 2 * time.Minute

const smartSelfTestScheduleJobName = "disk-smart-scheduler-tick"
const smartSelfTestEventJobName = "disk-smart-self-test-event-deliver"

const (
	smartSelfTestScheduleIdle     = "idle"
	smartSelfTestScheduleQueued   = "queued"
	smartSelfTestScheduleStarting = "starting"
	smartSelfTestScheduleRunning  = "running"
	smartSelfTestSchedulePassed   = "passed"
	smartSelfTestScheduleFailed   = "failed"
	smartSelfTestScheduleAborted  = "aborted"
	smartSelfTestScheduleUnknown  = "unknown"
	smartSelfTestScheduleMissed   = "missed"
)

var ErrSelfTestScheduleNotFound = errors.New("self-test schedule not found")
var ErrSelfTestScheduleRunning = errors.New("self-test schedule is running")
var ErrSelfTestSchedulerBusy = errors.New("self-test scheduler is busy")
var ErrInvalidSelfTestSchedule = errors.New("invalid self-test schedule")
var errSelfTestEventClaimLost = errors.New("self-test event claim lost")

type smartSelfTestSchedulerJob struct {
	RequestedAt   time.Time `json:"requestedAt"`
	DispatchToken string    `json:"dispatchToken"`
}

type smartSelfTestEventJob struct {
	EventID    uint   `json:"eventId"`
	ClaimToken string `json:"claimToken"`
}

type SelfTestScheduleInput struct {
	Device   string `json:"device" binding:"required"`
	TestType string `json:"testType" binding:"required"`
	CronExpr string `json:"cronExpr" binding:"required"`
	Enabled  bool   `json:"enabled"`
}

type SelfTestScheduleView struct {
	ID               uint       `json:"id"`
	DiskKey          string     `json:"diskKey"`
	Device           string     `json:"device"`
	Model            string     `json:"model"`
	Serial           string     `json:"serial"`
	TestType         string     `json:"testType"`
	CronExpr         string     `json:"cronExpr"`
	Enabled          bool       `json:"enabled"`
	QueuedAt         *time.Time `json:"queuedAt"`
	LastRunAt        *time.Time `json:"lastRunAt"`
	NextRunAt        *time.Time `json:"nextRunAt"`
	LastStatus       string     `json:"lastStatus"`
	LastError        string     `json:"lastError"`
	ProgressPct      int        `json:"progressPct"`
	ProgressKnown    bool       `json:"progressKnown"`
	EstimatedMinutes int        `json:"estimatedMinutes"`
}

func selfTestScheduleView(schedule models.DiskSmartSelfTestSchedule) SelfTestScheduleView {
	return SelfTestScheduleView{
		ID:               schedule.ID,
		DiskKey:          schedule.DiskKey,
		Device:           schedule.Device,
		Model:            schedule.Model,
		Serial:           schedule.Serial,
		TestType:         schedule.TestType,
		CronExpr:         schedule.CronExpr,
		Enabled:          schedule.Enabled,
		QueuedAt:         schedule.QueuedAt,
		LastRunAt:        schedule.LastRunAt,
		NextRunAt:        schedule.NextRunAt,
		LastStatus:       schedule.LastStatus,
		LastError:        schedule.LastError,
		ProgressPct:      schedule.ProgressPct,
		ProgressKnown:    schedule.ProgressKnown,
		EstimatedMinutes: schedule.EstimatedMinutes,
	}
}

func selfTestScheduleDiskKey(disk diskServiceInterfaces.DiskInfo) (string, error) {
	if strings.TrimSpace(disk.LunID) == "" && strings.TrimSpace(disk.Serial) == "" {
		return "", fmt.Errorf("%w: stable disk identity unavailable", ErrInvalidSelfTestSchedule)
	}
	return utils.GenerateDeterministicUUID(fmt.Sprintf("%s-%s", disk.LunID, disk.Serial)), nil
}

func (s *Service) resolveScheduledPhysicalDisk(record *models.DiskSmartSelfTestSchedule) (diskServiceInterfaces.DiskInfo, error) {
	disks, err := s.physicalDisks()
	if err != nil {
		return diskServiceInterfaces.DiskInfo{}, err
	}
	var resolved diskServiceInterfaces.DiskInfo
	matches := 0
	for _, disk := range disks {
		key, keyErr := selfTestScheduleDiskKey(disk)
		if keyErr == nil && key == record.DiskKey {
			resolved = disk
			matches++
		}
	}
	if matches == 1 {
		return resolved, nil
	}
	if matches > 1 {
		return diskServiceInterfaces.DiskInfo{}, fmt.Errorf("%w: stable disk identity is not unique", ErrInvalidSelfTestSchedule)
	}
	return diskServiceInterfaces.DiskInfo{}, fmt.Errorf("physical disk not found: %s", record.Device)
}

func (s *Service) validateScheduledPhysicalDiskIdentity(disk diskServiceInterfaces.DiskInfo, diskKey string) error {
	record := models.DiskSmartSelfTestSchedule{DiskKey: diskKey, Device: disk.Name}
	resolved, err := s.resolveScheduledPhysicalDisk(&record)
	if err != nil {
		return err
	}
	if resolved.Name != disk.Name {
		return fmt.Errorf("%w: stable disk identity resolved to another device", ErrInvalidSelfTestSchedule)
	}
	return nil
}

func (s *Service) scheduledSelfTestBackend() selfTestBackend {
	if s != nil && s.selfTestDriver != nil {
		return s.selfTestDriver
	}
	return librarySelfTestBackend{}
}

func (s *Service) activeSelfTestScheduleForDisk(ctx context.Context, disk diskServiceInterfaces.DiskInfo) (*models.DiskSmartSelfTestSchedule, error) {
	if s == nil || s.DB == nil {
		return nil, nil
	}
	diskKey, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		return nil, nil
	}
	var record models.DiskSmartSelfTestSchedule
	err = s.DB.WithContext(ctx).
		Where("disk_key = ? AND last_status IN ?", diskKey, []string{smartSelfTestScheduleStarting, smartSelfTestScheduleRunning}).
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Service) reservedSelfTestScheduleForDisk(ctx context.Context, disk diskServiceInterfaces.DiskInfo) (*models.DiskSmartSelfTestSchedule, error) {
	if s == nil || s.DB == nil {
		return nil, nil
	}
	diskKey, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		return nil, nil
	}
	var record models.DiskSmartSelfTestSchedule
	err = s.DB.WithContext(ctx).
		Where("disk_key = ? AND last_status IN ?", diskKey, []string{smartSelfTestScheduleQueued, smartSelfTestScheduleStarting, smartSelfTestScheduleRunning}).
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Service) markSelfTestScheduleManuallyAborted(ctx context.Context, record *models.DiskSmartSelfTestSchedule) error {
	if record == nil || s == nil || s.DB == nil {
		return nil
	}
	record.LastStatus = smartSelfTestScheduleAborted
	record.LastError = ""
	record.QueuedAt = nil
	record.QueueUpdatedAt = nil
	record.ProgressPct = -1
	record.ProgressKnown = false
	record.RunningObserved = false
	record.TimeoutAbortAttempted = false
	event := newScheduledSelfTestEvent(*record, "self_test_aborted", "warning", fmt.Sprintf("Disk %s scheduled S.M.A.R.T self-test was aborted", record.Device), fmt.Sprintf("The scheduled %s self-test was aborted manually.", record.TestType))
	if err := s.saveScheduledSelfTestWithEvent(ctx, record, &event); err != nil {
		return err
	}
	return nil
}

func normalizeScheduledSelfTestType(testType string) (smart.SelfTestKind, string, error) {
	switch strings.ToLower(strings.TrimSpace(testType)) {
	case "short":
		return smart.SelfTestKindShort, "short", nil
	case "long", "extended":
		return smart.SelfTestKindExtended, "extended", nil
	default:
		return "", "", fmt.Errorf("%w: test type", ErrInvalidSelfTestSchedule)
	}
}

func parseSelfTestSchedule(expr, testType string) (cron.Schedule, error) {
	return parseSelfTestScheduleAt(expr, testType, time.Local)
}

func parseSelfTestScheduleAt(expr, testType string, location *time.Location) (cron.Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("%w: cron expression required", ErrInvalidSelfTestSchedule)
	}
	upperExpr := strings.ToUpper(expr)
	if strings.HasPrefix(upperExpr, "TZ=") || strings.HasPrefix(upperExpr, "CRON_TZ=") {
		return nil, fmt.Errorf("%w: timezone prefixes are not supported", ErrInvalidSelfTestSchedule)
	}
	schedule, err := cron.ParseStandard(expr)
	if err != nil {
		return nil, fmt.Errorf("%w: cron expression", ErrInvalidSelfTestSchedule)
	}
	if _, _, err := normalizeScheduledSelfTestType(testType); err != nil {
		return nil, err
	}
	if nextSelfTestScheduleAt(schedule, time.Now(), location).IsZero() {
		return nil, fmt.Errorf("%w: cron expression has no next run", ErrInvalidSelfTestSchedule)
	}
	return schedule, nil
}

func nextSelfTestSchedule(schedule cron.Schedule, now time.Time) time.Time {
	return nextSelfTestScheduleAt(schedule, now, time.Local)
}

func nextSelfTestScheduleAt(schedule cron.Schedule, now time.Time, location *time.Location) time.Time {
	if schedule == nil {
		return time.Time{}
	}
	if location == nil {
		location = time.Local
	}
	return schedule.Next(now.In(location)).UTC()
}

func selfTestResultFingerprint(entry smart.SelfTestEntry) string {
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

func latestCompletedSelfTestResult(status *smart.SelfTestStatus) (*smart.SelfTestEntry, string) {
	entry, _, fingerprint := latestScheduledSelfTestResult(status, "")
	return entry, fingerprint
}

func latestScheduledSelfTestResult(status *smart.SelfTestStatus, testType string) (*smart.SelfTestEntry, string, string) {
	if status == nil {
		return nil, "", ""
	}
	if strings.EqualFold(status.Protocol, "ATA") && !status.ChecksumValid {
		return nil, "", ""
	}
	allFingerprints := make([]string, 0, len(status.Results))
	typeFingerprints := make([]string, 0, len(status.Results))
	var latest *smart.SelfTestEntry
	for i := range status.Results {
		entry := &status.Results[i]
		if entry.Outcome == smart.SelfTestOutcomeInProgress || entry.Status == "in_progress" || entry.Status == "unused" || entry.Status == "" {
			continue
		}
		fingerprint := selfTestResultFingerprint(*entry)
		allFingerprints = append(allFingerprints, fingerprint)
		if testType == "" || selfTestTypeMatches(testType, entry.Type) {
			typeFingerprints = append(typeFingerprints, fingerprint)
			if latest == nil {
				latest = entry
			}
		}
	}
	return latest, strings.Join(typeFingerprints, "\n"), strings.Join(allFingerprints, "\n")
}

func newScheduledSelfTestResult(status *smart.SelfTestStatus, testType, baseline string) *smart.SelfTestEntry {
	if status == nil || strings.EqualFold(status.Protocol, "ATA") && !status.ChecksumValid {
		return nil
	}
	counts := selfTestFingerprintCounts(baseline)
	for i := range status.Results {
		entry := &status.Results[i]
		if entry.Outcome == smart.SelfTestOutcomeInProgress || entry.Status == "in_progress" || entry.Status == "unused" || entry.Status == "" || !selfTestTypeMatches(testType, entry.Type) {
			continue
		}
		fingerprint := selfTestResultFingerprint(*entry)
		if counts[fingerprint] > 0 {
			counts[fingerprint]--
			continue
		}
		return entry
	}
	return nil
}

func selfTestTypeMatches(expected, observed string) bool {
	expected = strings.ToLower(strings.TrimSpace(expected))
	observed = strings.ToLower(strings.TrimSpace(observed))
	if expected == "long" {
		expected = "extended"
	}
	if observed == "long" {
		observed = "extended"
	}
	return expected == observed
}

func selfTestScheduleActive(status string) bool {
	return status == smartSelfTestScheduleStarting || status == smartSelfTestScheduleRunning
}

func scheduledSelfTestTerminalStatus(entry *smart.SelfTestEntry, executionStatus string) string {
	if entry != nil {
		switch entry.Outcome {
		case smart.SelfTestOutcomePassed:
			return smartSelfTestSchedulePassed
		case smart.SelfTestOutcomeFailed:
			return smartSelfTestScheduleFailed
		case smart.SelfTestOutcomeAborted:
			return smartSelfTestScheduleAborted
		}
	}
	switch {
	case executionStatus == "completed":
		return smartSelfTestSchedulePassed
	case strings.HasPrefix(executionStatus, "failed"), executionStatus == "fatal", executionStatus == "unknown_error", executionStatus == "completed_segment_failed":
		return smartSelfTestScheduleFailed
	case strings.HasPrefix(executionStatus, "aborted"), executionStatus == "interrupted":
		return smartSelfTestScheduleAborted
	default:
		return smartSelfTestScheduleUnknown
	}
}

func (s *Service) ListSelfTestSchedules(ctx context.Context) ([]SelfTestScheduleView, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("disk_service_not_initialized")
	}
	var schedules []models.DiskSmartSelfTestSchedule
	if err := s.DB.WithContext(ctx).Order("device ASC, test_type ASC").Find(&schedules).Error; err != nil {
		return nil, err
	}
	views := make([]SelfTestScheduleView, len(schedules))
	for i := range schedules {
		views[i] = selfTestScheduleView(schedules[i])
	}
	return views, nil
}

func (s *Service) CreateSelfTestSchedule(ctx context.Context, input SelfTestScheduleInput) (*SelfTestScheduleView, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("disk_service_not_initialized")
	}
	leaseToken, err := s.acquireSelfTestMutationLease(ctx)
	if err != nil {
		return nil, err
	}
	defer s.releaseSelfTestMutationLease(leaseToken)
	s.selfTestScheduleMu.Lock()
	defer s.selfTestScheduleMu.Unlock()
	disk, err := s.resolvePhysicalDisk(input.Device)
	if err != nil {
		return nil, err
	}
	kind, testType, err := normalizeScheduledSelfTestType(input.TestType)
	if err != nil {
		return nil, err
	}
	schedule, err := parseSelfTestSchedule(input.CronExpr, testType)
	if err != nil {
		return nil, err
	}
	deviceLock := s.selfTestDeviceMutex(disk.Name)
	deviceLock.Lock()
	capabilities, _, err := s.scheduledSelfTestBackend().Read(disk.Name)
	deviceLock.Unlock()
	if err != nil {
		return nil, err
	}
	if capabilities == nil || !capabilities.Supports(kind) {
		return nil, fmt.Errorf("%w: %s", smart.ErrUnsupportedFeature, testType)
	}
	diskKey, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		return nil, err
	}
	if err := s.validateScheduledPhysicalDiskIdentity(disk, diskKey); err != nil {
		return nil, err
	}
	next := nextSelfTestSchedule(schedule, time.Now())
	record := models.DiskSmartSelfTestSchedule{
		DiskKey:       diskKey,
		Device:        disk.Name,
		Model:         disk.Description,
		Serial:        disk.Serial,
		TestType:      testType,
		CronExpr:      strings.TrimSpace(input.CronExpr),
		Enabled:       input.Enabled,
		LastStatus:    smartSelfTestScheduleIdle,
		ProgressPct:   -1,
		NextRunAt:     &next,
		LastError:     "",
		ProgressKnown: false,
	}
	if err := s.DB.WithContext(ctx).Create(&record).Error; err != nil {
		return nil, err
	}
	view := selfTestScheduleView(record)
	return &view, nil
}

func (s *Service) UpdateSelfTestSchedule(ctx context.Context, id uint, input SelfTestScheduleInput) (*SelfTestScheduleView, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("disk_service_not_initialized")
	}
	leaseToken, err := s.acquireSelfTestMutationLease(ctx)
	if err != nil {
		return nil, err
	}
	defer s.releaseSelfTestMutationLease(leaseToken)
	s.selfTestScheduleMu.Lock()
	defer s.selfTestScheduleMu.Unlock()
	var record models.DiskSmartSelfTestSchedule
	if err := s.DB.WithContext(ctx).First(&record, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: %d", ErrSelfTestScheduleNotFound, id)
		}
		return nil, err
	}
	if selfTestScheduleActive(record.LastStatus) {
		return nil, ErrSelfTestScheduleRunning
	}
	kind, testType, err := normalizeScheduledSelfTestType(input.TestType)
	if err != nil {
		return nil, err
	}
	schedule, err := parseSelfTestSchedule(input.CronExpr, testType)
	if err != nil {
		return nil, err
	}
	inputDevice, deviceErr := normalizePhysicalDeviceName(input.Device)
	if !input.Enabled && deviceErr == nil && inputDevice == record.Device && testType == record.TestType {
		next := nextSelfTestSchedule(schedule, time.Now())
		record.CronExpr = strings.TrimSpace(input.CronExpr)
		record.Enabled = false
		record.NextRunAt = &next
		record.QueuedAt = nil
		record.QueueUpdatedAt = nil
		record.LastStatus = smartSelfTestScheduleIdle
		record.OccurrenceKey = ""
		record.LastError = ""
		record.ProgressPct = -1
		record.ProgressKnown = false
		record.RunningObserved = false
		record.TimeoutAbortAttempted = false
		if err := s.DB.WithContext(ctx).Save(&record).Error; err != nil {
			return nil, err
		}
		view := selfTestScheduleView(record)
		return &view, nil
	}
	disk, err := s.resolvePhysicalDisk(input.Device)
	if err != nil {
		return nil, err
	}
	deviceLock := s.selfTestDeviceMutex(disk.Name)
	deviceLock.Lock()
	capabilities, _, err := s.scheduledSelfTestBackend().Read(disk.Name)
	deviceLock.Unlock()
	if err != nil {
		return nil, err
	}
	if capabilities == nil || !capabilities.Supports(kind) {
		return nil, fmt.Errorf("%w: %s", smart.ErrUnsupportedFeature, testType)
	}
	diskKey, err := selfTestScheduleDiskKey(disk)
	if err != nil {
		return nil, err
	}
	if err := s.validateScheduledPhysicalDiskIdentity(disk, diskKey); err != nil {
		return nil, err
	}
	next := nextSelfTestSchedule(schedule, time.Now())
	identityChanged := record.DiskKey != diskKey || record.TestType != testType
	record.DiskKey = diskKey
	record.Device = disk.Name
	record.Model = disk.Description
	record.Serial = disk.Serial
	record.TestType = testType
	record.CronExpr = strings.TrimSpace(input.CronExpr)
	record.Enabled = input.Enabled
	record.NextRunAt = &next
	record.QueuedAt = nil
	record.QueueUpdatedAt = nil
	record.LastStatus = smartSelfTestScheduleIdle
	record.OccurrenceKey = ""
	record.LastError = ""
	record.ProgressPct = -1
	record.ProgressKnown = false
	record.RunningObserved = false
	record.TimeoutAbortAttempted = false
	if identityChanged {
		record.LastRunAt = nil
		record.BaselineFingerprint = ""
		record.BaselineLogFingerprint = ""
		record.BaselineValid = false
		record.LastResultFingerprint = ""
		record.EstimatedMinutes = 0
	}
	if err := s.DB.WithContext(ctx).Save(&record).Error; err != nil {
		return nil, err
	}
	view := selfTestScheduleView(record)
	return &view, nil
}

func (s *Service) DeleteSelfTestSchedule(ctx context.Context, id uint) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("disk_service_not_initialized")
	}
	leaseToken, err := s.acquireSelfTestMutationLease(ctx)
	if err != nil {
		return err
	}
	defer s.releaseSelfTestMutationLease(leaseToken)
	s.selfTestScheduleMu.Lock()
	defer s.selfTestScheduleMu.Unlock()
	var record models.DiskSmartSelfTestSchedule
	if err := s.DB.WithContext(ctx).First(&record, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: %d", ErrSelfTestScheduleNotFound, id)
		}
		return err
	}
	if selfTestScheduleActive(record.LastStatus) {
		return ErrSelfTestScheduleRunning
	}
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("schedule_id = ?", record.ID).Delete(&models.DiskSmartSelfTestEvent{}).Error; err != nil {
			return err
		}
		return tx.Delete(&record).Error
	})
}

func (s *Service) RegisterJobs() {
	db.QueueRegisterJSON(smartSelfTestScheduleJobName, func(ctx context.Context, job smartSelfTestSchedulerJob) error {
		return s.handleSelfTestSchedulerJob(ctx, job)
	})
	db.QueueRegisterJSON(smartSelfTestEventJobName, func(ctx context.Context, job smartSelfTestEventJob) error {
		return s.handleSelfTestEventJob(ctx, job)
	})
}

func (s *Service) SetSelfTestSchedulerReady(ready bool) {
	if s == nil {
		return
	}
	s.selfTestJobsReady.Store(ready)
}

func (s *Service) StartSelfTestScheduler(ctx context.Context) {
	if s == nil || s.DB == nil {
		return
	}
	scheduleTicker := time.NewTicker(smartSelfTestScheduleInterval)
	trackingTicker := time.NewTicker(smartSelfTestTrackInterval)
	eventTicker := time.NewTicker(smartSelfTestEventDispatchInterval)
	defer scheduleTicker.Stop()
	defer trackingTicker.Stop()
	defer eventTicker.Stop()
	if running, err := s.hasRunningSelfTestRuns(ctx); err != nil {
		logger.L.Error().Err(err).Msg("disk_self_test_run_lookup_failed")
	} else {
		s.selfTestTrackingActive.Store(running)
	}
	if err := s.enqueueSelfTestSchedulerJob(ctx, time.Now().UTC()); err != nil && !errors.Is(err, context.Canceled) {
		logger.L.Error().Err(err).Msg("disk_self_test_scheduler_enqueue_failed")
	}
	if pending, err := s.hasPendingSelfTestEvents(ctx); err != nil {
		logger.L.Error().Err(err).Msg("disk_self_test_notification_lookup_failed")
	} else if pending {
		s.selfTestEventRelayActive.Store(true)
		if err := s.runSelfTestEventRelayBatch(ctx, time.Now().UTC()); err != nil && !errors.Is(err, context.Canceled) {
			logger.L.Error().Err(err).Msg("disk_self_test_notification_enqueue_failed")
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-scheduleTicker.C:
			if err := s.enqueueSelfTestSchedulerJob(ctx, now.UTC()); err != nil && !errors.Is(err, context.Canceled) {
				logger.L.Error().Err(err).Msg("disk_self_test_scheduler_enqueue_failed")
			}
		case now := <-trackingTicker.C:
			if !s.selfTestTrackingActive.Load() {
				continue
			}
			running, err := s.hasRunningSelfTestRuns(ctx)
			if err != nil {
				logger.L.Error().Err(err).Msg("disk_self_test_run_lookup_failed")
				continue
			}
			if !running {
				s.selfTestTrackingActive.Store(false)
				continue
			}
			if err := s.enqueueSelfTestSchedulerJob(ctx, now.UTC()); err != nil && !errors.Is(err, context.Canceled) {
				logger.L.Error().Err(err).Msg("disk_self_test_tracker_enqueue_failed")
			}
		case now := <-eventTicker.C:
			if !s.selfTestEventRelayActive.Load() {
				continue
			}
			version := s.selfTestEventRelayVersion.Load()
			if err := s.runSelfTestEventRelayBatch(ctx, now.UTC()); err != nil && !errors.Is(err, context.Canceled) {
				logger.L.Error().Err(err).Msg("disk_self_test_notification_enqueue_failed")
				continue
			}
			s.refreshSelfTestEventRelay(ctx, version)
		}
	}
}

func (s *Service) enqueueSelfTestSchedulerJob(ctx context.Context, now time.Time) error {
	if s == nil || s.DB == nil || !s.selfTestJobsReady.Load() {
		return nil
	}
	now = now.UTC()
	lease := models.DiskSmartSelfTestSchedulerLease{ID: 1}
	if err := s.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&lease).Error; err != nil {
		return err
	}
	token := utils.GenerateRandomUUID()
	staleBefore := now.Add(-smartSelfTestDispatchLease)
	reservation := s.DB.WithContext(ctx).Model(&models.DiskSmartSelfTestSchedulerLease{}).
		Where("id = ? AND (dispatch_token = ? OR dispatched_at IS NULL OR dispatched_at < ?)", 1, "", staleBefore).
		Updates(map[string]any{"dispatch_token": token, "dispatched_at": now})
	if reservation.Error != nil {
		return reservation.Error
	}
	if reservation.RowsAffected == 0 {
		return nil
	}
	job := smartSelfTestSchedulerJob{RequestedAt: now, DispatchToken: token}
	var err error
	if s.selfTestJobEnqueue != nil {
		err = s.selfTestJobEnqueue(ctx, job)
	} else {
		err = db.EnqueueJSON(ctx, smartSelfTestScheduleJobName, job)
	}
	if err == nil {
		return nil
	}
	releaseErr := s.releaseSelfTestSchedulerDispatch(context.WithoutCancel(ctx), token)
	return errors.Join(err, releaseErr)
}

func (s *Service) handleSelfTestSchedulerJob(ctx context.Context, job smartSelfTestSchedulerJob) error {
	if s == nil || s.DB == nil || job.DispatchToken == "" || job.RequestedAt.IsZero() {
		return nil
	}
	now := time.Now().UTC()
	if !s.selfTestJobsReady.Load() || now.Sub(job.RequestedAt) > smartSelfTestDispatchLease || now.Before(job.RequestedAt.Add(-time.Minute)) {
		if err := s.releaseSelfTestSchedulerDispatch(context.WithoutCancel(ctx), job.DispatchToken); err != nil {
			logger.L.Error().Err(err).Msg("disk_self_test_scheduler_dispatch_release_failed")
		}
		return nil
	}
	var lease models.DiskSmartSelfTestSchedulerLease
	if err := s.DB.WithContext(ctx).Where("id = ? AND dispatch_token = ?", 1, job.DispatchToken).First(&lease).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) && !errors.Is(err, context.Canceled) {
			logger.L.Error().Err(err).Msg("disk_self_test_scheduler_dispatch_lookup_failed")
		}
		return nil
	}
	err := s.runSelfTestSchedulerTick(ctx, now)
	releaseErr := s.releaseSelfTestSchedulerDispatch(context.WithoutCancel(ctx), job.DispatchToken)
	jobErr := errors.Join(err, releaseErr)
	if jobErr != nil && !errors.Is(jobErr, context.Canceled) {
		logger.L.Error().Err(jobErr).Msg("disk_self_test_scheduler_tick_failed")
	}
	return nil
}

func (s *Service) releaseSelfTestSchedulerDispatch(ctx context.Context, token string) error {
	if token == "" || s == nil || s.DB == nil {
		return nil
	}
	return s.DB.WithContext(ctx).Model(&models.DiskSmartSelfTestSchedulerLease{}).
		Where("id = ? AND dispatch_token = ?", 1, token).
		Updates(map[string]any{"dispatch_token": "", "dispatched_at": nil}).Error
}

func (s *Service) runSelfTestSchedulerTick(ctx context.Context, now time.Time) error {
	if s == nil || s.DB == nil {
		return nil
	}
	now = now.UTC()
	leaseToken, acquired, err := s.acquireSelfTestSchedulerLease(ctx, now)
	if err != nil {
		return err
	}
	if !acquired {
		return nil
	}
	defer s.releaseSelfTestSchedulerLease(context.WithoutCancel(ctx), leaseToken)
	s.selfTestScheduleMu.Lock()
	defer s.selfTestScheduleMu.Unlock()
	if err := s.reconcileManualSelfTestRuns(ctx, now); err != nil {
		logger.L.Error().Err(err).Msg("disk_manual_self_test_reconcile_failed")
	}
	var schedules []models.DiskSmartSelfTestSchedule
	if err := s.DB.WithContext(ctx).
		Where("last_status IN ? OR (enabled = ? AND (next_run_at IS NULL OR julianday(next_run_at) <= julianday(?)))", []string{smartSelfTestScheduleQueued, smartSelfTestScheduleStarting, smartSelfTestScheduleRunning}, true, now).
		Order("id ASC").
		Find(&schedules).Error; err != nil {
		return err
	}
	for i := range schedules {
		if err := s.renewSelfTestSchedulerLease(ctx, leaseToken, time.Now().UTC()); err != nil {
			return err
		}
		record := &schedules[i]
		if !record.Enabled && record.LastStatus == smartSelfTestScheduleQueued {
			record.LastStatus = smartSelfTestScheduleIdle
			record.QueuedAt = nil
			record.QueueUpdatedAt = nil
			if err := s.DB.WithContext(ctx).Save(record).Error; err != nil {
				return err
			}
			continue
		}
		if record.Enabled && record.LastStatus != smartSelfTestScheduleQueued && !selfTestScheduleActive(record.LastStatus) && (record.NextRunAt == nil || !record.NextRunAt.After(now)) {
			scheduledFor := now
			if record.NextRunAt != nil {
				scheduledFor = *record.NextRunAt
			}
			record.OccurrenceKey = scheduledFor.UTC().Format(time.RFC3339Nano)
			parsed, err := parseSelfTestSchedule(record.CronExpr, record.TestType)
			if err != nil {
				record.LastStatus = smartSelfTestScheduleFailed
				record.LastError = err.Error()
				record.Enabled = false
				record.NextRunAt = nil
				record.QueuedAt = nil
				record.QueueUpdatedAt = nil
				event := newScheduledSelfTestEvent(*record, "self_test_schedule_failed", "warning", "Scheduled S.M.A.R.T self-test could not be queued", record.LastError)
				if err := s.saveScheduledSelfTestWithEvent(ctx, record, &event); err != nil {
					return err
				}
				continue
			}
			next := nextSelfTestSchedule(parsed, now)
			if record.NextRunAt == nil || now.Sub(*record.NextRunAt) > smartSelfTestScheduleCatchupWindow {
				record.NextRunAt = &next
				record.LastStatus = smartSelfTestScheduleMissed
				record.LastError = "scheduled run was missed while the service was unavailable"
				event := newScheduledSelfTestEvent(*record, "self_test_schedule_missed", "warning", fmt.Sprintf("Disk %s scheduled S.M.A.R.T self-test was missed", record.Device), record.LastError)
				if err := s.saveScheduledSelfTestWithEvent(ctx, record, &event); err != nil {
					return err
				}
				continue
			}
			queuedAt := now
			record.NextRunAt = &next
			record.QueuedAt = &queuedAt
			record.QueueUpdatedAt = &queuedAt
			record.LastStatus = smartSelfTestScheduleQueued
			record.LastError = ""
			if err := s.DB.WithContext(ctx).Save(record).Error; err != nil {
				return err
			}
		}
	}
	active := false
	for i := range schedules {
		if err := s.renewSelfTestSchedulerLease(ctx, leaseToken, time.Now().UTC()); err != nil {
			return err
		}
		record := &schedules[i]
		if !selfTestScheduleActive(record.LastStatus) {
			continue
		}
		if err := s.reconcileScheduledSelfTest(ctx, record, now); err != nil {
			return err
		}
		if selfTestScheduleActive(record.LastStatus) {
			active = true
		}
	}
	if active {
		heartbeatBefore := now.Add(-10 * time.Minute)
		if err := s.DB.WithContext(ctx).Model(&models.DiskSmartSelfTestSchedule{}).
			Where("last_status = ? AND (queue_updated_at IS NULL OR queue_updated_at < ?)", smartSelfTestScheduleQueued, heartbeatBefore).
			Update("queue_updated_at", now).Error; err != nil {
			return err
		}
		return nil
	}
	var queuedRecords []models.DiskSmartSelfTestSchedule
	if err := s.DB.WithContext(ctx).
		Where("enabled = ? AND last_status = ?", true, smartSelfTestScheduleQueued).
		Order("queued_at ASC, id ASC").
		Find(&queuedRecords).Error; err != nil {
		return err
	}
	for i := range queuedRecords {
		if err := s.renewSelfTestSchedulerLease(ctx, leaseToken, time.Now().UTC()); err != nil {
			return err
		}
		queued := &queuedRecords[i]
		heartbeat := queued.QueueUpdatedAt
		if heartbeat == nil {
			heartbeat = queued.QueuedAt
		}
		stale := heartbeat == nil || now.Sub(*heartbeat) > smartSelfTestScheduleCatchupWindow || (queued.NextRunAt != nil && !queued.NextRunAt.After(now))
		if stale {
			expiredError := "queued run expired while the service was unavailable"
			if queued.NextRunAt != nil && !queued.NextRunAt.After(now) {
				expiredError = "queued run was superseded by the next scheduled occurrence"
			} else if queued.LastError != "" {
				expiredError = fmt.Sprintf("queued run expired: %s", queued.LastError)
			}
			if parsed, err := parseSelfTestSchedule(queued.CronExpr, queued.TestType); err == nil && (queued.NextRunAt == nil || !queued.NextRunAt.After(now)) {
				next := nextSelfTestSchedule(parsed, now)
				queued.NextRunAt = &next
			}
			queued.LastStatus = smartSelfTestScheduleMissed
			queued.LastError = expiredError
			queued.QueuedAt = nil
			queued.QueueUpdatedAt = nil
			event := newScheduledSelfTestEvent(*queued, "self_test_schedule_missed", "warning", fmt.Sprintf("Disk %s scheduled S.M.A.R.T self-test was missed", queued.Device), queued.LastError)
			if err := s.saveScheduledSelfTestWithEvent(ctx, queued, &event); err != nil {
				return err
			}
			continue
		}
		if err := s.startScheduledSelfTest(ctx, queued, now); err != nil {
			return err
		}
		if selfTestScheduleActive(queued.LastStatus) {
			return nil
		}
	}
	return nil
}

func (s *Service) acquireSelfTestSchedulerLease(ctx context.Context, now time.Time) (string, bool, error) {
	now = now.UTC()
	token := utils.GenerateRandomUUID()
	lease := models.DiskSmartSelfTestSchedulerLease{ID: 1}
	if err := s.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&lease).Error; err != nil {
		return "", false, err
	}
	result := s.DB.WithContext(ctx).Model(&models.DiskSmartSelfTestSchedulerLease{}).
		Where("id = ? AND (token = ? OR expires_at <= ?)", 1, "", now).
		Updates(map[string]any{"token": token, "expires_at": now.Add(smartSelfTestSchedulerLeaseDuration)})
	if result.Error != nil {
		return "", false, result.Error
	}
	return token, result.RowsAffected == 1, nil
}

func (s *Service) acquireSelfTestMutationLease(ctx context.Context) (string, error) {
	if s == nil || s.DB == nil {
		return "", nil
	}
	token, acquired, err := s.acquireSelfTestSchedulerLease(ctx, time.Now().UTC())
	if err != nil {
		return "", err
	}
	if !acquired {
		return "", ErrSelfTestSchedulerBusy
	}
	return token, nil
}

func (s *Service) releaseSelfTestMutationLease(token string) {
	if token == "" || s == nil || s.DB == nil {
		return
	}
	s.releaseSelfTestSchedulerLease(context.Background(), token)
}

func (s *Service) releaseSelfTestSchedulerLease(ctx context.Context, token string) {
	if token == "" {
		return
	}
	if err := s.DB.WithContext(ctx).Model(&models.DiskSmartSelfTestSchedulerLease{}).
		Where("id = ? AND token = ?", 1, token).
		Updates(map[string]any{"token": "", "expires_at": time.Time{}}).Error; err != nil {
		logger.L.Error().Err(err).Msg("disk_self_test_scheduler_lease_release_failed")
	}
}

func (s *Service) renewSelfTestSchedulerLease(ctx context.Context, token string, now time.Time) error {
	now = now.UTC()
	result := s.DB.WithContext(ctx).Model(&models.DiskSmartSelfTestSchedulerLease{}).
		Where("id = ? AND token = ?", 1, token).
		Update("expires_at", now.Add(smartSelfTestSchedulerLeaseDuration))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("disk_self_test_scheduler_lease_lost")
	}
	return nil
}

func (s *Service) restoreStartingSelfTestSchedule(ctx context.Context, record *models.DiskSmartSelfTestSchedule, queued models.DiskSmartSelfTestSchedule, cause error) error {
	restore := s.DB.WithContext(context.WithoutCancel(ctx)).Model(&models.DiskSmartSelfTestSchedule{}).
		Where("id = ? AND last_status = ?", record.ID, smartSelfTestScheduleStarting).
		Updates(map[string]any{
			"device":                   queued.Device,
			"last_status":              queued.LastStatus,
			"last_error":               queued.LastError,
			"queued_at":                queued.QueuedAt,
			"queue_updated_at":         queued.QueueUpdatedAt,
			"last_run_at":              queued.LastRunAt,
			"baseline_fingerprint":     queued.BaselineFingerprint,
			"baseline_log_fingerprint": queued.BaselineLogFingerprint,
			"baseline_valid":           queued.BaselineValid,
			"progress_pct":             queued.ProgressPct,
			"progress_known":           queued.ProgressKnown,
			"estimated_minutes":        queued.EstimatedMinutes,
			"running_observed":         queued.RunningObserved,
			"timeout_abort_attempted":  queued.TimeoutAbortAttempted,
		})
	if restore.Error != nil {
		return errors.Join(cause, restore.Error)
	}
	*record = queued
	return cause
}

func (s *Service) startScheduledSelfTest(ctx context.Context, record *models.DiskSmartSelfTestSchedule, now time.Time) error {
	kind, _, err := normalizeScheduledSelfTestType(record.TestType)
	if err != nil {
		return s.failScheduledSelfTest(ctx, record, "self_test_schedule_failed", err)
	}
	disk, err := s.resolveScheduledPhysicalDisk(record)
	if err != nil {
		return s.failScheduledSelfTest(ctx, record, "self_test_device_unavailable", err)
	}
	if disk.Name != record.Device {
		record.Device = disk.Name
	}
	deviceLock := s.selfTestDeviceMutex(disk.Name)
	deviceLock.Lock()
	defer deviceLock.Unlock()
	backend := s.scheduledSelfTestBackend()
	capabilities, status, err := backend.Read(disk.Name)
	if err != nil {
		return s.failScheduledSelfTest(ctx, record, "self_test_capabilities_unavailable", err)
	}
	if capabilities == nil || !capabilities.Supports(kind) {
		return s.failScheduledSelfTest(ctx, record, "self_test_unsupported", fmt.Errorf("%w: %s", smart.ErrUnsupportedFeature, record.TestType))
	}
	if status == nil {
		return s.failScheduledSelfTest(ctx, record, "self_test_status_unavailable", fmt.Errorf("self-test status unavailable"))
	}
	if status.Running || status.State == smart.SelfTestStateAmbiguous {
		record.LastError = "device self-test is already running"
		if err := s.DB.WithContext(ctx).Save(record).Error; err != nil {
			return err
		}
		return nil
	}
	historyCtx := context.WithoutCancel(ctx)
	historyInfo := &diskServiceInterfaces.DiskSelfTestInfo{
		Device: disk.Name,
		Status: mapSelfTestState(status),
	}
	promoteRunningSelfTestResult(historyInfo)
	if err := s.attachSelfTestRunTimes(historyCtx, disk, historyInfo); err != nil {
		return err
	}
	queuedState := *record
	originalQueuedAt := record.QueuedAt
	originalQueueUpdatedAt := record.QueueUpdatedAt
	_, baseline, baselineLog := latestScheduledSelfTestResult(status, record.TestType)
	record.LastStatus = smartSelfTestScheduleStarting
	record.LastError = ""
	record.QueuedAt = nil
	record.QueueUpdatedAt = nil
	record.LastRunAt = &now
	record.BaselineFingerprint = baseline
	record.BaselineLogFingerprint = baselineLog
	record.BaselineValid = !strings.EqualFold(status.Protocol, "ATA") || status.ChecksumValid
	record.ProgressPct = -1
	record.ProgressKnown = false
	record.EstimatedMinutes = capabilities.DurationMinutes(kind)
	record.RunningObserved = false
	record.TimeoutAbortAttempted = false
	claim := s.DB.WithContext(ctx).Model(&models.DiskSmartSelfTestSchedule{}).
		Where("id = ? AND last_status = ?", record.ID, smartSelfTestScheduleQueued).
		Updates(map[string]any{
			"device":                   record.Device,
			"last_status":              record.LastStatus,
			"last_error":               record.LastError,
			"queued_at":                record.QueuedAt,
			"queue_updated_at":         record.QueueUpdatedAt,
			"last_run_at":              record.LastRunAt,
			"baseline_fingerprint":     record.BaselineFingerprint,
			"baseline_log_fingerprint": record.BaselineLogFingerprint,
			"baseline_valid":           record.BaselineValid,
			"progress_pct":             record.ProgressPct,
			"progress_known":           record.ProgressKnown,
			"estimated_minutes":        record.EstimatedMinutes,
			"running_observed":         record.RunningObserved,
			"timeout_abort_attempted":  record.TimeoutAbortAttempted,
			"updated_at":               now,
		})
	if claim.Error != nil {
		return claim.Error
	}
	if claim.RowsAffected == 0 {
		return s.DB.WithContext(ctx).First(record, record.ID).Error
	}
	if err := ctx.Err(); err != nil {
		return s.restoreStartingSelfTestSchedule(ctx, record, queuedState, err)
	}
	if err := s.recordScheduledSelfTestRun(historyCtx, disk, *record, now); err != nil {
		return s.restoreStartingSelfTestSchedule(ctx, record, queuedState, err)
	}
	runKey := scheduledSelfTestRunKey(*record)
	startErr := backend.Start(disk.Name, kind)
	s.invalidateSelfTestInfo(disk.Name)
	if startErr != nil {
		if discardErr := s.discardSelfTestRun(historyCtx, runKey); discardErr != nil {
			return errors.Join(startErr, discardErr)
		}
		err = startErr
		if errors.Is(err, smart.ErrSelfTestInProgress) {
			record.LastStatus = smartSelfTestScheduleQueued
			record.LastError = err.Error()
			record.QueuedAt = originalQueuedAt
			record.QueueUpdatedAt = originalQueueUpdatedAt
			record.LastRunAt = nil
			record.BaselineFingerprint = ""
			record.BaselineLogFingerprint = ""
			record.BaselineValid = false
			return s.DB.WithContext(ctx).Save(record).Error
		}
		if smart.IsControllerError(err) {
			record.LastError = err.Error()
			return s.DB.WithContext(ctx).Save(record).Error
		}
		return s.failScheduledSelfTest(ctx, record, "self_test_start_failed", err)
	}
	if err := s.supersedeUnfinishedSelfTestRuns(historyCtx, selfTestRunDiskKey(disk), runKey, now); err != nil {
		logger.L.Error().Err(err).Str("device", disk.Name).Msg("disk_self_test_run_supersede_failed")
	}
	s.storeActiveSelfTestKind(disk.Name, kind)
	record.LastStatus = smartSelfTestScheduleRunning
	record.LastError = ""
	event := newScheduledSelfTestEvent(*record, "self_test_started", "info", fmt.Sprintf("Disk %s scheduled S.M.A.R.T self-test started", record.Device), fmt.Sprintf("The scheduled %s self-test is now running.", record.TestType))
	if err := s.saveScheduledSelfTestWithEvent(ctx, record, &event); err != nil {
		return err
	}
	return nil
}

func (s *Service) reconcileScheduledSelfTest(ctx context.Context, record *models.DiskSmartSelfTestSchedule, now time.Time) error {
	before := *record
	disk, err := s.resolveScheduledPhysicalDisk(record)
	if err != nil {
		record.LastError = err.Error()
		if selfTestSchedulePastMaximumDeadline(record, now) {
			return s.finishScheduledSelfTestUnknown(ctx, record, "self_test_device_unavailable", record.LastError)
		}
		return s.saveScheduledSelfTestRuntimeIfChanged(ctx, before, record)
	}
	if disk.Name != record.Device {
		record.Device = disk.Name
	}
	deviceLock := s.selfTestDeviceMutex(disk.Name)
	deviceLock.Lock()
	defer deviceLock.Unlock()
	backend := s.scheduledSelfTestBackend()
	capabilities, status, err := backend.Read(disk.Name)
	if err != nil {
		record.LastError = err.Error()
		if selfTestSchedulePastMaximumDeadline(record, now) {
			return s.finishScheduledSelfTestUnknown(ctx, record, "self_test_status_unavailable", record.LastError)
		}
		return s.saveScheduledSelfTestRuntimeIfChanged(ctx, before, record)
	}
	if status == nil {
		record.LastError = "self-test status unavailable"
		if selfTestSchedulePastMaximumDeadline(record, now) {
			return s.finishScheduledSelfTestUnknown(ctx, record, "self_test_status_unavailable", record.LastError)
		}
		return s.saveScheduledSelfTestRuntimeIfChanged(ctx, before, record)
	}
	_, _, resultFingerprint := latestScheduledSelfTestResult(status, record.TestType)
	entry := newScheduledSelfTestResult(status, record.TestType, record.BaselineFingerprint)
	if before.LastStatus == smartSelfTestScheduleStarting && status.Running && status.Type != "" && !selfTestTypeMatches(record.TestType, string(status.Type)) {
		queuedAt := now
		record.LastStatus = smartSelfTestScheduleQueued
		record.LastError = "another self-test is already running"
		record.QueuedAt = &queuedAt
		record.QueueUpdatedAt = &queuedAt
		record.LastRunAt = nil
		record.BaselineFingerprint = ""
		record.BaselineLogFingerprint = ""
		record.BaselineValid = false
		record.ProgressPct = -1
		record.ProgressKnown = false
		record.RunningObserved = false
		return s.DB.WithContext(ctx).Save(record).Error
	}
	if status.Running || status.State == smart.SelfTestStateAmbiguous {
		record.LastStatus = smartSelfTestScheduleRunning
		record.ProgressPct = status.ProgressPct
		record.ProgressKnown = status.ProgressKnown
		record.RunningObserved = true
		if !selfTestSchedulePastMaximumDeadline(record, now) {
			record.LastError = ""
			if before.LastStatus == smartSelfTestScheduleStarting {
				event := newScheduledSelfTestEvent(*record, "self_test_started", "info", fmt.Sprintf("Disk %s scheduled S.M.A.R.T self-test started", record.Device), fmt.Sprintf("The scheduled %s self-test is now running.", record.TestType))
				return s.saveScheduledSelfTestWithEvent(ctx, record, &event)
			}
			return s.saveScheduledSelfTestRuntimeIfChanged(ctx, before, record)
		}
		return s.abortTimedOutScheduledSelfTest(ctx, record, capabilities, backend, disk.Name)
	}
	if capabilities != nil && !capabilities.ResultLog {
		record.LastError = "the device does not provide a self-test result log"
		if record.RunningObserved || selfTestSchedulePastDeadline(record, now, selfTestScheduleCompletionGrace(record)) {
			return s.finishScheduledSelfTestUnknown(ctx, record, "self_test_result_unknown", record.LastError)
		}
		return s.saveScheduledSelfTestRuntimeIfChanged(ctx, before, record)
	}
	if strings.EqualFold(status.Protocol, "ATA") && !status.ChecksumValid {
		record.LastError = "self-test result log checksum is invalid"
		if selfTestSchedulePastDeadline(record, now, selfTestScheduleCompletionGrace(record)) {
			return s.finishScheduledSelfTestUnknown(ctx, record, "self_test_result_unknown", record.LastError)
		}
		return s.saveScheduledSelfTestRuntimeIfChanged(ctx, before, record)
	}
	if !record.BaselineValid && !record.RunningObserved {
		record.LastError = "self-test result baseline was unavailable"
		if selfTestSchedulePastDeadline(record, now, selfTestScheduleCompletionGrace(record)) {
			return s.finishScheduledSelfTestUnknown(ctx, record, "self_test_result_unknown", record.LastError)
		}
		return s.saveScheduledSelfTestRuntimeIfChanged(ctx, before, record)
	}
	if entry == nil {
		if record.RunningObserved {
			if selfTestFingerprintHasAdditionalOccurrence(resultFingerprint, record.BaselineLogFingerprint) || (status.Type != "" && !selfTestTypeMatches(record.TestType, string(status.Type))) {
				return s.finishScheduledSelfTestUnknown(ctx, record, "self_test_result_unknown", "another self-test replaced the scheduled test status")
			}
		} else if !selfTestSchedulePastDeadline(record, now, selfTestScheduleCompletionGrace(record)) {
			return s.saveScheduledSelfTestRuntimeIfChanged(ctx, before, record)
		} else {
			return s.finishScheduledSelfTestUnknown(ctx, record, "self_test_result_unknown", "the device did not record a new self-test result")
		}
	}
	record.LastStatus = scheduledSelfTestTerminalStatus(entry, status.ExecutionStatus)
	s.selfTestActiveKinds.Delete(disk.Name)
	record.LastResultFingerprint = resultFingerprint
	record.QueuedAt = nil
	record.QueueUpdatedAt = nil
	record.ProgressPct = 100
	record.ProgressKnown = record.LastStatus != smartSelfTestScheduleUnknown
	record.RunningObserved = false
	record.TimeoutAbortAttempted = false
	record.LastError = ""
	var mappedEntry *diskServiceInterfaces.DiskSelfTestResult
	if entry != nil {
		result := mapSelfTestResult(*entry)
		mappedEntry = &result
	}
	if err := s.finishScheduledSelfTestRun(context.WithoutCancel(ctx), *record, mappedEntry, record.LastStatus, now); err != nil {
		logger.L.Error().Err(err).Uint("schedule_id", record.ID).Msg("disk_self_test_run_update_failed")
	}
	condition := "self_test_completed_unknown"
	severity := "warning"
	title := fmt.Sprintf("Disk %s scheduled S.M.A.R.T self-test result is unknown", record.Device)
	body := fmt.Sprintf("The scheduled %s self-test finished without a recognized result.", record.TestType)
	switch record.LastStatus {
	case smartSelfTestSchedulePassed:
		condition = "self_test_passed"
		severity = "info"
		title = fmt.Sprintf("Disk %s scheduled S.M.A.R.T self-test passed", record.Device)
		body = fmt.Sprintf("The scheduled %s self-test completed successfully.", record.TestType)
	case smartSelfTestScheduleFailed:
		condition = "self_test_failed"
		severity = "critical"
		title = fmt.Sprintf("Disk %s scheduled S.M.A.R.T self-test failed", record.Device)
		body = fmt.Sprintf("The scheduled %s self-test reported a failure.", record.TestType)
	case smartSelfTestScheduleAborted:
		condition = "self_test_aborted"
		severity = "warning"
		title = fmt.Sprintf("Disk %s scheduled S.M.A.R.T self-test was aborted", record.Device)
		body = fmt.Sprintf("The scheduled %s self-test did not complete.", record.TestType)
	}
	event := newScheduledSelfTestEvent(*record, condition, severity, title, body)
	if err := s.saveScheduledSelfTestWithEvent(ctx, record, &event); err != nil {
		return err
	}
	return nil
}

func (s *Service) saveScheduledSelfTestRuntimeIfChanged(ctx context.Context, before models.DiskSmartSelfTestSchedule, record *models.DiskSmartSelfTestSchedule) error {
	if before.Device == record.Device &&
		before.LastStatus == record.LastStatus &&
		before.LastError == record.LastError &&
		before.ProgressPct == record.ProgressPct &&
		before.ProgressKnown == record.ProgressKnown &&
		before.RunningObserved == record.RunningObserved &&
		before.TimeoutAbortAttempted == record.TimeoutAbortAttempted {
		return nil
	}
	return s.DB.WithContext(ctx).Save(record).Error
}

func selfTestScheduleCompletionGrace(record *models.DiskSmartSelfTestSchedule) time.Duration {
	duration := time.Duration(record.EstimatedMinutes) * time.Minute
	grace := duration / 4
	if grace < 30*time.Minute {
		return 30 * time.Minute
	}
	if grace > 6*time.Hour {
		return 6 * time.Hour
	}
	return grace
}

func selfTestSchedulePastMaximumDeadline(record *models.DiskSmartSelfTestSchedule, now time.Time) bool {
	if record == nil || record.LastRunAt == nil {
		return false
	}
	duration := time.Duration(record.EstimatedMinutes) * time.Minute
	if duration <= 0 {
		maximum := 6 * time.Hour
		if record.TestType == "extended" {
			maximum = 48 * time.Hour
		}
		return !now.Before(record.LastRunAt.Add(maximum))
	}
	grace := duration
	minimumGrace := time.Hour
	if record.TestType == "extended" {
		minimumGrace = 6 * time.Hour
	}
	if grace < minimumGrace {
		grace = minimumGrace
	}
	return !now.Before(record.LastRunAt.Add(duration + grace))
}

func (s *Service) abortTimedOutScheduledSelfTest(ctx context.Context, record *models.DiskSmartSelfTestSchedule, capabilities *smart.SelfTestCapabilities, backend selfTestBackend, device string) error {
	if record.TimeoutAbortAttempted {
		return s.finishScheduledSelfTestUnknown(ctx, record, "self_test_timeout_aborted", "the self-test remained active after its timeout abort attempt")
	}
	record.TimeoutAbortAttempted = true
	if capabilities == nil || !capabilities.Abort {
		return s.finishScheduledSelfTestUnknown(ctx, record, "self_test_timeout_aborted", "the self-test exceeded its maximum duration and cannot be aborted")
	}
	if err := backend.Stop(device); err != nil && !errors.Is(err, ErrSelfTestNotRunning) {
		return s.finishScheduledSelfTestUnknown(ctx, record, "self_test_timeout_aborted", fmt.Sprintf("the self-test exceeded its maximum duration and abort failed: %v", err))
	}
	s.invalidateSelfTestInfo(device)
	return s.finishScheduledSelfTestUnknown(ctx, record, "self_test_timeout_aborted", "the self-test exceeded its maximum duration and was aborted")
}

func selfTestSchedulePastDeadline(record *models.DiskSmartSelfTestSchedule, now time.Time, grace time.Duration) bool {
	if record == nil || record.LastRunAt == nil {
		return false
	}
	duration := time.Duration(record.EstimatedMinutes) * time.Minute
	return !now.Before(record.LastRunAt.Add(duration + grace))
}

func (s *Service) finishScheduledSelfTestUnknown(ctx context.Context, record *models.DiskSmartSelfTestSchedule, condition, message string) error {
	s.selfTestActiveKinds.Delete(record.Device)
	record.LastStatus = smartSelfTestScheduleUnknown
	record.LastError = message
	record.QueuedAt = nil
	record.QueueUpdatedAt = nil
	record.ProgressPct = -1
	record.ProgressKnown = false
	record.RunningObserved = false
	record.TimeoutAbortAttempted = false
	if err := s.finishScheduledSelfTestRun(context.WithoutCancel(ctx), *record, nil, smartSelfTestRunUnknown, time.Now().UTC()); err != nil {
		logger.L.Error().Err(err).Uint("schedule_id", record.ID).Msg("disk_self_test_run_update_failed")
	}
	event := newScheduledSelfTestEvent(*record, condition, "warning", fmt.Sprintf("Disk %s scheduled S.M.A.R.T self-test result is unknown", record.Device), message)
	if err := s.saveScheduledSelfTestWithEvent(ctx, record, &event); err != nil {
		return err
	}
	return nil
}

func (s *Service) failScheduledSelfTest(ctx context.Context, record *models.DiskSmartSelfTestSchedule, condition string, failure error) error {
	record.LastStatus = smartSelfTestScheduleFailed
	record.LastError = failure.Error()
	record.QueuedAt = nil
	record.QueueUpdatedAt = nil
	record.ProgressPct = -1
	record.ProgressKnown = false
	record.RunningObserved = false
	record.TimeoutAbortAttempted = false
	if err := s.finishScheduledSelfTestRun(context.WithoutCancel(ctx), *record, nil, smartSelfTestRunFailed, time.Now().UTC()); err != nil {
		logger.L.Error().Err(err).Uint("schedule_id", record.ID).Msg("disk_self_test_run_update_failed")
	}
	event := newScheduledSelfTestEvent(*record, condition, "warning", fmt.Sprintf("Disk %s scheduled S.M.A.R.T self-test could not start", record.Device), failure.Error())
	if err := s.saveScheduledSelfTestWithEvent(ctx, record, &event); err != nil {
		return err
	}
	return nil
}

func newScheduledSelfTestEvent(schedule models.DiskSmartSelfTestSchedule, condition, severity, title, body string) models.DiskSmartSelfTestEvent {
	return models.DiskSmartSelfTestEvent{
		EventKey:   scheduledSelfTestEventKey(schedule, condition),
		RunKey:     scheduledSelfTestRunKey(schedule),
		Source:     smartSelfTestRunSourceScheduled,
		ScheduleID: schedule.ID,
		DiskKey:    schedule.DiskKey,
		Device:     schedule.Device,
		TestType:   schedule.TestType,
		Condition:  condition,
		Severity:   severity,
		Title:      title,
		Body:       body,
	}
}

func scheduledSelfTestEventKey(schedule models.DiskSmartSelfTestSchedule, condition string) string {
	return scheduledSelfTestEventPrefix(schedule) + condition
}

func scheduledSelfTestEventPrefix(schedule models.DiskSmartSelfTestSchedule) string {
	anchor := schedule.OccurrenceKey
	if anchor == "" && schedule.LastRunAt != nil {
		anchor = schedule.LastRunAt.UTC().Format(time.RFC3339Nano)
	} else if anchor == "" && schedule.NextRunAt != nil {
		anchor = schedule.NextRunAt.UTC().Format(time.RFC3339Nano)
	} else if anchor == "" && !schedule.UpdatedAt.IsZero() {
		anchor = schedule.UpdatedAt.UTC().Format(time.RFC3339Nano)
	} else if anchor == "" {
		anchor = "none"
	}
	return fmt.Sprintf("%d|%s|", schedule.ID, anchor)
}

func (s *Service) saveScheduledSelfTestWithEvent(ctx context.Context, schedule *models.DiskSmartSelfTestSchedule, event *models.DiskSmartSelfTestEvent) error {
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(schedule).Error; err != nil {
			return err
		}
		if event == nil {
			return nil
		}
		event.ScheduleID = schedule.ID
		event.EventKey = scheduledSelfTestEventKey(*schedule, event.Condition)
		event.DiskKey = schedule.DiskKey
		event.Device = schedule.Device
		event.TestType = schedule.TestType
		eventPrefix := scheduledSelfTestEventPrefix(*schedule)
		eventPattern := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(eventPrefix) + "%"
		if err := tx.Where("schedule_id = ? AND event_key LIKE ? ESCAPE '\\' AND event_key <> ? AND claimed_at IS NULL", schedule.ID, eventPattern, event.EventKey).Delete(&models.DiskSmartSelfTestEvent{}).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := tx.Model(&models.DiskSmartSelfTestEvent{}).
			Where("schedule_id = ? AND event_key LIKE ? ESCAPE '\\' AND event_key <> ? AND claimed_at IS NOT NULL AND superseded_at IS NULL", schedule.ID, eventPattern, event.EventKey).
			Update("superseded_at", now).Error; err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(event).Error
	})
	if err == nil && event != nil {
		s.signalSelfTestEventRelay()
	}
	return err
}

func (s *Service) signalSelfTestEventRelay() {
	if s == nil {
		return
	}
	s.selfTestEventRelayVersion.Add(1)
	s.selfTestEventRelayActive.Store(true)
}

func (s *Service) hasPendingSelfTestEvents(ctx context.Context) (bool, error) {
	if s == nil || s.DB == nil {
		return false, nil
	}
	var events []models.DiskSmartSelfTestEvent
	result := s.DB.WithContext(ctx).
		Select("id").
		Where("dead_lettered_at IS NULL").
		Limit(1).
		Find(&events)
	return len(events) != 0, result.Error
}

func (s *Service) refreshSelfTestEventRelay(ctx context.Context, version uint64) {
	pending, err := s.hasPendingSelfTestEvents(ctx)
	if err != nil {
		logger.L.Error().Err(err).Msg("disk_self_test_notification_lookup_failed")
		return
	}
	if !pending && version == s.selfTestEventRelayVersion.Load() {
		s.selfTestEventRelayActive.Store(false)
	}
}

func (s *Service) runSelfTestEventRelayBatch(ctx context.Context, now time.Time) error {
	now = now.UTC()
	claimBefore := now.Add(-smartSelfTestEventClaimLease)
	if err := s.DB.WithContext(ctx).
		Where("superseded_at IS NOT NULL AND (claimed_at IS NULL OR claimed_at < ?)", claimBefore).
		Delete(&models.DiskSmartSelfTestEvent{}).Error; err != nil {
		return err
	}
	var events []models.DiskSmartSelfTestEvent
	if err := s.DB.WithContext(ctx).
		Where("superseded_at IS NULL AND dead_lettered_at IS NULL").
		Where("(claimed_at IS NULL OR claimed_at < ?) AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", claimBefore, now).
		Order("id ASC").
		Limit(smartSelfTestEventBatchSize).
		Find(&events).Error; err != nil {
		return err
	}
	var firstErr error
	for i := range events {
		token := utils.GenerateRandomUUID()
		claim := s.DB.WithContext(ctx).Model(&models.DiskSmartSelfTestEvent{}).
			Where("id = ? AND superseded_at IS NULL AND dead_lettered_at IS NULL", events[i].ID).
			Where("(claimed_at IS NULL OR claimed_at < ?) AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", claimBefore, now).
			Updates(map[string]any{"claim_token": token, "claimed_at": now})
		if claim.Error != nil {
			if firstErr == nil {
				firstErr = claim.Error
			}
			continue
		}
		if claim.RowsAffected == 0 {
			continue
		}
		job := smartSelfTestEventJob{EventID: events[i].ID, ClaimToken: token}
		var err error
		if s.selfTestEventEnqueue != nil {
			err = s.selfTestEventEnqueue(ctx, job)
		} else {
			err = db.EnqueueJSON(ctx, smartSelfTestEventJobName, job)
		}
		if err == nil {
			continue
		}
		releaseErr := s.releaseSelfTestEventClaim(context.WithoutCancel(ctx), events[i].ID, token)
		if firstErr == nil {
			firstErr = errors.Join(err, releaseErr)
		}
	}
	return firstErr
}

func (s *Service) handleSelfTestEventJob(ctx context.Context, job smartSelfTestEventJob) error {
	if s == nil || s.DB == nil || job.EventID == 0 || job.ClaimToken == "" {
		return nil
	}
	if !s.selfTestJobsReady.Load() {
		if err := s.releaseSelfTestEventClaim(context.WithoutCancel(ctx), job.EventID, job.ClaimToken); err != nil {
			logger.L.Error().Err(err).Uint("event_id", job.EventID).Msg("disk_self_test_notification_release_failed")
		}
		return nil
	}
	current := time.Now().UTC()
	workerToken := utils.GenerateRandomUUID()
	ownership := s.DB.WithContext(ctx).Model(&models.DiskSmartSelfTestEvent{}).
		Where("id = ? AND claim_token = ? AND claimed_at IS NOT NULL AND claimed_at > ?", job.EventID, job.ClaimToken, current.Add(-smartSelfTestEventClaimLease)).
		Updates(map[string]any{"claim_token": workerToken, "claimed_at": current})
	if ownership.Error != nil {
		if !errors.Is(ownership.Error, context.Canceled) {
			logger.L.Error().Err(ownership.Error).Uint("event_id", job.EventID).Msg("disk_self_test_notification_claim_failed")
		}
		return nil
	}
	if ownership.RowsAffected != 1 {
		return nil
	}
	var event models.DiskSmartSelfTestEvent
	if err := s.DB.WithContext(ctx).Where("id = ? AND claim_token = ?", job.EventID, workerToken).First(&event).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) && !errors.Is(err, context.Canceled) {
			logger.L.Error().Err(err).Uint("event_id", job.EventID).Msg("disk_self_test_notification_load_failed")
		}
		return nil
	}
	if err := s.deliverClaimedSelfTestEvent(ctx, &event, workerToken, current); err != nil && !errors.Is(err, context.Canceled) {
		logger.L.Error().Err(err).Uint("event_id", event.ID).Msg("disk_self_test_notification_delivery_failed")
	}
	return nil
}

func (s *Service) releaseSelfTestEventClaim(ctx context.Context, id uint, token string) error {
	if id == 0 || token == "" {
		return nil
	}
	return s.DB.WithContext(ctx).Model(&models.DiskSmartSelfTestEvent{}).
		Where("id = ? AND claim_token = ?", id, token).
		Updates(map[string]any{"claim_token": "", "claimed_at": nil}).Error
}

func (s *Service) deliverClaimedSelfTestEvent(ctx context.Context, event *models.DiskSmartSelfTestEvent, token string, now time.Time) error {
	if event == nil || event.ID == 0 || token == "" || event.ClaimToken != token {
		return nil
	}
	if event.SupersededAt != nil {
		return s.deleteClaimedSelfTestEvent(ctx, event, token)
	}
	blocked, retryAt, err := s.selfTestEventBlockedByEarlierLifecycle(ctx, event, now)
	if err != nil {
		return err
	}
	if blocked {
		return s.releaseClaimedSelfTestEventForOrdering(ctx, event, token, retryAt)
	}
	target := strings.TrimSpace(strings.ToLower(event.DiskKey))
	if target == "" {
		target = strings.TrimSpace(strings.ToLower(event.Device))
	}
	source := event.Source
	if source == "" {
		source = smartSelfTestRunSourceScheduled
		if event.ScheduleID == 0 {
			source = smartSelfTestRunSourceManual
		}
	}
	runKey := event.RunKey
	if runKey == "" && event.ScheduleID == 0 {
		suffix := "|" + event.Condition
		if strings.HasSuffix(event.EventKey, suffix) {
			runKey = strings.TrimSuffix(event.EventKey, suffix)
		}
	}
	input := notifier.EventInput{
		Kind:        notifier.KindForDiskSmart(notifier.DiskSmartSelfTestKindPrefix, target),
		Title:       event.Title,
		Body:        event.Body,
		Severity:    event.Severity,
		Source:      "system.disk.smart.selftest",
		Fingerprint: fmt.Sprintf("%s|selftest", target),
		Metadata: map[string]string{
			"condition":   event.Condition,
			"device":      event.Device,
			"disk_key":    target,
			"run_key":     runKey,
			"source":      source,
			"schedule_id": strconv.FormatUint(uint64(event.ScheduleID), 10),
			"test_type":   event.TestType,
		},
	}
	plan, err := decodeSelfTestEventTargets(event.DeliveryPlan)
	if err == nil && event.DeliveryPlan == "" {
		plan, err = notifier.DeliveryTargets(ctx, input)
		if err == nil {
			event.DeliveryPlan, err = encodeSelfTestEventTargets(plan)
		}
		if err == nil {
			err = s.updateClaimedSelfTestEvent(ctx, event.ID, token, map[string]any{"delivery_plan": event.DeliveryPlan})
		}
	}
	delivered, decodeErr := decodeSelfTestEventTargets(event.DeliveredTargets)
	if err == nil {
		err = decodeErr
	}
	var deliveryErr error
	deliveredSet := make(map[string]struct{}, len(delivered))
	for _, deliveredTarget := range delivered {
		deliveredSet[deliveredTarget] = struct{}{}
	}
	for _, deliveryTarget := range plan {
		if err != nil {
			break
		}
		if _, ok := deliveredSet[deliveryTarget]; ok {
			continue
		}
		active, checkErr := s.renewSelfTestEventClaim(ctx, event.ID, token, time.Now().UTC())
		if checkErr != nil {
			err = checkErr
			break
		}
		if !active {
			return s.deleteClaimedSelfTestEvent(ctx, event, token)
		}
		if _, emitErr := notifier.EmitTarget(ctx, input, deliveryTarget); emitErr != nil {
			if deliveryErr == nil {
				deliveryErr = emitErr
			}
			continue
		}
		delivered = append(delivered, deliveryTarget)
		deliveredSet[deliveryTarget] = struct{}{}
		event.DeliveredTargets, err = encodeSelfTestEventTargets(delivered)
		if err == nil {
			err = s.updateClaimedSelfTestEvent(ctx, event.ID, token, map[string]any{"delivered_targets": event.DeliveredTargets})
		}
	}
	if err == nil {
		err = deliveryErr
	}
	if err == nil && len(deliveredSet) == len(plan) {
		return s.deleteClaimedSelfTestEvent(ctx, event, token)
	}
	event.AttemptCount++
	retryBase := time.Now().UTC()
	nextAttempt := retryBase.Add(selfTestEventRetryDelay(event.ID, event.AttemptCount))
	deliveryError := "notification_delivery_incomplete"
	if err != nil {
		deliveryError = err.Error()
	}
	deadLetter := event.AttemptCount >= smartSelfTestEventMaxAttempts || !event.CreatedAt.IsZero() && retryBase.Sub(event.CreatedAt) >= smartSelfTestEventMaxAge
	var deadLetteredAt *time.Time
	if deadLetter {
		deadLetteredAt = &retryBase
	}
	release := s.DB.WithContext(context.WithoutCancel(ctx)).Model(&models.DiskSmartSelfTestEvent{}).
		Where("id = ? AND claim_token = ?", event.ID, token).
		Updates(map[string]any{
			"delivered_targets": event.DeliveredTargets,
			"attempt_count":     event.AttemptCount,
			"next_attempt_at":   nextAttempt,
			"delivery_error":    deliveryError,
			"dead_lettered_at":  deadLetteredAt,
			"claim_token":       "",
			"claimed_at":        nil,
		})
	releaseErr := release.Error
	if releaseErr == nil && release.RowsAffected != 1 {
		releaseErr = errSelfTestEventClaimLost
	}
	if errors.Is(err, errSelfTestEventClaimLost) || errors.Is(releaseErr, errSelfTestEventClaimLost) {
		return nil
	}
	if releaseErr != nil {
		logger.L.Error().Err(releaseErr).Str("event_key", event.EventKey).Msg("disk_self_test_notification_release_failed")
	}
	if deadLetter {
		logger.L.Error().Err(err).Str("event_key", event.EventKey).Msg("disk_self_test_notification_dead_lettered")
	} else if err != nil && !errors.Is(err, notifier.ErrEmitterNotConfigured) {
		logger.L.Error().Err(err).Str("event_key", event.EventKey).Msg("disk_self_test_notification_failed")
	}
	if err != nil {
		return err
	}
	return errors.New(deliveryError)
}

func (s *Service) selfTestEventBlockedByEarlierLifecycle(ctx context.Context, event *models.DiskSmartSelfTestEvent, now time.Time) (bool, time.Time, error) {
	if event.ScheduleID == 0 {
		return false, time.Time{}, nil
	}
	var earlier []models.DiskSmartSelfTestEvent
	result := s.DB.WithContext(ctx).
		Select("id", "next_attempt_at").
		Where("schedule_id = ? AND id < ? AND dead_lettered_at IS NULL", event.ScheduleID, event.ID).
		Order("id ASC").
		Limit(1).
		Find(&earlier)
	if result.Error != nil {
		return false, time.Time{}, result.Error
	}
	if len(earlier) == 0 {
		return false, time.Time{}, nil
	}
	retryAt := now.Add(smartSelfTestEventDispatchInterval)
	if earlier[0].NextAttemptAt != nil && earlier[0].NextAttemptAt.After(retryAt) {
		retryAt = *earlier[0].NextAttemptAt
	}
	return true, retryAt, nil
}

func (s *Service) releaseClaimedSelfTestEventForOrdering(ctx context.Context, event *models.DiskSmartSelfTestEvent, token string, retryAt time.Time) error {
	result := s.DB.WithContext(context.WithoutCancel(ctx)).Model(&models.DiskSmartSelfTestEvent{}).
		Where("id = ? AND claim_token = ?", event.ID, token).
		Updates(map[string]any{"claim_token": "", "claimed_at": nil, "next_attempt_at": retryAt})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return nil
	}
	return nil
}

func (s *Service) renewSelfTestEventClaim(ctx context.Context, id uint, token string, now time.Time) (bool, error) {
	result := s.DB.WithContext(ctx).Model(&models.DiskSmartSelfTestEvent{}).
		Where("id = ? AND claim_token = ? AND superseded_at IS NULL", id, token).
		Update("claimed_at", now)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func (s *Service) updateClaimedSelfTestEvent(ctx context.Context, id uint, token string, values map[string]any) error {
	result := s.DB.WithContext(ctx).Model(&models.DiskSmartSelfTestEvent{}).
		Where("id = ? AND claim_token = ?", id, token).
		Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errSelfTestEventClaimLost
	}
	return nil
}

func (s *Service) deleteClaimedSelfTestEvent(ctx context.Context, event *models.DiskSmartSelfTestEvent, token string) error {
	result := s.DB.WithContext(context.WithoutCancel(ctx)).Where("id = ? AND claim_token = ?", event.ID, token).Delete(&models.DiskSmartSelfTestEvent{})
	err := result.Error
	if err != nil {
		logger.L.Error().Err(err).Str("event_key", event.EventKey).Msg("disk_self_test_notification_cleanup_failed")
	}
	return err
}

func decodeSelfTestEventTargets(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return []string{}, nil
	}
	var targets []string
	if err := json.Unmarshal([]byte(value), &targets); err != nil {
		return nil, err
	}
	return normalizeSelfTestEventTargets(targets)
}

func normalizeSelfTestEventTargets(targets []string) ([]string, error) {
	seen := make(map[string]struct{}, len(targets))
	normalized := make([]string, 0, len(targets))
	for _, target := range targets {
		target = strings.TrimSpace(strings.ToLower(target))
		if target == "" {
			return nil, fmt.Errorf("invalid_notification_delivery_target")
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		normalized = append(normalized, target)
	}
	return normalized, nil
}

func encodeSelfTestEventTargets(targets []string) (string, error) {
	normalized, err := normalizeSelfTestEventTargets(targets)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func selfTestEventRetryDelay(id uint, attempt uint) time.Duration {
	shift := attempt - 1
	if shift > 7 {
		shift = 7
	}
	delay := 30 * time.Second * time.Duration(uint64(1)<<shift)
	if delay > time.Hour {
		delay = time.Hour
	}
	jitterWindow := delay / 4
	jitter := time.Duration((uint64(id)*1103515245 + uint64(attempt)*12345) % uint64(jitterWindow+1))
	if delay+jitter > time.Hour {
		return time.Hour
	}
	return delay + jitter
}
