// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func newAuthTestService(t *testing.T) *Service {
	t.Helper()

	db := testutil.NewSQLiteTestDB(
		t,
		&models.User{},
		&models.PAMIdentity{},
		&models.Token{},
		&models.SystemSecrets{},
	)

	return &Service{DB: db}
}

func TestGetOrCreatePAMIdentityReuse(t *testing.T) {
	svc := newAuthTestService(t)

	first, err := svc.getOrCreatePAMIdentity("root")
	if err != nil {
		t.Fatalf("expected_no_error_got: %v", err)
	}
	if first.ID == 0 {
		t.Fatalf("expected_non_zero_identity_id")
	}

	second, err := svc.getOrCreatePAMIdentity("root")
	if err != nil {
		t.Fatalf("expected_no_error_got: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("expected_same_identity_id_%d_got: %d", first.ID, second.ID)
	}
}

func TestVerifyTokenInDbForPAMIdentity(t *testing.T) {
	svc := newAuthTestService(t)

	identity, err := svc.getOrCreatePAMIdentity("root")
	if err != nil {
		t.Fatalf("failed_to_create_pam_identity: %v", err)
	}

	token := models.Token{
		UserID:   identity.ID,
		Token:    "pam-token",
		AuthType: "pam",
		Expiry:   time.Now().Add(time.Hour),
	}
	if err := svc.DB.Create(&token).Error; err != nil {
		t.Fatalf("failed_to_create_token: %v", err)
	}

	if ok := svc.VerifyTokenInDb("pam-token"); !ok {
		t.Fatalf("expected_token_to_verify")
	}

	if err := svc.DB.Delete(&identity).Error; err != nil {
		t.Fatalf("failed_to_delete_pam_identity: %v", err)
	}

	if ok := svc.VerifyTokenInDb("pam-token"); ok {
		t.Fatalf("expected_token_to_fail_verification_without_identity")
	}
}

func TestVerifyTokenInDbForLocalUser(t *testing.T) {
	svc := newAuthTestService(t)

	user := models.User{
		Username: "admin",
		Password: "pw",
		Admin:    true,
	}
	if err := svc.DB.Create(&user).Error; err != nil {
		t.Fatalf("failed_to_create_user: %v", err)
	}

	token := models.Token{
		UserID:   user.ID,
		Token:    "local-token",
		AuthType: "sylve",
		Expiry:   time.Now().Add(time.Hour),
	}
	if err := svc.DB.Create(&token).Error; err != nil {
		t.Fatalf("failed_to_create_token: %v", err)
	}

	if ok := svc.VerifyTokenInDb("local-token"); !ok {
		t.Fatalf("expected_token_to_verify")
	}

	if err := svc.DB.Delete(&user).Error; err != nil {
		t.Fatalf("failed_to_delete_user: %v", err)
	}

	if ok := svc.VerifyTokenInDb("local-token"); ok {
		t.Fatalf("expected_token_to_fail_verification_without_user")
	}
}
