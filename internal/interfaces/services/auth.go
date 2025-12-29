// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package serviceInterfaces

import (
	"crypto/tls"

	"github.com/alchemillahq/sylve/internal/db/models"
)

type CustomClaims struct {
	UserID   uint   `json:"userId"`
	Username string `json:"username"`
	AuthType string `json:"authType"`
}

type AuthServiceInterface interface {
	GetJWTSecret() (string, error)
	GetClusterKey() (string, error)
	CreateJWT(username, password, authType string, remember bool) (uint, string, error)
	CreateClusterJWT(userId uint, username string, authType string, forceSecret string) (string, error)
	VerifyClusterJWT(tokenString string) (CustomClaims, error)
	RevokeJWT(token string) error
	VerifyTokenInDb(token string) bool
	ValidateToken(tokenString string) (CustomClaims, error)
	InitSecret(name string, shaRounds int) error
	GetSecret(name string) (string, error)
	UpsertSecret(name string, data string) error
	ClearExpiredJWTTokens()
	GetTokenBySHA256(hash string) (string, error)
	IsValidClusterKey(clusterKey string) bool
	GetBasicSettings() (models.BasicSettings, error)

	ListGroups() ([]models.Group, error)
	CreateGroup(name string, members []string) error
	DeleteGroup(id uint) error
	AddUsersToGroup(usernames []string, groupName string) error

	ListUsers() ([]models.User, error)
	GetUserByID(id uint) (*models.User, error)
	CreateUser(user *models.User) error
	DeleteUser(userID uint) error
	EditUser(userID uint, username string, password string, email string, admin bool) error
	UpdateLastUsageTime(userID uint) error

	AuthenticatePAM(username, password string) (bool, error)

	GetSylveCertificate() (*tls.Config, error)
}
