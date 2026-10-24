// SPDX-License-Identifier: BSD-2-Clause

package jail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/pkgmanager"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func (s *Service) packagePhase(op *jailModels.JailPackageOperation, phase string) error {
	op.Phase = phase
	return s.DB.Model(op).Update("phase", phase).Error
}

func (s *Service) ExecutePackageTask(ctx context.Context, taskID uint) (runErr error) {
	var op jailModels.JailPackageOperation
	if err := s.DB.First(&op, "task_id = ?", taskID).Error; err != nil {
		return err
	}
	if op.ActiveJailID == nil {
		if op.Error != "" {
			return errors.New(op.Error)
		}
		return nil
	}
	if op.Phase == "applying" || op.Phase == "post_upgrade_audit" {
		err := fmt.Errorf("package_operation_interrupted: inspect installed packages and logs before requesting a fresh preview; no automatic retry")
		s.finishPackageOperation(op, err)
		return err
	}
	select {
	case s.packages.slots <- struct{}{}:
		defer func() { <-s.packages.slots }()
	case <-ctx.Done():
		s.finishPackageOperation(op, ctx.Err())
		return ctx.Err()
	}
	defer func() {
		if runErr != nil && (op.Action == "pkg-check-updates" || op.Action == "pkg-preview") {
			column := "check_error"
			if op.Scope == pkgmanager.ScopeSystem {
				column = "system_check_error"
			}
			_ = s.DB.Model(&jailModels.JailPackageState{}).Where("jail_id = ? AND guid = ?", op.JailID, op.GUID).Update(column, pkgmanager.Redact(runErr.Error())).Error
		}
		if runErr != nil && op.Action == "pkg-audit" && !expectedPackageSkip(runErr) {
			if jail, guid, err := s.packageJail(context.Background(), op.CTID); err == nil && jail.ID == op.JailID && guid == op.GUID {
				state, err := s.packageState(jail, guid)
				if err == nil {
					_ = s.savePackageInventory(state)
					_ = s.recordPackageAuditFailure(jail, guid, runErr)
				}
			}
		}
		s.finishPackageOperation(op, runErr)
	}()
	applyCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	jail, guid, err := s.packageJail(ctx, op.CTID)
	if err != nil {
		return err
	}
	if jail.ID != op.JailID || guid != op.GUID {
		return fmt.Errorf("jail_dataset_identity_changed")
	}
	target, snapshotSafe, err := s.packages.resolve(ctx, jail)
	if err != nil {
		return err
	}
	if op.Action == "pkg-upgrade" && target.Offline {
		return fmt.Errorf("jail_not_running")
	}
	dir, err := s.packageDirectory(op)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(dir, "output.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	log := pkgmanager.NewLogWriter(file)
	defer log.Flush()
	client := s.packages.client(target, log)
	scope, err := pkgmanager.NormalizeScope(op.Scope)
	if err != nil {
		return err
	}
	client.Scope = scope
	op.Scope = scope
	if err = s.packagePhase(&op, "reading_inventory"); err != nil {
		return err
	}
	now := time.Now().UTC()
	inventory, err := client.Inventory(ctx)
	if err != nil {
		return err
	}
	state, err := s.packageState(jail, guid)
	if err != nil {
		return err
	}
	retainPackageAvailability(inventory, state.Inventory)
	state.Inventory, state.InventoryAt = inventory, &now
	if op.Action == "pkg-audit" {
		state.LastAttemptAt = &now
	}
	if err = s.savePackageInventory(state); err != nil {
		return err
	}
	if op.Action == "pkg-audit" {
		if err = s.packagePhase(&op, "auditing"); err != nil {
			return err
		}
		report, auditErr := client.Audit(ctx, inventory, true)
		if auditErr != nil {
			return auditErr
		}
		if err = s.persistPackageAudit(jail, guid, inventory, report, op); err != nil {
			return err
		}
		return s.DB.Model(&op).Update("result", auditSummary(report)).Error
	}
	if scope == pkgmanager.ScopeSystem {
		info, err := client.InspectSystem(ctx, inventory)
		if err != nil {
			return err
		}
		client.System = &info
	}
	var names []string
	if op.Action == "pkg-preview" || op.Action == "pkg-check-updates" {
		names, err = eligiblePackageNames(inventory, op.Selection, op.All || op.Action == "pkg-check-updates", scope)
		if err != nil {
			return err
		}
		if len(names) == 0 {
			if op.Action == "pkg-check-updates" || op.All {
				if err = s.storeUpdateCheck(state, inventory, pkgmanager.Plan{}, scope); err != nil {
					return err
				}
			}
			if op.Action == "pkg-check-updates" {
				return nil
			}
			return s.DB.Model(&op).Update("result", "No eligible application packages to upgrade").Error
		}
	}
	repos, repoHash, err := client.Repositories(ctx)
	if err != nil {
		return err
	}
	if op.Action == "pkg-upgrade" {
		var plan jailModels.JailPackagePlan
		if err = s.DB.First(&plan, "id = ? AND jail_id = ? AND guid = ? AND applied_operation_id = ?", op.PlanID, op.JailID, op.GUID, op.ID).Error; err != nil {
			return fmt.Errorf("package_plan_not_found")
		}
		planScope, err := pkgmanager.NormalizeScope(plan.Scope)
		if err != nil || planScope != scope {
			return fmt.Errorf("package_plan_scope_changed")
		}
		if scope == pkgmanager.ScopeSystem {
			if !op.Acknowledge {
				return fmt.Errorf("package_system_acknowledgement_required")
			}
			if plan.System == nil || client.System == nil || *plan.System != *client.System {
				return fmt.Errorf("package_plan_stale")
			}
		}
		if time.Now().After(plan.ExpiresAt) || plan.InventoryHash != pkgmanager.InventoryDigest(inventory) || plan.RepositoryHash != repoHash {
			return fmt.Errorf("package_plan_stale")
		}
		catalogHash, err := client.CatalogDigest(ctx, repos)
		if err != nil {
			return err
		}
		if catalogHash != plan.CatalogHash {
			return fmt.Errorf("package_plan_stale")
		}
		if err = s.packagePhase(&op, "verifying"); err != nil {
			return err
		}
		current, err := client.Preview(ctx, repos, plan.Selection)
		if err != nil {
			return err
		}
		if current.Digest != plan.Plan.Digest {
			return fmt.Errorf("package_plan_stale")
		}
		if err = client.VerifyInstalled(ctx, current, inventory); err != nil {
			return err
		}
		if err = client.VerifyCandidates(ctx, current, repos); err != nil {
			return err
		}
		if op.Snapshot {
			if !snapshotSafe {
				return fmt.Errorf("package_snapshot_coverage_incomplete")
			}
			if err = s.packagePhase(&op, "snapshotting"); err != nil {
				return err
			}
			snapshot, err := s.CreateJailSnapshot(context.WithValue(ctx, packageOwnerKey{}, op.ID), op.CTID, "Packages "+time.Now().UTC().Format("2006-01-02 15:04"), "Before package upgrade; running-jail snapshot, not application-consistent. Operation "+op.ID)
			if err != nil {
				return fmt.Errorf("package_snapshot_failed: %w", err)
			}
			if err = s.DB.Model(&op).Update("snapshot_id", snapshot.ID).Error; err != nil {
				return err
			}
		}
		fresh, _, err := s.packages.resolve(ctx, jail)
		if err != nil {
			return err
		}
		if fresh != target {
			return fmt.Errorf("jail_runtime_identity_changed")
		}
		inventory, err = client.Inventory(ctx)
		if err != nil {
			return err
		}
		repos, repoHash, err = client.Repositories(ctx)
		if err != nil {
			return err
		}
		catalogHash, err = client.CatalogDigest(ctx, repos)
		if err != nil {
			return err
		}
		if pkgmanager.InventoryDigest(inventory) != plan.InventoryHash || repoHash != plan.RepositoryHash || catalogHash != plan.CatalogHash {
			return fmt.Errorf("package_plan_stale")
		}
		if err = s.packagePhase(&op, "applying"); err != nil {
			return err
		}
		if err = client.Apply(applyCtx, pkgmanager.UpgradeArgs(repos, plan.Selection, false), plan.Plan); err != nil {
			return err
		}
		if err = s.packagePhase(&op, "post_upgrade_audit"); err != nil {
			return err
		}
		message := "Packages upgraded. Review package/configuration messages; restart affected services or the jail as needed."
		if err = s.postPackageUpgrade(applyCtx, op, log); err != nil {
			message += " Follow-up audit unavailable: " + pkgmanager.Redact(err.Error())
		}
		return s.DB.Model(&op).Update("result", message).Error
	}
	refreshedAt, refreshedHash := state.RepositoriesRefreshedAt, state.RepositoryHash
	refreshColumn, hashColumn := "repositories_refreshed_at", "repository_hash"
	if scope == pkgmanager.ScopeSystem {
		refreshedAt, refreshedHash = state.SystemReposRefreshedAt, state.SystemRepositoryHash
		refreshColumn, hashColumn = "system_repos_refreshed_at", "system_repository_hash"
	}
	reuseCatalogs := op.Action == "pkg-preview" && refreshedAt != nil && refreshedHash == repoHash && !refreshedAt.After(time.Now()) && time.Since(*refreshedAt) < 5*time.Minute
	if reuseCatalogs {
		reuseCatalogs, err = client.CatalogsPresent(repos)
		if err != nil {
			return err
		}
	}
	if !reuseCatalogs {
		if err = s.packagePhase(&op, "refreshing_repositories"); err != nil {
			return err
		}
		if err = s.DB.Model(&state).Updates(map[string]any{refreshColumn: nil, hashColumn: ""}).Error; err != nil {
			return err
		}
		if err = client.Refresh(ctx, repos); err != nil {
			return err
		}
		if err = s.DB.Model(&state).Updates(map[string]any{refreshColumn: time.Now().UTC(), hashColumn: repoHash}).Error; err != nil {
			return err
		}
	}
	if err = s.packagePhase(&op, "solving"); err != nil {
		return err
	}
	plan, err := client.Preview(ctx, repos, names)
	if err != nil {
		return err
	}
	if err = client.ValidateInstalled(plan, inventory); err != nil {
		return err
	}
	if op.Action == "pkg-check-updates" {
		if err = s.storeUpdateCheck(state, inventory, plan, scope); err != nil {
			return err
		}
		return s.DB.Model(&op).Update("result", fmt.Sprintf("%d transaction actions available", len(plan.Changes))).Error
	}
	if op.All {
		if err = s.storeUpdateCheck(state, inventory, plan, scope); err != nil {
			return err
		}
	}
	catalogHash, err := client.CatalogDigest(ctx, repos)
	if err != nil {
		return err
	}
	stored := jailModels.JailPackagePlan{
		ID:             uuid.NewString(),
		JailID:         jail.ID,
		GUID:           guid,
		Selection:      names,
		Scope:          scope,
		System:         client.System,
		RepositoryHash: repoHash,
		CatalogHash:    catalogHash,
		InventoryHash:  pkgmanager.InventoryDigest(inventory),
		Plan:           plan,
		ExpiresAt:      time.Now().UTC().Add(10 * time.Minute),
	}
	if err = s.DB.Create(&stored).Error; err != nil {
		return err
	}
	return s.DB.Model(&op).Updates(map[string]any{"plan_id": stored.ID, "result": fmt.Sprintf("%d transaction actions", len(plan.Changes))}).Error
}

func (s *Service) savePackageInventory(state jailModels.JailPackageState) error {
	var old jailModels.JailPackageState
	if err := s.DB.First(&old, "jail_id = ?", state.JailID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return s.DB.Create(&state).Error
		}
		return err
	}
	if old.GUID != state.GUID {
		return s.DB.Save(&state).Error
	}
	return s.DB.Model(&state).Select("GUID", "CTID", "Inventory", "InventoryAt", "LastAttemptAt").Updates(&state).Error
}

func (s *Service) storeUpdateCheck(state jailModels.JailPackageState, inventory []pkgmanager.Package, plan pkgmanager.Plan, scope pkgmanager.Scope) error {
	available := make(map[string]string, len(plan.Changes))
	for _, change := range plan.Changes {
		available[change.Name] = change.To
	}
	for i := range inventory {
		kind := pkgmanager.Classify(inventory[i].Name, inventory[i].Origin)
		if (scope == pkgmanager.ScopeSystem && kind != pkgmanager.KindBase) || (scope != pkgmanager.ScopeSystem && kind != pkgmanager.KindApplication) {
			continue
		}
		inventory[i].Available = ""
		if version := available[inventory[i].Name]; version != inventory[i].Version {
			inventory[i].Available = version
		}
	}
	now := time.Now().UTC()
	state.Inventory = inventory
	if scope == pkgmanager.ScopeSystem {
		state.SystemCheckedAt = &now
		state.SystemCheckInventoryHash = pkgmanager.InventoryDigest(inventory)
		state.SystemCheckError = ""
		return s.DB.Model(&state).Select("Inventory", "SystemCheckedAt", "SystemCheckInventoryHash", "SystemCheckError").Updates(&state).Error
	}
	state.CheckedAt = &now
	state.CheckInventoryHash = pkgmanager.InventoryDigest(inventory)
	state.CheckError = ""
	return s.DB.Model(&state).Select("Inventory", "CheckedAt", "CheckInventoryHash", "CheckError").Updates(&state).Error
}

func auditSummary(report pkgmanager.Audit) string {
	if len(report.Packages) == 0 {
		return "No known vulnerabilities found"
	}
	count := 0
	for _, pkg := range report.Packages {
		count += len(pkg.Advisories)
	}
	return fmt.Sprintf("%d affected packages, %d advisories", len(report.Packages), count)
}

func (s *Service) postPackageUpgrade(ctx context.Context, op jailModels.JailPackageOperation, log io.Writer) (runErr error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	defer func() {
		if runErr != nil {
			_, _ = fmt.Fprintf(log, "Follow-up audit unavailable: %s\n", pkgmanager.Redact(runErr.Error()))
		}
	}()
	jail, guid, err := s.packageJail(ctx, op.CTID)
	if err != nil {
		return err
	}
	if jail.ID != op.JailID || guid != op.GUID {
		return fmt.Errorf("jail_dataset_identity_changed")
	}
	target, _, err := s.packages.resolve(ctx, jail)
	if err != nil {
		return err
	}
	client := s.packages.client(target, log)
	inventory, err := client.Inventory(ctx)
	if err != nil {
		return err
	}
	state, err := s.packageState(jail, guid)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	state.Inventory, state.InventoryAt, state.LastAttemptAt = inventory, &now, &now
	if err = s.savePackageInventory(state); err != nil {
		return err
	}
	report, err := client.Audit(ctx, inventory, true)
	if err != nil {
		_ = s.recordPackageAuditFailure(jail, guid, err)
		return err
	}
	return s.persistPackageAudit(jail, guid, inventory, report, op)
}

func (s *Service) finalizeRecoveredPackageTask(op jailModels.JailPackageOperation, runErr error) {
	if op.TaskID == nil {
		return
	}
	now := time.Now().UTC()
	status, message, failure := taskModels.LifecycleTaskStatusSuccess, "completed", ""
	if runErr != nil {
		status, message, failure = taskModels.LifecycleTaskStatusFailed, "failed", runErr.Error()
	}
	if err := s.DB.Model(&taskModels.GuestLifecycleTask{}).Where("id = ?", *op.TaskID).Updates(map[string]any{"status": status, "message": message, "error": failure, "finished_at": now}).Error; err != nil {
		logger.L.Error().Err(err).Msg("package_task_recovery_failed")
		return
	}
	db.FinalizeAsyncAuditRecord(s.packages.telemetry, "jail_"+op.Action, *op.TaskID, status, failure, nil)
}

func (s *Service) RecoverPackageTasks(ctx context.Context) error {
	if s.packages == nil {
		return nil
	}
	var operations []jailModels.JailPackageOperation
	if err := s.DB.WithContext(ctx).Where("active_jail_id IS NOT NULL OR task_id IN (SELECT id FROM guest_lifecycle_tasks WHERE status IN ?)", []string{"queued", "running"}).Find(&operations).Error; err != nil {
		return err
	}
	for _, op := range operations {
		if op.ActiveJailID == nil {
			var err error
			if op.Error != "" {
				err = errors.New(op.Error)
			}
			s.finalizeRecoveredPackageTask(op, err)
			continue
		}
		err := fmt.Errorf("package_operation_interrupted: inspect installed packages and logs before requesting a fresh audit or preview; no automatic retry")
		s.finishPackageOperation(op, err)
		s.finalizeRecoveredPackageTask(op, err)
	}
	return nil
}
