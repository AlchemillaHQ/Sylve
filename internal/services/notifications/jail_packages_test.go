// SPDX-License-Identifier: BSD-2-Clause

package notifications

import (
	"errors"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
	"gorm.io/gorm"
)

func TestPackageRuleTargetsRequireExactFreeBSDIncarnation(t *testing.T) {
	service := newTestService(t)
	if err := service.DB.AutoMigrate(&jailModels.Jail{}, &jailModels.Storage{}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []jailModels.JailType{jailModels.JailTypeFreeBSD, jailModels.JailTypeLinux, ""} {
		jail := jailModels.Jail{CTID: uint(200 + len(kind)), Name: "fixture-" + string(kind), Type: kind}
		if err := service.DB.Create(&jail).Error; err != nil {
			t.Fatal(err)
		}
		if err := service.DB.Create(&jailModels.Storage{JailID: jail.ID, Pool: "tank", GUID: "guid-" + string(kind), IsBase: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	definitions, err := packageTemplateDefinitions(service.DB)
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if len(definition.View.Targets) != 1 {
			t.Fatalf("targets %+v", definition.View.Targets)
		}
		key := definition.View.Targets[0].Key
		kind, err := ruleKindForTemplateTarget(definition.View.Key, key)
		if err != nil {
			t.Fatal(err)
		}
		template, target, ok := notifier.JailPackageKind(kind)
		if !ok || template != definition.View.Key || target != key {
			t.Fatalf("kind %s => %s %s %v", kind, template, target, ok)
		}
	}
}

func TestPackageLateAlertsCannotRecreateRulesForMissingTargets(t *testing.T) {
	for _, change := range []string{"deleted", "replaced root", "linux", "creation pending"} {
		t.Run(change, func(t *testing.T) {
			service := newTestService(t)
			if err := service.DB.AutoMigrate(&jailModels.Jail{}, &jailModels.Storage{}); err != nil {
				t.Fatal(err)
			}
			jail := jailModels.Jail{CTID: 900, Name: "fixture", Type: jailModels.JailTypeFreeBSD, Storages: []jailModels.Storage{{IsBase: true, Pool: "tank", GUID: "Root-GUID"}}}
			if err := service.DB.Create(&jail).Error; err != nil {
				t.Fatal(err)
			}
			input := notifier.EventInput{Kind: notifier.JailPackageHealthKindPrefix + notifier.JailPackageTarget(jail.ID, jail.Storages[0].GUID), Title: "Audit unavailable", Fingerprint: "previous-alert", Channels: []string{notifier.ChannelUI}}
			initial, err := service.Emit(t.Context(), input)
			if err != nil || initial.NotificationID == 0 {
				t.Fatalf("current jail did not get its rule/notification: %+v: %v", initial, err)
			}
			switch change {
			case "deleted":
				if err = service.DB.Where("jid = ?", jail.ID).Delete(&jailModels.Storage{}).Error; err != nil {
					t.Fatal(err)
				}
				err = service.DB.Delete(&jail).Error
			case "replaced root":
				err = service.DB.Model(&jailModels.Storage{}).Where("jid = ?", jail.ID).Update("guid", "replacement").Error
			case "linux":
				err = service.DB.Model(&jail).Update("type", jailModels.JailTypeLinux).Error
			case "creation pending":
				err = service.DB.Model(&jail).Update("creation_pending", true).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.GetRuleConfig(t.Context()); err != nil {
				t.Fatal(err)
			}
			targets, err := service.DeliveryTargets(t.Context(), input)
			if err != nil || len(targets) != 0 {
				t.Fatalf("late alert got delivery targets: %v: %v", targets, err)
			}
			late, err := service.EmitTarget(t.Context(), input, notifier.ChannelUI)
			if err != nil || late.NotificationID != 0 {
				t.Fatalf("late alert was emitted: %+v: %v", late, err)
			}
			var count int64
			if err = service.DB.Model(&models.NotificationKindRule{}).Where("kind = ?", input.Kind).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("obsolete rule was recreated: count=%d: %v", count, err)
			}
			var history models.Notification
			if err = service.DB.First(&history, initial.NotificationID).Error; err != nil || history.OccurrenceCount != 1 {
				t.Fatalf("existing notification history changed: %+v: %v", history, err)
			}
		})
	}
}

func TestPackageRuleSyncPrunesObsoleteRootsAndOrphansWithoutDeletingHistory(t *testing.T) {
	service := newTestService(t)
	if err := service.DB.AutoMigrate(&jailModels.Jail{}, &jailModels.Storage{}); err != nil {
		t.Fatal(err)
	}
	jail := jailModels.Jail{CTID: 900, Name: "fixture", Type: jailModels.JailTypeFreeBSD, Storages: []jailModels.Storage{{IsBase: true, Pool: "tank", GUID: "Root-GUID"}}}
	if err := service.DB.Create(&jail).Error; err != nil {
		t.Fatal(err)
	}
	currentTarget := notifier.JailPackageTarget(jail.ID, jail.Storages[0].GUID)
	current := models.NotificationKindRule{Kind: notifier.JailPackageVulnerabilitiesKindPrefix + currentTarget, UIEnabled: false, EmailEnabled: true, Config: `{"retained":true}`}
	disabled := models.NotificationKindRule{Kind: notifier.JailPackageHealthKindPrefix + currentTarget, UserDisabled: true}
	for _, rule := range []*models.NotificationKindRule{&current, &disabled} {
		if err := service.DB.Create(rule).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := service.DB.Model(&current).Update("ui_enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	oldKinds := []string{}
	for _, target := range []string{notifier.JailPackageTarget(jail.ID, "old-root"), notifier.JailPackageTarget(jail.ID+1, "deleted-root")} {
		for _, prefix := range []string{notifier.JailPackageVulnerabilitiesKindPrefix, notifier.JailPackageHealthKindPrefix} {
			kind := prefix + target
			oldKinds = append(oldKinds, kind)
			if err := service.DB.Create(&models.NotificationKindRule{Kind: kind, UserDisabled: prefix == notifier.JailPackageHealthKindPrefix}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	otherKinds := []string{notifier.ZFSPoolStateKindPrefix + "missing-pool", notifier.DiskSmartHealthKindPrefix + "missing-disk"}
	for _, kind := range otherKinds {
		if err := service.DB.Create(&models.NotificationKindRule{Kind: kind}).Error; err != nil {
			t.Fatal(err)
		}
	}
	history := models.Notification{Kind: oldKinds[0], Title: "Previous alert", Fingerprint: "retained-history"}
	if err := service.DB.Create(&history).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		view, err := service.GetRuleConfig(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, rule := range view.Rules {
			if _, _, ok := notifier.JailPackageKind(rule.Kind); ok && (!rule.Active || rule.ID != current.ID) {
				t.Fatalf("obsolete or user-disabled package rule was listed: %+v", rule)
			}
		}
	}
	var count int64
	if err := service.DB.Model(&models.NotificationKindRule{}).Where("kind IN ?", oldKinds).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("obsolete rules survived: %d: %v", count, err)
	}
	if err := service.DB.Model(&models.NotificationKindRule{}).Where("kind IN ?", otherKinds).Count(&count).Error; err != nil || count != int64(len(otherKinds)) {
		t.Fatalf("unrelated inactive rules were pruned: %d: %v", count, err)
	}
	var saved models.NotificationKindRule
	if err := service.DB.First(&saved, current.ID).Error; err != nil || saved.UIEnabled || !saved.EmailEnabled || saved.NtfyEnabled || saved.Config != current.Config {
		t.Fatalf("current rule settings changed: %+v: %v", saved, err)
	}
	saved = models.NotificationKindRule{}
	if err := service.DB.First(&saved, disabled.ID).Error; err != nil || !saved.UserDisabled {
		t.Fatalf("current disabled rule was removed or re-enabled: %+v: %v", saved, err)
	}
	if err := service.DB.First(&history, history.ID).Error; err != nil {
		t.Fatalf("notification history was removed: %v", err)
	}
	if err := service.DB.Model(&jailModels.Storage{}).Where("jid = ?", jail.ID).Update("guid", "replacement-root").Error; err != nil {
		t.Fatal(err)
	}
	view, err := service.GetRuleConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	packageRules := 0
	for _, rule := range view.Rules {
		if _, _, ok := notifier.JailPackageKind(rule.Kind); ok {
			packageRules++
			if !rule.Active || rule.TargetKey != notifier.JailPackageTarget(jail.ID, "replacement-root") {
				t.Fatalf("old-root rule survived replacement: %+v", rule)
			}
		}
	}
	if packageRules != 2 {
		t.Fatalf("replacement root has %d rules, want 2", packageRules)
	}
}

func TestPackageRuleSyncDoesNotPruneOnMetadataLookupFailure(t *testing.T) {
	service := newTestService(t)
	if err := service.DB.AutoMigrate(&jailModels.Jail{}, &jailModels.Storage{}); err != nil {
		t.Fatal(err)
	}
	rule := models.NotificationKindRule{Kind: notifier.JailPackageHealthKindPrefix + notifier.JailPackageTarget(1, "root")}
	if err := service.DB.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	cause := errors.New("metadata_lookup_unavailable")
	if err := service.DB.Callback().Query().Before("gorm:query").Register("package-metadata-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "jails" {
			tx.AddError(cause)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetRuleConfig(t.Context()); !errors.Is(err, cause) {
		t.Fatalf("lookup failure was swallowed: %v", err)
	}
	var saved models.NotificationKindRule
	if err := service.DB.First(&saved, rule.ID).Error; err != nil {
		t.Fatalf("uncertain metadata caused rule deletion: %v", err)
	}
}
