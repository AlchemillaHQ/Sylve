package iscsi

import (
	"strings"
	"testing"

	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	"github.com/alchemillahq/sylve/internal/testutil"
)

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
	if err == nil || err.Error() != "nickname_required" {
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
	if err == nil || err.Error() != "initiator_with_nickname_exists" {
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

func TestUpdateInitiatorNotFound(t *testing.T) {
	svc := newInitiatorTestService(t)
	err := svc.UpdateInitiator(999, "fblock0", "192.168.1.10", "iqn.2025-01.com.example:target0", "", "None", "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "initiator_not_found") {
		t.Fatalf("expected initiator_not_found error, got %v", err)
	}
}

func TestDeleteInitiatorNotFound(t *testing.T) {
	svc := newInitiatorTestService(t)
	err := svc.DeleteInitiator(999)
	if err == nil || !strings.Contains(err.Error(), "initiator_not_found") {
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
