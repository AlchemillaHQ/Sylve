// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const orchestrationMountFlag = "--rootdir"

type orchestrationPkgCall struct {
	path string
	env  []string
	args []string
}

func orchestrationTypeSpec() jailServiceInterfaces.BootstrapTypeSpec {
	return jailServiceInterfaces.BootstrapTypeSpec{
		Type:   "base",
		Name:   "%d-%d-Base",
		Label:  "FreeBSD %d.%d Base",
		PkgSet: "FreeBSD-set-base-jail",
	}
}

func orchestrationIdentity() bootstrapIdentity {
	return bootstrapIdentity{
		Pool:    "tank",
		Name:    "15-0-Base",
		Dataset: "tank/sylve/bootstraps/15-0-Base",
		Major:   15,
		Minor:   0,
		Type:    "base",
	}
}

func orchestrationMountpoint(args []string) string {
	for i, arg := range args {
		if arg == orchestrationMountFlag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func orchestrationSeedRecord(t *testing.T, svc *Service) jailModels.JailBootstrap {
	t.Helper()

	identity := orchestrationIdentity()
	record := jailModels.JailBootstrap{
		Pool:          identity.Pool,
		Dataset:       identity.Dataset,
		Name:          identity.Name,
		Major:         identity.Major,
		Minor:         identity.Minor,
		BootstrapType: identity.Type,
		Status:        "pending",
	}
	if err := svc.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	return record
}

func orchestrationRunner(
	t *testing.T,
	calls *[]orchestrationPkgCall,
	auditResult utils.CommandResult,
	auditErr error,
) bootstrapPkgRunFunc {
	t.Helper()

	return func(_ context.Context, env []string, path string, args ...string) (utils.CommandResult, error) {
		*calls = append(*calls, orchestrationPkgCall{
			path: path,
			env:  append([]string(nil), env...),
			args: append([]string(nil), args...),
		})

		if mountpoint := orchestrationMountpoint(args); mountpoint != "" {
			for _, dir := range []string{"etc", "root", "usr/share/skel", "usr/local/etc/pkg/repos"} {
				if err := os.MkdirAll(filepath.Join(mountpoint, dir), 0755); err != nil {
					t.Fatalf("prepare mountpoint: %v", err)
				}
			}
		}

		if strings.Contains(strings.Join(args, " "), "check -q -m -a") {
			return auditResult, auditErr
		}
		return utils.CommandResult{}, nil
	}
}

func TestRunBootstrapUsesResolvedPkgPathEnvironmentAndOrder(t *testing.T) {
	if _, err := os.Stat("/usr/share/keys/pkgbase-15"); err != nil {
		t.Skipf("pkgbase keys unavailable: %v", err)
	}
	t.Setenv("INSTALL_AS_USER", "yes")

	svc, runner := newBootstrapTestService(t, nil, "tank")
	record := orchestrationSeedRecord(t, svc)

	var calls []orchestrationPkgCall
	svc.bootstrapPkgRunFn = orchestrationRunner(t, &calls, utils.CommandResult{}, nil)

	svc.runBootstrap(
		record.ID,
		"tank:15-0-Base",
		jailServiceInterfaces.BootstrapRequest{Pool: "tank", Major: 15, Minor: 0, Type: "base"},
		orchestrationTypeSpec(),
		orchestrationIdentity(),
		"/resolved/pkg",
	)

	if len(calls) != 4 {
		t.Fatalf("pkg calls = %d, want 4: %#v", len(calls), calls)
	}
	joined := make([]string, len(calls))
	for i, call := range calls {
		joined[i] = strings.Join(call.args, " ")
		if call.path != "/resolved/pkg" {
			t.Fatalf("call %d used path %q", i, call.path)
		}
		for _, entry := range call.env {
			if strings.HasPrefix(entry, "INSTALL_AS_USER") {
				t.Fatalf("call %d leaked %q", i, entry)
			}
		}
	}
	if !strings.HasSuffix(joined[0], "update -r FreeBSD-base-release-0") {
		t.Fatalf("first call = %q", joined[0])
	}
	if !strings.HasSuffix(joined[1], "install -r FreeBSD-base-release-0 FreeBSD-set-base-jail") {
		t.Fatalf("second call = %q", joined[1])
	}
	if !strings.HasSuffix(joined[2], "install pkg") {
		t.Fatalf("third call = %q", joined[2])
	}
	if !strings.HasSuffix(joined[3], "check -q -m -a") {
		t.Fatalf("fourth call = %q", joined[3])
	}

	var stored jailModels.JailBootstrap
	if err := svc.DB.First(&stored, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "completed" || stored.Phase != "" {
		t.Fatalf("record = %#v", stored)
	}
	if !runner.hasDataset(orchestrationIdentity().Dataset) {
		t.Fatal("fake dataset was removed")
	}
	if _, err := os.Stat(filepath.Join(storedMountpoint(t, svc, record.ID), "etc", "rc.conf")); err != nil {
		t.Fatalf("rc.conf was not written after the audit: %v", err)
	}
}

func TestRunBootstrapAuditFailureCleansUpBeforeWritingConfig(t *testing.T) {
	if _, err := os.Stat("/usr/share/keys/pkgbase-15"); err != nil {
		t.Skipf("pkgbase keys unavailable: %v", err)
	}

	svc, runner := newBootstrapTestService(t, nil, "tank")
	record := orchestrationSeedRecord(t, svc)

	var calls []orchestrationPkgCall
	auditOutput := strings.Join([]string{
		"FreeBSD-runtime-15.1: /var/mail [gname] mail -> wheel",
		"FreeBSD-runtime-15.1: /var/empty [fflags] schg -> ",
	}, "\n")
	svc.bootstrapPkgRunFn = orchestrationRunner(
		t,
		&calls,
		utils.CommandResult{Output: auditOutput, ExitCode: 1},
		errors.New("exit status 1"),
	)

	svc.runBootstrap(
		record.ID,
		"tank:15-0-Base",
		jailServiceInterfaces.BootstrapRequest{Pool: "tank", Major: 15, Minor: 0, Type: "base"},
		orchestrationTypeSpec(),
		orchestrationIdentity(),
		"/resolved/pkg",
	)

	var stored jailModels.JailBootstrap
	if err := svc.DB.First(&stored, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "failed" || stored.Phase != "auditing_metadata" {
		t.Fatalf("record = %#v", stored)
	}
	for _, want := range []string{
		"bootstrap_metadata_audit_failed",
		"mismatches=2",
		"[gname] mail -> wheel",
	} {
		if !strings.Contains(stored.Error, want) {
			t.Fatalf("stored error missing %q: %s", want, stored.Error)
		}
	}
	if len(stored.Error) > maxBootstrapErrorBytes || !utf8.ValidString(stored.Error) {
		t.Fatalf("stored error violates the persistence cap: len=%d", len(stored.Error))
	}
	if runner.hasDataset(orchestrationIdentity().Dataset) {
		t.Fatal("failed audit left the partial dataset behind")
	}
	if _, err := os.Stat(filepath.Join(storedMountpoint(t, svc, record.ID), "etc", "rc.conf")); !os.IsNotExist(err) {
		t.Fatalf("writing_config ran before the audit completed: %v", err)
	}
}

func storedMountpoint(t *testing.T, svc *Service, recordID uint) string {
	t.Helper()

	var stored jailModels.JailBootstrap
	if err := svc.DB.First(&stored, recordID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.MountPoint == "" {
		t.Fatal("stored mountpoint is empty")
	}
	return stored.MountPoint
}
