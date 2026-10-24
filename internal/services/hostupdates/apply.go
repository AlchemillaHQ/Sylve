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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/pkg/pkgmanager"
	"github.com/alchemillahq/sylve/pkg/sylveupdate"
	"gorm.io/gorm"
)

type applyResult struct {
	rebootBootID    string
	bootEnvironment string
	executableHash  string
}

func trustedExecutable(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("untrusted_host_update_file")
	}
	return nil
}

func packageOwnership(ctx context.Context, client *pkgmanager.Client, executable string) (bool, string, error) {
	if _, err := os.Stat("/var/db/pkg/local.sqlite"); os.IsNotExist(err) {
		return false, "", nil
	} else if err != nil {
		return false, "", err
	}
	inventory, err := client.Inventory(ctx)
	if err != nil {
		return false, "", err
	}
	registered := false
	for _, pkg := range inventory {
		if pkg.Name == "sylve" || pkg.Origin == "sysutils/sylve" {
			registered = true
		}
	}
	owner, err := client.Run(ctx, "which", "-qo", executable)
	if err != nil {
		return false, "", err
	}
	if owner.ExitCode == 0 && strings.TrimSpace(owner.Stdout) != "" {
		return registered, strings.TrimSpace(owner.Stdout), nil
	}
	if owner.ExitCode == 1 {
		return registered, "", nil
	}
	return false, "", fmt.Errorf("sylve_package_ownership_unknown")
}

func verifyPackagedSylve(ctx context.Context, client *pkgmanager.Client, plan pkgmanager.Plan, inventory []pkgmanager.Package, executable string) error {
	for _, change := range plan.Changes {
		isSylve := change.Name == "sylve"
		for _, pkg := range inventory {
			if pkg.Name == change.Name && pkg.Origin == "sysutils/sylve" {
				isSylve = true
			}
		}
		if !isSylve {
			continue
		}
		result, err := client.Run(ctx, "query", "%Fp\t%Fs", change.Name)
		if err != nil {
			return err
		}
		checksum := ""
		for _, line := range strings.Split(result.Stdout, "\n") {
			file, digest, ok := strings.Cut(line, "\t")
			if ok && file == executable {
				if checksum != "" {
					return fmt.Errorf("sylve_package_ownership_unknown")
				}
				checksum = strings.TrimSpace(digest)
			}
		}
		if result.ExitCode != 0 || checksum == "" {
			return fmt.Errorf("sylve_package_ownership_unknown")
		}
		result, err = client.Run(ctx, "checksum", "-q", "-c", checksum, executable)
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("packaged_sylve_modified: remove the package before using standalone releases")
		}
	}
	return nil
}

func trustedDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || !info.IsDir() || info.Mode().Perm() != 0700 {
		return fmt.Errorf("untrusted_host_update_directory")
	}
	return nil
}

func (s *Service) apply(ctx context.Context, op *models.HostUpdateOperation, log io.Writer) (string, error) {
	var plan models.HostUpdatePlan
	if err := s.DB.First(&plan, "id = ? AND applied_operation_id = ?", op.PlanID, op.ID).Error; err != nil {
		return "", err
	}
	if time.Now().After(plan.ExpiresAt) {
		return "", fmt.Errorf("host_update_plan_stale")
	}
	state, err := s.state()
	if err != nil {
		return "", err
	}
	preflight, cancel := context.WithTimeout(ctx, 2*time.Minute)
	installed := s.installed(preflight, state)
	cancel()
	if installed.SHA256 != plan.ExecutableHash || installed.Reason == "sylve_executable_changed_restart_required" {
		return "", fmt.Errorf("host_update_plan_stale")
	}
	if err = s.phase(op, "verifying"); err != nil {
		return "", err
	}
	var result applyResult
	switch plan.Kind {
	case "sylve":
		err = s.applyRelease(ctx, op, plan, installed, &result, log)
	case "packages":
		err = s.applyPackages(ctx, op, plan, installed, &result, log)
	default:
		err = fmt.Errorf("invalid_host_update_request")
	}
	if result.rebootBootID != "" {
		err = errors.Join(err, s.DB.Model(&state).Update("reboot_boot_id", result.rebootBootID).Error)
	}
	err = errors.Join(err, s.DB.Model(&state).Updates(map[string]any{"check_inventory_hash": "", "audit_inventory_hash": ""}).Error)
	if err != nil {
		return "Some packages or the executable may have changed. Inspect the log and installed state before retrying; no automatic rollback was performed.", err
	}
	message := "Update complete. Review package messages and restart services as needed."
	if result.bootEnvironment != "" {
		message += " Pre-update boot environment: " + result.bootEnvironment + "."
	}
	if result.rebootBootID != "" {
		message += " Host reboot recommended."
	}
	if result.executableHash != s.runningDigest {
		actual, err := sylveupdate.FileDigest(s.executable)
		if err != nil || actual != result.executableHash {
			return "", fmt.Errorf("sylve_executable_changed_restart_required")
		}
		if s.restart == nil {
			return "The executable was updated. Restart Sylve manually.", fmt.Errorf("sylve_restart_unavailable")
		}
		if op.Action == "sylve-apply" {
			message = "Sylve executable installed. Restarting to verify the new build."
		}
		op.Phase, op.RestartDigest, op.Result = "restarting", result.executableHash, message
		return "", s.DB.Model(op).Select("Phase", "RestartDigest", "Result").Updates(op).Error
	}
	return s.completeApply(ctx, *op, plan, message)
}

func (s *Service) applyPackages(ctx context.Context, op *models.HostUpdateOperation, approved models.HostUpdatePlan, installed sylveupdate.Installed, result *applyResult, log io.Writer) error {
	applyCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if err := s.probe(); err != nil {
		return err
	}
	client := s.client(log)
	client.HostSystem = &approved.Host
	client.SylveVersion = installed.Version
	if installed.Mode != "package" {
		client.PreserveExecutable = installed.Path
	}
	inventory, err := client.Inventory(ctx)
	if err != nil {
		return err
	}
	if pkgmanager.InventoryDigest(inventory) != approved.InventoryHash {
		return fmt.Errorf("host_update_plan_stale")
	}
	info, err := client.InspectHost(ctx, inventory)
	if err != nil {
		return err
	}
	if info != approved.Host {
		return fmt.Errorf("host_update_plan_stale")
	}
	repos, repoHash, err := client.Repositories(ctx)
	if err != nil {
		return err
	}
	catalogHash, err := client.CatalogDigest(ctx, repos)
	if err != nil {
		return err
	}
	if repoHash != approved.RepositoryHash || catalogHash != approved.CatalogHash {
		return fmt.Errorf("host_update_plan_stale")
	}
	plan, err := client.HostPreview(ctx, repos, approved.Selection)
	if err != nil {
		return err
	}
	if plan.Digest != approved.Transaction.Digest {
		return fmt.Errorf("host_update_plan_stale")
	}
	if err = client.VerifyInstalled(ctx, plan, inventory); err != nil {
		return err
	}
	if err = client.VerifyCandidates(ctx, plan, repos); err != nil {
		return err
	}
	if err = verifyPackagedSylve(ctx, client, plan, inventory, installed.Path); err != nil {
		return err
	}
	if len(approved.Selection) > 0 && client.HostRebootRequired {
		return fmt.Errorf("host_base_selection_requires_all")
	}
	needsReboot := client.HostRebootRequired
	for _, change := range plan.Changes {
		if strings.HasPrefix(change.Name, "FreeBSD-") || pkgmanager.Classify(change.Name, "") == pkgmanager.KindKernel {
			needsReboot = true
		}
	}
	if op.BootEnvironment {
		if err = s.phase(op, "snapshotting"); err != nil {
			return err
		}
		if _, err = s.command(ctx, "/sbin/bectl", "check"); err != nil {
			return fmt.Errorf("boot_environment_unavailable")
		}
		name := fmt.Sprintf("sylve-before-%s", op.UUID[:8])
		if _, err = s.command(ctx, "/sbin/bectl", "create", "-r", name); err != nil {
			return fmt.Errorf("boot_environment_creation_failed: %w", err)
		}
		result.bootEnvironment = name
		_, _ = fmt.Fprintf(log, "Pre-update boot environment: %s. This does not back up separate data datasets or EFI partitions.\n", name)
	}
	if needsReboot {
		result.rebootBootID, err = s.command(ctx, "/sbin/sysctl", "-n", "kern.boottime")
		result.rebootBootID = bootIdentity(result.rebootBootID)
		if err != nil || result.rebootBootID == "" {
			return fmt.Errorf("host_boot_identity_unavailable")
		}
	}
	if err = s.phase(op, "applying"); err != nil {
		return err
	}
	if err = client.Apply(applyCtx, pkgmanager.UpgradeArgs(repos, approved.Selection, false), approved.Transaction); err != nil {
		return err
	}
	afterCtx, cancelAfter := context.WithTimeout(applyCtx, 2*time.Minute)
	defer cancelAfter()
	after, err := client.Inventory(afterCtx)
	if err != nil {
		return err
	}
	if err = pkgmanager.ValidateHostResult(plan, after); err != nil {
		return err
	}
	result.executableHash, err = sylveupdate.FileDigest(installed.Path)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(log, "Review package messages and .pkgnew/.pkgsave files. Restart affected services as necessary. No host reboot or bootcode installation was performed.")
	return nil
}

func (s *Service) applyRelease(ctx context.Context, op *models.HostUpdateOperation, plan models.HostUpdatePlan, installed sylveupdate.Installed, result *applyResult, log io.Writer) error {
	if plan.Release == nil {
		return fmt.Errorf("release_metadata_invalid")
	}
	if err := sylveupdate.Allowed(installed, *plan.Release); err != nil {
		return err
	}
	if installed.Path != "/usr/local/sbin/sylve" {
		return fmt.Errorf("sylve_standalone_path_required")
	}
	if err := trustedExecutable(installed.Path); err != nil {
		return err
	}
	if err := pkgmanager.ValidateTarget(ctx, pkgmanager.NewHostClient(nil).Target); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	client := s.client(log)
	registered, owner, err := s.ownership(ctx, client, installed.Path)
	if err != nil {
		return fmt.Errorf("sylve_package_ownership_unknown")
	}
	if registered || owner != "" {
		return fmt.Errorf("sylve_package_managed")
	}
	userland, err := s.command(ctx, "/bin/freebsd-version", "-u")
	if err != nil || !sylveupdate.Compatible(userland, plan.Release.FreeBSD) {
		return fmt.Errorf("sylve_release_requires_newer_freebsd")
	}
	stage, err := os.MkdirTemp(filepath.Dir(installed.Path), ".sylve-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	staged := filepath.Join(stage, "sylve")
	if err = s.phase(op, "downloading"); err != nil {
		return err
	}
	if err = s.releases.Download(ctx, *plan.Release, staged); err != nil {
		return err
	}
	current, err := sylveupdate.FileDigest(installed.Path)
	if err != nil || current != plan.ExecutableHash {
		return fmt.Errorf("sylve_executable_changed_restart_required")
	}
	registered, owner, err = s.ownership(ctx, client, installed.Path)
	if err != nil || registered || owner != "" {
		return fmt.Errorf("sylve_package_managed")
	}
	if err = os.Chmod(staged, 0555); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = s.phase(op, "applying"); err != nil {
		return err
	}
	if err = os.Rename(staged, installed.Path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(installed.Path))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return err
	}
	result.executableHash = plan.Release.SHA256
	_, _ = fmt.Fprintf(log, "Installed %s %s (%s). Sylve will restart; replication-protected guests may stop or fail over. No automatic downgrade/rollback is performed.\n", plan.Release.Channel, plan.Release.Version, plan.Release.Commit)
	return nil
}

func (s *Service) completeApply(ctx context.Context, op models.HostUpdateOperation, plan models.HostUpdatePlan, message string) (string, error) {
	if plan.Kind == "sylve" {
		if plan.Release == nil {
			return "", fmt.Errorf("release_metadata_invalid")
		}
		builtAt, err := sylveupdate.ParseBuildTime(s.builtAt)
		targetTime, targetErr := sylveupdate.ParseBuildTime(plan.Release.BuiltAt)
		if s.runningDigest != plan.Release.SHA256 || s.version != plan.Release.Version || s.commit != plan.Release.Commit || err != nil || targetErr != nil || !builtAt.Equal(targetTime) {
			return "", fmt.Errorf("sylve_startup_identity_mismatch")
		}
		if err = s.DB.Model(&models.HostUpdateState{}).Where("id = 1").Updates(map[string]any{"installed_channel": plan.Release.Channel, "installed_digest": s.runningDigest, "installed_built_at": plan.Release.BuiltAt}).Error; err != nil {
			return "", err
		}
		return "Sylve was updated and restarted successfully.", nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	client := s.client(nil)
	inventory, err := client.Inventory(ctx)
	if err == nil {
		var report pkgmanager.Audit
		report, err = client.Audit(ctx, inventory, true)
		if err == nil {
			err = s.storeAudit(inventory, report, op.ID)
		}
	}
	if err != nil {
		message += " Follow-up audit unavailable: " + pkgmanager.Redact(err.Error())
		_ = s.failedCheck("audit", err)
	}
	return message, nil
}

func (s *Service) completeRestart(op models.HostUpdateOperation) {
	var plan models.HostUpdatePlan
	message := op.Result
	err := s.DB.First(&plan, "id = ?", op.PlanID).Error
	if err == nil {
		actual, digestErr := sylveupdate.FileDigest(s.executable)
		if digestErr != nil || op.RestartDigest != s.runningDigest || actual != s.runningDigest || plan.Kind+"-apply" != op.Action {
			err = fmt.Errorf("sylve_startup_identity_mismatch")
		} else {
			message, err = s.completeApply(s.context, op, plan, message)
		}
	}
	s.finish(op, err, message)
}

func (s *Service) reconcileAudits() error {
	if s.Telemetry == nil {
		return nil
	}
	var records []infoModels.AuditRecord
	if err := s.Telemetry.Where("async_job_type = ? AND status = ?", "host_update", "pending").Find(&records).Error; err != nil {
		return err
	}
	for _, record := range records {
		if record.AsyncJobID == nil {
			continue
		}
		var op models.HostUpdateOperation
		err := s.DB.First(&op, *record.AsyncJobID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if op.FinishedAt != nil && record.AsyncOperationID == "host-update:"+op.UUID {
			if err := db.FinalizeAsyncAuditOperation(s.Telemetry, db.AsyncAuditRef{RecordID: record.ID, OperationID: record.AsyncOperationID}, op.Status, op.Error, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) Recover() error {
	if err := s.reconcileAudits(); err != nil {
		return err
	}
	var operations []models.HostUpdateOperation
	if err := s.DB.Where("active IS NOT NULL").Find(&operations).Error; err != nil {
		return err
	}
	for _, op := range operations {
		if op.Phase == "restarting" && op.RestartDigest != "" {
			go s.completeRestart(op)
			continue
		}
		if strings.HasSuffix(op.Action, "-apply") {
			if err := s.DB.Model(&models.HostUpdateState{}).Where("id = 1").Updates(map[string]any{"check_inventory_hash": "", "audit_inventory_hash": ""}).Error; err != nil {
				return err
			}
		}
		s.finish(op, fmt.Errorf("host_update_interrupted: inspect installed state and logs before requesting a fresh check or preview; no automatic retry"), "")
	}
	return nil
}
