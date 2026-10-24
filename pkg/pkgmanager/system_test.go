// SPDX-License-Identifier: BSD-2-Clause

package pkgmanager

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

const systemTestRepositories = "Repositories:\nFreeBSD-ports: {\nurl: \"https://example.invalid/ports\",\nenabled: yes,\n}\nFreeBSD-base: {\nurl: \"https://example.invalid/base_release_1\",\nenabled: yes,\n}\n"

func systemTestInventory() []Package {
	return []Package{
		{Name: "FreeBSD-runtime", Version: "15.1p1", Origin: "base/FreeBSD-runtime", Repository: "FreeBSD-base-release-1", Protected: true},
		{Name: "FreeBSD-clibs", Version: "15.1", Origin: "base/FreeBSD-clibs", Repository: "FreeBSD-base-release-1", Protected: true},
		{Name: "pkg", Version: "2.6.2", Origin: "ports-mgmt/pkg", Protected: true},
		{Name: "app", Version: "1", Origin: "misc/app"},
	}
}

func systemTestMetadata(name, version string) string {
	return fmt.Sprintf("%s\t%s\tbase/%s\tFreeBSD:15:amd64\t/\tFreeBSD_version\t1501000\n", name, version, name)
}

func TestPackageClassificationSeparatesSystemAndApplications(t *testing.T) {
	for _, test := range []struct {
		name, origin string
		kind         PackageKind
	}{
		{"app", "misc/app", KindApplication},
		{"pkg", "ports-mgmt/pkg", KindPackageManager},
		{"pkg-devel", "ports-mgmt/pkg-devel", KindPackageManager},
		{"FreeBSD-runtime", "base/FreeBSD-runtime", KindBase},
		{"FreeBSD-clibs", "base/FreeBSD-clibs", KindBase},
		{"FreeBSD-pkg-bootstrap", "base/FreeBSD-pkg-bootstrap", KindBase},
		{"FreeBSD-kernel-man", "base/FreeBSD-kernel-man", KindBase},
		{"FreeBSD-kernel-generic", "base/FreeBSD-kernel-generic", KindKernel},
		{"FreeBSD-bootloader", "base/FreeBSD-bootloader", KindKernel},
		{"drm-66-kmod", "graphics/drm-66-kmod", KindKernel},
		{"FreeBSD-runtime", "misc/not-base", KindProtected},
		{"custom", "base/custom", KindProtected},
		{"linux_base-rl9", "emulators/linux_base-rl9", KindProtected},
	} {
		t.Run(test.name+"/"+test.origin, func(t *testing.T) {
			if got := Classify(test.name, test.origin); got != test.kind || Protected(test.name, test.origin) != (test.kind != KindApplication) {
				t.Fatalf("classification %q, expected %q", got, test.kind)
			}
		})
	}
	if scope, err := NormalizeScope(""); err != nil || scope != ScopeApplications {
		t.Fatalf("legacy scope: %s %v", scope, err)
	}
	if _, err := NormalizeScope("everything"); err == nil {
		t.Fatal("accepted unknown scope")
	}
}

func TestInspectPkgbaseSystemFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name      string
		inventory func([]Package) []Package
		metadata  func(string) string
		clibs     func(string) string
		repos     string
		host      uint32
		hostError bool
		want      string
	}{
		{name: "valid"},
		{name: "additional annotations", metadata: func(s string) string { return s + strings.ReplaceAll(s, "FreeBSD_version", "unrelated") }},
		{name: "missing runtime registration", inventory: func(p []Package) []Package { return p[1:] }, want: "not_pkgbase"},
		{name: "missing clibs registration", inventory: func(p []Package) []Package { return append(p[:1], p[2:]...) }, want: "not_pkgbase"},
		{name: "noncanonical runtime origin", inventory: func(p []Package) []Package { p[0].Origin = "base/runtime"; return p }, want: "not_pkgbase"},
		{name: "noncanonical clibs origin", inventory: func(p []Package) []Package { p[1].Origin = "base/clibs"; return p }, want: "not_pkgbase"},
		{name: "mixed releases", inventory: func(p []Package) []Package { p[1].Version = "15.2"; return p }, want: "release_changed"},
		{name: "unknown release", inventory: func(p []Package) []Package { p[0].Version = "15.1.snap202610"; return p }, want: "release_unsupported"},
		{name: "locked base", inventory: func(p []Package) []Package { p[1].Locked = true; return p }, want: "package_locked"},
		{name: "historical source alias after a partial update", inventory: func(p []Package) []Package { p[0].Repository = "FreeBSD-base"; return p }},
		{name: "registered archives without a source annotation", inventory: func(p []Package) []Package { p[0].Repository = "unknown-repository"; return p }},
		{name: "old host", host: 1500999, want: "host_too_old"},
		{name: "unknown host", hostError: true, want: "host_version_unavailable"},
		{name: "wrong ABI", metadata: func(s string) string { return strings.ReplaceAll(s, "FreeBSD:15:amd64", "FreeBSD:16:amd64") }, want: "metadata_invalid"},
		{name: "clibs ABI mismatch", clibs: func(s string) string { return strings.ReplaceAll(s, "amd64", "aarch64") }, want: "abi_changed"},
		{name: "clibs registration mismatch", clibs: func(s string) string { return strings.ReplaceAll(s, "\t15.1\t", "\t15.1p1\t") }, want: "metadata_invalid"},
		{name: "wrong prefix", metadata: func(s string) string { return strings.ReplaceAll(s, "\t/\t", "\t/usr/local\t") }, want: "metadata_invalid"},
		{name: "wrong origin", metadata: func(s string) string { return strings.ReplaceAll(s, "base/FreeBSD-runtime", "misc/runtime") }, want: "metadata_invalid"},
		{name: "missing OSVERSION", metadata: func(s string) string { return strings.ReplaceAll(s, "FreeBSD_version", "unrelated") }, want: "metadata_invalid"},
		{name: "duplicate OSVERSION", metadata: func(s string) string { return s + s }, want: "metadata_invalid"},
		{name: "inconsistent annotation metadata", metadata: func(s string) string {
			return s + strings.ReplaceAll(strings.ReplaceAll(s, "FreeBSD_version", "unrelated"), "15.1p1", "15.1p2")
		}, want: "metadata_invalid"},
		{name: "OSVERSION release mismatch", metadata: func(s string) string { return strings.ReplaceAll(s, "1501000", "1502000") }, want: "metadata_invalid"},
		{name: "no base repository", repos: "Repositories:\nports: {\nurl: \"https://example.invalid/ports\",\nenabled: yes,\n}\n", want: "repository_unavailable"},
		{name: "ambiguous base aliases", repos: systemTestRepositories + "FreeBSD-base-other: {\nurl: \"https://other.invalid/base_release_1\",\nenabled: yes,\n}\n", want: "repository_ambiguous"},
	} {
		t.Run(test.name, func(t *testing.T) {
			inventory := systemTestInventory()
			if test.inventory != nil {
				inventory = test.inventory(inventory)
			}
			metadata := systemTestMetadata("FreeBSD-runtime", "15.1p1")
			if test.metadata != nil {
				metadata = test.metadata(metadata)
			}
			repos := systemTestRepositories
			if test.repos != "" {
				repos = test.repos
			}
			client := Client{Target: Target{JID: 1, Name: "fixture", Root: "/fixture"}, Scope: ScopeSystem,
				HostOSVersion: func() (uint32, error) {
					if test.hostError {
						return 0, errors.New("unavailable")
					}
					if test.host != 0 {
						return test.host, nil
					}
					return 1600027, nil
				},
				RunCommand: func(_ context.Context, args []string) (Result, error) {
					if args[len(args)-1] == "-vv" {
						return Result{Stdout: repos}, nil
					}
					if slices.Contains(args, systemPackageFormat) {
						if args[len(args)-1] == "FreeBSD-clibs" {
							value := systemTestMetadata("FreeBSD-clibs", "15.1")
							if test.clibs != nil {
								value = test.clibs(value)
							}
							return Result{Stdout: value}, nil
						}
						return Result{Stdout: metadata}, nil
					}
					t.Fatalf("unexpected command: %v", args)
					return Result{}, nil
				}}
			info, err := client.InspectSystem(t.Context(), inventory)
			if test.want != "" {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("expected %s, got %+v: %v", test.want, info, err)
				}
				return
			}
			if err != nil || info.Release != "15.1" || info.ABI != "FreeBSD:15:amd64" || info.Repository != "FreeBSD-base" || info.OSVersion != 1501000 || info.HostOSVersion != 1600027 {
				t.Fatalf("system %+v: %v", info, err)
			}
		})
	}
}

func TestSystemTransactionsRejectOtherScopesAndReleaseChanges(t *testing.T) {
	info := &SystemInfo{Release: "15.1", ABI: "FreeBSD:15:amd64", OSVersion: 1501000, HostOSVersion: 1600027, Repository: "FreeBSD-base"}
	for _, test := range []struct {
		name, action, from, to string
		allowed                bool
	}{
		{"FreeBSD-runtime", "upgrade", "15.1", "15.1p1", true},
		{"FreeBSD-kernel-man", "upgrade", "15.1", "15.1p1", true},
		{"FreeBSD-libnew", "install", "", "15.1p1", true},
		{"FreeBSD-runtime", "remove", "15.1", "", false},
		{"FreeBSD-runtime", "downgrade", "15.1p2", "15.1p1", false},
		{"FreeBSD-runtime", "upgrade", "15.1", "15.2", false},
		{"FreeBSD-runtime", "upgrade", "15.1", "16.0", false},
		{"FreeBSD-kernel-generic", "upgrade", "15.1", "15.1p1", false},
		{"FreeBSD-bootloader", "upgrade", "15.1", "15.1p1", false},
		{"pkg", "upgrade", "2.6.2", "2.8.4", false},
		{"pkg-devel", "upgrade", "2.6.2", "2.8.4", false},
		{"app", "upgrade", "1", "2", false},
	} {
		t.Run(test.name+"/"+test.action+"/"+test.to, func(t *testing.T) {
			client := Client{Scope: ScopeSystem, System: info}
			plan := Plan{Changes: []Change{{Name: test.name, Action: test.action, From: test.from, To: test.to}}}
			if err := client.validateScopedChanges(plan); (err == nil) != test.allowed {
				t.Fatalf("allowed=%v: %v", test.allowed, err)
			}
		})
	}
}

func TestSystemCandidateArchivesVerifyMetadataAndBootPaths(t *testing.T) {
	const archive = "/var/cache/pkg/FreeBSD-runtime-15.1p2~f8b1f712f0.pkg"
	for _, test := range []struct {
		name, files string
		metadata    func(string) string
		host        uint32
		want        string
	}{
		{name: "valid", files: "/usr/share/system-fixture/value\n"},
		{name: "kernel payload", files: "/boot/kernel/kernel\n", want: "kernel_package_change_unsupported"},
		{name: "normalized boot payload", files: "/usr/../boot/loader\n", want: "kernel_package_change_unsupported"},
		{name: "wrong ABI", metadata: func(s string) string { return strings.ReplaceAll(s, "amd64", "aarch64") }, want: "abi_changed"},
		{name: "wrong release", metadata: func(s string) string {
			return strings.ReplaceAll(s, "1501000", "1502000")
		}, want: "release_changed"},
		{name: "old host", host: 1500999, want: "host_too_old"},
		{name: "missing annotation", metadata: func(s string) string { return strings.ReplaceAll(s, "FreeBSD_version", "unrelated") }, want: "metadata_invalid"},
		{name: "not base", metadata: func(s string) string { return strings.ReplaceAll(s, "base/FreeBSD-runtime", "misc/runtime") }, want: "metadata_invalid"},
		{name: "wrong version", metadata: func(s string) string { return strings.ReplaceAll(s, "15.1p2", "15.1p3") }, want: "invalid_package_archive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := &SystemInfo{Release: "15.1", ABI: "FreeBSD:15:amd64", OSVersion: 1501000, HostOSVersion: 1600027, Repository: "FreeBSD-base"}
			if test.host != 0 {
				info.HostOSVersion = test.host
			}
			metadata := systemTestMetadata("FreeBSD-runtime", "15.1p2")
			if test.metadata != nil {
				metadata = test.metadata(metadata)
			}
			root := t.TempDir()
			client := Client{Scope: ScopeSystem, System: info, Target: Target{JID: 1, Name: "fixture", Root: root},
				RunCommand: func(_ context.Context, args []string) (Result, error) {
					switch {
					case slices.Contains(args, "search"):
						return Result{Stdout: archiveTestMetadata("FreeBSD-runtime", "15.1p2", "./Hashed/FreeBSD-runtime-15.1p2~d2b6c4f365.pkg")}, nil
					case slices.Contains(args, "fetch"):
						writeTestArchive(t, root, "FreeBSD-runtime", "15.1p2")
						return Result{}, nil
					case slices.Contains(args, "query"):
						command := args[slices.Index(args, "query"):]
						if len(command) != 4 || command[1] != "-F" || command[2] != archive {
							t.Fatalf("unexpected normalized pkgbase archive path: %v", command)
						}
						switch command[3] {
						case systemPackageFormat:
							return Result{Stdout: metadata}, nil
						case "%Fp":
							return Result{Stdout: test.files}, nil
						}
						fallthrough
					default:
						t.Fatalf("unexpected command: %v", args)
						return Result{}, nil
					}
				}}
			plan := Plan{Changes: []Change{{Action: "upgrade", Name: "FreeBSD-runtime", From: "15.1p1", To: "15.1p2", Repository: "FreeBSD-base"}}}
			err := client.VerifyCandidates(t.Context(), plan, []Repository{{Name: "FreeBSD-base"}})
			if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("expected %q: %v", test.want, err)
			}
		})
	}
}

func TestSystemCatalogCannotHideMissingOrWrongReleasePackages(t *testing.T) {
	const catalog = "FreeBSD-runtime\t15.1p2\tbase/FreeBSD-runtime\tFreeBSD:15:amd64\nFreeBSD-clibs\t15.1\tbase/FreeBSD-clibs\tFreeBSD:15:amd64\n"
	for _, test := range []struct {
		name, output, want string
	}{
		{"complete", catalog, ""},
		{"missing package", strings.Split(catalog, "\n")[0], "catalog_incomplete"},
		{"empty", "", "catalog_incomplete"},
		{"duplicate", catalog + catalog, "catalog_incomplete"},
		{"foreign package", catalog + "app\t1\tmisc/app\tFreeBSD:15:amd64\n", "catalog_incomplete"},
		{"wrong origin", strings.ReplaceAll(catalog, "base/FreeBSD-runtime", "misc/runtime"), "catalog_incomplete"},
		{"minor release change", strings.ReplaceAll(catalog, "15.1p2", "15.2"), "release_changed"},
		{"major release change", strings.ReplaceAll(catalog, "15.1p2", "16.0"), "release_changed"},
		{"ABI change", strings.ReplaceAll(catalog, "amd64", "aarch64"), "abi_changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := Client{Target: Target{JID: 1, Name: "fixture", Root: "/fixture"}, Scope: ScopeSystem, System: &SystemInfo{Release: "15.1", ABI: "FreeBSD:15:amd64", Repository: "FreeBSD-base"}, RunCommand: func(_ context.Context, args []string) (Result, error) {
				if !slices.Contains(args, "rquery") || !slices.Contains(args, "-U") || !slices.Contains(args, "FreeBSD-base") {
					t.Fatalf("unscoped or updating catalog query: %v", args)
				}
				return Result{Stdout: test.output}, nil
			}}
			err := client.validateSystemCatalog(t.Context(), []Repository{{Name: "FreeBSD-base"}}, []string{"FreeBSD-runtime", "FreeBSD-clibs"})
			if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("expected %q: %v", test.want, err)
			}
		})
	}
}
