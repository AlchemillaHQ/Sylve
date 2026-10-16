// SPDX-License-Identifier: BSD-2-Clause

package jail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	jailTypes "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/pkgmanager"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type PackageTaskRequester func(context.Context, uint, string, string, string, string) (uint, error)

type packageTaskPayload struct {
	OperationID string `json:"operationId"`
}

type packageAdmission struct {
	ordinary  int
	operation string
}
type packageOwnerKey struct{}

type packageRuntime struct {
	mu        sync.Mutex
	admission map[uint]*packageAdmission
	observers map[string]bool
	telemetry *gorm.DB
	request   PackageTaskRequester
	dataPath  string
	slots     chan struct{}
	resolve   func(context.Context, *jailModels.Jail) (pkgmanager.Target, bool, error)
	client    func(pkgmanager.Target, io.Writer) *pkgmanager.Client
}

func IsPackageAction(action string) bool {
	switch action {
	case "pkg-audit", "pkg-check-updates", "pkg-preview", "pkg-upgrade":
		return true
	}
	return false
}

func (s *Service) InitializePackages(telemetry *gorm.DB, request PackageTaskRequester) error {
	dataPath, err := config.GetDataPath()
	if err != nil {
		return err
	}
	s.packages = &packageRuntime{
		telemetry: telemetry,
		request:   request,
		dataPath:  filepath.Join(dataPath, "package-operations"),
		admission: make(map[uint]*packageAdmission),
		observers: make(map[string]bool),
		slots:     make(chan struct{}, 2),
	}
	s.packages.resolve = s.resolvePackageTarget
	s.packages.client = func(target pkgmanager.Target, log io.Writer) *pkgmanager.Client {
		return &pkgmanager.Client{Binary: pkgmanager.DefaultBinary, Target: target, Log: log}
	}
	return nil
}

func (s *Service) packageJail(ctx context.Context, ctID uint) (*jailModels.Jail, string, error) {
	if ctID == 0 {
		return nil, "", fmt.Errorf("invalid_ct_id")
	}
	jail := &jailModels.Jail{}
	if err := s.DB.WithContext(ctx).Preload("Storages").First(jail, "ct_id = ?", ctID).Error; err != nil {
		return nil, "", err
	}
	if jail.Type != jailModels.JailTypeFreeBSD {
		return nil, "", fmt.Errorf("package_management_unsupported_jail_type")
	}
	if jail.CreationPending {
		return nil, "", fmt.Errorf("jail_creation_in_progress")
	}
	_, storage, err := jailRootDatasetIdentity(jail)
	if err != nil {
		return nil, "", err
	}
	if storage.GUID == "" {
		return nil, "", fmt.Errorf("jail_dataset_identity_unavailable")
	}
	return jail, storage.GUID, nil
}

func (s *Service) resolvePackageTarget(ctx context.Context, jail *jailModels.Jail) (pkgmanager.Target, bool, error) {
	allowed, err := s.CanMutateProtectedJail(jail.CTID)
	if err != nil {
		return pkgmanager.Target{}, false, err
	}
	if !allowed {
		return pkgmanager.Target{}, false, fmt.Errorf("replication_lease_not_owned")
	}
	dataset, storage, err := jailRootDatasetIdentity(jail)
	if err != nil {
		return pkgmanager.Target{}, false, err
	}
	root, _, err := s.resolveFilesystemDatasetMountpoint(ctx, dataset, storage.GUID)
	if err != nil {
		return pkgmanager.Target{}, false, fmt.Errorf("jail_dataset_mountpoint_not_usable: %w", err)
	}
	targets, err := pkgmanager.ListTargets(ctx)
	if err != nil {
		return pkgmanager.Target{}, false, fmt.Errorf("jail_runtime_unavailable: %w", err)
	}
	target := pkgmanager.Target{Name: s.GetCTIDHash(jail.CTID), Root: root, Dataset: dataset, GUID: storage.GUID, Offline: true}
	for _, running := range targets {
		if running.Name != target.Name {
			if filepath.Clean(running.Root) == filepath.Clean(root) {
				return pkgmanager.Target{}, false, fmt.Errorf("jail_runtime_identity_changed")
			}
			continue
		}
		if filepath.Clean(running.Root) != filepath.Clean(root) {
			return pkgmanager.Target{}, false, fmt.Errorf("jail_runtime_identity_changed")
		}
		target.JID, target.Offline = running.JID, false
		break
	}
	safe, err := pkgmanager.ValidatePrivateRoot(ctx, target)
	if err != nil {
		return pkgmanager.Target{}, false, err
	}
	if err := pkgmanager.Probe(""); err != nil {
		return pkgmanager.Target{}, false, err
	}
	return target, safe && !target.Offline, nil
}

func (s *Service) packageState(jail *jailModels.Jail, guid string) (jailModels.JailPackageState, error) {
	state := jailModels.JailPackageState{}
	err := s.DB.First(&state, "jail_id = ?", jail.ID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return state, err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) || state.GUID != guid {
		state = jailModels.JailPackageState{
			JailID:    jail.ID,
			CTID:      jail.CTID,
			GUID:      guid,
			Inventory: []pkgmanager.Package{},
			Audit:     pkgmanager.Audit{Packages: []pkgmanager.AuditPackage{}},
		}
	}
	if auditHash := pkgmanager.AuditInventoryDigest(state.Inventory); state.AuditAt != nil && state.AuditInventoryHash != auditHash && state.AuditInventoryHash == pkgmanager.InventoryDigest(state.Inventory) {
		previous := state.AuditInventoryHash
		state.AuditInventoryHash = auditHash
		if err := s.DB.Model(&state).Where("guid = ? AND audit_inventory_hash = ?", guid, previous).Update("audit_inventory_hash", state.AuditInventoryHash).Error; err != nil {
			return state, err
		}
	}
	if state.Inventory == nil {
		state.Inventory = []pkgmanager.Package{}
	}
	if state.Audit.Packages == nil {
		state.Audit.Packages = []pkgmanager.AuditPackage{}
	}
	return state, nil
}

func monitorView(jail *jailModels.Jail, state jailModels.JailPackageState) jailTypes.PackageMonitoring {
	interval := jail.PackageAuditIntervalHours
	if interval != 6 && interval != 12 && interval != 24 {
		interval = 24
	}
	next := state.NextAuditAt
	if !jail.PackageMonitoringEnabled {
		next = nil
	}
	return jailTypes.PackageMonitoring{
		Enabled:       jail.PackageMonitoringEnabled,
		IntervalHours: interval,
		NextAuditAt:   next,
		LastAttemptAt: state.LastAttemptAt,
		LastSuccessAt: state.AuditAt,
		FailureCount:  state.FailureCount,
		SkipReason:    state.SkipReason,
	}
}

func invalidatePackageFreshness(tx *gorm.DB, jailID uint) error {
	return tx.Model(&jailModels.JailPackageState{}).Where("jail_id = ?", jailID).Updates(map[string]any{
		"audit_inventory_hash": "", "check_inventory_hash": "", "system_check_inventory_hash": "",
		"repositories_refreshed_at": nil, "repository_hash": "",
		"system_repos_refreshed_at": nil, "system_repository_hash": "",
		"next_audit_at": time.Now().UTC(),
	}).Error
}

func (s *Service) GetPackages(ctx context.Context, ctID uint) (jailTypes.PackageView, error) {
	jail, guid, err := s.packageJail(ctx, ctID)
	if err != nil {
		return jailTypes.PackageView{}, err
	}
	if s.packages == nil {
		return jailTypes.PackageView{}, fmt.Errorf("package_service_unavailable")
	}
	view, _, err := s.readPackages(ctx, jail, guid)
	return view, err
}

func (s *Service) readPackages(ctx context.Context, jail *jailModels.Jail, guid string) (jailTypes.PackageView, *pkgmanager.Target, error) {
	state, err := s.packageState(jail, guid)
	if err != nil {
		return jailTypes.PackageView{}, nil, err
	}
	view := jailTypes.PackageView{
		JailID:           jail.ID,
		GUID:             guid,
		Packages:         state.Inventory,
		InventoryAt:      state.InventoryAt,
		CheckedAt:        state.CheckedAt,
		SystemCheckedAt:  state.SystemCheckedAt,
		SystemCheckError: state.SystemCheckError,
		AuditAt:          state.AuditAt,
		Audit:            state.Audit,
		AuditError:       state.AuditError,
		CheckError:       state.CheckError,
		Monitoring:       monitorView(jail, state),
	}
	var resolved *pkgmanager.Target
	var active jailModels.JailPackageOperation
	query := s.DB.Where("jail_id = ? AND guid = ? AND active_jail_id IS NOT NULL", jail.ID, guid).Limit(1).Find(&active)
	if query.Error != nil {
		return view, nil, query.Error
	}
	if query.RowsAffected != 0 {
		view.Reason = "package_operation_in_progress"
		if active.TaskID != nil {
			job, err := s.packageJob(active)
			if err != nil {
				return view, nil, err
			}
			view.ActiveJob = &job
		}
	} else {
		readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		target, safe, targetErr := s.packages.resolve(readCtx, jail)
		if targetErr != nil {
			view.Reason = packageErrorCode(targetErr)
		} else {
			view.Offline = target.Offline
			view.SnapshotSupported = safe
			inventory, readErr := s.packages.client(target, nil).Inventory(readCtx)
			if readErr != nil {
				view.Reason = packageErrorCode(readErr)
			} else {
				retainPackageAvailability(inventory, state.Inventory)
				view.Packages = inventory
				now := time.Now().UTC()
				view.InventoryAt = &now
				view.Ready = true
				resolved = &target
			}
		}
	}
	hash := pkgmanager.InventoryDigest(view.Packages)
	view.UpdatesChecked = state.CheckedAt != nil && state.CheckInventoryHash == hash && state.CheckError == ""
	view.Pkgbase = pkgmanager.HasPkgbase(view.Packages)
	view.SystemUpdatesChecked = state.SystemCheckedAt != nil && state.SystemCheckInventoryHash == hash && state.SystemCheckError == ""
	freshHours := 24
	if view.Monitoring.Enabled {
		freshHours = view.Monitoring.IntervalHours
	}
	verified := view.Ready || (query.RowsAffected != 0 && active.Action != "pkg-upgrade")
	view.AuditStale = !verified || state.AuditAt == nil || state.AuditInventoryHash != pkgmanager.AuditInventoryDigest(view.Packages) || state.AuditError != "" || time.Since(*state.AuditAt) > time.Duration(freshHours+1)*time.Hour
	findings := make(map[string]pkgmanager.AuditPackage, len(state.Audit.Packages))
	for _, finding := range state.Audit.Packages {
		findings[finding.Name] = finding
	}
	for i := range view.Packages {
		pkg := &view.Packages[i]
		pkg.Kind = pkgmanager.Classify(pkg.Name, pkg.Origin)
		pkg.Protected = pkg.Kind != pkgmanager.KindApplication
		pkg.Advisories = []pkgmanager.Advisory{}
		if finding, ok := findings[pkg.Name]; ok && finding.Version == pkg.Version {
			pkg.Advisories = finding.Advisories
		}
		if (pkg.Kind == pkgmanager.KindBase && !view.SystemUpdatesChecked) ||
			(pkg.Kind == pkgmanager.KindApplication && !view.UpdatesChecked) ||
			(pkg.Kind != pkgmanager.KindBase && pkg.Kind != pkgmanager.KindApplication) {
			pkg.Available = ""
		}
	}
	return view, resolved, nil
}

func retainPackageAvailability(inventory, previous []pkgmanager.Package) {
	byName := make(map[string]pkgmanager.Package, len(previous))
	for _, pkg := range previous {
		byName[pkg.Name] = pkg
	}
	for i := range inventory {
		if old, ok := byName[inventory[i].Name]; ok && old.Version == inventory[i].Version {
			inventory[i].Available = old.Available
		}
	}
}

func packageErrorCode(err error) string {
	code, _, _ := strings.Cut(err.Error(), ":")
	return strings.TrimSpace(code)
}

func (s *Service) GetPackageDetails(ctx context.Context, ctID uint, name string) (jailTypes.PackageDetails, error) {
	if !pkgmanager.ValidName(name) {
		return jailTypes.PackageDetails{}, fmt.Errorf("invalid_package_name")
	}
	jail, guid, err := s.packageJail(ctx, ctID)
	if err != nil {
		return jailTypes.PackageDetails{}, err
	}
	if s.packages == nil {
		return jailTypes.PackageDetails{}, fmt.Errorf("package_service_unavailable")
	}
	view, target, err := s.readPackages(ctx, jail, guid)
	if err != nil {
		return jailTypes.PackageDetails{}, err
	}
	for _, pkg := range view.Packages {
		if pkg.Name != name {
			continue
		}
		detail := jailTypes.PackageDetails{Package: pkg, Dependencies: []pkgmanager.Dependency{}}
		if target != nil {
			deps, err := s.packages.client(*target, nil).Dependencies(ctx, name)
			if err != nil {
				return detail, err
			}
			detail.Dependencies = deps
			detail.DependenciesAvailable = true
		}
		return detail, nil
	}
	return jailTypes.PackageDetails{}, fmt.Errorf("package_not_found")
}

func (s *Service) ReserveSystemOperation(ctx context.Context, ctID uint) (func(), error) {
	if s.packages == nil {
		return func() {}, nil
	}
	runtime := s.packages
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	gate := runtime.admission[ctID]
	if gate == nil {
		gate = &packageAdmission{}
		runtime.admission[ctID] = gate
	}
	owner, _ := ctx.Value(packageOwnerKey{}).(string)
	if gate.operation != "" && gate.operation != owner {
		return nil, fmt.Errorf("package_operation_in_progress")
	}
	var active jailModels.JailPackageOperation
	result := s.DB.Where("ct_id = ? AND active_jail_id IS NOT NULL", ctID).Limit(1).Find(&active)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 0 && active.ID != owner {
		return nil, fmt.Errorf("package_operation_in_progress")
	}
	gate.ordinary++
	return func() {
		runtime.mu.Lock()
		gate.ordinary--
		if gate.ordinary == 0 && gate.operation == "" {
			delete(runtime.admission, ctID)
		}
		runtime.mu.Unlock()
	}, nil
}

func (s *Service) StartPackageOperation(ctx context.Context, ctID uint, action string, req jailTypes.PackageRequest, source, actor string) (jailTypes.PackageJob, error) {
	if !IsPackageAction(action) {
		return jailTypes.PackageJob{}, fmt.Errorf("invalid_package_action")
	}
	jail, guid, err := s.packageJail(ctx, ctID)
	if err != nil {
		return jailTypes.PackageJob{}, err
	}
	if s.packages == nil || s.packages.request == nil {
		return jailTypes.PackageJob{}, fmt.Errorf("package_service_unavailable")
	}
	scope, err := pkgmanager.NormalizeScope(req.Scope)
	if err != nil {
		return jailTypes.PackageJob{}, err
	}
	if action == "pkg-audit" && scope != pkgmanager.ScopeApplications {
		return jailTypes.PackageJob{}, fmt.Errorf("invalid_package_scope")
	}
	if len(req.Packages) > 512 {
		return jailTypes.PackageJob{}, fmt.Errorf("too_many_packages")
	}
	for _, name := range req.Packages {
		if !pkgmanager.ValidName(name) {
			return jailTypes.PackageJob{}, fmt.Errorf("invalid_package_name")
		}
	}
	if action == "pkg-preview" && req.All == (len(req.Packages) > 0) {
		return jailTypes.PackageJob{}, fmt.Errorf("package_selection_required")
	}
	if action == "pkg-preview" && scope == pkgmanager.ScopeSystem && !req.All {
		return jailTypes.PackageJob{}, fmt.Errorf("package_system_selection_required")
	}
	if action == "pkg-upgrade" {
		var plan jailModels.JailPackagePlan
		if err = s.DB.First(&plan, "id = ? AND jail_id = ? AND guid = ?", req.PlanID, jail.ID, guid).Error; err != nil {
			return jailTypes.PackageJob{}, fmt.Errorf("package_plan_not_found")
		}
		planScope, err := pkgmanager.NormalizeScope(plan.Scope)
		if err != nil {
			return jailTypes.PackageJob{}, err
		}
		if req.Scope != "" && scope != planScope {
			return jailTypes.PackageJob{}, fmt.Errorf("package_plan_scope_changed")
		}
		scope = planScope
		if plan.AppliedOperationID != "" {
			var existing jailModels.JailPackageOperation
			if err = s.DB.First(&existing, "id = ?", plan.AppliedOperationID).Error; err == nil && existing.TaskID != nil {
				return s.replayedPackageJob(ctx, ctID, *existing.TaskID)
			}
			return jailTypes.PackageJob{}, fmt.Errorf("package_plan_already_used")
		}
		if scope == pkgmanager.ScopeSystem {
			if plan.System == nil {
				return jailTypes.PackageJob{}, fmt.Errorf("package_plan_stale")
			}
			if !req.Acknowledge {
				return jailTypes.PackageJob{}, fmt.Errorf("package_system_acknowledgement_required")
			}
		}
		if time.Now().After(plan.ExpiresAt) {
			return jailTypes.PackageJob{}, fmt.Errorf("package_plan_stale")
		}
		if len(plan.Plan.Changes) == 0 {
			return jailTypes.PackageJob{}, fmt.Errorf("no_eligible_package_changes")
		}
		for _, change := range plan.Plan.Changes {
			if (change.Action == "remove" || change.Action == "downgrade") && !req.Acknowledge {
				return jailTypes.PackageJob{}, fmt.Errorf("package_changes_acknowledgement_required")
			}
		}
	}
	target, _, err := s.packages.resolve(ctx, jail)
	if err != nil {
		return jailTypes.PackageJob{}, err
	}
	if action == "pkg-upgrade" && target.Offline {
		return jailTypes.PackageJob{}, fmt.Errorf("jail_not_running")
	}
	runtime := s.packages
	runtime.mu.Lock()
	gate := runtime.admission[ctID]
	if gate == nil {
		gate = &packageAdmission{}
		runtime.admission[ctID] = gate
	}
	if gate.ordinary > 0 || gate.operation != "" {
		runtime.mu.Unlock()
		return jailTypes.PackageJob{}, fmt.Errorf("jail_operation_in_progress")
	}
	operation := jailModels.JailPackageOperation{
		ID:           uuid.NewString(),
		JailID:       jail.ID,
		ActiveJailID: &jail.ID,
		CTID:         ctID,
		GUID:         guid,
		Action:       action,
		Scope:        scope,
		Phase:        "queued",
		Selection:    req.Packages,
		All:          req.All,
		PlanID:       req.PlanID,
		Snapshot:     req.Snapshot,
		Acknowledge:  req.Acknowledge,
	}
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&operation).Error; err != nil {
			return fmt.Errorf("package_operation_in_progress: %w", err)
		}
		if action == "pkg-upgrade" {
			result := tx.Model(&jailModels.JailPackagePlan{}).Where("id = ? AND applied_operation_id = '' AND expires_at > ?", req.PlanID, time.Now().UTC()).Update("applied_operation_id", operation.ID)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("package_plan_already_used")
			}
		}
		return nil
	})
	if err != nil {
		runtime.mu.Unlock()
		return jailTypes.PackageJob{}, err
	}
	gate.operation = operation.ID
	runtime.mu.Unlock()
	payload, _ := json.Marshal(packageTaskPayload{OperationID: operation.ID})
	taskID, err := runtime.request(ctx, ctID, action, string(payload), source, actor)
	if err != nil {
		s.finishPackageOperation(operation, err)
		return jailTypes.PackageJob{}, err
	}
	return s.GetPackageJob(ctx, ctID, taskID)
}

func (s *Service) replayedPackageJob(ctx context.Context, ctID, taskID uint) (jailTypes.PackageJob, error) {
	ref, err := db.PrepareAsyncAuditRecord(s.packages.telemetry, ctx, "jail_pkg-upgrade", taskID, fmt.Sprintf("guest-lifecycle:jail_pkg-upgrade:%d", taskID))
	if err != nil {
		return jailTypes.PackageJob{}, err
	}
	job, err := s.GetPackageJob(ctx, ctID, taskID)
	if err != nil {
		return job, err
	}
	if job.Status == taskModels.LifecycleTaskStatusSuccess || job.Status == taskModels.LifecycleTaskStatusFailed {
		err = db.FinalizeAsyncAuditOperation(s.packages.telemetry, ref, job.Status, job.Error, nil)
	}
	return job, err
}

func (s *Service) PreparePackageTask(ctx context.Context, taskID, ctID uint, action, payload string) error {
	var request packageTaskPayload
	if json.Unmarshal([]byte(payload), &request) != nil {
		return fmt.Errorf("invalid_package_task")
	}
	result := s.DB.WithContext(ctx).Model(&jailModels.JailPackageOperation{}).Where("id = ? AND ct_id = ? AND action = ? AND active_jail_id IS NOT NULL AND task_id IS NULL", request.OperationID, ctID, action).Update("task_id", taskID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("invalid_package_task")
	}
	return nil
}

func (s *Service) finishPackageOperation(op jailModels.JailPackageOperation, err error) {
	now := time.Now().UTC()
	phase, failure := "completed", ""
	if err != nil {
		phase = "failed"
		failure = pkgmanager.Redact(err.Error())
	}
	if err := s.DB.Model(&jailModels.JailPackageOperation{}).Where("id = ?", op.ID).Updates(map[string]any{"active_jail_id": nil, "phase": phase, "error": failure, "finished_at": now}).Error; err != nil {
		logger.L.Error().Err(err).Str("operation_id", op.ID).Msg("package_completion_persistence_failed")
		return
	}
	if s.packages != nil {
		s.packages.mu.Lock()
		if gate := s.packages.admission[op.CTID]; gate != nil && gate.operation == op.ID {
			gate.operation = ""
			if gate.ordinary == 0 {
				delete(s.packages.admission, op.CTID)
			}
		}
		s.packages.mu.Unlock()
	}
}

func (s *Service) packageOperation(ctx context.Context, ctID, taskID uint) (jailModels.JailPackageOperation, error) {
	jail, guid, err := s.packageJail(ctx, ctID)
	if err != nil {
		return jailModels.JailPackageOperation{}, err
	}
	op := jailModels.JailPackageOperation{}
	if err = s.DB.First(&op, "task_id = ? AND jail_id = ? AND guid = ?", taskID, jail.ID, guid).Error; err != nil {
		return op, fmt.Errorf("package_job_not_found")
	}
	return op, nil
}

func (s *Service) GetPackageJob(ctx context.Context, ctID, taskID uint) (jailTypes.PackageJob, error) {
	op, err := s.packageOperation(ctx, ctID, taskID)
	if err != nil {
		return jailTypes.PackageJob{}, err
	}
	return s.packageJob(op)
}

func (s *Service) packageJob(op jailModels.JailPackageOperation) (jailTypes.PackageJob, error) {
	var task taskModels.GuestLifecycleTask
	if err := s.DB.First(&task, "id = ? AND guest_type = ? AND guest_id = ? AND action = ?", op.TaskID, "jail", op.CTID, op.Action).Error; err != nil {
		return jailTypes.PackageJob{}, err
	}
	scope, err := pkgmanager.NormalizeScope(op.Scope)
	if err != nil {
		return jailTypes.PackageJob{}, err
	}
	view := jailTypes.PackageJob{
		TaskID:      task.ID,
		OperationID: op.ID,
		Action:      op.Action,
		Scope:       scope,
		Phase:       op.Phase,
		Status:      task.Status,
		Error:       op.Error,
		Result:      op.Result,
		SnapshotID:  op.SnapshotID,
		CreatedAt:   op.CreatedAt,
		FinishedAt:  op.FinishedAt,
		Audit:       op.Audit,
	}
	if op.PlanID != "" {
		var plan jailModels.JailPackagePlan
		if err = s.DB.First(&plan, "id = ? AND jail_id = ? AND guid = ?", op.PlanID, op.JailID, op.GUID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return view, nil
			}
			return jailTypes.PackageJob{}, err
		}
		planScope, err := pkgmanager.NormalizeScope(plan.Scope)
		if err != nil || planScope != scope {
			return jailTypes.PackageJob{}, fmt.Errorf("package_plan_scope_changed")
		}
		view.Plan = &jailTypes.PackagePlan{
			ID:          plan.ID,
			ExpiresAt:   plan.ExpiresAt,
			Selection:   plan.Selection,
			Transaction: plan.Plan,
			Scope:       planScope,
			System:      plan.System,
		}
	}
	return view, nil
}

func (s *Service) packageDirectory(op jailModels.JailPackageOperation) (string, error) {
	if s.packages == nil {
		return "", fmt.Errorf("package_service_unavailable")
	}
	if _, err := uuid.Parse(op.ID); err != nil {
		return "", fmt.Errorf("invalid_package_operation")
	}
	dir := filepath.Join(s.packages.dataPath, op.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func (s *Service) PackageLogs(ctx context.Context, ctID, taskID uint, offset int64) (jailTypes.PackageLog, error) {
	op, err := s.packageOperation(ctx, ctID, taskID)
	if err != nil {
		return jailTypes.PackageLog{}, err
	}
	dir, err := s.packageDirectory(op)
	if err != nil {
		return jailTypes.PackageLog{}, err
	}
	file, err := os.Open(filepath.Join(dir, "output.log"))
	if os.IsNotExist(err) {
		return jailTypes.PackageLog{Next: offset}, nil
	}
	if err != nil {
		return jailTypes.PackageLog{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return jailTypes.PackageLog{}, err
	}
	if offset < 0 || offset > info.Size() {
		return jailTypes.PackageLog{}, fmt.Errorf("invalid_log_offset")
	}
	if _, err = file.Seek(offset, io.SeekStart); err != nil {
		return jailTypes.PackageLog{}, err
	}
	data, err := io.ReadAll(io.LimitReader(file, 64<<10))
	if err != nil {
		return jailTypes.PackageLog{}, err
	}
	return jailTypes.PackageLog{Text: pkgmanager.Redact(string(data)), Next: offset + int64(len(data))}, nil
}

func eligiblePackageNames(packages []pkgmanager.Package, selection []string, all bool, scope pkgmanager.Scope) ([]string, error) {
	scope, err := pkgmanager.NormalizeScope(scope)
	if err != nil {
		return nil, err
	}
	eligible := func(pkg pkgmanager.Package) bool {
		if pkg.Locked {
			return false
		}
		if scope == pkgmanager.ScopeSystem {
			return pkgmanager.Classify(pkg.Name, pkg.Origin) == pkgmanager.KindBase
		}
		return !pkg.Protected && pkgmanager.Classify(pkg.Name, pkg.Origin) == pkgmanager.KindApplication
	}
	names := []string{}
	if all {
		for _, pkg := range packages {
			if eligible(pkg) {
				names = append(names, pkg.Name)
			}
		}
	} else {
		byName := make(map[string]pkgmanager.Package, len(packages))
		for _, pkg := range packages {
			byName[pkg.Name] = pkg
		}
		seen := map[string]bool{}
		for _, name := range selection {
			pkg, ok := byName[name]
			if !ok {
				return nil, fmt.Errorf("package_not_installed: %s", name)
			}
			if !eligible(pkg) {
				return nil, fmt.Errorf("package_not_upgradeable: %s", name)
			}
			if seen[name] {
				return nil, fmt.Errorf("duplicate_package_selection")
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func (s *Service) CancelUnpublishedPackageTask(_ context.Context, taskID uint, cause error) error {
	var op jailModels.JailPackageOperation
	if err := s.DB.First(&op, "task_id = ?", taskID).Error; err != nil {
		return cause
	}
	s.finishPackageOperation(op, cause)
	return cause
}
