// SPDX-License-Identifier: BSD-2-Clause

package jail

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	jailTypes "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/pkg/pkgmanager"
)

type systemPackageFixture struct {
	inventory string
	host      uint32
	offline   bool
	refreshes int
}

func newSystemPackageTestService(t *testing.T) (*Service, jailModels.Jail, *systemPackageFixture) {
	t.Helper()
	s, jail := newPackageTestService(t)
	root := t.TempDir()
	for _, repo := range []string{"FreeBSD-base", "ports"} {
		name := filepath.Join(root, "var/db/pkg/repos", repo, "db")
		if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("fixture catalog"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	fixture := &systemPackageFixture{
		inventory: "FreeBSD-runtime\t15.1\tbase/FreeBSD-runtime\tFreeBSD-base-release-1\t1\t1\t0\nFreeBSD-clibs\t15.1\tbase/FreeBSD-clibs\tFreeBSD-base-release-1\t1\t1\t0\npkg\t2.6.2\tports-mgmt/pkg\tFreeBSD-base-release-1\t1\t0\t0\napp\t1\tmisc/app\tports\t1\t0\t0\n",
		host:      1600027,
	}
	s.packages.resolve = func(context.Context, *jailModels.Jail) (pkgmanager.Target, bool, error) {
		target := pkgmanager.Target{JID: 4, Name: "fixture", Root: root}
		if fixture.offline {
			target.JID, target.Offline, target.Dataset, target.GUID = 0, true, "tank/jail", jail.Storages[0].GUID
		}
		return target, !fixture.offline, nil
	}
	s.packages.client = func(target pkgmanager.Target, log io.Writer) *pkgmanager.Client {
		client := &pkgmanager.Client{Target: target, Log: log, HostOSVersion: func() (uint32, error) { return fixture.host, nil }}
		client.RunCommand = func(_ context.Context, args []string) (pkgmanager.Result, error) {
			if args[len(args)-1] == "-N" {
				return pkgmanager.Result{}, nil
			}
			if args[len(args)-1] == "-vv" {
				return pkgmanager.Result{Stdout: "Repositories:\nports: {\nurl: \"https://example.invalid/ports\",\nenabled: yes,\n}\nFreeBSD-base: {\nurl: \"https://example.invalid/base_release_1\",\nenabled: yes,\n}\n"}, nil
			}
			if q := slices.Index(args, "query"); q >= 0 {
				if args[q+1] == "-a" {
					return pkgmanager.Result{Stdout: fixture.inventory}, nil
				}
				if args[q+1] == "%Fp" || args[len(args)-1] == "%Fp" {
					return pkgmanager.Result{}, nil
				}
				const metadata = "%n\t%v\t%o\t%q\t%p\t%At\t%Av"
				if slices.Contains(args, metadata) {
					name, version := args[len(args)-1], "15.1"
					if args[q+1] == "-F" {
						name, version = "FreeBSD-runtime", "15.1p1"
					}
					return pkgmanager.Result{Stdout: fmt.Sprintf("%s\t%s\tbase/%s\tFreeBSD:15:amd64\t/\tFreeBSD_version\t1501000\n", name, version, name)}, nil
				}
			}
			if p := slices.Index(args, "update"); p >= 0 {
				fixture.refreshes++
				if !slices.Equal(args[p+1:], []string{"-r", "FreeBSD-base"}) {
					t.Fatalf("system check refreshed other repositories: %v", args)
				}
				return pkgmanager.Result{}, nil
			}
			if slices.Contains(args, "rquery") {
				return pkgmanager.Result{Stdout: "FreeBSD-runtime\t15.1p1\tbase/FreeBSD-runtime\tFreeBSD:15:amd64\nFreeBSD-clibs\t15.1\tbase/FreeBSD-clibs\tFreeBSD:15:amd64\n"}, nil
			}
			if slices.Contains(args, "upgrade") {
				if !slices.Contains(args, "-n") || !slices.Contains(args, "FreeBSD-base") || !slices.Contains(args, "FreeBSD-runtime") || !slices.Contains(args, "FreeBSD-clibs") || slices.Contains(args, "app") || slices.Contains(args, "pkg") {
					t.Fatalf("unscoped system preview or attempted installer: %v", args)
				}
				return pkgmanager.Result{Stdout: "The following 1 package(s) will be affected (of 2 checked):\nInstalled packages to be UPGRADED:\n\tFreeBSD-runtime: 15.1 -> 15.1p1 [FreeBSD-base]\nNumber of packages to be upgraded: 1\n"}, nil
			}
			if slices.Contains(args, "fetch") {
				return pkgmanager.Result{}, nil
			}
			if slices.Contains(args, "search") {
				return pkgmanager.Result{Stdout: `[{"name":"FreeBSD-runtime","version":"15.1p1","repopath":"All/FreeBSD-runtime-15.1p1.pkg"}]`}, nil
			}
			t.Fatalf("unexpected command: %v", args)
			return pkgmanager.Result{}, nil
		}
		return client
	}
	return s, jail, fixture
}

func TestPackageSystemPreviewPersistsIdentityAndDerivesApplyScope(t *testing.T) {
	s, jail, fixture := newSystemPackageTestService(t)
	job := runPackageTestTask(t, s, jail.CTID, "pkg-preview", jailTypes.PackageRequest{Scope: pkgmanager.ScopeSystem, All: true})
	if job.Scope != pkgmanager.ScopeSystem || job.Plan == nil || job.Plan.Scope != pkgmanager.ScopeSystem || job.Plan.System == nil || job.Plan.System.Release != "15.1" || job.Plan.System.Repository != "FreeBSD-base" {
		t.Fatalf("system preview %+v", job)
	}
	var upgrades int64
	if err := s.DB.Model(&taskModels.GuestLifecycleTask{}).Where("action = ?", "pkg-upgrade").Count(&upgrades).Error; err != nil || upgrades != 0 {
		t.Fatalf("preview queued an installer without confirmation: %d %v", upgrades, err)
	}
	for _, request := range []jailTypes.PackageRequest{
		{PlanID: job.Plan.ID},
		{PlanID: job.Plan.ID, Scope: pkgmanager.ScopeApplications, Acknowledge: true},
	} {
		if _, err := s.StartPackageOperation(t.Context(), jail.CTID, "pkg-upgrade", request, "user", "test"); err == nil {
			t.Fatal("accepted missing system acknowledgement or caller scope substitution")
		}
	}
	var plan jailModels.JailPackagePlan
	if err := s.DB.First(&plan, "id = ?", job.Plan.ID).Error; err != nil || plan.AppliedOperationID != "" {
		t.Fatalf("rejected request consumed plan: %+v %v", plan, err)
	}
	apply, err := s.StartPackageOperation(t.Context(), jail.CTID, "pkg-upgrade", jailTypes.PackageRequest{PlanID: job.Plan.ID, Acknowledge: true}, "user", "test")
	if err != nil || apply.Scope != pkgmanager.ScopeSystem {
		t.Fatalf("scope was not derived from the plan: %+v %v", apply, err)
	}
	replayed, err := s.StartPackageOperation(t.Context(), jail.CTID, "pkg-upgrade", jailTypes.PackageRequest{PlanID: job.Plan.ID}, "user", "test")
	if err != nil || replayed.TaskID != apply.TaskID {
		t.Fatalf("system apply was not idempotent: %+v %v", replayed, err)
	}
	fixture.host++
	if err = s.ExecutePackageTask(t.Context(), apply.TaskID); err == nil || !strings.Contains(err.Error(), "plan_stale") {
		t.Fatalf("accepted changed host identity: %v", err)
	}
}

func TestPackageSystemOfflinePreviewCannotConsumeOrRunAnUpgrade(t *testing.T) {
	s, jail, fixture := newSystemPackageTestService(t)
	fixture.offline = true
	job := runPackageTestTask(t, s, jail.CTID, "pkg-preview", jailTypes.PackageRequest{Scope: pkgmanager.ScopeSystem, All: true})
	if job.Plan == nil {
		t.Fatalf("offline system preview: %+v", job)
	}
	if _, err := s.StartPackageOperation(t.Context(), jail.CTID, "pkg-upgrade", jailTypes.PackageRequest{PlanID: job.Plan.ID, Acknowledge: true}, "user", "test"); err == nil || err.Error() != "jail_not_running" {
		t.Fatalf("offline admission: %v", err)
	}
	var plan jailModels.JailPackagePlan
	if err := s.DB.First(&plan, "id = ?", job.Plan.ID).Error; err != nil || plan.AppliedOperationID != "" {
		t.Fatalf("offline rejection consumed plan: %+v %v", plan, err)
	}
	fixture.offline = false
	apply, err := s.StartPackageOperation(t.Context(), jail.CTID, "pkg-upgrade", jailTypes.PackageRequest{PlanID: job.Plan.ID, Acknowledge: true}, "user", "test")
	if err != nil {
		t.Fatal(err)
	}
	fixture.offline = true
	if err = s.ExecutePackageTask(t.Context(), apply.TaskID); err == nil || err.Error() != "jail_not_running" {
		t.Fatalf("queued offline application: %v", err)
	}
}

func TestPackageSystemAndApplicationFreshnessStayIndependent(t *testing.T) {
	s, jail, fixture := newSystemPackageTestService(t)
	inventory, err := pkgmanager.ParseInventory(fixture.inventory)
	if err != nil {
		t.Fatal(err)
	}
	for i := range inventory {
		if inventory[i].Name == "app" {
			inventory[i].Available = "2"
		}
	}
	now := time.Now().UTC()
	state := jailModels.JailPackageState{JailID: jail.ID, CTID: jail.CTID, GUID: jail.Storages[0].GUID, Inventory: inventory, CheckedAt: &now, CheckInventoryHash: pkgmanager.InventoryDigest(inventory)}
	if err = s.DB.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	runPackageTestTask(t, s, jail.CTID, "pkg-check-updates", jailTypes.PackageRequest{Scope: pkgmanager.ScopeSystem})
	view, err := s.GetPackages(t.Context(), jail.CTID)
	if err != nil || !view.Pkgbase || !view.UpdatesChecked || !view.SystemUpdatesChecked || view.SystemCheckedAt == nil {
		t.Fatalf("separate freshness: %+v %v", view, err)
	}
	for _, pkg := range view.Packages {
		if pkg.Name == "app" && pkg.Available != "2" || pkg.Name == "FreeBSD-runtime" && (pkg.Available != "15.1p1" || pkg.Kind != pkgmanager.KindBase) {
			t.Fatalf("lost scoped available version: %+v", pkg)
		}
	}
	fixture.host = 1500000
	job, err := s.StartPackageOperation(t.Context(), jail.CTID, "pkg-preview", jailTypes.PackageRequest{Scope: pkgmanager.ScopeSystem, All: true}, "user", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ExecutePackageTask(t.Context(), job.TaskID); err == nil || !strings.Contains(err.Error(), "host_too_old") {
		t.Fatalf("old host: %v", err)
	}
	view, err = s.GetPackages(t.Context(), jail.CTID)
	if err != nil || !view.UpdatesChecked || view.SystemUpdatesChecked || view.SystemCheckError == "" {
		t.Fatalf("system failure affected application freshness: %+v %v", view, err)
	}
}

func TestPackageSystemScopeRequiresWholeJailPreviewAndPkgbaseRecords(t *testing.T) {
	s, jail, fixture := newSystemPackageTestService(t)
	for _, request := range []jailTypes.PackageRequest{
		{Scope: "everything", All: true},
		{Scope: pkgmanager.ScopeSystem, Packages: []string{"FreeBSD-runtime"}},
	} {
		if _, err := s.StartPackageOperation(t.Context(), jail.CTID, "pkg-preview", request, "user", "test"); err == nil {
			t.Fatalf("accepted invalid scoped request: %+v", request)
		}
	}
	fixture.inventory = "app\t1\tmisc/app\tports\t1\t0\t0\n"
	job, err := s.StartPackageOperation(t.Context(), jail.CTID, "pkg-preview", jailTypes.PackageRequest{Scope: pkgmanager.ScopeSystem, All: true}, "user", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ExecutePackageTask(t.Context(), job.TaskID); err == nil || !strings.Contains(err.Error(), "not_pkgbase") {
		t.Fatalf("accepted traditional base: %v", err)
	}
}

func TestPackageRepositoryRefreshReuseStaysWithinItsScope(t *testing.T) {
	s, jail, fixture := newSystemPackageTestService(t)
	client := s.packages.client(pkgmanager.Target{JID: 4, Name: "fixture", Root: t.TempDir()}, nil)
	_, hash, err := client.Repositories(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	state := jailModels.JailPackageState{JailID: jail.ID, CTID: jail.CTID, GUID: jail.Storages[0].GUID, RepositoriesRefreshedAt: &now, RepositoryHash: hash}
	if err = s.DB.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		runPackageTestTask(t, s, jail.CTID, "pkg-preview", jailTypes.PackageRequest{Scope: pkgmanager.ScopeSystem, All: true})
		if fixture.refreshes != 1 {
			t.Fatalf("system refresh count=%d; application freshness must not satisfy system refresh, and the second system preview must reuse it", fixture.refreshes)
		}
	}
}
