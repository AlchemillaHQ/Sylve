// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func bootstrapName(spec jailServiceInterfaces.BootstrapTypeSpec, major, minor int) string {
	return fmt.Sprintf(spec.Name, major, minor)
}

func bootstrapLabel(spec jailServiceInterfaces.BootstrapTypeSpec, major int) string {
	return fmt.Sprintf(spec.Label, major)
}

func (s *Service) ListBootstraps(ctx context.Context, pool string) ([]jailServiceInterfaces.BootstrapEntry, error) {
	var entries []jailServiceInterfaces.BootstrapEntry

	for _, ver := range jailServiceInterfaces.SupportedVersions {
		for _, bt := range jailServiceInterfaces.BootstrapTypes {
			name := bootstrapName(bt, ver.Major, ver.Minor)
			dataset := fmt.Sprintf("%s/sylve/bootstraps/%s", pool, name)
			mountPoint := fmt.Sprintf("/%s/sylve/bootstraps/%s", pool, name)

			entry := jailServiceInterfaces.BootstrapEntry{
				Pool:       pool,
				Name:       name,
				Label:      bootstrapLabel(bt, ver.Major),
				Dataset:    dataset,
				MountPoint: mountPoint,
				Major:      ver.Major,
				Minor:      ver.Minor,
				Type:       bt.Type,
			}

			ds, _ := s.GZFS.ZFS.Get(ctx, dataset, false)
			entry.Exists = ds != nil

			var record jailModels.JailBootstrap
			s.DB.
				Where("pool = ? AND major = ? AND minor = ? AND bootstrap_type = ?",
					pool, ver.Major, ver.Minor, bt.Type).
				Limit(1).Find(&record)
			if record.ID != 0 {
				entry.Status = record.Status
				entry.Phase = record.Phase
				entry.Error = record.Error
			} else if entry.Exists {
				entry.Status = "completed"
			}

			entries = append(entries, entry)
		}
	}

	return entries, nil
}

func (s *Service) CreateBootstrap(ctx context.Context, req jailServiceInterfaces.BootstrapRequest) error {
	versionSupported := false
	for _, v := range jailServiceInterfaces.SupportedVersions {
		if v.Major == req.Major && v.Minor == req.Minor {
			versionSupported = true
			break
		}
	}

	if !versionSupported {
		return fmt.Errorf("unsupported_bootstrap_version: %d.%d", req.Major, req.Minor)
	}

	var typeSpec *jailServiceInterfaces.BootstrapTypeSpec
	for _, bt := range jailServiceInterfaces.BootstrapTypes {
		if bt.Type == req.Type {
			cp := bt
			typeSpec = &cp
			break
		}
	}
	if typeSpec == nil {
		return fmt.Errorf("unsupported_bootstrap_type: %s", req.Type)
	}

	keyDir := fmt.Sprintf("/usr/share/keys/pkgbase-%d/trusted", req.Major)
	if _, err := os.Stat(keyDir); os.IsNotExist(err) {
		return fmt.Errorf("pkgbase_signing_keys_not_found: %s", keyDir)
	}
	if _, err := exec.LookPath("pkg"); err != nil {
		return fmt.Errorf("pkg_not_found")
	}

	pools, err := s.System.GetUsablePools(ctx)
	if err != nil {
		return fmt.Errorf("failed_to_get_usable_pools: %w", err)
	}
	poolFound := false
	for _, p := range pools {
		if p.Name == req.Pool {
			poolFound = true
			break
		}
	}
	if !poolFound {
		return fmt.Errorf("pool_not_found")
	}

	name := bootstrapName(*typeSpec, req.Major, req.Minor)
	dataset := fmt.Sprintf("%s/sylve/bootstraps/%s", req.Pool, name)
	mountPoint := fmt.Sprintf("/%s/sylve/bootstraps/%s", req.Pool, name)
	lockKey := fmt.Sprintf("%s:%s", req.Pool, name)

	if _, loaded := s.bootstrapActiveMu.LoadOrStore(lockKey, true); loaded {
		return fmt.Errorf("bootstrap_already_in_progress")
	}

	var record jailModels.JailBootstrap
	s.DB.Where("pool = ? AND major = ? AND minor = ? AND bootstrap_type = ?",
		req.Pool, req.Major, req.Minor, req.Type).Limit(1).Find(&record)

	if record.ID != 0 {
		switch record.Status {
		case "running", "pending":
			s.bootstrapActiveMu.Delete(lockKey)
			return fmt.Errorf("bootstrap_already_in_progress")
		case "completed":
			s.bootstrapActiveMu.Delete(lockKey)
			return nil
		default:
			if err := s.DB.Model(&record).Updates(map[string]interface{}{
				"status": "pending",
				"phase":  "",
				"error":  "",
			}).Error; err != nil {
				s.bootstrapActiveMu.Delete(lockKey)
				return fmt.Errorf("failed_to_reset_bootstrap_record: %w", err)
			}
		}
	} else {
		record = jailModels.JailBootstrap{
			Pool:          req.Pool,
			Dataset:       dataset,
			MountPoint:    mountPoint,
			Name:          name,
			Major:         req.Major,
			Minor:         req.Minor,
			BootstrapType: req.Type,
			Status:        "pending",
		}
		if err := s.DB.Create(&record).Error; err != nil {
			s.bootstrapActiveMu.Delete(lockKey)
			return fmt.Errorf("failed_to_create_bootstrap_record: %w", err)
		}
	}

	go s.runBootstrap(record.ID, lockKey, req, *typeSpec, dataset, mountPoint, name)
	return nil
}

func (s *Service) updateBootstrapRecord(id uint, status, phase, errMsg string) {
	if err := s.DB.Model(&jailModels.JailBootstrap{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status": status,
		"phase":  phase,
		"error":  errMsg,
	}).Error; err != nil {
		logger.L.Error().Err(err).Msgf("bootstrap: failed to update record %d", id)
	}
}

func (s *Service) runBootstrap(
	recordID uint,
	lockKey string,
	req jailServiceInterfaces.BootstrapRequest,
	typeSpec jailServiceInterfaces.BootstrapTypeSpec,
	dataset, mountPoint, name string,
) {
	defer s.bootstrapActiveMu.Delete(lockKey)

	bCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	tempDir, err := os.MkdirTemp("", "sylve-bootstrap-*")
	if err != nil {
		s.updateBootstrapRecord(recordID, "failed", "", fmt.Sprintf("failed_to_create_temp_dir: %s", err.Error()))
		return
	}
	defer os.RemoveAll(tempDir)

	datasetCreated := false
	failStep := func(phase string, err error) {
		logger.L.Error().Err(err).Msgf("bootstrap %s: failed at phase %s", name, phase)
		if datasetCreated {
			if ds, dErr := s.GZFS.ZFS.Get(bCtx, dataset, false); dErr == nil && ds != nil {
				if dErr := ds.Destroy(bCtx, true, false); dErr != nil {
					logger.L.Warn().Err(dErr).Msgf("bootstrap %s: failed to destroy partial dataset %s", name, dataset)
				}
			}
		}
		s.updateBootstrapRecord(recordID, "failed", phase, err.Error())
	}

	arch, err := utils.RunCommandWithContext(bCtx, "sysctl", "-n", "hw.machine_arch")
	if err != nil {
		failStep("pre_check", fmt.Errorf("failed_to_get_arch: %w", err))
		return
	}
	arch = strings.TrimSpace(arch)

	abi := fmt.Sprintf("FreeBSD:%d:%s", req.Major, arch)
	osVersion := fmt.Sprintf("%d00000", req.Major)
	repoName := fmt.Sprintf("FreeBSD-base-release-%d", req.Minor)
	repoURL := fmt.Sprintf("pkg+https://pkg.freebsd.org/${ABI}/base_release_%d", req.Minor)
	fingerprintsRelPath := fmt.Sprintf("/usr/share/keys/pkgbase-%d", req.Major)

	s.updateBootstrapRecord(recordID, "running", "creating_dataset", "")
	parentDataset := fmt.Sprintf("%s/sylve/bootstraps", req.Pool)
	if pds, _ := s.GZFS.ZFS.Get(bCtx, parentDataset, false); pds == nil {
		if _, err = s.GZFS.ZFS.CreateFilesystem(bCtx, parentDataset, nil); err != nil {
			failStep("creating_dataset", fmt.Errorf("failed_to_create_parent_dataset: %w", err))
			return
		}
	}
	_, err = s.GZFS.ZFS.CreateFilesystem(bCtx, dataset, nil)
	if err != nil {
		failStep("creating_dataset", fmt.Errorf("failed_to_create_dataset: %w", err))
		return
	}
	datasetCreated = true

	s.updateBootstrapRecord(recordID, "running", "copying_keys", "")
	hostKeyDir := fmt.Sprintf("/usr/share/keys/pkgbase-%d", req.Major)
	jailKeyDir := filepath.Join(mountPoint, "usr", "share", "keys", fmt.Sprintf("pkgbase-%d", req.Major))
	if err = os.MkdirAll(filepath.Dir(jailKeyDir), 0755); err != nil {
		failStep("copying_keys", fmt.Errorf("failed_to_create_key_parent_dir: %w", err))
		return
	}
	if _, err = utils.RunCommandWithContext(bCtx, "cp", "-a", hostKeyDir, filepath.Dir(jailKeyDir)+"/"); err != nil {
		failStep("copying_keys", fmt.Errorf("failed_to_copy_signing_keys: %w", err))
		return
	}

	s.updateBootstrapRecord(recordID, "running", "writing_repo_conf", "")
	repoConfDir := filepath.Join(tempDir, "repo")
	if err = os.MkdirAll(repoConfDir, 0755); err != nil {
		failStep("writing_repo_conf", fmt.Errorf("failed_to_create_repo_conf_dir: %w", err))
		return
	}
	repoConf := fmt.Sprintf(`%s: {
    url:              "%s",
    mirror_type:      "srv",
    signature_type:   "fingerprints",
    fingerprints:     "%s",
    enabled:          yes
}
`, repoName, repoURL, fingerprintsRelPath)
	repoConfPath := filepath.Join(repoConfDir, repoName+".conf")
	if err = os.WriteFile(repoConfPath, []byte(repoConf), 0644); err != nil {
		failStep("writing_repo_conf", fmt.Errorf("failed_to_write_repo_conf: %w", err))
		return
	}

	pkgArgs := func(subcmd ...string) []string {
		base := []string{
			"--rootdir", mountPoint,
			"--repo-conf-dir", repoConfDir,
			"-o", "IGNORE_OSVERSION=yes",
			"-o", "OSVERSION=" + osVersion,
			"-o", fmt.Sprintf("VERSION_MAJOR=%d", req.Major),
			"-o", fmt.Sprintf("VERSION_MINOR=%d", req.Minor),
			"-o", "ABI=" + abi,
			"-o", "ASSUME_ALWAYS_YES=yes",
			"-o", "FINGERPRINTS=" + fingerprintsRelPath,
			"-o", "PKG_DBDIR=" + filepath.Join(tempDir, "pkg-db"),
			"-o", "INSTALL_AS_USER=yes",
		}
		return append(base, subcmd...)
	}

	s.updateBootstrapRecord(recordID, "running", "updating_repo", "")
	if _, err = utils.RunCommandWithContext(bCtx, "pkg", pkgArgs("update", "-r", repoName)...); err != nil {
		failStep("updating_repo", fmt.Errorf("failed_to_update_repo: %w", err))
		return
	}

	s.updateBootstrapRecord(recordID, "running", "installing", "")
	if _, err = utils.RunCommandWithContext(bCtx, "pkg", pkgArgs("install", "-r", repoName, typeSpec.PkgSet)...); err != nil {
		failStep("installing", fmt.Errorf("failed_to_install_packages: %w", err))
		return
	}

	if _, err = utils.RunCommandWithContext(bCtx, "pkg", pkgArgs("install", "pkg")...); err != nil {
		failStep("installing", fmt.Errorf("failed_to_install_pkg: %w", err))
		return
	}

	s.updateBootstrapRecord(recordID, "running", "writing_config", "")

	_ = os.WriteFile(filepath.Join(mountPoint, "root", ".hushlogin"), []byte(""), 0644)

	skelDir := filepath.Join(mountPoint, "usr", "share", "skel")
	_ = os.MkdirAll(skelDir, 0755)
	_ = os.WriteFile(filepath.Join(skelDir, "dot.hushlogin"), []byte(""), 0644)

	rcConf := ""
	if err = os.WriteFile(filepath.Join(mountPoint, "etc", "rc.conf"), []byte(rcConf), 0644); err != nil {
		failStep("writing_config", fmt.Errorf("failed_to_write_rc_conf: %w", err))
		return
	}

	if err = os.WriteFile(filepath.Join(mountPoint, "etc", "fstab"), []byte(""), 0644); err != nil {
		failStep("writing_config", fmt.Errorf("failed_to_write_fstab: %w", err))
		return
	}

	pkgRepoDir := filepath.Join(mountPoint, "usr", "local", "etc", "pkg", "repos")
	if err = os.MkdirAll(pkgRepoDir, 0755); err != nil {
		failStep("writing_config", fmt.Errorf("failed_to_create_pkg_repo_dir: %w", err))
		return
	}
	baseRepoConf := fmt.Sprintf(`FreeBSD-base: {
  url: "pkg+https://pkg.FreeBSD.org/${ABI}/base_release_%d",
  mirror_type: "srv",
  signature_type: "fingerprints",
  fingerprints: "/usr/share/keys/pkgbase-%d",
  enabled: yes
}
`, req.Minor, req.Major)
	if err = os.WriteFile(filepath.Join(pkgRepoDir, "FreeBSD-base.conf"), []byte(baseRepoConf), 0644); err != nil {
		failStep("writing_config", fmt.Errorf("failed_to_write_base_repo_conf: %w", err))
		return
	}

	if srcResolv, rErr := os.ReadFile("/etc/resolv.conf"); rErr == nil {
		_ = os.WriteFile(filepath.Join(mountPoint, "etc", "resolv.conf"), srcResolv, 0644)
	}

	s.updateBootstrapRecord(recordID, "completed", "", "")
	logger.L.Info().Msgf("bootstrap %s: completed successfully", name)
}

func (s *Service) DeleteBootstrap(ctx context.Context, pool, name string) error {
	var record jailModels.JailBootstrap
	s.DB.Where("pool = ? AND name = ?", pool, name).Limit(1).Find(&record)

	if record.ID != 0 {
		if record.Status == "running" || record.Status == "pending" {
			return fmt.Errorf("bootstrap_in_progress")
		}
	}

	dataset := fmt.Sprintf("%s/sylve/bootstraps/%s", pool, name)
	ds, _ := s.GZFS.ZFS.Get(ctx, dataset, false)
	if ds != nil {
		if err := ds.Destroy(ctx, true, false); err != nil {
			return fmt.Errorf("failed_to_destroy_bootstrap_dataset: %w", err)
		}
	}

	if record.ID != 0 {
		if err := s.DB.Delete(&record).Error; err != nil {
			return fmt.Errorf("failed_to_delete_bootstrap_record: %w", err)
		}
	}

	return nil
}

func (s *Service) RecoverInterruptedBootstraps(ctx context.Context) {
	var stale []jailModels.JailBootstrap
	if err := s.DB.Where("status IN ?", []string{"running", "pending"}).Find(&stale).Error; err != nil {
		logger.L.Error().Err(err).Msg("bootstrap recovery: failed to query stale records")
		return
	}

	for _, b := range stale {
		logger.L.Warn().Msgf("bootstrap recovery: found interrupted bootstrap %s (pool=%s, status=%s) — cleaning up", b.Name, b.Pool, b.Status)

		if ds, dErr := s.GZFS.ZFS.Get(ctx, b.Dataset, false); dErr == nil && ds != nil {
			if dErr := ds.Destroy(ctx, true, false); dErr != nil {
				if !strings.Contains(strings.ToLower(dErr.Error()), "does not exist") {
					logger.L.Warn().Err(dErr).Msgf("bootstrap recovery: failed to destroy partial dataset %s", b.Dataset)
				}
			}
		}

		if err := s.DB.Model(&b).Updates(map[string]interface{}{
			"status": "failed",
			"phase":  "",
			"error":  "interrupted_by_server_restart",
		}).Error; err != nil {
			logger.L.Error().Err(err).Msgf("bootstrap recovery: failed to update record %d", b.ID)
		}
	}
}
