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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
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

	svc := NewService(db)
	notifier.SetEmitter(svc)
	return svc
}

type mockDiskService struct {
	disks []diskServiceInterfaces.Disk
}

type trackingInventoryDiskService struct {
	mockDiskService
	inventoryCalls int
	fullCalls      int
}

func (m *trackingInventoryDiskService) GetDiskDevices(ctx context.Context) ([]diskServiceInterfaces.Disk, error) {
	m.fullCalls++
	return m.disks, nil
}

func (m *trackingInventoryDiskService) GetDiskDevicesInventory(ctx context.Context) ([]diskServiceInterfaces.Disk, error) {
	m.inventoryCalls++
	return m.disks, nil
}

func (m *mockDiskService) GetDiskDevices(ctx context.Context) ([]diskServiceInterfaces.Disk, error) {
	return m.disks, nil
}

func (m *mockDiskService) GetDiskDevicesInventory(ctx context.Context) ([]diskServiceInterfaces.Disk, error) {
	return m.disks, nil
}

func (m *mockDiskService) GetSmartData(disk diskServiceInterfaces.DiskInfo) (any, *diskServiceInterfaces.DiskSelfTestLog, error) {
	return nil, nil, nil
}

func (m *mockDiskService) GetWearOut(disk any) (float64, error) {
	return 0, nil
}

func (m *mockDiskService) GetDiskSize(device string) (uint64, error) {
	return 0, nil
}

func (m *mockDiskService) DestroyPartitionTable(device string) error {
	return nil
}

func (m *mockDiskService) IsDiskGPT(device string) bool {
	return false
}

func (m *mockDiskService) RunSelfTest(disk diskServiceInterfaces.DiskInfo, testType string) error {
	return nil
}

func (m *mockDiskService) GetSelfTestLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskSelfTestLog, error) {
	return &diskServiceInterfaces.DiskSelfTestLog{}, nil
}

func (m *mockDiskService) GetErrorLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskErrorLog, error) {
	return &diskServiceInterfaces.DiskErrorLog{}, nil
}

func (m *mockDiskService) GetNVMEErrorLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskNVMEErrorLog, error) {
	return &diskServiceInterfaces.DiskNVMEErrorLog{}, nil
}

func (m *mockDiskService) GetSCTStatus(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskSCTStatus, error) {
	return &diskServiceInterfaces.DiskSCTStatus{}, nil
}

func (m *mockDiskService) GetSCTTempHistory(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskSCTTempHistory, error) {
	return &diskServiceInterfaces.DiskSCTTempHistory{}, nil
}

func (m *mockDiskService) AbortSelfTest(disk diskServiceInterfaces.DiskInfo) error {
	return nil
}

func (m *mockDiskService) GetExtendedSelfTestLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskSelfTestLog, error) {
	return &diskServiceInterfaces.DiskSelfTestLog{}, nil
}

func (m *mockDiskService) GetExtendedErrorLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskErrorLog, error) {
	return &diskServiceInterfaces.DiskErrorLog{}, nil
}

func (m *mockDiskService) GetLogDirectory(disk diskServiceInterfaces.DiskInfo) ([]uint8, error) {
	return nil, nil
}

func (m *mockDiskService) GetDeviceStatistics(disk diskServiceInterfaces.DiskInfo) ([]diskServiceInterfaces.DiskAttribute, error) {
	return nil, nil
}

func (m *mockDiskService) GetSelectiveSelfTestLog(disk diskServiceInterfaces.DiskInfo) (*diskServiceInterfaces.DiskSelfTestLog, error) {
	return &diskServiceInterfaces.DiskSelfTestLog{}, nil
}

func (m *mockDiskService) SetSCTFeatureControl(disk diskServiceInterfaces.DiskInfo, featureCode uint16, state uint16, persistent bool) error {
	return nil
}

func (m *mockDiskService) SetSCTErrorRecoveryControl(disk diskServiceInterfaces.DiskInfo, read bool, timeLimit uint16) error {
	return nil
}

func newTestServiceWithDisks(t *testing.T, disks []diskServiceInterfaces.Disk) *Service {
	t.Helper()
	svc := newTestService(t)
	svc.SetDiskService(&mockDiskService{disks: disks})
	return svc
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

func TestEmitReportsTotalTransportFailureWhenUIIsDisabled(t *testing.T) {
	svc := newTestService(t)
	kind := "system.test.transport_failure"
	rule := models.NotificationKindRule{Kind: kind, UIEnabled: true, NtfyEnabled: true}
	if err := svc.DB.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Model(&rule).Update("ui_enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateTransportConfig(context.Background(), TransportConfigUpdate{
		Transports: []TransportConfigEntryUpdate{
			{Name: "ntfy", Type: TransportTypeNtfy, Enabled: true, Ntfy: &NtfyTransportConfigUpdate{BaseURL: "https://ntfy.sh", Topic: "ops"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	svc.SetNtfySender(func(context.Context, models.NotificationTransportConfig, notifier.EventInput, string) error {
		calls++
		return errors.New("transport unavailable")
	})
	result, err := svc.Emit(context.Background(), notifier.EventInput{Kind: kind, Title: "Transport failure", Fingerprint: "transport-failure"})
	if err == nil || result.SentNtfy || result.NotificationID != 0 || calls != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
	}
	var count int64
	if err := svc.DB.Model(&models.Notification{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("notifications=%d", count)
	}
}

func TestTargetedDeliveryAddressesOneTransportRow(t *testing.T) {
	svc := newTestService(t)
	kind := "system.test.targeted_delivery"
	rule := models.NotificationKindRule{Kind: kind, NtfyEnabled: true}
	if err := svc.DB.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Model(&rule).Update("ui_enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	view, err := svc.UpdateTransportConfig(context.Background(), TransportConfigUpdate{
		Transports: []TransportConfigEntryUpdate{
			{Name: "first", Type: TransportTypeNtfy, Enabled: true, Ntfy: &NtfyTransportConfigUpdate{BaseURL: "https://ntfy.sh", Topic: "first"}},
			{Name: "second", Type: TransportTypeNtfy, Enabled: true, Ntfy: &NtfyTransportConfigUpdate{BaseURL: "https://ntfy.sh", Topic: "second"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := notifier.EventInput{Kind: kind, Title: "targeted", Fingerprint: "targeted"}
	targets, err := svc.DeliveryTargets(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Transports) != 2 || len(targets) != 2 {
		t.Fatalf("transports=%+v targets=%v", view.Transports, targets)
	}
	calls := map[uint]int{}
	svc.SetNtfySender(func(_ context.Context, cfg models.NotificationTransportConfig, _ notifier.EventInput, _ string) error {
		calls[cfg.ID]++
		return nil
	})
	if _, err := svc.EmitTarget(context.Background(), input, targets[1]); err != nil {
		t.Fatal(err)
	}
	if calls[view.Transports[0].ID] != 0 || calls[view.Transports[1].ID] != 1 {
		t.Fatalf("calls=%v", calls)
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

	if len(updated.Rules) != 0 {
		t.Fatalf("expected_no_rules_after_delete got: %d", len(updated.Rules))
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

func TestDismissDoesNotPersistSuppressionForZFSPoolState(t *testing.T) {
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
		t.Fatalf("update_transport_config_failed: %v", err)
	}

	input := notifier.EventInput{
		Kind:        notifier.KindForZFSPoolState("test"),
		Title:       "Pool degraded",
		Body:        "test pool degraded",
		Severity:    "warning",
		Fingerprint: "test|vdev0|degraded",
	}

	first, err := svc.Emit(context.Background(), input)
	if err != nil {
		t.Fatalf("first_emit_failed: %v", err)
	}
	if first.Suppressed {
		t.Fatalf("expected_first_emit_not_suppressed")
	}

	if err := svc.Dismiss(context.Background(), first.NotificationID); err != nil {
		t.Fatalf("dismiss_failed: %v", err)
	}

	second, err := svc.Emit(context.Background(), input)
	if err != nil {
		t.Fatalf("second_emit_failed: %v", err)
	}
	if second.Suppressed {
		t.Fatalf("expected_zfs_emit_not_suppressed_after_dismiss")
	}

	if ntfyCalls != 2 {
		t.Fatalf("expected_ntfy_called_twice got: %d", ntfyCalls)
	}
	if emailCalls != 2 {
		t.Fatalf("expected_email_called_twice got: %d", emailCalls)
	}

	var suppressions int64
	if err := svc.DB.Model(&models.NotificationSuppression{}).
		Where("kind = ?", input.Kind).
		Count(&suppressions).Error; err != nil {
		t.Fatalf("failed_to_count_suppressions: %v", err)
	}
	if suppressions != 0 {
		t.Fatalf("expected_no_suppression_rows_for_zfs_kind got: %d", suppressions)
	}
}

func TestTestRuleEmitsThroughPipeline(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test SSD", Type: "SSD", SmartData: diskServiceInterfaces.SmartData{}},
	})

	if err := svc.DB.Create(&models.BasicSettings{Pools: []string{"zroot"}}).Error; err != nil {
		t.Fatalf("failed_to_seed_basic_settings: %v", err)
	}
	if _, err := svc.GetRuleConfig(context.Background()); err != nil {
		t.Fatalf("seed_rule_config_failed: %v", err)
	}

	err := svc.TestRule(context.Background(), TestRuleInput{
		TemplateKey: RuleTemplateDiskSmartTemperature,
		TargetKey:   "ada0",
		Condition:   "temperature_warning",
	})
	if err != nil {
		t.Fatalf("test_rule_failed: %v", err)
	}

	var count int64
	if err := svc.DB.Model(&models.Notification{}).Count(&count).Error; err != nil {
		t.Fatalf("failed_to_count_notifications: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected_1_notification_in_db got: %d", count)
	}
}

func TestDiskSmartSelfTestDismissalDoesNotPersistSuppression(t *testing.T) {
	kind := notifier.KindForDiskSmart(notifier.DiskSmartSelfTestKindPrefix, "ada0")
	if shouldPersistSuppressionForKind(kind) {
		t.Fatalf("self_test_kind_should_not_persist_suppression=%s", kind)
	}
}

func TestTestRuleSendsThroughTransports(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test SSD", Type: "SSD", SmartData: diskServiceInterfaces.SmartData{}},
	})

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
				Ntfy:    &NtfyTransportConfigUpdate{BaseURL: "https://ntfy.sh", Topic: "test"},
			},
			{
				Name:    "smtp",
				Type:    TransportTypeSMTP,
				Enabled: true,
				Email:   &EmailTransportConfigUpdate{SMTPHost: "localhost", SMTPPort: 1025, SMTPFrom: "t@t.com", Recipients: []string{"t@t.com"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("update_transport_config_failed: %v", err)
	}

	if err := svc.TestRule(context.Background(), TestRuleInput{
		TemplateKey: RuleTemplateDiskSmartWearout,
		TargetKey:   "ada0",
		Condition:   "wearout_critical",
	}); err != nil {
		t.Fatalf("test_rule_failed: %v", err)
	}

	if ntfyCalls != 1 {
		t.Fatalf("expected_ntfy_called_once got: %d", ntfyCalls)
	}
	if emailCalls != 1 {
		t.Fatalf("expected_email_called_once got: %d", emailCalls)
	}
}

func TestTestRuleRejectsUnknownTemplate(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test SSD", Type: "SSD", SmartData: diskServiceInterfaces.SmartData{}},
	})

	err := svc.TestRule(context.Background(), TestRuleInput{
		TemplateKey: "system.nonexistent",
		TargetKey:   "ada0",
	})
	if err == nil {
		t.Fatalf("expected_error_for_unknown_template")
	}
	if err.Error() != "notification_rule_template_not_found" {
		t.Fatalf("expected_template_not_found got: %v", err)
	}
}

func TestTestRuleFallsBackToFirstTarget(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test SSD", Type: "SSD", SmartData: diskServiceInterfaces.SmartData{}},
		{Device: "ada1", Model: "Test HDD", Type: "HDD", SmartData: diskServiceInterfaces.SmartData{}},
	})

	if err := svc.TestRule(context.Background(), TestRuleInput{
		TemplateKey: RuleTemplateDiskSmartTemperature,
	}); err != nil {
		t.Fatalf("test_rule_without_target_failed: %v", err)
	}
}

func TestTestRuleRejectsTargetNotInTemplate(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test SSD", Type: "SSD", SmartData: diskServiceInterfaces.SmartData{}},
	})

	err := svc.TestRule(context.Background(), TestRuleInput{
		TemplateKey: RuleTemplateDiskSmartTemperature,
		TargetKey:   "nonexistent",
	})
	if err == nil {
		t.Fatalf("expected_error_for_unknown_target")
	}
	if err.Error() != "notification_rule_target_not_found" {
		t.Fatalf("expected_target_not_found got: %v", err)
	}
}

func TestDiskSmartTemplatesAppearWhenDiskServiceSet(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test SSD", Type: "SSD", SmartData: diskServiceInterfaces.SmartData{}},
		{Device: "nda0", Model: "Test NVMe", Type: "NVMe", SmartData: &diskServiceInterfaces.SMARTNvme{}},
	})

	view, err := svc.GetRuleConfig(context.Background())
	if err != nil {
		t.Fatalf("get_rule_config_failed: %v", err)
	}

	if len(view.Templates) < 6 {
		t.Fatalf("expected_at_least_6_templates got: %d", len(view.Templates))
	}

	templateKeys := map[string]bool{}
	templateLabels := map[string]string{}
	for _, tpl := range view.Templates {
		templateKeys[tpl.Key] = true
		templateLabels[tpl.Key] = tpl.Label
	}
	for _, key := range []string{
		RuleTemplateDiskSmartTemperature,
		RuleTemplateDiskSmartWearout,
		RuleTemplateDiskSmartHealth,
		RuleTemplateDiskSmartNvme,
		RuleTemplateDiskSmartSelfTest,
		RuleTemplateZFSPoolState,
	} {
		if !templateKeys[key] {
			t.Fatalf("expected_template_%s", key)
		}
	}
	for key, label := range map[string]string{
		RuleTemplateDiskSmartTemperature: "Disk S.M.A.R.T Temperature",
		RuleTemplateDiskSmartWearout:     "Disk S.M.A.R.T Wear-Out",
		RuleTemplateDiskSmartHealth:      "Disk S.M.A.R.T Health",
		RuleTemplateDiskSmartSelfTest:    "Disk S.M.A.R.T Self-Test",
	} {
		if templateLabels[key] != label {
			t.Fatalf("unexpected_template_label key=%s got=%q want=%q", key, templateLabels[key], label)
		}
	}
	foundSelfTestRule := false
	for _, rule := range view.Rules {
		if rule.TemplateKey == RuleTemplateDiskSmartSelfTest && rule.TargetKey == "ada0" {
			foundSelfTestRule = true
			break
		}
	}
	if !foundSelfTestRule {
		t.Fatal("expected_managed_self_test_rule")
	}
}

func TestDiskSmartTemplateDiscoveryUsesInventory(t *testing.T) {
	svc := newTestService(t)
	diskService := &trackingInventoryDiskService{mockDiskService: mockDiskService{disks: []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test HDD", Type: "HDD"},
	}}}
	svc.SetDiskService(diskService)
	if _, err := svc.GetRuleConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if diskService.inventoryCalls != 1 || diskService.fullCalls != 0 {
		t.Fatalf("inventory_calls=%d full_calls=%d", diskService.inventoryCalls, diskService.fullCalls)
	}
}

func TestDiskSmartSelfTestLegacyKindMigration(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test HDD", Type: "HDD"},
	})
	legacyKind := RuleTemplateDiskSmartSelfTest + "ada0"
	newKind := notifier.KindForDiskSmart(notifier.DiskSmartSelfTestKindPrefix, "ada0")
	legacyRule := models.NotificationKindRule{
		Kind:           legacyKind,
		UIEnabled:      true,
		NtfyEnabled:    false,
		EmailEnabled:   true,
		DiscordEnabled: true,
		Config:         `{"legacy":true}`,
	}
	if err := svc.DB.Create(&legacyRule).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Model(&legacyRule).Update("ui_enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	now := svc.now().UTC()
	legacyNotification := models.Notification{
		Kind:            legacyKind,
		Title:           "legacy self-test failure",
		Severity:        models.NotificationSeverityWarning,
		Fingerprint:     "legacy-self-test-failure",
		OccurrenceCount: 1,
		FirstOccurredAt: now,
		LastOccurredAt:  now,
	}
	if err := svc.DB.Create(&legacyNotification).Error; err != nil {
		t.Fatal(err)
	}
	legacySuppression := models.NotificationSuppression{
		Kind:        legacyKind,
		Fingerprint: "legacy-self-test-suppression",
	}
	if err := svc.DB.Create(&legacySuppression).Error; err != nil {
		t.Fatal(err)
	}

	view, err := svc.GetRuleConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var migratedRule models.NotificationKindRule
	if err := svc.DB.Where("kind = ?", newKind).First(&migratedRule).Error; err != nil {
		t.Fatal(err)
	}
	if migratedRule.ID != legacyRule.ID || migratedRule.UIEnabled || migratedRule.NtfyEnabled || !migratedRule.EmailEnabled || !migratedRule.DiscordEnabled || migratedRule.Config != `{"legacy":true}` {
		t.Fatalf("legacy_rule_settings_not_preserved: %+v", migratedRule)
	}
	var legacyRuleCount int64
	if err := svc.DB.Model(&models.NotificationKindRule{}).Where("kind = ?", legacyKind).Count(&legacyRuleCount).Error; err != nil {
		t.Fatal(err)
	}
	if legacyRuleCount != 0 {
		t.Fatalf("legacy_rule_not_removed count=%d", legacyRuleCount)
	}
	var migratedNotification models.Notification
	if err := svc.DB.First(&migratedNotification, legacyNotification.ID).Error; err != nil {
		t.Fatal(err)
	}
	if migratedNotification.Kind != newKind {
		t.Fatalf("legacy_notification_kind=%q", migratedNotification.Kind)
	}
	var migratedSuppression models.NotificationSuppression
	if err := svc.DB.First(&migratedSuppression, legacySuppression.ID).Error; err != nil {
		t.Fatal(err)
	}
	if migratedSuppression.Kind != newKind {
		t.Fatalf("legacy_suppression_kind=%q", migratedSuppression.Kind)
	}
	found := false
	for _, rule := range view.Rules {
		if rule.ID == legacyRule.ID && rule.TemplateKey == RuleTemplateDiskSmartSelfTest && rule.TargetKey == "ada0" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("migrated_self_test_rule_missing_from_view")
	}
}

func TestDiskSmartSelfTestLegacySettingsOverrideNewAutomaticRule(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{{Device: "ada0", Type: "HDD"}})
	legacyKind := RuleTemplateDiskSmartSelfTest + "ada0"
	newKind := notifier.KindForDiskSmart(notifier.DiskSmartSelfTestKindPrefix, "ada0")
	legacy := models.NotificationKindRule{
		Kind: legacyKind, UIEnabled: true, NtfyEnabled: true, EmailEnabled: true, DiscordEnabled: true, Config: `{"legacy":true}`,
	}
	if err := svc.DB.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Model(&legacy).Updates(map[string]any{"ui_enabled": false, "ntfy_enabled": false, "email_enabled": false, "user_disabled": true}).Error; err != nil {
		t.Fatal(err)
	}
	current := models.NotificationKindRule{
		Kind: newKind, UIEnabled: true, NtfyEnabled: true, EmailEnabled: true, DiscordEnabled: false, Config: "{}",
	}
	if err := svc.DB.Create(&current).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetRuleConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	var migrated models.NotificationKindRule
	if err := svc.DB.First(&migrated, current.ID).Error; err != nil {
		t.Fatal(err)
	}
	if migrated.UIEnabled || migrated.NtfyEnabled || migrated.EmailEnabled || !migrated.DiscordEnabled || !migrated.UserDisabled || migrated.Config != `{"legacy":true}` {
		t.Fatalf("migrated=%+v", migrated)
	}
	var count int64
	if err := svc.DB.Model(&models.NotificationKindRule{}).Where("kind = ?", legacyKind).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("legacy_count=%d", count)
	}
}

func TestDiskSmartStableIdentityMigration(t *testing.T) {
	const diskKey = "d782e080-43c1-5abc-9def-123456789abc"
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{UUID: diskKey, IdentityStable: true, Device: "ada0", Model: "Stable SSD", Type: "SSD"},
	})
	oldKind := notifier.KindForDiskSmart(notifier.DiskSmartTemperatureKindPrefix, "ada0")
	newKind := notifier.KindForDiskSmart(notifier.DiskSmartTemperatureKindPrefix, diskKey)
	rule := models.NotificationKindRule{
		Kind: oldKind, UIEnabled: true, NtfyEnabled: true, EmailEnabled: true, DiscordEnabled: true,
		Config: `{"warningCelsius":45,"criticalCelsius":60}`,
	}
	if err := svc.DB.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Model(&rule).Updates(map[string]any{"ui_enabled": false, "ntfy_enabled": false}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	notification := models.Notification{
		Kind: oldKind, Title: "temperature", Severity: models.NotificationSeverityWarning,
		Source: "system.disk.smart", Fingerprint: "ada0|temperature_warning",
		Metadata:        map[string]string{"device": "ada0", "condition": "temperature_warning"},
		OccurrenceCount: 1, FirstOccurredAt: now, LastOccurredAt: now,
	}
	if err := svc.DB.Create(&notification).Error; err != nil {
		t.Fatal(err)
	}
	view, err := svc.GetRuleConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var migrated models.NotificationKindRule
	if err := svc.DB.Where("kind = ?", newKind).First(&migrated).Error; err != nil {
		t.Fatal(err)
	}
	if migrated.UIEnabled || migrated.NtfyEnabled || !migrated.EmailEnabled || !migrated.DiscordEnabled || migrated.Config != rule.Config {
		t.Fatalf("migrated=%+v", migrated)
	}
	var stored models.Notification
	if err := svc.DB.First(&stored, notification.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Kind != newKind || stored.Fingerprint != diskKey+"|temperature" || stored.Metadata["device"] != "ada0" || stored.Metadata["disk_key"] != diskKey {
		t.Fatalf("stored=%+v", stored)
	}
	found := false
	for _, entry := range view.Rules {
		if entry.TemplateKey == RuleTemplateDiskSmartTemperature && entry.TargetKey == diskKey && entry.TargetLabel == "ada0 (Stable SSD)" {
			found = true
		}
	}
	if !found {
		t.Fatalf("view=%+v", view)
	}
}

func TestDiskSmartLegacyConditionMigration(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test HDD", Type: "HDD"},
	})
	now := svc.now().UTC()
	current := models.Notification{
		Kind:            notifier.KindForDiskSmart(notifier.DiskSmartTemperatureKindPrefix, "ada0"),
		Title:           "current temperature warning",
		Severity:        models.NotificationSeverityWarning,
		Source:          "system.disk.smart",
		Fingerprint:     "ada0|temperature_warning",
		Metadata:        map[string]string{"device": "ada0", "condition": "temperature_warning"},
		OccurrenceCount: 3,
		FirstOccurredAt: now.Add(-2 * time.Hour),
		LastOccurredAt:  now.Add(-time.Hour),
	}
	legacy := models.Notification{
		Kind:            current.Kind,
		Title:           "legacy temperature warning",
		Severity:        models.NotificationSeverityWarning,
		Source:          "system.disk.smart",
		Fingerprint:     "ada0|high temperature",
		Metadata:        map[string]string{"device": "ada0", "condition": "high temperature"},
		OccurrenceCount: 2,
		FirstOccurredAt: now.Add(-3 * time.Hour),
		LastOccurredAt:  now,
	}
	if err := svc.DB.Create(&current).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetRuleConfig(context.Background()); err != nil {
		t.Fatal(err)
	}

	var stored []models.Notification
	if err := svc.DB.Where("source = ?", "system.disk.smart").Find(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 {
		t.Fatalf("expected_1_merged_notification got=%d", len(stored))
	}
	merged := stored[0]
	if merged.ID != current.ID || merged.Fingerprint != "ada0|temperature" || merged.Metadata["condition"] != "temperature_warning" {
		t.Fatalf("legacy_condition_not_normalized: %+v", merged)
	}
	if merged.OccurrenceCount != 5 || !merged.FirstOccurredAt.Equal(now.Add(-3*time.Hour)) || !merged.LastOccurredAt.Equal(now) {
		t.Fatalf("legacy_notification_not_merged: %+v", merged)
	}
	if merged.Title != legacy.Title {
		t.Fatalf("newest_notification_content_not_preserved got=%q want=%q", merged.Title, legacy.Title)
	}
}

func TestDiskSmartLegacyConditionMigrationReloadsMergedWinner(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test HDD", Type: "HDD"},
	})
	now := svc.now().UTC()
	legacy := models.Notification{
		Kind:            notifier.KindForDiskSmart(notifier.DiskSmartTemperatureKindPrefix, "ada0"),
		Title:           "legacy temperature warning",
		Severity:        models.NotificationSeverityWarning,
		Source:          "system.disk.smart",
		Fingerprint:     "ada0|high temperature",
		Metadata:        map[string]string{"device": "ada0", "condition": "high temperature"},
		OccurrenceCount: 2,
		FirstOccurredAt: now.Add(-3 * time.Hour),
		LastOccurredAt:  now,
	}
	current := models.Notification{
		Kind:            legacy.Kind,
		Title:           "current temperature warning",
		Severity:        models.NotificationSeverityWarning,
		Source:          "system.disk.smart",
		Fingerprint:     "ada0|temperature",
		Metadata:        map[string]string{"device": "ada0", "condition": "temperature_warning"},
		OccurrenceCount: 3,
		FirstOccurredAt: now.Add(-2 * time.Hour),
		LastOccurredAt:  now.Add(-time.Hour),
	}
	if err := svc.DB.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Create(&current).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetRuleConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	var stored []models.Notification
	if err := svc.DB.Where("source = ?", "system.disk.smart").Find(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].ID != current.ID || stored[0].OccurrenceCount != 5 || stored[0].Title != legacy.Title || stored[0].Metadata["condition"] != "temperature_warning" {
		t.Fatalf("stored=%+v", stored)
	}
}

func TestLegacyDiskSmartCondition(t *testing.T) {
	tests := map[string]string{
		"smart unavailable":      "smart_unavailable",
		"critical temperature":   "temperature_critical",
		"high temperature":       "temperature_warning",
		"temperature normal":     "temperature_normal",
		"SMART health failed":    "health_failed",
		"SMART health recovered": "health_recovered",
		"critical wear-out":      "wearout_critical",
		"high wear-out":          "wearout_warning",
		"wear-out normal":        "wearout_normal",
		"sector issues":          "sector_issues",
		"sector issues cleared":  "sector_issues_cleared",
		"NVMe warning":           "nvme_warning",
		"NVMe recovered":         "nvme_recovered",
	}
	for legacy, expected := range tests {
		actual, ok := legacyDiskSmartCondition(legacy)
		if !ok || actual != expected {
			t.Fatalf("legacy=%q got=%q ok=%t want=%q", legacy, actual, ok, expected)
		}
	}
	if condition, ok := legacyDiskSmartCondition("temperature_warning"); ok || condition != "" {
		t.Fatalf("stable_condition_was_treated_as_legacy=%q", condition)
	}
}

func TestDiskSmartStableIdentityMigrationPrefersDeviceRuleOverAutomaticRule(t *testing.T) {
	const diskKey = "d782e080-43c1-5abc-9def-123456789abc"
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{UUID: diskKey, IdentityStable: true, Device: "ada0", Model: "Stable SSD", Type: "SSD"},
	})
	oldKind := notifier.KindForDiskSmart(notifier.DiskSmartTemperatureKindPrefix, "ada0")
	newKind := notifier.KindForDiskSmart(notifier.DiskSmartTemperatureKindPrefix, diskKey)
	oldRule := models.NotificationKindRule{
		Kind: oldKind, UIEnabled: true, NtfyEnabled: true, EmailEnabled: true, DiscordEnabled: true,
		Config: `{"warningCelsius":45,"criticalCelsius":60}`,
	}
	if err := svc.DB.Create(&oldRule).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Model(&oldRule).Updates(map[string]any{"ui_enabled": false, "ntfy_enabled": false, "email_enabled": false, "user_disabled": true}).Error; err != nil {
		t.Fatal(err)
	}
	current := models.NotificationKindRule{
		Kind: newKind, UIEnabled: true, NtfyEnabled: true, EmailEnabled: true,
		Config: `{"criticalCelsius":65,"warningCelsius":55}`,
	}
	if err := svc.DB.Create(&current).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := svc.GetRuleConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	var migrated models.NotificationKindRule
	if err := svc.DB.First(&migrated, current.ID).Error; err != nil {
		t.Fatal(err)
	}
	if migrated.UIEnabled || migrated.NtfyEnabled || migrated.EmailEnabled || !migrated.DiscordEnabled || !migrated.UserDisabled || migrated.Config != oldRule.Config {
		t.Fatalf("migrated=%+v", migrated)
	}
	var count int64
	if err := svc.DB.Model(&models.NotificationKindRule{}).Where("kind = ?", oldKind).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("old_rule_count=%d", count)
	}
}

func TestNotificationRuleReplacementPreservesNewestCustomSettings(t *testing.T) {
	now := time.Now().UTC()
	current := models.NotificationKindRule{Kind: "current", Config: `{"custom":true}`, UpdatedAt: now}
	candidate := models.NotificationKindRule{Kind: "candidate", Config: `{"custom":true}`, UpdatedAt: now.Add(-time.Minute)}
	if notificationRuleShouldReplace(current, candidate) {
		t.Fatal("older_candidate_replaced_current")
	}
	candidate.UpdatedAt = now.Add(time.Minute)
	if !notificationRuleShouldReplace(current, candidate) {
		t.Fatal("newer_candidate_was_not_selected")
	}
}

func TestDiskSmartTemplatesIncludeInventoryDisksWithoutCurrentData(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test SSD", Type: "SSD", SmartData: diskServiceInterfaces.SmartData{}},
		{Device: "ada1", Model: "Test HDD", Type: "HDD", SmartData: nil},
		{Device: "cd0", Model: "Test Optical", Type: "Optical", SmartData: nil},
	})

	view, err := svc.GetRuleConfig(context.Background())
	if err != nil {
		t.Fatalf("get_rule_config_failed: %v", err)
	}

	foundUnavailable := false
	for _, tpl := range view.Templates {
		for _, target := range tpl.Targets {
			if target.Key == "ada1" && tpl.Key != RuleTemplateDiskSmartWearout && tpl.Key != RuleTemplateDiskSmartNvme {
				foundUnavailable = true
			}
			if target.Key == "cd0" {
				t.Fatalf("unsupported_inventory_device_should_not_be_a_target got: %s in template %s", target.Key, tpl.Key)
			}
		}
	}
	if !foundUnavailable {
		t.Fatal("inventory_disk_without_current_data_missing")
	}
}

func TestWearoutTemplateTargetsOnlySSDandNVMe(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test SSD", Type: "SSD", SmartData: diskServiceInterfaces.SmartData{}},
		{Device: "da0", Model: "Test SCSI SSD", Type: "SSD", SmartData: diskServiceInterfaces.SmartData{Device: diskServiceInterfaces.DeviceInfo{Protocol: "SCSI"}}},
		{Device: "ada1", Model: "Test HDD", Type: "HDD", SmartData: diskServiceInterfaces.SmartData{}},
		{Device: "nda0", Model: "Test NVMe", Type: "NVMe", SmartData: &diskServiceInterfaces.SMARTNvme{}},
	})

	view, err := svc.GetRuleConfig(context.Background())
	if err != nil {
		t.Fatalf("get_rule_config_failed: %v", err)
	}

	var wearTpl *RuleTemplateView
	for idx := range view.Templates {
		if view.Templates[idx].Key == RuleTemplateDiskSmartWearout {
			wearTpl = &view.Templates[idx]
			break
		}
	}
	if wearTpl == nil {
		t.Fatalf("expected_wearout_template")
	}

	hasSSD := false
	hasSCSISSD := false
	hasNVMe := false
	hasHDD := false
	for _, target := range wearTpl.Targets {
		switch target.Key {
		case "ada0":
			hasSSD = true
		case "nda0":
			hasNVMe = true
		case "da0":
			hasSCSISSD = true
		case "ada1":
			hasHDD = true
		}
	}
	if !hasSSD {
		t.Fatalf("expected_ssd_in_wearout_targets")
	}
	if !hasNVMe {
		t.Fatalf("expected_nvme_in_wearout_targets")
	}
	if !hasSCSISSD {
		t.Fatalf("expected_scsi_ssd_in_wearout_targets")
	}
	if hasHDD {
		t.Fatalf("hdd_should_not_be_in_wearout_targets")
	}
}

func TestNvmeTemplateTargetsOnlyNVMe(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test SSD", Type: "SSD", SmartData: diskServiceInterfaces.SmartData{}},
		{Device: "nda0", Model: "Test NVMe", Type: "NVMe", SmartData: &diskServiceInterfaces.SMARTNvme{}},
	})

	view, err := svc.GetRuleConfig(context.Background())
	if err != nil {
		t.Fatalf("get_rule_config_failed: %v", err)
	}

	var nvmeTpl *RuleTemplateView
	for idx := range view.Templates {
		if view.Templates[idx].Key == RuleTemplateDiskSmartNvme {
			nvmeTpl = &view.Templates[idx]
			break
		}
	}
	if nvmeTpl == nil {
		t.Fatalf("expected_nvme_template")
	}
	if len(nvmeTpl.Targets) != 1 || nvmeTpl.Targets[0].Key != "nda0" {
		t.Fatalf("expected_only_nvme_target got: %+v", nvmeTpl.Targets)
	}
}

func TestDiskSmartConfigDefaultsWritten(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test SSD", Type: "SSD", SmartData: diskServiceInterfaces.SmartData{}},
	})

	if _, err := svc.GetRuleConfig(context.Background()); err != nil {
		t.Fatalf("get_rule_config_failed: %v", err)
	}

	tempKind := notifier.KindForDiskSmart(notifier.DiskSmartTemperatureKindPrefix, "ada0")
	wearKind := notifier.KindForDiskSmart(notifier.DiskSmartWearoutKindPrefix, "ada0")
	healthKind := notifier.KindForDiskSmart(notifier.DiskSmartHealthKindPrefix, "ada0")
	selfTestKind := notifier.KindForDiskSmart(notifier.DiskSmartSelfTestKindPrefix, "ada0")

	for _, tc := range []struct {
		kind           string
		expectNonEmpty bool
	}{
		{tempKind, true},
		{wearKind, true},
		{healthKind, false},
		{selfTestKind, false},
	} {
		var rule models.NotificationKindRule
		if err := svc.DB.Where("kind = ?", tc.kind).First(&rule).Error; err != nil {
			t.Fatalf("failed_to_load_rule kind=%s: %v", tc.kind, err)
		}
		if tc.expectNonEmpty && rule.Config == "" {
			t.Fatalf("expected_non_empty_config_for_%s got empty", tc.kind)
		}
		if !tc.expectNonEmpty && rule.Config != "" && rule.Config != "{}" {
			t.Fatalf("expected_empty_config_for_%s got: %s", tc.kind, rule.Config)
		}
	}
}

func TestDiskSmartTargetLabelsIncludeModel(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "CONSISTENT SSD", Type: "SSD", SmartData: diskServiceInterfaces.SmartData{}},
	})

	view, err := svc.GetRuleConfig(context.Background())
	if err != nil {
		t.Fatalf("get_rule_config_failed: %v", err)
	}

	for _, tpl := range view.Templates {
		if tpl.Key == RuleTemplateDiskSmartTemperature {
			if len(tpl.Targets) < 1 {
				t.Fatalf("expected_targets")
			}
			if tpl.Targets[0].Label != "ada0 (CONSISTENT SSD)" {
				t.Fatalf("expected_label_with_model got: %s", tpl.Targets[0].Label)
			}
		}
	}
}

func TestUpdateRulePersistsConfig(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test SSD", Type: "SSD", SmartData: diskServiceInterfaces.SmartData{}},
	})

	view, err := svc.GetRuleConfig(context.Background())
	if err != nil {
		t.Fatalf("get_rule_config_failed: %v", err)
	}

	var tempRuleID uint
	for _, rule := range view.Rules {
		if rule.TemplateKey == RuleTemplateDiskSmartTemperature && rule.TargetKey == "ada0" {
			tempRuleID = rule.ID
			break
		}
	}
	if tempRuleID == 0 {
		t.Fatalf("expected_temperature_rule")
	}

	_, err = svc.UpdateRule(context.Background(), tempRuleID, RuleUpdateInput{
		UIEnabled:    true,
		NtfyEnabled:  true,
		EmailEnabled: true,
		Config:       `{"warningCelsius":45,"criticalCelsius":60}`,
	})
	if err != nil {
		t.Fatalf("update_rule_failed: %v", err)
	}

	var stored models.NotificationKindRule
	if err := svc.DB.Where("id = ?", tempRuleID).First(&stored).Error; err != nil {
		t.Fatalf("failed_to_load_updated_rule: %v", err)
	}
	if stored.Config != `{"warningCelsius":45,"criticalCelsius":60}` {
		t.Fatalf("expected_updated_config got: %s", stored.Config)
	}
}

func TestUpdateRuleRejectsInvalidSmartThresholds(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test SSD", Type: "SSD", SmartData: diskServiceInterfaces.SmartData{}},
	})
	view, err := svc.GetRuleConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]uint{}
	for _, rule := range view.Rules {
		ids[rule.TemplateKey] = rule.ID
	}
	tests := []struct {
		template string
		config   string
	}{
		{template: RuleTemplateDiskSmartTemperature, config: `{"warningCelsius":70,"criticalCelsius":60}`},
		{template: RuleTemplateDiskSmartTemperature, config: `{"warningCelsius":55,"criticalCelsius":201}`},
		{template: RuleTemplateDiskSmartWearout, config: `{"warningPercent":90,"criticalPercent":80}`},
		{template: RuleTemplateDiskSmartWearout, config: `{"warningPercent":80,"criticalPercent":101}`},
	}
	for _, test := range tests {
		_, err := svc.UpdateRule(context.Background(), ids[test.template], RuleUpdateInput{Config: test.config})
		if err == nil || !strings.HasPrefix(err.Error(), "invalid_notification_rule_") {
			t.Fatalf("template=%s err=%v", test.template, err)
		}
	}
}

func TestDeleteDiskRuleStaysDeleted(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test SSD", Type: "SSD", SmartData: diskServiceInterfaces.SmartData{}},
	})
	ntfyCalls := 0
	emailCalls := 0
	svc.SetNtfySender(func(context.Context, models.NotificationTransportConfig, notifier.EventInput, string) error {
		ntfyCalls++
		return nil
	})
	svc.SetEmailSender(func(context.Context, models.NotificationTransportConfig, notifier.EventInput, string) error {
		emailCalls++
		return nil
	})
	if _, err := svc.UpdateTransportConfig(context.Background(), TransportConfigUpdate{
		Transports: []TransportConfigEntryUpdate{
			{Name: "ntfy", Type: TransportTypeNtfy, Enabled: true, Ntfy: &NtfyTransportConfigUpdate{BaseURL: "https://ntfy.sh", Topic: "ops"}},
			{Name: "smtp", Type: TransportTypeSMTP, Enabled: true, Email: &EmailTransportConfigUpdate{SMTPHost: "smtp.example.com", SMTPPort: 587, SMTPFrom: "ops@example.com", Recipients: []string{"ops@example.com"}}},
		},
	}); err != nil {
		t.Fatalf("seed_transport_config_failed: %v", err)
	}

	view, err := svc.GetRuleConfig(context.Background())
	if err != nil {
		t.Fatalf("get_rule_config_failed: %v", err)
	}

	var healthRuleID uint
	for _, rule := range view.Rules {
		if rule.TemplateKey == RuleTemplateDiskSmartHealth && rule.TargetKey == "ada0" {
			healthRuleID = rule.ID
			break
		}
	}
	if healthRuleID == 0 {
		t.Fatalf("expected_health_rule")
	}

	if _, err := svc.DeleteRule(context.Background(), healthRuleID); err != nil {
		t.Fatalf("delete_rule_failed: %v", err)
	}

	after, err := svc.GetRuleConfig(context.Background())
	if err != nil {
		t.Fatalf("get_rule_config_after_delete_failed: %v", err)
	}

	for _, rule := range after.Rules {
		if rule.TemplateKey == RuleTemplateDiskSmartHealth && rule.TargetKey == "ada0" {
			t.Fatalf("deleted_rule_should_not_reappear")
		}
	}

	result, err := svc.Emit(context.Background(), notifier.EventInput{
		Kind:        notifier.KindForDiskSmart(notifier.DiskSmartHealthKindPrefix, "ada0"),
		Title:       "Disk health failed",
		Severity:    "critical",
		Fingerprint: "ada0|health",
		Metadata:    map[string]string{"device": "ada0", "disk_key": "ada0", "condition": "health_failed"},
	})
	if err != nil {
		t.Fatalf("emit_deleted_rule_failed: %v", err)
	}
	var count int64
	if err := svc.DB.Model(&models.Notification{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if result.NotificationID != 0 || result.SentNtfy || result.SentEmail || count != 0 || ntfyCalls != 0 || emailCalls != 0 {
		t.Fatalf("deleted_rule_emitted result=%+v count=%d ntfy=%d email=%d", result, count, ntfyCalls, emailCalls)
	}
}

func TestGetRuleConfigWithoutDiskServiceShowsOnlyZFS(t *testing.T) {
	svc := newTestService(t)
	if err := svc.DB.Create(&models.BasicSettings{Pools: []string{"zroot"}}).Error; err != nil {
		t.Fatalf("failed_to_seed_basic_settings: %v", err)
	}

	view, err := svc.GetRuleConfig(context.Background())
	if err != nil {
		t.Fatalf("get_rule_config_failed: %v", err)
	}

	if len(view.Templates) != 1 {
		t.Fatalf("expected_1_template_without_disk_service got: %d", len(view.Templates))
	}
	if view.Templates[0].Key != RuleTemplateZFSPoolState {
		t.Fatalf("expected_only_zfs_template got: %s", view.Templates[0].Key)
	}
}

func TestTestRuleWithZFSPoolStateTemplate(t *testing.T) {
	svc := newTestService(t)
	if err := svc.DB.Create(&models.BasicSettings{Pools: []string{"zroot"}}).Error; err != nil {
		t.Fatalf("failed_to_seed_basic_settings: %v", err)
	}
	if _, err := svc.GetRuleConfig(context.Background()); err != nil {
		t.Fatalf("seed_rule_config_failed: %v", err)
	}

	err := svc.TestRule(context.Background(), TestRuleInput{
		TemplateKey: RuleTemplateZFSPoolState,
		Condition:   "pool_degraded",
	})
	if err != nil {
		t.Fatalf("test_rule_zfs_failed: %v", err)
	}

	var notif models.Notification
	if err := svc.DB.Where("kind = ?", notifier.KindForZFSPoolState("zroot")).First(&notif).Error; err != nil {
		t.Fatalf("expected_zfs_test_notification: %v", err)
	}
	if notif.Severity != models.NotificationSeverityWarning {
		t.Fatalf("expected_warning_severity_for_degraded got: %s", notif.Severity)
	}
}

func TestTestRuleWithNVMeTemplate(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "nda0", Model: "Test NVMe", Type: "NVMe", SmartData: &diskServiceInterfaces.SMARTNvme{}},
	})

	err := svc.TestRule(context.Background(), TestRuleInput{
		TemplateKey: RuleTemplateDiskSmartNvme,
		Condition:   "nvme_warning",
	})
	if err != nil {
		t.Fatalf("test_rule_nvme_failed: %v", err)
	}

	nvmeKind := notifier.KindForDiskSmart(notifier.DiskSmartNvmeKindPrefix, "nda0")
	var notif models.Notification
	if err := svc.DB.Where("kind = ?", nvmeKind).First(&notif).Error; err != nil {
		t.Fatalf("expected_nvme_test_notification: %v", err)
	}
}

func TestTestRuleWithSelfTestTemplate(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test SSD", Type: "SSD"},
	})

	for _, condition := range []string{"self_test_failed", "self_test_passed"} {
		if err := svc.TestRule(context.Background(), TestRuleInput{
			TemplateKey: RuleTemplateDiskSmartSelfTest,
			TargetKey:   "ada0",
			Condition:   condition,
		}); err != nil {
			t.Fatalf("test_rule_self_test_failed condition=%s: %v", condition, err)
		}
	}

	kind := notifier.KindForDiskSmart(notifier.DiskSmartSelfTestKindPrefix, "ada0")
	var notifications []models.Notification
	if err := svc.DB.Where("kind = ?", kind).Order("id ASC").Find(&notifications).Error; err != nil {
		t.Fatalf("load_self_test_notifications_failed: %v", err)
	}
	if len(notifications) != 2 {
		t.Fatalf("expected_2_self_test_notifications got=%d", len(notifications))
	}
	if notifications[0].Title != "Disk ada0 self-test failed" || notifications[1].Title != "Disk ada0 self-test passed" {
		t.Fatalf("unexpected_self_test_notifications: %+v", notifications)
	}
	if notifications[0].Severity != models.NotificationSeverityCritical || notifications[1].Severity != models.NotificationSeverityInfo {
		t.Fatalf("unexpected_self_test_severities: %+v", notifications)
	}
}

func TestTestRuleDefaultConditionPerTemplate(t *testing.T) {
	svc := newTestServiceWithDisks(t, []diskServiceInterfaces.Disk{
		{Device: "ada0", Model: "Test SSD", Type: "SSD", SmartData: diskServiceInterfaces.SmartData{}},
	})

	tests := []struct {
		templateKey   string
		expectedTitle string
	}{
		{RuleTemplateDiskSmartTemperature, "Disk ada0 temperature high: 60 C"},
		{RuleTemplateDiskSmartWearout, "Disk ada0 wear-out high: 85.0%"},
		{RuleTemplateDiskSmartHealth, "Disk ada0 S.M.A.R.T health check FAILED"},
		{RuleTemplateDiskSmartSelfTest, "Disk ada0 self-test failed"},
	}

	for _, tc := range tests {
		if err := svc.TestRule(context.Background(), TestRuleInput{
			TemplateKey: tc.templateKey,
			TargetKey:   "ada0",
		}); err != nil {
			t.Fatalf("test_rule_failed_for_%s: %v", tc.templateKey, err)
		}

		kind, _ := ruleKindForTemplateTarget(tc.templateKey, "ada0")
		var notif models.Notification
		if err := svc.DB.Where("kind = ?", kind).First(&notif).Error; err != nil {
			t.Fatalf("expected_notification_for_%s: %v", tc.templateKey, err)
		}
		if notif.Title != tc.expectedTitle {
			t.Fatalf("unexpected_title_for_%s got=%q want=%q", tc.templateKey, notif.Title, tc.expectedTitle)
		}
	}
}
