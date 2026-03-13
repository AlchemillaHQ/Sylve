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
