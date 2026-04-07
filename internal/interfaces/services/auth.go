// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package serviceInterfaces

import (
	"context"
	"crypto/tls"

	"github.com/alchemillahq/sylve/internal/db/models"
)

type CustomClaims struct {
	UserID   uint   `json:"userId"`
	Username string `json:"username"`
	AuthType string `json:"authType"`
	TokenUse string `json:"tokenUse,omitempty"`
}

// CreateUserOpts contains create-time-only parameters not stored directly on the model.
type CreateUserOpts struct {
	NewPrimaryGroup bool
	AuxGroupIDs     []uint
}

// EditUserOpts contains all editable fields for an existing user.
type EditUserOpts struct {
	FullName        string
	Username        string
	Password        string
	Email           string
	Admin           bool
	UID             int
	Shell           string
	HomeDirectory   string
	HomeDirPerms    uint
	SSHPublicKey    string
	DisablePassword bool
	Locked          bool
	DoasEnabled     bool
	NewPrimaryGroup bool
	PrimaryGroupID  *uint
	AuxGroupIDs     []uint
}

type AuthServiceInterface interface {
	GetJWTSecret() (string, error)
	GetClusterKey() (string, error)
	CreateJWT(username, password, authType string, remember bool) (uint, string, error)
	CreateScopedJWT(userID uint, username, authType, scope string, expiresInSeconds int64) (string, error)
	CreateClusterJWT(userId uint, username string, authType string, forceSecret string) (string, error)
	CreateInternalClusterJWT(username string, forceSecret string) (string, error)
	VerifyClusterJWT(tokenString string) (CustomClaims, error)
	RevokeJWT(token string) error
	VerifyTokenInDb(token string) bool
	ValidateToken(tokenString string) (CustomClaims, error)
	ValidateScopedJWT(tokenString, expectedScope string) (CustomClaims, error)
	InitSecret(name string, shaRounds int) error
	GetSecret(name string) (string, error)
	UpsertSecret(name string, data string) error
	ClearExpiredJWTTokens(ctx context.Context)
	GetTokenBySHA256(hash string) (string, error)
	IsValidClusterKey(clusterKey string) bool
	GetBasicSettings() (models.BasicSettings, error)

	ListGroups() ([]models.Group, error)
	CreateGroup(name string, members []string) error
	DeleteGroup(id uint) error
	AddUsersToGroup(usernames []string, groupName string) error

	ListUsers() ([]models.User, error)
	GetUserByID(id uint) (*models.User, error)
	CreateUser(user *models.User, opts CreateUserOpts) error
	DeleteUser(userID uint) error
	EditUser(userID uint, opts EditUserOpts) error
	GetNextUID() (int, error)
	UpdateLastUsageTime(userID uint) error

	AuthenticatePAM(username, password string) (bool, error)

	GetSylveCertificate() (*tls.Config, error)
}
