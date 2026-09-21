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
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"
)

type pkgProbeCall struct {
	env  []string
	args []string
}

type pkgProbeRecorder struct {
	calls     []pkgProbeCall
	responses map[string]utils.CommandResult
	errs      map[string]error
}

func (r *pkgProbeRecorder) probe(_ context.Context, env []string, _ string, args ...string) (utils.CommandResult, error) {
	key := strings.Join(args, " ")
	r.calls = append(r.calls, pkgProbeCall{
		env:  append([]string(nil), env...),
		args: append([]string(nil), args...),
	})
	return r.responses[key], r.errs[key]
}

func TestParsePkgVersion(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want bool
	}{
		{raw: "2.4.0", want: true},
		{raw: "2.4.2", want: true},
		{raw: "2.4.2.1", want: true},
		{raw: "2.4.2_1", want: true},
		{raw: "2.4.2-1a2b3c4", want: true},
		{raw: "2.4.2-1a2b3c4-dirty", want: true},
		{raw: "2.4.2.1_2-abc1234-dirty", want: true},
		{raw: "2.5.1", want: true},
		{raw: "2.4", want: false},
		{raw: "2.4.2_", want: false},
		{raw: "2.4.2_1_2", want: false},
		{raw: "2.4.2-", want: false},
		{raw: "2.4.2-dirty", want: false},
		{raw: "2.4.2-abc-def", want: false},
		{raw: "2.4.2+1", want: false},
		{raw: "2..4", want: false},
		{raw: "2.4.x", want: false},
		{raw: "2.4.2 build", want: false},
		{raw: "garbage", want: false},
		{raw: "", want: false},
	} {
		t.Run(test.raw, func(t *testing.T) {
			_, ok := parsePkgVersion(test.raw)
			if ok != test.want {
				t.Fatalf("parsePkgVersion(%q) ok = %v, want %v", test.raw, ok, test.want)
			}
		})
	}
}

func TestPkgVersionAtLeast(t *testing.T) {
	floor, ok := parsePkgVersion(bootstrapPkgMinimumVersion)
	if !ok {
		t.Fatal("minimum version does not parse")
	}
	for _, test := range []struct {
		raw  string
		want bool
	}{
		{raw: "2.4.0", want: true},
		{raw: "2.4.0-1a2b3c4", want: true},
		{raw: "2.4.2_1", want: true},
		{raw: "2.5.0", want: true},
		{raw: "3.0.0", want: true},
		{raw: "2.3.99.9", want: false},
		{raw: "2.4.0.0", want: true},
		{raw: "2.3.0", want: false},
	} {
		parsed, ok := parsePkgVersion(test.raw)
		if !ok {
			t.Fatalf("parsePkgVersion(%q) failed", test.raw)
		}
		if got := parsed.atLeast(floor); got != test.want {
			t.Fatalf("%q atLeast %q = %v, want %v", test.raw, bootstrapPkgMinimumVersion, got, test.want)
		}
	}
}

func TestResolveAndValidatePkg_ProbesActivationWithoutInheritedOverrides(t *testing.T) {
	recorder := &pkgProbeRecorder{
		responses: map[string]utils.CommandResult{
			"-N": {},
			"-v": {Output: "2.4.2\n"},
		},
		errs: map[string]error{},
	}
	baseEnv := []string{
		"PATH=/sbin:/bin",
		"INSTALL_AS_USER=yes",
		"INSTALL_AS_USER=",
		"ASSUME_ALWAYS_YES=yes",
		"HOME=/root",
	}

	path, err := resolveAndValidatePkg(
		context.Background(),
		func(string) (string, error) { return "/usr/sbin/pkg", nil },
		recorder.probe,
		baseEnv,
	)
	if err != nil {
		t.Fatalf("resolveAndValidatePkg: %v", err)
	}
	if path != "/usr/sbin/pkg" {
		t.Fatalf("path = %q", path)
	}
	if len(recorder.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(recorder.calls))
	}
	if strings.Join(recorder.calls[0].args, " ") != "-N" {
		t.Fatalf("first probe = %v, want -N", recorder.calls[0].args)
	}
	if strings.Join(recorder.calls[1].args, " ") != "-v" {
		t.Fatalf("second probe = %v, want -v", recorder.calls[1].args)
	}
	for _, call := range recorder.calls {
		for _, entry := range call.env {
			if strings.HasPrefix(entry, "INSTALL_AS_USER") ||
				strings.HasPrefix(entry, "ASSUME_ALWAYS_YES") {
				t.Fatalf("preflight env leaked %q", entry)
			}
		}
	}
}

func TestResolveAndValidatePkg_MapsShimMissingToNotFound(t *testing.T) {
	recorder := &pkgProbeRecorder{
		responses: map[string]utils.CommandResult{
			"-N": {Output: "pkg: pkg is not installed\n", ExitCode: 1},
		},
		errs: map[string]error{"-N": errors.New("exit status 1")},
	}

	_, err := resolveAndValidatePkg(
		context.Background(),
		func(string) (string, error) { return "/usr/sbin/pkg", nil },
		recorder.probe,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "pkg_not_found") {
		t.Fatalf("expected pkg_not_found, got %v", err)
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(recorder.calls))
	}
}

func TestResolveAndValidatePkg_ContinuesAfterEmptyHostDatabase(t *testing.T) {
	recorder := &pkgProbeRecorder{
		responses: map[string]utils.CommandResult{
			"-N": {Output: "pkg: package database non-existent\n", ExitCode: 1},
			"-v": {Output: "2.4.2\n"},
		},
		errs: map[string]error{"-N": errors.New("exit status 1")},
	}

	path, err := resolveAndValidatePkg(
		context.Background(),
		func(string) (string, error) { return "/usr/local/sbin/pkg", nil },
		recorder.probe,
		nil,
	)
	if err != nil {
		t.Fatalf("resolveAndValidatePkg: %v", err)
	}
	if path != "/usr/local/sbin/pkg" || len(recorder.calls) != 2 {
		t.Fatalf("path=%q calls=%d", path, len(recorder.calls))
	}
}

func TestResolveAndValidatePkg_RejectsOldAndUnparseableVersions(t *testing.T) {
	for _, raw := range []string{"2.3.99.1", "garbage"} {
		t.Run(raw, func(t *testing.T) {
			recorder := &pkgProbeRecorder{
				responses: map[string]utils.CommandResult{
					"-N": {},
					"-v": {Output: raw + "\n"},
				},
				errs: map[string]error{},
			}

			_, err := resolveAndValidatePkg(
				context.Background(),
				func(string) (string, error) { return "/usr/local/sbin/pkg", nil },
				recorder.probe,
				nil,
			)
			if err == nil || !strings.Contains(err.Error(), "pkg_version_unsupported") {
				t.Fatalf("expected pkg_version_unsupported, got %v", err)
			}
			if !strings.Contains(err.Error(), "/usr/local/sbin/pkg") ||
				!strings.Contains(err.Error(), raw) {
				t.Fatalf("error lacks resolved path or raw version: %v", err)
			}
		})
	}
}

func TestResolveAndValidatePkg_AcceptsRevisionAndGitSuffix(t *testing.T) {
	recorder := &pkgProbeRecorder{
		responses: map[string]utils.CommandResult{
			"-N": {},
			"-v": {Output: "2.4.2_1-1a2b3c4-dirty\n"},
		},
		errs: map[string]error{},
	}

	path, err := resolveAndValidatePkg(
		context.Background(),
		func(string) (string, error) { return "/usr/local/sbin/pkg", nil },
		recorder.probe,
		nil,
	)
	if err != nil {
		t.Fatalf("resolveAndValidatePkg: %v", err)
	}
	if path != "/usr/local/sbin/pkg" {
		t.Fatalf("path = %q", path)
	}
}

func TestBuildBootstrapPkgArgsOmitsInstallAsUser(t *testing.T) {
	cfg := bootstrapPkgArgsConfig{
		MountPoint:          "/tank/sylve/bootstraps/15-0-Base",
		RepoConfDir:         "/tmp/repo",
		OSVersion:           "1500000",
		ABI:                 "FreeBSD:15:amd64",
		Major:               15,
		Minor:               0,
		FingerprintsRelPath: "/usr/share/keys/pkgbase-15",
		PkgDBDir:            "/tank/sylve/bootstraps/15-0-Base/var/db/pkg",
	}

	args := buildBootstrapPkgArgs(cfg, "install", "-r", "FreeBSD-base-release-0", "FreeBSD-set-base-jail")
	joined := strings.Join(args, "\n")
	if strings.Contains(joined, "INSTALL_AS_USER") {
		t.Fatalf("bootstrap args contain INSTALL_AS_USER: %v", args)
	}

	for _, want := range []string{
		"--rootdir",
		cfg.MountPoint,
		"--repo-conf-dir",
		cfg.RepoConfDir,
		"ASSUME_ALWAYS_YES=yes",
		"ABI=" + cfg.ABI,
		"FINGERPRINTS=" + cfg.FingerprintsRelPath,
		"PKG_DBDIR=" + cfg.PkgDBDir,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("bootstrap args missing %q: %v", want, args)
		}
	}
	if got := args[len(args)-1]; got != "FreeBSD-set-base-jail" {
		t.Fatalf("subcommand not appended: %v", args)
	}
}

func TestBuildBootstrapAuditArgs(t *testing.T) {
	args := buildBootstrapAuditArgs(
		"/tank/sylve/bootstraps/15-0-Base",
		"/tank/sylve/bootstraps/15-0-Base/var/db/pkg",
		"FreeBSD:15:amd64",
	)
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--rootdir /tank/sylve/bootstraps/15-0-Base",
		"PKG_DBDIR=/tank/sylve/bootstraps/15-0-Base/var/db/pkg",
		"ABI=FreeBSD:15:amd64",
		"check -q -m -a",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("audit args missing %q: %v", want, args)
		}
	}
	if strings.Contains(joined, "INSTALL_AS_USER") {
		t.Fatalf("audit args contain INSTALL_AS_USER: %v", args)
	}
}

func TestNewBootstrapAuditErrorCountsAndSamplesOutput(t *testing.T) {
	var lines []string
	for i := 0; i < 60; i++ {
		if i%20 == 0 {
			lines = append(lines, fmt.Sprintf("FreeBSD-runtime-15.1: /var/mail [gname] mail -> wheel"))
			continue
		}
		lines = append(lines, fmt.Sprintf("pkg: checking entry %d", i))
	}
	output := strings.Join(lines, "\n") + "\n"
	result := utils.CommandResult{Output: output, ExitCode: 1}

	err := newBootstrapAuditError(result, errors.New("exit status 1"))
	if err == nil {
		t.Fatal("expected error")
	}
	message := err.Error()
	for _, want := range []string{
		"bootstrap_metadata_audit_failed",
		"exit_status=1",
		"mismatches=3",
		"lines=60",
		"[... 10 more lines]",
		"[gname] mail -> wheel",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("summary missing %q: %s", want, message)
		}
	}
	if got := countMetadataMismatchLines(output); got != 3 {
		t.Fatalf("countMetadataMismatchLines = %d, want 3", got)
	}
}

func TestNewBootstrapCommandErrorSamplesOutput(t *testing.T) {
	var lines []string
	for i := 0; i < 120; i++ {
		lines = append(lines, fmt.Sprintf("install line %d", i))
	}
	result := utils.CommandResult{Output: strings.Join(lines, "\n"), ExitCode: 1}

	err := newBootstrapCommandError("failed_to_install_packages", result, errors.New("exit status 1"))
	message := err.Error()
	if !strings.HasPrefix(message, "failed_to_install_packages: exit_status=1") {
		t.Fatalf("unexpected error prefix: %s", message)
	}
	if !strings.Contains(message, "[... 70 more lines]") {
		t.Fatalf("expected omitted line count: %s", message)
	}
	if count := strings.Count(message, "\n"); count > bootstrapAuditSampleLines+1 {
		t.Fatalf("summary not sampled: %d newlines", count)
	}
}

func TestCapBootstrapErrorBoundsAndKeepsUTF8(t *testing.T) {
	message := strings.Repeat("é", maxBootstrapErrorBytes) + "tail"
	capped := capBootstrapError(message)
	if len(capped) > maxBootstrapErrorBytes {
		t.Fatalf("capped length = %d", len(capped))
	}
	if !utf8.ValidString(capped) {
		t.Fatal("capped message is not valid UTF-8")
	}
	if !strings.HasSuffix(capped, bootstrapErrorTruncationToken) {
		t.Fatalf("capped message lacks truncation token: %q", capped[len(capped)-40:])
	}
}

func TestCapBootstrapErrorWithSuffixKeepsCleanupFailure(t *testing.T) {
	message := strings.Repeat("m", maxBootstrapErrorBytes*2)
	suffix := ": cleanup_failed: dataset is busy"
	capped := capBootstrapErrorWithSuffix(message, suffix)
	if len(capped) > maxBootstrapErrorBytes {
		t.Fatalf("capped length = %d", len(capped))
	}
	if !strings.HasSuffix(capped, suffix) {
		t.Fatalf("cleanup suffix was truncated: %q", capped[len(capped)-60:])
	}
	if !utf8.ValidString(capped) {
		t.Fatal("capped message is not valid UTF-8")
	}
}

func TestCapBootstrapErrorNormalizesInvalidUTF8(t *testing.T) {
	short := "prefix\xff"
	if got := capBootstrapError(short); !utf8.ValidString(got) {
		t.Fatalf("short capped message is not valid UTF-8: %q", got)
	}

	long := "prefix\xff\xfe" + strings.Repeat("x", maxBootstrapErrorBytes)
	capped := capBootstrapError(long)
	if !utf8.ValidString(capped) {
		t.Fatal("capped message is not valid UTF-8")
	}
	if len(capped) > maxBootstrapErrorBytes {
		t.Fatalf("capped length = %d", len(capped))
	}
}

func TestBootstrapErrorsIncludeCauseWithoutExitStatus(t *testing.T) {
	cause := errors.New(`exec: "pkg": executable file not found in $PATH`)

	commandErr := newBootstrapCommandError(
		"failed_to_update_repo",
		utils.CommandResult{ExitCode: -1},
		cause,
	)
	for _, want := range []string{"failed_to_update_repo", "exit_status=-1", "executable file not found"} {
		if !strings.Contains(commandErr.Error(), want) {
			t.Fatalf("command error missing %q: %v", want, commandErr)
		}
	}

	auditErr := newBootstrapAuditError(utils.CommandResult{ExitCode: -1}, cause)
	for _, want := range []string{"bootstrap_metadata_audit_failed", "exit_status=-1", "executable file not found"} {
		if !strings.Contains(auditErr.Error(), want) {
			t.Fatalf("audit error missing %q: %v", want, auditErr)
		}
	}
}

func TestCreateBootstrap_PropagatesPkgPreflightFailure(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")
	svc.bootstrapPkgPreflightFn = func(context.Context) (string, error) {
		return "", fmt.Errorf("pkg_version_unsupported: resolved=/usr/sbin/pkg output=\"2.3.0\"")
	}

	_, err := svc.CreateBootstrap(context.Background(), jailServiceInterfaces.BootstrapRequest{
		Pool: "tank", Major: 15, Minor: 0, Type: "base",
	})
	if err == nil || !strings.Contains(err.Error(), "pkg_version_unsupported") {
		t.Fatalf("expected pkg_version_unsupported, got %v", err)
	}

	var count int64
	if err := svc.DB.Model(&jailModels.JailBootstrap{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("preflight failure created %d bootstrap records", count)
	}
}

func TestCreateBootstrap_RequiresCleanupForPhaseMarkedDataset(t *testing.T) {
	dataset := "tank/sylve/bootstraps/15-0-Base"
	svc, _ := newBootstrapTestService(t, []string{dataset}, "tank")

	if err := svc.DB.Create(&jailModels.JailBootstrap{
		Pool:          "tank",
		Dataset:       dataset,
		MountPoint:    "/tank/sylve/bootstraps/15-0-Base",
		Name:          "15-0-Base",
		Major:         15,
		Minor:         0,
		BootstrapType: "base",
		Status:        "failed",
		Phase:         "legacy_pkgbase_reset_v2",
	}).Error; err != nil {
		t.Fatal(err)
	}

	_, err := svc.CreateBootstrap(context.Background(), jailServiceInterfaces.BootstrapRequest{
		Pool: "tank", Major: 15, Minor: 0, Type: "base",
	})
	if err == nil || !strings.Contains(err.Error(), "bootstrap_cleanup_required") {
		t.Fatalf("expected bootstrap_cleanup_required, got %v", err)
	}
}

func TestUpdateBootstrapRecordCapsStoredError(t *testing.T) {
	svc, _ := newBootstrapTestService(t, nil, "tank")

	record := jailModels.JailBootstrap{
		Pool:          "tank",
		Dataset:       "tank/sylve/bootstraps/15-0-Base",
		Name:          "15-0-Base",
		Major:         15,
		Minor:         0,
		BootstrapType: "base",
		Status:        "running",
	}
	if err := svc.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}

	svc.updateBootstrapRecord(
		record.ID,
		"failed",
		"installing",
		strings.Repeat("x", maxBootstrapErrorBytes*3),
	)

	var stored jailModels.JailBootstrap
	if err := svc.DB.First(&stored, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "failed" || stored.Phase != "installing" {
		t.Fatalf("unexpected stored record: %#v", stored)
	}
	if len(stored.Error) > maxBootstrapErrorBytes {
		t.Fatalf("stored error length = %d", len(stored.Error))
	}
	if !utf8.ValidString(stored.Error) {
		t.Fatal("stored error is not valid UTF-8")
	}
}
