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
	"fmt"
	"os"
	"strings"

	"github.com/alchemillahq/gzfs"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/system"
	"github.com/alchemillahq/sylve/pkg/utils"

	iface "github.com/alchemillahq/sylve/pkg/network/iface"
)

const (
	sambaACLType    = "nfsv4"
	sambaACLMode    = "restricted"
	sambaACLInherit = "passthrough"
	guestACEName    = "everyone@"
	readACLPerm     = "read_set/execute"
	legacyReadPerm  = "read_set"
	writeACLPerm    = "modify_set"
)

func (s *Service) GetGlobalConfig() (sambaModels.SambaSettings, error) {
	var settings sambaModels.SambaSettings
	if err := s.DB.First(&settings).Error; err != nil {
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
	appleExtensions bool) error {
	if unixCharset == "" || workgroup == "" || serverString == "" {
		return fmt.Errorf("unixCharset, workgroup, and serverString cannot be empty")
	}

	if interfaces == "" {
		interfaces = "lo0"
	}

	supportedCharsets := utils.GetSupportedCharsets()

	if !utils.StringInSlice(unixCharset, supportedCharsets) {
		return fmt.Errorf("unsupported unixCharset: %s", unixCharset)
	}

	if !utils.IsValidWorkgroup(workgroup) {
		return fmt.Errorf("invalid workgroup name: %s", workgroup)
	}

	if !utils.IsValidServerString(serverString) {
		return fmt.Errorf("invalid server string: %s", serverString)
	}

	interfacesList := strings.Split(interfaces, ",")
	interfacesList = utils.RemoveDuplicates(interfacesList)

	for _, eIface := range interfacesList {
		eIface = strings.TrimSpace(eIface)
		_, err := iface.Get(eIface)
		if err != nil && !strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("invalid interface '%s': %w", eIface, err)
		} else if err != nil && strings.Contains(err.Error(), "not found") {
			logger.L.Warn().Str("interface", eIface).Msg("Interface not found, continuing without it")
			interfacesList = utils.RemoveStringFromSlice(interfacesList, eIface)
		}
	}

	if len(interfacesList) > 0 {
		interfaces = strings.Join(interfacesList, ",")
	} else {
		interfaces = "lo0"
	}

	var settings sambaModels.SambaSettings
	if err := s.DB.First(&settings).Error; err != nil {
		return fmt.Errorf("failed to retrieve Samba settings: %w", err)
	}

	settings.UnixCharset = unixCharset
	settings.Workgroup = workgroup
	settings.ServerString = serverString
	settings.Interfaces = interfaces
	settings.BindInterfacesOnly = bindInterfacesOnly
	settings.AppleExtensions = appleExtensions

	if err := s.DB.Save(&settings).Error; err != nil {
		return fmt.Errorf("failed to update Samba settings: %w", err)
	}

	return s.WriteConfig(ctx, true)
}

func (s *Service) hasGuestOnlyShares() (bool, error) {
	var count int64
	if err := s.DB.Model(&sambaModels.SambaShare{}).Where("guest_ok = ?", true).Count(&count).Error; err != nil {
		return false, fmt.Errorf("failed_to_check_guest_shares: %w", err)
	}

	return count > 0, nil
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

	if err := dataset.SetProperties(
		ctx,
		"acltype", sambaACLType,
		"aclmode", sambaACLMode,
		"aclinherit", sambaACLInherit,
	); err != nil {
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

func (s *Service) syncSambaDatasetPrincipalACLs(
	mountpoint string,
	previous sambaPrincipalNames,
	desired sambaPrincipalNames,
	strict bool,
) error {
	if mountpoint == "" || mountpoint == "-" {
		err := fmt.Errorf("dataset_not_mounted")
		if strict {
			return err
		}

		logger.L.Warn().Err(err).Str("mountpoint", mountpoint).Msg("failed_to_enforce_samba_dataset_principal_acls")
		return nil
	}

	previous = normalizeSambaPrincipalNames(previous)
	desired = normalizeSambaPrincipalNames(desired)

	removeACL := func(principalType string, principalName string, permissionSet string) {
		entry := fmt.Sprintf("%s:%s:%s:fd:allow", principalType, principalName, permissionSet)

		if _, err := utils.RunCommand("/bin/setfacl", "-x", entry, mountpoint); err != nil {
			logger.L.Warn().
				Err(err).
				Str("principal", principalName).
				Str("principal_type", principalType).
				Str("permission_set", permissionSet).
				Str("mountpoint", mountpoint).
				Msg("failed_to_remove_samba_dataset_principal_acl_entry")
		}
	}

	addACL := func(principalType string, principalName string, permissionSet string) error {
		entry := fmt.Sprintf("%s:%s:%s:fd:allow", principalType, principalName, permissionSet)

		_, err := utils.RunCommand("/bin/setfacl", "-m", entry, mountpoint)
		if err != nil {
			wrapped := fmt.Errorf(
				"failed_to_set_acl_for_%s_%s_on_%s: %w",
				principalType,
				principalName,
				mountpoint,
				err,
			)
			if strict {
				return wrapped
			}

			logger.L.Warn().
				Err(wrapped).
				Str("principal", principalName).
				Str("principal_type", principalType).
				Str("permission_set", permissionSet).
				Str("mountpoint", mountpoint).
				Msg("failed_to_enforce_samba_dataset_principal_acls")
		}

		return nil
	}

	targetUsers := mergePrincipalNames(previous.ReadUsers, previous.WriteUsers, desired.ReadUsers, desired.WriteUsers)
	targetGroups := mergePrincipalNames(previous.ReadGroups, previous.WriteGroups, desired.ReadGroups, desired.WriteGroups)

	for _, user := range targetUsers {
		removeACL("u", user, legacyReadPerm)
		removeACL("u", user, readACLPerm)
		removeACL("u", user, writeACLPerm)
	}

	for _, group := range targetGroups {
		removeACL("g", group, legacyReadPerm)
		removeACL("g", group, readACLPerm)
		removeACL("g", group, writeACLPerm)
	}

	for _, user := range desired.ReadUsers {
		if err := addACL("u", user, readACLPerm); err != nil {
			return err
		}
	}

	for _, user := range desired.WriteUsers {
		if err := addACL("u", user, writeACLPerm); err != nil {
			return err
		}
	}

	for _, group := range desired.ReadGroups {
		if err := addACL("g", group, readACLPerm); err != nil {
			return err
		}
	}

	for _, group := range desired.WriteGroups {
		if err := addACL("g", group, writeACLPerm); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) syncSambaDatasetGuestACL(
	mountpoint string,
	guestEnabled bool,
	guestWriteable bool,
	strict bool,
) error {
	if mountpoint == "" || mountpoint == "-" {
		err := fmt.Errorf("dataset_not_mounted")
		if strict {
			return err
		}

		logger.L.Warn().Err(err).Str("mountpoint", mountpoint).Msg("failed_to_enforce_samba_dataset_guest_acl")
		return nil
	}

	removeACL := func(permissionSet string) {
		entry := fmt.Sprintf("%s:%s:fd:allow", guestACEName, permissionSet)
		if _, err := utils.RunCommand("/bin/setfacl", "-x", entry, mountpoint); err != nil {
			logger.L.Warn().
				Err(err).
				Str("permission_set", permissionSet).
				Str("mountpoint", mountpoint).
				Msg("failed_to_remove_samba_dataset_guest_acl_entry")
		}
	}

	addACL := func(permissionSet string) error {
		entry := fmt.Sprintf("%s:%s:fd:allow", guestACEName, permissionSet)
		_, err := utils.RunCommand("/bin/setfacl", "-m", entry, mountpoint)
		if err != nil {
			wrapped := fmt.Errorf("failed_to_set_guest_acl_for_%s_on_%s: %w", permissionSet, mountpoint, err)
			if strict {
				return wrapped
			}

			logger.L.Warn().
				Err(wrapped).
				Str("permission_set", permissionSet).
				Str("mountpoint", mountpoint).
				Msg("failed_to_enforce_samba_dataset_guest_acl")
		}
		return nil
	}

	removeACL(legacyReadPerm)
	removeACL(readACLPerm)
	removeACL(writeACLPerm)

	if !guestEnabled {
		return nil
	}

	if guestWriteable {
		return addACL(writeACLPerm)
	}

	return addACL(readACLPerm)
}

func (s *Service) GlobalConfig() (string, error) {
	settings, err := s.GetGlobalConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get global Samba settings: %w", err)
	}

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

	hasGuestShares, err := s.hasGuestOnlyShares()
	if err != nil {
		return "", err
	}

	if hasGuestShares {
		config += "map to guest = Bad User\n"
	}

	if settings.AppleExtensions {
		config += "min protocol = SMB2\n"
		config += "ea support = yes\n"
		config += "vfs objects = fruit streams_xattr full_audit zfsacl\n"
		config += "fruit:metadata = stream\n"
		config += "fruit:model = MacSamba\n"
		config += "fruit:veto_appledouble = no\n"
		config += "fruit:nfs_aces = no\n"
		config += "fruit:wipe_intentionally_left_blank_rfork = yes\n"
		config += "fruit:delete_empty_adfiles = yes\n"
		config += "fruit:posix_rename = yes\n"
	} else {
		config += "vfs objects = full_audit zfsacl\n"
	}
	config += "inherit acls = yes\n"

	return config, nil
}

func (s *Service) ShareConfig(ctx context.Context) (string, error) {
	shares := []sambaModels.SambaShare{}
	if err := s.DB.
		Preload("ReadOnlyUsers").
		Preload("WriteableUsers").
		Preload("ReadOnlyGroups").
		Preload("WriteableGroups").
		Find(&shares).Error; err != nil {
		return "", fmt.Errorf("failed to retrieve Samba shares: %w", err)
	}

	var datasets = make(map[string]*gzfs.Dataset)
	for _, share := range shares {
		if _, exists := datasets[share.Dataset]; !exists {
			ds, err := s.GZFS.ZFS.GetByGUID(ctx, share.Dataset, false)
			if err != nil {
				return "", fmt.Errorf("failed to fetch dataset for share %s: %v", share.Name, err)
			}

			if ds == nil {
				return "", fmt.Errorf("dataset for share %s not found", share.Name)
			}

			if ds.Mountpoint == "-" || ds.Mountpoint == "" {
				return "", fmt.Errorf("dataset %s for share %s is not mounted", ds.Name, share.Name)
			}

			// Best-effort during config generation so a single property-set failure
			// doesn't prevent Samba from reloading otherwise valid share config.
			_ = s.ensureSambaDatasetACLProperties(ctx, ds, false)

			datasets[share.Dataset] = ds
		}
	}

	var config strings.Builder
	for _, share := range shares {
		dataset := datasets[share.Dataset]

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

		principals := namesFromShareAssociations(share)
		principals = normalizeSambaPrincipalNames(principals)

		guestWriteable := share.GuestOk && !share.ReadOnly
		if share.GuestOk {
			// Best-effort during config generation to avoid breaking Samba reload.
			_ = s.syncSambaDatasetPrincipalACLs(dataset.Mountpoint, principals, sambaPrincipalNames{}, false)
			_ = s.syncSambaDatasetGuestACL(dataset.Mountpoint, true, guestWriteable, false)
		} else {
			// Best-effort during config generation to avoid breaking Samba reload.
			_ = s.syncSambaDatasetGuestACL(dataset.Mountpoint, false, false, false)
			_ = s.syncSambaDatasetPrincipalACLs(dataset.Mountpoint, sambaPrincipalNames{}, principals, false)
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
			config.WriteString(fmt.Sprintf("\tvalid users = %s\n", strings.Join(validUsers, " ")))
		}

		if !share.GuestOk {
			if writePrincipalCount == 0 || readPrincipalCount > 0 {
				config.WriteString("\tread only = yes\n")
			} else {
				config.WriteString("\tread only = no\n")
			}
		}

		if !share.GuestOk && len(writeList) > 0 {
			config.WriteString(fmt.Sprintf("\twrite list = %s\n", strings.Join(writeList, " ")))
		}

		config.WriteString(fmt.Sprintf("\tcreate mask = %s\n", share.CreateMask))
		config.WriteString(fmt.Sprintf("\tdirectory mask = %s\n", share.DirectoryMask))
		if share.TimeMachine {
			config.WriteString("\tfruit:time machine = yes\n")
			if share.TimeMachineMaxSize > 0 {
				config.WriteString(fmt.Sprintf("\tfruit:time machine max size = %dG\n", share.TimeMachineMaxSize))
			}
		}
		config.WriteString("\tfull_audit:prefix = sylve-smb-al|%u|%I|%m|%S|%P\n")
		config.WriteString("\tfull_audit:success = openat close read write renameat unlinkat mkdirat create_file connect disconnect\n")
		config.WriteString("\tfull_audit:failure = all !getwd !get_real_filename !fgetxattr !fget_dos_attributes\n")
		config.WriteString("\tfull_audit:facility = LOCAL7\n")
		config.WriteString("\tfull_audit:priority = ALERT\n")
		config.WriteString("\tfull_audit:syslog = true\n")
		config.WriteString("\tfull_audit:log_secdesc = true\n")

		config.WriteString("\n\n")
	}

	return config.String(), nil
}

func (s *Service) WriteAvahiConfig() error {
	var shares []sambaModels.SambaShare
	if err := s.DB.Where("time_machine = ?", true).Find(&shares).Error; err != nil {
		return fmt.Errorf("failed to retrieve Time Machine shares: %w", err)
	}

	var diskEntries string
	for i, share := range shares {
		diskEntries += fmt.Sprintf("\t\t<txt-record>dk%d=adVN=%s,adVF=0x82</txt-record>\n", i, share.Name)
	}

	xml := fmt.Sprintf(`<?xml version="1.0" standalone='no'?>
<!DOCTYPE service-group SYSTEM "avahi-service.dtd">
<service-group>
	<name replace-wildcards="yes">%%h</name>
	<service>
		<type>_smb._tcp</type>
		<port>445</port>
	</service>
	<service>
		<type>_device-info._tcp</type>
		<port>0</port>
		<txt-record>model=RackMac</txt-record>
	</service>
	<service>
		<type>_adisk._tcp</type>
		<txt-record>sys=waMa=0,adVF=0x100</txt-record>
%s	</service>
</service-group>`, diskEntries)

	dir := "/usr/local/etc/avahi/services"
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create avahi services directory: %w", err)
	}

	if err := os.WriteFile(dir+"/timemachine.service", []byte(xml), 0644); err != nil {
		return fmt.Errorf("failed to write Avahi config: %w", err)
	}

	return nil
}

func (s *Service) WriteConfig(ctx context.Context, reload bool) error {
	gCfg, err := s.GlobalConfig()
	if err != nil {
		return err
	}

	if gCfg == "" {
		return fmt.Errorf("global configuration is empty")
	}

	shareCfg, err := s.ShareConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to get share configuration: %w", err)
	}

	fullConfig := gCfg + "\n" + shareCfg
	filePath := "/usr/local/etc/smb4.conf"

	if err := os.WriteFile(filePath, []byte(fullConfig), 0644); err != nil {
		return fmt.Errorf("failed to write Samba configuration to %s: %w", filePath, err)
	}

	settings, err := s.GetGlobalConfig()
	if err != nil {
		return fmt.Errorf("failed to get global config for avahi management: %w", err)
	}

	if settings.AppleExtensions {
		if err := s.WriteAvahiConfig(); err != nil {
			logger.L.Warn().Err(err).Msg("failed to write avahi config")
		}
		if err := system.ServiceAction("dbus", "onerestart"); err != nil {
			logger.L.Warn().Err(err).Msg("failed to restart dbus")
		}
		if err := system.ServiceAction("avahi-daemon", "onerestart"); err != nil {
			logger.L.Warn().Err(err).Msg("failed to restart avahi-daemon")
		}
	} else {
		avahiPath := "/usr/local/etc/avahi/services/timemachine.service"
		if _, err := os.Stat(avahiPath); err == nil {
			if err := os.Remove(avahiPath); err != nil {
				logger.L.Warn().Err(err).Msg("failed to remove avahi timemachine service file")
			}
		}
		if err := system.ServiceAction("avahi-daemon", "onestop"); err != nil {
			logger.L.Warn().Err(err).Msg("failed to stop avahi-daemon")
		}
	}

	if reload {
		if err := system.ServiceAction("samba_server", "onerestart"); err != nil {
			return fmt.Errorf("failed to restart Samba service: %w", err)
		}
	}

	return nil
}
