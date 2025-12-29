// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var _ serviceInterfaces.AuthServiceInterface = (*Service)(nil)

type Service struct {
	DB *gorm.DB
}
type JWT struct {
	jwt.RegisteredClaims
	CustomClaims serviceInterfaces.CustomClaims `json:"custom_claims"`
}

func NewAuthService(db *gorm.DB) serviceInterfaces.AuthServiceInterface {
	return &Service{
		DB: db,
	}
}

func (s *Service) GetJWTSecret() (string, error) {
	var secret models.SystemSecrets

	if err := s.DB.Where("name = ?", "JWTSecret").First(&secret).Error; err != nil {
		return "", fmt.Errorf("jwt_secret_not_found")
	}

	return secret.Data, nil
}

func (s *Service) GetClusterKey() (string, error) {
	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err != nil {
		return "", fmt.Errorf("cluster_key_not_found")
	}

	return c.Key, nil
}

func (s *Service) CreateJWT(username, password, authType string, remember bool) (uint, string, error) {
	var user models.User

	if authType == "sylve" {
		if err := s.DB.Where("username = ?", username).First(&user).Error; err != nil {
			return 0, "", fmt.Errorf("invalid_credentials")
		}

		if !utils.CheckPasswordHash(password, user.Password) {
			return 0, "", fmt.Errorf("invalid_credentials")
		}

		if !user.Admin {
			return 0, "", fmt.Errorf("only_admin_allowed")
		}
	} else if authType == "pam" {
		valid, err := s.AuthenticatePAM(username, password)

		if err != nil {
			return 0, "", fmt.Errorf("pam_auth_error")
		}

		if !valid {
			return 0, "", fmt.Errorf("invalid_credentials")
		}

		user.ID = utils.StringToUintId(username)
		user.Username = username
	} else {
		return 0, "", fmt.Errorf("invalid_auth_type")
	}

	var expiry time.Time

	if remember {
		expiry = time.Now().Add(time.Hour * 24 * 7)
	} else {
		expiry = time.Now().Add(time.Hour * 24)
	}

	data := JWT{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiry),
			ID:        uuid.NewString(),
		},
		CustomClaims: serviceInterfaces.CustomClaims{
			UserID:   user.ID,
			Username: user.Username,
			AuthType: authType,
		},
	}

	secret, err := s.GetJWTSecret()

	if err != nil {
		return 0, "", fmt.Errorf("jwt_secret_not_found")
	}

	token, err := (jwt.NewWithClaims(jwt.SigningMethodHS256, data)).SignedString([]byte(secret))

	if err != nil {
		return 0, "", fmt.Errorf("jwt_signing_failed")
	}

	tokenRecord := models.Token{
		Token:    token,
		AuthType: authType,
		UserID:   user.ID,
		Expiry:   expiry,
	}

	err = s.DB.Create(&tokenRecord).Error
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: tokens.token") {
			if updateErr := s.DB.Model(&tokenRecord).
				Where("token = ?", tokenRecord.Token).
				Updates(models.Token{UserID: tokenRecord.UserID}).Error; updateErr != nil {
				return 0, "", fmt.Errorf("token_update_failed: %v", updateErr)
			}
		} else {
			return 0, "", fmt.Errorf("token_save_failed: %v", err)
		}
	}

	return user.ID, token, nil
}

func (s *Service) CreateClusterJWT(userId uint, username string, authType string, forceSecret string) (string, error) {
	var clusterKey string

	if forceSecret != "" {
		clusterKey = forceSecret
	} else {
		var err error

		clusterKey, err = s.GetClusterKey()
		if err != nil {
			return "", fmt.Errorf("failed_to_get_cluster_key: %w", err)
		}
	}

	data := JWT{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour * 24)),
			ID:        uuid.NewString(),
		},
		CustomClaims: serviceInterfaces.CustomClaims{
			UserID:   userId,
			Username: username,
			AuthType: authType,
		},
	}

	token, err := (jwt.NewWithClaims(jwt.SigningMethodHS256, data)).SignedString([]byte(clusterKey))
	if err != nil {
		return "", fmt.Errorf("failed_to_sign_jwt: %w", err)
	}

	return token, nil
}

func (s *Service) VerifyClusterJWT(tokenString string) (serviceInterfaces.CustomClaims, error) {
	clusterKey, err := s.GetClusterKey()
	if err != nil || clusterKey == "" {
		return serviceInterfaces.CustomClaims{}, fmt.Errorf("failed_to_get_cluster_key: %w", err)
	}

	token, err := jwt.ParseWithClaims(tokenString, &JWT{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(clusterKey), nil
	})

	if err != nil {
		return serviceInterfaces.CustomClaims{}, fmt.Errorf("jwt_invalid: %w", err)
	}

	claims, ok := token.Claims.(*JWT)

	if !ok || !token.Valid {
		return serviceInterfaces.CustomClaims{}, fmt.Errorf("jwt_invalid")
	}

	if time.Now().After(claims.ExpiresAt.Time) {
		return serviceInterfaces.CustomClaims{}, fmt.Errorf("jwt_expired")
	}

	return claims.CustomClaims, nil
}

func (s *Service) RevokeJWT(token string) error {
	var tokenRecord models.Token

	if err := s.DB.Where("token = ?", token).First(&tokenRecord).Error; err != nil {
		return fmt.Errorf("token_not_found")
	}

	if err := s.DB.Delete(&tokenRecord).Error; err != nil {
		return fmt.Errorf("token_delete_failed")
	}

	var user models.User

	if err := s.DB.Where("id = ?", tokenRecord.UserID).First(&user).Error; err != nil {
		return fmt.Errorf("user_not_found")
	}

	return nil
}

func (s *Service) VerifyTokenInDb(token string) bool {
	var tokenRecord models.Token

	if err := s.DB.Where("token = ?", token).First(&tokenRecord).Error; err != nil {
		logger.L.Error().Msgf("Token not found: %v", err)
		return false
	}

	var user models.User

	if err := s.DB.Where("id = ?", tokenRecord.UserID).First(&user).Error; err != nil {
		logger.L.Error().Msgf("User not found: %v", err)
		return false
	}

	return true
}

func (s *Service) ValidateToken(tokenString string) (serviceInterfaces.CustomClaims, error) {
	secret, err := s.GetJWTSecret()

	if err != nil {
		return serviceInterfaces.CustomClaims{}, fmt.Errorf("jwt_secret_not_found")
	}

	token, err := jwt.ParseWithClaims(tokenString, &JWT{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	})

	if err != nil {
		return serviceInterfaces.CustomClaims{}, fmt.Errorf("jwt_invalid")
	}

	claims, ok := token.Claims.(*JWT)

	if !ok || !token.Valid {
		return serviceInterfaces.CustomClaims{}, fmt.Errorf("jwt_invalid")
	}

	if time.Now().After(claims.ExpiresAt.Time) {
		return serviceInterfaces.CustomClaims{}, fmt.Errorf("jwt_expired")
	}

	if !s.VerifyTokenInDb(tokenString) {
		return serviceInterfaces.CustomClaims{}, fmt.Errorf("jwt_not_found_in_db")
	}

	return claims.CustomClaims, nil
}

func (s *Service) InitSecret(name string, shaRounds int) error {
	uuid, err := utils.GetSystemUUID()
	if err != nil {
		return fmt.Errorf("failed to get device UUID: %w", err)
	}

	secret := utils.SHA256(uuid, shaRounds)

	var systemSecret models.SystemSecrets
	err = s.DB.Where("name = ?", name).First(&systemSecret).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			newSecret := models.SystemSecrets{
				Name: name,
				Data: secret,
			}
			if err := s.DB.Create(&newSecret).Error; err != nil {
				return fmt.Errorf("failed to create %s: %w", name, err)
			}
			logger.L.Debug().Msgf("Created new %s", name)
		} else {
			return fmt.Errorf("error fetching %s: %w", name, err)
		}
	} else {
		if systemSecret.Data != secret {
			if err := s.DB.Model(&systemSecret).Update("data", secret).Error; err != nil {
				return fmt.Errorf("failed to update %s: %w", name, err)
			}
			logger.L.Debug().Msgf("Updated existing %s", name)
		} else {
			logger.L.Debug().Msgf("%s is up to date", name)
		}
	}

	return nil
}

func (s *Service) GetSecret(name string) (string, error) {
	var secret models.SystemSecrets

	if err := s.DB.Where("name = ?", name).First(&secret).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", fmt.Errorf("secret_not_found")
		} else {
			return "", fmt.Errorf("failed_to_get_secret")
		}
	}

	return secret.Data, nil
}

func (s *Service) UpsertSecret(name string, data string) error {
	var secret models.SystemSecrets

	err := s.DB.Where("name = ?", name).First(&secret).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			newSecret := models.SystemSecrets{
				Name: name,
				Data: data,
			}
			if err := s.DB.Create(&newSecret).Error; err != nil {
				return fmt.Errorf("failed_to_create")
			}
		} else {
			return fmt.Errorf("failed_to_fetch")
		}
	} else {
		if secret.Data != data {
			if err := s.DB.Model(&secret).Update("data", data).Error; err != nil {
				return fmt.Errorf("failed_to_update")
			}
		} else {
			return fmt.Errorf("already_upto_date")
		}
	}

	return nil
}

func (s *Service) ClearExpiredJWTTokens() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			err := s.DB.Where("expiry < ?", time.Now()).Delete(&models.Token{})
			if err.Error != nil {
				logger.L.Error().Msgf("Error deleting expired tokens: %v", err.Error)
			} else {
				if err.RowsAffected > 0 {
					logger.L.Info().Msgf("Cleared %d expired tokens", err.RowsAffected)
				}
			}
		}
	}
}

func (s *Service) GetTokenBySHA256(hash string) (string, error) {
	var tokens []models.Token
	if err := s.DB.Find(&tokens).Error; err != nil {
		return "", fmt.Errorf("failed_to_fetch_tokens: %v", err)
	}

	for _, token := range tokens {
		tokenHash := utils.SHA256(token.Token, 1)
		if tokenHash == hash {
			return token.Token, nil
		}
	}

	return "", fmt.Errorf("token_not_found")
}

func (s *Service) IsValidClusterKey(clusterKey string) bool {
	var count int64
	s.DB.Model(&clusterModels.Cluster{}).Where("key = ?", clusterKey).Count(&count)
	return count > 0
}

func (s *Service) GetBasicSettings() (models.BasicSettings, error) {
	var settings models.BasicSettings
	if err := s.DB.First(&settings).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return settings, fmt.Errorf("basic_settings_not_found")
		}

		return settings, fmt.Errorf("failed_to_fetch_basic_settings: %v", err)
	}

	return settings, nil
}
