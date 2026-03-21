// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	passkeyChallengeTypeRegister = "register"
	passkeyChallengeTypeLogin    = "login"
	passkeyChallengeTTL          = 5 * time.Minute
	passkeyMaxPerUser            = 10
)

type PasskeyCredentialInfo struct {
	ID           uint      `json:"id"`
	UserID       uint      `json:"userId"`
	CredentialID string    `json:"credentialId"`
	Label        string    `json:"label"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type passkeyUser struct {
	model       models.User
	credentials []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte {
	return []byte(strconv.FormatUint(uint64(u.model.ID), 10))
}

func (u *passkeyUser) WebAuthnName() string {
	return u.model.Username
}

func (u *passkeyUser) WebAuthnDisplayName() string {
	return u.model.Username
}

func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential {
	return u.credentials
}

func (s *Service) newWebAuthn(rpID, origin string) (*webauthn.WebAuthn, error) {
	cfg := &webauthn.Config{
		RPID:                  strings.TrimSpace(rpID),
		RPDisplayName:         "Sylve",
		RPOrigins:             []string{strings.TrimSpace(origin)},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationPreferred,
		},
	}

	return webauthn.New(cfg)
}

func encodeCredentialID(rawID []byte) string {
	return base64.RawURLEncoding.EncodeToString(rawID)
}

func parsePasskeyUserHandle(userHandle []byte) (uint, error) {
	if len(userHandle) == 0 {
		return 0, fmt.Errorf("blank_user_handle")
	}

	id, err := strconv.ParseUint(string(userHandle), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid_user_handle")
	}

	return uint(id), nil
}

func credentialJSONRequest(raw json.RawMessage) (*http.Request, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("credential_required")
	}

	r, err := http.NewRequest(http.MethodPost, "/webauthn", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("failed_to_create_request: %w", err)
	}

	r.Header.Set("Content-Type", "application/json")

	return r, nil
}

func (s *Service) savePasskeyChallenge(userID *uint, challengeType string, session *webauthn.SessionData) (string, error) {
	sessionData, err := json.Marshal(session)
	if err != nil {
		return "", fmt.Errorf("failed_to_marshal_session")
	}

	requestID := uuid.NewString()
	model := models.WebAuthnChallenge{
		RequestID:   requestID,
		UserID:      userID,
		Type:        challengeType,
		SessionData: sessionData,
		Used:        false,
		ExpiresAt:   time.Now().Add(passkeyChallengeTTL),
	}

	if err := s.DB.Create(&model).Error; err != nil {
		return "", fmt.Errorf("failed_to_store_challenge")
	}

	return requestID, nil
}

func (s *Service) loadPasskeyChallenge(requestID, challengeType string) (*models.WebAuthnChallenge, *webauthn.SessionData, error) {
	var challenge models.WebAuthnChallenge
	if err := s.DB.Where("request_id = ? AND type = ?", requestID, challengeType).First(&challenge).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, fmt.Errorf("challenge_not_found")
		}

		return nil, nil, fmt.Errorf("failed_to_load_challenge")
	}

	if challenge.Used {
		return nil, nil, fmt.Errorf("challenge_used")
	}

	if time.Now().After(challenge.ExpiresAt) {
		return nil, nil, fmt.Errorf("challenge_expired")
	}

	var session webauthn.SessionData
	if err := json.Unmarshal(challenge.SessionData, &session); err != nil {
		return nil, nil, fmt.Errorf("invalid_challenge_session")
	}

	return &challenge, &session, nil
}

func (s *Service) loadPasskeyUser(userID uint) (*passkeyUser, error) {
	var user models.User
	if err := s.DB.First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("user_not_found")
		}

		return nil, fmt.Errorf("failed_to_load_user")
	}

	var records []models.WebAuthnCredential
	if err := s.DB.Where("user_id = ?", userID).Find(&records).Error; err != nil {
		return nil, fmt.Errorf("failed_to_load_credentials")
	}

	credentials := make([]webauthn.Credential, 0, len(records))
	for _, record := range records {
		var credential webauthn.Credential
		if err := json.Unmarshal(record.Data, &credential); err != nil {
			return nil, fmt.Errorf("failed_to_decode_credential")
		}

		credentials = append(credentials, credential)
	}

	return &passkeyUser{
		model:       user,
		credentials: credentials,
	}, nil
}

func (s *Service) BeginPasskeyRegistration(userID uint, rpID, origin string) (string, any, error) {
	user, err := s.loadPasskeyUser(userID)
	if err != nil {
		return "", nil, err
	}

	if len(user.credentials) >= passkeyMaxPerUser {
		return "", nil, fmt.Errorf("passkey_limit_reached")
	}

	w, err := s.newWebAuthn(rpID, origin)
	if err != nil {
		return "", nil, fmt.Errorf("failed_to_initialize_webauthn")
	}

	creation, session, err := w.BeginRegistration(
		user,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(webauthn.Credentials(user.credentials).CredentialDescriptors()),
		webauthn.WithExtensions(protocol.AuthenticationExtensions{"credProps": true}),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
	)
	if err != nil {
		return "", nil, fmt.Errorf("failed_to_begin_registration")
	}

	requestID, err := s.savePasskeyChallenge(&userID, passkeyChallengeTypeRegister, session)
	if err != nil {
		return "", nil, err
	}

	return requestID, creation.Response, nil
}

func (s *Service) FinishPasskeyRegistration(requestID string, credentialRaw json.RawMessage, label string, rpID, origin string) error {
	challenge, session, err := s.loadPasskeyChallenge(requestID, passkeyChallengeTypeRegister)
	if err != nil {
		return err
	}

	if challenge.UserID == nil {
		return fmt.Errorf("challenge_user_not_found")
	}

	user, err := s.loadPasskeyUser(*challenge.UserID)
	if err != nil {
		return err
	}

	w, err := s.newWebAuthn(rpID, origin)
	if err != nil {
		return fmt.Errorf("failed_to_initialize_webauthn")
	}

	request, err := credentialJSONRequest(credentialRaw)
	if err != nil {
		return err
	}

	credential, err := w.FinishRegistration(user, *session, request)
	if err != nil {
		return fmt.Errorf("invalid_passkey_registration")
	}

	data, err := json.Marshal(credential)
	if err != nil {
		return fmt.Errorf("failed_to_encode_credential")
	}

	credentialID := encodeCredentialID(credential.ID)
	trimmedLabel := strings.TrimSpace(label)

	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&models.WebAuthnCredential{}).Where("user_id = ?", user.model.ID).Count(&count).Error; err != nil {
			return fmt.Errorf("failed_to_count_passkeys")
		}

		if count >= passkeyMaxPerUser {
			return fmt.Errorf("passkey_limit_reached")
		}

		record := models.WebAuthnCredential{
			UserID:       user.model.ID,
			CredentialID: credentialID,
			Label:        trimmedLabel,
			Data:         data,
		}

		if err := tx.Create(&record).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(strings.ToLower(err.Error()), "unique") {
				return fmt.Errorf("credential_already_registered")
			}

			return fmt.Errorf("failed_to_store_credential")
		}

		result := tx.Model(&models.WebAuthnChallenge{}).
			Where("id = ? AND used = ?", challenge.ID, false).
			Update("used", true)
		if result.Error != nil {
			return fmt.Errorf("failed_to_update_challenge")
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("challenge_used")
		}

		return nil
	}); err != nil {
		return err
	}

	return nil
}

func (s *Service) BeginPasskeyLogin(rpID, origin string) (string, any, error) {
	w, err := s.newWebAuthn(rpID, origin)
	if err != nil {
		return "", nil, fmt.Errorf("failed_to_initialize_webauthn")
	}

	assertion, session, err := w.BeginDiscoverableLogin(
		webauthn.WithUserVerification(protocol.VerificationPreferred),
	)
	if err != nil {
		return "", nil, fmt.Errorf("failed_to_begin_login")
	}

	requestID, err := s.savePasskeyChallenge(nil, passkeyChallengeTypeLogin, session)
	if err != nil {
		return "", nil, err
	}

	return requestID, assertion.Response, nil
}

func (s *Service) FinishPasskeyLogin(requestID string, credentialRaw json.RawMessage, remember bool, rpID, origin string) (models.User, string, error) {
	challenge, session, err := s.loadPasskeyChallenge(requestID, passkeyChallengeTypeLogin)
	if err != nil {
		return models.User{}, "", err
	}

	w, err := s.newWebAuthn(rpID, origin)
	if err != nil {
		return models.User{}, "", fmt.Errorf("failed_to_initialize_webauthn")
	}

	request, err := credentialJSONRequest(credentialRaw)
	if err != nil {
		return models.User{}, "", err
	}

	var loaded *passkeyUser
	validatedUser, validatedCredential, err := w.FinishPasskeyLogin(
		func(rawID, userHandle []byte) (webauthn.User, error) {
			userID, err := parsePasskeyUserHandle(userHandle)
			if err != nil {
				return nil, err
			}

			loaded, err = s.loadPasskeyUser(userID)
			if err != nil {
				return nil, err
			}

			return loaded, nil
		},
		*session,
		request,
	)
	if err != nil {
		return models.User{}, "", fmt.Errorf("invalid_credentials")
	}

	if loaded == nil {
		u, ok := validatedUser.(*passkeyUser)
		if !ok || u == nil {
			return models.User{}, "", fmt.Errorf("failed_to_resolve_user")
		}
		loaded = u
	}

	challengeResult := s.DB.Model(&models.WebAuthnChallenge{}).
		Where("id = ? AND used = ?", challenge.ID, false).
		Update("used", true)
	if challengeResult.Error != nil {
		return models.User{}, "", fmt.Errorf("failed_to_update_challenge")
	}
	if challengeResult.RowsAffected == 0 {
		return models.User{}, "", fmt.Errorf("challenge_used")
	}

	if !loaded.model.Admin {
		return models.User{}, "", fmt.Errorf("only_admin_allowed")
	}

	data, err := json.Marshal(validatedCredential)
	if err != nil {
		return models.User{}, "", fmt.Errorf("failed_to_encode_credential")
	}

	credentialID := encodeCredentialID(validatedCredential.ID)

	result := s.DB.Model(&models.WebAuthnCredential{}).
		Where("user_id = ? AND credential_id = ?", loaded.model.ID, credentialID).
		Updates(map[string]any{
			"data":       data,
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return models.User{}, "", fmt.Errorf("failed_to_update_credential")
	}
	if result.RowsAffected == 0 {
		return models.User{}, "", fmt.Errorf("credential_not_found")
	}

	token, err := s.issueJWT(loaded.model, AuthTypeSylvePasskey, remember)
	if err != nil {
		return models.User{}, "", err
	}

	return loaded.model, token, nil
}

func (s *Service) ListUserPasskeys(userID uint) ([]PasskeyCredentialInfo, error) {
	var records []models.WebAuthnCredential
	if err := s.DB.Where("user_id = ?", userID).Order("created_at desc").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_passkeys")
	}

	out := make([]PasskeyCredentialInfo, 0, len(records))
	for _, record := range records {
		out = append(out, PasskeyCredentialInfo{
			ID:           record.ID,
			UserID:       record.UserID,
			CredentialID: record.CredentialID,
			Label:        record.Label,
			CreatedAt:    record.CreatedAt,
			UpdatedAt:    record.UpdatedAt,
		})
	}

	return out, nil
}

func (s *Service) DeleteUserPasskey(userID uint, credentialID string) error {
	credentialID = strings.TrimSpace(credentialID)
	if credentialID == "" {
		return fmt.Errorf("invalid_credential_id")
	}

	result := s.DB.Where("user_id = ? AND credential_id = ?", userID, credentialID).Delete(&models.WebAuthnCredential{})
	if result.Error != nil {
		return fmt.Errorf("failed_to_delete_passkey")
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("credential_not_found")
	}

	return nil
}
