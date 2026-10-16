// SPDX-License-Identifier: BSD-2-Clause

package jail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	jailTypes "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/pkgmanager"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func newPackageTestService(t *testing.T) (*Service, jailModels.Jail) {
	t.Helper()
	database := testutil.NewSQLiteTestDB(t,
		&jailModels.Jail{},
		&jailModels.Storage{},
		&jailModels.JailHooks{},
		&jailModels.Network{},
		&jailModels.JailSnapshot{},
		&taskModels.GuestLifecycleTask{},
		&jailModels.JailPackageState{},
		&jailModels.JailPackagePlan{},
		&jailModels.JailPackageOperation{},
		&jailModels.JailPackageNotificationEvent{},
	)
	jail := jailModels.Jail{CTID: 900, Name: "package-fixture", Type: jailModels.JailTypeFreeBSD, Storages: []jailModels.Storage{{Pool: "tank", GUID: "123456789", IsBase: true}}}
	if err := database.Create(&jail).Error; err != nil {
		t.Fatal(err)
	}
	s := &Service{
		DB: database,
		packages: &packageRuntime{
			admission: map[uint]*packageAdmission{},
			observers: map[string]bool{},
			dataPath:  t.TempDir(),
			slots:     make(chan struct{}, 2),
		},
	}
	s.packages.resolve = func(context.Context, *jailModels.Jail) (pkgmanager.Target, bool, error) {
		return pkgmanager.Target{JID: 4, Name: "fixture", Root: "/fixture"}, true, nil
	}
	s.packages.request = func(ctx context.Context, ctID uint, action, payload, source, actor string) (uint, error) {
		task := taskModels.GuestLifecycleTask{GuestType: "jail", GuestID: ctID, Action: action, Status: "queued", Source: source, Payload: payload, RequestedBy: actor}
		if err := database.Create(&task).Error; err != nil {
			return 0, err
		}
		return task.ID, s.PreparePackageTask(ctx, task.ID, ctID, action, payload)
	}
	s.packages.client = func(target pkgmanager.Target, log io.Writer) *pkgmanager.Client {
		return &pkgmanager.Client{Target: target, Log: log, RunCommand: func(context.Context, []string) (pkgmanager.Result, error) {
			t.Error("unexpected command")
			return pkgmanager.Result{}, fmt.Errorf("unexpected_command")
		}}
	}
	return s, jail
}

func runPackageTestTask(t *testing.T, s *Service, ctID uint, action string, request jailTypes.PackageRequest) jailTypes.PackageJob {
	t.Helper()
	job, err := s.StartPackageOperation(t.Context(), ctID, action, request, "user", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ExecutePackageTask(t.Context(), job.TaskID); err != nil {
		t.Fatal(err)
	}
	job, err = s.GetPackageJob(t.Context(), ctID, job.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestPackageTypeGatesEveryServiceEntry(t *testing.T) {
	for _, kind := range []string{"linux", ""} {
		t.Run("type_"+kind, func(t *testing.T) {
			s, jail := newPackageTestService(t)
			if err := s.DB.Model(&jail).Update("type", kind).Error; err != nil {
				t.Fatal(err)
			}
			s.packages.resolve = func(context.Context, *jailModels.Jail) (pkgmanager.Target, bool, error) {
				t.Fatal("resolved unsupported target")
				return pkgmanager.Target{}, false, nil
			}
			checks := []func() error{
				func() error { _, err := s.GetPackages(t.Context(), jail.CTID); return err },
				func() error {
					_, err := s.StartPackageOperation(t.Context(), jail.CTID, "pkg-audit", jailTypes.PackageRequest{}, "user", "test")
					return err
				},
				func() error { _, err := s.GetPackageMonitoring(t.Context(), jail.CTID); return err },
				func() error {
					_, err := s.SetPackageMonitoring(t.Context(), jail.CTID, jailTypes.PackageMonitoring{Enabled: true, IntervalHours: 24})
					return err
				},
				func() error { _, err := s.GetPackageJob(t.Context(), jail.CTID, 1); return err },
				func() error { _, err := s.PackageLogs(t.Context(), jail.CTID, 1, 0); return err },
				func() error { _, err := s.GetPackageDetails(t.Context(), jail.CTID, "app"); return err },
			}
			for i, check := range checks {
				if err := check(); err == nil || !strings.Contains(err.Error(), "unsupported_jail_type") {
					t.Fatalf("check %d: %v", i, err)
				}
			}
		})
	}
}

func TestPackageAdmissionCoversLifecycleAndStorage(t *testing.T) {
	s, jail := newPackageTestService(t)
	release, err := s.ReserveSystemOperation(t.Context(), jail.CTID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartPackageOperation(t.Context(), jail.CTID, "pkg-audit", jailTypes.PackageRequest{}, "user", "test"); err == nil {
		t.Fatal("package admitted during ordinary mutation")
	}
	release()
	job, err := s.StartPackageOperation(t.Context(), jail.CTID, "pkg-audit", jailTypes.PackageRequest{}, "user", "test")
	if err != nil {
		t.Fatal(err)
	}
	checks := []func() error{
		func() error { return s.JailActionContext(t.Context(), int(jail.CTID), "stop") },
		func() error { _, err := s.CreateJailSnapshot(t.Context(), jail.CTID, "test", ""); return err },
		func() error { _, err := s.RollbackJailSnapshot(t.Context(), jail.CTID, 1); return err },
		func() error { return s.DeleteJailSnapshot(t.Context(), jail.CTID, 1) },
		func() error { return s.ConvertJailToTemplate(t.Context(), jail.CTID, ConvertToTemplateRequest{}) },
		func() error { return s.DeleteJail(t.Context(), jail.CTID, false, true) },
		func() error { return s.RetireJailLocalMetadata(t.Context(), jail.CTID, false) },
		func() error { return s.ModifyFstab(jail.CTID, "") },
		func() error { _, err := s.AddNetwork(jail.CTID, jailTypes.AddJailNetworkRequest{}); return err },
	}
	for i, check := range checks {
		if err := check(); err == nil || !strings.Contains(err.Error(), "package_operation_in_progress") {
			t.Fatalf("check %d: %v", i, err)
		}
	}
	owner := context.WithValue(t.Context(), packageOwnerKey{}, job.OperationID)
	release, err = s.ReserveSystemOperation(owner, jail.CTID)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestPackagePlansBoundToIdentitySingleUseAndAcknowledgement(t *testing.T) {
	s, jail := newPackageTestService(t)
	plan := jailModels.JailPackagePlan{ID: uuid.NewString(), JailID: jail.ID, GUID: jail.Storages[0].GUID, ExpiresAt: time.Now().Add(time.Minute), Plan: pkgmanager.Plan{Changes: []pkgmanager.Change{{Action: "remove", Name: "old"}}}}
	if err := s.DB.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPackageOperation(t.Context(), jail.CTID, "pkg-upgrade", jailTypes.PackageRequest{PlanID: plan.ID}, "user", "test"); err == nil || !strings.Contains(err.Error(), "acknowledgement") {
		t.Fatal(err)
	}
	job, err := s.StartPackageOperation(t.Context(), jail.CTID, "pkg-upgrade", jailTypes.PackageRequest{PlanID: plan.ID, Acknowledge: true}, "user", "test")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := s.StartPackageOperation(t.Context(), jail.CTID, "pkg-upgrade", jailTypes.PackageRequest{PlanID: plan.ID, Acknowledge: true}, "user", "test")
	if err != nil || job.TaskID != replayed.TaskID {
		t.Fatalf("duplicate %+v %v", replayed, err)
	}
	if err = s.DB.Model(&jailModels.Storage{}).Where("jid = ?", jail.ID).Update("guid", "987654321").Error; err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetPackageJob(t.Context(), jail.CTID, job.TaskID); err == nil {
		t.Fatal("old incarnation job accessible")
	}
	if _, err = s.StartPackageOperation(t.Context(), jail.CTID, "pkg-upgrade", jailTypes.PackageRequest{PlanID: plan.ID, Acknowledge: true}, "user", "test"); err == nil {
		t.Fatal("old incarnation plan accessible")
	}
}

func TestPackageStateChangesAndRecurrence(t *testing.T) {
	s, jail := newPackageTestService(t)
	guid := jail.Storages[0].GUID
	state, err := s.packageState(&jail, guid)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.savePackageInventory(state); err != nil {
		t.Fatal(err)
	}
	inventory := []pkgmanager.Package{{Name: "app", Version: "1"}}
	report := pkgmanager.Audit{Packages: []pkgmanager.AuditPackage{{Name: "app", Version: "1", Advisories: []pkgmanager.Advisory{{ID: "one"}, {ID: "two"}}}}}
	apply := func(report pkgmanager.Audit) {
		t.Helper()
		if err := s.persistPackageAudit(&jail, guid, inventory, report, jailModels.JailPackageOperation{}); err != nil {
			t.Fatal(err)
		}
	}
	count := func(want int64) {
		t.Helper()
		var got int64
		if err := s.DB.Model(&jailModels.JailPackageNotificationEvent{}).Count(&got).Error; err != nil || got != want {
			t.Fatalf("events %d want %d: %v", got, want, err)
		}
	}
	apply(report)
	count(1)
	report.Packages[0].Advisories[0], report.Packages[0].Advisories[1] = report.Packages[0].Advisories[1], report.Packages[0].Advisories[0]
	apply(report)
	count(1)
	report.Packages[0].Advisories = []pkgmanager.Advisory{{ID: "two"}, {ID: "three"}}
	apply(report)
	count(2)
	for range 4 {
		if err = s.recordPackageAuditFailure(&jail, guid, errors.New("feed_failed")); err != nil {
			t.Fatal(err)
		}
	}
	count(3)
	saved, err := s.packageState(&jail, guid)
	if err != nil || len(saved.Audit.Packages) != 1 || len(saved.Audit.Packages[0].Advisories) != 2 || saved.Audit.Packages[0].Advisories[0].ID != "two" || saved.Audit.Packages[0].Advisories[1].ID != "three" {
		t.Fatal("failure changed findings", saved.Audit, err)
	}
	apply(pkgmanager.Audit{Packages: []pkgmanager.AuditPackage{}})
	count(5)
	report.Packages[0].Advisories = []pkgmanager.Advisory{{ID: "two"}}
	apply(report)
	count(6)
	var events []jailModels.JailPackageNotificationEvent
	if err = s.DB.Where("kind LIKE ?", notifier.JailPackageVulnerabilitiesKindPrefix+"%").Order("created_at ASC").Find(&events).Error; err != nil || len(events) != 4 {
		t.Fatalf("recurrence events %+v %v", events, err)
	}
	if events[0].ID == events[3].ID || !strings.Contains(events[3].Body, "1 newly observed") {
		t.Fatalf("recurrence did not create an independent alert: %+v", events[3])
	}
}

func TestPackageInventorySavePreservesMonitoringAndResetsIncarnation(t *testing.T) {
	s, jail := newPackageTestService(t)
	state, err := s.packageState(&jail, jail.Storages[0].GUID)
	if err != nil {
		t.Fatal(err)
	}
	state.AuditError = "previous_feed_failure"
	state.FailureCount = 2
	if err = s.savePackageInventory(state); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPackageMonitoring(t.Context(), jail.CTID, jailTypes.PackageMonitoring{Enabled: true, IntervalHours: 6}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	state.Inventory = []pkgmanager.Package{{Name: "app", Version: "2"}}
	state.InventoryAt, state.LastAttemptAt = &now, &now
	if err = s.savePackageInventory(state); err != nil {
		t.Fatal(err)
	}
	saved, err := s.packageState(&jail, state.GUID)
	if err != nil {
		t.Fatal(err)
	}
	monitor, err := s.GetPackageMonitoring(t.Context(), jail.CTID)
	if err != nil || !monitor.Enabled || monitor.IntervalHours != 6 || saved.NextAuditAt == nil || saved.AuditError != state.AuditError || saved.FailureCount != 2 {
		t.Fatalf("inventory scan overwrote monitoring/audit state: %+v", saved)
	}
	if len(saved.Inventory) != 1 || saved.Inventory[0].Version != "2" || saved.InventoryAt == nil || !saved.InventoryAt.Equal(now) || saved.LastAttemptAt == nil || !saved.LastAttemptAt.Equal(now) {
		t.Fatalf("inventory scan was not saved: %+v", saved)
	}
	if err = s.DB.Model(&jailModels.Storage{}).Where("jid = ?", jail.ID).Update("guid", "replacement-guid").Error; err != nil {
		t.Fatal(err)
	}
	current, guid, err := s.packageJail(t.Context(), jail.CTID)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := s.packageState(current, guid)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.savePackageInventory(replacement); err != nil {
		t.Fatal(err)
	}
	saved = jailModels.JailPackageState{}
	if err = s.DB.First(&saved, "jail_id = ?", jail.ID).Error; err != nil {
		t.Fatal(err)
	}
	monitor, err = s.GetPackageMonitoring(t.Context(), jail.CTID)
	if err != nil || saved.GUID != guid || len(saved.Inventory) != 0 || saved.InventoryAt != nil || saved.AuditError != "" || saved.FailureCount != 0 || !monitor.Enabled || monitor.IntervalHours != 6 {
		t.Fatalf("replacement retained old incarnation state: %+v", saved)
	}
}

func TestPackageInventorySaveReturnsLookupErrors(t *testing.T) {
	s, jail := newPackageTestService(t)
	cause := errors.New("temporary_database_error")
	if err := s.DB.Callback().Query().Before("gorm:query").Register("package-test-state-lookup-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "jail_package_states" {
			tx.AddError(cause)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.savePackageInventory(jailModels.JailPackageState{JailID: jail.ID, GUID: jail.Storages[0].GUID}); !errors.Is(err, cause) {
		t.Fatalf("expected lookup failure, got %v", err)
	}
}

type packageEmitter struct {
	calls        map[string]int
	targetsError error
}

func (e *packageEmitter) Emit(context.Context, notifier.EventInput) (notifier.EmitResult, error) {
	return notifier.EmitResult{}, nil
}
func (e *packageEmitter) DeliveryTargets(context.Context, notifier.EventInput) ([]string, error) {
	return []string{"ui", "email"}, e.targetsError
}
func (e *packageEmitter) EmitTarget(_ context.Context, _ notifier.EventInput, target string) (notifier.EmitResult, error) {
	e.calls[target]++
	if target == "email" && e.calls[target] == 1 {
		return notifier.EmitResult{}, errors.New("temporary")
	}
	return notifier.EmitResult{}, nil
}

func TestPackageNotificationRetriesOnlyFailedDestination(t *testing.T) {
	s, jail := newPackageTestService(t)
	emitter := &packageEmitter{calls: map[string]int{}}
	notifier.SetEmitter(emitter)
	t.Cleanup(func() { notifier.SetEmitter(nil) })
	if err := s.newPackageEvent(s.DB, &jail, jail.Storages[0].GUID, notifier.JailPackageHealthKindPrefix, "test", "test", "warning", jailModels.JailPackageOperation{}); err != nil {
		t.Fatal(err)
	}
	if err := s.dispatchPackageEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	var pending jailModels.JailPackageNotificationEvent
	if err := s.DB.First(&pending).Error; err != nil {
		t.Fatal(err)
	}
	if len(pending.Targets) != 2 || len(pending.Delivered) != 1 || pending.FinishedAt != nil {
		t.Fatalf("pending event %+v", pending)
	}
	if err := s.DB.Model(&jailModels.JailPackageNotificationEvent{}).Where("finished_at IS NULL").Update("next_attempt_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.dispatchPackageEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	if emitter.calls["ui"] != 1 || emitter.calls["email"] != 2 {
		t.Fatal(emitter.calls)
	}
}

func TestPackageSchedulerAuditsOfflineAndSkipsUnmountedWithoutStarting(t *testing.T) {
	s, jail := newPackageTestService(t)
	s.packages.resolve = func(context.Context, *jailModels.Jail) (pkgmanager.Target, bool, error) {
		return pkgmanager.Target{Name: "fixture", Root: "/fixture", Dataset: "tank/jail", GUID: jail.Storages[0].GUID, Offline: true}, false, nil
	}
	if _, err := s.SetPackageMonitoring(t.Context(), jail.CTID, jailTypes.PackageMonitoring{Enabled: true, IntervalHours: 6}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.runPackageSchedules(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	s.DB.Model(&taskModels.GuestLifecycleTask{}).Count(&count)
	if count != 1 {
		t.Fatalf("scheduled tasks %d", count)
	}
	s2, jail2 := newPackageTestService(t)
	s2.packages.resolve = func(context.Context, *jailModels.Jail) (pkgmanager.Target, bool, error) {
		return pkgmanager.Target{}, false, errors.New("package_root_not_mounted")
	}
	if _, err := s2.SetPackageMonitoring(t.Context(), jail2.CTID, jailTypes.PackageMonitoring{Enabled: true, IntervalHours: 24}); err != nil {
		t.Fatal(err)
	}
	if err := s2.runPackageSchedules(t.Context()); err != nil {
		t.Fatal(err)
	}
	state, err := s2.packageState(&jail2, jail2.Storages[0].GUID)
	if err != nil || state.SkipReason != "package_root_not_mounted" || state.FailureCount != 0 {
		t.Fatalf("skip %+v %v", state, err)
	}
}

func TestOfflinePackageInventoryAuditChecksAndPreviews(t *testing.T) {
	for _, action := range []string{"pkg-audit", "pkg-check-updates", "pkg-preview"} {
		t.Run(action, func(t *testing.T) {
			s, jail, fixture := newPackageReadTestService(t)
			fixture.offline = true
			view, err := s.GetPackages(t.Context(), jail.CTID)
			if err != nil || !view.Ready || !view.Offline || view.SnapshotSupported || len(view.Packages) != 1 {
				t.Fatalf("offline inventory %+v: %v", view, err)
			}
			details, err := s.GetPackageDetails(t.Context(), jail.CTID, "app")
			if err != nil || !details.DependenciesAvailable {
				t.Fatalf("offline details %+v: %v", details, err)
			}
			job := runPackageTestTask(t, s, jail.CTID, action, jailTypes.PackageRequest{All: true})
			if job.Phase != "completed" {
				t.Fatalf("offline operation %+v", job)
			}
			view, err = s.GetPackages(t.Context(), jail.CTID)
			if err != nil || !view.Ready || !view.Offline {
				t.Fatalf("offline result %+v: %v", view, err)
			}
			switch action {
			case "pkg-audit":
				if view.AuditAt == nil || view.AuditStale {
					t.Fatal("fresh offline audit marked stale")
				}
			case "pkg-check-updates":
				if !view.UpdatesChecked || view.Packages[0].Available != "2" {
					t.Fatal("offline update check not retained", view)
				}
			case "pkg-preview":
				if job.Plan == nil {
					t.Fatal("offline preview not retained")
				}
			}
		})
	}
}

func TestPackageEmptyPreviewsAndChecksCompleteWithoutRepositories(t *testing.T) {
	for _, action := range []string{"pkg-preview", "pkg-check-updates"} {
		for _, test := range []struct {
			name      string
			inventory string
		}{
			{name: "empty"},
			{name: "protected_and_locked", inventory: "FreeBSD-runtime\t15.1\tbase/FreeBSD-runtime\tFreeBSD-base\t1\t1\t0\npkg\t2.6.2\tports-mgmt/pkg\tFreeBSD-base\t1\t0\t0\napp\t1\tmisc/app\tports\t1\t0\t1\n"},
		} {
			t.Run(action+"/"+test.name, func(t *testing.T) {
				s, jail := newPackageTestService(t)
				s.packages.client = func(target pkgmanager.Target, log io.Writer) *pkgmanager.Client {
					return &pkgmanager.Client{Target: target, Log: log, RunCommand: func(_ context.Context, args []string) (pkgmanager.Result, error) {
						switch args[len(args)-1] {
						case "-N":
							return pkgmanager.Result{}, nil
						case pkgmanager.InventoryFormat:
							return pkgmanager.Result{Stdout: test.inventory}, nil
						default:
							t.Fatalf("empty result accessed repositories or attempted a transaction: %v", args)
							return pkgmanager.Result{}, nil
						}
					}}
				}
				job := runPackageTestTask(t, s, jail.CTID, action, jailTypes.PackageRequest{All: true})
				var operation jailModels.JailPackageOperation
				if err := s.DB.First(&operation, "id = ?", job.OperationID).Error; err != nil {
					t.Fatal(err)
				}
				if operation.Phase != "completed" || operation.Error != "" || operation.ActiveJailID != nil || operation.FinishedAt == nil || operation.PlanID != "" {
					t.Fatalf("empty operation did not complete normally: %+v", operation)
				}
				if action == "pkg-preview" && operation.Result != "No eligible application packages to upgrade" {
					t.Fatalf("missing empty preview result: %+v", operation)
				}
				if action == "pkg-check-updates" {
					view, err := s.GetPackages(t.Context(), jail.CTID)
					if err != nil || !view.UpdatesChecked || view.CheckError != "" {
						t.Fatalf("empty update check not retained: %+v: %v", view, err)
					}
				}
			})
		}
	}
}

func TestPackageUpgradeRequiresRunningJailAtAdmissionAndExecution(t *testing.T) {
	for _, stopAfterQueue := range []bool{false, true} {
		t.Run(fmt.Sprintf("stop_after_queue_%v", stopAfterQueue), func(t *testing.T) {
			s, jail := newPackageTestService(t)
			plan := jailModels.JailPackagePlan{ID: uuid.NewString(), JailID: jail.ID, GUID: jail.Storages[0].GUID, ExpiresAt: time.Now().Add(time.Minute), Plan: pkgmanager.Plan{Changes: []pkgmanager.Change{{Action: "upgrade", Name: "app", From: "1", To: "2"}}}}
			if err := s.DB.Create(&plan).Error; err != nil {
				t.Fatal(err)
			}
			online := stopAfterQueue
			s.packages.resolve = func(context.Context, *jailModels.Jail) (pkgmanager.Target, bool, error) {
				if online {
					return pkgmanager.Target{JID: 4, Name: "fixture", Root: "/fixture"}, true, nil
				}
				return pkgmanager.Target{Name: "fixture", Root: "/fixture", Dataset: "tank/jail", GUID: jail.Storages[0].GUID, Offline: true}, false, nil
			}
			job, err := s.StartPackageOperation(t.Context(), jail.CTID, "pkg-upgrade", jailTypes.PackageRequest{PlanID: plan.ID, Snapshot: true}, "user", "test")
			if stopAfterQueue {
				if err != nil {
					t.Fatal(err)
				}
				online = false
				err = s.ExecutePackageTask(t.Context(), job.TaskID)
			} else {
				if readErr := s.DB.First(&plan, "id = ?", plan.ID).Error; readErr != nil || plan.AppliedOperationID != "" {
					t.Fatalf("offline admission consumed preview %+v: %v", plan, readErr)
				}
			}
			if err == nil || err.Error() != "jail_not_running" {
				t.Fatalf("offline upgrade not rejected: %v", err)
			}
		})
	}
}

func TestPackageRecoveryUsesLockNotReusedPID(t *testing.T) {
	dir := t.TempDir()
	file, err := os.OpenFile(filepath.Join(dir, "worker.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if result, live, err := packageWorkerResult(dir); err != nil || !live || result != nil {
		t.Fatalf("locked result %+v %v %v", result, live, err)
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if result, live, err := packageWorkerResult(dir); err != nil || live || result == nil || !strings.Contains(result.Error, "interrupted") {
		t.Fatalf("unknown result %+v %v %v", result, live, err)
	}
	if err = os.WriteFile(filepath.Join(dir, "result.json"), []byte(`{"error":""}`), 0600); err != nil {
		t.Fatal(err)
	}
	if result, live, err := packageWorkerResult(dir); err != nil || live || result.Error != "" {
		t.Fatalf("completed %+v %v %v", result, live, err)
	}
}

func TestPackageStartupReconcilesTerminalOperationWithoutReplay(t *testing.T) {
	s, jail := newPackageTestService(t)
	job, err := s.StartPackageOperation(t.Context(), jail.CTID, "pkg-audit", jailTypes.PackageRequest{}, "user", "tester")
	if err != nil {
		t.Fatal(err)
	}
	var operation jailModels.JailPackageOperation
	if err = s.DB.First(&operation, "id = ?", job.OperationID).Error; err != nil {
		t.Fatal(err)
	}
	s.finishPackageOperation(operation, nil)
	if err = s.RecoverPackageTasks(t.Context()); err != nil {
		t.Fatal(err)
	}
	var task taskModels.GuestLifecycleTask
	if err = s.DB.First(&task, job.TaskID).Error; err != nil || task.Status != "success" {
		t.Fatalf("task %+v %v", task, err)
	}
}

func TestEligiblePackageSelectionNeverSilentlyDropsRows(t *testing.T) {
	packages := []pkgmanager.Package{{Name: "app"}, {Name: "locked", Locked: true}, {Name: "base", Protected: true}}
	for _, selection := range [][]string{{"app", "locked"}, {"app", "base"}, {"missing"}, {"app", "app"}} {
		if _, err := eligiblePackageNames(packages, selection, false, pkgmanager.ScopeApplications); err == nil {
			t.Fatal(selection)
		}
	}
	all, err := eligiblePackageNames(packages, nil, true, pkgmanager.ScopeApplications)
	if err != nil || len(all) != 1 || all[0] != "app" {
		t.Fatal(all, err)
	}
	all, err = eligiblePackageNames(packages[1:], nil, true, pkgmanager.ScopeApplications)
	if err != nil || len(all) != 0 {
		t.Fatalf("empty eligible selection should not be an error: %v: %v", all, err)
	}
}

func TestPackageJobAllowsPrunedPlansButReturnsLookupErrors(t *testing.T) {
	s, jail := newPackageTestService(t)
	job, err := s.StartPackageOperation(t.Context(), jail.CTID, "pkg-preview", jailTypes.PackageRequest{All: true}, "user", "tester")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DB.Model(&jailModels.JailPackageOperation{}).Where("id = ?", job.OperationID).Update("plan_id", uuid.NewString()).Error; err != nil {
		t.Fatal(err)
	}
	if job, err = s.GetPackageJob(t.Context(), jail.CTID, job.TaskID); err != nil || job.Plan != nil {
		t.Fatalf("pruned plan prevented job access: %+v: %v", job, err)
	}
	cause := errors.New("temporary_database_error")
	if err := s.DB.Callback().Query().Before("gorm:query").Register("package-plan-lookup-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "jail_package_plans" {
			tx.AddError(cause)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetPackageJob(t.Context(), jail.CTID, job.TaskID); !errors.Is(err, cause) {
		t.Fatalf("plan lookup error was hidden: %v", err)
	}
}

func TestPackageAuditFreshnessUsesMonitoringCadence(t *testing.T) {
	s, jail, fixture := newPackageReadTestService(t)
	inventory, err := pkgmanager.ParseInventory(fixture.inventory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPackageMonitoring(t.Context(), jail.CTID, jailTypes.PackageMonitoring{Enabled: true, IntervalHours: 6}); err != nil {
		t.Fatal(err)
	}
	state, err := s.packageState(&jail, jail.Storages[0].GUID)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-8 * time.Hour)
	state.Inventory = inventory
	state.AuditAt = &old
	state.AuditInventoryHash = pkgmanager.AuditInventoryDigest(inventory)
	if err = s.DB.Save(&state).Error; err != nil {
		t.Fatal(err)
	}
	view, err := s.GetPackages(t.Context(), jail.CTID)
	if err != nil || !view.AuditStale {
		t.Fatalf("overdue six-hour audit %+v %v", view, err)
	}
	if _, err = s.SetPackageMonitoring(t.Context(), jail.CTID, jailTypes.PackageMonitoring{Enabled: true, IntervalHours: 24}); err != nil {
		t.Fatal(err)
	}
	view, err = s.GetPackages(t.Context(), jail.CTID)
	if err != nil || view.AuditStale {
		t.Fatalf("current daily audit %+v %v", view, err)
	}
}

type packageReadFixture struct {
	inventory    string
	repositories string
	refreshError error
	offline      bool
	refreshes    int
	solves       int
	fileQueries  int
	fetches      int
	root         string
}

func newPackageReadTestService(t *testing.T) (*Service, jailModels.Jail, *packageReadFixture) {
	t.Helper()
	s, jail := newPackageTestService(t)
	fixture := &packageReadFixture{
		inventory:    "app\t1\tmisc/app\tports\t1\t0\t0\n",
		repositories: "Repositories:\nports: {\nurl: \"https://example.invalid/ports\",\nenabled: yes,\n}\n",
		root:         t.TempDir(),
	}
	for _, name := range []string{"var/db/pkg/repos/ports/db", "var/db/pkg/vuln.xml"} {
		file := filepath.Join(fixture.root, name)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s.packages.resolve = func(context.Context, *jailModels.Jail) (pkgmanager.Target, bool, error) {
		target := pkgmanager.Target{JID: 4, Name: "fixture", Root: fixture.root}
		if fixture.offline {
			target.JID, target.Offline, target.Dataset, target.GUID = 0, true, "tank/jail", jail.Storages[0].GUID
		}
		return target, !fixture.offline, nil
	}
	s.packages.client = func(target pkgmanager.Target, log io.Writer) *pkgmanager.Client {
		return &pkgmanager.Client{Target: target, Log: log, RunCommand: func(_ context.Context, args []string) (pkgmanager.Result, error) {
			if fixture.offline && (args[0] != "-c" || args[1] != fixture.root) {
				t.Fatalf("not using the offline root: %v", args)
			}
			switch {
			case args[len(args)-1] == "-N":
				return pkgmanager.Result{}, nil
			case args[len(args)-1] == "-vv":
				return pkgmanager.Result{Stdout: fixture.repositories}, nil
			case slices.Contains(args, "update"):
				fixture.refreshes++
				if fixture.refreshError != nil {
					return pkgmanager.Result{}, fixture.refreshError
				}
				return pkgmanager.Result{}, os.WriteFile(filepath.Join(fixture.root, "var/db/pkg/repos/ports/db"), []byte("fixture"), 0600)
			case slices.Contains(args, "upgrade"):
				if !slices.Contains(args, "-n") {
					t.Fatal("attempted installation")
				}
				fixture.solves++
				return pkgmanager.Result{Stdout: "The following 1 package(s) will be affected (of 1 checked):\nInstalled packages to be UPGRADED:\n\tapp: 1 -> 2 [ports]\nNumber of packages to be upgraded: 1\n"}, nil
			case slices.Contains(args, "search"):
				return pkgmanager.Result{Stdout: `[{"name":"app","version":"2","repopath":"All/app-2.pkg"}]`}, nil
			case slices.Contains(args, "fetch"):
				fixture.fetches++
				return pkgmanager.Result{}, nil
			case slices.Contains(args, "audit"):
				return pkgmanager.Result{Stdout: `{"pkg_count":0,"packages":null}`}, nil
			}
			if q := slices.Index(args, "query"); q >= 0 {
				switch {
				case args[q+1] == "-a":
					return pkgmanager.Result{Stdout: fixture.inventory}, nil
				case slices.Contains(args, "%Fp"):
					fixture.fileQueries++
					if target.Offline {
						return pkgmanager.Result{}, nil
					}
					return pkgmanager.Result{Stdout: "/usr/local/bin/app\n"}, nil
				case args[q+1] == "-F":
					return pkgmanager.Result{Stdout: "app\t2\tmisc/app\n"}, nil
				case args[q+1] == "%dn\t%dv\t%do":
					return pkgmanager.Result{Stdout: "library\t1\tdevel/library\n"}, nil
				}
			}
			t.Fatalf("unexpected command: %v", args)
			return pkgmanager.Result{}, nil
		}}
	}
	return s, jail, fixture
}

func TestPackageDetailsIncludeDependencies(t *testing.T) {
	s, jail, _ := newPackageReadTestService(t)
	details, err := s.GetPackageDetails(t.Context(), jail.CTID, "app")
	want := []pkgmanager.Dependency{{Name: "library", Version: "1", Origin: "devel/library"}}
	if err != nil || details.Package.Name != "app" || !details.DependenciesAvailable || !slices.Equal(details.Dependencies, want) {
		t.Fatalf("details=%+v: %v", details, err)
	}
}

func TestPackageAuditFreshnessIgnoresMetadataAndReadOnlyWork(t *testing.T) {
	for _, test := range []struct {
		name        string
		action      string
		inventory   string
		auditError  string
		unavailable bool
		stale       bool
	}{
		{name: "unchanged"},
		{name: "metadata", inventory: "app\t1\tother/origin\tother-repo\t100\t1\t1\n"},
		{name: "preview", action: "pkg-preview"},
		{name: "changed version", inventory: "app\t2\tmisc/app\tports\t1\t0\t0\n", stale: true},
		{name: "removed package", inventory: "\n", stale: true},
		{name: "added package", inventory: "app\t1\tmisc/app\tports\t1\t0\t0\nnew\t1\tmisc/new\tports\t1\t0\t0\n", stale: true},
		{name: "previous error", auditError: "package_audit_failed", stale: true},
		{name: "unavailable", unavailable: true, stale: true},
		{name: "upgrade", action: "pkg-upgrade", stale: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, jail, fixture := newPackageReadTestService(t)
			inventory, err := pkgmanager.ParseInventory(fixture.inventory)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			state := jailModels.JailPackageState{JailID: jail.ID, CTID: jail.CTID, GUID: jail.Storages[0].GUID, Inventory: inventory, AuditAt: &now, AuditInventoryHash: pkgmanager.AuditInventoryDigest(inventory), AuditError: test.auditError}
			if err = s.DB.Create(&state).Error; err != nil {
				t.Fatal(err)
			}
			if test.inventory != "" {
				fixture.inventory = test.inventory
			}
			if test.action != "" {
				request := jailTypes.PackageRequest{All: true}
				if test.action == "pkg-upgrade" {
					plan := jailModels.JailPackagePlan{ID: uuid.NewString(), JailID: jail.ID, GUID: state.GUID, ExpiresAt: now.Add(time.Minute), Plan: pkgmanager.Plan{Changes: []pkgmanager.Change{{Action: "upgrade", Name: "app", From: "1", To: "2"}}}}
					if err = s.DB.Create(&plan).Error; err != nil {
						t.Fatal(err)
					}
					request.PlanID = plan.ID
				}
				if _, err = s.StartPackageOperation(t.Context(), jail.CTID, test.action, request, "user", "test"); err != nil {
					t.Fatal(err)
				}
			}
			if test.unavailable {
				s.packages.resolve = func(context.Context, *jailModels.Jail) (pkgmanager.Target, bool, error) {
					return pkgmanager.Target{}, false, errors.New("package_root_not_mounted")
				}
			}
			view, err := s.GetPackages(t.Context(), jail.CTID)
			if err != nil || view.AuditStale != test.stale {
				t.Fatalf("stale=%v, expected %v: %+v: %v", view.AuditStale, test.stale, view, err)
			}
		})
	}
}

func TestPackageExistingAuditIdentityIsNarrowedBeforeInventoryChanges(t *testing.T) {
	s, jail, fixture := newPackageReadTestService(t)
	inventory, err := pkgmanager.ParseInventory(fixture.inventory)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	state := jailModels.JailPackageState{JailID: jail.ID, CTID: jail.CTID, GUID: jail.Storages[0].GUID, Inventory: inventory, AuditAt: &now, AuditInventoryHash: pkgmanager.InventoryDigest(inventory)}
	if err = s.DB.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	fixture.inventory = "app\t1\tmisc/app\tports\t200\t1\t0\n"
	runPackageTestTask(t, s, jail.CTID, "pkg-check-updates", jailTypes.PackageRequest{})
	view, err := s.GetPackages(t.Context(), jail.CTID)
	if err != nil || view.AuditStale {
		t.Fatalf("unchanged audited packages lost freshness: %+v: %v", view, err)
	}
	if err = s.DB.Model(&state).Update("audit_inventory_hash", "").Error; err != nil {
		t.Fatal(err)
	}
	view, err = s.GetPackages(t.Context(), jail.CTID)
	if err != nil || !view.AuditStale {
		t.Fatalf("invalidated audit was trusted: %+v: %v", view, err)
	}
}

func TestPackagePreviewsReuseOnlyRecentMatchingCatalogsAndNeverPlans(t *testing.T) {
	for _, scenario := range []string{"recent", "expired", "changed configuration", "missing catalog", "failed refresh"} {
		t.Run(scenario, func(t *testing.T) {
			s, jail, fixture := newPackageReadTestService(t)
			runPackageTestTask(t, s, jail.CTID, "pkg-check-updates", jailTypes.PackageRequest{})
			if fixture.refreshes != 1 || fixture.fileQueries != 0 || fixture.fetches != 0 {
				t.Fatalf("update check did installation preflight: %+v", fixture)
			}
			wantRefreshes, wantSolves := 2, 3
			switch scenario {
			case "recent":
				wantRefreshes = 1
			case "expired":
				if err := s.DB.Model(&jailModels.JailPackageState{}).Where("jail_id = ?", jail.ID).Update("repositories_refreshed_at", time.Now().Add(-6*time.Minute)).Error; err != nil {
					t.Fatal(err)
				}
			case "changed configuration":
				fixture.repositories = strings.ReplaceAll(fixture.repositories, "example.invalid", "other.invalid")
			case "missing catalog":
				if err := os.Remove(filepath.Join(fixture.root, "var/db/pkg/repos/ports/db")); err != nil {
					t.Fatal(err)
				}
			case "failed refresh":
				fixture.refreshError = errors.New("refresh_failed")
				job, err := s.StartPackageOperation(t.Context(), jail.CTID, "pkg-check-updates", jailTypes.PackageRequest{}, "user", "test")
				if err != nil {
					t.Fatal(err)
				}
				if err = s.ExecutePackageTask(t.Context(), job.TaskID); err == nil {
					t.Fatal("refresh failure was ignored")
				}
				fixture.refreshError = nil
				wantRefreshes = 3
			}
			first := runPackageTestTask(t, s, jail.CTID, "pkg-preview", jailTypes.PackageRequest{All: true})
			second := runPackageTestTask(t, s, jail.CTID, "pkg-preview", jailTypes.PackageRequest{All: true})
			if fixture.refreshes != wantRefreshes || fixture.solves != wantSolves || fixture.fetches != 2 || first.Plan == nil || second.Plan == nil || first.Plan.ID == second.Plan.ID {
				t.Fatalf("catalog or plan reuse: refreshes=%d solves=%d fetches=%d first=%+v second=%+v", fixture.refreshes, fixture.solves, fixture.fetches, first.Plan, second.Plan)
			}
			runPackageTestTask(t, s, jail.CTID, "pkg-check-updates", jailTypes.PackageRequest{})
			if fixture.refreshes != wantRefreshes+1 {
				t.Fatal("explicit update check reused catalogs")
			}
		})
	}
}

func TestPackageOnlyAllApplicationPreviewsMarkUpdatesChecked(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(fmt.Sprint(all), func(t *testing.T) {
			s, jail, _ := newPackageReadTestService(t)
			request := jailTypes.PackageRequest{All: all}
			if !all {
				request.Packages = []string{"app"}
			}
			runPackageTestTask(t, s, jail.CTID, "pkg-preview", request)
			view, err := s.GetPackages(t.Context(), jail.CTID)
			if err != nil || view.UpdatesChecked != all || view.SystemUpdatesChecked {
				t.Fatalf("preview freshness: %+v: %v", view, err)
			}
			if all && view.Packages[0].Available != "2" {
				t.Fatal("all-app preview lost the available version")
			}
		})
	}
}

func TestPackageSchedulerUsesJailMetadataAndResetsReplacedRoots(t *testing.T) {
	s, jail := newPackageTestService(t)
	if err := s.DB.Model(&jail).Updates(map[string]any{"package_monitoring_enabled": true, "package_audit_interval_hours": 12}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.runPackageSchedules(t.Context()); err != nil {
		t.Fatal(err)
	}
	var operation jailModels.JailPackageOperation
	if err := s.DB.First(&operation).Error; err != nil {
		t.Fatal(err)
	}
	s.finishPackageOperation(operation, nil)
	if err := s.DB.Model(&jailModels.Storage{}).Where("jid = ?", jail.ID).Update("guid", "replacement").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.runPackageSchedules(t.Context()); err != nil {
		t.Fatal(err)
	}
	var state jailModels.JailPackageState
	if err := s.DB.First(&state).Error; err != nil || state.GUID != "replacement" || state.NextAuditAt == nil || time.Until(*state.NextAuditAt) < 11*time.Hour {
		t.Fatalf("replacement schedule %+v: %v", state, err)
	}
	var count int64
	if err := s.DB.Model(&taskModels.GuestLifecycleTask{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("metadata scheduler tasks=%d: %v", count, err)
	}
	if err := s.DB.Model(&jail).Update("package_monitoring_enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Model(&state).Update("next_audit_at", nil).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.runPackageSchedules(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Model(&taskModels.GuestLifecycleTask{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("disabled metadata scheduled a task: %d: %v", count, err)
	}
}

func TestPackageSchedulerMalformedRootsDoNotStarveEligibleJails(t *testing.T) {
	s, jail := newPackageTestService(t)
	if err := s.DB.Model(&jail).Update("package_monitoring_enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Model(&jailModels.Storage{}).Where("jid = ?", jail.ID).Update("pool", "tank/shared").Error; err != nil {
		t.Fatal(err)
	}
	for i, pool := range []string{" ", "tank"} {
		other := jailModels.Jail{CTID: jail.CTID + uint(i) + 1, Name: fmt.Sprintf("monitor-%d", i), Type: jailModels.JailTypeFreeBSD, PackageMonitoringEnabled: true, Storages: []jailModels.Storage{{Pool: pool, GUID: fmt.Sprintf("guid-%d", i), IsBase: true}}}
		if err := s.DB.Create(&other).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := s.runPackageSchedules(t.Context()); err != nil {
		t.Fatal(err)
	}
	var tasks []taskModels.GuestLifecycleTask
	if err := s.DB.Find(&tasks).Error; err != nil || len(tasks) != 1 || tasks[0].GuestID != jail.CTID+2 {
		t.Fatalf("eligible jail was starved by malformed metadata: %+v: %v", tasks, err)
	}
}

func TestPackageCompletedInstallerKeepsFollowUpAuditWithoutReplaying(t *testing.T) {
	for _, test := range []struct {
		phase      string
		auditFails bool
	}{{"applying", false}, {"post_upgrade_audit", false}, {"post_upgrade_audit", true}} {
		t.Run(fmt.Sprintf("%s/failure=%v", test.phase, test.auditFails), func(t *testing.T) {
			s, jail, fixture := newPackageReadTestService(t)
			now := time.Now().UTC()
			plan := jailModels.JailPackagePlan{ID: uuid.NewString(), JailID: jail.ID, GUID: jail.Storages[0].GUID, ExpiresAt: now.Add(time.Minute), Plan: pkgmanager.Plan{Changes: []pkgmanager.Change{{Action: "upgrade", Name: "app", From: "1", To: "2"}}}}
			if err := s.DB.Create(&plan).Error; err != nil {
				t.Fatal(err)
			}
			job, err := s.StartPackageOperation(t.Context(), jail.CTID, "pkg-upgrade", jailTypes.PackageRequest{PlanID: plan.ID}, "user", "test")
			if err != nil {
				t.Fatal(err)
			}
			var operation jailModels.JailPackageOperation
			if err = s.DB.First(&operation, "id = ?", job.OperationID).Error; err != nil {
				t.Fatal(err)
			}
			if err = s.packagePhase(&operation, test.phase); err != nil {
				t.Fatal(err)
			}
			dir, err := s.packageDirectory(operation)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dir, "result.json"), []byte(`{"error":""}`), 0600); err != nil {
				t.Fatal(err)
			}
			fixture.inventory = "app\t2\tmisc/app\tports\t1\t0\t0\n"
			factory := s.packages.client
			audited := false
			s.packages.client = func(target pkgmanager.Target, log io.Writer) *pkgmanager.Client {
				client := factory(target, log)
				run := client.RunCommand
				client.RunCommand = func(ctx context.Context, args []string) (pkgmanager.Result, error) {
					if slices.Contains(args, "audit") {
						var current jailModels.JailPackageOperation
						if err := s.DB.First(&current, "id = ?", operation.ID).Error; err != nil || current.Phase != "post_upgrade_audit" {
							t.Fatalf("follow-up phase=%s: %v", current.Phase, err)
						}
						audited = true
						if test.auditFails {
							return pkgmanager.Result{Stderr: "feed fetch failed", ExitCode: 1}, nil
						}
					}
					return run(ctx, args)
				}
				return client
			}
			if err = s.ExecutePackageTask(t.Context(), job.TaskID); err != nil {
				t.Fatal(err)
			}
			if err = s.DB.First(&operation, "id = ?", operation.ID).Error; err != nil || !audited || operation.Phase != "completed" || operation.ActiveJailID != nil || (operation.Audit == nil) != test.auditFails || fixture.solves != 0 || fixture.fetches != 0 {
				t.Fatalf("completed installer was replayed or not audited: %+v: %v", operation, err)
			}
			if test.auditFails {
				log, err := os.ReadFile(filepath.Join(dir, "output.log"))
				if err != nil || !strings.Contains(string(log), "Follow-up audit unavailable") || !strings.Contains(string(log), "feed fetch failed") {
					t.Fatalf("follow-up audit failure lost its diagnostic: %q: %v", log, err)
				}
			}
		})
	}
}

func TestPackageFailedInstallerInvalidatesRecordedResults(t *testing.T) {
	s, jail, fixture := newPackageReadTestService(t)
	inventory, err := pkgmanager.ParseInventory(fixture.inventory)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	state := jailModels.JailPackageState{JailID: jail.ID, CTID: jail.CTID, GUID: jail.Storages[0].GUID, Inventory: inventory, AuditAt: &now, AuditInventoryHash: pkgmanager.AuditInventoryDigest(inventory), CheckedAt: &now, CheckInventoryHash: pkgmanager.InventoryDigest(inventory), SystemCheckedAt: &now, SystemCheckInventoryHash: pkgmanager.InventoryDigest(inventory)}
	if err = s.DB.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	task := taskModels.GuestLifecycleTask{GuestType: "jail", GuestID: jail.CTID, Action: "pkg-upgrade", Status: "running"}
	if err = s.DB.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	operation := jailModels.JailPackageOperation{ID: uuid.NewString(), TaskID: &task.ID, JailID: jail.ID, ActiveJailID: &jail.ID, CTID: jail.CTID, GUID: state.GUID, Action: task.Action, Phase: "applying"}
	if err = s.DB.Create(&operation).Error; err != nil {
		t.Fatal(err)
	}
	dir, err := s.packageDirectory(operation)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "result.json"), []byte(`{"error":"package_upgrade_failed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.ExecutePackageTask(t.Context(), task.ID); err == nil {
		t.Fatal("failed installer reported success")
	}
	view, err := s.GetPackages(t.Context(), jail.CTID)
	if err != nil || !view.Ready || !view.AuditStale || view.AuditError == "" || view.UpdatesChecked || view.SystemUpdatesChecked {
		t.Fatalf("failed installer retained trusted results: %+v: %v", view, err)
	}
}
