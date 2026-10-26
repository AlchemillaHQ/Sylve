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
	"regexp"
	"slices"

	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
)

var nfsPrincipal = regexp.MustCompile(`^nfs/[A-Za-z0-9][A-Za-z0-9.-]{0,252}@[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`)
var securityFlavors = []string{"sys", "krb5", "krb5i", "krb5p"}

func shareSecurity(share nfsModels.NFSShare) []string {
	if share.Security == nil {
		return []string{"sys"}
	}
	return share.Security
}

func kerberosPolicy(config nfsModels.NFSConfig, shares []nfsModels.NFSShare) (bool, error) {
	required := slices.ContainsFunc(shares, func(share nfsModels.NFSShare) bool {
		return share.Enabled && slices.ContainsFunc(shareSecurity(share), func(flavor string) bool { return flavor != "sys" })
	})
	if !required {
		return false, nil
	}
	if config.NumericIDs && config.Protocol != "v3" {
		return true, conflict("numericIds", "Disable numeric NFSv4 IDs before using Kerberos.")
	}
	if config.KerberosPrincipal == "" {
		return true, conflict("kerberosPrincipal", "Select an installed NFS service principal.")
	}
	return true, nil
}

func (s *Service) kerberosState(ctx context.Context, config nfsModels.NFSConfig, shares []nfsModels.NFSShare) (string, error) {
	required, err := kerberosPolicy(config, shares)
	if err != nil || !required {
		return "", err
	}
	if s.KerberosState == nil {
		return "", conflict("kerberosPrincipal", "Directory Services is unavailable.")
	}
	state, err := s.KerberosState(ctx, config.KerberosPrincipal)
	if err != nil {
		return "", conflict("kerberosPrincipal", err.Error())
	}
	if state == "" {
		return "", conflict("kerberosPrincipal", "Installed NFS service credentials are unavailable.")
	}
	return state, nil
}
