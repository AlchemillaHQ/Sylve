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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/alchemillahq/sylve/pkg/utils"
)

const (
	bootstrapPkgMinimumVersion    = "2.4.0"
	maxBootstrapErrorBytes        = 8 * 1024
	bootstrapErrorTruncationToken = "\n[bootstrap error truncated]"
	bootstrapAuditSampleLines     = 50
	bootstrapPkgOutputSampleBytes = 512
)

type pkgVersion struct {
	parts [4]int
}

type pkgProbeFunc func(ctx context.Context, env []string, path string, args ...string) (utils.CommandResult, error)

type bootstrapPkgRunFunc func(ctx context.Context, env []string, path string, args ...string) (utils.CommandResult, error)

func parsePkgVersion(raw string) (pkgVersion, bool) {
	value := strings.TrimSpace(raw)
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return pkgVersion{}, false
	}

	if idx := strings.IndexByte(value, '-'); idx >= 0 {
		suffix := value[idx+1:]
		value = value[:idx]
		if suffix == "" {
			return pkgVersion{}, false
		}
		parts := strings.Split(suffix, "-")
		if len(parts) > 2 || parts[0] == "" {
			return pkgVersion{}, false
		}
		if !isHexString(parts[0]) {
			return pkgVersion{}, false
		}
		if len(parts) == 2 && parts[1] != "dirty" {
			return pkgVersion{}, false
		}
	}

	if idx := strings.IndexByte(value, '_'); idx >= 0 {
		revision := value[idx+1:]
		value = value[:idx]
		if revision == "" || !isDecimalString(revision) {
			return pkgVersion{}, false
		}
	}

	components := strings.Split(value, ".")
	if len(components) < 3 || len(components) > 4 {
		return pkgVersion{}, false
	}

	parsed := pkgVersion{}
	for i, component := range components {
		if !isDecimalString(component) {
			return pkgVersion{}, false
		}
		number, err := strconv.Atoi(component)
		if err != nil {
			return pkgVersion{}, false
		}
		parsed.parts[i] = number
	}

	return parsed, true
}

func isDecimalString(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isHexString(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

func (v pkgVersion) atLeast(other pkgVersion) bool {
	for i := range v.parts {
		if v.parts[i] != other.parts[i] {
			return v.parts[i] > other.parts[i]
		}
	}
	return true
}

func resolveAndValidatePkg(
	ctx context.Context,
	lookPath func(string) (string, error),
	probe pkgProbeFunc,
	baseEnv []string,
) (string, error) {
	path, err := lookPath("pkg")
	if err != nil || strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("pkg_not_found")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("pkg_not_found")
	}

	env := utils.FilterEnv(baseEnv, "INSTALL_AS_USER", "ASSUME_ALWAYS_YES")

	activation, activationErr := probe(ctx, env, absolute, "-N")
	if activationErr != nil &&
		isPkgNotInstalledDiagnostic(activation.Output+" "+activationErr.Error()) {
		return "", fmt.Errorf("pkg_not_found")
	}

	versionResult, versionErr := probe(ctx, env, absolute, "-v")
	raw := strings.TrimSpace(versionResult.Output)
	if versionErr != nil {
		return "", newPkgVersionUnsupportedError(absolute, raw, versionErr)
	}

	parsed, ok := parsePkgVersion(raw)
	floor, okFloor := parsePkgVersion(bootstrapPkgMinimumVersion)
	if !ok || !okFloor || !parsed.atLeast(floor) {
		return "", newPkgVersionUnsupportedError(absolute, raw, nil)
	}

	return absolute, nil
}

func isPkgNotInstalledDiagnostic(text string) bool {
	return strings.Contains(strings.ToLower(text), "pkg is not installed")
}

func newPkgVersionUnsupportedError(path, raw string, cause error) error {
	sample := sampleCommandOutput(raw, bootstrapPkgOutputSampleBytes)
	if cause != nil {
		return fmt.Errorf("pkg_version_unsupported: resolved=%s output=%q: %w", path, sample, cause)
	}
	return fmt.Errorf("pkg_version_unsupported: resolved=%s output=%q", path, sample)
}

func (s *Service) preflightBootstrapPkg(ctx context.Context) (string, error) {
	if s.bootstrapPkgPreflightFn != nil {
		return s.bootstrapPkgPreflightFn(ctx)
	}
	return resolveAndValidatePkg(ctx, exec.LookPath, utils.RunCommandWithEnvContext, os.Environ())
}

func (s *Service) runBootstrapPkg(ctx context.Context, env []string, path string, args ...string) (utils.CommandResult, error) {
	if s.bootstrapPkgRunFn != nil {
		return s.bootstrapPkgRunFn(ctx, env, path, args...)
	}
	return utils.RunCommandWithEnvContext(ctx, env, path, args...)
}

type bootstrapPkgArgsConfig struct {
	MountPoint          string
	RepoConfDir         string
	OSVersion           string
	ABI                 string
	Major               int
	Minor               int
	FingerprintsRelPath string
	PkgDBDir            string
}

func buildBootstrapPkgArgs(cfg bootstrapPkgArgsConfig, subcmd ...string) []string {
	base := []string{
		"--rootdir", cfg.MountPoint,
		"--repo-conf-dir", cfg.RepoConfDir,
		"-o", "IGNORE_OSVERSION=yes",
		"-o", "OSVERSION=" + cfg.OSVersion,
		"-o", fmt.Sprintf("VERSION_MAJOR=%d", cfg.Major),
		"-o", fmt.Sprintf("VERSION_MINOR=%d", cfg.Minor),
		"-o", "ABI=" + cfg.ABI,
		"-o", "ASSUME_ALWAYS_YES=yes",
		"-o", "FINGERPRINTS=" + cfg.FingerprintsRelPath,
		"-o", "PKG_DBDIR=" + cfg.PkgDBDir,
	}
	return append(base, subcmd...)
}

func buildBootstrapAuditArgs(mountPoint, pkgDBDir, abi string) []string {
	return []string{
		"--rootdir", mountPoint,
		"-o", "PKG_DBDIR=" + pkgDBDir,
		"-o", "ABI=" + abi,
		"-o", "IGNORE_OSVERSION=yes",
		"check", "-q", "-m", "-a",
	}
}

func newBootstrapAuditError(result utils.CommandResult, cause error) error {
	exitCode := result.ExitCode
	if cause != nil && exitCode == 0 {
		exitCode = -1
	}

	details := summarizeCommandOutput(result.Output, bootstrapAuditSampleLines)
	if exitCode <= 0 && cause != nil {
		if details != "" {
			details += " "
		}
		details += fmt.Sprintf("cause=%q", sampleCommandOutput(cause.Error(), bootstrapPkgOutputSampleBytes))
	}

	return fmt.Errorf(
		"bootstrap_metadata_audit_failed: exit_status=%d mismatches=%d lines=%d: %s",
		exitCode,
		countMetadataMismatchLines(result.Output),
		countNonEmptyLines(result.Output),
		details,
	)
}

func newBootstrapCommandError(code string, result utils.CommandResult, cause error) error {
	exitCode := result.ExitCode
	if cause != nil && exitCode == 0 {
		exitCode = -1
	}

	details := summarizeCommandOutput(result.Output, bootstrapAuditSampleLines)
	if exitCode <= 0 && cause != nil {
		if details != "" {
			details += " "
		}
		details += fmt.Sprintf("cause=%q", sampleCommandOutput(cause.Error(), bootstrapPkgOutputSampleBytes))
	}

	return fmt.Errorf(
		"%s: exit_status=%d: %s",
		code,
		exitCode,
		details,
	)
}

func countMetadataMismatchLines(output string) int {
	count := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, " [") &&
			strings.Contains(line, "] ") &&
			strings.Contains(line, " -> ") {
			count++
		}
	}
	return count
}

func countNonEmptyLines(output string) int {
	count := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func summarizeCommandOutput(output string, maxLines int) string {
	trimmed := strings.TrimRight(output, "\n")
	if strings.TrimSpace(trimmed) == "" {
		return ""
	}

	lines := strings.Split(trimmed, "\n")
	if len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}

	return strings.Join(lines[:maxLines], "\n") +
		fmt.Sprintf("\n[... %d more lines]", len(lines)-maxLines)
}

func sampleCommandOutput(output string, limit int) string {
	trimmed := strings.TrimSpace(output)
	if len(trimmed) <= limit {
		return trimmed
	}

	sample := trimmed[:limit]
	for len(sample) > 0 && !utf8.ValidString(sample) {
		sample = sample[:len(sample)-1]
	}
	return sample + "..."
}

func capBootstrapError(message string) string {
	return capUTF8Bytes(message, maxBootstrapErrorBytes)
}

func capBootstrapErrorWithSuffix(message, suffix string) string {
	if suffix == "" {
		return capBootstrapError(message)
	}

	budget := maxBootstrapErrorBytes - len(suffix)
	if budget <= 0 {
		return capUTF8Bytes(suffix, maxBootstrapErrorBytes)
	}
	return capUTF8Bytes(message, budget) + suffix
}

func capUTF8Bytes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	value = strings.ToValidUTF8(value, "\uFFFD")
	if len(value) <= limit {
		return value
	}

	token := bootstrapErrorTruncationToken
	if len(token) >= limit {
		return validUTF8Prefix(value[:limit])
	}
	return validUTF8Prefix(value[:limit-len(token)]) + token
}

func validUTF8Prefix(value string) string {
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
