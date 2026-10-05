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
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func newTargetTestService(t *testing.T) *Service {
	t.Helper()
	db := testutil.NewSQLiteTestDB(t,
		&models.BasicSettings{},
		&iscsiModels.ISCSIInitiator{},
		&iscsiModels.ISCSITarget{},
		&iscsiModels.ISCSITargetPortal{},
		&iscsiModels.ISCSITargetLUN{},
	)
	if err := db.Create(&models.BasicSettings{Services: []models.AvailableService{models.ISCSI}}).Error; err != nil {
		t.Fatal(err)
	}
	svc := &Service{DB: db, ipv6Only: func() bool { return true }, backingStat: func(string) (os.FileInfo, error) { return os.Stat("/dev/null") }}
	fixture := &fakeTargetRuntime{service: svc}
	svc.runtime = &targetRuntime{run: fixture.run, deadline: 10 * time.Second, settle: time.Microsecond, interval: time.Microsecond}
	setTargetConfigPathForTest(t, t.TempDir()+"/ctl.conf")
	setInitiatorConfigPathForTest(t, t.TempDir()+"/iscsi.conf")
	return svc
}

func TestCreateTargetMissingTargetName(t *testing.T) {
	svc := newTargetTestService(t)
	err := svc.CreateTarget("", "", "None", "", "", "", "")
	if err == nil || !errors.Is(err, ErrInvalidRequest) || err.Error() != "target_name_required" {
		t.Fatalf("expected target_name_required, got %v", err)
	}
}

func TestCreateTargetIQNConflict(t *testing.T) {
	svc := newTargetTestService(t)
	svc.DB.Create(&iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:target0", AuthMethod: "None"})
	err := svc.CreateTarget("iqn.2025-01.com.example:target0", "", "None", "", "", "", "")
	if err == nil || !errors.Is(err, ErrConflict) || err.Error() != "target_with_name_exists" {
		t.Fatalf("expected target_with_name_exists, got %v", err)
	}
}

func TestCreateTargetInvalidAuthMethod(t *testing.T) {
	svc := newTargetTestService(t)
	err := svc.CreateTarget("iqn.2025-01.com.example:target0", "", "INVALID", "", "", "", "")
	if err == nil || !strings.HasPrefix(err.Error(), "invalid_auth_method") {
		t.Fatalf("expected invalid_auth_method error, got %v", err)
	}
}

func TestCreateTargetCHAPRequiresCredentials(t *testing.T) {
	svc := newTargetTestService(t)
	err := svc.CreateTarget("iqn.2025-01.com.example:target0", "", "CHAP", "", "", "", "")
	if err == nil || err.Error() != "chap_name_and_secret_required_for_chap" {
		t.Fatalf("expected chap_name_and_secret_required_for_chap, got %v", err)
	}
}

func TestCreateTargetMutualCHAPRequiresBothSecrets(t *testing.T) {
	svc := newTargetTestService(t)
	err := svc.CreateTarget("iqn.2025-01.com.example:target0", "", "MutualCHAP", "user1", "secretpassw0rd", "", "")
	if err == nil || err.Error() != "mutual_chap_name_and_secret_required_for_mutual_chap" {
		t.Fatalf("expected mutual_chap_name_and_secret_required_for_mutual_chap, got %v", err)
	}
}

func TestAddPortalMissingAddress(t *testing.T) {
	svc := newTargetTestService(t)
	svc.DB.Create(&iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:target0", AuthMethod: "None"})
	var tgt iscsiModels.ISCSITarget
	svc.DB.First(&tgt)
	err := svc.AddPortal(tgt.ID, "", 3260)
	if err == nil || err.Error() != "portal_address_required" {
		t.Fatalf("expected portal_address_required, got %v", err)
	}
}

func TestAddPortalDefaultsPort(t *testing.T) {
	svc := newTargetTestService(t)
	target := iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:target0", AuthMethod: "None"}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.AddPortal(target.ID, "[0:0:0:0:0:0:0:1]", 0); err != nil {
		t.Fatal(err)
	}
	var portal iscsiModels.ISCSITargetPortal
	if err := svc.DB.Where("target_id = ?", target.ID).First(&portal).Error; err != nil {
		t.Fatal(err)
	}
	if portal.Port != 3260 || portal.Address != "[::1]" {
		t.Fatalf("stored portal=%s:%d; want [::1]:3260", portal.Address, portal.Port)
	}
	if err := svc.AddPortal(target.ID, "::1", 3260); !errors.Is(err, ErrConflict) {
		t.Fatalf("canonical duplicate error=%v", err)
	}
}

func TestAddLUNMissingZVol(t *testing.T) {
	svc := newTargetTestService(t)
	svc.DB.Create(&iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:target0", AuthMethod: "None"})
	var tgt iscsiModels.ISCSITarget
	svc.DB.First(&tgt)
	err := svc.AddLUN(tgt.ID, 0, "")
	if err == nil || err.Error() != "zvol_required" {
		t.Fatalf("expected zvol_required, got %v", err)
	}
}

func TestAddLUNDuplicateLUNNumber(t *testing.T) {
	svc := newTargetTestService(t)
	svc.DB.Create(&iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:target0", AuthMethod: "None"})
	var tgt iscsiModels.ISCSITarget
	svc.DB.First(&tgt)
	svc.DB.Create(&iscsiModels.ISCSITargetLUN{TargetID: tgt.ID, LUNNumber: 0, ZVol: "tank/vol0"})
	err := svc.AddLUN(tgt.ID, 0, "tank/vol1")
	if err == nil || !errors.Is(err, ErrConflict) || err.Error() != "lun_number_already_in_use" {
		t.Fatalf("expected lun_number_already_in_use, got %v", err)
	}
}

func TestDeleteTarget(t *testing.T) {
	svc := newTargetTestService(t)
	svc.DB.Create(&iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:todelete", AuthMethod: "None"})
	var tgt iscsiModels.ISCSITarget
	if err := svc.DB.Where("target_name = ?", "iqn.2025-01.com.example:todelete").First(&tgt).Error; err != nil {
		t.Fatalf("fixture not found: %v", err)
	}
	if err := svc.DeleteTarget(tgt.ID); err != nil {
		t.Fatalf("DeleteTarget: %v", err)
	}
	var count int64
	svc.DB.Model(&iscsiModels.ISCSITarget{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected 0 targets after delete, got %d", count)
	}
}

func TestDeleteTargetRejectsActiveConnections(t *testing.T) {
	svc := newTargetTestService(t)
	target := iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:active", AuthMethod: "None"}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatalf("create target: %v", err)
	}
	portal := iscsiModels.ISCSITargetPortal{TargetID: target.ID, Address: "192.0.2.10", Port: 3260}
	lun := iscsiModels.ISCSITargetLUN{TargetID: target.ID, LUNNumber: 0, ZVol: "tank/vol0"}
	if err := svc.DB.Create(&portal).Error; err != nil {
		t.Fatalf("create portal: %v", err)
	}
	if err := svc.DB.Create(&lun).Error; err != nil {
		t.Fatalf("create LUN: %v", err)
	}

	svc.runtime.run = func(_ context.Context, _, command string, args ...string) (string, error) {
		if command != "/usr/sbin/ctladm" || !strings.Contains(strings.Join(args, " "), "islist -x") {
			t.Fatalf("unexpected command: %s %v", command, args)
		}
		return "<ctlislist><connection><initiator>iqn.client</initiator><target>" + target.TargetName + "</target></connection></ctlislist>", nil
	}

	err := svc.DeleteTarget(target.ID)
	if !errors.Is(err, ErrConflict) || err.Error() != "target_has_active_connections" {
		t.Fatalf("error = %v, want target_has_active_connections", err)
	}
	for name, model := range map[string]any{
		"target": &iscsiModels.ISCSITarget{},
		"portal": &iscsiModels.ISCSITargetPortal{},
		"LUN":    &iscsiModels.ISCSITargetLUN{},
	} {
		var count int64
		if err := svc.DB.Model(model).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 1 {
			t.Fatalf("%s count = %d, want 1", name, count)
		}
	}
}

func TestGenerateTargetConfig(t *testing.T) {
	svc := newTargetTestService(t)

	// Target 1: None auth, one portal, one LUN
	tgt1 := iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:target0", Alias: "MyTarget", AuthMethod: "None"}
	svc.DB.Create(&tgt1)
	svc.DB.Create(&iscsiModels.ISCSITargetPortal{TargetID: tgt1.ID, Address: "192.168.1.10", Port: 3260})
	svc.DB.Create(&iscsiModels.ISCSITargetLUN{TargetID: tgt1.ID, LUNNumber: 0, ZVol: "tank/vol0"})

	// Target 2: CHAP auth, one portal, one LUN
	tgt2 := iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:target1", AuthMethod: "CHAP", CHAPName: "user1", CHAPSecret: "secretpassw0rd"}
	svc.DB.Create(&tgt2)
	svc.DB.Create(&iscsiModels.ISCSITargetPortal{TargetID: tgt2.ID, Address: "192.168.1.10", Port: 3260})
	svc.DB.Create(&iscsiModels.ISCSITargetLUN{TargetID: tgt2.ID, LUNNumber: 0, ZVol: "tank/vol1"})

	// Target 3: MutualCHAP, one portal, two LUNs
	tgt3 := iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:target2", AuthMethod: "MutualCHAP", CHAPName: "user2", CHAPSecret: "hiddenpassw0rd", MutualCHAPName: "muser2", MutualCHAPSecret: "mutualpassw0rd"}
	svc.DB.Create(&tgt3)
	svc.DB.Create(&iscsiModels.ISCSITargetPortal{TargetID: tgt3.ID, Address: "192.168.1.10", Port: 3261})
	svc.DB.Create(&iscsiModels.ISCSITargetLUN{TargetID: tgt3.ID, LUNNumber: 0, ZVol: "tank/vol2"})
	svc.DB.Create(&iscsiModels.ISCSITargetLUN{TargetID: tgt3.ID, LUNNumber: 1, ZVol: "tank/vol3"})

	cfg, err := svc.GenerateTargetConfig()
	if err != nil {
		t.Fatalf("GenerateTargetConfig failed: %v", err)
	}

	if !strings.Contains(cfg, configMarker) {
		t.Error("config should contain the Sylve marker comment")
	}
	if !strings.Contains(cfg, "portal-group pg-") {
		t.Error("config should contain portal-group blocks")
	}
	if !strings.Contains(cfg, "192.168.1.10:3260") {
		t.Error("config should contain portal listen address")
	}
	if !strings.Contains(cfg, "iqn.2025-01.com.example:target0") {
		t.Error("config should contain target0 IQN")
	}
	if !strings.Contains(cfg, "no-authentication") {
		t.Error("config should use no-authentication for None auth method")
	}
	if !strings.Contains(cfg, `"MyTarget"`) {
		t.Error("config should contain the alias")
	}
	if !strings.Contains(cfg, "/dev/zvol/tank/vol0") {
		t.Error("config should contain the zvol path for vol0")
	}
	if !strings.Contains(cfg, "auth-group ag-") {
		t.Error("config should contain auth-group blocks for CHAP targets")
	}
	if !strings.Contains(cfg, "discovery-auth-group no-authentication") {
		t.Error("config should contain discovery-auth-group no-authentication in portal-group")
	}
	if !strings.Contains(cfg, "\tchap ") {
		t.Error("config should contain chap entry for CHAP target")
	}
	if !strings.Contains(cfg, "chap-mutual") {
		t.Error("config should contain chap-mutual for MutualCHAP target")
	}
	if !strings.Contains(cfg, "/dev/zvol/tank/vol2") {
		t.Error("config should contain vol2 path for MutualCHAP target")
	}
	if !strings.Contains(cfg, "/dev/zvol/tank/vol3") {
		t.Error("config should contain vol3 path for second LUN")
	}
	if !strings.Contains(cfg, "192.168.1.10:3261") {
		t.Error("config should contain custom portal port 3261")
	}
}

func TestAddPortalRejectsInvalidPortAndDuplicates(t *testing.T) {
	svc := newTargetTestService(t)
	target := iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:target0", AuthMethod: "None"}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatalf("create target fixture: %v", err)
	}

	if err := svc.AddPortal(target.ID, "192.0.2.10", 65536); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected invalid port error, got %v", err)
	}
	if err := svc.DB.Create(&iscsiModels.ISCSITargetPortal{TargetID: target.ID, Address: "192.0.2.10", Port: 3260}).Error; err != nil {
		t.Fatalf("create portal fixture: %v", err)
	}
	if err := svc.AddPortal(target.ID, "192.0.2.10", 3260); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected duplicate portal conflict, got %v", err)
	}
}

func TestAddLUNRejectsDuplicateZVol(t *testing.T) {
	svc := newTargetTestService(t)
	first := iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:target0", AuthMethod: "None"}
	second := iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:target1", AuthMethod: "None"}
	if err := svc.DB.Create(&first).Error; err != nil {
		t.Fatalf("create first target fixture: %v", err)
	}
	if err := svc.DB.Create(&second).Error; err != nil {
		t.Fatalf("create second target fixture: %v", err)
	}
	if err := svc.DB.Create(&iscsiModels.ISCSITargetLUN{TargetID: first.ID, LUNNumber: 0, ZVol: "tank/vol0"}).Error; err != nil {
		t.Fatalf("create LUN fixture: %v", err)
	}

	if err := svc.AddLUN(second.ID, 0, "tank/vol0"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected duplicate zvol conflict, got %v", err)
	}
}

func TestConcurrentAddLUNAllowsOnlyOneTargetToUseZVol(t *testing.T) {
	svc := newTargetTestService(t)
	first := iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:target0", AuthMethod: "None"}
	second := iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:target1", AuthMethod: "None"}
	if err := svc.DB.Create(&first).Error; err != nil {
		t.Fatalf("create first target: %v", err)
	}
	if err := svc.DB.Create(&second).Error; err != nil {
		t.Fatalf("create second target: %v", err)
	}

	results := make(chan error, 2)
	go func() { results <- svc.AddLUN(first.ID, 0, "tank/shared") }()
	go func() { results <- svc.AddLUN(second.ID, 0, "tank/shared") }()

	var successes, conflicts int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent AddLUN error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want 1 each", successes, conflicts)
	}
}

func TestGenerateTargetConfigFormatsIPv6Portal(t *testing.T) {
	svc := newTargetTestService(t)
	target := iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:target0", AuthMethod: "None"}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatalf("create target: %v", err)
	}
	if err := svc.DB.Create(&iscsiModels.ISCSITargetPortal{TargetID: target.ID, Address: "2001:db8::10", Port: 3260}).Error; err != nil {
		t.Fatalf("create portal: %v", err)
	}

	cfg, err := svc.GenerateTargetConfig()
	if err != nil {
		t.Fatalf("GenerateTargetConfig: %v", err)
	}
	if !strings.Contains(cfg, `listen "[2001:db8::10]:3260"`) {
		t.Fatalf("generated config does not contain bracketed IPv6 portal:\n%s", cfg)
	}
}

func TestRemoveTargetChildrenEnforcesOwnership(t *testing.T) {
	svc := newTargetTestService(t)
	first := iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:first", AuthMethod: "None"}
	second := iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:second", AuthMethod: "None"}
	if err := svc.DB.Create(&first).Error; err != nil {
		t.Fatalf("create first target: %v", err)
	}
	if err := svc.DB.Create(&second).Error; err != nil {
		t.Fatalf("create second target: %v", err)
	}
	portal := iscsiModels.ISCSITargetPortal{TargetID: first.ID, Address: "192.0.2.10", Port: 3260}
	lun := iscsiModels.ISCSITargetLUN{TargetID: first.ID, LUNNumber: 0, ZVol: "tank/vol0"}
	if err := svc.DB.Create(&portal).Error; err != nil {
		t.Fatalf("create portal: %v", err)
	}
	if err := svc.DB.Create(&lun).Error; err != nil {
		t.Fatalf("create LUN: %v", err)
	}

	if err := svc.RemovePortal(second.ID, portal.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected wrong-parent portal not found, got %v", err)
	}
	if err := svc.RemoveLUN(second.ID, lun.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected wrong-parent LUN not found, got %v", err)
	}
}

func TestUpdateTargetPreservesOmittedSecrets(t *testing.T) {
	svc := newTargetTestService(t)

	target := iscsiModels.ISCSITarget{
		TargetName:       "iqn.2025-01.com.example:target0",
		AuthMethod:       "MutualCHAP",
		CHAPName:         "chap-user",
		CHAPSecret:       "secretpassw0rd",
		MutualCHAPName:   "target-user",
		MutualCHAPSecret: "targetpassw0rd",
	}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatalf("create target fixture: %v", err)
	}

	if err := svc.UpdateTarget(
		target.ID,
		target.TargetName,
		"updated",
		target.AuthMethod,
		target.CHAPName,
		"",
		target.MutualCHAPName,
		"",
	); err != nil {
		t.Fatalf("update target: %v", err)
	}

	var updated iscsiModels.ISCSITarget
	if err := svc.DB.First(&updated, target.ID).Error; err != nil {
		t.Fatalf("load updated target: %v", err)
	}
	if updated.CHAPSecret != target.CHAPSecret || updated.MutualCHAPSecret != target.MutualCHAPSecret {
		t.Fatal("omitted secrets were changed")
	}
}

func TestPortalCandidateConflictDoesNotCommit(t *testing.T) {
	svc := newTargetTestService(t)
	first := iscsiModels.ISCSITarget{TargetName: "iqn.2026-10.test:first", AuthMethod: "None", Portals: []iscsiModels.ISCSITargetPortal{{Address: "0.0.0.0", Port: 3260}}}
	second := iscsiModels.ISCSITarget{TargetName: "iqn.2026-10.test:second", AuthMethod: "None"}
	svc.DB.Create(&first)
	svc.DB.Create(&second)
	before := []byte("preserved file")
	os.WriteFile(svc.targetPath(), before, 0600)
	if err := svc.AddPortal(second.ID, "127.0.0.1", 3260); !errors.Is(err, ErrConflict) {
		t.Fatalf("error=%v", err)
	}
	var count int64
	svc.DB.Model(&iscsiModels.ISCSITargetPortal{}).Where("target_id = ?", second.ID).Count(&count)
	if count != 0 {
		t.Fatal("rejected portal committed")
	}
	after, _ := os.ReadFile(svc.targetPath())
	if string(after) != string(before) {
		t.Fatal("rejected portal replaced config")
	}
}

func TestPortalHashCollisionFailsBeforeInsert(t *testing.T) {
	svc := newTargetTestService(t)
	svc.portalGroupName = func(portalEndpoint) string { return "pg-e-collision" }
	target := iscsiModels.ISCSITarget{TargetName: "iqn.2026-10.test:collision", AuthMethod: "None", Portals: []iscsiModels.ISCSITargetPortal{{Address: "127.0.0.1", Port: 3260}}}
	svc.DB.Create(&target)
	if err := svc.AddPortal(target.ID, "127.0.0.2", 3260); !errors.Is(err, ErrConflict) || err.Error() != "portal_group_hash_collision" {
		t.Fatalf("error=%v", err)
	}
	var count int64
	svc.DB.Model(&iscsiModels.ISCSITargetPortal{}).Count(&count)
	if count != 1 {
		t.Fatal("collision committed")
	}
}

func TestAddLUNRequiresExistingDeviceBeforeCommit(t *testing.T) {
	svc := newTargetTestService(t)
	svc.backingStat = os.Stat
	target := iscsiModels.ISCSITarget{TargetName: "iqn.2026-10.test:missing", AuthMethod: "None"}
	svc.DB.Create(&target)
	if err := svc.AddLUN(target.ID, 0, "sylve-missing-test/backing"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error=%v", err)
	}
	var count int64
	svc.DB.Model(&iscsiModels.ISCSITargetLUN{}).Count(&count)
	if count != 0 {
		t.Fatal("missing backing committed")
	}
}

func TestAddLUNRejectsNativeNumberLimitBeforeCommit(t *testing.T) {
	svc := newTargetTestService(t)
	target := iscsiModels.ISCSITarget{TargetName: "iqn.2026-10.test:lun-limit", AuthMethod: "None"}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.AddLUN(target.ID, 1024, "tank/test"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error=%v", err)
	}
	var count int64
	if err := svc.DB.Model(&iscsiModels.ISCSITargetLUN{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("out-of-range LUN number was committed")
	}
}

type fakeTargetRuntime struct {
	service      *Service
	running      bool
	live         *targetConfiguration
	removedPorts map[int]bool
}

func (f *fakeTargetRuntime) run(ctx context.Context, input, command string, args ...string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	switch command {
	case "/usr/sbin/diskinfo":
		return "fixture 512 65536", nil
	case "/usr/sbin/ctld":
		return "", nil
	case "/usr/bin/iscsictl":
		return "", nil
	case "/sbin/sysctl":
		if len(args) == 2 && args[0] == "-n" && args[1] == "kern.boottime" {
			return "fixture boot", nil
		}
	case "/bin/ps":
		return "fixture process birth", nil
	case "/bin/pgrep":
		if f.running {
			return "23942", nil
		}
		return "", nil
	case "/usr/sbin/service":
		if args[0] == "iscsid" {
			return "", nil
		}
		switch args[1] {
		case "onestatus":
			if f.running {
				return "ctld is running as pid 23942.", nil
			}
			return "", errTargetStopped
		case "onestop":
			f.running = false
			f.live = nil
			return "", nil
		case "onestart", "onereload":
			data, err := os.ReadFile(f.service.targetPath())
			if err != nil {
				return "", err
			}
			f.live, err = f.service.inspectTargetConfig(string(data))
			if err != nil {
				return "", err
			}
			for i := range f.live.targets {
				for j := range f.live.targets[i].luns {
					f.live.targets[i].luns[j].size = 65536
				}
			}
			f.running = true
			f.removedPorts = nil
			return "", nil
		}
	case "/usr/bin/sockstat":
		var out strings.Builder
		if f.live != nil && f.running {
			for _, endpoint := range f.live.activeEndpoints() {
				family := "tcp4"
				if endpoint.address.Is6() {
					family = "tcp6"
				}
				fmt.Fprintf(&out, "root ctld 23942 3 %s %s *:*\n", family, endpoint.listen())
			}
		}
		return out.String(), nil
	case "/usr/sbin/ctladm":
		ports, luns := f.inventory()
		var data []byte
		var err error
		if args[0] == "portlist" {
			data, err = xml.Marshal(ports)
		} else if args[0] == "devlist" {
			data, err = xml.Marshal(luns)
		} else if args[0] == "islist" {
			return "<ctlislist/>", nil
		} else if args[0] == "port" {
			for _, port := range ports.Ports {
				wanted := []string{"port", "-r", "-d", "iscsi", "-p", fmt.Sprint(port.ID), "-O", "cfiscsi_target=" + port.Target, "-O", "cfiscsi_portal_group_tag=" + fmt.Sprint(port.Tag)}
				if slices.Equal(args, wanted) {
					if f.removedPorts == nil {
						f.removedPorts = make(map[int]bool)
					}
					f.removedPorts[port.ID] = true
					return "", nil
				}
			}
			return "", errors.New("unexpected port removal")
		} else {
			return "", errors.New("unexpected CTL command")
		}
		return string(data), err
	}
	return "", errors.New("unexpected test command")
}

func (f *fakeTargetRuntime) inventory() (*ctlPorts, *ctlLUNs) {
	ports, luns := &ctlPorts{}, &ctlLUNs{}
	if f.live == nil {
		return ports, luns
	}
	ids := make(map[string]int)
	for _, target := range f.live.targets {
		for _, lun := range target.luns {
			ids[lun.name] = len(luns.LUNs)
			luns.LUNs = append(luns.LUNs, ctlLUN{ID: ids[lun.name], Name: lun.name, Backend: "block", Blocks: 128, Blocksize: 512, File: lun.path, Serial: fmt.Sprintf("fixture-%d", ids[lun.name])})
		}
	}
	tags := make(map[string]int)
	portID := 3
	for _, target := range f.live.targets {
		for _, name := range target.groups {
			if len(f.live.groups[name].listeners) == 0 {
				continue
			}
			if tags[name] == 0 {
				tags[name] = len(tags) + 1
			}
			port := ctlPort{ID: portID, Target: target.name, Group: name, Frontend: "iscsi", Online: "YES", Tag: tags[name], LUNMap: "on"}
			portID++
			for _, lun := range target.luns {
				port.LUNs = append(port.LUNs, struct {
					Number int `xml:"id,attr"`
					ID     int `xml:",chardata"`
				}{lun.number, ids[lun.name]})
			}
			if !f.removedPorts[port.ID] {
				ports.Ports = append(ports.Ports, port)
			}
		}
	}
	return ports, luns
}
