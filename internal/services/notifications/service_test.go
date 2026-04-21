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
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func newTestService(t *testing.T) *Service {
	t.Helper()

	db := testutil.NewSQLiteTestDB(
		t,
		&models.Notification{},
		&models.NotificationSuppression{},
		&models.NotificationKindRule{},
		&models.NotificationTransportConfig{},
		&models.BasicSettings{},
		&models.SystemSecrets{},
	)

	return NewService(db)
}

func TestEmitCreatesAndIncrementsByFingerprint(t *testing.T) {
	svc := newTestService(t)

	input := notifier.EventInput{
		Kind:        "storage.disks",
		Title:       "Disk failure",
		Body:        "Disk ada0 is unhealthy",
		Severity:    "warning",
		Source:      "smartd",
		Fingerprint: "disk-ada0-failure",
	}

	res1, err := svc.Emit(context.Background(), input)
	if err != nil {
		t.Fatalf("emit_1_failed: %v", err)
	}
	if res1.NotificationID == 0 {
		t.Fatalf("expected_notification_id_on_first_emit")
	}
	if res1.Suppressed {
		t.Fatalf("expected_first_emit_not_suppressed")
	}

	res2, err := svc.Emit(context.Background(), input)
	if err != nil {
		t.Fatalf("emit_2_failed: %v", err)
	}
	if res2.NotificationID != res1.NotificationID {
		t.Fatalf("expected_same_notification_id got first=%d second=%d", res1.NotificationID, res2.NotificationID)
	}

	var notif models.Notification
	if err := svc.DB.First(&notif, res1.NotificationID).Error; err != nil {
		t.Fatalf("failed_to_load_notification: %v", err)
	}
	if notif.OccurrenceCount != 2 {
		t.Fatalf("expected_occurrence_count_2 got: %d", notif.OccurrenceCount)
	}
}

func TestDismissSuppressesFutureEmits(t *testing.T) {
	svc := newTestService(t)

	input := notifier.EventInput{
		Kind:        "storage.disks",
		Title:       "Disk failure",
		Body:        "Disk ada0 is unhealthy",
		Severity:    "critical",
		Fingerprint: "disk-ada0-failure",
	}

	created, err := svc.Emit(context.Background(), input)
	if err != nil {
		t.Fatalf("emit_failed: %v", err)
	}

	if err := svc.Dismiss(context.Background(), created.NotificationID); err != nil {
		t.Fatalf("dismiss_failed: %v", err)
	}

	suppressed, err := svc.Emit(context.Background(), input)
	if err != nil {
		t.Fatalf("emit_after_dismiss_failed: %v", err)
	}
	if !suppressed.Suppressed {
		t.Fatalf("expected_emit_to_be_suppressed")
	}

	activeCount, err := svc.CountActive(context.Background())
	if err != nil {
		t.Fatalf("count_active_failed: %v", err)
	}
	if activeCount != 0 {
		t.Fatalf("expected_no_active_notifications got: %d", activeCount)
	}
}

func TestTransportSendersRespectConfigAndSuppression(t *testing.T) {
	svc := newTestService(t)

	ntfyCalls := 0
	emailCalls := 0
	svc.SetNtfySender(func(ctx context.Context, cfg models.NotificationTransportConfig, input notifier.EventInput, token string) error {
		ntfyCalls++
		return nil
	})
	svc.SetEmailSender(func(ctx context.Context, cfg models.NotificationTransportConfig, input notifier.EventInput, password string) error {
		emailCalls++
		return nil
	})

	_, err := svc.UpdateTransportConfig(context.Background(), TransportConfigUpdate{
		Transports: []TransportConfigEntryUpdate{
			{
				Name:    "Primary Ntfy",
				Type:    TransportTypeNtfy,
				Enabled: true,
				Ntfy: &NtfyTransportConfigUpdate{
					BaseURL: "https://ntfy.sh",
					Topic:   "sylve",
				},
			},
			{
				Name:    "Primary SMTP",
				Type:    TransportTypeSMTP,
				Enabled: true,
				Email: &EmailTransportConfigUpdate{
					SMTPHost:   "localhost",
					SMTPPort:   1025,
					SMTPFrom:   "alerts@example.com",
					Recipients: []string{"ops@example.com"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("update_transport_config_failed: %v", err)
	}

	input := notifier.EventInput{
		Kind:        "network.firewall",
		Title:       "Firewall drop spike",
		Body:        "Inbound drop threshold crossed",
		Severity:    "warning",
		Fingerprint: "firewall-drop-spike",
	}

	created, err := svc.Emit(context.Background(), input)
	if err != nil {
		t.Fatalf("emit_failed: %v", err)
	}

	if ntfyCalls != 1 {
		t.Fatalf("expected_ntfy_called_once got: %d", ntfyCalls)
	}
	if emailCalls != 1 {
		t.Fatalf("expected_email_called_once got: %d", emailCalls)
	}

	if err := svc.Dismiss(context.Background(), created.NotificationID); err != nil {
		t.Fatalf("dismiss_failed: %v", err)
	}

	_, err = svc.Emit(context.Background(), input)
	if err != nil {
		t.Fatalf("emit_after_dismiss_failed: %v", err)
	}

	if ntfyCalls != 1 {
		t.Fatalf("expected_ntfy_not_called_after_suppression got: %d", ntfyCalls)
	}
	if emailCalls != 1 {
		t.Fatalf("expected_email_not_called_after_suppression got: %d", emailCalls)
	}
}

func TestTransportConfigStoresRecipientsAndSecretFlags(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&models.Notification{},
		&models.NotificationSuppression{},
		&models.NotificationKindRule{},
		&models.NotificationTransportConfig{},
		&models.BasicSettings{},
		&models.SystemSecrets{},
	)
	svc := NewService(db)

	token := "ntfy-token"
	password := "smtp-pass"
	view, err := svc.UpdateTransportConfig(context.Background(), TransportConfigUpdate{
		Transports: []TransportConfigEntryUpdate{
			{
				Name:    "Ntfy Transport",
				Type:    TransportTypeNtfy,
				Enabled: true,
				Ntfy: &NtfyTransportConfigUpdate{
					BaseURL:   "https://ntfy.sh",
					Topic:     "alerts",
					AuthToken: &token,
				},
			},
			{
				Name:    "SMTP Transport",
				Type:    TransportTypeSMTP,
				Enabled: true,
				Email: &EmailTransportConfigUpdate{
					SMTPHost:     "smtp.example.com",
					SMTPPort:     587,
					SMTPUsername: "smtp-user",
					SMTPFrom:     "alerts@example.com",
					SMTPUseTLS:   true,
					Recipients:   []string{"b@example.com", "a@example.com", "a@example.com"},
					SMTPPassword: &password,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("update_transport_config_failed: %v", err)
	}

	if len(view.Transports) == 0 {
		t.Fatalf("expected_transport_entries")
	}

	var ntfyEntry *TransportConfigEntryView
	var smtpEntry *TransportConfigEntryView
	for idx := range view.Transports {
		entry := &view.Transports[idx]
		if entry.Type == TransportTypeNtfy {
			ntfyEntry = entry
		}
		if entry.Type == TransportTypeSMTP {
			smtpEntry = entry
		}
	}
	if ntfyEntry == nil {
		t.Fatalf("expected_ntfy_transport_entry")
	}
	if smtpEntry == nil {
		t.Fatalf("expected_smtp_transport_entry")
	}
	if ntfyEntry.Ntfy == nil {
		t.Fatalf("expected_ntfy_view")
	}
	if smtpEntry.Email == nil {
		t.Fatalf("expected_smtp_view")
	}

	if !ntfyEntry.Ntfy.HasAuthToken {
		t.Fatalf("expected_ntfy_secret_flag_true")
	}
	if !smtpEntry.Email.HasPassword {
		t.Fatalf("expected_smtp_secret_flag_true")
	}

	if len(smtpEntry.Email.Recipients) != 2 {
		t.Fatalf("expected_deduplicated_recipients got: %d", len(smtpEntry.Email.Recipients))
	}
	if smtpEntry.Email.Recipients[0] != "a@example.com" {
		t.Fatalf("expected_sorted_recipients got_first=%s", smtpEntry.Email.Recipients[0])
	}
}

func TestUpdateTransportConfigRejectsEmptyTransportName(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.UpdateTransportConfig(context.Background(), TransportConfigUpdate{
		Transports: []TransportConfigEntryUpdate{
			{
				Name:    "   ",
				Type:    TransportTypeNtfy,
				Enabled: true,
				Ntfy: &NtfyTransportConfigUpdate{
					BaseURL: "https://ntfy.sh",
					Topic:   "alerts",
				},
			},
		},
	})
	if err == nil {
		t.Fatalf("expected_error_for_blank_transport_name")
	}
	if err.Error() != "transport_name_required" {
		t.Fatalf("expected_transport_name_required got: %v", err)
	}
}

func TestTestTransportSendsForNtfyAndSMTPEvenWhenDisabled(t *testing.T) {
	svc := newTestService(t)

	ntfyCalls := 0
	emailCalls := 0
	svc.SetNtfySender(func(ctx context.Context, cfg models.NotificationTransportConfig, input notifier.EventInput, token string) error {
		ntfyCalls++
		return nil
	})
	svc.SetEmailSender(func(ctx context.Context, cfg models.NotificationTransportConfig, input notifier.EventInput, password string) error {
		emailCalls++
		return nil
	})

	view, err := svc.UpdateTransportConfig(context.Background(), TransportConfigUpdate{
		Transports: []TransportConfigEntryUpdate{
			{
				Name:    "Ntfy Transport",
				Type:    TransportTypeNtfy,
				Enabled: false,
				Ntfy: &NtfyTransportConfigUpdate{
					BaseURL: "https://ntfy.sh",
					Topic:   "alerts",
				},
			},
			{
				Name:    "SMTP Transport",
				Type:    TransportTypeSMTP,
				Enabled: false,
				Email: &EmailTransportConfigUpdate{
					SMTPHost:   "smtp.example.com",
					SMTPPort:   587,
					SMTPFrom:   "alerts@example.com",
					Recipients: []string{"alerts@example.com"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("update_transport_config_failed: %v", err)
	}

	var ntfyID uint
	var smtpID uint
	for _, transport := range view.Transports {
		switch transport.Type {
		case TransportTypeNtfy:
			ntfyID = transport.ID
		case TransportTypeSMTP:
			smtpID = transport.ID
		}
	}
	if ntfyID == 0 || smtpID == 0 {
		t.Fatalf("expected_both_transport_ids ntfy=%d smtp=%d", ntfyID, smtpID)
	}

	if err := svc.TestTransport(context.Background(), ntfyID); err != nil {
		t.Fatalf("test_ntfy_transport_failed: %v", err)
	}
	if err := svc.TestTransport(context.Background(), smtpID); err != nil {
		t.Fatalf("test_smtp_transport_failed: %v", err)
	}

	if ntfyCalls != 1 {
		t.Fatalf("expected_ntfy_test_call_once got: %d", ntfyCalls)
	}
	if emailCalls != 1 {
		t.Fatalf("expected_smtp_test_call_once got: %d", emailCalls)
	}
}

func TestSuppressionDoesNotCrossKindsWithSameFingerprint(t *testing.T) {
	svc := newTestService(t)

	fingerprint := "shared-fingerprint"
	first, err := svc.Emit(context.Background(), notifier.EventInput{
		Kind:        "storage.disks",
		Title:       "Storage alert",
		Body:        "Disk alert body",
		Severity:    "warning",
		Fingerprint: fingerprint,
	})
	if err != nil {
		t.Fatalf("emit_first_failed: %v", err)
	}

	if err := svc.Dismiss(context.Background(), first.NotificationID); err != nil {
		t.Fatalf("dismiss_first_failed: %v", err)
	}

	second, err := svc.Emit(context.Background(), notifier.EventInput{
		Kind:        "network.firewall",
		Title:       "Network alert",
		Body:        "Firewall alert body",
		Severity:    "warning",
		Fingerprint: fingerprint,
	})
	if err != nil {
		t.Fatalf("emit_second_failed: %v", err)
	}

	if second.Suppressed {
		t.Fatalf("expected_second_kind_not_suppressed")
	}
}

func TestEmitSendsAcrossMultipleTransportRows(t *testing.T) {
	svc := newTestService(t)

	ntfyCalls := 0
	emailCalls := 0
	svc.SetNtfySender(func(ctx context.Context, cfg models.NotificationTransportConfig, input notifier.EventInput, token string) error {
		ntfyCalls++
		return nil
	})
	svc.SetEmailSender(func(ctx context.Context, cfg models.NotificationTransportConfig, input notifier.EventInput, password string) error {
		emailCalls++
		return nil
	})

	_, err := svc.UpdateTransportConfig(context.Background(), TransportConfigUpdate{
		Transports: []TransportConfigEntryUpdate{
			{
				Name:    "Primary Ntfy",
				Type:    TransportTypeNtfy,
				Enabled: true,
				Ntfy: &NtfyTransportConfigUpdate{
					BaseURL: "https://ntfy.sh",
					Topic:   "ops",
				},
			},
			{
				Name:    "SMTP Team",
				Type:    TransportTypeSMTP,
				Enabled: true,
				Email: &EmailTransportConfigUpdate{
					SMTPHost:   "smtp.example.com",
					SMTPPort:   587,
					SMTPFrom:   "alerts@example.com",
					Recipients: []string{"ops@example.com"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("update_transport_configs_failed: %v", err)
	}

	view, err := svc.GetTransportConfig(context.Background())
	if err != nil {
		t.Fatalf("get_transport_config_failed: %v", err)
	}
	if len(view.Transports) < 2 {
		t.Fatalf("expected_multiple_transport_rows got: %d", len(view.Transports))
	}

	_, err = svc.Emit(context.Background(), notifier.EventInput{
		Kind:        "system.multi",
		Title:       "multi transport",
		Severity:    "warning",
		Fingerprint: "multi-transport",
	})
	if err != nil {
		t.Fatalf("emit_failed: %v", err)
	}

	if ntfyCalls != 1 {
		t.Fatalf("expected_ntfy_called_once got: %d", ntfyCalls)
	}
	if emailCalls != 1 {
		t.Fatalf("expected_email_called_once got: %d", emailCalls)
	}
}

func TestUpdateTransportConfigRemovesOmittedRows(t *testing.T) {
	svc := newTestService(t)

	firstToken := "first-token"
	firstPassword := "first-pass"

	view, err := svc.UpdateTransportConfig(context.Background(), TransportConfigUpdate{
		Transports: []TransportConfigEntryUpdate{
			{
				Name:    "First",
				Type:    TransportTypeNtfy,
				Enabled: true,
				Ntfy: &NtfyTransportConfigUpdate{
					BaseURL:   "https://ntfy.sh",
					Topic:     "first",
					AuthToken: &firstToken,
				},
			},
			{
				Name:    "Second",
				Type:    TransportTypeSMTP,
				Enabled: true,
				Email: &EmailTransportConfigUpdate{
					SMTPHost:     "smtp.first.local",
					SMTPPort:     587,
					SMTPFrom:     "first@example.com",
					Recipients:   []string{"first@example.com"},
					SMTPPassword: &firstPassword,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("update_initial_failed: %v", err)
	}

	if len(view.Transports) != 2 {
		t.Fatalf("expected_2_transports_after_initial_update got: %d", len(view.Transports))
	}

	keep := view.Transports[0]
	remove := view.Transports[1]

	var keepUpdate TransportConfigEntryUpdate
	if keep.Type == TransportTypeNtfy {
		if keep.Ntfy == nil {
			t.Fatalf("expected_ntfy_payload_for_ntfy_transport")
		}
		keepUpdate = TransportConfigEntryUpdate{
			ID:      keep.ID,
			Name:    keep.Name,
			Type:    keep.Type,
			Enabled: keep.Enabled,
			Ntfy: &NtfyTransportConfigUpdate{
				BaseURL: keep.Ntfy.BaseURL,
				Topic:   keep.Ntfy.Topic,
			},
		}
	} else {
		if keep.Email == nil {
			t.Fatalf("expected_email_payload_for_smtp_transport")
		}
		keepUpdate = TransportConfigEntryUpdate{
			ID:      keep.ID,
			Name:    keep.Name,
			Type:    keep.Type,
			Enabled: keep.Enabled,
			Email: &EmailTransportConfigUpdate{
				SMTPHost:   keep.Email.SMTPHost,
				SMTPPort:   keep.Email.SMTPPort,
				SMTPFrom:   keep.Email.SMTPFrom,
				Recipients: keep.Email.Recipients,
			},
		}
	}

	_, err = svc.UpdateTransportConfig(context.Background(), TransportConfigUpdate{
		Transports: []TransportConfigEntryUpdate{
			keepUpdate,
		},
	})
	if err != nil {
		t.Fatalf("update_with_omitted_row_failed: %v", err)
	}

	after, err := svc.GetTransportConfig(context.Background())
	if err != nil {
		t.Fatalf("get_after_failed: %v", err)
	}
	if len(after.Transports) != 1 {
		t.Fatalf("expected_1_transport_after_omit got: %d", len(after.Transports))
	}

	var remaining int64
	if err := svc.DB.Model(&models.NotificationTransportConfig{}).Where("id = ?", remove.ID).Count(&remaining).Error; err != nil {
		t.Fatalf("count_removed_transport_failed: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected_removed_transport_deleted")
	}

}

func TestGetRuleConfigAutoSyncsPools(t *testing.T) {
	svc := newTestService(t)

	if err := svc.DB.Create(&models.BasicSettings{Pools: []string{"zroot", "tank"}}).Error; err != nil {
		t.Fatalf("failed_to_seed_basic_settings: %v", err)
	}

	view, err := svc.GetRuleConfig(context.Background())
	if err != nil {
		t.Fatalf("get_rule_config_failed: %v", err)
	}

	if len(view.Templates) != 1 {
		t.Fatalf("expected_1_template got: %d", len(view.Templates))
	}
	if view.Templates[0].Key != RuleTemplateZFSPoolState {
		t.Fatalf("unexpected_template_key: %s", view.Templates[0].Key)
	}

	if len(view.Rules) != 2 {
		t.Fatalf("expected_2_rules got: %d", len(view.Rules))
	}
	rulesByTarget := map[string]RuleConfigEntryView{}
	for _, rule := range view.Rules {
		rulesByTarget[rule.TargetKey] = rule
		if rule.TemplateKey != RuleTemplateZFSPoolState {
			t.Fatalf("unexpected_template_key: %s", rule.TemplateKey)
		}
		if !rule.Active {
			t.Fatalf("expected_rule_active_for_target=%s", rule.TargetKey)
		}
		if rule.Kind != notifier.KindForZFSPoolState(rule.TargetKey) {
			t.Fatalf("unexpected_rule_kind target=%s kind=%s", rule.TargetKey, rule.Kind)
		}
		if !rule.UIEnabled || !rule.NtfyEnabled || !rule.EmailEnabled {
			t.Fatalf("expected_default_enabled_rule got: %+v", rule)
		}
	}

	if _, ok := rulesByTarget["tank"]; !ok {
		t.Fatalf("expected_tank_rule_present")
	}
	if _, ok := rulesByTarget["zroot"]; !ok {
		t.Fatalf("expected_zroot_rule_present")
	}
}

func TestGetRuleConfigMarksRemovedPoolsInactive(t *testing.T) {
	svc := newTestService(t)

	settings := models.BasicSettings{Pools: []string{"zroot", "tank"}}
	if err := svc.DB.Create(&settings).Error; err != nil {
		t.Fatalf("failed_to_seed_basic_settings: %v", err)
	}

	if _, err := svc.GetRuleConfig(context.Background()); err != nil {
		t.Fatalf("initial_get_rule_config_failed: %v", err)
	}

	settings.Pools = []string{"zroot"}
	if err := svc.DB.Save(&settings).Error; err != nil {
		t.Fatalf("failed_to_update_basic_settings: %v", err)
	}

	view, err := svc.GetRuleConfig(context.Background())
	if err != nil {
		t.Fatalf("get_rule_config_failed: %v", err)
	}

	if len(view.Rules) != 2 {
		t.Fatalf("expected_2_rules_including_inactive got: %d", len(view.Rules))
	}

	statusByTarget := map[string]bool{}
	for _, rule := range view.Rules {
		statusByTarget[rule.TargetKey] = rule.Active
	}

	if !statusByTarget["zroot"] {
		t.Fatalf("expected_zroot_rule_active")
	}
	if statusByTarget["tank"] {
		t.Fatalf("expected_tank_rule_inactive")
	}
}

func TestUpdateRuleConfigPersistsChanges(t *testing.T) {
	svc := newTestService(t)
	if err := svc.DB.Create(&models.BasicSettings{Pools: []string{"zroot"}}).Error; err != nil {
		t.Fatalf("failed_to_seed_basic_settings: %v", err)
	}

	view, err := svc.GetRuleConfig(context.Background())
	if err != nil {
		t.Fatalf("get_rule_config_failed: %v", err)
	}
	if len(view.Rules) != 1 {
		t.Fatalf("expected_single_rule_before_update got: %d", len(view.Rules))
	}
	ruleID := view.Rules[0].ID

	updated, err := svc.UpdateRuleConfig(context.Background(), RuleConfigUpdate{
		Rules: []RuleConfigEntryUpdate{
			{
				ID:           ruleID,
				UIEnabled:    false,
				NtfyEnabled:  false,
				EmailEnabled: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("update_rule_config_failed: %v", err)
	}

	if len(updated.Rules) != 1 {
		t.Fatalf("expected_single_rule_after_update got: %d", len(updated.Rules))
	}

	rule := updated.Rules[0]
	if rule.UIEnabled || rule.NtfyEnabled || !rule.EmailEnabled {
		t.Fatalf("unexpected_rule_state_after_update: %+v", rule)
	}

	var stored models.NotificationKindRule
	kind := notifier.KindForZFSPoolState("zroot")
	if err := svc.DB.Where("kind = ?", kind).First(&stored).Error; err != nil {
		t.Fatalf("failed_to_load_updated_rule: %v", err)
	}
	if stored.UIEnabled || stored.NtfyEnabled || !stored.EmailEnabled {
		t.Fatalf("unexpected_stored_rule_state: %+v", stored)
	}
}

func TestDeleteRuleRecreatesActivePoolRule(t *testing.T) {
	svc := newTestService(t)
	if err := svc.DB.Create(&models.BasicSettings{Pools: []string{"zroot"}}).Error; err != nil {
		t.Fatalf("failed_to_seed_basic_settings: %v", err)
	}

	view, err := svc.GetRuleConfig(context.Background())
	if err != nil {
		t.Fatalf("get_rule_config_failed: %v", err)
	}
	if len(view.Rules) != 1 {
		t.Fatalf("expected_single_rule got: %d", len(view.Rules))
	}

	updated, err := svc.DeleteRule(context.Background(), view.Rules[0].ID)
	if err != nil {
		t.Fatalf("delete_rule_failed: %v", err)
	}

	if len(updated.Rules) != 1 {
		t.Fatalf("expected_single_rule_after_delete got: %d", len(updated.Rules))
	}
	if updated.Rules[0].TargetKey != "zroot" {
		t.Fatalf("expected_zroot_rule_after_delete got: %s", updated.Rules[0].TargetKey)
	}
	if !updated.Rules[0].Active {
		t.Fatalf("expected_rule_active_after_delete")
	}
}

func TestUpdateRuleConfigRejectsUnknownPool(t *testing.T) {
	svc := newTestService(t)
	if err := svc.DB.Create(&models.BasicSettings{Pools: []string{"zroot"}}).Error; err != nil {
		t.Fatalf("failed_to_seed_basic_settings: %v", err)
	}

	_, err := svc.UpdateRuleConfig(context.Background(), RuleConfigUpdate{
		Rules: []RuleConfigEntryUpdate{
			{
				Pool:         "tank",
				UIEnabled:    true,
				NtfyEnabled:  true,
				EmailEnabled: true,
			},
		},
	})
	if err == nil {
		t.Fatalf("expected_error_for_unknown_pool_rule")
	}
	if err.Error() != "notification_rule_not_found" {
		t.Fatalf("expected_notification_rule_not_found got: %v", err)
	}
}

func TestCreateRuleRejectsDuplicateAutoManagedRule(t *testing.T) {
	svc := newTestService(t)
	if err := svc.DB.Create(&models.BasicSettings{Pools: []string{"zroot"}}).Error; err != nil {
		t.Fatalf("failed_to_seed_basic_settings: %v", err)
	}

	if _, err := svc.GetRuleConfig(context.Background()); err != nil {
		t.Fatalf("get_rule_config_failed: %v", err)
	}

	_, err := svc.CreateRule(context.Background(), RuleCreateInput{
		TemplateKey:  RuleTemplateZFSPoolState,
		TargetKey:    "zroot",
		UIEnabled:    true,
		NtfyEnabled:  true,
		EmailEnabled: true,
	})
	if err == nil {
		t.Fatalf("expected_duplicate_rule_error")
	}
	if err.Error() != "notification_rule_already_exists" {
		t.Fatalf("expected_notification_rule_already_exists got: %v", err)
	}
}

func TestEmitHonorsUIAndChannelRuleToggles(t *testing.T) {
	svc := newTestService(t)
	if err := svc.DB.Create(&models.BasicSettings{Pools: []string{"zroot"}}).Error; err != nil {
		t.Fatalf("failed_to_seed_basic_settings: %v", err)
	}

	ntfyCalls := 0
	emailCalls := 0
	svc.SetNtfySender(func(ctx context.Context, cfg models.NotificationTransportConfig, input notifier.EventInput, token string) error {
		ntfyCalls++
		return nil
	})
	svc.SetEmailSender(func(ctx context.Context, cfg models.NotificationTransportConfig, input notifier.EventInput, password string) error {
		emailCalls++
		return nil
	})

	_, err := svc.UpdateTransportConfig(context.Background(), TransportConfigUpdate{
		Transports: []TransportConfigEntryUpdate{
			{
				Name:    "ntfy",
				Type:    TransportTypeNtfy,
				Enabled: true,
				Ntfy: &NtfyTransportConfigUpdate{
					BaseURL: "https://ntfy.sh",
					Topic:   "ops",
				},
			},
			{
				Name:    "smtp",
				Type:    TransportTypeSMTP,
				Enabled: true,
				Email: &EmailTransportConfigUpdate{
					SMTPHost:   "smtp.example.com",
					SMTPPort:   587,
					SMTPFrom:   "ops@example.com",
					Recipients: []string{"ops@example.com"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("failed_to_seed_transport_config: %v", err)
	}

	kind := notifier.KindForZFSPoolState("zroot")
	_, err = svc.UpdateRuleConfig(context.Background(), RuleConfigUpdate{
		Rules: []RuleConfigEntryUpdate{
			{
				Pool:         "zroot",
				UIEnabled:    false,
				NtfyEnabled:  false,
				EmailEnabled: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("failed_to_update_kind_rule: %v", err)
	}

	res, err := svc.Emit(context.Background(), notifier.EventInput{
		Kind:        kind,
		Title:       "Pool degraded",
		Body:        "zroot has degraded vdev",
		Severity:    "warning",
		Fingerprint: "zroot|vdev0|degraded",
	})
	if err != nil {
		t.Fatalf("emit_failed: %v", err)
	}

	if res.NotificationID != 0 {
		t.Fatalf("expected_no_ui_notification_id_when_ui_disabled got: %d", res.NotificationID)
	}
	if ntfyCalls != 0 {
		t.Fatalf("expected_ntfy_not_sent_when_kind_ntfy_disabled got: %d", ntfyCalls)
	}
	if emailCalls != 1 {
		t.Fatalf("expected_email_sent_once got: %d", emailCalls)
	}

	var count int64
	if err := svc.DB.Model(&models.Notification{}).Count(&count).Error; err != nil {
		t.Fatalf("failed_to_count_notifications: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected_no_ui_notifications_stored got: %d", count)
	}
}
