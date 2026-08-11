// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/go-webauthn/webauthn/webauthn"
)

func newPasskeyTestService(t *testing.T) *Service {
	t.Helper()

	db := testutil.NewSQLiteTestDB(
		t,
		&models.User{},
		&models.Token{},
		&models.SystemSecrets{},
		&models.WebAuthnCredential{},
		&models.WebAuthnChallenge{},
	)

	return &Service{DB: db}
}

func TestParsePasskeyUserHandle(t *testing.T) {
	id, err := parsePasskeyUserHandle([]byte("42"))
	if err != nil {
		t.Fatalf("expected_no_error_got: %v", err)
	}
	if id != 42 {
		t.Fatalf("expected_id_42_got: %d", id)
	}

	if _, err := parsePasskeyUserHandle([]byte("not-a-number")); err == nil {
		t.Fatalf("expected_error_for_invalid_user_handle")
	}
	if _, err := parsePasskeyUserHandle([]byte("0")); err == nil {
		t.Fatalf("expected_error_for_zero_user_handle")
	}
}

func TestPasskeyRegistrationEligibility(t *testing.T) {
	tests := []struct {
		name string
		user models.User
		want bool
	}{
		{name: "admin with Sylve password", user: models.User{Admin: true, Password: "hash"}, want: true},
		{name: "admin without Sylve password", user: models.User{Admin: true}, want: false},
		{name: "non-admin with Sylve password", user: models.User{Password: "hash"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPasskeyRegistrationEligible(tt.user); got != tt.want {
				t.Fatalf("expected eligibility %v, got %v", tt.want, got)
			}
		})
	}
}

func TestPasskeyRegistrationRejectsIneligibleTargetAtBothCeremonyBoundaries(t *testing.T) {
	svc := newPasskeyTestService(t)
	user := models.User{Username: "former-admin", Admin: false, Password: "hash"}
	if err := svc.DB.Create(&user).Error; err != nil {
		t.Fatalf("failed_to_seed_user: %v", err)
	}

	if _, _, err := svc.BeginPasskeyRegistration(user.ID, "sylve.example.com", "https://sylve.example.com"); err == nil || err.Error() != "passkey_registration_not_allowed" {
		t.Fatalf("expected_begin_registration_not_allowed_got: %v", err)
	}

	sessionData, err := json.Marshal(webauthn.SessionData{Challenge: "abc"})
	if err != nil {
		t.Fatalf("failed_to_encode_session: %v", err)
	}
	challenge := models.WebAuthnChallenge{
		RequestID:   "ineligible-finish",
		UserID:      &user.ID,
		Type:        passkeyChallengeTypeRegister,
		SessionData: sessionData,
		ExpiresAt:   time.Now().Add(time.Minute),
	}
	if err := svc.DB.Create(&challenge).Error; err != nil {
		t.Fatalf("failed_to_seed_challenge: %v", err)
	}

	if _, err := svc.FinishPasskeyRegistration(
		challenge.RequestID,
		json.RawMessage(`{}`),
		"Laptop",
		"sylve.example.com",
		"https://sylve.example.com",
	); err == nil || err.Error() != "passkey_registration_not_allowed" {
		t.Fatalf("expected_finish_registration_not_allowed_got: %v", err)
	}
}

func TestFinishPasskeyRegistrationValidatesLabelBeforeCeremony(t *testing.T) {
	svc := newPasskeyTestService(t)

	if _, err := svc.FinishPasskeyRegistration("missing", nil, "   ", "example.com", "https://example.com"); err == nil || err.Error() != "passkey_label_required" {
		t.Fatalf("expected_label_required_got: %v", err)
	}
	if _, err := svc.FinishPasskeyRegistration("missing", nil, strings.Repeat("é", 129), "example.com", "https://example.com"); err == nil || err.Error() != "passkey_label_too_long" {
		t.Fatalf("expected_label_too_long_got: %v", err)
	}
}

func TestUserPasskeyManagementAllowsCleanupForIneligibleUser(t *testing.T) {
	svc := newPasskeyTestService(t)
	user := models.User{Username: "former-admin", Admin: false}
	if err := svc.DB.Create(&user).Error; err != nil {
		t.Fatalf("failed_to_seed_user: %v", err)
	}

	credentialID := encodeCredentialID([]byte("credential"))
	record := models.WebAuthnCredential{
		UserID:       user.ID,
		CredentialID: credentialID,
		Label:        "Old key",
		Data:         []byte(`{}`),
	}
	if err := svc.DB.Create(&record).Error; err != nil {
		t.Fatalf("failed_to_seed_credential: %v", err)
	}

	passkeys, err := svc.ListUserPasskeys(user.ID)
	if err != nil {
		t.Fatalf("expected_list_success_got: %v", err)
	}
	if len(passkeys) != 1 || passkeys[0].CredentialID != credentialID {
		t.Fatalf("unexpected_passkey_list: %+v", passkeys)
	}

	deleted, err := svc.DeleteUserPasskey(user.ID, credentialID)
	if err != nil {
		t.Fatalf("expected_delete_success_got: %v", err)
	}
	if deleted.UserID != user.ID || deleted.CredentialID != credentialID || deleted.Label != "Old key" {
		t.Fatalf("unexpected_deleted_identity: %+v", deleted)
	}

	if _, err := svc.DeleteUserPasskey(user.ID, credentialID); err == nil || err.Error() != "credential_not_found" {
		t.Fatalf("expected_credential_not_found_got: %v", err)
	}
	if _, err := svc.ListUserPasskeys(user.ID + 1000); err == nil || err.Error() != "user_not_found" {
		t.Fatalf("expected_user_not_found_got: %v", err)
	}
	if _, err := svc.DeleteUserPasskey(user.ID, "not base64!"); err == nil || err.Error() != "invalid_credential_id" {
		t.Fatalf("expected_invalid_credential_id_got: %v", err)
	}
}

func TestLoadPasskeyChallengeLifecycle(t *testing.T) {
	svc := newPasskeyTestService(t)

	if _, _, err := svc.loadPasskeyChallenge("missing", passkeyChallengeTypeLogin); err == nil || !strings.Contains(err.Error(), "challenge_not_found") {
		t.Fatalf("expected_challenge_not_found_error_got: %v", err)
	}

	sessionData, _ := json.Marshal(webauthn.SessionData{
		Challenge: "abc",
	})

	used := models.WebAuthnChallenge{
		RequestID:   "used",
		Type:        passkeyChallengeTypeLogin,
		SessionData: sessionData,
		Used:        true,
		ExpiresAt:   time.Now().Add(time.Minute),
	}
	if err := svc.DB.Create(&used).Error; err != nil {
		t.Fatalf("failed_to_create_used_challenge: %v", err)
	}

	if _, _, err := svc.loadPasskeyChallenge("used", passkeyChallengeTypeLogin); err == nil || !strings.Contains(err.Error(), "challenge_used") {
		t.Fatalf("expected_challenge_used_error_got: %v", err)
	}

	expired := models.WebAuthnChallenge{
		RequestID:   "expired",
		Type:        passkeyChallengeTypeLogin,
		SessionData: sessionData,
		Used:        false,
		ExpiresAt:   time.Now().Add(-time.Minute),
	}
	if err := svc.DB.Create(&expired).Error; err != nil {
		t.Fatalf("failed_to_create_expired_challenge: %v", err)
	}

	if _, _, err := svc.loadPasskeyChallenge("expired", passkeyChallengeTypeLogin); err == nil || !strings.Contains(err.Error(), "challenge_expired") {
		t.Fatalf("expected_challenge_expired_error_got: %v", err)
	}

	valid := models.WebAuthnChallenge{
		RequestID:   "valid",
		Type:        passkeyChallengeTypeLogin,
		SessionData: sessionData,
		Used:        false,
		ExpiresAt:   time.Now().Add(time.Minute),
	}
	if err := svc.DB.Create(&valid).Error; err != nil {
		t.Fatalf("failed_to_create_valid_challenge: %v", err)
	}

	challenge, session, err := svc.loadPasskeyChallenge("valid", passkeyChallengeTypeLogin)
	if err != nil {
		t.Fatalf("expected_no_error_got: %v", err)
	}
	if challenge.RequestID != "valid" {
		t.Fatalf("expected_request_id_valid_got: %s", challenge.RequestID)
	}
	if session.Challenge != "abc" {
		t.Fatalf("expected_challenge_abc_got: %s", session.Challenge)
	}
}

func TestIssueJWTPersistsToken(t *testing.T) {
	svc := newPasskeyTestService(t)

	if err := svc.DB.Create(&models.SystemSecrets{
		Name: "JWTSecret",
		Data: "test-secret",
	}).Error; err != nil {
		t.Fatalf("failed_to_seed_jwt_secret: %v", err)
	}

	user := models.User{
		ID:       1,
		Username: "admin",
		Admin:    true,
	}
	if err := svc.DB.Create(&user).Error; err != nil {
		t.Fatalf("failed_to_seed_user: %v", err)
	}

	token, err := svc.issueJWT(user, AuthTypeSylvePasskey, false)
	if err != nil {
		t.Fatalf("expected_no_error_got: %v", err)
	}
	if token == "" {
		t.Fatalf("expected_non_empty_token")
	}

	var tokenRecord models.Token
	if err := svc.DB.Where("token = ?", token).First(&tokenRecord).Error; err != nil {
		t.Fatalf("failed_to_load_token_record: %v", err)
	}
	if tokenRecord.UserID != user.ID {
		t.Fatalf("expected_user_id_%d_got: %d", user.ID, tokenRecord.UserID)
	}
	if tokenRecord.AuthType != AuthTypeSylvePasskey {
		t.Fatalf("expected_auth_type_%s_got: %s", AuthTypeSylvePasskey, tokenRecord.AuthType)
	}
}
