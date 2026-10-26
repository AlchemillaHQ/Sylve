// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package nfs

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
)

func mappingNumber(value string) (string, error) {
	number, err := strconv.ParseUint(value, 10, 32)
	if err != nil || number == 1<<32-1 {
		return "", errors.New("invalid Unix identity from NSS")
	}
	return strconv.FormatUint(number, 10), nil
}

// Resolve through NSS, including identities provided by Directory Services. Emit
// numeric credentials so mountd cannot silently substitute nobody on lookup failure.
func (d *nativeRuntime) mappingCredential(ctx context.Context, user, group string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	lookup := func(database, name string, fields int) ([]string, error) {
		result, err := d.command(ctx, "/usr/bin/getent", database, name)
		text := strings.TrimSpace(result.Output)
		parts := strings.Split(text, ":")
		// FreeBSD omits the final separator when a group has no members.
		validFields := len(parts) == fields || database == "group" && len(parts) == 3
		if err != nil || strings.ContainsAny(text, "\r\n") || !validFields || parts[0] == "" {
			return nil, errors.New("mapping user or group is unavailable in NSS")
		}
		return parts, nil
	}
	account, err := lookup("passwd", user, 7)
	if err != nil {
		return "", err
	}
	uid, err := mappingNumber(account[2])
	if err != nil {
		return "", err
	}
	primary, err := mappingNumber(account[3])
	if err != nil {
		return "", err
	}
	groups := []string{primary}
	if group != "" {
		account, err := lookup("group", group, 4)
		if err != nil {
			return "", err
		}
		groups[0], err = mappingNumber(account[2])
		if err != nil {
			return "", err
		}
	} else {
		result, err := d.command(ctx, "/usr/bin/id", "-G", user)
		if err != nil || strings.TrimSpace(result.Output) == "" {
			return "", errors.New("mapping user's groups are unavailable in NSS")
		}
		additional := []string{}
		for _, value := range strings.Fields(result.Output) {
			gid, err := mappingNumber(value)
			if err != nil {
				return "", err
			}
			if gid != primary {
				additional = append(additional, gid)
			}
		}
		slices.Sort(additional)
		groups = append(groups, slices.Compact(additional)...)
	}
	if len(groups) > 1024 {
		return "", errors.New("mapping user exceeds FreeBSD's export group limit")
	}
	return uid + ":" + strings.Join(groups, ":"), nil
}

func (s *Service) resolveMappings(ctx context.Context, shares []nfsModels.NFSShare) (map[uint]string, []nfsModels.MappingIdentity, error) {
	credentials := make(map[[2]string]string)
	options := make(map[uint]string)
	for _, share := range shares {
		if !share.Enabled {
			continue
		}
		mode, user, group := "maproot", share.MaprootUser, share.MaprootGroup
		if share.MapallUser != "" {
			mode, user, group = "mapall", share.MapallUser, share.MapallGroup
		}
		if user == "" {
			continue
		}
		key := [2]string{user, group}
		credential, ok := credentials[key]
		if !ok {
			var err error
			credential, err = s.resolveMapping(ctx, user, group)
			if err != nil {
				return nil, nil, conflict(mode+"User", err.Error())
			}
			credentials[key] = credential
		}
		options[share.ID] = " -" + mode + "=" + credential
	}
	identities := make([]nfsModels.MappingIdentity, 0, len(credentials))
	for key, credential := range credentials {
		identities = append(identities, nfsModels.MappingIdentity{User: key[0], Group: key[1], Credential: credential})
	}
	slices.SortFunc(identities, func(a, b nfsModels.MappingIdentity) int {
		if order := strings.Compare(a.User, b.User); order != 0 {
			return order
		}
		return strings.Compare(a.Group, b.Group)
	})
	return options, identities, nil
}
