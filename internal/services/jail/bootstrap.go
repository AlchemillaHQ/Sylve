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
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	sysctl "github.com/alchemillahq/sylve/pkg/utils/sysctl"
)

const bootstrapCleanupTimeout = 2 * time.Minute

type bootstrapIdentity struct {
	Pool       string
	Name       string
	Dataset    string
	MountPoint string
	Major      int
	Minor      int
	Type       string
}

func bootstrapName(spec jailServiceInterfaces.BootstrapTypeSpec, major, minor int) string {
	return fmt.Sprintf(spec.Name, major, minor)
}

func bootstrapLabel(spec jailServiceInterfaces.BootstrapTypeSpec, major, minor int) string {
	return fmt.Sprintf(spec.Label, major, minor)
}

func normalizeBootstrapPool(pool string) (string, error) {
	pool = strings.TrimSpace(pool)
	if pool == "" || strings.Contains(pool, "/") {
		return "", fmt.Errorf("invalid_bootstrap_pool")
	}
	return pool, nil
}

func canonicalBootstrapIdentity(pool, name string) (bootstrapIdentity, error) {
	pool, err := normalizeBootstrapPool(pool)
	if err != nil {
		return bootstrapIdentity{}, err
	}

	name = strings.TrimSpace(name)
	for _, version := range jailServiceInterfaces.SupportedVersions {
		for _, bootstrapType := range jailServiceInterfaces.BootstrapTypes {
			canonicalName := bootstrapName(bootstrapType, version.Major, version.Minor)
			if name != canonicalName {
				continue
			}

			root := pool + "/sylve/bootstraps"
			dataset := root + "/" + canonicalName
			if !strings.HasPrefix(dataset, root+"/") || strings.TrimPrefix(dataset, root+"/") != canonicalName {
				return bootstrapIdentity{}, fmt.Errorf("invalid_bootstrap_name")
			}

			return bootstrapIdentity{
				Pool:       pool,
				Name:       canonicalName,
				Dataset:    dataset,
				MountPoint: "/" + dataset,
				Major:      version.Major,
				Minor:      version.Minor,
				Type:       bootstrapType.Type,
			}, nil
		}
	}

	return bootstrapIdentity{}, fmt.Errorf("invalid_bootstrap_name")
}

func bootstrapRecordMatchesIdentity(record jailModels.JailBootstrap, identity bootstrapIdentity) bool {
	return record.Pool == identity.Pool &&
		record.Name == identity.Name &&
		record.Dataset == identity.Dataset &&
		record.MountPoint == identity.MountPoint &&
		record.Major == identity.Major &&
		record.Minor == identity.Minor &&
		record.BootstrapType == identity.Type
}

func bootstrapCleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), bootstrapCleanupTimeout)
}

func isMissingBootstrapDatasetError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "dataset does not exist") ||
		strings.Contains(message, "no such dataset")
}

func (s *Service) requireUsableBootstrapPool(ctx context.Context, pool string) (string, error) {
	pool, err := normalizeBootstrapPool(pool)
	if err != nil {
		return "", err
	}
	if s.System == nil {
		return "", fmt.Errorf("bootstrap_system_service_unavailable")
	}

	pools, err := s.System.GetUsablePools(ctx)
	if err != nil {
		return "", fmt.Errorf("failed_to_get_usable_pools: %w", err)
	}
	for _, candidate := range pools {
		if candidate != nil && candidate.Name == pool {
			return pool, nil
		}
	}

	return "", fmt.Errorf("pool_not_found")
}

func (s *Service) getBootstrapDataset(
	ctx context.Context,
	identity bootstrapIdentity,
) (*gzfs.Dataset, error) {
	if s.GZFS == nil {
		return nil, fmt.Errorf("bootstrap_zfs_service_unavailable")
	}
	dataset, err := s.GZFS.ZFS.Get(ctx, identity.Dataset, false)
	if err != nil {
		if isMissingBootstrapDatasetError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed_to_get_bootstrap_dataset: %s: %w", identity.Name, err)
	}
	if dataset == nil {
		return nil, nil
	}
	if strings.TrimSpace(strings.Trim(dataset.Name, "/")) != identity.Dataset {
		return nil, fmt.Errorf("invalid_resolved_bootstrap_dataset: %s", dataset.Name)
	}
	return dataset, nil
}

func parseBootstrapHostVersion(release string) (jailServiceInterfaces.SupportedBootstrapVersion, error) {
	release = strings.TrimSpace(release)
	version := jailServiceInterfaces.SupportedBootstrapVersion{}
	if release == "" {
		return version, fmt.Errorf("host_release_empty")
	}
	base := strings.SplitN(release, "-", 2)[0]
	parts := strings.Split(base, ".")
	if len(parts) < 2 {
		return version, fmt.Errorf("unexpected_host_release_format:%s", release)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return version, fmt.Errorf("unexpected_host_release_major:%s", release)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 0 {
		return version, fmt.Errorf("unexpected_host_release_minor:%s", release)
	}
	version.Major = major
	version.Minor = minor
	return version, nil
}

func bootstrapVersionAllowedOnHost(requested, host jailServiceInterfaces.SupportedBootstrapVersion) bool {
	return requested.Major < host.Major ||
		(requested.Major == host.Major && requested.Minor <= host.Minor)
}

func (s *Service) bootstrapHostVersion() (jailServiceInterfaces.SupportedBootstrapVersion, error) {
	var release string
	var err error
	if s != nil && s.bootstrapHostReleaseFn != nil {
		release, err = s.bootstrapHostReleaseFn()
	} else {
		release, err = sysctl.GetString("kern.osrelease")
	}
	if err != nil {
		return jailServiceInterfaces.SupportedBootstrapVersion{}, fmt.Errorf("read_kern_osrelease_failed: %w", err)
	}
	return parseBootstrapHostVersion(release)
}

func (s *Service) requireBootstrapVersionCompatible(major, minor int) error {
	host, err := s.bootstrapHostVersion()
	if err != nil {
		return fmt.Errorf("failed_to_determine_host_freebsd_version: %w", err)
	}
	requested := jailServiceInterfaces.SupportedBootstrapVersion{Major: major, Minor: minor}
	if !bootstrapVersionAllowedOnHost(requested, host) {
		return fmt.Errorf(
			"bootstrap_version_newer_than_host:requested=%d.%d,host=%d.%d",
			major, minor, host.Major, host.Minor,
		)
	}
	return nil
}

func (s *Service) ListBootstraps(ctx context.Context, pool string) ([]jailServiceInterfaces.BootstrapEntry, error) {
	pool, err := s.requireUsableBootstrapPool(ctx, pool)
	if err != nil {
		return nil, err
	}

	entries := make([]jailServiceInterfaces.BootstrapEntry, 0)
	hostVersion, err := s.bootstrapHostVersion()
	if err != nil {
		return nil, fmt.Errorf("failed_to_determine_host_freebsd_version: %w", err)
	}

	var records []jailModels.JailBootstrap
	if err := s.DB.Where("pool = ?", pool).Find(&records).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_bootstrap_records: %w", err)
	}
	recordsByName := make(map[string]jailModels.JailBootstrap, len(records))
	for _, record := range records {
		recordsByName[record.Name] = record
	}

	for _, ver := range jailServiceInterfaces.SupportedVersions {
		if !bootstrapVersionAllowedOnHost(ver, hostVersion) {
			continue
		}
		for _, bt := range jailServiceInterfaces.BootstrapTypes {
			name := bootstrapName(bt, ver.Major, ver.Minor)
			identity, identityErr := canonicalBootstrapIdentity(pool, name)
			if identityErr != nil {
				return nil, identityErr
			}

			entry := jailServiceInterfaces.BootstrapEntry{
				Pool:       pool,
				Name:       name,
				Label:      bootstrapLabel(bt, ver.Major, ver.Minor),
				Dataset:    identity.Dataset,
				MountPoint: identity.MountPoint,
				Major:      ver.Major,
				Minor:      ver.Minor,
				Type:       bt.Type,
			}

			ds, err := s.getBootstrapDataset(ctx, identity)
			if err != nil {
				return nil, err
			}
			entry.Exists = ds != nil

			if record, ok := recordsByName[name]; ok {
				if !bootstrapRecordMatchesIdentity(record, identity) {
					entry.Status = "failed"
					entry.Error = "bootstrap_record_mismatch"
					entries = append(entries, entry)
					continue
				}
				entry.Status = record.Status
				entry.Phase = record.Phase
				entry.Error = record.Error
				switch record.Status {
				case "pending", "running", "completed", "failed":
				default:
					entry.Status = "failed"
					entry.Phase = ""
					entry.Error = "bootstrap_invalid_status"
				}
				if record.Status == "completed" && !entry.Exists {
					entry.Status = "failed"
					entry.Phase = ""
					entry.Error = "bootstrap_dataset_missing"
				}
			} else if entry.Exists {
				entry.Status = "orphaned"
				entry.Error = "bootstrap_record_missing"
			}

			entries = append(entries, entry)
		}
	}

	return entries, nil
}

func (s *Service) CreateBootstrap(
	ctx context.Context,
	req jailServiceInterfaces.BootstrapRequest,
) (jailServiceInterfaces.BootstrapCreateResult, error) {
	var result jailServiceInterfaces.BootstrapCreateResult
	req.Type = strings.TrimSpace(req.Type)

	versionSupported := false
	for _, v := range jailServiceInterfaces.SupportedVersions {
		if v.Major == req.Major && v.Minor == req.Minor {
			versionSupported = true
			break
		}
	}

	if !versionSupported {
		return result, fmt.Errorf("unsupported_bootstrap_version: %d.%d", req.Major, req.Minor)
	}
	if err := s.requireBootstrapVersionCompatible(req.Major, req.Minor); err != nil {
		return result, err
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
		return result, fmt.Errorf("unsupported_bootstrap_type: %s", req.Type)
	}

	s.bootstrapUseMu.Lock()
	defer s.bootstrapUseMu.Unlock()

	pool, err := s.requireUsableBootstrapPool(ctx, req.Pool)
	if err != nil {
		return result, err
	}
	req.Pool = pool

	identity, err := canonicalBootstrapIdentity(
		pool,
		bootstrapName(*typeSpec, req.Major, req.Minor),
	)
	if err != nil {
		return result, err
	}
	result.Pool = identity.Pool
	result.Name = identity.Name

	lockKey := identity.Pool + ":" + identity.Name
	if _, active := s.bootstrapActiveMu.Load(lockKey); active {
		return result, fmt.Errorf("bootstrap_already_in_progress")
	}

	var record jailModels.JailBootstrap
	if err := s.DB.Where("pool = ? AND name = ?", identity.Pool, identity.Name).
		Limit(1).
		Find(&record).Error; err != nil {
		return result, fmt.Errorf("failed_to_get_bootstrap_record: %w", err)
	}

	if record.ID != 0 {
		if !bootstrapRecordMatchesIdentity(record, identity) {
			return result, fmt.Errorf("bootstrap_record_mismatch")
		}
		switch record.Status {
		case "running", "pending":
			return result, fmt.Errorf("bootstrap_already_in_progress")
		case "completed", "failed":
			// Reconciled against the physical dataset below.
		default:
			return result, fmt.Errorf("bootstrap_invalid_status: %s", record.Status)
		}
	}

	dataset, err := s.getBootstrapDataset(ctx, identity)
	if err != nil {
		return result, err
	}
	if record.ID == 0 && dataset != nil {
		return result, fmt.Errorf("bootstrap_dataset_unmanaged")
	}
	if record.ID != 0 && record.Status == "completed" && dataset != nil {
		result.Status = "completed"
		result.Outcome = "already_completed"
		return result, nil
	}
	if record.ID != 0 && record.Status == "failed" && dataset != nil {
		return result, fmt.Errorf("bootstrap_cleanup_required")
	}

	keyDir := fmt.Sprintf("/usr/share/keys/pkgbase-%d/trusted", req.Major)
	if _, err := os.Stat(keyDir); err != nil {
		if os.IsNotExist(err) {
			return result, fmt.Errorf("pkgbase_signing_keys_not_found: %s", keyDir)
		}
		return result, fmt.Errorf("failed_to_check_pkgbase_signing_keys: %w", err)
	}
	if _, err := exec.LookPath("pkg"); err != nil {
		return result, fmt.Errorf("pkg_not_found")
	}

	if _, loaded := s.bootstrapActiveMu.LoadOrStore(lockKey, true); loaded {
		return result, fmt.Errorf("bootstrap_already_in_progress")
	}

	if record.ID != 0 {
		if err := s.DB.Model(&record).Updates(map[string]interface{}{
			"pool":           identity.Pool,
			"dataset":        identity.Dataset,
			"mount_point":    identity.MountPoint,
			"name":           identity.Name,
			"major":          identity.Major,
			"minor":          identity.Minor,
			"bootstrap_type": identity.Type,
			"status":         "pending",
			"phase":          "",
			"error":          "",
		}).Error; err != nil {
			s.bootstrapActiveMu.Delete(lockKey)
			return result, fmt.Errorf("failed_to_reset_bootstrap_record: %w", err)
		}
	} else {
		record = jailModels.JailBootstrap{
			Pool:          identity.Pool,
			Dataset:       identity.Dataset,
			MountPoint:    identity.MountPoint,
			Name:          identity.Name,
			Major:         identity.Major,
			Minor:         identity.Minor,
			BootstrapType: identity.Type,
			Status:        "pending",
		}
		if err := s.DB.Create(&record).Error; err != nil {
			s.bootstrapActiveMu.Delete(lockKey)
			return result, fmt.Errorf("failed_to_create_bootstrap_record: %w", err)
		}
	}

	go s.runBootstrap(
		record.ID,
		lockKey,
		req,
		*typeSpec,
		identity.Dataset,
		identity.MountPoint,
		identity.Name,
	)
	result.Status = "pending"
	result.Outcome = "queued"
	return result, nil
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
		errMessage := err.Error()
		if datasetCreated {
			cleanupCtx, cleanupCancel := bootstrapCleanupContext()
			defer cleanupCancel()
			identity, identityErr := canonicalBootstrapIdentity(req.Pool, name)
			if identityErr != nil || identity.Dataset != dataset {
				cleanupErr := fmt.Errorf("invalid_bootstrap_cleanup_target")
				logger.L.Warn().Err(cleanupErr).Msgf("bootstrap %s: refused partial dataset cleanup", name)
				errMessage += ": cleanup_failed: " + cleanupErr.Error()
			} else if ds, dErr := s.getBootstrapDataset(cleanupCtx, identity); dErr != nil {
				logger.L.Warn().Err(dErr).Msgf("bootstrap %s: failed to inspect partial dataset %s", name, dataset)
				errMessage += ": cleanup_failed: " + dErr.Error()
			} else if ds != nil {
				if dErr := ds.Destroy(cleanupCtx, true, false); dErr != nil &&
					!isMissingBootstrapDatasetError(dErr) {
					logger.L.Warn().Err(dErr).Msgf("bootstrap %s: failed to destroy partial dataset %s", name, dataset)
					errMessage += ": cleanup_failed: " + dErr.Error()
				}
			}
		}
		s.updateBootstrapRecord(recordID, "failed", phase, errMessage)
	}

	arch, err := sysctl.GetString("hw.machine_arch")
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
	pds, parentGetErr := s.GZFS.ZFS.Get(bCtx, parentDataset, false)
	if parentGetErr != nil && !isMissingBootstrapDatasetError(parentGetErr) {
		failStep("creating_dataset", fmt.Errorf("failed_to_get_parent_dataset: %w", parentGetErr))
		return
	}
	if pds == nil {
		if _, err = s.GZFS.ZFS.CreateFilesystem(bCtx, parentDataset, nil); err != nil {
			// Different bootstrap types may both encounter a missing fallback
			// parent. Accept a concurrent creator only after an exact recheck.
			rechecked, recheckErr := s.GZFS.ZFS.Get(bCtx, parentDataset, false)
			if recheckErr != nil || rechecked == nil {
				failStep("creating_dataset", fmt.Errorf("failed_to_create_parent_dataset: %w", err))
				return
			}
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

func (s *Service) DeleteBootstrap(
	ctx context.Context,
	pool string,
	name string,
) (jailServiceInterfaces.BootstrapDeleteResult, error) {
	var result jailServiceInterfaces.BootstrapDeleteResult

	s.bootstrapUseMu.Lock()
	defer s.bootstrapUseMu.Unlock()

	pool, err := s.requireUsableBootstrapPool(ctx, pool)
	if err != nil {
		return result, err
	}
	identity, err := canonicalBootstrapIdentity(pool, name)
	if err != nil {
		return result, err
	}
	result.Pool = identity.Pool
	result.Name = identity.Name

	lockKey := identity.Pool + ":" + identity.Name
	if _, active := s.bootstrapActiveMu.Load(lockKey); active {
		return result, fmt.Errorf("bootstrap_already_in_progress")
	}

	var record jailModels.JailBootstrap
	if err := s.DB.Where("pool = ? AND name = ?", identity.Pool, identity.Name).
		Limit(1).
		Find(&record).Error; err != nil {
		return result, fmt.Errorf("failed_to_get_bootstrap_record: %w", err)
	}

	if record.ID != 0 {
		if record.Status == "running" || record.Status == "pending" {
			return result, fmt.Errorf("bootstrap_already_in_progress")
		}
	}

	ds, err := s.getBootstrapDataset(ctx, identity)
	if err != nil {
		return result, err
	}
	if ds != nil {
		if err := ds.Destroy(ctx, true, false); err != nil {
			if !isMissingBootstrapDatasetError(err) {
				return result, fmt.Errorf("failed_to_destroy_bootstrap_dataset: %w", err)
			}
		} else {
			result.DatasetDeleted = true
		}
	}

	if record.ID != 0 {
		if err := s.DB.Delete(&record).Error; err != nil {
			return result, fmt.Errorf("failed_to_delete_bootstrap_record: %w", err)
		}
		result.RecordDeleted = true
	}

	result.Outcome = "already_absent"
	if result.DatasetDeleted || result.RecordDeleted {
		result.Outcome = "deleted"
	}
	return result, nil
}

func (s *Service) RecoverInterruptedBootstraps(ctx context.Context) {
	s.bootstrapUseMu.Lock()
	defer s.bootstrapUseMu.Unlock()

	var stale []jailModels.JailBootstrap
	if err := s.DB.WithContext(ctx).
		Where("status IN ?", []string{"running", "pending"}).
		Find(&stale).Error; err != nil {
		logger.L.Error().Err(err).Msg("bootstrap recovery: failed to query stale records")
		return
	}

	for _, b := range stale {
		lockKey := strings.TrimSpace(b.Pool) + ":" + strings.TrimSpace(b.Name)
		if _, active := s.bootstrapActiveMu.Load(lockKey); active {
			continue
		}

		logger.L.Warn().Msgf("bootstrap recovery: found interrupted bootstrap %s (pool=%s, status=%s) — cleaning up", b.Name, b.Pool, b.Status)
		recoveryError := "interrupted_by_server_restart"

		identity, identityErr := canonicalBootstrapIdentity(b.Pool, b.Name)
		if identityErr != nil || !bootstrapRecordMatchesIdentity(b, identity) {
			recoveryError += ": invalid_bootstrap_record"
			unsafeErr := identityErr
			if unsafeErr == nil {
				unsafeErr = fmt.Errorf("bootstrap_record_mismatch")
			}
			logger.L.Warn().Err(unsafeErr).Msgf(
				"bootstrap recovery: refused unsafe dataset target for record %d",
				b.ID,
			)
		} else {
			cleanupCtx, cleanupCancel := bootstrapCleanupContext()
			ds, getErr := s.getBootstrapDataset(cleanupCtx, identity)
			if getErr != nil {
				recoveryError += ": cleanup_failed: " + getErr.Error()
				logger.L.Warn().Err(getErr).Msgf(
					"bootstrap recovery: failed to inspect partial dataset %s",
					identity.Dataset,
				)
			} else if ds != nil {
				if destroyErr := ds.Destroy(cleanupCtx, true, false); destroyErr != nil &&
					!isMissingBootstrapDatasetError(destroyErr) {
					recoveryError += ": cleanup_failed: " + destroyErr.Error()
					logger.L.Warn().Err(destroyErr).Msgf(
						"bootstrap recovery: failed to destroy partial dataset %s",
						identity.Dataset,
					)
				}
			}
			cleanupCancel()
		}

		if err := s.DB.Model(&b).Updates(map[string]interface{}{
			"status": "failed",
			"phase":  "",
			"error":  recoveryError,
		}).Error; err != nil {
			logger.L.Error().Err(err).Msgf("bootstrap recovery: failed to update record %d", b.ID)
		}
	}
}
