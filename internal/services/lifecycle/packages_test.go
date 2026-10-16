// SPDX-License-Identifier: BSD-2-Clause

package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
)

func TestPackageTaskDelegationAuditAndNoMutationRetry(t *testing.T) {
	service, primary, telemetry := setupLifecycleAuditTest(t)
	audit := createStartedLifecycleAudit(t, telemetry)
	prepared, executed := false, 0
	service.packagePrepareFn = func(_ context.Context, taskID, ctID uint, action, payload string) error {
		prepared = true
		if taskID == 0 || ctID != 900 || action != "pkg-audit" || payload != "{}" {
			t.Fatalf("invalid preparation %d %d %s %s", taskID, ctID, action, payload)
		}
		return nil
	}
	service.packageExecuteFn = func(context.Context, uint) error {
		if !prepared {
			t.Fatal("executed before binding")
		}
		executed++
		return errors.New("feed_failed")
	}
	service.jailActionFn = func(int, string) error { t.Fatal("package task reached lifecycle commands"); return nil }
	task, _, err := service.RequestActionWithPayload(db.ContextWithAuditRecordID(t.Context(), audit.ID), "jail", 900, "pkg-audit", "user", "tester", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err = telemetry.First(&audit, audit.ID).Error; err != nil || audit.AsyncJobType != "jail_pkg-audit" || audit.Status != "pending" {
		t.Fatalf("audit %+v %v", audit, err)
	}
	if err = service.ExecuteTask(t.Context(), task.ID); err == nil || !strings.Contains(err.Error(), "feed_failed") {
		t.Fatal(err)
	}
	if err = service.ExecuteTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	if executed != 1 {
		t.Fatal("operational attempt replayed", executed)
	}
	var terminal taskModels.GuestLifecycleTask
	if err = primary.First(&terminal, task.ID).Error; err != nil || terminal.Status != "failed" {
		t.Fatalf("task %+v %v", terminal, err)
	}
}

func TestPackageActionsAreJailOnly(t *testing.T) {
	for _, action := range []string{"pkg-audit", "pkg-preview", "pkg-check-updates", "pkg-upgrade"} {
		if err := validateAction("jail", action); err != nil {
			t.Fatal(err)
		}
		for _, guestType := range []string{"vm", "jail-template", "vm-template"} {
			if err := validateAction(guestType, action); err == nil {
				t.Fatalf("accepted %s %s", guestType, action)
			}
		}
	}
}

func TestPackageTaskClaimsResumeOnlyCommittedWorkerPhases(t *testing.T) {
	for _, test := range []struct {
		phase     string
		claimable bool
	}{{"applying", true}, {"post_upgrade_audit", true}, {"verifying", false}} {
		t.Run(test.phase, func(t *testing.T) {
			s, database := newLifecycleTestService(t)
			if err := database.AutoMigrate(&jailModels.JailPackageOperation{}); err != nil {
				t.Fatal(err)
			}
			task := taskModels.GuestLifecycleTask{GuestType: "jail", GuestID: 900, Action: "pkg-upgrade", Status: "running"}
			if err := database.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			jailID := uint(1)
			op := jailModels.JailPackageOperation{ID: test.phase, TaskID: &task.ID, JailID: jailID, ActiveJailID: &jailID, CTID: task.GuestID, GUID: "fixture", Action: task.Action, Phase: test.phase}
			if err := database.Create(&op).Error; err != nil {
				t.Fatal(err)
			}
			claimed, err := s.claimTaskForExecution(t.Context(), task.ID, task.Action, time.Now().UTC())
			if err != nil || claimed != test.claimable {
				t.Fatalf("phase=%s claimed=%v: %v", test.phase, claimed, err)
			}
		})
	}
}
