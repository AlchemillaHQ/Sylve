// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package pkgmanager

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"
	"golang.org/x/sys/unix"
)

type HostInfo struct {
	ABI             string `json:"abi"`
	Branch          string `json:"branch"`
	Repository      string `json:"repository"`
	OSVersion       uint32 `json:"osVersion"`
	KernelOSVersion uint32 `json:"kernelOSVersion"`
	PackageManager  string `json:"packageManager"`
}

func NewHostClient(log io.Writer) *Client {
	return &Client{Binary: DefaultBinary, Target: Target{Host: true, Name: "host", Root: "/"}, Scope: ScopeHost, Log: log}
}

var hostPackageVersion = regexp.MustCompile(`^([1-9][0-9]*)(?:\.snap[0-9]{14}|\.([0-9]+)(?:p[0-9]+)?)(?:_[0-9]+)?$`)

func HostBranch(version string) (string, error) {
	parts := hostPackageVersion.FindStringSubmatch(version)
	if parts == nil {
		return "", fmt.Errorf("host_base_version_unsupported: %s", version)
	}
	major, _ := strconv.Atoi(parts[1])
	if major < 15 {
		return "", fmt.Errorf("host_base_version_unsupported: %s", version)
	}
	if parts[2] == "" {
		return parts[1] + ".snap", nil
	}
	return parts[1] + "." + parts[2], nil
}

func (c *Client) InspectHost(ctx context.Context, inventory []Package) (HostInfo, error) {
	if !c.Target.Host || c.Scope != ScopeHost || !HasPkgbase(inventory) {
		return HostInfo{}, fmt.Errorf("host_updates_require_pkgbase")
	}
	owner, err := c.Run(ctx, "which", "-qo", "/usr/bin/uname")
	if err != nil {
		return HostInfo{}, err
	}
	if owner.ExitCode != 0 || strings.TrimSpace(owner.Stdout) != "base/FreeBSD-runtime" {
		return HostInfo{}, fmt.Errorf("host_updates_require_pkgbase")
	}
	var info HostInfo
	var runtimeVersion string
	for _, pkg := range inventory {
		if pkg.Name == "pkg" || pkg.Name == "pkg-devel" {
			if info.PackageManager != "" {
				return HostInfo{}, fmt.Errorf("host_package_manager_ambiguous")
			}
			info.PackageManager = pkg.Name
		}
		if !strings.HasPrefix(pkg.Origin, "base/") && !strings.HasPrefix(pkg.Name, "FreeBSD-") {
			continue
		}
		branch, err := HostBranch(pkg.Version)
		if err != nil || pkg.Origin != "base/"+pkg.Name {
			return HostInfo{}, fmt.Errorf("host_base_metadata_invalid")
		}
		if info.Branch != "" && info.Branch != branch {
			return HostInfo{}, fmt.Errorf("host_base_branch_mixed")
		}
		info.Branch = branch
		if pkg.Name == "FreeBSD-runtime" {
			info.Repository = pkg.Repository
			runtimeVersion = pkg.Version
		}
		if Classify(pkg.Name, pkg.Origin) == KindKernel {
			kernel, err := c.hostMetadata(ctx, pkg.Name, "")
			if err != nil {
				return HostInfo{}, err
			}
			if kernel.version != pkg.Version || kernel.osVersion == 0 {
				return HostInfo{}, fmt.Errorf("package_inventory_changed")
			}
			info.KernelOSVersion = max(info.KernelOSVersion, kernel.osVersion)
		}
	}
	metadata, err := c.hostMetadata(ctx, "FreeBSD-runtime", "")
	if err != nil {
		return HostInfo{}, err
	}
	major := strings.Split(info.Branch, ".")[0]
	arch := runtime.GOARCH
	if arch == "arm64" {
		arch = "aarch64"
	}
	info.ABI = "FreeBSD:" + major + ":" + arch
	info.OSVersion = metadata.osVersion
	if metadata.version != runtimeVersion || metadata.abi != info.ABI || info.OSVersion/100000 == 0 || strconv.Itoa(int(info.OSVersion/100000)) != major {
		return HostInfo{}, fmt.Errorf("host_base_metadata_invalid")
	}
	readVersion := hostOSVersion
	if c.HostOSVersion != nil {
		readVersion = c.HostOSVersion
	}
	running, err := readVersion()
	if err != nil || running/100000 != info.OSVersion/100000 {
		return HostInfo{}, fmt.Errorf("host_kernel_major_mismatch")
	}
	repos, _, err := c.Repositories(ctx)
	if err != nil {
		return HostInfo{}, err
	}
	if !slices.ContainsFunc(repos, func(repo Repository) bool { return repo.Name == info.Repository }) {
		return HostInfo{}, fmt.Errorf("host_base_repository_unavailable")
	}
	return info, nil
}

func (c *Client) HostPreview(ctx context.Context, repos []Repository, names []string) (Plan, error) {
	if c.HostSystem == nil || !c.Target.Host {
		return Plan{}, fmt.Errorf("host_base_metadata_invalid")
	}
	for _, name := range names {
		if !ValidName(name) {
			return Plan{}, fmt.Errorf("invalid_package_name")
		}
	}
	if manager := c.HostSystem.PackageManager; manager != "" {
		if c.Log != nil {
			_, _ = fmt.Fprintln(c.Log, "Checking for package-manager updates.")
		}
		managerPlan, err := c.Preview(ctx, repos, []string{manager})
		if err != nil {
			return Plan{}, err
		}
		if slices.ContainsFunc(managerPlan.Changes, func(change Change) bool { return change.Name == manager && change.Action == "upgrade" }) {
			c.HostSelection = []string{manager}
			return managerPlan, c.validateHostChanges(managerPlan)
		}
	}
	c.HostSelection = slices.Clone(names)
	if c.Log != nil {
		_, _ = fmt.Fprintln(c.Log, "Resolving host package upgrades.")
	}
	plan, err := c.Preview(ctx, repos, names)
	if err != nil {
		return Plan{}, err
	}
	return plan, c.validateHostChanges(plan)
}

func (c *Client) validateHostChanges(plan Plan) error {
	if !c.Target.Host || c.HostSystem == nil {
		return fmt.Errorf("host_base_metadata_invalid")
	}
	for _, change := range plan.Changes {
		if !ValidName(change.Name) {
			return fmt.Errorf("invalid_package_name")
		}
		if !slices.Contains([]string{"upgrade", "install", "reinstall", "remove", "downgrade"}, change.Action) {
			return fmt.Errorf("unsupported_package_plan")
		}
		if strings.HasPrefix(change.Name, "FreeBSD-") {
			if change.Action == "remove" || change.Action == "downgrade" {
				return fmt.Errorf("host_base_transaction_unsupported: %s", change.Name)
			}
			for _, version := range []string{change.From, change.To} {
				if version == "" {
					continue
				}
				branch, err := HostBranch(version)
				if err != nil || branch != c.HostSystem.Branch {
					return fmt.Errorf("host_base_branch_changed: %s", change.Name)
				}
			}
		}
		if (change.Name == "pkg" || change.Name == "pkg-devel" || change.Name == "sylve") && (change.Action == "remove" || change.Action == "downgrade") {
			return fmt.Errorf("host_protected_package_change: %s", change.Name)
		}
	}
	return nil
}

func (c *Client) hostMetadata(ctx context.Context, name, archive string) (systemPackage, error) {
	args := []string{"query", systemPackageFormat, name}
	if archive != "" {
		args = []string{"query", "-F", archive, systemPackageFormat}
	}
	result, err := c.Run(ctx, args...)
	if err != nil {
		return systemPackage{}, err
	}
	if result.ExitCode != 0 {
		return systemPackage{}, commandError(result)
	}
	var metadata systemPackage
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 7 || fields[0] != name {
			return systemPackage{}, fmt.Errorf("host_package_metadata_invalid: %s", name)
		}
		if metadata.name != "" && (metadata.name != fields[0] || metadata.version != fields[1] || metadata.origin != fields[2] || metadata.abi != fields[3] || metadata.prefix != fields[4]) {
			return systemPackage{}, fmt.Errorf("host_package_metadata_invalid: %s", name)
		}
		metadata.name, metadata.version, metadata.origin, metadata.abi, metadata.prefix = fields[0], fields[1], fields[2], fields[3], fields[4]
		if fields[5] == "FreeBSD_version" {
			value, parseErr := strconv.ParseUint(fields[6], 10, 32)
			if parseErr != nil || value == 0 || metadata.osVersion != 0 {
				return systemPackage{}, fmt.Errorf("host_package_metadata_invalid: %s", name)
			}
			metadata.osVersion = uint32(value)
		}
	}
	return metadata, nil
}

func (c *Client) hostChangeRepository(ctx context.Context, change Change, repos []Repository) (string, error) {
	if change.Repository != "" {
		if slices.ContainsFunc(repos, func(repo Repository) bool { return repo.Name == change.Repository }) {
			return change.Repository, nil
		}
		return "", fmt.Errorf("package_repository_ambiguous: %s", change.Name)
	}
	var selected string
	for _, repo := range repos {
		result, err := c.Run(ctx, "rquery", "-U", "-r", repo.Name, "%n\t%v", change.Name)
		if err != nil {
			return "", err
		}
		if result.ExitCode != 0 {
			return "", commandError(result)
		}
		if strings.TrimSpace(result.Stdout) == change.Name+"\t"+change.To {
			if selected != "" {
				return "", fmt.Errorf("package_repository_ambiguous: %s", change.Name)
			}
			selected = repo.Name
		}
	}
	if selected == "" {
		return "", fmt.Errorf("package_repository_ambiguous: %s", change.Name)
	}
	return selected, nil
}

func (c *Client) verifyHostCandidates(ctx context.Context, plan Plan, repos []Repository) error {
	if err := c.validateHostChanges(plan); err != nil {
		return err
	}
	targetOSVersion := max(c.HostSystem.OSVersion, c.HostSystem.KernelOSVersion)
	var applications []systemPackage
	err := c.inspectCandidates(ctx, plan, repos, func(change Change, archive string) error {
		metadata, err := c.hostMetadata(ctx, change.Name, archive)
		if err != nil {
			return err
		}
		if metadata.version != change.To || metadata.origin == "" {
			return fmt.Errorf("host_package_metadata_invalid: %s", change.Name)
		}
		if strings.HasPrefix(change.Name, "FreeBSD-") || strings.HasPrefix(metadata.origin, "base/") {
			branch, err := HostBranch(metadata.version)
			if err != nil || branch != c.HostSystem.Branch || metadata.origin != "base/"+change.Name || metadata.prefix != "/" || metadata.abi != c.HostSystem.ABI || metadata.osVersion/100000 != c.HostSystem.OSVersion/100000 {
				return fmt.Errorf("host_base_branch_changed: %s", change.Name)
			}
			targetOSVersion = max(targetOSVersion, metadata.osVersion)
		} else {
			wildcard := strings.Join(strings.Split(c.HostSystem.ABI, ":")[:2], ":") + ":*"
			if metadata.abi != c.HostSystem.ABI && metadata.abi != wildcard && metadata.abi != "*" {
				return fmt.Errorf("host_package_abi_changed: %s", change.Name)
			}
			if metadata.osVersion != 0 && metadata.osVersion/100000 != c.HostSystem.OSVersion/100000 {
				return fmt.Errorf("host_package_abi_changed: %s", change.Name)
			}
			if !strings.HasSuffix(c.HostSystem.Branch, ".snap") && metadata.osVersion != 0 && metadata.osVersion/1000 > c.HostSystem.OSVersion/1000 {
				return fmt.Errorf("host_package_requires_newer_release: %s", change.Name)
			}
			applications = append(applications, metadata)
		}
		if (change.Name == "pkg" || change.Name == "pkg-devel") && !supportedHostPkgVersion(metadata.version) {
			return fmt.Errorf("pkg_version_unsupported")
		}
		if metadata.origin == "sysutils/sylve" || change.Name == "sylve" {
			if c.PreserveExecutable != "" {
				return fmt.Errorf("standalone_sylve_package_conflict")
			}
			if c.SylveVersion == "" {
				return fmt.Errorf("sylve_version_unknown")
			}
			if !sylvePackageUpgradeAllowed(c.SylveVersion, metadata.version) {
				return fmt.Errorf("sylve_downgrade_forbidden")
			}
		}
		_, err = c.packageFiles(ctx, change.Name, archive)
		return err
	})
	if err != nil {
		return err
	}
	for _, metadata := range applications {
		if metadata.osVersion > targetOSVersion {
			return fmt.Errorf("host_package_requires_newer_base: %s", metadata.name)
		}
	}
	return nil
}

func sylvePackageUpgradeAllowed(current, target string) bool {
	target, _, _ = strings.Cut(target, ",")
	target, _, _ = strings.Cut(target, "_")
	return semver.IsValid("v"+current) && semver.IsValid("v"+target) && semver.Compare("v"+target, "v"+current) >= 0
}

func ValidateHostResult(plan Plan, inventory []Package) error {
	installed := make(map[string]string, len(inventory))
	for _, pkg := range inventory {
		installed[pkg.Name] = pkg.Version
	}
	for _, change := range plan.Changes {
		version, present := installed[change.Name]
		if change.Action == "remove" {
			if present {
				return fmt.Errorf("host_update_result_mismatch: %s", change.Name)
			}
		} else if !present || version != change.To {
			return fmt.Errorf("host_update_result_mismatch: %s", change.Name)
		}
	}
	return nil
}

func supportedHostPkgVersion(version string) bool {
	version, _, _ = strings.Cut(version, ",")
	version, _, _ = strings.Cut(version, "_")
	return semver.IsValid("v"+version) && semver.Compare("v"+version, "v2.8.4") >= 0 && semver.Compare("v"+version, "v3.0.0") < 0
}

func validateHostTarget() error {
	jailed, err := unix.SysctlUint32("security.jail.jailed")
	if err != nil || jailed != 0 {
		return fmt.Errorf("host_updates_require_host")
	}
	return nil
}

func ProbeHost() error {
	if err := validateHostTarget(); err != nil {
		return err
	}
	version, err := probeVersion(DefaultBinary)
	if err != nil {
		return err
	}
	if !supportedHostPkgVersion(version) {
		return fmt.Errorf("pkg_version_unsupported")
	}
	return nil
}
