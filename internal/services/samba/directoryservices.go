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
	"strconv"
	"strings"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/services/directoryservices"
)

func (s *Service) lockDirectoryConfiguration() (func(), error) {
	if s.Directory == nil {
		return func() {}, nil
	}
	return s.Directory.LockConfiguration()
}

func (s *Service) directorySettings() (directoryModels.ActiveDirectory, error) {
	if s.directoryOverride != nil {
		return *s.directoryOverride, nil
	}
	if s.Directory == nil {
		return directoryModels.ActiveDirectory{}, nil
	}
	// Use this service's DB handle: global settings validation runs in a transaction.
	var ad directoryModels.ActiveDirectory
	if err := s.DB.Limit(1).Find(&ad).Error; err != nil {
		return ad, err
	}
	return ad, nil
}

func (s *Service) directoryConfig(extraGlobalConfig string) (string, error) {
	ad, err := s.directorySettings()
	if err != nil {
		return "", err
	}
	if extraGlobalConfig != "" && (s.Directory != nil || s.directoryOverride != nil) {
		managed := ad.Enabled || ad.Joined || ad.MembershipPending != ""
		if !managed {
			managed, err = s.DirectoryPrincipalsInUse()
			if err != nil {
				return "", err
			}
		}
		if managed {
			if err := validateDirectoryExtraGlobalConfig(extraGlobalConfig); err != nil {
				return "", err
			}
		}
	}
	if !ad.Enabled || ad.Domain == "" {
		return "", nil
	}
	version, err := sambaRunCommand("/usr/local/bin/net", "--version")
	if err != nil {
		return "", err
	}
	member, err := directoryservices.SambaConfig(ad, version)
	if err != nil || extraGlobalConfig == "" || !strings.Contains(member, "sync machine password to keytab") {
		return member, err
	}
	// Let Samba resolve continued lines, quoted lists, and duplicate settings
	// before expanding aliases into the single native SPN synchronization rule.
	text, err := inspectSambaConfig("[global]\n" + extraGlobalConfig + "\n" + member)
	if err != nil {
		return "", err
	}
	hostnames, err := directoryservices.SambaKeytabHostnames(text, ad.Domain)
	if err != nil {
		return "", err
	}
	return directoryservices.SambaConfig(ad, version, hostnames...)
}

func validateDirectoryExtraGlobalConfig(config string) error {
	if strings.ContainsRune(config, '\x00') {
		return invalidGlobalConfig("extra globals cannot contain null bytes while Directory Services manages identities")
	}
	// Samba supports continued lines and ignores whitespace in parameter names.
	lines := strings.Split(config, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		for strings.HasSuffix(line, "\\") && i+1 < len(lines) {
			i++
			line = strings.TrimSuffix(line, "\\") + strings.TrimRight(lines[i], " \t\r")
		}
		if strings.HasPrefix(line, "[") {
			return invalidGlobalConfig("extra globals cannot contain section headers while Directory Services manages identities")
		}
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			continue // The complete configuration is still checked by testparm.
		}
		name := strings.ToLower(strings.Join(strings.Fields(key), ""))
		// Backend-specific options (including RID base_rid and AUTORID rangesize)
		// can change Unix IDs without changing the generated backend/range lines.
		reserved := strings.HasPrefix(name, "idmap")
		switch name {
		case "winbindseparator", "winbindnssinfo", "winbindnormalizenames", "usernamemap", "usernamemapscript", "passdbbackend", "include", "configbackend", "registryshares":
			reserved = true
		}
		if reserved {
			return invalidGlobalConfig("%s cannot be set in extra globals while Directory Services manages identities; use Directory Services settings", strings.TrimSpace(key))
		}
	}
	return nil
}

func memberSharePrincipals(share sambaModels.SambaShare, ad directoryModels.ActiveDirectory) sambaPrincipalNames {
	names := namesFromACLPrincipals(share.ReadOnlyUsers, share.WriteableUsers, share.ReadOnlyGroups, share.WriteableGroups)
	if ad.Enabled {
		// Unqualified names can resolve to the domain when default-domain mode
		// is enabled. Local selections must retain their local identity.
		for _, users := range []*[]string{&names.ReadUsers, &names.WriteUsers} {
			for i := range *users {
				(*users)[i] = ad.ComputerName + "\\" + (*users)[i]
			}
		}
		for _, groups := range []*[]string{&names.ReadGroups, &names.WriteGroups} {
			for i := range *groups {
				(*groups)[i] = "Unix Groups\\" + (*groups)[i]
			}
		}
	}
	// Samba matches a bare SID directly against the security token for both
	// users and groups. Do not prefix group SIDs with @ or resolve saved names:
	// renaming/reusing an AD name must never grant access to a different SID.
	for _, principal := range share.DirectoryPermissions.Read {
		names.ReadUsers = append(names.ReadUsers, principal.SID)
	}
	for _, principal := range share.DirectoryPermissions.Write {
		names.WriteUsers = append(names.WriteUsers, principal.SID)
	}
	return names
}

func (s *Service) ValidateDirectoryConfig(ctx context.Context, ad directoryModels.ActiveDirectory) error {
	candidate := &Service{DB: s.DB, GZFS: s.GZFS, Directory: s.Directory, directoryOverride: &ad}
	global, err := candidate.GlobalConfig()
	if err != nil {
		if errors.Is(err, ErrInvalidGlobalConfig) {
			return fmt.Errorf("%w: %v", directoryservices.ErrInvalid, err)
		}
		return err
	}
	shares, err := candidate.GetShares()
	if err != nil {
		return err
	}
	active := []sambaModels.SambaShare{}
	for _, share := range shares {
		if share.Enabled {
			active = append(active, share)
		}
	}
	text, err := candidate.shareConfig(ctx, active, false, nil)
	if err != nil {
		return err
	}
	if err := validateSambaConfig(global + "\n" + text); err != nil {
		return fmt.Errorf("%w: %v", directoryservices.ErrInvalid, err)
	}
	return nil
}

func (s *Service) DirectoryPrincipalsInUse() (bool, error) {
	var shares []sambaModels.SambaShare
	if err := s.DB.Select("directory_permissions").Find(&shares).Error; err != nil {
		return false, err
	}
	for _, share := range shares {
		if len(share.DirectoryPermissions.Read)+len(share.DirectoryPermissions.Write) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) resolveDirectoryPermissions(ctx context.Context, previous directoryModels.SharePermissions, request *directoryservices.SharePermissionInput) (directoryModels.SharePermissions, error) {
	if request == nil {
		return previous, nil
	}
	input := *request
	if input.ReadSIDs == nil {
		for _, principal := range previous.Read {
			input.ReadSIDs = append(input.ReadSIDs, principal.SID)
		}
	}
	if input.WriteSIDs == nil {
		for _, principal := range previous.Write {
			input.WriteSIDs = append(input.WriteSIDs, principal.SID)
		}
	}
	if len(input.ReadSIDs)+len(input.WriteSIDs) == 0 {
		return directoryModels.SharePermissions{Read: []directoryModels.Principal{}, Write: []directoryModels.Principal{}}, nil
	}
	if s.Directory == nil {
		return previous, fmt.Errorf("%w: directory services are unavailable", directoryservices.ErrInvalid)
	}
	return s.Directory.ResolveSharePermissions(ctx, input, previous)
}

func appendDirectoryACLPrincipals(names sambaPrincipalNames, permissions directoryModels.SharePermissions) sambaPrincipalNames {
	for _, group := range []struct {
		principals    []directoryModels.Principal
		users, groups *[]string
	}{{permissions.Read, &names.ReadUsers, &names.ReadGroups}, {permissions.Write, &names.WriteUsers, &names.WriteGroups}} {
		for _, principal := range group.principals {
			name := strconv.FormatUint(uint64(principal.UnixID), 10)
			if principal.Kind == "group" {
				*group.groups = append(*group.groups, name)
			} else {
				*group.users = append(*group.users, name)
			}
		}
	}
	return names
}

func namesFromShareACLs(share sambaModels.SambaShare) sambaPrincipalNames {
	return appendDirectoryACLPrincipals(namesFromACLPrincipals(share.ReadOnlyUsers, share.WriteableUsers, share.ReadOnlyGroups, share.WriteableGroups), share.DirectoryPermissions)
}

func sambaPrincipalList(names []string) string {
	out := make([]string, len(names))
	for i, name := range names {
		if strings.ContainsAny(name, " \t\\") {
			name = `"` + name + `"`
		}
		out[i] = name
	}
	return strings.Join(out, " ")
}
