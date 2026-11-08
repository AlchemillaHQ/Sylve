// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package hostupdates

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/pkgmanager"
	"github.com/alchemillahq/sylve/pkg/sylveupdate"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Request struct {
	PlanID          string   `json:"planId"`
	Packages        []string `json:"packages"`
	Acknowledge     bool     `json:"acknowledge"`
	BootEnvironment bool     `json:"bootEnvironment"`
}

type Job struct {
	models.HostUpdateOperation
	Plan *models.HostUpdatePlan `json:"plan"`
}

type View struct {
	models.HostUpdateState
	Ready                    bool                  `json:"ready"`
	Pkgbase                  bool                  `json:"pkgbase"`
	Reason                   string                `json:"reason"`
	UpdatesChecked           bool                  `json:"updatesChecked"`
	AuditStale               bool                  `json:"auditStale"`
	BootEnvironmentSupported bool                  `json:"bootEnvironmentSupported"`
	Sylve                    sylveupdate.Installed `json:"sylve"`
	ReleaseAvailable         bool                  `json:"releaseAvailable"`
	ActiveJob                *Job                  `json:"activeJob"`
}

type Monitoring struct {
	Enabled       bool   `json:"enabled"`
	IntervalHours int    `json:"intervalHours"`
	CronExpr      string `json:"cronExpr"`
}

type Service struct {
	DB               *gorm.DB
	Telemetry        *gorm.DB
	context          context.Context
	directory        string
	version          string
	commit           string
	builtAt          string
	executable       string
	executableInfo   os.FileInfo
	runningDigest    string
	bootEnvironments bool
	restart          func()
	mu               sync.Mutex
	releaseMu        sync.Mutex
	releaseAttemptAt time.Time
	readMu           sync.Mutex
	readID           uint
	cancelRead       context.CancelCauseFunc
	client           func(io.Writer) *pkgmanager.Client
	probe            func() error
	command          func(context.Context, string, ...string) (string, error)
	releases         *sylveupdate.Client
	ownership        func(context.Context, *pkgmanager.Client, string) (bool, string, error)
	installed        func(context.Context, models.HostUpdateState) sylveupdate.Installed
}

func NewService(ctx context.Context, database, telemetry *gorm.DB, dataPath, version, commit, builtAt string, restart func()) (*Service, error) {
	directory, err := filepath.Abs(filepath.Join(dataPath, "host-updates"))
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	if err = trustedDirectory(directory); err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(executable)
	if err != nil {
		return nil, err
	}
	digest, err := sylveupdate.FileDigest(executable)
	if err != nil {
		return nil, err
	}
	s := &Service{
		DB:             database,
		Telemetry:      telemetry,
		context:        ctx,
		directory:      directory,
		version:        version,
		commit:         commit,
		executable:     executable,
		executableInfo: info,
		runningDigest:  digest,
		restart:        restart,
		client:         pkgmanager.NewHostClient,
		probe:          pkgmanager.ProbeHost,
		releases:       sylveupdate.NewClient(),
		command:        runCommand,
		ownership:      packageOwnership,
	}
	if timestamp, err := sylveupdate.ParseBuildTime(builtAt); err == nil {
		s.builtAt = timestamp.Format(time.RFC3339Nano)
	}
	_, err = s.command(ctx, "/sbin/bectl", "check")
	s.bootEnvironments = err == nil
	s.installed = s.inspectInstalled
	if _, err = s.state(); err != nil {
		return nil, err
	}
	return s, nil
}

func runCommand(ctx context.Context, binary string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = pkgmanager.Environment()
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func (s *Service) state() (models.HostUpdateState, error) {
	state := models.HostUpdateState{}
	err := s.DB.First(&state, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		state = models.HostUpdateState{
			ID:             1,
			Monitoring:     true,
			IntervalHours:  24,
			ReleaseChannel: "rolling",
			Inventory:      []pkgmanager.Package{},
			Audit:          pkgmanager.Audit{Packages: []pkgmanager.AuditPackage{}},
			Available:      pkgmanager.Plan{Changes: []pkgmanager.Change{}},
		}
		err = s.DB.Create(&state).Error
	}
	if state.Inventory == nil {
		state.Inventory = []pkgmanager.Package{}
	}
	if state.Audit.Packages == nil {
		state.Audit.Packages = []pkgmanager.AuditPackage{}
	}
	if state.Available.Changes == nil {
		state.Available.Changes = []pkgmanager.Change{}
	}
	return state, err
}

func (s *Service) installedBuildTime(state models.HostUpdateState) string {
	timestamps := []string{s.builtAt}
	if state.InstalledDigest == s.runningDigest {
		timestamps = append(timestamps, state.InstalledBuiltAt)
	}
	if state.Release != nil && state.Release.SHA256 == s.runningDigest {
		timestamps = append(timestamps, state.Release.BuiltAt)
	}
	for _, value := range timestamps {
		if timestamp, err := sylveupdate.ParseBuildTime(value); err == nil {
			return timestamp.Format(time.RFC3339Nano)
		}
	}
	var plan models.HostUpdatePlan
	err := s.DB.Joins("JOIN host_update_operations AS operation ON operation.plan_id = host_update_plans.id").
		Where("operation.action = ? AND operation.status = ?", "sylve-apply", "success").
		Where("json_extract(host_update_plans.release, '$.sha256') = ?", s.runningDigest).
		Order("operation.id DESC").Limit(1).Find(&plan).Error
	if err == nil && plan.Release != nil {
		if timestamp, err := sylveupdate.ParseBuildTime(plan.Release.BuiltAt); err == nil {
			return timestamp.Format(time.RFC3339Nano)
		}
	}
	return ""
}

func (s *Service) inspectInstalled(ctx context.Context, state models.HostUpdateState) sylveupdate.Installed {
	installed := sylveupdate.Installed{Mode: "unknown", Version: s.version, Commit: s.commit, BuiltAt: s.installedBuildTime(state), Channel: "rolling", SHA256: s.runningDigest, Path: s.executable}
	if state.InstalledDigest == s.runningDigest && state.InstalledChannel != "" {
		installed.Channel = state.InstalledChannel
	}
	info, err := os.Stat(s.executable)
	if err != nil || !os.SameFile(info, s.executableInfo) || info.Size() != s.executableInfo.Size() || !info.ModTime().Equal(s.executableInfo.ModTime()) {
		installed.Reason = "sylve_executable_changed_restart_required"
		return installed
	}
	packaged, owner, err := s.ownership(ctx, s.client(nil), s.executable)
	if err != nil {
		installed.Reason = "sylve_package_ownership_unknown"
		return installed
	}
	if owner != "" {
		if owner != "sysutils/sylve" {
			installed.Reason = "sylve_package_ownership_conflict"
			return installed
		}
		installed.Mode = "package"
		installed.Channel = "package"
		installed.Reason = "sylve_package_managed"
		return installed
	}
	if packaged {
		installed.Mode = "mixed"
		installed.Reason = "sylve_package_ownership_conflict"
		return installed
	}
	if s.executable != "/usr/local/sbin/sylve" {
		installed.Reason = "sylve_standalone_path_required"
		return installed
	}
	if err := trustedExecutable(s.executable); err != nil {
		installed.Reason = "sylve_executable_not_trusted"
		return installed
	}
	installed.Mode = "standalone"
	return installed
}

func (s *Service) Get(ctx context.Context) (View, error) {
	state, err := s.state()
	if err != nil {
		return View{}, err
	}
	view := View{HostUpdateState: state, AuditStale: true, BootEnvironmentSupported: s.bootEnvironments}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	view.Sylve = s.installed(ctx, state)
	view.ReleaseAvailable = releaseAvailable(state, view.Sylve)
	var active models.HostUpdateOperation
	if err = s.DB.Where("active IS NOT NULL").Limit(1).Find(&active).Error; err != nil {
		return view, err
	}
	if active.ID != 0 {
		job, err := s.Job(ctx, active.ID)
		if err != nil {
			return view, err
		}
		view.ActiveJob = &job
		view.Reason = "host_update_in_progress"
		view.Pkgbase = pkgmanager.HasPkgbase(state.Inventory)
	} else if err = s.probe(); err != nil {
		view.Reason = code(err)
	} else {
		client := s.client(nil)
		inventory, err := client.Inventory(ctx)
		if err != nil {
			view.Reason = code(err)
		} else {
			view.Inventory = inventory
			view.Pkgbase = pkgmanager.HasPkgbase(inventory)
			_, err = client.InspectHost(ctx, inventory)
			if err != nil {
				view.Reason = code(err)
			} else {
				view.Ready = true
			}
		}
	}
	// While a job runs, the last stored results still describe the stored inventory.
	current := view.Ready || view.ActiveJob != nil
	hash := pkgmanager.InventoryDigest(view.Inventory)
	now := time.Now().UTC()
	view.UpdatesChecked = current && state.CheckInventoryHash == hash && state.CheckError == "" && monitoringResultFresh(state, state.CheckedAt, now)
	view.AuditStale = !current || state.AuditInventoryHash != pkgmanager.AuditInventoryDigest(view.Inventory) || state.AuditError != "" || !monitoringResultFresh(state, state.AuditAt, now)
	findings := map[string]pkgmanager.AuditPackage{}
	for _, pkg := range state.Audit.Packages {
		findings[pkg.Name] = pkg
	}
	available := map[string]string{}
	if view.UpdatesChecked {
		for _, change := range state.Available.Changes {
			available[change.Name] = change.To
		}
	}
	for i := range view.Inventory {
		pkg := &view.Inventory[i]
		pkg.Available = available[pkg.Name]
		pkg.Advisories = []pkgmanager.Advisory{}
		if finding, ok := findings[pkg.Name]; ok && finding.Version == pkg.Version {
			pkg.Advisories = finding.Advisories
		}
	}
	return view, nil
}

type HistoryPage struct {
	LastPage int                          `json:"last_page"`
	Data     []models.HostUpdateOperation `json:"data"`
}

func (s *Service) History(ctx context.Context, page, size int, sortField, sortDir string) (HistoryPage, error) {
	page, size = max(page, 1), min(max(size, 1), 500)
	column, ok := map[string]string{"id": "id", "action": "action", "status": "status", "actor": "actor", "createdAt": "created_at", "finishedAt": "finished_at"}[sortField]
	if !ok {
		column = "id"
	}
	direction := "DESC"
	if sortDir == "asc" {
		direction = "ASC"
	}
	var total int64
	if err := s.DB.WithContext(ctx).Model(&models.HostUpdateOperation{}).Where("active IS NULL").Count(&total).Error; err != nil {
		return HistoryPage{}, err
	}
	history := HistoryPage{LastPage: max(1, int((total+int64(size)-1)/int64(size))), Data: []models.HostUpdateOperation{}}
	err := s.DB.WithContext(ctx).Where("active IS NULL").Order(column + " " + direction).Offset((page - 1) * size).Limit(size).Find(&history.Data).Error
	return history, err
}

func code(err error) string {
	value, _, _ := strings.Cut(err.Error(), ":")
	return strings.TrimSpace(value)
}

func requireIdle(database *gorm.DB) error {
	var active int64
	if err := database.Model(&models.HostUpdateOperation{}).Where("active IS NOT NULL").Count(&active).Error; err != nil {
		return err
	}
	if active != 0 {
		return fmt.Errorf("host_update_in_progress")
	}
	return nil
}

func (s *Service) SetMonitoring(ctx context.Context, input Monitoring) (models.HostUpdateState, error) {
	input.CronExpr = strings.Join(strings.Fields(input.CronExpr), " ")
	now := time.Now().UTC()
	scheduled, err := nextMonitoringRun(models.HostUpdateState{IntervalHours: input.IntervalHours, CronExpr: input.CronExpr}, now)
	if err != nil {
		return models.HostUpdateState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := requireIdle(s.DB); err != nil {
		return models.HostUpdateState{}, err
	}
	state, err := s.state()
	if err != nil {
		return state, err
	}
	var next *time.Time
	if input.Enabled {
		next = &now
		if input.CronExpr != "" {
			next = &scheduled
		}
	}
	if input.CronExpr != "" {
		input.IntervalHours = state.IntervalHours
	}
	updates := map[string]any{"monitoring": input.Enabled, "interval_hours": input.IntervalHours, "cron_expr": input.CronExpr, "next_check_at": next}
	err = s.DB.WithContext(ctx).Model(&state).Updates(updates).Error
	if err != nil {
		return state, err
	}
	return s.state()
}

func (s *Service) SetReleaseChannel(ctx context.Context, channel string) (models.HostUpdateState, error) {
	if channel != "rolling" && channel != "tagged" {
		return models.HostUpdateState{}, fmt.Errorf("invalid_release_channel")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseMu.Lock()
	defer s.releaseMu.Unlock()
	if err := requireIdle(s.DB); err != nil {
		return models.HostUpdateState{}, err
	}
	state, err := s.state()
	if err != nil || state.ReleaseChannel == channel {
		return state, err
	}
	err = s.DB.WithContext(ctx).Model(&state).Updates(map[string]any{
		"release_channel": channel, "release": nil, "release_checked_at": nil, "release_error": "",
	}).Error
	if err != nil {
		return state, err
	}
	s.releaseAttemptAt = time.Time{}
	return s.state()
}

func (s *Service) Start(ctx context.Context, action string, input Request, actor string) (Job, error) {
	if !slices.Contains([]string{"check", "audit", "packages-preview", "packages-apply", "sylve-preview", "sylve-apply"}, action) {
		return Job{}, fmt.Errorf("invalid_update_action")
	}
	if len(input.Packages) > 512 {
		return Job{}, fmt.Errorf("too_many_packages")
	}
	for _, name := range input.Packages {
		if !pkgmanager.ValidName(name) {
			return Job{}, fmt.Errorf("invalid_package_name")
		}
	}
	applying := strings.HasSuffix(action, "-apply")
	if !applying && input.PlanID != "" {
		return Job{}, fmt.Errorf("invalid_host_update_request")
	}
	if applying && !input.Acknowledge {
		return Job{}, fmt.Errorf("host_update_acknowledgement_required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var plan models.HostUpdatePlan
	if applying {
		if err := s.DB.First(&plan, "id = ?", input.PlanID).Error; err != nil {
			return Job{}, fmt.Errorf("host_update_plan_not_found")
		}
		if plan.Kind+"-apply" != action {
			return Job{}, fmt.Errorf("host_update_plan_kind_changed")
		}
		if plan.AppliedOperationID != nil {
			job, err := s.Job(ctx, *plan.AppliedOperationID)
			if err != nil {
				return job, err
			}
			ref, err := db.PrepareAsyncAuditRecord(s.Telemetry, ctx, "host_update", job.ID, "host-update:"+job.UUID)
			if err == nil && job.FinishedAt != nil {
				err = db.FinalizeAsyncAuditOperation(s.Telemetry, ref, job.Status, job.Error, nil)
			}
			return job, err
		}
		if time.Now().After(plan.ExpiresAt) {
			return Job{}, fmt.Errorf("host_update_plan_stale")
		}
		if plan.Kind == "packages" && len(plan.Transaction.Changes) == 0 {
			return Job{}, fmt.Errorf("no_package_changes")
		}
	}
	channel := ""
	if strings.HasPrefix(action, "sylve-") {
		state, err := s.state()
		if err != nil {
			return Job{}, err
		}
		channel = state.ReleaseChannel
		if applying && (plan.Release == nil || plan.Release.Channel != channel) {
			return Job{}, fmt.Errorf("host_update_plan_stale")
		}
	}
	one := uint(1)
	op := models.HostUpdateOperation{
		UUID:            uuid.NewString(),
		Active:          &one,
		Action:          action,
		Status:          "queued",
		Phase:           "queued",
		PlanID:          input.PlanID,
		Channel:         channel,
		Actor:           actor,
		BootEnvironment: input.BootEnvironment,
	}
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireIdle(tx); err != nil {
			return err
		}
		if err := tx.Create(&op).Error; err != nil {
			return err
		}
		if applying {
			result := tx.Model(&models.HostUpdatePlan{}).Where("id = ? AND applied_operation_id IS NULL AND expires_at > ?", plan.ID, time.Now().UTC()).Update("applied_operation_id", op.ID)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("host_update_plan_already_used")
			}
		}
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	if _, err = db.PrepareAsyncAuditRecord(s.Telemetry, ctx, "host_update", op.ID, "host-update:"+op.UUID); err != nil {
		s.finish(op, err, "")
		return Job{}, err
	}
	go s.execute(op, input.Packages)
	return Job{HostUpdateOperation: op}, nil
}

func (s *Service) Job(ctx context.Context, id uint) (Job, error) {
	job := Job{}
	if err := s.DB.WithContext(ctx).First(&job.HostUpdateOperation, id).Error; err != nil {
		return job, err
	}
	if job.PlanID != "" {
		var plan models.HostUpdatePlan
		if err := s.DB.First(&plan, "id = ?", job.PlanID).Error; err != nil {
			return job, err
		}
		job.Plan = &plan
	}
	return job, nil
}

func (s *Service) operationDirectory(op models.HostUpdateOperation) (string, error) {
	if _, err := uuid.Parse(op.UUID); err != nil {
		return "", fmt.Errorf("invalid_update_operation")
	}
	dir := filepath.Join(s.directory, op.UUID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if err := trustedDirectory(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func (s *Service) Logs(ctx context.Context, id uint, offset int64) (string, int64, error) {
	job, err := s.Job(ctx, id)
	if err != nil {
		return "", offset, err
	}
	dir, err := s.operationDirectory(job.HostUpdateOperation)
	if err != nil {
		return "", offset, err
	}
	f, err := os.Open(filepath.Join(dir, "output.log"))
	if os.IsNotExist(err) {
		return "", offset, nil
	}
	if err != nil {
		return "", offset, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", offset, err
	}
	if offset < 0 || offset > info.Size() {
		return "", offset, fmt.Errorf("invalid_log_offset")
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return "", offset, err
	}
	data, err := io.ReadAll(io.LimitReader(f, 64<<10))
	return pkgmanager.Redact(string(data)), offset + int64(len(data)), err
}

func (s *Service) phase(op *models.HostUpdateOperation, phase string) error {
	op.Phase, op.Status = phase, "running"
	return s.DB.Model(op).Updates(map[string]any{"phase": phase, "status": "running"}).Error
}

const rebootNotice = " Host reboot recommended."

func (s *Service) finish(op models.HostUpdateOperation, cause error, message string) {
	now := time.Now().UTC()
	status, phase, failure := "success", "completed", ""
	if cause != nil {
		status, phase, failure = "failed", "failed", pkgmanager.Redact(cause.Error())
	}
	finalize := func(tx *gorm.DB) error {
		updates := map[string]any{"active": nil, "status": status, "phase": phase, "error": failure, "finished_at": now}
		if message != "" {
			updates["result"] = message
		}
		result := tx.Model(&models.HostUpdateOperation{}).Where("id = ? AND active IS NOT NULL", op.ID).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 && strings.HasSuffix(op.Action, "-apply") {
			severity, title := "info", "Host update completed"
			if cause != nil {
				severity, title = "warning", "Host update failed"
			} else if strings.Contains(message, rebootNotice) {
				severity, title = "warning", "Host update completed, reboot recommended"
			}
			return s.event(tx, "operation", title, strings.TrimSpace(message+" "+failure), severity, op.ID, "history")
		}
		return nil
	}
	// A failed write would leave the operation active and block every later one
	// until Sylve restarts.
	err := s.DB.Transaction(finalize)
	for attempt := 1; err != nil && attempt < 3; attempt++ {
		time.Sleep(time.Duration(attempt) * time.Second)
		err = s.DB.Transaction(finalize)
	}
	if err != nil {
		logger.L.Error().Err(err).Msg("host_update_completion_failed")
		return
	}
	if err = db.FinalizeAsyncAuditOperation(s.Telemetry, db.AsyncAuditRef{OperationID: "host-update:" + op.UUID}, status, failure, nil); err != nil {
		logger.L.Error().Err(err).Msg("host_update_audit_completion_failed")
	}
}

func (s *Service) execute(op models.HostUpdateOperation, selection []string) {
	dir, err := s.operationDirectory(op)
	if err != nil {
		s.finish(op, err, "")
		return
	}
	file, err := os.OpenFile(filepath.Join(dir, "output.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		s.finish(op, err, "")
		return
	}
	defer file.Close()
	log := pkgmanager.NewLogWriter(file)
	defer log.Flush()
	if strings.HasSuffix(op.Action, "-apply") {
		message, err := s.apply(s.context, &op, log)
		if err != nil {
			_, _ = fmt.Fprintln(log, pkgmanager.Redact(err.Error()))
		} else if op.Phase == "restarting" {
			log.Flush()
			s.restart()
			return
		}
		s.finish(op, err, message)
		return
	}
	ctx, cancel := context.WithCancelCause(s.context)
	defer cancel(nil)
	s.readMu.Lock()
	s.readID, s.cancelRead = op.ID, cancel
	s.readMu.Unlock()
	defer func() {
		s.readMu.Lock()
		if s.readID == op.ID {
			s.readID, s.cancelRead = 0, nil
		}
		s.readMu.Unlock()
	}()
	ctx, stop := context.WithTimeout(ctx, 30*time.Minute)
	defer stop()
	err = s.readOperation(ctx, &op, selection, log)
	if err != nil && errors.Is(context.Cause(ctx), errCancelled) {
		err = errCancelled
	}
	if err != nil {
		_, _ = fmt.Fprintln(log, pkgmanager.Redact(err.Error()))
	}
	s.finish(op, err, "")
}

var errCancelled = errors.New("host_update_cancelled")

// Only read operations can be cancelled; killing pkg mid-install could leave
// the host half-upgraded.
func (s *Service) Cancel(ctx context.Context, id uint) (Job, error) {
	s.readMu.Lock()
	cancel := s.cancelRead
	if s.readID != id {
		cancel = nil
	}
	s.readMu.Unlock()
	if cancel == nil {
		return Job{}, fmt.Errorf("host_update_not_cancellable")
	}
	cancel(errCancelled)
	return s.Job(ctx, id)
}

func (s *Service) readOperation(ctx context.Context, op *models.HostUpdateOperation, selection []string, log io.Writer) error {
	state, err := s.state()
	if err != nil {
		return err
	}
	switch op.Action {
	case "sylve-preview":
		return s.releasePreview(ctx, op, state)
	case "check":
		return errors.Join(s.monitor(ctx, op, state, log), s.checkRelease(ctx))
	case "audit":
		return s.monitor(ctx, op, state, log)
	}
	client, inventory, err := s.inspect(ctx, op, state, log)
	if err != nil {
		return err
	}
	if len(selection) > 0 {
		seen := map[string]bool{}
		for _, name := range selection {
			index := slices.IndexFunc(inventory, func(pkg pkgmanager.Package) bool { return pkg.Name == name })
			if index < 0 || seen[name] || inventory[index].Locked {
				return fmt.Errorf("invalid_package_selection")
			}
			if strings.HasPrefix(inventory[index].Origin, "base/") || pkgmanager.Classify(inventory[index].Name, inventory[index].Origin) == pkgmanager.KindKernel {
				return fmt.Errorf("host_base_selection_requires_all")
			}
			seen[name] = true
		}
	}
	all := len(selection) == 0
	repos, repoHash, err := client.Repositories(ctx)
	if err != nil {
		return err
	}
	if err = s.refreshRepositories(ctx, op, client, inventory, repos, repoHash); err != nil {
		return err
	}
	if err = s.phase(op, "solving"); err != nil {
		return err
	}
	plan, err := client.HostPreview(ctx, repos, selection)
	if err != nil {
		return err
	}
	if err = client.ValidateInstalled(plan, inventory); err != nil {
		return err
	}
	selection = client.HostSelection
	if all {
		if err = s.storeAvailable(inventory, plan, op.ID); err != nil {
			return err
		}
	}
	if len(plan.Changes) == 0 {
		return s.DB.Model(op).Update("result", "No package changes available.").Error
	}
	catalogHash, err := client.CatalogDigest(ctx, repos)
	if err != nil {
		return err
	}
	stored := models.HostUpdatePlan{
		ID:             uuid.NewString(),
		Kind:           "packages",
		Selection:      selection,
		Transaction:    plan,
		Host:           *client.HostSystem,
		InventoryHash:  pkgmanager.InventoryDigest(inventory),
		RepositoryHash: repoHash,
		CatalogHash:    catalogHash,
		ExecutableHash: s.runningDigest,
		ExpiresAt:      time.Now().UTC().Add(10 * time.Minute),
	}
	if err = s.DB.Create(&stored).Error; err != nil {
		return err
	}
	op.PlanID = stored.ID
	return s.DB.Model(op).Update("plan_id", stored.ID).Error
}

func (s *Service) inspect(ctx context.Context, op *models.HostUpdateOperation, state models.HostUpdateState, log io.Writer) (*pkgmanager.Client, []pkgmanager.Package, error) {
	if err := s.phase(op, "reading_inventory"); err != nil {
		return nil, nil, err
	}
	client := s.client(log)
	if err := s.probe(); err != nil {
		return nil, nil, err
	}
	inventory, err := client.Inventory(ctx)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()
	if err = s.DB.Model(&state).Select("Inventory", "InventoryAt", "LastAttemptAt").Updates(&models.HostUpdateState{Inventory: inventory, InventoryAt: &now, LastAttemptAt: &now}).Error; err != nil {
		return nil, nil, err
	}
	info, err := client.InspectHost(ctx, inventory)
	if err != nil {
		return nil, nil, err
	}
	client.HostSystem = &info
	client.SylveVersion = s.version
	if s.installed(ctx, state).Mode != "package" {
		client.PreserveExecutable = s.executable
	}
	return client, inventory, nil
}

// monitor runs a check or an audit. Inspection, audit and package failures are
// stored in their own columns, so a failing vulnerability feed doesn't hide
// available updates.
func (s *Service) monitor(ctx context.Context, op *models.HostUpdateOperation, state models.HostUpdateState, log io.Writer) error {
	client, inventory, err := s.inspect(ctx, op, state, log)
	if err != nil {
		column := "check_error"
		if op.Action == "audit" {
			column = "audit_error"
		}
		s.storeError(column, err)
		if op.Action == "check" && code(err) == "host_updates_require_pkgbase" {
			return nil
		}
	} else {
		err = s.auditPackages(ctx, op, client, inventory)
		s.storeError("audit_error", err)
		if op.Action == "check" {
			packageErr := s.checkPackages(ctx, op, client, inventory)
			s.storeError("check_error", packageErr)
			err = errors.Join(err, packageErr)
		}
	}
	if healthErr := s.recordHealth(op.Action, err); healthErr != nil {
		logger.L.Warn().Err(healthErr).Msg("host_update_health_persistence_failed")
	}
	return err
}

func (s *Service) auditPackages(ctx context.Context, op *models.HostUpdateOperation, client *pkgmanager.Client, inventory []pkgmanager.Package) error {
	if err := s.phase(op, "auditing"); err != nil {
		return err
	}
	report, err := client.Audit(ctx, inventory, true)
	if err != nil {
		return err
	}
	return s.storeAudit(inventory, report, op.ID)
}

func (s *Service) checkPackages(ctx context.Context, op *models.HostUpdateOperation, client *pkgmanager.Client, inventory []pkgmanager.Package) error {
	repos, repoHash, err := client.Repositories(ctx)
	if err != nil {
		return err
	}
	if err = s.refreshRepositories(ctx, op, client, inventory, repos, repoHash); err != nil {
		return err
	}
	if err = s.phase(op, "solving"); err != nil {
		return err
	}
	plan, err := client.HostPreview(ctx, repos, nil)
	if err != nil {
		return err
	}
	if err = client.ValidateInstalled(plan, inventory); err != nil {
		return err
	}
	return s.storeAvailable(inventory, plan, op.ID)
}

func (s *Service) refreshRepositories(ctx context.Context, op *models.HostUpdateOperation, client *pkgmanager.Client, inventory []pkgmanager.Package, repos []pkgmanager.Repository, repoHash string) error {
	state, err := s.state()
	if err != nil {
		return err
	}
	inventoryHash := pkgmanager.InventoryDigest(inventory)
	refreshed := state.RepositoriesRefreshedAt
	reuse := op.Action == "packages-preview" && refreshed != nil && !refreshed.After(time.Now()) && time.Since(*refreshed) < 5*time.Minute && state.RepositoryHash == repoHash && state.RepositoryInventoryHash == inventoryHash
	if reuse {
		reuse, err = client.CatalogsPresent(repos)
		if err != nil {
			return err
		}
	}
	if reuse {
		if client.Log != nil {
			_, _ = fmt.Fprintln(client.Log, "Reusing recent repository catalogs. Use Check for updates to refresh them.")
		}
		return nil
	}
	if err = s.phase(op, "refreshing_repositories"); err != nil {
		return err
	}
	if err = s.DB.Model(&state).Updates(map[string]any{"repositories_refreshed_at": nil, "repository_hash": "", "repository_inventory_hash": ""}).Error; err != nil {
		return err
	}
	if err = client.Refresh(ctx, repos); err != nil {
		return err
	}
	return s.DB.Model(&state).Updates(map[string]any{"repositories_refreshed_at": time.Now().UTC(), "repository_hash": repoHash, "repository_inventory_hash": inventoryHash}).Error
}
