// SPDX-License-Identifier: BSD-2-Clause

package pkgmanager

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Scope string

const (
	ScopeApplications Scope = "applications"
	ScopeSystem       Scope = "system"
	ScopeHost         Scope = "host"
)

func NormalizeScope(scope Scope) (Scope, error) {
	switch scope {
	case "", ScopeApplications:
		return ScopeApplications, nil
	case ScopeSystem:
		return ScopeSystem, nil
	case ScopeHost:
		return ScopeHost, nil
	default:
		return "", fmt.Errorf("invalid_package_scope")
	}
}

type PackageKind string

const (
	KindApplication    PackageKind = "application"
	KindBase           PackageKind = "base"
	KindPackageManager PackageKind = "package-manager"
	KindKernel         PackageKind = "kernel"
	KindProtected      PackageKind = "protected"
)

func Classify(name, origin string) PackageKind {
	if name == "pkg" || name == "pkg-devel" {
		return KindPackageManager
	}
	if strings.HasSuffix(name, "-kmod") || strings.Contains(name, "-kmod-") || strings.Contains(origin, "drm-kmod") ||
		name == "FreeBSD-kernel" || (strings.HasPrefix(name, "FreeBSD-kernel-") && name != "FreeBSD-kernel-man") ||
		strings.HasPrefix(name, "FreeBSD-bootloader") {
		return KindKernel
	}
	if strings.HasPrefix(name, "FreeBSD-") && origin == "base/"+name {
		return KindBase
	}
	if strings.HasPrefix(name, "FreeBSD-") || strings.HasPrefix(origin, "base/") || strings.HasPrefix(origin, "emulators/linux_base") {
		return KindProtected
	}
	return KindApplication
}

func HasPkgbase(inventory []Package) bool {
	runtime, clibs := false, false
	for _, pkg := range inventory {
		if Classify(pkg.Name, pkg.Origin) != KindBase {
			continue
		}
		runtime = runtime || pkg.Name == "FreeBSD-runtime"
		clibs = clibs || pkg.Name == "FreeBSD-clibs"
	}
	return runtime && clibs
}

type SystemInfo struct {
	Release       string `json:"release"`
	ABI           string `json:"abi"`
	OSVersion     uint32 `json:"osVersion"`
	HostOSVersion uint32 `json:"hostOSVersion"`
	Repository    string `json:"repository"`
}

var systemVersion = regexp.MustCompile(`^([1-9][0-9]*)\.([0-9]+)(?:p[0-9]+)?(?:_[0-9]+)?$`)
var systemABI = regexp.MustCompile(`^FreeBSD:([1-9][0-9]*):[A-Za-z0-9_]+$`)

func systemRelease(version string) (string, uint32, error) {
	parts := systemVersion.FindStringSubmatch(version)
	if parts == nil {
		return "", 0, fmt.Errorf("package_system_release_unsupported")
	}
	major, majorErr := strconv.ParseUint(parts[1], 10, 16)
	minor, minorErr := strconv.ParseUint(parts[2], 10, 8)
	if majorErr != nil || minorErr != nil || minor > 99 || major > 999 {
		return "", 0, fmt.Errorf("package_system_release_unsupported")
	}
	return fmt.Sprintf("%d.%d", major, minor), uint32(major*100000 + minor*1000), nil
}

type systemPackage struct {
	name, version, origin, abi, prefix string
	osVersion                          uint32
}

const systemPackageFormat = "%n\t%v\t%o\t%q\t%p\t%At\t%Av"

func (c *Client) systemPackage(ctx context.Context, name, archive string) (systemPackage, error) {
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
	seenVersion := false
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 7 || fields[0] != name {
			return systemPackage{}, fmt.Errorf("package_system_metadata_invalid")
		}
		next := systemPackage{name: fields[0], version: fields[1], origin: fields[2], abi: fields[3], prefix: fields[4]}
		next.osVersion = metadata.osVersion
		if metadata.name != "" && metadata != next {
			return systemPackage{}, fmt.Errorf("package_system_metadata_invalid")
		}
		metadata = next
		if fields[5] == "FreeBSD_version" {
			value, err := strconv.ParseUint(fields[6], 10, 32)
			if err != nil || value == 0 || seenVersion {
				return systemPackage{}, fmt.Errorf("package_system_metadata_invalid")
			}
			metadata.osVersion, seenVersion = uint32(value), true
		}
	}
	if !seenVersion || metadata.prefix != "/" || Classify(metadata.name, metadata.origin) != KindBase {
		return systemPackage{}, fmt.Errorf("package_system_metadata_invalid")
	}
	return metadata, nil
}

func (c *Client) InspectSystem(ctx context.Context, inventory []Package) (SystemInfo, error) {
	var runtime, clibs Package
	for _, pkg := range inventory {
		if pkg.Name == "FreeBSD-runtime" {
			runtime = pkg
		}
		if pkg.Name == "FreeBSD-clibs" {
			clibs = pkg
		}
	}
	if Classify(runtime.Name, runtime.Origin) != KindBase || Classify(clibs.Name, clibs.Origin) != KindBase {
		return SystemInfo{}, fmt.Errorf("package_system_not_pkgbase")
	}
	release, floor, err := systemRelease(runtime.Version)
	if err != nil {
		return SystemInfo{}, err
	}
	for _, pkg := range inventory {
		if !strings.HasPrefix(pkg.Name, "FreeBSD-") && !strings.HasPrefix(pkg.Origin, "base/") {
			continue
		}
		if pkg.Origin != "base/"+pkg.Name {
			return SystemInfo{}, fmt.Errorf("package_system_metadata_invalid")
		}
		other, _, err := systemRelease(pkg.Version)
		if err != nil || other != release {
			return SystemInfo{}, fmt.Errorf("package_system_release_changed")
		}
		if Classify(pkg.Name, pkg.Origin) == KindBase && pkg.Locked {
			return SystemInfo{}, fmt.Errorf("package_system_package_locked")
		}
	}
	metadata, err := c.systemPackage(ctx, runtime.Name, "")
	if err != nil {
		return SystemInfo{}, err
	}
	abi := systemABI.FindStringSubmatch(metadata.abi)
	if metadata.version != runtime.Version || abi == nil || abi[1] != strings.Split(release, ".")[0] || metadata.osVersion/1000 != floor/1000 {
		return SystemInfo{}, fmt.Errorf("package_system_metadata_invalid")
	}
	readVersion := hostOSVersion
	if c.HostOSVersion != nil {
		readVersion = c.HostOSVersion
	}
	host, err := readVersion()
	if err != nil {
		return SystemInfo{}, fmt.Errorf("package_system_host_version_unavailable")
	}
	if host < metadata.osVersion {
		return SystemInfo{}, fmt.Errorf("package_system_host_too_old")
	}
	info := SystemInfo{Release: release, ABI: metadata.abi, OSVersion: metadata.osVersion, HostOSVersion: host, Repository: runtime.Repository}
	probe := *c
	probe.Scope, probe.System = ScopeSystem, &info
	clibsMetadata, err := c.systemPackage(ctx, clibs.Name, "")
	if err != nil {
		return SystemInfo{}, err
	}
	if clibsMetadata.version != clibs.Version {
		return SystemInfo{}, fmt.Errorf("package_system_metadata_invalid")
	}
	if err = probe.validateSystemPackage(clibsMetadata); err != nil {
		return SystemInfo{}, err
	}
	if clibsMetadata.osVersion > info.OSVersion {
		info.OSVersion = clibsMetadata.osVersion
	}
	repos, _, err := probe.Repositories(ctx)
	if err != nil {
		return SystemInfo{}, err
	}
	info.Repository = repos[0].Name
	return info, nil
}

const systemCatalogFormat = "%n\t%v\t%o\t%q"

func (c *Client) validateSystemCatalog(ctx context.Context, repos []Repository, names []string) error {
	if c.System == nil || len(repos) != 1 || repos[0].Name != c.System.Repository {
		return fmt.Errorf("package_system_repository_mixed")
	}
	args := append([]string{"rquery", "-U"}, RepositoryArgs(repos)...)
	args = append(args, systemCatalogFormat)
	args = append(args, names...)
	result, err := c.Run(ctx, args...)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("package_system_catalog_incomplete")
	}
	expected := make(map[string]bool, len(names))
	for _, name := range names {
		expected[name] = false
	}
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			return fmt.Errorf("package_system_catalog_incomplete")
		}
		seen, wanted := expected[fields[0]]
		if !wanted || seen || Classify(fields[0], fields[2]) != KindBase {
			return fmt.Errorf("package_system_catalog_incomplete")
		}
		release, _, err := systemRelease(fields[1])
		if err != nil || release != c.System.Release {
			return fmt.Errorf("package_system_release_changed")
		}
		if fields[3] != c.System.ABI {
			return fmt.Errorf("package_system_abi_changed")
		}
		expected[fields[0]] = true
	}
	for _, seen := range expected {
		if !seen {
			return fmt.Errorf("package_system_catalog_incomplete")
		}
	}
	return nil
}

func (c *Client) validateSystemPackage(metadata systemPackage) error {
	if c.System == nil {
		return fmt.Errorf("package_system_metadata_invalid")
	}
	release, floor, err := systemRelease(metadata.version)
	if err != nil || release != c.System.Release || metadata.osVersion/1000 != floor/1000 {
		return fmt.Errorf("package_system_release_changed")
	}
	if metadata.abi != c.System.ABI {
		return fmt.Errorf("package_system_abi_changed")
	}
	if metadata.osVersion > c.System.HostOSVersion {
		return fmt.Errorf("package_system_host_too_old")
	}
	return nil
}

func (c *Client) validateScopedChanges(plan Plan) error {
	if c.Scope == ScopeHost {
		return c.validateHostChanges(plan)
	}
	scope, err := NormalizeScope(c.Scope)
	if err != nil {
		return err
	}
	for _, change := range plan.Changes {
		if !ValidName(change.Name) {
			return fmt.Errorf("invalid_package_name")
		}
		if scope == ScopeApplications {
			if Protected(change.Name, "") {
				return fmt.Errorf("protected_package_change: %s", change.Name)
			}
			continue
		}
		if c.System == nil || Classify(change.Name, "base/"+change.Name) != KindBase {
			return fmt.Errorf("package_system_transaction_unsupported: %s", change.Name)
		}
		if change.Action != "upgrade" && change.Action != "install" && change.Action != "reinstall" {
			return fmt.Errorf("package_system_transaction_unsupported: %s", change.Name)
		}
		for _, version := range []string{change.From, change.To} {
			if version == "" {
				continue
			}
			release, _, err := systemRelease(version)
			if err != nil || release != c.System.Release {
				return fmt.Errorf("package_system_release_changed")
			}
		}
	}
	return nil
}
