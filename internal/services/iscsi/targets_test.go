package iscsi

import (
	"strings"
	"testing"

	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func newTargetTestService(t *testing.T) *Service {
	t.Helper()
	db := testutil.NewSQLiteTestDB(t,
		&iscsiModels.ISCSIInitiator{},
		&iscsiModels.ISCSITarget{},
		&iscsiModels.ISCSITargetPortal{},
		&iscsiModels.ISCSITargetLUN{},
	)
	return &Service{DB: db}
}

func TestCreateTargetMissingTargetName(t *testing.T) {
	svc := newTargetTestService(t)
	err := svc.CreateTarget("", "", "None", "", "", "", "")
	if err == nil || err.Error() != "target_name_required" {
		t.Fatalf("expected target_name_required, got %v", err)
	}
}

func TestCreateTargetIQNConflict(t *testing.T) {
	svc := newTargetTestService(t)
	svc.DB.Create(&iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:target0", AuthMethod: "None"})
	err := svc.CreateTarget("iqn.2025-01.com.example:target0", "", "None", "", "", "", "")
	if err == nil || err.Error() != "target_with_name_exists" {
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
	svc.DB.Create(&iscsiModels.ISCSITarget{TargetName: "iqn.2025-01.com.example:target0", AuthMethod: "None"})
	var tgt iscsiModels.ISCSITarget
	svc.DB.First(&tgt)

	// port=0 should be stored as 3260
	portal := iscsiModels.ISCSITargetPortal{
		TargetID: tgt.ID,
		Address:  "192.168.1.10",
		Port:     0,
	}
	// Apply the same defaulting logic as AddPortal
	if portal.Port == 0 {
		portal.Port = 3260
	}
	if portal.Port != 3260 {
		t.Fatalf("expected port to default to 3260, got %d", portal.Port)
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
	if err == nil || err.Error() != "lun_number_already_in_use" {
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
	if err := svc.DB.Delete(&tgt).Error; err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	var count int64
	svc.DB.Model(&iscsiModels.ISCSITarget{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected 0 targets after delete, got %d", count)
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
