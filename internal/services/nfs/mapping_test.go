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
	"strings"
	"testing"

	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func TestMappingValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*nfsModels.NFSShareConfig)
	}{
		{"root group without user", func(s *nfsModels.NFSShareConfig) { s.MaprootGroup = "wheel" }},
		{"all group without user", func(s *nfsModels.NFSShareConfig) { s.MapallGroup = "wheel" }},
		{"both modes", func(s *nfsModels.NFSShareConfig) { s.MaprootUser, s.MapallUser = "root", "nobody" }},
		{"credential injection", func(s *nfsModels.NFSShareConfig) { s.MaprootUser = "root:wheel" }},
		{"option injection", func(s *nfsModels.NFSShareConfig) { s.MapallUser = "-root" }},
		{"newline", func(s *nfsModels.NFSShareConfig) { s.MaprootUser = "user\nroot" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := shareInput("1", "192.0.2.1")
			test.change(&input)
			if _, err := normalizeShare(input); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("invalid mapping accepted: %v", err)
			}
		})
	}
	input := shareInput("1", "192.0.2.1")
	input.MaprootUser = " DOMAIN\\a user "
	input.MaprootGroup = "DOMAIN\\a group"
	normalized, err := normalizeShare(input)
	if err != nil || normalized.MaprootUser != "DOMAIN\\a user" {
		t.Fatalf("qualified directory mapping rejected: %#v, %v", normalized, err)
	}
}

func TestMappingCredentialsResolveNumericIDsAndSupplementaryGroups(t *testing.T) {
	for _, test := range []struct {
		name, user, group, passwd, groups, groupEntry, want string
	}{
		{"user groups", "DOMAIN\\user", "", "DOMAIN\\user:*:1001:1002::/home/user:/bin/false\n", "1002 9 7 9\n", "", "1001:1002:7:9"},
		{"explicit group", "root", "wheel", "root:*:0:0::/root:/bin/sh\n", "", "wheel:*:5:\n", "0:5"},
		{"empty group members", "daemon", "nobody", "daemon:*:1:1::/:/bin/false\n", "", "nobody:*:65534\n", "1:65534"},
		{"unknown user", "missing", "", "", "", "", ""},
		{"invalid uid", "bad", "", "bad:*:4294967295:0::/:/bin/false\n", "0", "", ""},
		{"invalid group", "bad", "", "bad:*:1:2::/:/bin/false\n", "2 invalid", "", ""},
		{"missing group", "user", "missing", "user:*:1:2::/:/bin/false\n", "", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newNativeRuntime()
			d.run = func(ctx context.Context, env []string, command string, args ...string) (utils.CommandResult, error) {
				if ctx.Err() != nil {
					return utils.CommandResult{}, ctx.Err()
				}
				output := ""
				switch command {
				case "/usr/bin/getent":
					if len(args) != 2 {
						t.Fatalf("unexpected NSS arguments: %v", args)
					}
					if args[0] == "passwd" && args[1] == test.user {
						output = test.passwd
					}
					if args[0] == "group" && args[1] == test.group {
						output = test.groupEntry
					}
				case "/usr/bin/id":
					if len(args) != 2 || args[0] != "-G" || args[1] != test.user {
						t.Fatalf("unexpected group lookup: %v", args)
					}
					output = test.groups
				default:
					t.Fatalf("unexpected NSS command: %s", command)
				}
				if output == "" {
					return utils.CommandResult{ExitCode: 2}, errors.New("not found")
				}
				return utils.CommandResult{Output: output}, nil
			}
			credential, err := d.mappingCredential(t.Context(), test.user, test.group)
			if test.want == "" && err == nil || test.want != "" && (err != nil || credential != test.want) {
				t.Fatalf("credential = %q, %v; want %q", credential, err, test.want)
			}
		})
	}
}

func TestMappingIdentityDriftWithdrawsRatherThanChangingAccess(t *testing.T) {
	service, driver, share := configuredRuntime(t)
	credential := "1001:1002"
	service.resolveMapping = func(context.Context, string, string) (string, error) { return credential, nil }
	share.MapallUser = "DOMAIN\\user"
	if _, err := service.SaveShare(t.Context(), share.ID, share.NFSShareConfig); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(driver.exports, " -mapall=1001:1002") {
		t.Fatal("mapping not rendered")
	}
	credential = "2001:2002"
	if err := service.checkRuntime(t.Context()); !errors.Is(err, ErrApplyFailed) || driver.running || driver.exports != exportsMarker+"\n" {
		t.Fatalf("identity drift did not revoke exports: %q, %v", driver.exports, err)
	}
	service.resolveMapping = func(context.Context, string, string) (string, error) { return "", errors.New("directory unavailable") }
	share.Enabled = false
	if _, err := service.SaveShare(t.Context(), share.ID, share.NFSShareConfig); err != nil {
		t.Fatalf("unavailable identity prevented disabling: %v", err)
	}
}

func TestUnavailableMappingIsNotSavedAsAnonymousAccess(t *testing.T) {
	service := newTestService(t, testDataset("1", "tank/data", "/mnt/data"))
	service.resolveMapping = func(context.Context, string, string) (string, error) { return "", errors.New("unknown account") }
	input := shareInput("1", "192.0.2.1")
	input.MaprootUser = "missing"
	if _, err := service.SaveShare(t.Context(), 0, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown mapping accepted: %v", err)
	}
	shares, err := service.GetShares(t.Context())
	if err != nil || len(shares) != 0 {
		t.Fatalf("invalid mapping persisted: %#v, %v", shares, err)
	}
}
