// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package models

import (
	"time"
)

type User struct {
	ID            uint      `gorm:"primarykey" json:"id"`
	Username      string    `gorm:"unique" json:"username"`
	Email         string    `json:"email"`
	Password      string    `json:"-"`
	Notes         string    `json:"notes"`
	TOTP          string    `json:"totp"`
	Admin         bool      `json:"admin"`
	CreatedAt     time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt     time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
	LastLoginTime time.Time `json:"lastLoginTime"`

	Tokens []Token `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"tokens,omitempty"`
	Groups []Group `gorm:"many2many:user_groups;constraint:OnDelete:CASCADE" json:"groups,omitempty"`
}

type Group struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	Name      string    `gorm:"unique" json:"name"`
	Notes     string    `json:"notes"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
	Users     []User    `gorm:"many2many:user_groups;constraint:OnDelete:CASCADE" json:"users,omitempty"`
}

type PAMIdentity struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	Username  string    `gorm:"uniqueIndex;not null" json:"username"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
}

type Token struct {
	ID        uint      `gorm:"primarykey" json:"id,omitempty"`
	UserID    uint      `json:"userId,omitempty"`
	Token     string    `gorm:"index:,unique" json:"token,omitempty"`
	AuthType  string    `json:"authType,omitempty"`
	Expiry    time.Time `json:"expiry,omitempty"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"createdAt,omitempty"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updatedAt,omitempty"`

	User *User `gorm:"constraint:OnUpdate:CASCADE,OnDelete:SET NULL" json:"user,omitempty"`
}

type WebAuthnCredential struct {
	ID           uint      `gorm:"primarykey" json:"id"`
	UserID       uint      `gorm:"index;not null" json:"userId"`
	CredentialID string    `gorm:"uniqueIndex;not null" json:"credentialId"`
	Label        string    `gorm:"default:''" json:"label"`
	Data         []byte    `gorm:"type:blob;not null" json:"-"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime" json:"updatedAt"`

	User *User `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"user,omitempty"`
}

type WebAuthnChallenge struct {
	ID          uint      `gorm:"primarykey" json:"id"`
	RequestID   string    `gorm:"uniqueIndex;not null" json:"requestId"`
	UserID      *uint     `gorm:"index" json:"userId,omitempty"`
	Type        string    `gorm:"index;not null" json:"type"`
	SessionData []byte    `gorm:"type:blob;not null" json:"-"`
	Used        bool      `gorm:"index;default:false" json:"used"`
	ExpiresAt   time.Time `gorm:"index;not null" json:"expiresAt"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
}

type SystemSecrets struct {
	ID        uint   `gorm:"primarykey"`
	Name      string `gorm:"primarykey,unique"`
	Data      string
	CreatedAt time.Time `gorm:"autoCreateTime" json:"createdAt,omitempty"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updatedAt,omitempty"`
}
