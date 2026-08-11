// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package authHandlers

import (
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/services/auth"
)

// PublicGroupSummary is the non-recursive group representation embedded in a user.
type PublicGroupSummary struct {
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	Notes string `json:"notes"`
}

// PublicUser is the API representation of a user. Database-only authentication
// state is deliberately excluded from this type.
type PublicUser struct {
	ID              uint                 `json:"id"`
	Username        string               `json:"username"`
	FullName        string               `json:"fullName"`
	Email           string               `json:"email"`
	Notes           string               `json:"notes"`
	Admin           bool                 `json:"admin"`
	UID             int                  `json:"uid"`
	Shell           string               `json:"shell"`
	HomeDirectory   string               `json:"homeDirectory"`
	HomeDirPerms    uint                 `json:"homeDirPerms"`
	SSHPublicKey    string               `json:"sshPublicKey"`
	DisablePassword bool                 `json:"disablePassword"`
	Locked          bool                 `json:"locked"`
	DoasEnabled     bool                 `json:"doasEnabled"`
	PrimaryGroupID  *uint                `json:"primaryGroupId"`
	Source          string               `json:"source"`
	PasskeyEligible bool                 `json:"passkeyEligible"`
	CreatedAt       time.Time            `json:"createdAt"`
	UpdatedAt       time.Time            `json:"updatedAt"`
	LastLoginTime   time.Time            `json:"lastLoginTime"`
	Groups          []PublicGroupSummary `json:"groups,omitempty"`
}

// PublicGroup is the public group representation used when group responses
// include their users.
type PublicGroup struct {
	ID        uint         `json:"id"`
	Name      string       `json:"name"`
	Notes     string       `json:"notes"`
	CreatedAt time.Time    `json:"createdAt"`
	UpdatedAt time.Time    `json:"updatedAt"`
	Users     []PublicUser `json:"users,omitempty"`
}

// UserMutationResult gives callers and audit presentation a safe, stable
// identity for a completed user mutation.
type UserMutationResult struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
}

func publicUserFromModel(user models.User) PublicUser {
	groups := make([]PublicGroupSummary, 0, len(user.Groups))
	for _, group := range user.Groups {
		groups = append(groups, PublicGroupSummary{
			ID:    group.ID,
			Name:  group.Name,
			Notes: group.Notes,
		})
	}

	return PublicUser{
		ID:              user.ID,
		Username:        user.Username,
		FullName:        user.FullName,
		Email:           user.Email,
		Notes:           user.Notes,
		Admin:           user.Admin,
		UID:             user.UID,
		Shell:           user.Shell,
		HomeDirectory:   user.HomeDirectory,
		HomeDirPerms:    user.HomeDirPerms,
		SSHPublicKey:    user.SSHPublicKey,
		DisablePassword: user.DisablePassword,
		Locked:          user.Locked,
		DoasEnabled:     user.DoasEnabled,
		PrimaryGroupID:  user.PrimaryGroupID,
		Source:          user.Source,
		PasskeyEligible: auth.IsPasskeyRegistrationEligible(user),
		CreatedAt:       user.CreatedAt,
		UpdatedAt:       user.UpdatedAt,
		LastLoginTime:   user.LastLoginTime,
		Groups:          groups,
	}
}

func publicUsersFromModels(users []models.User) []PublicUser {
	result := make([]PublicUser, 0, len(users))
	for _, user := range users {
		result = append(result, publicUserFromModel(user))
	}
	return result
}

func publicGroupsFromModels(groups []models.Group) []PublicGroup {
	result := make([]PublicGroup, 0, len(groups))
	for _, group := range groups {
		result = append(result, PublicGroup{
			ID:        group.ID,
			Name:      group.Name,
			Notes:     group.Notes,
			CreatedAt: group.CreatedAt,
			UpdatedAt: group.UpdatedAt,
			Users:     publicUsersFromModels(group.Users),
		})
	}
	return result
}
