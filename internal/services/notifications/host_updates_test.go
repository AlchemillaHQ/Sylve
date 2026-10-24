// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package notifications

import (
	"context"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
)

func TestHostUpdateRulesAreNodeLocalAndRespectDeletion(t *testing.T) {
	service := newTestService(t)
	if err := service.DB.AutoMigrate(&models.HostUpdateState{}); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.Create(&models.HostUpdateState{ID: 1}).Error; err != nil {
		t.Fatal(err)
	}
	view, err := service.GetRuleConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var hostRules []RuleConfigEntryView
	for _, rule := range view.Rules {
		if strings.HasPrefix(rule.TemplateKey, "system.updates.") {
			hostRules = append(hostRules, rule)
		}
	}
	if len(hostRules) != 4 {
		t.Fatalf("host rules: %+v", hostRules)
	}
	for _, rule := range hostRules {
		if rule.TargetKey != "host" || !rule.Active || !rule.UIEnabled || !rule.EmailEnabled {
			t.Fatalf("bad defaults: %+v", rule)
		}
		template, target, ok := notifier.HostUpdateKind(rule.Kind)
		if !ok || target != "host" || template != rule.TemplateKey {
			t.Fatalf("bad host kind: %s", rule.Kind)
		}
	}
	deleted := hostRules[0]
	if _, err = service.DeleteRule(t.Context(), deleted.ID); err != nil {
		t.Fatal(err)
	}
	view, err = service.GetRuleConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range view.Rules {
		if rule.Kind == deleted.Kind {
			t.Fatal("deleted host rule recreated")
		}
	}
	if _, err = ruleKindForTemplateTarget("system.updates.available", "other-host"); err == nil {
		t.Fatal("accepted a different host target")
	}
}

func TestHostUpdateRuleTestsDoNotRunPackageOperations(t *testing.T) {
	service := newTestService(t)
	if err := service.DB.AutoMigrate(&models.HostUpdateState{}); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.Create(&models.HostUpdateState{ID: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetRuleConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.TestRule(t.Context(), TestRuleInput{TemplateKey: "system.updates.health", TargetKey: "host"}); err != nil {
		t.Fatal(err)
	}
	var notification models.Notification
	if err := service.DB.Where("kind = ?", "system.updates.health.host").First(&notification).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notification.Body, "No packages were changed") || notification.Metadata["test"] != "true" {
		t.Fatalf("not a test notification: %+v", notification)
	}
}
