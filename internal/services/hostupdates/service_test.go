// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package hostupdates

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
	"github.com/alchemillahq/sylve/pkg/pkgmanager"
	"github.com/alchemillahq/sylve/pkg/sylveupdate"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testService(t *testing.T) *Service {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	connection.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = connection.Close() })
	if err = database.AutoMigrate(&models.HostUpdateState{}, &models.HostUpdatePlan{}, &models.HostUpdateOperation{}, &models.HostUpdateEvent{}); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "sylve")
	if err = os.WriteFile(executable, []byte("fixture executable"), 0755); err != nil {
		t.Fatal(err)
	}
	digest, err := sylveupdate.FileDigest(executable)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{DB: database, context: t.Context(), directory: t.TempDir(), executable: executable, runningDigest: digest, version: "0.3.1", commit: strings.Repeat("a", 40), builtAt: "2026-10-18T12:00:00Z", releases: sylveupdate.NewClient()}
	s.installed = s.inspectInstalled
	s.ownership = func(context.Context, *pkgmanager.Client, string) (bool, string, error) {
		return true, "sysutils/sylve", nil
	}
	s.probe = func() error { return errors.New("fixture_probe_disabled") }
	s.client = func(log io.Writer) *pkgmanager.Client {
		client := pkgmanager.NewHostClient(log)
		client.RunCommand = func(context.Context, []string) (pkgmanager.Result, error) {
			return pkgmanager.Result{}, errors.New("unexpected_external_command")
		}
		return client
	}
	s.command = func(context.Context, string, ...string) (string, error) {
		return "", errors.New("unexpected_external_command")
	}
	if _, err = s.state(); err != nil {
		t.Fatal(err)
	}
	return s
}

func eventCount(t *testing.T, s *Service, kind string) int64 {
	t.Helper()
	var count int64
	if err := s.DB.Model(&models.HostUpdateEvent{}).Where("kind = ?", "system.updates."+kind+".host").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func TestAuditFindingsRetainedOnFailureAndNotificationsDeduplicated(t *testing.T) {
	s := testService(t)
	inventory := []pkgmanager.Package{{Name: "example", Version: "1", Origin: "test/example"}}
	report := pkgmanager.Audit{Packages: []pkgmanager.AuditPackage{{Name: "example", Version: "1", Advisories: []pkgmanager.Advisory{{ID: "advisory-1", Description: "fixture", URL: "https://vuxml.freebsd.org/fixture.html"}}}}}
	for range 2 {
		if err := s.storeAudit(inventory, report, 0); err != nil {
			t.Fatal(err)
		}
	}
	if eventCount(t, s, "vulnerabilities") != 1 {
		t.Fatal("repeated audit emitted duplicate findings")
	}
	for range 4 {
		if err := s.failedCheck("audit", errors.New("network_unavailable: https://user:secret@example.org/feed")); err != nil {
			t.Fatal(err)
		}
	}
	state, err := s.state()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Audit.Packages) != 1 || state.AuditAt == nil || state.FailureCount != 4 || !state.HealthFailed || strings.Contains(state.AuditError, "secret") {
		t.Fatalf("lost last-good findings or secret redaction: %+v", state)
	}
	if eventCount(t, s, "health") != 1 {
		t.Fatal("persistent failures should emit one health event")
	}
	for range 2 {
		if err := s.successfulCheck(); err != nil {
			t.Fatal(err)
		}
	}
	if eventCount(t, s, "health") != 2 {
		t.Fatal("recovery should emit exactly one event")
	}
	if err = s.storeAudit(inventory, pkgmanager.Audit{Packages: []pkgmanager.AuditPackage{}}, 0); err != nil {
		t.Fatal(err)
	}
	if eventCount(t, s, "vulnerabilities") != 2 {
		t.Fatal("resolved findings not reported")
	}
}

func TestSystemChecksKeepAuditAndSelfUpdateEligibilityIndependent(t *testing.T) {
	for _, auditFailure := range []bool{true, false} {
		t.Run(fmt.Sprintf("audit failure=%v", auditFailure), func(t *testing.T) {
			s := testService(t)
			s.probe = func() error { return nil }
			if !auditFailure {
				s.ownership = func(context.Context, *pkgmanager.Client, string) (bool, string, error) {
					return false, "", nil
				}
			}
			arch := runtime.GOARCH
			if arch == "arm64" {
				arch = "aarch64"
			}
			audited, refreshed, solved := false, false, false
			s.client = func(log io.Writer) *pkgmanager.Client {
				client := pkgmanager.NewHostClient(log)
				client.HostOSVersion = func() (uint32, error) { return 1600001, nil }
				client.RunCommand = func(_ context.Context, args []string) (pkgmanager.Result, error) {
					switch {
					case slices.Contains(args, "-N"):
						return pkgmanager.Result{}, nil
					case slices.Contains(args, "which"):
						return pkgmanager.Result{Stdout: "base/FreeBSD-runtime\n"}, nil
					case slices.Contains(args, "query") && slices.Contains(args, "-a"):
						return pkgmanager.Result{Stdout: "FreeBSD-runtime\t16.snap20261019120000\tbase/FreeBSD-runtime\tbase\t10\t0\t0\nFreeBSD-clibs\t16.snap20261019120000\tbase/FreeBSD-clibs\tbase\t10\t0\t0\n"}, nil
					case slices.Contains(args, "query"):
						return pkgmanager.Result{Stdout: "FreeBSD-runtime\t16.snap20261019120000\tbase/FreeBSD-runtime\tFreeBSD:16:" + arch + "\t/\tFreeBSD_version\t1600001\n"}, nil
					case slices.Contains(args, "-vv"):
						return pkgmanager.Result{Stdout: "Repositories:\n  base: {\n    url: \"pkg+https://pkg.freebsd.org/base_latest\",\n    enabled: yes\n  }\n"}, nil
					case slices.Contains(args, "audit"):
						audited = true
						if auditFailure {
							return pkgmanager.Result{ExitCode: 1, Stderr: "fixture feed unavailable"}, nil
						}
						return pkgmanager.Result{Stdout: `{"pkg_count":0,"packages":null}`}, nil
					case slices.Contains(args, "update"):
						refreshed = true
						return pkgmanager.Result{}, nil
					case slices.Contains(args, "upgrade"):
						solved = true
						return pkgmanager.Result{Stdout: "Your packages are up to date.\n"}, nil
					default:
						return pkgmanager.Result{}, fmt.Errorf("unexpected command: %v", args)
					}
				}
				return client
			}
			op := models.HostUpdateOperation{UUID: uuid.NewString(), Action: "check"}
			if err := s.DB.Create(&op).Error; err != nil {
				t.Fatal(err)
			}
			err := s.readOperation(t.Context(), &op, nil, io.Discard)
			if (auditFailure && (err == nil || code(err) != "package_audit_failed")) || (!auditFailure && err != nil) || !audited || !refreshed || !solved {
				t.Fatalf("system checks were blocked: audit=%v refresh=%v solve=%v err=%v", audited, refreshed, solved, err)
			}
			state, err := s.state()
			if err != nil || state.CheckedAt == nil || state.CheckInventoryHash == "" || state.CheckError != "" || auditFailure != strings.Contains(state.AuditError, "fixture feed unavailable") {
				t.Fatalf("partial check results lost: %+v %v", state, err)
			}
		})
	}
}

func TestPreviewReusesRecentCatalogsAndInvalidatesOnChanges(t *testing.T) {
	for _, change := range []string{"unchanged", "explicit check", "expired", "future", "inventory", "lock", "repositories", "missing catalog", "empty catalog", "symlink catalog", "refresh failure"} {
		t.Run(change, func(t *testing.T) {
			s := testService(t)
			root := t.TempDir()
			catalog := filepath.Join(root, "var/db/pkg/repos/ports/db")
			if err := os.MkdirAll(filepath.Dir(catalog), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(catalog, []byte("fixture catalog"), 0600); err != nil {
				t.Fatal(err)
			}
			var log bytes.Buffer
			refreshes := 0
			client := &pkgmanager.Client{Target: pkgmanager.Target{JID: 1, Name: "fixture", Root: root}, Log: &log, RunCommand: func(_ context.Context, args []string) (pkgmanager.Result, error) {
				if !slices.Contains(args, "update") || !slices.Contains(args, "ports") {
					t.Fatalf("unexpected command: %v", args)
				}
				refreshes++
				if change == "refresh failure" && refreshes > 1 {
					return pkgmanager.Result{}, errors.New("fixture network failure")
				}
				return pkgmanager.Result{}, nil
			}}
			repos := []pkgmanager.Repository{{Name: "ports", URL: "https://example.invalid/ports"}}
			inventory := []pkgmanager.Package{{Name: "app", Version: "1", Origin: "misc/app"}}
			op := models.HostUpdateOperation{UUID: uuid.NewString(), Action: "packages-preview"}
			if err := s.DB.Create(&op).Error; err != nil {
				t.Fatal(err)
			}
			if err := s.refreshRepositories(t.Context(), &op, client, inventory, repos, "repos-1"); err != nil {
				t.Fatal(err)
			}
			state, err := s.state()
			if err != nil || state.RepositoriesRefreshedAt == nil || state.RepositoryHash != "repos-1" || state.RepositoryInventoryHash != pkgmanager.InventoryDigest(inventory) || refreshes != 1 {
				t.Fatalf("successful refresh was not cached: %+v %v", state, err)
			}
			firstRefresh := *state.RepositoriesRefreshedAt
			repoHash := "repos-1"
			switch change {
			case "explicit check":
				op.Action = "check"
			case "expired", "future":
				when := time.Now().Add(-6 * time.Minute)
				if change == "future" {
					when = time.Now().Add(time.Minute)
				}
				if err := s.DB.Model(&state).Update("repositories_refreshed_at", when).Error; err != nil {
					t.Fatal(err)
				}
			case "inventory":
				inventory[0].Version = "2"
			case "lock":
				inventory[0].Locked = true
			case "repositories":
				repoHash = "repos-2"
			case "missing catalog":
				if err := os.Remove(catalog); err != nil {
					t.Fatal(err)
				}
			case "empty catalog":
				if err := os.WriteFile(catalog, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink catalog":
				if err := os.Rename(catalog, catalog+"-outside"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(catalog+"-outside", catalog); err != nil {
					t.Fatal(err)
				}
			case "refresh failure":
				op.Action = "check"
			}
			err = s.refreshRepositories(t.Context(), &op, client, inventory, repos, repoHash)
			wantError := change == "symlink catalog" || change == "refresh failure"
			if (err != nil) != wantError {
				t.Fatalf("unexpected refresh result: %v", err)
			}
			wantRefreshes := 2
			if change == "unchanged" || change == "symlink catalog" {
				wantRefreshes = 1
			}
			if refreshes != wantRefreshes {
				t.Fatalf("expected %d refreshes, got %d", wantRefreshes, refreshes)
			}
			state, err = s.state()
			if err != nil {
				t.Fatal(err)
			}
			if change == "unchanged" && (!state.RepositoriesRefreshedAt.Equal(firstRefresh) || !strings.Contains(log.String(), "Reusing recent repository catalogs")) {
				t.Fatal("reuse was not reported or extended the freshness window")
			}
			if change == "refresh failure" && (state.RepositoriesRefreshedAt != nil || state.RepositoryHash != "" || state.RepositoryInventoryHash != "") {
				t.Fatal("failed refresh retained reusable metadata")
			}
		})
	}
}

func TestHostPreviewsDoNotPrepareArchivesBeforePinningCatalogs(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(fmt.Sprintf("selected=%v", selected), func(t *testing.T) {
			s := testService(t)
			s.probe = func() error { return nil }
			repository := "absent-" + uuid.NewString()
			arch := runtime.GOARCH
			if arch == "arm64" {
				arch = "aarch64"
			}
			s.client = func(log io.Writer) *pkgmanager.Client {
				client := pkgmanager.NewHostClient(log)
				client.HostOSVersion = func() (uint32, error) { return 1600001, nil }
				client.RunCommand = func(_ context.Context, args []string) (pkgmanager.Result, error) {
					switch {
					case slices.Contains(args, "-N"):
						return pkgmanager.Result{}, nil
					case slices.Contains(args, "which"):
						return pkgmanager.Result{Stdout: "base/FreeBSD-runtime\n"}, nil
					case slices.Contains(args, "query") && slices.Contains(args, "-a"):
						return pkgmanager.Result{Stdout: "FreeBSD-runtime\t16.snap20261019000000\tbase/FreeBSD-runtime\t" + repository + "\t10\t0\t0\nFreeBSD-clibs\t16.snap20261019000000\tbase/FreeBSD-clibs\t" + repository + "\t10\t0\t0\napp\t1\tmisc/app\t" + repository + "\t10\t0\t0\npkg\t2.8.4\tports-mgmt/pkg\t" + repository + "\t10\t0\t0\n"}, nil
					case slices.Contains(args, "query") && args[len(args)-1] == "FreeBSD-runtime":
						return pkgmanager.Result{Stdout: "FreeBSD-runtime\t16.snap20261019000000\tbase/FreeBSD-runtime\tFreeBSD:16:" + arch + "\t/\tFreeBSD_version\t1600001\n"}, nil
					case slices.Contains(args, "-vv"):
						return pkgmanager.Result{Stdout: "Repositories:\n" + repository + ": {\nurl: \"https://example.invalid/base_latest\",\nenabled: yes\n}\n"}, nil
					case slices.Contains(args, "update"):
						return pkgmanager.Result{}, nil
					case slices.Contains(args, "upgrade"):
						if !slices.Contains(args, "-n") {
							t.Fatal("preview attempted installation")
						}
						if args[len(args)-1] == "pkg" {
							return pkgmanager.Result{Stdout: "Your packages are up to date.\n"}, nil
						}
						return pkgmanager.Result{Stdout: "The following 1 package(s) will be affected (of 1 checked):\nInstalled packages to be UPGRADED:\n\tapp: 1 -> 2 [" + repository + "]\nNumber of packages to be upgraded: 1\n"}, nil
					default:
						t.Errorf("preview performed archive or file preflight: %v", args)
						return pkgmanager.Result{}, fmt.Errorf("unexpected_archive_preflight")
					}
				}
				return client
			}
			op := models.HostUpdateOperation{UUID: uuid.NewString(), Action: "packages-preview"}
			if err := s.DB.Create(&op).Error; err != nil {
				t.Fatal(err)
			}
			var selection []string
			if selected {
				selection = []string{"app"}
			}
			err := s.readOperation(t.Context(), &op, selection, io.Discard)
			if !errors.Is(err, os.ErrNotExist) || op.Phase != "solving" || op.PlanID != "" {
				t.Fatalf("preview did not reach its missing-catalog approval check without archive work: %+v %v", op, err)
			}
		})
	}
}

func TestUpdateAvailabilityAndInventorySerialization(t *testing.T) {
	s := testService(t)
	inventory := []pkgmanager.Package{{Name: "example", Version: "1", Origin: "test/example"}}
	plan := pkgmanager.Plan{Changes: []pkgmanager.Change{{Name: "example", Action: "upgrade", From: "1", To: "2"}}}
	plan.Seal()
	for range 2 {
		if err := s.storeAvailable(inventory, plan, 0); err != nil {
			t.Fatal(err)
		}
	}
	state, err := s.state()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Inventory) != 1 || state.Inventory[0].Name != "example" || state.CheckInventoryHash != pkgmanager.InventoryDigest(inventory) || state.Available.Digest != plan.Digest {
		t.Fatalf("serialization lost fields: %+v", state)
	}
	if eventCount(t, s, "available") != 1 {
		t.Fatal("duplicate availability event")
	}
}

func TestInstallationOwnershipFailsClosed(t *testing.T) {
	s := testService(t)
	state, _ := s.state()
	for _, test := range []struct {
		name       string
		registered bool
		owner      string
		failure    error
		want       string
	}{
		{"package", true, "sysutils/sylve", nil, "package"},
		{"mixed", true, "", nil, "mixed"},
		{"other-owner", false, "test/other", nil, "unknown"},
		{"query-failed", false, "", errors.New("database locked"), "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s.ownership = func(context.Context, *pkgmanager.Client, string) (bool, string, error) {
				return test.registered, test.owner, test.failure
			}
			if got := s.installed(t.Context(), state); got.Mode != test.want {
				t.Fatalf("got %+v", got)
			}
		})
	}
	if err := os.WriteFile(s.executable, []byte("externally replaced executable"), 0555); err != nil {
		t.Fatal(err)
	}
	if got := s.installed(t.Context(), state); got.Reason != "sylve_executable_changed_restart_required" {
		t.Fatalf("missed externally replaced executable: %+v", got)
	}
}

func TestInstalledBuildTimeRequiresEmbeddedOrHashBoundMetadata(t *testing.T) {
	const recovered = "2026-10-19T12:00:00Z"
	for _, test := range []struct {
		name  string
		setup func(*Service, *models.HostUpdateState)
		want  string
	}{
		{"embedded", func(*Service, *models.HostUpdateState) {}, "2026-10-18T12:00:00Z"},
		{"unknown", func(s *Service, _ *models.HostUpdateState) { s.builtAt = "" }, ""},
		{"invalid embedded date", func(s *Service, _ *models.HostUpdateState) { s.builtAt = "invalid" }, ""},
		{"saved receipt", func(s *Service, state *models.HostUpdateState) {
			s.builtAt = ""
			state.InstalledDigest, state.InstalledBuiltAt = s.runningDigest, recovered
		}, recovered},
		{"receipt for different executable", func(s *Service, state *models.HostUpdateState) {
			s.builtAt = ""
			state.InstalledDigest, state.InstalledBuiltAt = strings.Repeat("f", 64), recovered
		}, ""},
		{"matching release metadata", func(s *Service, state *models.HostUpdateState) {
			s.builtAt = ""
			state.Release = &sylveupdate.Candidate{SHA256: s.runningDigest, BuiltAt: recovered}
		}, recovered},
		{"unrelated release metadata", func(s *Service, state *models.HostUpdateState) {
			s.builtAt = ""
			state.Release = &sylveupdate.Candidate{SHA256: strings.Repeat("f", 64), BuiltAt: recovered}
		}, ""},
		{"embedded date takes priority", func(s *Service, state *models.HostUpdateState) {
			state.InstalledDigest, state.InstalledBuiltAt = s.runningDigest, recovered
			state.Release = &sylveupdate.Candidate{SHA256: s.runningDigest, BuiltAt: recovered}
		}, "2026-10-18T12:00:00Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := testService(t)
			state, err := s.state()
			if err != nil {
				t.Fatal(err)
			}
			test.setup(s, &state)
			if got := s.installedBuildTime(state); got != test.want {
				t.Fatalf("build date = %q, want %q", got, test.want)
			}
		})
	}
}

func TestLegacyBuildTimeCanUseOnlySuccessfulMatchingUpdateReceipts(t *testing.T) {
	for _, name := range []string{"matching", "different executable", "failed operation", "invalid timestamp"} {
		t.Run(name, func(t *testing.T) {
			s := testService(t)
			s.builtAt = ""
			target := sylveupdate.Candidate{SHA256: s.runningDigest, BuiltAt: "2026-10-19T12:00:00Z"}
			status := "success"
			switch name {
			case "different executable":
				target.SHA256 = strings.Repeat("f", 64)
			case "failed operation":
				status = "failed"
			case "invalid timestamp":
				target.BuiltAt = "invalid"
			}
			plan := models.HostUpdatePlan{ID: uuid.NewString(), Kind: "sylve", Release: &target}
			if err := s.DB.Create(&plan).Error; err != nil {
				t.Fatal(err)
			}
			op := models.HostUpdateOperation{UUID: uuid.NewString(), Action: "sylve-apply", Status: status, PlanID: plan.ID}
			if err := s.DB.Create(&op).Error; err != nil {
				t.Fatal(err)
			}
			state, err := s.state()
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if name == "matching" {
				want = target.BuiltAt
			}
			if got := s.installedBuildTime(state); got != want {
				t.Fatalf("build date = %q, want %q", got, want)
			}
		})
	}
}

func TestReleaseCheckRetainsRecoveredBuildDateWhenLatestReleaseChanges(t *testing.T) {
	s := testService(t)
	s.builtAt = ""
	state, err := s.state()
	if err != nil {
		t.Fatal(err)
	}
	const installedTime = "2026-10-19T12:00:00Z"
	state.Release = &sylveupdate.Candidate{SHA256: s.runningDigest, BuiltAt: installedTime}
	if err := s.DB.Model(&state).Select("Release").Updates(&state).Error; err != nil {
		t.Fatal(err)
	}
	s.installed = func(_ context.Context, state models.HostUpdateState) sylveupdate.Installed {
		return sylveupdate.Installed{Mode: "standalone", Version: s.version, Commit: s.commit, BuiltAt: s.installedBuildTime(state), Channel: "rolling", SHA256: s.runningDigest, Path: s.executable}
	}
	s.command = func(context.Context, string, ...string) (string, error) { return "15.1-RELEASE", nil }
	metadata := `{"version":"0.3.1","commit":"` + strings.Repeat("b", 40) + `","freebsd":"15.1-RELEASE","builtAt":"2026-10-24T12:00:00Z"}`
	sum := sha256.Sum256([]byte(metadata))
	s.releases.HTTP.Transport = releaseTransport(func(request *http.Request) (*http.Response, error) {
		body := metadata
		if request.URL.Host == "api.github.com" {
			base := "https://github.com/AlchemillaHQ/Sylve/releases/download/tip/"
			raw, err := json.Marshal(map[string]any{"tag_name": "tip", "assets": []map[string]any{
				{"id": 1, "name": "sylve-" + runtime.GOARCH, "browser_download_url": base + "sylve-" + runtime.GOARCH, "digest": "sha256:" + strings.Repeat("2", 64), "size": 123},
				{"id": 2, "name": "sylve-version.json", "browser_download_url": base + "sylve-version.json", "digest": "sha256:" + hex.EncodeToString(sum[:]), "size": len(metadata)},
			}})
			if err != nil {
				t.Fatal(err)
			}
			body = string(raw)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	if err := s.checkRelease(t.Context()); err != nil {
		t.Fatal(err)
	}
	state, err = s.state()
	if err != nil || state.InstalledDigest != s.runningDigest || state.InstalledBuiltAt != installedTime || state.Release == nil || state.Release.SHA256 == s.runningDigest {
		t.Fatalf("release check lost the recovered installed date: %+v %v", state, err)
	}
	installed := s.installed(t.Context(), state)
	if installed.BuiltAt != installedTime || !releaseAvailable(state, installed) {
		t.Fatalf("latest release replaced the installed build date: %+v", installed)
	}
}

func TestReleaseCheckSkipsUnsupportedInstallationWithoutStaleFindings(t *testing.T) {
	s := testService(t)
	s.ownership = func(context.Context, *pkgmanager.Client, string) (bool, string, error) {
		return false, "", nil
	}
	previous := time.Now().Add(-time.Hour)
	state, err := s.state()
	if err != nil {
		t.Fatal(err)
	}
	state.Release = &sylveupdate.Candidate{Version: "0.3.2", SHA256: strings.Repeat("b", 64)}
	state.ReleaseCheckedAt, state.ReleaseError = &previous, "previous_release_failure"
	if err = s.DB.Save(&state).Error; err != nil {
		t.Fatal(err)
	}
	if current := s.installed(t.Context(), state); current.Reason != "sylve_standalone_path_required" {
		t.Fatalf("expected a nonstandard standalone path: %+v", current)
	}
	if err = s.checkRelease(t.Context()); err != nil {
		t.Fatalf("self-update eligibility failed the host check: %v", err)
	}
	state, err = s.state()
	if err != nil || state.Release != nil || state.ReleaseCheckedAt != nil || state.ReleaseError != "" {
		t.Fatalf("ineligible self-update retained stale release findings: %+v %v", state, err)
	}
	if eventCount(t, s, "available") != 0 || eventCount(t, s, "health") != 0 {
		t.Fatal("ineligible standalone path generated update or health notifications")
	}
	s.ownership = func(context.Context, *pkgmanager.Client, string) (bool, string, error) {
		return false, "", errors.New("fixture package database failure")
	}
	if err = s.checkRelease(t.Context()); err == nil || code(err) != "sylve_package_ownership_unknown" {
		t.Fatalf("genuine ownership failure was ignored: %v", err)
	}
}

func TestApplyRequiresApprovalAndConsumesPlanOnce(t *testing.T) {
	s := testService(t)
	plan := models.HostUpdatePlan{ID: uuid.NewString(), Kind: "packages", InventoryHash: "approved-inventory", RepositoryHash: "approved-repositories", CatalogHash: "approved-catalog", ExecutableHash: s.runningDigest, ExpiresAt: time.Now().Add(time.Hour), Transaction: pkgmanager.Plan{Changes: []pkgmanager.Change{{Name: "example", Action: "upgrade", From: "1", To: "2"}}}}
	if err := s.DB.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(t.Context(), "packages-apply", Request{PlanID: plan.ID}, "tester"); err == nil || code(err) != "host_update_acknowledgement_required" {
		t.Fatalf("approval error: %v", err)
	}
	if _, err := s.Start(t.Context(), "sylve-apply", Request{PlanID: plan.ID, Acknowledge: true}, "tester"); err == nil || code(err) != "host_update_plan_kind_changed" {
		t.Fatalf("kind error: %v", err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	s.probe = func() error {
		close(entered)
		<-release
		return errors.New("fixture_apply_stopped")
	}
	job, err := s.Start(t.Context(), "packages-apply", Request{PlanID: plan.ID, Acknowledge: true}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("installation preflight not reached")
	}
	if _, err = s.Start(t.Context(), "audit", Request{}, "tester"); err == nil || code(err) != "host_update_in_progress" {
		t.Fatalf("concurrent operation admitted: %v", err)
	}
	replayed, err := s.Start(t.Context(), "packages-apply", Request{PlanID: plan.ID, Acknowledge: true}, "tester")
	if err != nil || replayed.ID != job.ID {
		t.Fatalf("plan replay created new operation: %+v, %v", replayed, err)
	}
	close(release)
	completed := waitJob(t, s, job.ID)
	if completed.Status != "failed" {
		t.Fatalf("unexpected terminal status: %+v", completed)
	}
	if eventCount(t, s, "operation") != 1 {
		t.Fatal("operation result missing")
	}
	s.finish(completed.HostUpdateOperation, errors.New("fixture_apply_stopped"), "")
	if eventCount(t, s, "operation") != 1 {
		t.Fatal("completion replay duplicated notification")
	}
}

func waitJob(t *testing.T, s *Service, id uint) Job {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(5 * time.Second)
	for {
		job, err := s.Job(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.FinishedAt != nil {
			return job
		}
		select {
		case <-ticker.C:
		case <-timeout:
			t.Fatal("operation did not finish")
		}
	}
}

func TestRestartCompletionVerifiesNewProcessBeforeSuccess(t *testing.T) {
	s := testService(t)
	if err := os.WriteFile(s.executable, []byte("new executable"), 0555); err != nil {
		t.Fatal(err)
	}
	newDigest, _ := sylveupdate.FileDigest(s.executable)
	target := sylveupdate.Candidate{Version: "0.3.2", Commit: strings.Repeat("b", 40), BuiltAt: "2026-10-24T12:00:00Z", Channel: "rolling", SHA256: newDigest}
	plan := models.HostUpdatePlan{ID: uuid.NewString(), Kind: "sylve", Release: &target, ExpiresAt: time.Now().Add(time.Hour)}
	if err := s.DB.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	one := uint(1)
	op := models.HostUpdateOperation{UUID: uuid.NewString(), Active: &one, Action: "sylve-apply", Phase: "restarting", Status: "running", PlanID: plan.ID, RestartDigest: newDigest}
	if err := s.DB.Create(&op).Error; err != nil {
		t.Fatal(err)
	}
	job, err := s.Job(t.Context(), op.ID)
	if err != nil || job.Phase != "restarting" || job.FinishedAt != nil {
		t.Fatalf("marked success before startup: %+v, %v", job, err)
	}
	s.runningDigest, s.version, s.commit = newDigest, target.Version, target.Commit
	s.builtAt = target.BuiltAt
	if err = s.Recover(); err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, s, op.ID)
	state, _ := s.state()
	if err != nil || job.Status != "success" || job.FinishedAt == nil || state.InstalledDigest != newDigest || state.InstalledChannel != "rolling" || state.InstalledBuiltAt != target.BuiltAt || job.Result != "Sylve was updated and restarted successfully." {
		t.Fatalf("startup did not finalize receipt: %+v %+v %v", job, state, err)
	}
	if eventCount(t, s, "operation") != 1 {
		t.Fatal("result notification missing")
	}
}

func TestRestartCompletionRejectsUnverifiedEmbeddedBuildTime(t *testing.T) {
	for _, stamp := range []string{"", "invalid", "2026-10-18T12:00:00Z", "2026-10-24T12:00:00.1Z"} {
		t.Run(stamp, func(t *testing.T) {
			s := testService(t)
			s.builtAt = stamp
			target := sylveupdate.Candidate{Version: s.version, Commit: s.commit, BuiltAt: "2026-10-24T12:00:00Z", Channel: "rolling", SHA256: s.runningDigest}
			plan := models.HostUpdatePlan{ID: uuid.NewString(), Kind: "sylve", Release: &target}
			if err := s.DB.Create(&plan).Error; err != nil {
				t.Fatal(err)
			}
			if err := s.DB.Model(&models.HostUpdateState{ID: 1}).Select("Release").Updates(&models.HostUpdateState{Release: &target}).Error; err != nil {
				t.Fatal(err)
			}
			one := uint(1)
			op := models.HostUpdateOperation{UUID: uuid.NewString(), Active: &one, Action: "sylve-apply", Status: "running", Phase: "restarting", PlanID: plan.ID, RestartDigest: s.runningDigest}
			if err := s.DB.Create(&op).Error; err != nil {
				t.Fatal(err)
			}
			if err := s.Recover(); err != nil {
				t.Fatal(err)
			}
			job := waitJob(t, s, op.ID)
			if job.Status != "failed" || job.Error != "sylve_startup_identity_mismatch" {
				t.Fatalf("unverified startup succeeded: %+v", job)
			}
			state, err := s.state()
			if err != nil || state.InstalledDigest != "" || state.InstalledBuiltAt != "" {
				t.Fatalf("unverified process was recorded as installed: %+v %v", state, err)
			}
		})
	}
}

func TestRecoveryMarksUnfinishedOperationsInterrupted(t *testing.T) {
	for _, phase := range []string{"queued", "verifying", "applying", "restarting"} {
		t.Run(phase, func(t *testing.T) {
			s := testService(t)
			one := uint(1)
			op := models.HostUpdateOperation{UUID: uuid.NewString(), Active: &one, Action: "packages-apply", Phase: phase, Status: "running"}
			if err := s.DB.Create(&op).Error; err != nil {
				t.Fatal(err)
			}
			if err := s.Recover(); err != nil {
				t.Fatal(err)
			}
			job := waitJob(t, s, op.ID)
			if job.Status != "failed" || !strings.Contains(job.Error, "host_update_interrupted") || job.Active != nil {
				t.Fatalf("unexpected recovery: %+v", job)
			}
		})
	}
}

func TestMonitoringValidationAndActiveOperationFence(t *testing.T) {
	s := testService(t)
	for _, input := range []Monitoring{{Enabled: true, IntervalHours: 1}, {Enabled: true, IntervalHours: 24, CronExpr: "invalid"}} {
		if _, err := s.SetMonitoring(t.Context(), input); err == nil {
			t.Fatalf("accepted %+v", input)
		}
	}
	state, err := s.SetMonitoring(t.Context(), Monitoring{Enabled: false, IntervalHours: 12})
	if err != nil || state.Monitoring || state.NextCheckAt != nil || state.IntervalHours != 12 {
		t.Fatalf("monitor disabled: %+v %v", state, err)
	}
	one := uint(1)
	if err = s.DB.Create(&models.HostUpdateOperation{UUID: uuid.NewString(), Active: &one}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetMonitoring(t.Context(), Monitoring{Enabled: true, IntervalHours: 24}); err == nil {
		t.Fatal("monitor configuration raced active operation")
	}
}

func TestReleaseChannelDoesNotChangeMonitoring(t *testing.T) {
	s := testService(t)
	state, err := s.SetMonitoring(t.Context(), Monitoring{Enabled: true, CronExpr: "15 3 * * 0"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	state.Release = &sylveupdate.Candidate{Channel: "rolling", Version: "0.3.2"}
	state.ReleaseCheckedAt, state.ReleaseError = &now, "previous_release_failure"
	if err = s.DB.Save(&state).Error; err != nil {
		t.Fatal(err)
	}
	saved, err := s.SetReleaseChannel(t.Context(), "tagged")
	if err != nil || saved.ReleaseChannel != "tagged" || saved.Release != nil || saved.ReleaseCheckedAt != nil || saved.ReleaseError != "" || saved.Monitoring != state.Monitoring || saved.CronExpr != state.CronExpr || !saved.NextCheckAt.Equal(*state.NextCheckAt) {
		t.Fatalf("changing channel changed the schedule or retained the old result: %+v %v", saved, err)
	}
	for _, channel := range []string{"", "external"} {
		if _, err := s.SetReleaseChannel(t.Context(), channel); err == nil || code(err) != "invalid_release_channel" {
			t.Fatalf("invalid channel was accepted: %q %v", channel, err)
		}
	}
	saved.Release = &sylveupdate.Candidate{Channel: "tagged", Version: "0.3.2"}
	saved.ReleaseCheckedAt = &now
	if err = s.DB.Save(&saved).Error; err != nil {
		t.Fatal(err)
	}
	unchanged, err := s.SetReleaseChannel(t.Context(), "tagged")
	if err != nil || unchanged.Release == nil || unchanged.ReleaseCheckedAt == nil {
		t.Fatalf("saving the same channel discarded its result: %+v %v", unchanged, err)
	}
	saved, err = s.SetMonitoring(t.Context(), Monitoring{Enabled: false, IntervalHours: 12})
	if err != nil || saved.ReleaseChannel != "tagged" || saved.Release == nil || saved.ReleaseCheckedAt == nil || saved.NextCheckAt != nil {
		t.Fatalf("monitoring changed the saved channel or release result: %+v %v", saved, err)
	}
	one := uint(1)
	if err = s.DB.Create(&models.HostUpdateOperation{UUID: uuid.NewString(), Active: &one}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetReleaseChannel(t.Context(), "rolling"); err == nil || code(err) != "host_update_in_progress" {
		t.Fatalf("channel changed during an active operation: %v", err)
	}
}

func TestReleaseAvailabilityUsesInstalledIdentity(t *testing.T) {
	current := sylveupdate.Installed{Mode: "standalone", Version: "0.3.1", BuiltAt: "2026-10-19T12:00:00Z", SHA256: strings.Repeat("1", 64)}
	target := sylveupdate.Candidate{Channel: "rolling", Version: "0.3.2", Commit: strings.Repeat("b", 40), BuiltAt: "2026-10-20T12:00:00Z", SHA256: strings.Repeat("2", 64)}
	checked := time.Now().UTC()
	for _, tc := range []struct {
		name   string
		change func(*models.HostUpdateState, *sylveupdate.Installed)
		want   bool
	}{
		{"newer", func(*models.HostUpdateState, *sylveupdate.Installed) {}, true},
		{"installed", func(_ *models.HostUpdateState, installed *sylveupdate.Installed) { installed.SHA256 = target.SHA256 }, false},
		{"wrong channel", func(state *models.HostUpdateState, _ *sylveupdate.Installed) { state.ReleaseChannel = "tagged" }, false},
		{"failed check", func(state *models.HostUpdateState, _ *sylveupdate.Installed) {
			state.ReleaseError = "release_fetch_failed"
		}, false},
		{"unchecked", func(state *models.HostUpdateState, _ *sylveupdate.Installed) { state.ReleaseCheckedAt = nil }, false},
		{"no candidate", func(state *models.HostUpdateState, _ *sylveupdate.Installed) { state.Release = nil }, false},
		{"package-owned", func(_ *models.HostUpdateState, installed *sylveupdate.Installed) { installed.Mode = "package" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate, installed := target, current
			state := models.HostUpdateState{ReleaseChannel: "rolling", Release: &candidate, ReleaseCheckedAt: &checked}
			tc.change(&state, &installed)
			if got := releaseAvailable(state, installed); got != tc.want {
				t.Fatalf("release availability = %v, want %v", got, tc.want)
			}
		})
	}
	s := testService(t)
	s.installed = func(context.Context, models.HostUpdateState) sylveupdate.Installed { return current }
	if err := s.DB.Model(&models.HostUpdateState{ID: 1}).Select("Release", "ReleaseCheckedAt").Updates(&models.HostUpdateState{Release: &target, ReleaseCheckedAt: &checked}).Error; err != nil {
		t.Fatal(err)
	}
	view, err := s.Get(t.Context())
	if err != nil || !view.ReleaseAvailable {
		t.Fatalf("eligible release was not exposed: %+v %v", view, err)
	}
	current.SHA256, current.Version = target.SHA256, target.Version
	view, err = s.Get(t.Context())
	if err != nil || view.ReleaseAvailable || view.Release == nil {
		t.Fatalf("installed candidate was still offered: %+v %v", view, err)
	}
}

type releaseTransport func(*http.Request) (*http.Response, error)

func (fn releaseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestSylveReleaseReadsUseSavedChannelWithoutOperationsOrPackageWork(t *testing.T) {
	s, requests := releaseReadService(t)
	s.Telemetry = s.DB
	if err := s.Telemetry.AutoMigrate(&infoModels.AuditRecord{}); err != nil {
		t.Fatal(err)
	}
	current := sylveupdate.Installed{Mode: "standalone", Version: s.version, Commit: s.commit, BuiltAt: s.builtAt, SHA256: s.runningDigest, Path: s.executable}
	s.installed = func(context.Context, models.HostUpdateState) sylveupdate.Installed { return current }
	s.probe = func() error {
		t.Error("release check reached the host package probe")
		return errors.New("unexpected_probe")
	}
	s.command = func(_ context.Context, binary string, args ...string) (string, error) {
		if binary != "/bin/freebsd-version" || !slices.Equal(args, []string{"-u"}) {
			t.Errorf("unexpected release-check command: %s %v", binary, args)
		}
		return "15.1-RELEASE", nil
	}
	transport := s.releases.HTTP.Transport
	fail := false
	s.releases.HTTP.Transport = releaseTransport(func(request *http.Request) (*http.Response, error) {
		if fail {
			requests.Add(1)
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
		}
		return transport.RoundTrip(request)
	})
	if _, err := s.SetReleaseChannel(t.Context(), "tagged"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		view, err := s.GetRelease(t.Context(), true)
		if err != nil {
			t.Fatal(err)
		}
		if !view.ReleaseAvailable || view.ReleaseChannel != "tagged" || view.Release == nil || view.Release.Channel != "tagged" {
			t.Fatalf("standalone release read ignored the saved channel: %+v", view)
		}
	}
	s.releaseAttemptAt = time.Time{}
	if view, err := s.GetRelease(t.Context(), false); err != nil || !view.ReleaseAvailable {
		t.Fatalf("cached release was not available on page load: %+v %v", view, err)
	}
	for _, model := range []any{&models.HostUpdateOperation{}, &models.HostUpdatePlan{}, &infoModels.AuditRecord{}} {
		var count int64
		if err := s.DB.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("release read created %T rows: %d %v", model, count, err)
		}
	}
	state, err := s.state()
	if err != nil || !releaseAvailable(state, current) || state.CheckedAt != nil || state.AuditAt != nil || requests.Load() != 4 || eventCount(t, s, "available") != 1 {
		t.Fatalf("release check changed package results or repeated notifications: %+v %v", state, err)
	}
	var event models.HostUpdateEvent
	if err := s.DB.Where("kind = ?", "system.updates.available.host").First(&event).Error; err != nil || !strings.HasSuffix(event.Metadata["path"], "/system/updates/sylve") {
		t.Fatalf("Sylve availability notification did not link to the Sylve page: %+v %v", event, err)
	}
	preview, err := s.Start(t.Context(), "sylve-preview", Request{}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	preview = waitJob(t, s, preview.ID)
	if preview.Status != "success" || preview.Plan == nil || preview.Plan.Release == nil || preview.Plan.Release.Channel != "tagged" || preview.Plan.ExecutableHash != current.SHA256 {
		t.Fatalf("update approval did not pin the saved channel and current executable: %+v", preview)
	}
	fail = true
	if _, err := s.GetRelease(t.Context(), true); err == nil || code(err) != "release_fetch_failed" {
		t.Fatalf("failed fetch was reported as success: %v", err)
	}
	failed, err := s.state()
	if err != nil || failed.Release == nil || !failed.ReleaseCheckedAt.Equal(*state.ReleaseCheckedAt) || failed.ReleaseError == "" || releaseAvailable(failed, current) {
		t.Fatalf("failed check lost recorded findings or offered a stale update: %+v %v", failed, err)
	}
	attempts := requests.Load()
	if view, err := s.GetRelease(t.Context(), false); err != nil || view.ReleaseError == "" || view.Release == nil || view.ReleaseAvailable || requests.Load() != attempts {
		t.Fatalf("page load retried a recent failure or lost stale metadata: %+v %v", view, err)
	}
	fail = false
	current.Version, current.SHA256 = state.Release.Version, state.Release.SHA256
	if _, err := s.GetRelease(t.Context(), true); err != nil {
		t.Fatalf("already-current tagged release was treated as an error: %v", err)
	}
	state, err = s.state()
	if err != nil || state.ReleaseError != "" || releaseAvailable(state, current) {
		t.Fatalf("already-current release was still available: %+v %v", state, err)
	}
}

func TestChangingReleaseChannelInvalidatesUnusedApproval(t *testing.T) {
	s := testService(t)
	plan := models.HostUpdatePlan{ID: uuid.NewString(), Kind: "sylve", Release: &sylveupdate.Candidate{Channel: "rolling"}, ExpiresAt: time.Now().UTC().Add(time.Minute)}
	if err := s.DB.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetReleaseChannel(t.Context(), "tagged"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(t.Context(), "sylve-apply", Request{PlanID: plan.ID, Acknowledge: true}, "tester"); err == nil || code(err) != "host_update_plan_stale" {
		t.Fatalf("approval for the previous channel was accepted: %v", err)
	}
	var operations int64
	if err := s.DB.Model(&models.HostUpdateOperation{}).Count(&operations).Error; err != nil || operations != 0 {
		t.Fatalf("stale approval queued work: %d %v", operations, err)
	}
}

func TestStandaloneReleaseReadRejectsPackageOwnedInstallation(t *testing.T) {
	s := testService(t)
	s.releases.HTTP.Transport = releaseTransport(func(*http.Request) (*http.Response, error) {
		t.Error("package-owned installation requested standalone release metadata")
		return nil, errors.New("unexpected_release_request")
	})
	if _, err := s.GetRelease(t.Context(), false); err == nil || code(err) != "sylve_package_managed" {
		t.Fatalf("package-owned installation used the release updater: %v", err)
	}
	if _, err := s.Start(t.Context(), "sylve-check", Request{}, "tester"); err == nil || code(err) != "invalid_update_action" {
		t.Fatalf("release metadata still starts an audited POST operation: %v", err)
	}
}

func TestManualPathReleaseChecksInspectMetadataWithoutEnablingUpdates(t *testing.T) {
	for _, channel := range []string{"rolling", "tagged"} {
		t.Run(channel, func(t *testing.T) {
			s, requests := releaseReadService(t)
			s.version, s.builtAt = "0.3.3", "2026-10-25T12:00:00Z"
			s.probe = func() error {
				t.Error("metadata inspection reached host package management")
				return errors.New("unexpected_probe")
			}
			s.command = func(context.Context, string, ...string) (string, error) {
				t.Error("metadata inspection attempted an installation preflight")
				return "", errors.New("unexpected_command")
			}
			if _, err := s.SetReleaseChannel(t.Context(), channel); err != nil {
				t.Fatal(err)
			}
			if err := s.checkRelease(t.Context()); err != nil {
				t.Fatal(err)
			}
			view, err := s.GetRelease(t.Context(), false)
			if err != nil {
				t.Fatal(err)
			}
			if view.Release == nil || view.ReleaseChannel != channel || view.ReleaseAvailable {
				t.Fatalf("manual-path metadata read failed or offered an update: %+v", view)
			}
			state, err := s.state()
			if err != nil || state.Release == nil || state.Release.Version != "0.3.2" || state.Release.BuiltAt != "2026-10-24T12:00:00Z" || state.ReleaseCheckedAt == nil || state.ReleaseError != "" || requests.Load() != 2 {
				t.Fatalf("published metadata was not available for inspection: %+v %v", state, err)
			}
			current := s.installed(t.Context(), state)
			if current.Reason != "sylve_standalone_path_required" || releaseAvailable(state, current) || state.InstalledDigest != "" || state.InstalledBuiltAt != "" || eventCount(t, s, "available") != 0 {
				t.Fatalf("metadata inspection treated a manual installation as updateable: %+v %+v", current, state)
			}
			preview, err := s.Start(t.Context(), "sylve-preview", Request{}, "tester")
			if err != nil {
				t.Fatal(err)
			}
			preview = waitJob(t, s, preview.ID)
			if preview.Status != "failed" || preview.Error != "sylve_standalone_path_required" || preview.Plan != nil || requests.Load() != 2 {
				t.Fatalf("manual-path inspection bypassed the installation guard: %+v", preview)
			}
		})
	}
}

func TestScheduledChecksDoNotCreateRequestAuditRecords(t *testing.T) {
	for _, busy := range []bool{false, true} {
		t.Run(fmt.Sprintf("busy=%v", busy), func(t *testing.T) {
			s := testService(t)
			s.Telemetry = s.DB
			if err := s.Telemetry.AutoMigrate(&infoModels.AuditRecord{}); err != nil {
				t.Fatal(err)
			}
			if busy {
				one := uint(1)
				if err := s.DB.Create(&models.HostUpdateOperation{UUID: uuid.NewString(), Active: &one, Action: "audit"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := s.schedule(); err != nil {
				t.Fatal(err)
			}
			var op models.HostUpdateOperation
			if err := s.DB.First(&op).Error; err != nil {
				t.Fatal(err)
			}
			if !busy {
				if op.Action != "check" || op.Actor != "host-update-monitor" {
					t.Fatalf("scheduled check was not recorded in update history: %+v", op)
				}
				if job := waitJob(t, s, op.ID); job.Status != "failed" || job.Error != "fixture_probe_disabled" {
					t.Fatalf("scheduled failure was not retained: %+v", job)
				}
			}
			if err := s.schedule(); err != nil {
				t.Fatal(err)
			}
			var count int64
			if err := s.Telemetry.Model(&infoModels.AuditRecord{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("scheduled monitoring created request audit rows: %d %v", count, err)
			}
			state, err := s.state()
			if err != nil || state.NextCheckAt == nil || !state.NextCheckAt.After(time.Now()) {
				t.Fatalf("monitoring schedule was not preserved: %+v %v", state, err)
			}
		})
	}
}

func TestUserTriggeredChecksStillBindAndFinalizeRequestAuditRecords(t *testing.T) {
	for _, action := range []string{"check", "audit"} {
		t.Run(action, func(t *testing.T) {
			s := testService(t)
			s.Telemetry = s.DB
			if err := s.Telemetry.AutoMigrate(&infoModels.AuditRecord{}); err != nil {
				t.Fatal(err)
			}
			record := infoModels.AuditRecord{User: "tester", AuthType: "local", Started: time.Now().UTC(), Action: `{}`, Status: "started", Version: 2}
			if err := s.Telemetry.Create(&record).Error; err != nil {
				t.Fatal(err)
			}
			job, err := s.Start(db.ContextWithAuditRecordID(t.Context(), record.ID), action, Request{}, "tester")
			if err != nil {
				t.Fatal(err)
			}
			terminal := waitJob(t, s, job.ID)
			if err := s.reconcileAudits(); err != nil {
				t.Fatal(err)
			}
			if err := s.Telemetry.First(&record, record.ID).Error; err != nil || record.User != "tester" || record.Status != terminal.Status || record.AsyncJobID == nil || *record.AsyncJobID != job.ID || record.AsyncOperationID != "host-update:"+job.UUID || record.Ended.IsZero() {
				t.Fatalf("user request lost its audit record: %+v %v", record, err)
			}
		})
	}
}

func TestPublicViewDoesNotExposeFreshAuditWithoutInventoryVerification(t *testing.T) {
	s := testService(t)
	inventory := []pkgmanager.Package{{Name: "example", Version: "1", Origin: "test/example"}}
	if err := s.storeAudit(inventory, pkgmanager.Audit{Packages: []pkgmanager.AuditPackage{}}, 0); err != nil {
		t.Fatal(err)
	}
	view, err := s.Get(t.Context())
	if err != nil || !view.AuditStale || view.Ready {
		t.Fatalf("unverified inventory marked clean: %+v %v", view, err)
	}
	if !slices.EqualFunc(view.Inventory, inventory, func(a, b pkgmanager.Package) bool { return a.Name == b.Name && a.Version == b.Version }) {
		t.Fatal("last inventory lost")
	}
}

func TestMissingOrExpiredPlanDoesNotQueueWork(t *testing.T) {
	s := testService(t)
	plan := models.HostUpdatePlan{ID: uuid.NewString(), Kind: "packages", ExpiresAt: time.Now().Add(-time.Minute)}
	if err := s.DB.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"missing", plan.ID} {
		if _, err := s.Start(t.Context(), "packages-apply", Request{PlanID: id, Acknowledge: true}, "test"); err == nil {
			t.Fatal("queued invalid plan")
		}
	}
	var count int64
	if err := s.DB.Model(&models.HostUpdateOperation{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("queued operations: %d %v", count, err)
	}
}

func TestPackagedSylveChecksExecutableRatherThanLocalConfiguration(t *testing.T) {
	c := pkgmanager.NewHostClient(nil)
	path := "/usr/local/sbin/sylve"
	modified := false
	c.RunCommand = func(_ context.Context, args []string) (pkgmanager.Result, error) {
		if slices.Contains(args, "query") {
			return pkgmanager.Result{Stdout: "/usr/local/etc/sylve/config.json\tlocal-configuration-checksum\n" + path + "\t2$expected-digest\n"}, nil
		}
		if !slices.Contains(args, "checksum") || !slices.Equal(args[len(args)-5:], []string{"checksum", "-q", "-c", "2$expected-digest", path}) {
			t.Fatalf("unexpected command: %v", args)
		}
		if modified {
			return pkgmanager.Result{ExitCode: 1}, nil
		}
		return pkgmanager.Result{}, nil
	}
	plan := pkgmanager.Plan{Changes: []pkgmanager.Change{{Name: "sylve", Action: "upgrade", From: "0.3.1", To: "0.3.2"}}}
	if err := verifyPackagedSylve(t.Context(), c, plan, nil, path); err != nil {
		t.Fatal(err)
	}
	modified = true
	if err := verifyPackagedSylve(t.Context(), c, plan, nil, path); err == nil || code(err) != "packaged_sylve_modified" {
		t.Fatalf("hybrid executable accepted: %v", err)
	}
}

type deliveryStub struct {
	targets []string
	calls   map[string]int
	fail    bool
}

func (*deliveryStub) Emit(context.Context, notifier.EventInput) (notifier.EmitResult, error) {
	return notifier.EmitResult{}, errors.New("unexpected broadcast")
}
func (s *deliveryStub) DeliveryTargets(context.Context, notifier.EventInput) ([]string, error) {
	return s.targets, nil
}
func (s *deliveryStub) EmitTarget(_ context.Context, event notifier.EventInput, target string) (notifier.EmitResult, error) {
	s.calls[target]++
	if target == "email" && s.fail {
		return notifier.EmitResult{}, errors.New("temporary delivery failure")
	}
	if target == "removed" {
		return notifier.EmitResult{}, notifier.PermanentDeliveryError(errors.New("transport removed"))
	}
	return notifier.EmitResult{}, nil
}

func TestNotificationRetriesOnlyUndeliveredTargetsAcrossRestart(t *testing.T) {
	s := testService(t)
	emitter := &deliveryStub{targets: []string{"ui", "email", "removed"}, calls: map[string]int{}, fail: true}
	notifier.SetEmitter(emitter)
	t.Cleanup(func() { notifier.SetEmitter(nil) })
	if err := s.event(s.DB, "available", "Update available", "fixture", "info", 0, "packages"); err != nil {
		t.Fatal(err)
	}
	if err := s.dispatchEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	var event models.HostUpdateEvent
	if err := s.DB.First(&event).Error; err != nil {
		t.Fatal(err)
	}
	if event.FinishedAt != nil || !slices.Equal(event.Delivered, []string{"ui", "removed"}) {
		t.Fatalf("delivery progress not durable: %+v", event)
	}
	if err := s.DB.Model(&event).Update("next_attempt_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	emitter.fail = false
	restarted := &Service{DB: s.DB}
	if err := restarted.dispatchEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	if emitter.calls["ui"] != 1 || emitter.calls["removed"] != 1 || emitter.calls["email"] != 2 {
		t.Fatalf("duplicate or lost delivery: %+v", emitter.calls)
	}
	if err := s.DB.First(&event).Error; err != nil || event.FinishedAt == nil {
		t.Fatalf("delivery did not finish: %+v %v", event, err)
	}
}

func TestRecoveryReconcilesTerminalAuditAfterDatabaseBoundaryCrash(t *testing.T) {
	s := testService(t)
	telemetry, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	connection, _ := telemetry.DB()
	connection.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = connection.Close() })
	if err = telemetry.AutoMigrate(&infoModels.AuditRecord{}); err != nil {
		t.Fatal(err)
	}
	s.Telemetry = telemetry
	finished := time.Now().UTC()
	op := models.HostUpdateOperation{UUID: uuid.NewString(), Status: "success", FinishedAt: &finished}
	if err = s.DB.Create(&op).Error; err != nil {
		t.Fatal(err)
	}
	record := infoModels.AuditRecord{Started: finished, Status: "started", Action: `{}`}
	if err = telemetry.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = db.PrepareAsyncAuditRecord(telemetry, db.ContextWithAuditRecordID(t.Context(), record.ID), "host_update", op.ID, "host-update:"+op.UUID); err != nil {
		t.Fatal(err)
	}
	if err = s.reconcileAudits(); err != nil {
		t.Fatal(err)
	}
	if err = telemetry.First(&record, record.ID).Error; err != nil || record.Status != "success" || record.Ended.IsZero() {
		t.Fatalf("audit remained pending: %+v %v", record, err)
	}
}

func TestRebootWarningSurvivesMissingBootIdentityAndTimezoneChanges(t *testing.T) {
	s := testService(t)
	raw := "{ sec = 1792310176, usec = 146848 } Sun Oct 18 13:26:16 2026"
	if bootIdentity(raw) != bootIdentity("{ sec = 1792310176, usec = 146848 } Sun Oct 18 07:56:16 2026") {
		t.Fatal("timezone changed boot identity")
	}
	if err := s.DB.Model(&models.HostUpdateState{}).Where("id = 1").Update("reboot_boot_id", bootIdentity(raw)).Error; err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		output   string
		expected bool
	}{{raw, true}, {"unavailable", true}, {"{ sec = 1792400000, usec = 0 } next boot", false}} {
		s.command = func(context.Context, string, ...string) (string, error) { return test.output, nil }
		view, err := s.Get(t.Context())
		if err != nil || view.RebootRecommended != test.expected {
			t.Fatalf("reboot marker %q: %+v %v", test.output, view.RebootRecommended, err)
		}
	}
}
