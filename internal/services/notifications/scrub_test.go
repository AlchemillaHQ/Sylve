// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package notifications

import (
	"context"
	"testing"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
)

type scrubRulePoolService struct{ pools []*gzfs.ZPool }

func (s *scrubRulePoolService) GetUsablePools(context.Context) ([]*gzfs.ZPool, error) {
	return s.pools, nil
}

func TestPoolScrubRuleTargetsUseGUIDAndDisplayName(t *testing.T) {
	s := newTestService(t)
	t.Cleanup(func() { notifier.SetEmitter(nil) })
	if err := s.DB.Create(&models.BasicSettings{Pools: []string{"tank"}}).Error; err != nil {
		t.Fatal(err)
	}
	poolService := &scrubRulePoolService{pools: []*gzfs.ZPool{{PoolGUID: "18446744073709551615", Name: "tank"}}}
	s.PoolService = poolService
	view, err := s.GetRuleConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rule := range view.Rules {
		if rule.TemplateKey == RuleTemplateZFSScrub {
			found = rule.TargetKey == "18446744073709551615" && rule.TargetLabel == "tank" && rule.Active
		}
	}
	if !found {
		t.Fatalf("missing GUID-scoped readable scrub rule: %+v", view)
	}
	poolService.pools[0].Name = "renamed"
	view, err = s.GetRuleConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range view.Rules {
		if rule.TemplateKey == RuleTemplateZFSScrub && (rule.TargetKey != "18446744073709551615" || rule.TargetLabel != "renamed") {
			t.Fatalf("rename changed identity: %+v", rule)
		}
	}
	poolService.pools = nil
	view, err = s.GetRuleConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range view.Rules {
		if rule.TemplateKey == RuleTemplateZFSScrub && rule.Active {
			t.Fatal("missing pool remained an active rule target")
		}
	}
}

func TestPoolScrubNotificationDismissalDoesNotSuppressNextRun(t *testing.T) {
	s := newTestService(t)
	t.Cleanup(func() { notifier.SetEmitter(nil) })
	input := notifier.EventInput{Kind: notifier.KindForZFSScrub("101"), Title: "Scrub failed", Severity: "critical", Fingerprint: "101|scrub", Metadata: map[string]string{"condition": "scrub_failed", "run_key": "first"}}
	result, err := s.Emit(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Dismiss(t.Context(), result.NotificationID); err != nil {
		t.Fatal(err)
	}
	input.Metadata["run_key"] = "second"
	result, err = s.Emit(t.Context(), input)
	if err != nil || result.Suppressed {
		t.Fatalf("next run suppressed: %+v %v", result, err)
	}
	var row models.Notification
	if err := s.DB.First(&row, result.NotificationID).Error; err != nil {
		t.Fatal(err)
	}
	if row.DismissedAt != nil || row.Metadata["run_key"] != "second" {
		t.Fatalf("dismissal persisted across runs: %+v", row)
	}
	var count int64
	s.DB.Model(&models.NotificationSuppression{}).Where("kind = ?", input.Kind).Count(&count)
	if count != 0 {
		t.Fatal("scrub lifecycle created permanent dismissal suppression")
	}
}
