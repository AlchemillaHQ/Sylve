// SPDX-License-Identifier: BSD-2-Clause

package pkgmanager

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPackageCommandsRequireJailAndCannotInjectFlags(t *testing.T) {
	c := Client{Target: Target{JID: 23, Name: "jail", Root: "/private/jail"}}
	args, err := c.args("query", "-a", InventoryFormat)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.HasPrefix(joined, "-j 23 ") || !strings.Contains(joined, "IGNORE_OSVERSION=false") || !strings.Contains(joined, "REPO_AUTOUPDATE=false") || !strings.Contains(joined, "PKG_ENABLE_PLUGINS=false") {
		t.Fatal(args)
	}
	for _, target := range []Target{{}, {JID: 23, Name: "jail", Root: "/"}, {Name: "jail", Root: "/private"}} {
		c.Target = target
		if _, err = c.args("query"); err == nil {
			t.Fatal("accepted unbound target", target)
		}
	}
	for _, name := range []string{"-y", ";shutdown", "test\napp", "foo/bar", "foo*", "a b"} {
		if ValidName(name) {
			t.Fatalf("accepted %q", name)
		}
	}
	for _, name := range []string{"foo-bar", "python311-foo", "openssl-quictls", "test+flavor"} {
		if !ValidName(name) {
			t.Fatalf("rejected %q", name)
		}
	}
}

func TestPackageCommandOutputIsBounded(t *testing.T) {
	buffer := boundedBuffer{limit: 4}
	for _, chunk := range []string{"ab", "cdef", "more output"} {
		if n, err := buffer.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("write=%d: %v", n, err)
		}
	}
	if buffer.String() != "abcd" || !buffer.truncated {
		t.Fatalf("output=%q truncated=%v", buffer.String(), buffer.truncated)
	}
}

func TestApplyStopsWithDaemonContext(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "pkg-fixture")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	client := Client{Binary: binary, Target: Target{JID: 4, Name: "fixture", Root: "/fixture"}}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := client.Apply(ctx, []string{"upgrade"}, Plan{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("installer ignored cancellation: %v", err)
	}
}

func TestOfflineCommandsAreBoundAndInspectionOnly(t *testing.T) {
	target := Target{Name: "fixture", Root: "/private/jail", Dataset: "tank/jail", GUID: "123", Offline: true}
	client := Client{Target: target, RunCommand: func(context.Context, []string) (Result, error) {
		t.Fatal("attempted an offline mutation")
		return Result{}, nil
	}}
	for _, command := range [][]string{{"-N"}, {"-vv"}, {"query", "-a", InventoryFormat}, {"search", "-U", "-r", "ports", "-e", "-S", "pkg-name", "-R", "--raw-format", "json", "app-2"}, {"audit", "--raw=json", "-F"}, {"update", "-r", "ports"}, {"fetch", "-U", "-y", "app-2"}, UpgradeArgs([]Repository{{Name: "ports"}}, []string{"app"}, true)} {
		args, err := client.args(command...)
		if err != nil || len(args) < 2 || args[0] != "-c" || args[1] != target.Root || slices.Contains(args, "-j") {
			t.Fatalf("offline args %v: %v", args, err)
		}
		if command[0] != "-vv" && (!slices.Contains(args, "RUN_SCRIPTS=false") || !slices.Contains(args, "PKG_TRIGGERS_ENABLE=false") || !slices.Contains(args, "VALID_URL_SCHEME=pkg+http,pkg+https,https,http,file")) {
			t.Fatalf("offline execution safeguards missing: %v", args)
		}
	}
	for _, command := range [][]string{nil, {"install", "app"}, {"delete", "app"}, {"shell"}, {"triggers"}, {"bootstrap"}, {"upg", "-n"}, {"upgrade", "-y"}, {"-N", "install", "app"}, {"-vv", "upgrade"}, UpgradeArgs(nil, []string{"app"}, false)} {
		if _, err := client.Run(t.Context(), command...); err == nil {
			t.Fatalf("accepted offline mutation: %v", command)
		}
	}
	for _, scope := range []Scope{"", ScopeApplications, ScopeSystem} {
		client.Scope = scope
		if err := client.Apply(t.Context(), UpgradeArgs(nil, []string{"app"}, false), Plan{}); err == nil || err.Error() != "jail_not_running" {
			t.Fatalf("accepted offline installer for scope %q: %v", scope, err)
		}
	}
	for _, invalid := range []Target{
		{Name: target.Name, Root: target.Root, Offline: true},
		{Name: target.Name, Root: "/", Dataset: target.Dataset, GUID: target.GUID, Offline: true},
		{JID: 1, Name: target.Name, Root: target.Root, Dataset: target.Dataset, GUID: target.GUID, Offline: true},
	} {
		client.Target = invalid
		if _, err := client.args("query", "-a", InventoryFormat); err == nil {
			t.Fatalf("accepted unbound offline target: %+v", invalid)
		}
	}
}

func TestOfflineRepositoriesRejectExternalCommandTransports(t *testing.T) {
	for _, scheme := range []string{"https", "pkg+https", "file", "ssh", "tcp"} {
		t.Run(scheme, func(t *testing.T) {
			client := Client{Target: Target{Name: "fixture", Root: "/private/jail", Dataset: "tank/jail", GUID: "123", Offline: true}, RunCommand: func(context.Context, []string) (Result, error) {
				return Result{Stdout: "Repositories:\nports: {\nurl: \"" + scheme + "://example.invalid/ports\",\nenabled: yes,\n}\n"}, nil
			}}
			_, _, err := client.Repositories(t.Context())
			reject := scheme == "ssh" || scheme == "tcp"
			if (err != nil) != reject {
				t.Fatalf("%s repository: %v", scheme, err)
			}
		})
	}
}

func TestProtectedTransactionIncludesImplicitDependencies(t *testing.T) {
	c := Client{Target: Target{JID: 1, Name: "jail", Root: "/jail"}, RunCommand: func(context.Context, []string) (Result, error) {
		t.Fatal("attempted fetch of protected transaction")
		return Result{}, nil
	}}
	for _, name := range []string{"FreeBSD-runtime", "pkg", "drm-66-kmod"} {
		if err := c.VerifyCandidates(t.Context(), Plan{Changes: []Change{{Action: "install", Name: name, To: "1"}}}, []Repository{{Name: "ports"}}); err == nil {
			t.Fatal(name)
		}
	}
	for _, origin := range []string{"base/runtime", "base/kernel"} {
		if !Protected("custom-package", origin) {
			t.Fatal(origin)
		}
	}
}

func TestPackageFileChecksCoverInstalledAndCandidateArchives(t *testing.T) {
	for _, source := range []string{"installed", "candidate"} {
		for _, test := range []struct {
			name   string
			files  string
			reject bool
		}{
			{name: "application", files: "/usr/local/bin/app\n"},
			{name: "boot-like name", files: "/usr/local/share/boot/config\n"},
			{name: "boot directory", files: "/boot\n", reject: true},
			{name: "kernel", files: "/boot/kernel/kernel\n", reject: true},
			{name: "relative boot", files: "boot/loader\n", reject: true},
			{name: "normalized boot", files: "/usr/local/../../boot/loader\n", reject: true},
		} {
			t.Run(source+"/"+test.name, func(t *testing.T) {
				root := t.TempDir()
				client := Client{Target: Target{JID: 1, Name: "fixture", Root: root}, RunCommand: func(_ context.Context, args []string) (Result, error) {
					if slices.Contains(args, "search") {
						return Result{Stdout: archiveTestMetadata("app", "2", "All/app-2.pkg")}, nil
					}
					switch args[len(args)-1] {
					case "app-2":
						writeTestArchive(t, root, "app", "2")
						return Result{}, nil
					case "%n\t%v\t%o":
						return Result{Stdout: "app\t2\tmisc/app\n"}, nil
					case "%Fp", "app":
						expected := []string{"query", "%Fp", "app"}
						if source == "candidate" {
							expected = []string{"query", "-F", "/var/cache/pkg/app-2~f8b1f712f0.pkg", "%Fp"}
						}
						if !slices.Equal(args[slices.Index(args, "query"):], expected) {
							t.Fatalf("unexpected file-list query: %v", args)
						}
						return Result{Stdout: test.files}, nil
					default:
						t.Fatalf("unexpected command: %v", args)
						return Result{}, nil
					}
				}}
				plan := Plan{Changes: []Change{{Action: "upgrade", Name: "app", From: "1", To: "2", Repository: "ports"}}}
				var err error
				if source == "installed" {
					err = client.VerifyInstalled(t.Context(), plan, []Package{{Name: "app", Version: "1"}})
				} else {
					err = client.VerifyCandidates(t.Context(), plan, []Repository{{Name: "ports"}})
				}
				if test.reject {
					if err == nil || !strings.Contains(err.Error(), "kernel_package_change_unsupported") {
						t.Fatalf("expected boot-path rejection, got %v", err)
					}
					if len(client.ManagedPaths) != 0 {
						t.Fatalf("retained rejected paths: %v", client.ManagedPaths)
					}
				} else if err != nil || !slices.Equal(client.ManagedPaths, strings.Split(strings.TrimSuffix(test.files, "\n"), "\n")) {
					t.Fatalf("managed paths %v: %v", client.ManagedPaths, err)
				}
			})
		}
	}
}

func TestTransactionSubsetAndChangedRepositoryRejected(t *testing.T) {
	approved, err := ParsePlan(examplePlan)
	if err != nil {
		t.Fatal(err)
	}
	actual := approved
	actual.Changes = append([]Change(nil), approved.Changes[:1]...)
	if err = ValidateTransaction(actual, approved); err == nil {
		t.Fatal("approved partial transaction")
	}
	actual.Changes = append([]Change(nil), approved.Changes...)
	actual.Changes[0].Repository = "other"
	if err = ValidateTransaction(actual, approved); err == nil {
		t.Fatal("approved changed repository")
	}
}

func TestLogWriterRedactsSplitSecretsAndBoundsOutput(t *testing.T) {
	var output bytes.Buffer
	writer := NewLogWriter(&output)
	_, _ = writer.Write([]byte("https://username:sec"))
	_, _ = writer.Write([]byte("ret@example.org/repo\n\x1b[31mmessage\n"))
	writer.Flush()
	if strings.Contains(output.String(), "secret") || strings.ContainsRune(output.String(), '\x1b') {
		t.Fatal(output.String())
	}
	for range 1024 {
		_, _ = writer.Write([]byte(strings.Repeat("x", 16383) + "\n"))
	}
	writer.Flush()
	if output.Len() > MaxLogBytes+100 {
		t.Fatal("unbounded log", output.Len())
	}
}

func TestLogFileLimitSurvivesWorkerHandoff(t *testing.T) {
	file, err := os.OpenFile(filepath.Join(t.TempDir(), "output.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err = file.Truncate(MaxLogBytes - 8); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		writer := NewLogWriter(file)
		_, _ = writer.Write([]byte("another worker log line\n"))
		writer.Flush()
	}
	info, err := file.Stat()
	if err != nil || info.Size() > MaxLogBytes {
		t.Fatalf("log size %v err %v", info, err)
	}
}

func TestReinstallAndDestructivePlanGrammar(t *testing.T) {
	output := `The following 3 package(s) will be affected (of 4 checked):
Installed packages to be REMOVED:
	old-library: 1.0
Installed packages to be DOWNGRADED:
	app: 3.0 -> 2.0 [ports]
Installed packages to be REINSTALLED:
	my-long-app-1.0 [ports] (options changed)
Number of packages to be removed: 1
Number of packages to be downgraded: 1
Number of packages to be reinstalled: 1
`
	plan, err := ParsePlan(output)
	if err != nil || len(plan.Changes) != 3 {
		t.Fatalf("%+v %v", plan, err)
	}
}

func TestPackageCommandLogsOmitQueryDataButKeepDiagnostics(t *testing.T) {
	for _, test := range []struct {
		command []string
		stdout  bool
	}{
		{[]string{"query", "%Fp", "app"}, false},
		{[]string{"query", "-a", InventoryFormat}, false},
		{[]string{"query", "-F", "/cache/app.pkg", "%n\t%v\t%o"}, false},
		{[]string{"rquery", "-U", "%n"}, false},
		{[]string{"search", "-U", "app"}, false},
		{[]string{"-vv"}, false},
		{[]string{"update"}, true},
		{[]string{"audit", "--raw=json"}, true},
		{[]string{"upgrade", "-U", "-n", "app"}, true},
		{[]string{"fetch", "-U", "app"}, true},
	} {
		t.Run(strings.Join(test.command, " "), func(t *testing.T) {
			var log bytes.Buffer
			client := Client{Target: Target{JID: 1, Name: "fixture", Root: t.TempDir()}, Log: &log,
				RunCommand: func(context.Context, []string) (Result, error) {
					return Result{Stdout: "successful-output\n", Stderr: "diagnostic https://user:secret@example.invalid/repo\n"}, nil
				}}
			if _, err := client.Run(t.Context(), test.command...); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(log.String(), "successful-output") != test.stdout || !strings.Contains(log.String(), "diagnostic") || strings.Contains(log.String(), "secret") {
				t.Fatalf("unexpected command log: %q", log.String())
			}
		})
	}
	var failure bytes.Buffer
	client := Client{Target: Target{JID: 1, Name: "fixture", Root: t.TempDir()}, Log: &failure,
		RunCommand: func(context.Context, []string) (Result, error) {
			return Result{Stdout: "query failure details\n", ExitCode: 1}, nil
		}}
	if _, err := client.Run(t.Context(), "query", "%Fp", "app"); err != nil || !strings.Contains(failure.String(), "query failure details") {
		t.Fatalf("query failure diagnostics were lost: %q: %v", failure.String(), err)
	}
}

func TestPackageCatalogPresenceRequiresRegularPrivateFiles(t *testing.T) {
	for _, kind := range []string{"missing", "empty", "regular", "symlink", "directory", "parent symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			catalog := filepath.Join(root, "var/db/pkg/repos/ports/db")
			if err := os.MkdirAll(filepath.Dir(catalog), 0755); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "regular", "empty":
				data := []byte("fixture")
				if kind == "empty" {
					data = nil
				}
				if err := os.WriteFile(catalog, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(t.TempDir(), "external"), catalog); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(catalog, 0700); err != nil {
					t.Fatal(err)
				}
			case "parent symlink":
				if err := os.Remove(filepath.Dir(catalog)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Dir(catalog)); err != nil {
					t.Fatal(err)
				}
			}
			client := Client{Target: Target{JID: 1, Name: "fixture", Root: root}}
			present, err := client.CatalogsPresent([]Repository{{Name: "ports"}})
			invalid := kind == "symlink" || kind == "directory" || kind == "parent symlink"
			if present != (kind == "regular") || (err != nil) != invalid {
				t.Fatalf("present=%v: %v", present, err)
			}
		})
	}
}

func TestPackageManagedPathsAreDeduplicatedAcrossVerificationPhases(t *testing.T) {
	client := Client{Target: Target{JID: 1, Name: "fixture", Root: t.TempDir()}}
	for _, files := range [][]string{{"/usr/local/bin/app", "", "/usr/local/lib/library"}, {"/usr/local/bin/app", "/usr/local/bin/app", ""}} {
		if err := client.verifyPackagePaths(t.Context(), files); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(client.ManagedPaths, []string{"/usr/local/bin/app", "/usr/local/lib/library"}) {
		t.Fatalf("duplicate or empty managed paths: %v", client.ManagedPaths)
	}
}

func TestPackageAuditDigestTracksVersionsNotTransactionMetadata(t *testing.T) {
	original := []Package{{Name: "app", Version: "1", Origin: "misc/app", Repository: "ports", Size: 1}, {Name: "other", Version: "2"}}
	changed := append([]Package(nil), original...)
	changed[0].Origin, changed[0].Repository, changed[0].Size = "other/origin", "other", 200
	changed[0].Automatic, changed[0].Locked, changed[0].Protected = true, true, true
	changed[0].Kind, changed[0].Available, changed[0].Advisories = KindProtected, "2", []Advisory{{ID: "one"}}
	changed[0], changed[1] = changed[1], changed[0]
	if AuditInventoryDigest(original) != AuditInventoryDigest(changed) || InventoryDigest(original) == InventoryDigest(changed) {
		t.Fatal("audit metadata identity was not separated from transaction identity")
	}
	changed[1].Version = "2"
	if AuditInventoryDigest(original) == AuditInventoryDigest(changed) {
		t.Fatal("version change retained audit identity")
	}
	changed[1].Version, changed[1].Name = "1", "replacement"
	if AuditInventoryDigest(original) == AuditInventoryDigest(changed) || AuditInventoryDigest(original) == AuditInventoryDigest(original[:1]) {
		t.Fatal("package replacement/removal retained audit identity")
	}
	if original[0].Name != "app" || original[0].Version != "1" || original[0].Locked {
		t.Fatal("digest modified the inventory")
	}
}

func TestPackageAuditOnlyRejectsAuditedPackageChanges(t *testing.T) {
	for _, test := range []struct {
		name      string
		inventory string
		reject    bool
	}{
		{"metadata", "app\t1\tother/origin\tother\t200\t1\t1\n", false},
		{"version", "app\t2\tmisc/app\tports\t1\t0\t0\n", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			feed := filepath.Join(root, "var/db/pkg/vuln.xml")
			if err := os.MkdirAll(filepath.Dir(feed), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(feed, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			client := Client{Target: Target{JID: 1, Name: "fixture", Root: root}, RunCommand: func(_ context.Context, args []string) (Result, error) {
				switch {
				case slices.Contains(args, "audit"):
					return Result{Stdout: `{"pkg_count":0,"packages":null}`}, nil
				case args[len(args)-1] == "-N":
					return Result{}, nil
				case args[len(args)-1] == InventoryFormat:
					return Result{Stdout: test.inventory}, nil
				default:
					t.Fatalf("unexpected audit command: %v", args)
					return Result{}, nil
				}
			}}
			_, err := client.Audit(t.Context(), []Package{{Name: "app", Version: "1"}}, true)
			if (err != nil) != test.reject || err != nil && !strings.Contains(err.Error(), "inventory_changed_during_audit") {
				t.Fatalf("audit package identity: %v", err)
			}
		})
	}
}
