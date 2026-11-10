// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"errors"
	"fmt"
	"slices"

	"github.com/alchemillahq/sylve/internal/db/models"
	"gorm.io/gorm"
)

type AuthRealm struct {
	ID            uint               `gorm:"primaryKey" json:"id"`
	Name          string             `gorm:"uniqueIndex;not null" json:"name"`
	Label         string             `gorm:"not null" json:"label"`
	Issuer        string             `gorm:"not null" json:"issuer"`
	ClientID      string             `gorm:"not null" json:"clientId"`
	ClientSecret  string             `gorm:"not null" json:"-"`
	HasSecret     bool               `gorm:"-" json:"hasSecret"`
	Scopes        string             `gorm:"not null" json:"scopes"`
	UsernameClaim string             `gorm:"not null" json:"usernameClaim"`
	GroupsClaim   string             `gorm:"not null" json:"groupsClaim"`
	GroupMappings []OIDCGroupMapping `gorm:"serializer:json;not null" json:"groupMappings"`
	AutoCreate    bool               `gorm:"not null" json:"autoCreate"`
	QueryUserInfo bool               `gorm:"not null" json:"queryUserInfo"`
	Prompt        string             `gorm:"not null" json:"prompt"`
	ACRValues     string             `gorm:"not null" json:"acrValues"`
	HostedDomains string             `gorm:"not null;default:''" json:"hostedDomains"`
	SessionHours  int                `gorm:"not null" json:"sessionHours"`
	CACert        string             `gorm:"type:text;not null" json:"caCert"`
	IsDefault     bool               `gorm:"not null" json:"default"`
	Enabled       bool               `gorm:"not null" json:"enabled"`
}

func (AuthRealm) TableName() string { return "auth_realms" }

type OIDCGroupMapping struct {
	Claim string `json:"claim"`
	Group string `json:"group"`
}

// A nil ClientSecret keeps the stored one.
type AuthRealmSet struct {
	AuthRealm
	ClientSecret *string `json:"clientSecret,omitempty"`
}

// OIDCUser signs in through a realm. A pre-created user has no subject until their first login
// binds one. Each node keeps its own users row for them, named <username>@<realm>.
type OIDCUser struct {
	ID                  uint     `gorm:"primaryKey" json:"id"`
	Realm               string   `gorm:"not null;uniqueIndex:idx_oidc_users_name" json:"realm"`
	Username            string   `gorm:"not null;uniqueIndex:idx_oidc_users_name" json:"username"`
	Subject             string   `gorm:"not null;index" json:"subject"`
	FullName            string   `gorm:"not null" json:"fullName"`
	Email               string   `gorm:"not null" json:"email"`
	Notes               string   `gorm:"not null" json:"notes"`
	Groups              []string `gorm:"serializer:json;not null" json:"groups"`
	IssuedAt            int64    `gorm:"not null" json:"issuedAt"`
	Locked              bool     `gorm:"not null" json:"locked"`
	ConfirmGuestActions bool     `gorm:"not null" json:"confirmGuestActions"`
}

func (OIDCUser) TableName() string { return "oidc_users" }

func (u OIDCUser) Principal() string { return u.Username + "@" + u.Realm }

const UserSourceOIDC = "oidc"

func RegisterOIDCHandlers(f *FSMDispatcher) {
	OnSince(f, aclProtocol, "auth_realm", "set", func(db *gorm.DB, set *AuthRealmSet) error {
		realm := set.AuthRealm
		if realm.Name == "" || realm.Issuer == "" || realm.ClientID == "" {
			return fmt.Errorf("invalid_auth_realm")
		}
		var current AuthRealm
		err := db.Where("name = ?", realm.Name).First(&current).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			realm.ID = 0
		case err != nil:
			return err
		case current.Issuer != realm.Issuer || current.ClientID != realm.ClientID:
			return fmt.Errorf("auth_realm_issuer_fixed")
		default:
			realm.ID, realm.ClientSecret = current.ID, current.ClientSecret
		}
		if set.ClientSecret != nil {
			realm.ClientSecret = *set.ClientSecret
		}
		if realm.GroupMappings == nil {
			realm.GroupMappings = []OIDCGroupMapping{}
		}
		if realm.IsDefault {
			if err := db.Model(&AuthRealm{}).Where("name <> ?", realm.Name).Update("is_default", false).Error; err != nil {
				return err
			}
		}
		return db.Save(&realm).Error
	})
	OnSince(f, aclProtocol, "auth_realm", "delete", func(db *gorm.DB, realm *AuthRealm) error {
		var users []OIDCUser
		if err := db.Where("realm = ?", realm.Name).Find(&users).Error; err != nil {
			return err
		}
		for _, user := range users {
			if err := deleteOIDCUser(db, user); err != nil {
				return err
			}
		}
		return db.Where("name = ?", realm.Name).Delete(&AuthRealm{}).Error
	})
	OnSince(f, aclProtocol, "oidc_user", "create", func(db *gorm.DB, user *OIDCUser) error {
		if user.Username == "" {
			return fmt.Errorf("invalid_oidc_user")
		}
		if err := db.Where("name = ?", user.Realm).First(&AuthRealm{}).Error; err != nil {
			return err
		}
		created := OIDCUser{
			Realm: user.Realm, Username: user.Username, FullName: user.FullName, Email: user.Email,
			Notes: user.Notes, Groups: []string{},
		}
		if err := db.Create(&created).Error; err != nil {
			return err
		}
		return mirrorOIDCUser(db, created)
	})
	OnSince(f, aclProtocol, "oidc_user", "login", func(db *gorm.DB, login *OIDCUser) error {
		if login.Subject == "" || login.Username == "" {
			return fmt.Errorf("invalid_oidc_user")
		}
		user, err := oidcLoginUser(db, login)
		if err != nil {
			return err
		}
		user.FullName, user.Email, user.Groups, user.IssuedAt = login.FullName, login.Email, login.Groups, login.IssuedAt
		if user.Groups == nil {
			user.Groups = []string{}
		}
		if err := db.Save(&user).Error; err != nil {
			return err
		}
		return mirrorOIDCUser(db, user)
	})
	OnSince(f, aclProtocol, "oidc_user", "update", func(db *gorm.DB, user *OIDCUser) error {
		result := db.Model(&OIDCUser{}).Where("realm = ? AND username = ?", user.Realm, user.Username).
			Select("notes", "locked", "confirm_guest_actions").Updates(user)
		if err := requireRowsAffected(result, 1); err != nil {
			return err
		}
		var updated OIDCUser
		if err := db.Where("realm = ? AND username = ?", user.Realm, user.Username).First(&updated).Error; err != nil {
			return err
		}
		return mirrorOIDCUser(db, updated)
	})
	OnSince(f, aclProtocol, "oidc_user", "delete", func(db *gorm.DB, user *OIDCUser) error {
		var existing OIDCUser
		if err := db.Where("realm = ? AND username = ?", user.Realm, user.Username).First(&existing).Error; err != nil {
			return err
		}
		return deleteOIDCUser(db, existing)
	})
}

// An ID token older than the last accepted one is refused, so a delayed callback can't restore
// groups a newer login removed.
func oidcLoginUser(db *gorm.DB, login *OIDCUser) (OIDCUser, error) {
	var realm AuthRealm
	if err := db.Where("name = ?", login.Realm).First(&realm).Error; err != nil {
		return OIDCUser{}, err
	}
	var user OIDCUser
	err := db.Where("realm = ? AND subject = ?", login.Realm, login.Subject).First(&user).Error
	if err == nil {
		if login.IssuedAt < user.IssuedAt {
			return OIDCUser{}, fmt.Errorf("oidc_login_stale")
		}
		return user, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return OIDCUser{}, err
	}
	err = db.Where("realm = ? AND username = ?", login.Realm, login.Username).First(&user).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		if !realm.AutoCreate {
			return OIDCUser{}, fmt.Errorf("oidc_user_not_registered")
		}
		return OIDCUser{Realm: login.Realm, Username: login.Username, Subject: login.Subject}, nil
	case err != nil:
		return OIDCUser{}, err
	case user.Subject != "":
		return OIDCUser{}, fmt.Errorf("oidc_username_taken")
	}
	user.Subject = login.Subject
	return user, nil
}

func deleteOIDCUser(db *gorm.DB, user OIDCUser) error {
	subject := user.Principal()
	if err := db.Where("subject_type = ? AND subject = ?", ACLSubjectUser, subject).Delete(&ACLGrant{}).Error; err != nil {
		return err
	}
	if err := renameGroupMember(db, subject, ""); err != nil {
		return err
	}
	var row models.User
	if err := db.Where("username = ? AND source = ?", subject, UserSourceOIDC).Limit(1).Find(&row).Error; err != nil {
		return err
	}
	if row.ID != 0 {
		if err := DeleteUserRowTx(db, row.ID); err != nil {
			return err
		}
	}
	return db.Delete(&OIDCUser{}, user.ID).Error
}

// Only realm rows are touched: if a native row were ever named name@realm, creating the copy
// fails instead of removing it.
func mirrorOIDCUser(db *gorm.DB, user OIDCUser) error {
	var row models.User
	if err := db.Where("username = ? AND source = ?", user.Principal(), UserSourceOIDC).Limit(1).Find(&row).Error; err != nil {
		return err
	}
	if row.ID != 0 && row.SharedUserID != user.ID {
		if err := DeleteUserRowTx(db, row.ID); err != nil {
			return err
		}
		row = models.User{}
	}
	if row.ID == 0 {
		return db.Create(&models.User{
			Username: user.Principal(), FullName: user.FullName, Email: user.Email, Notes: user.Notes,
			Locked: user.Locked, ConfirmGuestActions: user.ConfirmGuestActions, DisablePassword: true,
			Source: UserSourceOIDC, SharedUserID: user.ID,
		}).Error
	}
	if row.Locked != user.Locked {
		if err := db.Where("user_id = ?", row.ID).Delete(&models.Token{}).Error; err != nil {
			return err
		}
	}
	return db.Model(&row).Updates(map[string]any{
		"full_name": user.FullName, "email": user.Email, "notes": user.Notes, "locked": user.Locked,
		"confirm_guest_actions": user.ConfirmGuestActions,
	}).Error
}

func reconcileOIDCUsersTx(tx *gorm.DB) error {
	var users []OIDCUser
	if err := tx.Find(&users).Error; err != nil {
		return err
	}
	var rows []models.User
	if err := tx.Where("source = ?", UserSourceOIDC).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if !slices.ContainsFunc(users, func(user OIDCUser) bool { return user.Principal() == row.Username }) {
			if err := DeleteUserRowTx(tx, row.ID); err != nil {
				return err
			}
		}
	}
	for _, user := range users {
		if err := mirrorOIDCUser(tx, user); err != nil {
			return err
		}
	}
	return nil
}

func prepareOIDCUsersForJoinTx(tx *gorm.DB) error {
	var rows []models.User
	if err := tx.Where("source = ?", UserSourceOIDC).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := DeleteUserRowTx(tx, row.ID); err != nil {
			return err
		}
	}
	return nil
}

func pruneRealmMappings(db *gorm.DB, group string) error {
	var realms []AuthRealm
	if err := db.Find(&realms).Error; err != nil {
		return err
	}
	for _, realm := range realms {
		kept := slices.DeleteFunc(slices.Clone(realm.GroupMappings), func(m OIDCGroupMapping) bool { return m.Group == group })
		if len(kept) == len(realm.GroupMappings) {
			continue
		}
		realm.GroupMappings = kept
		if err := db.Save(&realm).Error; err != nil {
			return err
		}
	}
	return nil
}
