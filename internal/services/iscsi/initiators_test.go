package iscsi

import (
	"strings"
	"testing"

	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	db := testutil.NewSQLiteTestDB(t, &iscsiModels.ISCSIInitiator{})
	return &Service{DB: db}
}

func TestCreateInitiatorNicknameConflict(t *testing.T) {
	svc := newTestService(t)
	svc.DB.Create(&iscsiModels.ISCSIInitiator{Nickname: "myblock", TargetAddress: "192.168.1.1", TargetName: "iqn.2012-06.org.example:target1", AuthMethod: "None"})
	err := svc.CreateInitiator("myblock", "192.168.1.2", "iqn.2012-06.org.example:target2", "", "None", "", "", "", "")
	if err == nil || err.Error() != "initiator_with_nickname_exists" {
		t.Fatalf("expected initiator_with_nickname_exists, got %v", err)
	}
}

func TestCreateInitiatorMissingTargetAddress(t *testing.T) {
	svc := newTestService(t)
	err := svc.CreateInitiator("myblock", "", "iqn.2012-06.org.example:target1", "", "None", "", "", "", "")
	if err == nil || err.Error() != "target_address_required" {
		t.Fatalf("expected target_address_required, got %v", err)
	}
}

func TestCreateInitiatorMissingTargetName(t *testing.T) {
	svc := newTestService(t)
	err := svc.CreateInitiator("myblock", "192.168.1.1", "", "", "None", "", "", "", "")
	if err == nil || err.Error() != "target_name_required" {
		t.Fatalf("expected target_name_required, got %v", err)
	}
}

func TestCreateInitiatorCHAPRequiresCredentials(t *testing.T) {
	svc := newTestService(t)
	err := svc.CreateInitiator("myblock", "192.168.1.1", "iqn.2012-06.org.example:target1", "", "CHAP", "", "", "", "")
	if err == nil || err.Error() != "chap_name_and_secret_required_for_chap" {
		t.Fatalf("expected chap_name_and_secret_required_for_chap, got %v", err)
	}
}

func TestCreateInitiatorMutualCHAPRequiresTgtCredentials(t *testing.T) {
	svc := newTestService(t)
	err := svc.CreateInitiator("myblock", "192.168.1.1", "iqn.2012-06.org.example:target1", "", "MutualCHAP", "user1", "secret1", "", "")
	if err == nil || err.Error() != "tgt_chap_name_and_secret_required_for_mutual_chap" {
		t.Fatalf("expected tgt_chap_name_and_secret_required_for_mutual_chap, got %v", err)
	}
}

func TestDeleteInitiator(t *testing.T) {
	svc := newTestService(t)
	svc.DB.Create(&iscsiModels.ISCSIInitiator{Nickname: "todelete", TargetAddress: "192.168.1.1", TargetName: "iqn.2012-06.org.example:target1", AuthMethod: "None"})
	var created iscsiModels.ISCSIInitiator
	if err := svc.DB.Where("nickname = ?", "todelete").First(&created).Error; err != nil {
		t.Fatalf("fixture not found: %v", err)
	}
	if err := svc.DB.Delete(&created).Error; err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	var count int64
	svc.DB.Model(&iscsiModels.ISCSIInitiator{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected 0 initiators after delete, got %d", count)
	}
}

func TestGenerateConfig(t *testing.T) {
	svc := newTestService(t)
	svc.DB.Create(&iscsiModels.ISCSIInitiator{Nickname: "fblock0", TargetAddress: "192.168.1.10", TargetName: "iqn.2012-06.org.example:target1", AuthMethod: "None"})
	svc.DB.Create(&iscsiModels.ISCSIInitiator{Nickname: "wblock0", TargetAddress: "192.168.1.10", TargetName: "iqn.2012-06.org.example:target2", AuthMethod: "CHAP", CHAPName: "inituser1", CHAPSecret: "secretpassw0rd"})
	svc.DB.Create(&iscsiModels.ISCSIInitiator{Nickname: "mblock0", TargetAddress: "192.168.1.10", TargetName: "iqn.2012-06.org.example:target3", AuthMethod: "MutualCHAP", CHAPName: "inituser2", CHAPSecret: "hiddenpassw0rd", TgtCHAPName: "targetuser2", TgtCHAPSecret: "freepassw0rd"})
	cfg, err := svc.GenerateConfig()
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}
	if !strings.Contains(cfg, configMarker) {
		t.Error("config should contain the Sylve marker comment")
	}
	if !strings.Contains(cfg, "fblock0 {") {
		t.Error("config should contain fblock0 block")
	}
	if !strings.Contains(cfg, "wblock0 {") {
		t.Error("config should contain wblock0 block")
	}
	if !strings.Contains(cfg, "chapiname") {
		t.Error("config should contain chapiname for CHAP initiator")
	}
	if !strings.Contains(cfg, "mblock0 {") {
		t.Error("config should contain mblock0 block")
	}
	if !strings.Contains(cfg, "tgtChapName") {
		t.Error("config should contain tgtChapName for MutualCHAP initiator")
	}
	if !strings.Contains(cfg, "tgtChapSecret") {
		t.Error("config should contain tgtChapSecret for MutualCHAP initiator")
	}
}
