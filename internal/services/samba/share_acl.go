// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package samba

import (
	"fmt"
	"os/user"
	"slices"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/logger"
)

const sambaACLInheritFlags = "fd-----"

var (
	sambaACLPermissionText = map[string]string{
		legacyReadPerm: "r-----a-R-c---",
		readACLPerm:    "r-x---a-R-c---",
		writeACLPerm:   "rwxpDdaARWc--s",
	}
	sambaLookupUserID = func(name string) (string, error) {
		found, err := user.Lookup(name)
		if err != nil {
			return "", err
		}
		return found.Uid, nil
	}
	sambaLookupGroupID = func(name string) (string, error) {
		found, err := user.LookupGroup(name)
		if err != nil {
			return "", err
		}
		return found.Gid, nil
	}
)

type sambaACE struct {
	key        string
	permission string
	entry      string
}

type sambaACLEntry struct {
	position int
	key      string
	perms    string
	managed  bool
}

func sambaPrincipalKey(tag, name string) string {
	prefix, lookup := "user:", sambaLookupUserID
	if tag == "g" {
		prefix, lookup = "group:", sambaLookupGroupID
	}
	if _, err := strconv.ParseUint(name, 10, 32); err == nil {
		return prefix + name
	}
	if id, err := lookup(name); err == nil {
		return prefix + id
	}
	return prefix + name
}

func sambaPrincipalACE(tag, name, permission string) sambaACE {
	return sambaACE{
		key:        sambaPrincipalKey(tag, name),
		permission: permission,
		entry:      fmt.Sprintf("%s:%s:%s:fd:allow", tag, name, permission),
	}
}

func sambaGuestACE(permission string) sambaACE {
	return sambaACE{key: guestACEName, permission: permission, entry: fmt.Sprintf("%s:%s:fd:allow", guestACEName, permission)}
}

func isSambaManagedPermission(perms string) bool {
	for _, text := range sambaACLPermissionText {
		if perms == text {
			return true
		}
	}
	return false
}

func parseSambaACL(output string) ([]sambaACLEntry, bool) {
	var entries []sambaACLEntry
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ":")
		var key, perms, flags, kind string
		switch len(fields) {
		case 4:
			key, perms, flags, kind = fields[0], fields[1], fields[2], fields[3]
		case 5:
			key, perms, flags, kind = fields[0]+":"+fields[1], fields[2], fields[3], fields[4]
		default:
			return nil, false
		}
		principal := strings.HasPrefix(key, "user:") || strings.HasPrefix(key, "group:") || key == guestACEName
		entries = append(entries, sambaACLEntry{
			position: len(entries),
			key:      key,
			perms:    perms,
			managed:  principal && kind == "allow" && flags == sambaACLInheritFlags && isSambaManagedPermission(perms),
		})
	}
	return entries, true
}

func planSambaACL(entries []sambaACLEntry, owned []string, desired []sambaACE) ([]int, []sambaACE) {
	want := make(map[string]sambaACE, len(desired))
	for _, ace := range desired {
		want[ace.key] = ace
	}
	kept := make(map[string]int)
	for _, entry := range entries {
		if ace, wanted := want[entry.key]; wanted && entry.managed && entry.perms == sambaACLPermissionText[ace.permission] {
			if _, done := kept[entry.key]; !done {
				kept[entry.key] = entry.position
			}
		}
	}
	replaced := make(map[string]int)
	for _, entry := range entries {
		if _, wanted := want[entry.key]; wanted && entry.managed && entry.key != guestACEName {
			_, done := kept[entry.key]
			_, chosen := replaced[entry.key]
			if !done && !chosen {
				replaced[entry.key] = entry.position
			}
		}
	}

	var removals []int
	for _, entry := range entries {
		_, wanted := want[entry.key]
		if !entry.managed || (!wanted && !slices.Contains(owned, entry.key)) {
			continue
		}
		if position, ok := kept[entry.key]; ok && position == entry.position {
			continue
		}
		if position, ok := replaced[entry.key]; ok && position == entry.position {
			continue
		}
		removals = append(removals, entry.position)
	}

	var additions []sambaACE
	for _, ace := range desired {
		if _, ok := kept[ace.key]; !ok {
			additions = append(additions, ace)
		}
	}
	return removals, additions
}

func sambaACLFailure(strict bool, mountpoint string, err error) error {
	if strict {
		return err
	}
	logger.L.Warn().Err(err).Str("mountpoint", mountpoint).Msg("failed_to_enforce_samba_dataset_acl")
	return nil
}

func sambaGuestPermission(writeable bool) string {
	if writeable {
		return writeACLPerm
	}
	return readACLPerm
}

func (s *Service) reconcileSambaACL(mountpoint string, owned []string, desired []sambaACE, strict bool) error {
	output, err := sambaRunCommand("/bin/getfacl", "-nq", mountpoint)
	if err != nil {
		return sambaACLFailure(strict, mountpoint, fmt.Errorf("failed_to_read_acl_on_%s: %w", mountpoint, err))
	}
	entries, ok := parseSambaACL(output)
	if !ok {
		return sambaACLFailure(strict, mountpoint, fmt.Errorf("unreadable_acl_on_%s", mountpoint))
	}
	removals, additions := planSambaACL(entries, owned, desired)

	for i := len(removals) - 1; i >= 0; i-- {
		if _, err := sambaRunCommand("/bin/setfacl", "-x", strconv.Itoa(removals[i]), mountpoint); err != nil {
			logger.L.Warn().Err(err).Str("mountpoint", mountpoint).Int("position", removals[i]).Msg("failed_to_remove_samba_dataset_acl_entry")
		}
	}

	var batch []string
	var principals []sambaACE
	for _, ace := range additions {
		if ace.key == guestACEName {
			if _, err := sambaRunCommand("/bin/setfacl", "-a", "0", ace.entry, mountpoint); err != nil {
				if err := sambaACLFailure(strict, mountpoint, fmt.Errorf("failed_to_set_acl_%s_on_%s: %w", ace.entry, mountpoint, err)); err != nil {
					return err
				}
			}
			continue
		}
		batch = append(batch, ace.entry)
		principals = append(principals, ace)
	}
	if len(batch) == 0 {
		return nil
	}
	if _, err := sambaRunCommand("/bin/setfacl", "-m", strings.Join(batch, ","), mountpoint); err == nil {
		return nil
	}
	for _, ace := range principals {
		if _, err := sambaRunCommand("/bin/setfacl", "-m", ace.entry, mountpoint); err != nil {
			if err := sambaACLFailure(strict, mountpoint, fmt.Errorf("failed_to_set_acl_%s_on_%s: %w", ace.entry, mountpoint, err)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) syncSambaShareACL(
	mountpoint string,
	previous sambaPrincipalNames,
	desired sambaPrincipalNames,
	guestEnabled bool,
	guestWriteable bool,
	strict bool,
) error {
	if mountpoint == "" || mountpoint == "-" {
		return sambaACLFailure(strict, mountpoint, fmt.Errorf("dataset_not_mounted"))
	}
	aces := sambaPrincipalACEs(normalizeSambaPrincipalNames(desired))
	if guestEnabled {
		aces = append(aces, sambaGuestACE(sambaGuestPermission(guestWriteable)))
	}
	owned := append(sambaPrincipalKeys(normalizeSambaPrincipalNames(previous)), guestACEName)
	return s.reconcileSambaACL(mountpoint, owned, aces, strict)
}

func sambaPrincipalACEs(names sambaPrincipalNames) []sambaACE {
	var aces []sambaACE
	for _, list := range []struct {
		tag, permission string
		names           []string
	}{
		{"u", readACLPerm, names.ReadUsers},
		{"u", writeACLPerm, names.WriteUsers},
		{"g", readACLPerm, names.ReadGroups},
		{"g", writeACLPerm, names.WriteGroups},
	} {
		for _, name := range list.names {
			aces = append(aces, sambaPrincipalACE(list.tag, name, list.permission))
		}
	}
	return aces
}

func sambaPrincipalKeys(names sambaPrincipalNames) []string {
	var keys []string
	for _, name := range mergePrincipalNames(names.ReadUsers, names.WriteUsers) {
		keys = append(keys, sambaPrincipalKey("u", name))
	}
	for _, name := range mergePrincipalNames(names.ReadGroups, names.WriteGroups) {
		keys = append(keys, sambaPrincipalKey("g", name))
	}
	return keys
}
