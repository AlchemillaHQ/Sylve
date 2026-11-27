// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package samba

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/network/interfaceref"
	"github.com/alchemillahq/sylve/internal/services/directoryservices"
	"github.com/alchemillahq/sylve/pkg/system"
	"github.com/alchemillahq/sylve/pkg/utils"

	iface "github.com/alchemillahq/sylve/pkg/network/iface"
	"gorm.io/gorm"
)

var ErrInvalidGlobalConfig = errors.New("invalid_samba_global_config")
var ErrInvalidShareConfig = errors.New("invalid_samba_share_config")

type invalidGlobalConfigError struct {
	detail error
}

func (e *invalidGlobalConfigError) Error() string {
	return e.detail.Error()
}

func (e *invalidGlobalConfigError) Unwrap() []error {
	return []error{ErrInvalidGlobalConfig, e.detail}
}

func invalidGlobalConfig(format string, args ...any) error {
	return &invalidGlobalConfigError{detail: fmt.Errorf(format, args...)}
}

const (
	// MaxRequestBodyBytes bounds JSON request processing while leaving ample room
	// for administrator-supplied global Samba configuration.
	MaxRequestBodyBytes int64 = 1 * 1024 * 1024

	sambaACLType    = "nfsv4"
	sambaACLMode    = "restricted"
	sambaACLInherit = "passthrough"
	guestACEName    = "everyone@"
	readACLPerm     = "read_set/execute"
	legacyReadPerm  = "read_set"
	writeACLPerm    = "modify_set"
)

var (
	sambaRunCommand        = utils.RunCommand
	sambaSupportedCharsets = utils.GetSupportedCharsets
	sambaGetInterface      = iface.Get
	sambaConfigFilePath    = "/usr/local/etc/smb4.conf"
	sambaTestparmPath      = "/usr/local/bin/testparm"
	sambaAtomicWriteFile   = utils.AtomicWriteFile
)

var allowedSambaAuditOperations = map[string]struct{}{
	"connect":     {},
	"disconnect":  {},
	"create_file": {},
	"mkdirat":     {},
	"unlinkat":    {},
	"renameat":    {},
	"openat":      {},
	"close":       {},
	"read":        {},
	"write":       {},
}

func validateSambaShareInput(name, createMask, directoryMask string, auditedOperations []string) error {
	if err := validateSambaShareName(name); err != nil {
		return err
	}

	validMask := func(mask string) bool {
		if len(mask) != 4 {
			return false
		}
		for _, char := range mask {
			if char < '0' || char > '7' {
				return false
			}
		}
		return true
	}

	if !validMask(createMask) {
		return fmt.Errorf("invalid_create_mask")
	}
	if !validMask(directoryMask) {
		return fmt.Errorf("invalid_directory_mask")
	}

	for _, operation := range auditedOperations {
		if !ValidAuditOperation(operation) {
			return fmt.Errorf("invalid_audit_operation: %s", operation)
		}
	}

	return nil
}

func validateExtraShareConfig(config string) error {
	if strings.ContainsRune(config, '\x00') {
		return fmt.Errorf("%w: extra share configuration cannot contain null bytes", ErrInvalidShareConfig)
	}
	for _, line := range strings.Split(config, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			return fmt.Errorf("%w: extra share configuration cannot contain section headers", ErrInvalidShareConfig)
		}
	}
	return nil
}

var ErrSambaNotInitialized = errors.New("samba_not_initialized")

func (s *Service) GetGlobalConfig() (sambaModels.SambaSettings, error) {
	var settings sambaModels.SambaSettings
	if err := s.DB.First(&settings).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return sambaModels.SambaSettings{}, ErrSambaNotInitialized
		}
		return sambaModels.SambaSettings{}, fmt.Errorf("failed to retrieve Samba settings: %w", err)
	}
	return settings, nil
}

func (s *Service) SetGlobalConfig(
	ctx context.Context,
	unixCharset string,
	workgroup string,
	serverString string,
	interfaces string,
	bindInterfacesOnly bool,
	appleExtensions bool,
	advertiseMdns bool,
	extraGlobalConfig *string) error {
	unlock, err := s.lockDirectoryConfiguration()
	if err != nil {
		return err
	}
	defer unlock()
	unlockInterfaceReferences := s.lockInterfaceReferencesRead()
	defer unlockInterfaceReferences()

	if unixCharset == "" || workgroup == "" || serverString == "" {
		return invalidGlobalConfig("unixCharset, workgroup, and serverString cannot be empty")
	}

	if interfaces == "" {
		interfaces = "lo0"
	}

	supportedCharsets := sambaSupportedCharsets()

	if !slices.Contains(supportedCharsets, unixCharset) {
		return invalidGlobalConfig("unsupported unixCharset: %s", unixCharset)
	}

	if !utils.IsValidWorkgroup(workgroup) {
		return invalidGlobalConfig("invalid workgroup name: %s", workgroup)
	}

	if !utils.IsValidServerString(serverString) || strings.ContainsAny(serverString, "\r\n\x00") ||
		strings.HasSuffix(serverString, `\`) {
		return invalidGlobalConfig("invalid server string: %s", serverString)
	}

	interfaceNames := make([]string, 0, 4)
	for _, name := range strings.Split(interfaces, ",") {
		if name = strings.TrimSpace(name); name != "" && !slices.Contains(interfaceNames, name) {
			interfaceNames = append(interfaceNames, name)
		}
	}
	for _, name := range interfaceNames {
		if _, err := sambaGetInterface(name); err != nil {
			if strings.Contains(err.Error(), "not found") {
				return invalidGlobalConfig("unknown interface: %s", name)
			}
			return invalidGlobalConfig("invalid interface '%s': %v", name, err)
		}
	}
	if len(interfaceNames) == 0 {
		interfaceNames = []string{"lo0"}
	}
	interfaces = strings.Join(interfaceNames, ",")
	if err := interfaceref.RejectFilteredStandardBridgeInterfaces(s.DB, interfaceNames...); err != nil {
		if errors.Is(err, interfaceref.ErrFilteredStandardSwitchL2Only) {
			return invalidGlobalConfig(
				"a VLAN-filtered Standard Switch base is layer 2 only; select its Host VLAN interface",
			)
		}
		return fmt.Errorf("failed to validate Samba interfaces: %w", err)
	}

	update := func() error {
		return s.DB.Transaction(func(tx *gorm.DB) error {
			var settings sambaModels.SambaSettings
			if err := tx.First(&settings).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrSambaNotInitialized
				}
				return fmt.Errorf("failed to retrieve Samba settings: %w", err)
			}

			enableMdns := advertiseMdns

			settings.UnixCharset = unixCharset
			settings.Workgroup = workgroup
			settings.ServerString = serverString
			settings.Interfaces = interfaces
			settings.BindInterfacesOnly = bindInterfacesOnly
			settings.AppleExtensions = appleExtensions
			settings.AdvertiseMdns = advertiseMdns
			if extraGlobalConfig != nil {
				settings.ExtraGlobalConfig = *extraGlobalConfig
			}

			if err := tx.Save(&settings).Error; err != nil {
				return fmt.Errorf("failed to update Samba settings: %w", err)
			}

			if enableMdns {
				if s.EnsureMdnsEnabled == nil {
					return fmt.Errorf("mdns service dependency is unavailable")
				}
				if err := s.EnsureMdnsEnabled(tx); err != nil {
					return fmt.Errorf("failed to enable mdns for Samba advertisements: %w", err)
				}
			}

			transactionalService := &Service{
				DB:                tx,
				ZFS:               s.ZFS,
				GZFS:              s.GZFS,
				Directory:         s.Directory,
				directoryOverride: s.directoryOverride,
			}
			if _, err := sambaRenderConfig(transactionalService, ctx); err != nil {
				if errors.Is(err, errSambaConfigRejected) {
					return invalidGlobalConfig("%v", err)
				}
				return err
			}
			return nil
		})
	}

	if s.WithServiceSettingsLock != nil {
		if err := s.WithServiceSettingsLock(update); err != nil {
			return err
		}
	} else if err := update(); err != nil {
		return err
	}

	if err := sambaWriteConfig(s, ctx, sambaRestartService); err != nil {
		return sambaApplyFailure(err)
	}
	if s.OnConfigChange != nil {
		if err := s.OnConfigChange(); err != nil {
			return sambaApplyFailure(fmt.Errorf("mdns rebuild failed after samba config change: %w", err))
		}
	}

	return nil
}

var sambaRenderConfig = func(s *Service, ctx context.Context) (string, error) {
	return s.renderConfig(ctx, false)
}

func (s *Service) hasGuestOnlyShares() (bool, error) {
	var count int64
	if err := s.DB.Model(&sambaModels.SambaShare{}).Where("enabled = ? AND guest_ok = ?", true, true).Count(&count).Error; err != nil {
		return false, fmt.Errorf("failed_to_check_guest_shares: %w", err)
	}

	return count > 0, nil
}

func isSambaDatasetMounted(dataset *gzfs.Dataset) bool {
	return dataset != nil && dataset.Type == gzfs.DatasetTypeFilesystem &&
		dataset.Mountpoint != "" && dataset.Mountpoint != "-" &&
		dataset.Properties["mounted"].Value == "yes"
}

func sambaACLPropertyChanges(dataset *gzfs.Dataset) []string {
	var changes []string
	for _, property := range [][2]string{
		{"acltype", sambaACLType},
		{"aclmode", sambaACLMode},
		{"aclinherit", sambaACLInherit},
	} {
		if current, ok := dataset.Properties[property[0]]; !ok || current.Value != property[1] {
			changes = append(changes, property[0], property[1])
		}
	}
	return changes
}

func (s *Service) ensureSambaDatasetACLProperties(
	ctx context.Context,
	dataset *gzfs.Dataset,
	strict bool,
) error {
	if dataset == nil {
		err := fmt.Errorf("dataset_not_found")
		if strict {
			return err
		}

		logger.L.Warn().Err(err).Msg("failed_to_enforce_samba_dataset_acl_properties")
		return nil
	}

	if dataset.Type != gzfs.DatasetTypeFilesystem {
		err := fmt.Errorf("dataset_not_filesystem: %s", dataset.Name)
		if strict {
			return err
		}

		logger.L.Warn().Err(err).Str("dataset", dataset.Name).Msg("failed_to_enforce_samba_dataset_acl_properties")
		return nil
	}

	changes := sambaACLPropertyChanges(dataset)
	if len(changes) == 0 {
		return nil
	}
	if err := dataset.SetProperties(ctx, changes...); err != nil {
		wrapped := fmt.Errorf("failed_to_set_samba_acl_properties_for_dataset_%s: %w", dataset.Name, err)
		if strict {
			return wrapped
		}

		logger.L.Warn().Err(wrapped).Str("dataset", dataset.Name).Msg("failed_to_enforce_samba_dataset_acl_properties")
	}

	return nil
}

func uniquePrincipalNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))

	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		if _, exists := seen[name]; exists {
			continue
		}

		seen[name] = struct{}{}
		out = append(out, name)
	}

	return out
}

func normalizeSambaPrincipalNames(input sambaPrincipalNames) sambaPrincipalNames {
	normalized := sambaPrincipalNames{
		ReadUsers:   uniquePrincipalNames(input.ReadUsers),
		WriteUsers:  uniquePrincipalNames(input.WriteUsers),
		ReadGroups:  uniquePrincipalNames(input.ReadGroups),
		WriteGroups: uniquePrincipalNames(input.WriteGroups),
	}

	writeUsers := make(map[string]struct{}, len(normalized.WriteUsers))
	for _, user := range normalized.WriteUsers {
		writeUsers[user] = struct{}{}
	}

	filteredReadUsers := make([]string, 0, len(normalized.ReadUsers))
	for _, user := range normalized.ReadUsers {
		if _, exists := writeUsers[user]; exists {
			continue
		}
		filteredReadUsers = append(filteredReadUsers, user)
	}
	normalized.ReadUsers = filteredReadUsers

	writeGroups := make(map[string]struct{}, len(normalized.WriteGroups))
	for _, group := range normalized.WriteGroups {
		writeGroups[group] = struct{}{}
	}

	filteredReadGroups := make([]string, 0, len(normalized.ReadGroups))
	for _, group := range normalized.ReadGroups {
		if _, exists := writeGroups[group]; exists {
			continue
		}
		filteredReadGroups = append(filteredReadGroups, group)
	}
	normalized.ReadGroups = filteredReadGroups

	return normalized
}

func mergePrincipalNames(lists ...[]string) []string {
	merged := make([]string, 0)
	for _, list := range lists {
		merged = append(merged, list...)
	}
	return uniquePrincipalNames(merged)
}

func (s *Service) GlobalConfig() (string, error) {
	settings, err := s.GetGlobalConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get global Samba settings: %w", err)
	}
	hasGuestShares, err := s.hasGuestOnlyShares()
	if err != nil {
		return "", err
	}
	directoryConfig, err := s.directoryConfig(settings.ExtraGlobalConfig)
	if err != nil {
		return "", err
	}
	return globalConfig(settings, hasGuestShares, directoryConfig), nil
}

func globalConfig(settings sambaModels.SambaSettings, hasGuestShares bool, directoryConfig string) string {
	var config string
	config += "# === This file is automatically generated by Sylve, don't edit! ===\n"

	config += "[global]\n"
	config += fmt.Sprintf("unix charset = %s\n", settings.UnixCharset)
	config += fmt.Sprintf("workgroup = %s\n", settings.Workgroup)
	config += fmt.Sprintf("server string = %s\n", settings.ServerString)

	interfaces := settings.Interfaces
	if interfaces == "" {
		interfaces = "lo0"
	} else {
		interfaces = strings.ReplaceAll(interfaces, ",", " ")
	}

	config += fmt.Sprintf("interfaces = %s\n", interfaces)

	if settings.BindInterfacesOnly {
		config += "bind interfaces only = yes\n"
	} else {
		config += "bind interfaces only = no\n"
	}

	if hasGuestShares {
		config += "map to guest = Bad User\n"
	}

	config += "multicast dns register = no\n"

	if settings.AppleExtensions {
		config += "min protocol = SMB2\n"
		config += "ea support = yes\n"
	}
	config += "inherit acls = yes\n"

	if settings.ExtraGlobalConfig != "" {
		// This is an administrator escape hatch. Keep the text opaque here and
		// let testparm evaluate it as part of the complete generated file.
		config += "\n# Extra global configuration supplied through Sylve\n"
		config += settings.ExtraGlobalConfig
		if !strings.HasSuffix(settings.ExtraGlobalConfig, "\n") {
			config += "\n"
		}
	}
	// Directory Services owns membership and ID mapping while enabled. Keep
	// those settings authoritative even when other advanced globals are set.
	config += directoryConfig

	return config
}

func (s *Service) shareConfig(ctx context.Context, shares []sambaModels.SambaShare, enforceACLs bool, datasets map[string]*gzfs.Dataset) (string, error) {
	settings, err := s.GetGlobalConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get Samba settings: %w", err)
	}
	ad, err := s.directorySettings()
	if err != nil {
		return "", err
	}

	if datasets == nil {
		datasets = make(map[string]*gzfs.Dataset)
	}
	for _, share := range shares {
		if err := validateSambaShareInput(share.Name, share.CreateMask, share.DirectoryMask, share.AuditedOperations); err != nil {
			return "", fmt.Errorf("invalid configuration for share %q: %w", share.Name, err)
		}
		if err := validateExtraShareConfig(share.ExtraShareConfig); err != nil {
			return "", err
		}

		ds, exists := datasets[share.Dataset]
		if !exists {
			ds, err = s.datasetByGUID(ctx, share.Dataset)
			if err != nil {
				return "", fmt.Errorf("failed to fetch dataset for share %s: %v", share.Name, err)
			}
			if !isSambaDatasetMounted(ds) {
				continue
			}
		}

		if ds == nil {
			return "", fmt.Errorf("dataset for share %s not found", share.Name)
		}

		if ds.Mountpoint == "-" || ds.Mountpoint == "" {
			return "", fmt.Errorf("dataset %s for share %s is not mounted", ds.Name, share.Name)
		}

		// Best-effort during config generation so a single property-set failure
		// doesn't prevent Samba from reloading otherwise valid share config.
		if !exists && enforceACLs {
			_ = s.ensureSambaDatasetACLProperties(ctx, ds, false)
		}

		datasets[share.Dataset] = ds
	}

	var config strings.Builder
	for _, share := range shares {
		dataset := datasets[share.Dataset]
		if dataset == nil {
			continue
		}

		config.WriteString(fmt.Sprintf("[%s]\n", share.Name))
		config.WriteString(fmt.Sprintf("\tpath = %s\n", dataset.Mountpoint))

		if share.GuestOk {
			config.WriteString(fmt.Sprintf("\tguest ok = yes\n"))
			config.WriteString("\tguest only = yes\n")

			if share.ReadOnly {
				config.WriteString("\tread only = yes\n")
			} else {
				config.WriteString("\tread only = no\n")
			}
		} else {
			config.WriteString(fmt.Sprintf("\tguest ok = no\n"))
		}

		principals := memberSharePrincipals(share, ad)
		principals = normalizeSambaPrincipalNames(principals)
		aclPrincipals := namesFromShareACLs(share)

		guestWriteable := share.GuestOk && !share.ReadOnly
		if enforceACLs && share.GuestOk {
			_ = s.syncSambaShareACL(dataset.Mountpoint, aclPrincipals, sambaPrincipalNames{}, true, guestWriteable, false)
		} else if enforceACLs {
			_ = s.syncSambaShareACL(dataset.Mountpoint, sambaPrincipalNames{}, aclPrincipals, false, false, false)
		}

		readUsers := principals.ReadUsers
		writeUsers := principals.WriteUsers
		readGroups := principals.ReadGroups
		writeGroups := principals.WriteGroups

		validUsers := make([]string, 0, len(readUsers)+len(writeUsers)+len(readGroups)+len(writeGroups))
		validUsers = append(validUsers, readUsers...)
		validUsers = append(validUsers, writeUsers...)
		for _, group := range readGroups {
			validUsers = append(validUsers, "@"+group)
		}
		for _, group := range writeGroups {
			validUsers = append(validUsers, "@"+group)
		}
		validUsers = uniquePrincipalNames(validUsers)

		writeList := make([]string, 0, len(writeUsers)+len(writeGroups))
		writeList = append(writeList, writeUsers...)
		for _, group := range writeGroups {
			writeList = append(writeList, "@"+group)
		}
		writeList = uniquePrincipalNames(writeList)

		readPrincipalCount := len(readUsers) + len(readGroups)
		writePrincipalCount := len(writeUsers) + len(writeGroups)

		if !share.GuestOk && len(validUsers) > 0 {
			config.WriteString(fmt.Sprintf("\tvalid users = %s\n", sambaPrincipalList(validUsers)))
		}

		if !share.GuestOk {
			if writePrincipalCount == 0 || readPrincipalCount > 0 {
				config.WriteString("\tread only = yes\n")
			} else {
				config.WriteString("\tread only = no\n")
			}
		}

		if !share.GuestOk && len(writeList) > 0 {
			config.WriteString(fmt.Sprintf("\twrite list = %s\n", sambaPrincipalList(writeList)))
		}

		config.WriteString(fmt.Sprintf("\tcreate mask = %s\n", share.CreateMask))
		config.WriteString(fmt.Sprintf("\tdirectory mask = %s\n", share.DirectoryMask))

		// Per-share VFS objects
		var vfsObjects []string
		if settings.AppleExtensions {
			vfsObjects = append(vfsObjects, "fruit", "streams_xattr")
		}
		if share.AuditEnabled && len(share.AuditedOperations) > 0 {
			vfsObjects = append(vfsObjects, "full_audit")
		}
		vfsObjects = append(vfsObjects, "zfsacl")
		config.WriteString(fmt.Sprintf("\tvfs objects = %s\n", strings.Join(vfsObjects, " ")))

		// Per-share fruit config
		if settings.AppleExtensions {
			config.WriteString("\tfruit:metadata = stream\n")
			config.WriteString("\tfruit:model = MacSamba\n")
			config.WriteString("\tfruit:veto_appledouble = yes\n")
			config.WriteString("\tfruit:convert_adouble = no\n")
			config.WriteString("\tfruit:nfs_aces = no\n")
			config.WriteString("\tfruit:wipe_intentionally_left_blank_rfork = yes\n")
			config.WriteString("\tfruit:delete_empty_adfiles = yes\n")
			config.WriteString("\tfruit:posix_rename = yes\n")
		}

		if settings.AppleExtensions && share.TimeMachine {
			config.WriteString("\tfruit:time machine = yes\n")
			if share.TimeMachineMaxSize > 0 {
				config.WriteString(fmt.Sprintf("\tfruit:time machine max size = %dG\n", share.TimeMachineMaxSize))
			}
		}

		// Per-share audit config
		if share.AuditEnabled && len(share.AuditedOperations) > 0 {
			config.WriteString("\tfull_audit:prefix = sylve-smb-al|%u|%I|%m|%S|%P\n")
			opsStr := strings.Join(share.AuditedOperations, " ")
			config.WriteString(fmt.Sprintf("\tfull_audit:success = %s\n", opsStr))
			config.WriteString(fmt.Sprintf("\tfull_audit:failure = %s\n", opsStr))
			config.WriteString("\tfull_audit:syslog = true\n")
			config.WriteString("\tfull_audit:facility = LOCAL5\n")
			config.WriteString("\tfull_audit:priority = NOTICE\n")
			config.WriteString("\tfull_audit:log_secdesc = true\n")
		}

		config.WriteString(share.ExtraShareConfig)
		config.WriteString("\n\n")
	}

	return config.String(), nil
}

func (s *Service) validateShareConfig(ctx context.Context, candidate sambaModels.SambaShare, dataset *gzfs.Dataset) error {
	shares, err := s.GetShares()
	if err != nil {
		return err
	}
	active := make([]sambaModels.SambaShare, 0, len(shares)+1)
	replaced := false
	for _, share := range shares {
		if share.ID == candidate.ID {
			active = append(active, candidate)
			replaced = true
		} else if share.Enabled {
			active = append(active, share)
		}
	}
	if !replaced {
		active = append(active, candidate)
	}

	if dataset == nil {
		// An unavailable disabled share needs only a parseable path for validation.
		// This placeholder is never written to the active configuration.
		dataset = &gzfs.Dataset{Mountpoint: "/"}
	}
	shareConfig, err := s.shareConfig(ctx, active, false, map[string]*gzfs.Dataset{candidate.Dataset: dataset})
	if err != nil {
		return err
	}
	settings, err := s.GetGlobalConfig()
	if err != nil {
		return err
	}
	hasGuestShares := false
	for _, share := range active {
		hasGuestShares = hasGuestShares || share.GuestOk
	}
	directoryConfig, err := s.directoryConfig(settings.ExtraGlobalConfig)
	if err != nil {
		return err
	}
	if err := validateSambaConfig(globalConfig(settings, hasGuestShares, directoryConfig) + "\n" + shareConfig); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidShareConfig, err)
	}
	return nil
}

func validateSambaConfig(config string) error {
	_, err := inspectSambaConfig(config)
	return err
}

func inspectSambaConfig(config string) (string, error) {
	dir := filepath.Dir(sambaConfigFilePath)
	file, err := os.CreateTemp(dir, ".smb4.conf-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary Samba configuration: %w", err)
	}
	temporaryPath := file.Name()
	defer os.Remove(temporaryPath)

	if _, err := file.WriteString(config); err != nil {
		file.Close()
		return "", fmt.Errorf("failed to write temporary Samba configuration: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("failed to close temporary Samba configuration: %w", err)
	}

	output, err := sambaRunCommand(sambaTestparmPath, "-s", temporaryPath)
	if err != nil {
		if detail := strings.TrimSpace(output); detail != "" {
			return "", fmt.Errorf("%w: %s", errSambaConfigRejected, detail)
		}
		return "", fmt.Errorf("%w: %v", errSambaConfigRejected, err)
	}

	return output, nil
}

var errSambaConfigRejected = errors.New("samba_config_rejected")

func (s *Service) renderConfig(ctx context.Context, enforceACLs bool) (string, error) {
	if s.Directory != nil && s.Directory.RecoveryPending() {
		return "", fmt.Errorf("%w: roll back the interrupted directory services restore before changing Samba configuration", directoryservices.ErrConflict)
	}
	gCfg, err := s.GlobalConfig()
	if err != nil {
		return "", err
	}
	if gCfg == "" {
		return "", fmt.Errorf("global configuration is empty")
	}

	shares, err := s.enabledShares()
	if err != nil {
		return "", err
	}
	shareCfg, err := s.shareConfig(ctx, shares, enforceACLs, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get share configuration: %w", err)
	}

	fullConfig := gCfg + "\n" + shareCfg
	if err := validateSambaConfig(fullConfig); err != nil {
		return "", err
	}
	return fullConfig, nil
}

type sambaServiceAction int

const (
	sambaKeepService sambaServiceAction = iota
	sambaReloadService
	sambaRestartService
)

var (
	sambaServiceRunning = sambaSmbdResponds
	sambaServiceCommand = system.ServiceAction
	sambaReloadDaemons  = reloadSambaDaemons
)

func (s *Service) sambaServiceEnabled() (bool, error) {
	var settings models.BasicSettings
	if err := s.DB.First(&settings).Error; err != nil {
		return false, fmt.Errorf("failed_to_get_service_settings: %w", err)
	}
	return slices.Contains(settings.Services, models.SambaServer), nil
}

func (s *Service) applySambaService(action sambaServiceAction) error {
	if action == sambaKeepService {
		return nil
	}
	enabled, err := s.sambaServiceEnabled()
	if err != nil || !enabled {
		return err
	}
	command := "onerestart"
	if action == sambaReloadService {
		command = "onestart"
		if sambaServiceRunning() {
			err := sambaReloadDaemons()
			if err == nil {
				return nil
			}
			logger.L.Warn().Err(err).Msg("samba_reload_message_failed")
			command = "onereload"
		}
	}
	if err := sambaServiceCommand("samba_server", command); err != nil {
		return fmt.Errorf("samba_service_%s_failed: %w", strings.TrimPrefix(command, "one"), err)
	}
	return nil
}

func (s *Service) WriteConfig(ctx context.Context, reload bool) error {
	action := sambaKeepService
	if reload {
		action = sambaRestartService
	}
	if err := s.writeConfig(ctx, action); err != nil {
		return err
	}
	s.announceConfigChange()
	return nil
}

func (s *Service) reloadConfig(ctx context.Context) error {
	if err := sambaWriteConfig(s, ctx, sambaReloadService); err != nil {
		return err
	}
	s.announceConfigChange()
	return nil
}

func (s *Service) announceConfigChange() {
	if s.OnConfigChange == nil {
		return
	}
	if err := s.OnConfigChange(); err != nil {
		logger.L.Warn().Err(err).Msg("mdns rebuild failed after samba config change")
	}
}

func (s *Service) writeConfig(ctx context.Context, action sambaServiceAction) error {
	fullConfig, err := s.renderConfig(ctx, true)
	if err != nil {
		return err
	}

	if err := sambaAtomicWriteFile(sambaConfigFilePath, []byte(fullConfig), 0644); err != nil {
		return fmt.Errorf("failed to write Samba configuration to %s: %w", sambaConfigFilePath, err)
	}

	return s.applySambaService(action)
}
