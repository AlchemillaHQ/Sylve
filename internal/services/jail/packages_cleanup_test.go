// SPDX-License-Identifier: BSD-2-Clause

package jail

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailTypes "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
	"github.com/alchemillahq/sylve/pkg/pkgmanager"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestPackageNotificationsDoNotBlockOtherJails(t *testing.T) {
	for _, change := range []string{"deleted", "reused pending CTID", "pending", "linux", "replaced root", "malformed root", "lookup failure"} {
		t.Run(change, func(t *testing.T) {
			s, jail := newPackageTestService(t)
			other := jailModels.Jail{CTID: jail.CTID + 1, Name: "other", Type: jailModels.JailTypeFreeBSD, Storages: []jailModels.Storage{{Pool: "tank", GUID: "other-root", IsBase: true}}}
			if err := s.DB.Create(&other).Error; err != nil {
				t.Fatal(err)
			}
			for _, target := range []jailModels.Jail{jail, other} {
				if err := s.newPackageEvent(s.DB, &target, target.Storages[0].GUID, notifier.JailPackageHealthKindPrefix, "test", "test", "warning", jailModels.JailPackageOperation{}); err != nil {
					t.Fatal(err)
				}
			}
			switch change {
			case "deleted", "reused pending CTID":
				if err := s.DB.Where("jid = ?", jail.ID).Delete(&jailModels.Storage{}).Error; err != nil {
					t.Fatal(err)
				}
				if err := s.DB.Delete(&jail).Error; err != nil {
					t.Fatal(err)
				}
				if change == "reused pending CTID" {
					if err := s.DB.Create(&jailModels.Jail{CTID: jail.CTID, Name: "replacement", Type: jailModels.JailTypeFreeBSD, CreationPending: true}).Error; err != nil {
						t.Fatal(err)
					}
				}
			case "pending":
				if err := s.DB.Model(&jail).Update("creation_pending", true).Error; err != nil {
					t.Fatal(err)
				}
			case "linux":
				if err := s.DB.Model(&jail).Update("type", jailModels.JailTypeLinux).Error; err != nil {
					t.Fatal(err)
				}
			case "replaced root":
				if err := s.DB.Model(&jailModels.Storage{}).Where("jid = ?", jail.ID).Update("guid", "replacement-root").Error; err != nil {
					t.Fatal(err)
				}
			case "malformed root":
				if err := s.DB.Model(&jailModels.Storage{}).Where("jid = ?", jail.ID).Update("pool", "tank/shared").Error; err != nil {
					t.Fatal(err)
				}
			case "lookup failure":
				if err := s.DB.Callback().Query().After("gorm:query").Register("package-single-lookup-failure", func(tx *gorm.DB) {
					if loaded, ok := tx.Statement.Dest.(*jailModels.Jail); ok && loaded.ID == jail.ID {
						tx.AddError(errors.New("temporary_database_error"))
					}
				}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { s.DB.Callback().Query().Remove("package-single-lookup-failure") })
			}
			emitter := &packageEmitter{calls: map[string]int{}}
			notifier.SetEmitter(emitter)
			t.Cleanup(func() { notifier.SetEmitter(nil) })
			err := s.dispatchPackageEvents(t.Context())
			retry := change == "malformed root" || change == "lookup failure"
			if (err != nil) != retry {
				t.Fatalf("dispatch error=%v, want retry=%t", err, retry)
			}
			var old, current jailModels.JailPackageNotificationEvent
			if err := s.DB.First(&old, "jail_id = ?", jail.ID).Error; err != nil {
				t.Fatal(err)
			}
			if err := s.DB.First(&current, "jail_id = ?", other.ID).Error; err != nil {
				t.Fatal(err)
			}
			if (old.FinishedAt == nil) != retry || old.TargetsLoaded || old.Attempts != 1 || !old.NextAttemptAt.After(time.Now()) {
				t.Fatalf("obsolete/uncertain event was mishandled: %+v", old)
			}
			if !current.TargetsLoaded || len(current.Delivered) != 1 || emitter.calls["ui"] != 1 || emitter.calls["email"] != 1 {
				t.Fatalf("unrelated jail was blocked: %+v: %v", current, emitter.calls)
			}
			if err := s.dispatchPackageEvents(t.Context()); err != nil || emitter.calls["ui"] != 1 || emitter.calls["email"] != 1 {
				t.Fatalf("events ignored backoff: %v: %v", emitter.calls, err)
			}
		})
	}
}

func TestPackageNotificationTargetLookupFailuresBackOffAndExpire(t *testing.T) {
	s, jail := newPackageTestService(t)
	cause := errors.New("target_lookup_unavailable")
	emitter := &packageEmitter{calls: map[string]int{}, targetsError: cause}
	notifier.SetEmitter(emitter)
	t.Cleanup(func() { notifier.SetEmitter(nil) })
	if err := s.newPackageEvent(s.DB, &jail, jail.Storages[0].GUID, notifier.JailPackageHealthKindPrefix, "test", "test", "warning", jailModels.JailPackageOperation{}); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 10; attempt++ {
		before := time.Now()
		if err := s.dispatchPackageEvents(t.Context()); !errors.Is(err, cause) {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		var event jailModels.JailPackageNotificationEvent
		if err := s.DB.First(&event).Error; err != nil {
			t.Fatal(err)
		}
		if event.Attempts != attempt || event.TargetsLoaded || len(event.Delivered) != 0 || len(emitter.calls) != 0 || (event.FinishedAt != nil) != (attempt == 10) || event.NextAttemptAt.Before(before.Add(time.Duration(attempt*attempt)*time.Minute)) {
			t.Fatalf("lookup failure did not use bounded backoff: %+v", event)
		}
		if err := s.DB.Model(&event).Update("next_attempt_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := s.dispatchPackageEvents(t.Context()); err != nil {
		t.Fatalf("exhausted event was retried: %v", err)
	}
}

func TestPackagePruningRemovesOrphanStateButRetainsCurrentStateAndRecentHistory(t *testing.T) {
	s, jail := newPackageTestService(t)
	old := time.Now().UTC().Add(-31 * 24 * time.Hour)
	recent := time.Now().UTC().Add(-time.Hour)
	for _, id := range []uint{jail.ID, jail.ID + 1} {
		if err := s.DB.Create(&jailModels.JailPackageState{JailID: id, CTID: jail.CTID, GUID: "root", Inventory: []pkgmanager.Package{{Name: "app", Version: "1"}}, CreatedAt: old}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, finished := range []time.Time{old, recent} {
		op := jailModels.JailPackageOperation{ID: uuid.NewString(), JailID: jail.ID + 1, CTID: jail.CTID, GUID: "root", Action: "pkg-audit", Phase: "completed", FinishedAt: &finished}
		if err := s.DB.Create(&op).Error; err != nil {
			t.Fatal(err)
		}
		dir, err := s.packageDirectory(op)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "output.log"), []byte("history"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s.prunePackageOperations()
	if got := countJailDeleteRows(t, s.DB, &jailModels.JailPackageState{}, "jail_id = ?", jail.ID); got != 1 {
		t.Fatal("current jail's state was pruned")
	}
	if got := countJailDeleteRows(t, s.DB, &jailModels.JailPackageState{}, "jail_id = ?", jail.ID+1); got != 0 {
		t.Fatal("orphan state survived pruning")
	}
	var operations []jailModels.JailPackageOperation
	if err := s.DB.Find(&operations).Error; err != nil || len(operations) != 1 || !operations[0].FinishedAt.Equal(recent) {
		t.Fatalf("operation retention changed: %+v: %v", operations, err)
	}
	entries, err := os.ReadDir(s.packages.dataPath)
	if err != nil || len(entries) != 1 || entries[0].Name() != operations[0].ID {
		t.Fatalf("operation directory retention changed: %v: %v", entries, err)
	}
}

func TestPackageSnapshotRollbackInvalidatesAllFreshness(t *testing.T) {
	for _, outcome := range []string{"metadata restored", "rollback failed", "invalidation failed"} {
		t.Run(outcome, func(t *testing.T) {
			s, jail, fixture := newPackageReadTestService(t)
			fixture.inventory += "FreeBSD-runtime\t14.3\tbase/runtime\tbase\t1\t0\t0\n"
			inventory, err := pkgmanager.ParseInventory(fixture.inventory)
			if err != nil {
				t.Fatal(err)
			}
			target, _, err := s.packages.resolve(t.Context(), &jail)
			if err != nil {
				t.Fatal(err)
			}
			_, repoHash, err := s.packages.client(target, nil).Repositories(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			digest := pkgmanager.InventoryDigest(inventory)
			state := jailModels.JailPackageState{JailID: jail.ID, CTID: jail.CTID, GUID: jail.Storages[0].GUID, Inventory: inventory, Audit: pkgmanager.Audit{Packages: []pkgmanager.AuditPackage{{Name: "app", Version: "1", Advisories: []pkgmanager.Advisory{{ID: "previous"}}}}}, AuditAt: &now, AuditInventoryHash: pkgmanager.AuditInventoryDigest(inventory), CheckedAt: &now, CheckInventoryHash: digest, SystemCheckedAt: &now, SystemCheckInventoryHash: digest, RepositoriesRefreshedAt: &now, RepositoryHash: repoHash, SystemReposRefreshedAt: &now, SystemRepositoryHash: "base"}
			if err := s.DB.Create(&state).Error; err != nil {
				t.Fatal(err)
			}
			assertInvalidated := func() {
				t.Helper()
				var saved jailModels.JailPackageState
				if err := s.DB.First(&saved, "jail_id = ?", jail.ID).Error; err != nil {
					t.Fatal(err)
				}
				if saved.AuditInventoryHash != "" || saved.CheckInventoryHash != "" || saved.SystemCheckInventoryHash != "" || saved.RepositoriesRefreshedAt != nil || saved.RepositoryHash != "" || saved.SystemReposRefreshedAt != nil || saved.SystemRepositoryHash != "" || saved.NextAuditAt == nil {
					t.Fatalf("snapshot rollback retained freshness: %+v", saved)
				}
				if len(saved.Inventory) != len(inventory) || len(saved.Audit.Packages) != 1 || saved.AuditAt == nil || !saved.AuditAt.Equal(now) {
					t.Fatalf("invalidation deleted recorded results: %+v", saved)
				}
			}
			if outcome == "metadata restored" {
				if err := s.restoreJailDatabaseFromSnapshot(jail.CTID, jail); err != nil {
					t.Fatal(err)
				}
			} else {
				const snapshot = "tank/sylve/jails/900@before"
				injected := errors.New("injected rollback failure")
				runner := &jailSnapshotRunner{datasets: map[string]jailSnapshotRunnerDataset{snapshot: {datasetType: gzfs.DatasetTypeSnapshot}}}
				s.GZFS = gzfs.NewClient(gzfs.Options{Runner: runner})
				rolledBack := false
				runner.rollback = func() error {
					rolledBack = true
					assertInvalidated()
					return injected
				}
				if outcome == "invalidation failed" {
					if err := s.DB.Callback().Update().Before("gorm:update").Register("package-invalidation-failure", func(tx *gorm.DB) {
						if tx.Statement.Table == "jail_package_states" {
							tx.AddError(injected)
						}
					}); err != nil {
						t.Fatal(err)
					}
				}
				if err := s.rollbackJailSnapshotTargets(t.Context(), jail.ID, []string{snapshot}); !errors.Is(err, injected) {
					t.Fatalf("rollback error=%v", err)
				}
				if outcome == "invalidation failed" {
					if rolledBack || len(runner.commands) != 0 {
						t.Fatal("filesystem mutation started despite failed invalidation")
					}
					return
				}
				if !rolledBack {
					t.Fatal("mocked rollback was not reached")
				}
			}
			assertInvalidated()
			view, err := s.GetPackages(t.Context(), jail.CTID)
			if err != nil || !view.AuditStale || view.UpdatesChecked || view.SystemUpdatesChecked {
				t.Fatalf("rollback retained trusted results: %+v: %v", view, err)
			}
			if outcome == "metadata restored" {
				runPackageTestTask(t, s, jail.CTID, "pkg-preview", jailTypes.PackageRequest{All: true})
				if fixture.refreshes != 1 {
					t.Fatal("preview reused a rolled-back repository catalog")
				}
			}
		})
	}
}

func TestPackageSnapshotMetadataRestoreRollsBackOnInvalidationFailure(t *testing.T) {
	s, jail := newPackageTestService(t)
	if err := s.DB.Create(&jailModels.JailPackageState{JailID: jail.ID, CTID: jail.CTID, GUID: jail.Storages[0].GUID, CheckInventoryHash: "previous"}).Error; err != nil {
		t.Fatal(err)
	}
	cause := errors.New("invalidation_failed")
	if err := s.DB.Callback().Update().Before("gorm:update").Register("package-invalidation-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "jail_package_states" {
			tx.AddError(cause)
		}
	}); err != nil {
		t.Fatal(err)
	}
	restored := jail
	restored.Hostname = "snapshot-host"
	if err := s.restoreJailDatabaseFromSnapshot(jail.CTID, restored); !errors.Is(err, cause) {
		t.Fatalf("restore error=%v", err)
	}
	var current jailModels.Jail
	if err := s.DB.First(&current, jail.ID).Error; err != nil || current.Hostname != jail.Hostname {
		t.Fatalf("failed invalidation committed metadata: %+v: %v", current, err)
	}
	var state jailModels.JailPackageState
	if err := s.DB.First(&state, "jail_id = ?", jail.ID).Error; err != nil || state.GUID != jail.Storages[0].GUID || state.CheckInventoryHash != "previous" {
		t.Fatalf("failed restore did not roll back package state: %+v: %v", state, err)
	}
}
