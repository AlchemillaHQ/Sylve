// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

type bootstrapCleanupZFSRunner struct {
	datasets   map[string]*gzfs.Dataset
	failList   map[string]bool
	failDelete map[string]bool
	calls      [][]string
}

func (r *bootstrapCleanupZFSRunner) add(name string) {
	pool := strings.SplitN(name, "/", 2)[0]
	r.datasets[name] = &gzfs.Dataset{
		Name: name, Pool: pool, Type: gzfs.DatasetTypeFilesystem,
		Properties: map[string]gzfs.ZFSProperty{},
	}
}

func (r *bootstrapCleanupZFSRunner) Run(ctx context.Context, _ io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if name != "zfs" || len(args) < 2 {
		return fmt.Errorf("unexpected command: %s %v", name, args)
	}
	r.calls = append(r.calls, append([]string(nil), args...))
	target := args[len(args)-1]
	if target == "-j" {
		target = args[len(args)-2]
	}
	switch args[0] {
	case "list":
		if r.failList[target] {
			fmt.Fprint(stderr, "permission denied")
			return errors.New("exit status 1")
		}
		dataset := r.datasets[target]
		if dataset == nil {
			fmt.Fprintf(stderr, "cannot open '%s': dataset does not exist", target)
			return errors.New("exit status 1")
		}
		return json.NewEncoder(stdout).Encode(gzfs.DatasetList{
			Datasets: map[string]*gzfs.Dataset{target: dataset},
		})
	case "destroy":
		if !reflect.DeepEqual(args, []string{"destroy", "-r", target}) {
			return fmt.Errorf("unsafe destroy arguments: %v", args)
		}
		if r.failDelete[target] {
			fmt.Fprint(stderr, "dataset is busy")
			return errors.New("exit status 1")
		}
		for name := range r.datasets {
			if name == target || strings.HasPrefix(name, target+"/") || strings.HasPrefix(name, target+"@") {
				delete(r.datasets, name)
			}
		}
		return nil
	default:
		return fmt.Errorf("unexpected ZFS subcommand: %s", args[0])
	}
}

func newBootstrapCleanupTest(t *testing.T) (*gorm.DB, *gzfs.Client, *bootstrapCleanupZFSRunner) {
	t.Helper()
	database := testutil.NewSQLiteTestDB(t, &models.Migrations{}, &jailModels.JailBootstrap{})
	runner := &bootstrapCleanupZFSRunner{
		datasets: map[string]*gzfs.Dataset{}, failList: map[string]bool{}, failDelete: map[string]bool{},
	}
	client := gzfs.NewClient(gzfs.Options{Runner: runner, ZFSBin: "zfs"})
	return database, client, runner
}

func seedCleanupBootstrap(t *testing.T, database *gorm.DB, runner *bootstrapCleanupZFSRunner, pool string, minor int, kind, status string) jailModels.JailBootstrap {
	t.Helper()
	suffix := "Base"
	if kind == "minimal" {
		suffix = "Minimal"
	}
	record := jailModels.JailBootstrap{
		Pool: pool, Major: 15, Minor: minor, BootstrapType: kind,
		Name: fmt.Sprintf("15-%d-%s", minor, suffix), Status: status,
	}
	record.Dataset = pool + "/sylve/bootstraps/" + record.Name
	if err := database.Create(&record).Error; err != nil {
		t.Fatalf("seed bootstrap: %v", err)
	}
	runner.add(pool)
	runner.add(record.Dataset)
	return record
}

func assertBootstrapCleanupMarker(t *testing.T, database *gorm.DB, name string, want int64) {
	t.Helper()
	var count int64
	if err := database.Model(&models.Migrations{}).Where("name = ?", name).Count(&count).Error; err != nil {
		t.Fatalf("count migration marker: %v", err)
	}
	if count != want {
		t.Fatalf("migration %s count = %d, want %d", name, count, want)
	}
}

func TestCleanupLegacyJailBootstrapsRemovesOnlyRecordedBasesOnce(t *testing.T) {
	database, client, runner := newBootstrapCleanupTest(t)
	records := []jailModels.JailBootstrap{
		seedCleanupBootstrap(t, database, runner, "tank", 0, "base", "completed"),
		seedCleanupBootstrap(t, database, runner, "tank", 0, "minimal", "failed"),
		seedCleanupBootstrap(t, database, runner, "backup", 1, "base", "running"),
		seedCleanupBootstrap(t, database, runner, "backup", 1, "minimal", "pending"),
	}
	delete(runner.datasets, records[1].Dataset)
	runner.add(records[0].Dataset + "@snapshot")
	runner.add(records[0].Dataset + "/child")
	preserved := []string{
		"tank", "backup", "tank/sylve", "tank/sylve/bootstraps", "tank/sylve/jails/113",
		"tank/sylve/virtual-machines/114", "tank/sylve/bootstraps/15-1-Base", "tank/important",
	}
	for _, name := range preserved {
		runner.add(name)
	}
	if err := database.Exec("CREATE TABLE jails (ct_id INTEGER PRIMARY KEY, name TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("INSERT INTO jails VALUES (113, 'existing-jail')").Error; err != nil {
		t.Fatal(err)
	}

	if err := cleanupLegacyJailBootstrapsWithZFS(database, client); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	var remaining int64
	if err := database.Model(&jailModels.JailBootstrap{}).Count(&remaining).Error; err != nil || remaining != 0 {
		t.Fatalf("remaining bootstrap records = %d, error = %v", remaining, err)
	}
	for _, record := range records {
		if runner.datasets[record.Dataset] != nil {
			t.Errorf("legacy bootstrap survived: %s", record.Dataset)
		}
	}
	for _, name := range preserved {
		if runner.datasets[name] == nil {
			t.Errorf("unrelated dataset was removed: %s", name)
		}
	}
	var jailName string
	if err := database.Table("jails").Where("ct_id = 113").Pluck("name", &jailName).Error; err != nil || jailName != "existing-jail" {
		t.Fatalf("existing jail changed: name=%q error=%v", jailName, err)
	}
	assertBootstrapCleanupMarker(t, database, legacyJailBootstrapCleanupMigration, 1)

	replacement := seedCleanupBootstrap(t, database, runner, "tank", 0, "base", "completed")
	calls := len(runner.calls)
	if err := cleanupLegacyJailBootstrapsWithZFS(database, client); err != nil {
		t.Fatalf("repeat cleanup: %v", err)
	}
	if len(runner.calls) != calls || runner.datasets[replacement.Dataset] == nil {
		t.Fatal("completed migration touched ZFS again")
	}
	if err := database.First(&jailModels.JailBootstrap{}, replacement.ID).Error; err != nil {
		t.Fatalf("replacement record was removed: %v", err)
	}
}

func TestCleanupLegacyJailBootstrapsFreshInstall(t *testing.T) {
	database, client, runner := newBootstrapCleanupTest(t)
	if err := cleanupLegacyJailBootstrapsWithZFS(database, client); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 0 {
		t.Fatal("fresh installation queried ZFS")
	}
	assertBootstrapCleanupMarker(t, database, legacyJailBootstrapCleanupMigration, 1)
	record := seedCleanupBootstrap(t, database, runner, "tank", 1, "base", "completed")
	if err := cleanupLegacyJailBootstrapsWithZFS(database, client); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 0 || runner.datasets[record.Dataset] == nil {
		t.Fatal("fresh installation later removed a new bootstrap")
	}
}

func TestCleanupLegacyJailBootstrapsRetriesOnlyOriginalTargets(t *testing.T) {
	for _, failure := range []string{"missing pool", "pool lookup failure", "dataset lookup failure", "destroy failure"} {
		t.Run(failure, func(t *testing.T) {
			database, client, runner := newBootstrapCleanupTest(t)
			removed := seedCleanupBootstrap(t, database, runner, "tank", 0, "base", "completed")
			deferred := seedCleanupBootstrap(t, database, runner, "backup", 1, "minimal", "completed")
			switch failure {
			case "missing pool":
				delete(runner.datasets, deferred.Pool)
			case "pool lookup failure":
				runner.failList[deferred.Pool] = true
			case "dataset lookup failure":
				runner.failList[deferred.Dataset] = true
			case "destroy failure":
				runner.failDelete[deferred.Dataset] = true
			}
			if err := cleanupLegacyJailBootstrapsWithZFS(database, client); err != nil {
				t.Fatalf("storage failure blocked startup: %v", err)
			}
			if runner.datasets[removed.Dataset] != nil || runner.datasets[deferred.Dataset] == nil {
				t.Fatal("unexpected partial cleanup result")
			}
			var stored jailModels.JailBootstrap
			if err := database.First(&stored, deferred.ID).Error; err != nil {
				t.Fatal(err)
			}
			if stored.Status != "failed" || stored.Phase != legacyJailBootstrapCleanupPhase {
				t.Fatalf("deferred bootstrap remains usable or lost its cleanup marker: %#v", stored)
			}
			assertBootstrapCleanupMarker(t, database, legacyJailBootstrapCleanupStarted, 1)
			assertBootstrapCleanupMarker(t, database, legacyJailBootstrapCleanupMigration, 0)

			replacement := seedCleanupBootstrap(t, database, runner, "tank", 0, "base", "completed")
			newBase := seedCleanupBootstrap(t, database, runner, "tank", 1, "base", "completed")
			runner.add(deferred.Pool)
			clear(runner.failList)
			clear(runner.failDelete)
			if err := cleanupLegacyJailBootstrapsWithZFS(database, client); err != nil {
				t.Fatalf("retry: %v", err)
			}
			if runner.datasets[deferred.Dataset] != nil {
				t.Fatal("retry left original target behind")
			}
			for _, record := range []jailModels.JailBootstrap{replacement, newBase} {
				if runner.datasets[record.Dataset] == nil {
					t.Errorf("retry deleted new dataset %s", record.Dataset)
				}
				if err := database.First(&jailModels.JailBootstrap{}, record.ID).Error; err != nil {
					t.Errorf("retry deleted new record %d: %v", record.ID, err)
				}
			}
			assertBootstrapCleanupMarker(t, database, legacyJailBootstrapCleanupMigration, 1)
		})
	}
}

func TestCleanupLegacyJailBootstrapsRefusesUnsafeRecords(t *testing.T) {
	for _, change := range []struct {
		name string
		edit func(*jailModels.JailBootstrap)
	}{
		{"pool root", func(b *jailModels.JailBootstrap) { b.Dataset = b.Pool }},
		{"bootstrap parent", func(b *jailModels.JailBootstrap) { b.Dataset = b.Pool + "/sylve/bootstraps" }},
		{"existing jail", func(b *jailModels.JailBootstrap) { b.Dataset = b.Pool + "/sylve/jails/113" }},
		{"different pool", func(b *jailModels.JailBootstrap) { b.Pool = "backup" }},
		{"option as pool", func(b *jailModels.JailBootstrap) { b.Pool = "-r" }},
		{"nested pool", func(b *jailModels.JailBootstrap) { b.Pool = "tank/child" }},
		{"snapshot", func(b *jailModels.JailBootstrap) { b.Dataset += "@snapshot" }},
		{"different name", func(b *jailModels.JailBootstrap) { b.Name = "../jails/113" }},
		{"different type", func(b *jailModels.JailBootstrap) { b.BootstrapType = "minimal" }},
		{"different version", func(b *jailModels.JailBootstrap) { b.Major = 14 }},
	} {
		t.Run(change.name, func(t *testing.T) {
			database, client, runner := newBootstrapCleanupTest(t)
			record := seedCleanupBootstrap(t, database, runner, "tank", 0, "base", "completed")
			change.edit(&record)
			if err := database.Save(&record).Error; err != nil {
				t.Fatal(err)
			}
			if err := database.First(&record, record.ID).Error; err != nil {
				t.Fatal(err)
			}
			if err := cleanupLegacyJailBootstrapsWithZFS(database, client); err != nil {
				t.Fatal(err)
			}
			if len(runner.calls) != 0 {
				t.Fatalf("unsafe record reached ZFS: %v", runner.calls)
			}
			var stored jailModels.JailBootstrap
			if err := database.First(&stored, record.ID).Error; err != nil || !reflect.DeepEqual(stored, record) {
				t.Fatalf("unsafe record was changed: %#v, error=%v", stored, err)
			}
		})
	}
}

func TestCleanupLegacyJailBootstrapsValidatesResolvedDataset(t *testing.T) {
	for _, change := range []struct {
		name string
		edit func(*gzfs.Dataset)
	}{
		{"name", func(d *gzfs.Dataset) { d.Name = "tank/sylve/jails/113" }},
		{"pool", func(d *gzfs.Dataset) { d.Pool = "backup" }},
		{"type", func(d *gzfs.Dataset) { d.Type = gzfs.DatasetTypeVolume }},
	} {
		t.Run(change.name, func(t *testing.T) {
			database, client, runner := newBootstrapCleanupTest(t)
			record := seedCleanupBootstrap(t, database, runner, "tank", 0, "base", "completed")
			change.edit(runner.datasets[record.Dataset])
			if err := cleanupLegacyJailBootstrapsWithZFS(database, client); err != nil {
				t.Fatal(err)
			}
			for _, args := range runner.calls {
				if args[0] == "destroy" {
					t.Fatalf("mismatched dataset was destroyed: %v", args)
				}
			}
			assertBootstrapCleanupMarker(t, database, legacyJailBootstrapCleanupMigration, 0)
		})
	}
}

func TestCleanupLegacyJailBootstrapsPreparationIsAtomic(t *testing.T) {
	database, client, runner := newBootstrapCleanupTest(t)
	record := seedCleanupBootstrap(t, database, runner, "tank", 0, "base", "completed")
	if err := database.Exec(`CREATE TRIGGER fail_bootstrap_cleanup_start BEFORE INSERT ON migrations
		WHEN NEW.name = '` + legacyJailBootstrapCleanupStarted + `'
		BEGIN SELECT RAISE(ABORT, 'forced marker failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := cleanupLegacyJailBootstrapsWithZFS(database, client); err == nil {
		t.Fatal("expected marker failure")
	}
	if len(runner.calls) != 0 {
		t.Fatal("touched ZFS before target capture committed")
	}
	var stored jailModels.JailBootstrap
	if err := database.First(&stored, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "completed" || stored.Phase != "" {
		t.Fatalf("target changes were not rolled back: %#v", stored)
	}
	assertBootstrapCleanupMarker(t, database, legacyJailBootstrapCleanupStarted, 0)
}

func TestCleanupLegacyJailBootstrapsRecoversAfterRecordDeletionFailure(t *testing.T) {
	database, client, runner := newBootstrapCleanupTest(t)
	record := seedCleanupBootstrap(t, database, runner, "tank", 0, "base", "completed")
	if err := database.Exec(`CREATE TRIGGER fail_bootstrap_cleanup_delete BEFORE DELETE ON jail_bootstraps
		BEGIN SELECT RAISE(ABORT, 'forced delete failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := cleanupLegacyJailBootstrapsWithZFS(database, client); err == nil {
		t.Fatal("expected record deletion failure")
	}
	if runner.datasets[record.Dataset] != nil {
		t.Fatal("fixture did not reach dataset deletion")
	}
	if err := database.Exec("DROP TRIGGER fail_bootstrap_cleanup_delete").Error; err != nil {
		t.Fatal(err)
	}
	if err := cleanupLegacyJailBootstrapsWithZFS(database, client); err != nil {
		t.Fatalf("retry after dataset was already removed: %v", err)
	}
	if err := database.First(&jailModels.JailBootstrap{}, record.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("old record survived: %v", err)
	}
	assertBootstrapCleanupMarker(t, database, legacyJailBootstrapCleanupMigration, 1)
}

func TestCleanupLegacyJailBootstrapsCompletionFailurePreservesReplacements(t *testing.T) {
	database, client, runner := newBootstrapCleanupTest(t)
	seedCleanupBootstrap(t, database, runner, "tank", 0, "base", "completed")
	if err := database.Exec(`CREATE TRIGGER fail_bootstrap_cleanup_completion BEFORE INSERT ON migrations
		WHEN NEW.name = '` + legacyJailBootstrapCleanupMigration + `'
		BEGIN SELECT RAISE(ABORT, 'forced completion failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := cleanupLegacyJailBootstrapsWithZFS(database, client); err == nil {
		t.Fatal("expected completion marker failure")
	}
	assertBootstrapCleanupMarker(t, database, legacyJailBootstrapCleanupStarted, 1)
	assertBootstrapCleanupMarker(t, database, legacyJailBootstrapCleanupMigration, 0)
	replacement := seedCleanupBootstrap(t, database, runner, "tank", 0, "base", "completed")
	if err := database.Exec("DROP TRIGGER fail_bootstrap_cleanup_completion").Error; err != nil {
		t.Fatal(err)
	}
	calls := len(runner.calls)
	if err := cleanupLegacyJailBootstrapsWithZFS(database, client); err != nil {
		t.Fatalf("retry completion: %v", err)
	}
	if len(runner.calls) != calls || runner.datasets[replacement.Dataset] == nil {
		t.Fatal("retry after completion failure touched replacement")
	}
	assertBootstrapCleanupMarker(t, database, legacyJailBootstrapCleanupMigration, 1)
}

func TestCleanupLegacyJailBootstrapsPreservesRestartedBuild(t *testing.T) {
	database, client, runner := newBootstrapCleanupTest(t)
	record := seedCleanupBootstrap(t, database, runner, "tank", 0, "base", "completed")
	runner.failDelete[record.Dataset] = true
	if err := cleanupLegacyJailBootstrapsWithZFS(database, client); err != nil {
		t.Fatal(err)
	}
	delete(runner.datasets, record.Dataset)
	if err := database.Model(&record).Updates(map[string]any{
		"status": "running", "phase": "installing_pkgbase",
	}).Error; err != nil {
		t.Fatal(err)
	}
	runner.add(record.Dataset)
	clear(runner.failDelete)
	calls := len(runner.calls)
	if err := cleanupLegacyJailBootstrapsWithZFS(database, client); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != calls || runner.datasets[record.Dataset] == nil {
		t.Fatal("retry touched a replacement build reusing the old record")
	}
	var stored jailModels.JailBootstrap
	if err := database.First(&stored, record.ID).Error; err != nil || stored.Status != "running" {
		t.Fatalf("replacement build was changed: %#v, error=%v", stored, err)
	}
	assertBootstrapCleanupMarker(t, database, legacyJailBootstrapCleanupMigration, 1)
}
