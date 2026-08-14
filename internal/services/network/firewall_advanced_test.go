// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"gorm.io/gorm"
)

func setupFirewallAdvancedService(t *testing.T) (*Service, *gorm.DB, networkModels.FirewallAdvancedSettings) {
	t.Helper()
	svc, db := newNetworkServiceForTest(t,
		&networkModels.FirewallAdvancedSettings{},
		&networkModels.FirewallTrafficRule{},
		&networkModels.FirewallNATRule{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
	)
	settings := networkModels.FirewallAdvancedSettings{PreRules: "set skip on lo0", PostRules: "pass all"}
	if err := db.Create(&settings).Error; err != nil {
		t.Fatalf("seed advanced settings: %v", err)
	}
	return svc, db, settings
}

func stubFirewallValidationCommand(t *testing.T, resultErr error) {
	t.Helper()
	original := firewallRunCommand
	firewallRunCommand = func(command string, args ...string) (string, error) {
		if command != "/sbin/pfctl" || len(args) != 2 || args[0] != "-nf" {
			t.Fatalf("unexpected validation command: %s %v", command, args)
		}
		return "", resultErr
	}
	t.Cleanup(func() {
		firewallRunCommand = original
	})
}

func TestValidateFirewallAdvancedRequestBoundsEverySection(t *testing.T) {
	requests := []networkServiceInterfaces.FirewallAdvancedRequest{
		{PreRules: strings.Repeat("a", MaxFirewallAdvancedSectionBytes+1)},
		{PreNatDecl: strings.Repeat("a", MaxFirewallAdvancedSectionBytes+1)},
		{PostNatDecl: strings.Repeat("a", MaxFirewallAdvancedSectionBytes+1)},
		{PreTrafficAnchor: strings.Repeat("a", MaxFirewallAdvancedSectionBytes+1)},
		{PostTrafficAnchor: strings.Repeat("a", MaxFirewallAdvancedSectionBytes+1)},
		{PostRules: strings.Repeat("a", MaxFirewallAdvancedSectionBytes+1)},
	}
	for i := range requests {
		if err := validateFirewallAdvancedRequest(&requests[i]); !errors.Is(err, ErrInvalidFirewallAdvancedSettings) {
			t.Fatalf("section %d expected invalid advanced settings, got %v", i, err)
		}
	}
	if err := validateFirewallAdvancedRequest(nil); !errors.Is(err, ErrInvalidFirewallAdvancedSettings) {
		t.Fatalf("nil request expected invalid advanced settings, got %v", err)
	}
}

func TestPreviewRenderedConfigDoesNotPersistCandidate(t *testing.T) {
	svc, db, originalSettings := setupFirewallAdvancedService(t)
	stubFirewallValidationCommand(t, nil)

	candidate := &networkServiceInterfaces.FirewallAdvancedRequest{
		PreRules:  "set block-policy drop",
		PostRules: "block all",
	}
	rendered, err := svc.PreviewRenderedConfig(candidate)
	if err != nil {
		t.Fatalf("preview candidate: %v", err)
	}
	if !strings.Contains(rendered.PfConf, candidate.PreRules) || !strings.Contains(rendered.PfConf, candidate.PostRules) {
		t.Fatalf("rendered preview omitted candidate sections: %s", rendered.PfConf)
	}

	var stored networkModels.FirewallAdvancedSettings
	if err := db.First(&stored, originalSettings.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.PreRules != originalSettings.PreRules || stored.PostRules != originalSettings.PostRules {
		t.Fatalf("preview persisted candidate: %+v", stored)
	}
}

func TestPreviewRenderedConfigReturnsSanitizedValidationDetail(t *testing.T) {
	svc, _, _ := setupFirewallAdvancedService(t)
	stubFirewallValidationCommand(t, errors.New("command failed; output: pf.conf:3: syntax error"))

	_, err := svc.PreviewRenderedConfig(&networkServiceInterfaces.FirewallAdvancedRequest{PreRules: "invalid rule"})
	if !errors.Is(err, ErrInvalidFirewallAdvancedSettings) {
		t.Fatalf("expected invalid advanced settings, got %v", err)
	}
	detail := FirewallAdvancedValidationDetail(err)
	if !strings.Contains(detail, "pf_validation_failed") || !strings.Contains(detail, "syntax error") {
		t.Fatalf("missing sanitized validation detail: %q", detail)
	}
}

func TestUpdateFirewallAdvancedSettingsValidatesBeforePersisting(t *testing.T) {
	svc, db, originalSettings := setupFirewallAdvancedService(t)
	stubFirewallValidationCommand(t, errors.New("command failed; output: pf.conf:2: syntax error"))

	err := svc.UpdateFirewallAdvancedSettings(&networkServiceInterfaces.FirewallAdvancedRequest{PreRules: "invalid rule"})
	if !errors.Is(err, ErrInvalidFirewallAdvancedSettings) {
		t.Fatalf("expected validation failure, got %v", err)
	}

	var stored networkModels.FirewallAdvancedSettings
	if err := db.First(&stored, originalSettings.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.PreRules != originalSettings.PreRules || stored.PostRules != originalSettings.PostRules {
		t.Fatalf("invalid candidate was persisted: %+v", stored)
	}
}

func TestRestoreFirewallAdvancedSettingsAfterApplyFailureRestoresAndReapplies(t *testing.T) {
	svc, db, previous := setupFirewallAdvancedService(t)
	if err := db.Model(&previous).Updates(map[string]any{"pre_rules": "candidate", "post_rules": "block all"}).Error; err != nil {
		t.Fatal(err)
	}

	applyErr := errors.New("apply failed")
	reapplyCalls := 0
	err := svc.restoreFirewallAdvancedSettingsAfterApplyFailure(previous, applyErr, func() error {
		reapplyCalls++
		var restored networkModels.FirewallAdvancedSettings
		if err := db.First(&restored, previous.ID).Error; err != nil {
			return err
		}
		if restored.PreRules != previous.PreRules || restored.PostRules != previous.PostRules {
			return errors.New("previous settings were not restored before reapply")
		}
		return nil
	})
	if !errors.Is(err, applyErr) || reapplyCalls != 1 {
		t.Fatalf("unexpected rollback result: err=%v reapplyCalls=%d", err, reapplyCalls)
	}
}

func TestReadOptionalFirewallConfigFile(t *testing.T) {
	dir := t.TempDir()
	missing, err := readOptionalFirewallConfigFile(filepath.Join(dir, "missing.conf"))
	if err != nil || missing != "" {
		t.Fatalf("missing optional file: content=%q err=%v", missing, err)
	}

	path := filepath.Join(dir, "pf.conf")
	if err := os.WriteFile(path, []byte("pass all\n"), 0600); err != nil {
		t.Fatal(err)
	}
	content, err := readOptionalFirewallConfigFile(path)
	if err != nil || content != "pass all\n" {
		t.Fatalf("read rendered file: content=%q err=%v", content, err)
	}

	if _, err := readOptionalFirewallConfigFile(dir); err == nil {
		t.Fatal("expected non-missing read failure to be propagated")
	}
}
