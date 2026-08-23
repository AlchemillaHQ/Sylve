// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package iscsi

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
)

type testInitiatorZPoolChecker struct {
	poolName string
	err      error
}

func (c testInitiatorZPoolChecker) ActiveISCSIZPool(context.Context) (string, error) {
	return c.poolName, c.err
}

func newInitiatorTestService(t *testing.T) *Service {
	t.Helper()
	db := testutil.NewSQLiteTestDB(t,
		&iscsiModels.ISCSIInitiator{},
	)
	return &Service{DB: db}
}

func TestCreateInitiatorMissingNickname(t *testing.T) {
	svc := newInitiatorTestService(t)
	err := svc.CreateInitiator("", "192.168.1.10", "iqn.2025-01.com.example:target0", "", "None", "", "", "", "")
	if err == nil || !errors.Is(err, ErrInvalidRequest) || err.Error() != "nickname_required" {
		t.Fatalf("expected nickname_required, got %v", err)
	}
}

func TestCreateInitiatorMissingTargetAddress(t *testing.T) {
	svc := newInitiatorTestService(t)
	err := svc.CreateInitiator("fblock0", "", "iqn.2025-01.com.example:target0", "", "None", "", "", "", "")
	if err == nil || err.Error() != "target_address_required" {
		t.Fatalf("expected target_address_required, got %v", err)
	}
}

func TestCreateInitiatorMissingTargetName(t *testing.T) {
	svc := newInitiatorTestService(t)
	err := svc.CreateInitiator("fblock0", "192.168.1.10", "", "", "None", "", "", "", "")
	if err == nil || err.Error() != "target_name_required" {
		t.Fatalf("expected target_name_required, got %v", err)
	}
}

func TestCreateInitiatorDuplicateNickname(t *testing.T) {
	svc := newInitiatorTestService(t)
	svc.DB.Create(&iscsiModels.ISCSIInitiator{Nickname: "fblock0", TargetAddress: "192.168.1.10", TargetName: "iqn.2025-01.com.example:target0"})
	err := svc.CreateInitiator("fblock0", "192.168.1.11", "iqn.2025-01.com.example:target1", "", "None", "", "", "", "")
	if err == nil || !errors.Is(err, ErrConflict) || err.Error() != "initiator_with_nickname_exists" {
		t.Fatalf("expected initiator_with_nickname_exists, got %v", err)
	}
}

func TestCreateInitiatorInvalidAuthMethod(t *testing.T) {
	svc := newInitiatorTestService(t)
	err := svc.CreateInitiator("fblock0", "192.168.1.10", "iqn.2025-01.com.example:target0", "", "INVALID", "", "", "", "")
	if err == nil || !strings.HasPrefix(err.Error(), "invalid_auth_method") {
		t.Fatalf("expected invalid_auth_method error, got %v", err)
	}
}

func TestCreateInitiatorCHAPRequiresCredentials(t *testing.T) {
	svc := newInitiatorTestService(t)
	err := svc.CreateInitiator("fblock0", "192.168.1.10", "iqn.2025-01.com.example:target0", "", "CHAP", "", "", "", "")
	if err == nil || err.Error() != "chap_name_and_secret_required_for_chap" {
		t.Fatalf("expected chap_name_and_secret_required_for_chap, got %v", err)
	}
}

func TestCreateInitiatorCHAPSecretTooShort(t *testing.T) {
	svc := newInitiatorTestService(t)
	err := svc.CreateInitiator("fblock0", "192.168.1.10", "iqn.2025-01.com.example:target0", "", "CHAP", "user1", "short", "", "")
	if err == nil || err.Error() != "chap_secret_must_be_12_to_16_characters" {
		t.Fatalf("expected chap_secret_must_be_12_to_16_characters, got %v", err)
	}
}

func TestCreateInitiatorMutualCHAPRequiresBothCredentials(t *testing.T) {
	svc := newInitiatorTestService(t)
	err := svc.CreateInitiator("fblock0", "192.168.1.10", "iqn.2025-01.com.example:target0", "", "MutualCHAP", "user1", "secretpassw0rd", "", "")
	if err == nil || err.Error() != "tgt_chap_name_and_secret_required_for_mutual_chap" {
		t.Fatalf("expected tgt_chap_name_and_secret_required_for_mutual_chap, got %v", err)
	}
}

func TestCreateInitiatorNormalizesIPv6TargetAddress(t *testing.T) {
	svc := newInitiatorTestService(t)
	setInitiatorConfigPathForTest(t, t.TempDir()+"/iscsi.conf")
	restoreCommand := utils.SetCommandForTest(func(string, ...string) *exec.Cmd {
		return exec.Command("/usr/bin/true")
	})
	t.Cleanup(restoreCommand)

	err := svc.CreateInitiator(
		"fblock0",
		"2001:db8::10",
		"iqn.2025-01.com.example:target0",
		"",
		"None",
		"",
		"",
		"",
		"",
	)
	if err != nil {
		t.Fatalf("CreateInitiator: %v", err)
	}

	var initiator iscsiModels.ISCSIInitiator
	if err := svc.DB.First(&initiator).Error; err != nil {
		t.Fatalf("load initiator: %v", err)
	}
	if initiator.TargetAddress != "[2001:db8::10]" {
		t.Fatalf("target address = %q, want bracketed IPv6", initiator.TargetAddress)
	}
}

func TestCreateInitiatorAddsOnlyCreatedSession(t *testing.T) {
	svc := newInitiatorTestService(t)
	setInitiatorConfigPathForTest(t, t.TempDir()+"/iscsi.conf")

	var calls [][]string
	restoreCommand := utils.SetCommandForTest(func(command string, args ...string) *exec.Cmd {
		calls = append(calls, append([]string{command}, args...))
		return exec.Command("/usr/bin/true")
	})
	t.Cleanup(restoreCommand)

	if err := svc.CreateInitiator("fblock0", "192.0.2.10", "iqn.2025-01.com.example:target0", "", "None", "", "", "", ""); err != nil {
		t.Fatalf("CreateInitiator: %v", err)
	}
	want := [][]string{{"/usr/bin/iscsictl", "-An", "fblock0"}}
	if len(calls) != len(want) || !slices.Equal(calls[0], want[0]) {
		t.Fatalf("commands = %#v, want %#v", calls, want)
	}
}

func TestUpdateInitiatorReconnectsOnlyUpdatedSession(t *testing.T) {
	svc := newInitiatorTestService(t)
	setInitiatorConfigPathForTest(t, t.TempDir()+"/iscsi.conf")
	initiator := iscsiModels.ISCSIInitiator{
		Nickname:      "old-name",
		TargetAddress: "192.0.2.10",
		TargetName:    "iqn.2025-01.com.example:target0",
		AuthMethod:    "None",
	}
	if err := svc.DB.Create(&initiator).Error; err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	if err := svc.writeConfig(false); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	var calls [][]string
	restoreCommand := utils.SetCommandForTest(func(command string, args ...string) *exec.Cmd {
		calls = append(calls, append([]string{command}, args...))
		config, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("read config during %v: %v", args, err)
		}
		if slices.Equal(args, []string{"-Rn", "old-name"}) && !strings.Contains(string(config), "old-name {") {
			t.Fatalf("old nickname was removed from config before session logout: %s", config)
		}
		if slices.Equal(args, []string{"-An", "new-name"}) && !strings.Contains(string(config), "new-name {") {
			t.Fatalf("new nickname was not written before session login: %s", config)
		}
		return exec.Command("/usr/bin/true")
	})
	t.Cleanup(restoreCommand)

	if err := svc.UpdateInitiator(initiator.ID, "new-name", initiator.TargetAddress, initiator.TargetName, "", "None", "", "", "", ""); err != nil {
		t.Fatalf("UpdateInitiator: %v", err)
	}
	want := [][]string{
		{"/usr/bin/iscsictl", "-Rn", "old-name"},
		{"/usr/bin/iscsictl", "-An", "new-name"},
	}
	if len(calls) != len(want) || !slices.Equal(calls[0], want[0]) || !slices.Equal(calls[1], want[1]) {
		t.Fatalf("commands = %#v, want %#v", calls, want)
	}
}

func TestConnectInitiatorReconnectsOnlySelectedSession(t *testing.T) {
	svc := newInitiatorTestService(t)
	initiator := iscsiModels.ISCSIInitiator{
		Nickname:      "fblock0",
		TargetAddress: "192.0.2.10",
		TargetName:    "iqn.2025-01.com.example:target0",
		AuthMethod:    "None",
	}
	if err := svc.DB.Create(&initiator).Error; err != nil {
		t.Fatalf("create fixture: %v", err)
	}

	var calls [][]string
	restoreCommand := utils.SetCommandForTest(func(command string, args ...string) *exec.Cmd {
		calls = append(calls, append([]string{command}, args...))
		return exec.Command("/usr/bin/true")
	})
	t.Cleanup(restoreCommand)

	if err := svc.ConnectInitiator(initiator.ID); err != nil {
		t.Fatalf("ConnectInitiator: %v", err)
	}
	want := [][]string{
		{"/usr/bin/iscsictl", "-Rn", "fblock0"},
		{"/usr/bin/iscsictl", "-An", "fblock0"},
	}
	if len(calls) != len(want) || !slices.Equal(calls[0], want[0]) || !slices.Equal(calls[1], want[1]) {
		t.Fatalf("commands = %#v, want %#v", calls, want)
	}
}

func TestConnectInitiatorAddsSessionWhenNoSessionCanBeRemoved(t *testing.T) {
	svc := newInitiatorTestService(t)
	initiator := iscsiModels.ISCSIInitiator{
		Nickname:      "fblock0",
		TargetAddress: "192.0.2.10",
		TargetName:    "iqn.2025-01.com.example:target0",
		AuthMethod:    "None",
	}
	if err := svc.DB.Create(&initiator).Error; err != nil {
		t.Fatalf("create fixture: %v", err)
	}

	restoreCommand := utils.SetCommandForTest(func(_ string, args ...string) *exec.Cmd {
		if slices.Equal(args, []string{"-Rn", "fblock0"}) {
			return exec.Command("/usr/bin/false")
		}
		return exec.Command("/usr/bin/true")
	})
	t.Cleanup(restoreCommand)

	if err := svc.ConnectInitiator(initiator.ID); err != nil {
		t.Fatalf("ConnectInitiator: %v", err)
	}
}

func TestInitiatorMutationsRejectConnectedZPoolDevice(t *testing.T) {
	const targetName = "iqn.2025-01.com.example:target0"
	newService := func(t *testing.T) (*Service, iscsiModels.ISCSIInitiator) {
		t.Helper()
		svc := newInitiatorTestService(t)
		svc.SetInitiatorZPoolChecker(testInitiatorZPoolChecker{poolName: "tank"})
		initiator := iscsiModels.ISCSIInitiator{
			Nickname:      "fblock0",
			TargetAddress: "192.0.2.10",
			TargetName:    targetName,
			AuthMethod:    "None",
		}
		if err := svc.DB.Create(&initiator).Error; err != nil {
			t.Fatalf("create fixture: %v", err)
		}
		return svc, initiator
	}

	restoreCommand := utils.SetCommandForTest(func(string, ...string) *exec.Cmd {
		return exec.Command("/usr/bin/printf", "Target Portal State\n"+targetName+" 192.0.2.10 Connected:\n")
	})
	t.Cleanup(restoreCommand)

	t.Run("delete", func(t *testing.T) {
		svc, initiator := newService(t)
		err := svc.DeleteInitiator(initiator.ID)
		if !errors.Is(err, ErrConflict) || err.Error() != "initiator_in_use_by_zfs_pool" {
			t.Fatalf("error = %v, want initiator_in_use_by_zfs_pool", err)
		}
		var count int64
		if err := svc.DB.Model(&iscsiModels.ISCSIInitiator{}).Where("id = ?", initiator.ID).Count(&count).Error; err != nil {
			t.Fatalf("count initiator: %v", err)
		}
		if count != 1 {
			t.Fatalf("initiator count = %d, want 1", count)
		}
	})

	t.Run("update", func(t *testing.T) {
		svc, initiator := newService(t)
		err := svc.UpdateInitiator(initiator.ID, "renamed", initiator.TargetAddress, initiator.TargetName, "", "None", "", "", "", "")
		if !errors.Is(err, ErrConflict) || err.Error() != "initiator_in_use_by_zfs_pool" {
			t.Fatalf("error = %v, want initiator_in_use_by_zfs_pool", err)
		}
		var stored iscsiModels.ISCSIInitiator
		if err := svc.DB.First(&stored, initiator.ID).Error; err != nil {
			t.Fatalf("load initiator: %v", err)
		}
		if stored.Nickname != "fblock0" {
			t.Fatalf("nickname = %q, want fblock0", stored.Nickname)
		}
	})

	t.Run("reconnect", func(t *testing.T) {
		svc, initiator := newService(t)
		err := svc.ConnectInitiator(initiator.ID)
		if !errors.Is(err, ErrConflict) || err.Error() != "initiator_in_use_by_zfs_pool" {
			t.Fatalf("error = %v, want initiator_in_use_by_zfs_pool", err)
		}
	})
}

func TestInitiatorMutationFailsClosedWhenZPoolUsageCannotBeChecked(t *testing.T) {
	svc := newInitiatorTestService(t)
	svc.SetInitiatorZPoolChecker(testInitiatorZPoolChecker{err: errors.New("inventory unavailable")})
	initiator := iscsiModels.ISCSIInitiator{
		Nickname:      "fblock0",
		TargetAddress: "192.0.2.10",
		TargetName:    "iqn.2025-01.com.example:target0",
		AuthMethod:    "None",
	}
	if err := svc.DB.Create(&initiator).Error; err != nil {
		t.Fatalf("create fixture: %v", err)
	}

	err := svc.DeleteInitiator(initiator.ID)
	if !errors.Is(err, ErrConflict) || err.Error() != "unable_to_verify_initiator_not_in_use" {
		t.Fatalf("error = %v, want unable_to_verify_initiator_not_in_use", err)
	}
}

func TestDeleteInitiatorLogsOutBeforeRemovingConfigAndRecord(t *testing.T) {
	svc := newInitiatorTestService(t)
	setInitiatorConfigPathForTest(t, t.TempDir()+"/iscsi.conf")
	initiator := iscsiModels.ISCSIInitiator{
		Nickname:      "fblock0",
		TargetAddress: "192.0.2.10",
		TargetName:    "iqn.2025-01.com.example:target0",
		AuthMethod:    "None",
	}
	if err := svc.DB.Create(&initiator).Error; err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	if err := svc.writeConfig(false); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	restoreCommand := utils.SetCommandForTest(func(_ string, args ...string) *exec.Cmd {
		config, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("read config during %v: %v", args, err)
		}
		if !strings.Contains(string(config), "fblock0 {") {
			t.Fatalf("nickname was removed from config before session logout: %s", config)
		}
		return exec.Command("/usr/bin/true")
	})
	t.Cleanup(restoreCommand)

	if err := svc.DeleteInitiator(initiator.ID); err != nil {
		t.Fatalf("DeleteInitiator: %v", err)
	}
	var count int64
	if err := svc.DB.Model(&iscsiModels.ISCSIInitiator{}).Where("id = ?", initiator.ID).Count(&count).Error; err != nil {
		t.Fatalf("count initiator: %v", err)
	}
	if count != 0 {
		t.Fatalf("initiator count = %d, want 0", count)
	}
}

func TestDeleteInitiatorLogoutFailurePreservesRecord(t *testing.T) {
	svc := newInitiatorTestService(t)
	initiator := iscsiModels.ISCSIInitiator{
		Nickname:      "fblock0",
		TargetAddress: "192.0.2.10",
		TargetName:    "iqn.2025-01.com.example:target0",
		AuthMethod:    "None",
	}
	if err := svc.DB.Create(&initiator).Error; err != nil {
		t.Fatalf("create fixture: %v", err)
	}

	restoreCommand := utils.SetCommandForTest(func(string, ...string) *exec.Cmd {
		return exec.Command("/usr/bin/false")
	})
	t.Cleanup(restoreCommand)

	err := svc.DeleteInitiator(initiator.ID)
	if !errors.Is(err, ErrRuntimeFailed) || err.Error() != "failed_to_remove_iscsi_session" {
		t.Fatalf("error = %v, want failed_to_remove_iscsi_session runtime failure", err)
	}
	var count int64
	if err := svc.DB.Model(&iscsiModels.ISCSIInitiator{}).Where("id = ?", initiator.ID).Count(&count).Error; err != nil {
		t.Fatalf("count initiator: %v", err)
	}
	if count != 1 {
		t.Fatalf("initiator count = %d, want 1", count)
	}
}

func TestUpdateInitiatorNotFound(t *testing.T) {
	svc := newInitiatorTestService(t)
	err := svc.UpdateInitiator(999, "fblock0", "192.168.1.10", "iqn.2025-01.com.example:target0", "", "None", "", "", "", "")
	if err == nil || !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "initiator_not_found") {
		t.Fatalf("expected initiator_not_found error, got %v", err)
	}
}

func TestDeleteInitiatorNotFound(t *testing.T) {
	svc := newInitiatorTestService(t)
	err := svc.DeleteInitiator(999)
	if err == nil || !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "initiator_not_found") {
		t.Fatalf("expected initiator_not_found error, got %v", err)
	}
}

func TestGetInitiators(t *testing.T) {
	svc := newInitiatorTestService(t)
	svc.DB.Create(&iscsiModels.ISCSIInitiator{Nickname: "fblock0", TargetAddress: "192.168.1.10", TargetName: "iqn.2025-01.com.example:target0"})
	svc.DB.Create(&iscsiModels.ISCSIInitiator{Nickname: "fblock1", TargetAddress: "192.168.1.11", TargetName: "iqn.2025-01.com.example:target1"})

	initiators, err := svc.GetInitiators()
	if err != nil {
		t.Fatalf("GetInitiators failed: %v", err)
	}
	if len(initiators) != 2 {
		t.Fatalf("expected 2 initiators, got %d", len(initiators))
	}
}

func TestGenerateInitiatorConfig(t *testing.T) {
	svc := newInitiatorTestService(t)

	svc.DB.Create(&iscsiModels.ISCSIInitiator{
		Nickname:      "fblock0",
		TargetAddress: "192.168.1.10",
		TargetName:    "iqn.2025-01.com.example:target0",
		AuthMethod:    "None",
	})

	svc.DB.Create(&iscsiModels.ISCSIInitiator{
		Nickname:      "fblock1",
		TargetAddress: "192.168.1.11",
		TargetName:    "iqn.2025-01.com.example:target1",
		InitiatorName: "iqn.2012-06.org.example.freebsd:nobody",
		AuthMethod:    "CHAP",
		CHAPName:      "user1",
		CHAPSecret:    "secretpassw0rd",
	})

	cfg, err := svc.GenerateConfig()
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	if !strings.Contains(cfg, configMarker) {
		t.Error("config should contain the Sylve marker comment")
	}
	if !strings.Contains(cfg, "fblock0 {") {
		t.Error("config should contain fblock0 section")
	}
	if !strings.Contains(cfg, "fblock1 {") {
		t.Error("config should contain fblock1 section")
	}
	if !strings.Contains(cfg, "192.168.1.10") {
		t.Error("config should contain target address 192.168.1.10")
	}
	if !strings.Contains(cfg, "iqn.2025-01.com.example:target0") {
		t.Error("config should contain target0 IQN")
	}
	if !strings.Contains(cfg, "iqn.2012-06.org.example.freebsd:nobody") {
		t.Error("config should contain custom initiator name")
	}
	if !strings.Contains(cfg, "CHAP") {
		t.Error("config should contain CHAP auth method")
	}
	if !strings.Contains(cfg, "user1") {
		t.Error("config should contain CHAP user name")
	}
	if !strings.Contains(cfg, "secretpassw0rd") {
		t.Error("config should contain CHAP secret")
	}
}

func TestUpdateInitiatorPreservesOmittedSecrets(t *testing.T) {
	svc := newInitiatorTestService(t)
	setInitiatorConfigPathForTest(t, t.TempDir()+"/iscsi.conf")
	restoreCommand := utils.SetCommandForTest(func(string, ...string) *exec.Cmd {
		return exec.Command("true")
	})
	defer restoreCommand()

	initiator := iscsiModels.ISCSIInitiator{
		Nickname:      "fblock0",
		TargetAddress: "192.168.1.10",
		TargetName:    "iqn.2025-01.com.example:target0",
		AuthMethod:    "MutualCHAP",
		CHAPName:      "chap-user",
		CHAPSecret:    "secretpassw0rd",
		TgtCHAPName:   "target-user",
		TgtCHAPSecret: "targetpassw0rd",
	}
	if err := svc.DB.Create(&initiator).Error; err != nil {
		t.Fatalf("create initiator fixture: %v", err)
	}

	err := svc.UpdateInitiator(
		initiator.ID,
		initiator.Nickname,
		initiator.TargetAddress,
		initiator.TargetName,
		initiator.InitiatorName,
		initiator.AuthMethod,
		initiator.CHAPName,
		"",
		initiator.TgtCHAPName,
		"",
	)
	if err != nil {
		t.Fatalf("update initiator: %v", err)
	}

	var updated iscsiModels.ISCSIInitiator
	if err := svc.DB.First(&updated, initiator.ID).Error; err != nil {
		t.Fatalf("load updated initiator: %v", err)
	}
	if updated.CHAPSecret != initiator.CHAPSecret || updated.TgtCHAPSecret != initiator.TgtCHAPSecret {
		t.Fatalf("secrets changed: chap=%q target=%q", updated.CHAPSecret, updated.TgtCHAPSecret)
	}
}

func TestUpdateInitiatorClearsUnusedSecrets(t *testing.T) {
	svc := newInitiatorTestService(t)
	setInitiatorConfigPathForTest(t, t.TempDir()+"/iscsi.conf")
	restoreCommand := utils.SetCommandForTest(func(string, ...string) *exec.Cmd {
		return exec.Command("true")
	})
	defer restoreCommand()

	initiator := iscsiModels.ISCSIInitiator{
		Nickname:      "fblock0",
		TargetAddress: "192.168.1.10",
		TargetName:    "iqn.2025-01.com.example:target0",
		AuthMethod:    "CHAP",
		CHAPName:      "chap-user",
		CHAPSecret:    "secretpassw0rd",
	}
	if err := svc.DB.Create(&initiator).Error; err != nil {
		t.Fatalf("create initiator fixture: %v", err)
	}

	if err := svc.UpdateInitiator(
		initiator.ID,
		initiator.Nickname,
		initiator.TargetAddress,
		initiator.TargetName,
		initiator.InitiatorName,
		"None",
		initiator.CHAPName,
		"",
		"",
		"",
	); err != nil {
		t.Fatalf("update initiator: %v", err)
	}

	var updated iscsiModels.ISCSIInitiator
	if err := svc.DB.First(&updated, initiator.ID).Error; err != nil {
		t.Fatalf("load updated initiator: %v", err)
	}
	if updated.CHAPName != "" || updated.CHAPSecret != "" || updated.TgtCHAPName != "" || updated.TgtCHAPSecret != "" {
		t.Fatalf("unused credentials were retained: %+v", updated)
	}
}
