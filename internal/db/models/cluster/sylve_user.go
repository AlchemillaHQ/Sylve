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

	"github.com/alchemillahq/sylve/internal/db/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SylveUser is a local (sylve realm) user. Each node keeps its own users row for it, so sessions,
// passkeys and everything else keyed on users.id stay per node.
type SylveUser struct {
	ID                  uint   `gorm:"primaryKey" json:"id"`
	Username            string `gorm:"uniqueIndex;not null" json:"username"`
	Password            string `gorm:"not null" json:"password"`
	FullName            string `json:"fullName"`
	Email               string `json:"email"`
	Notes               string `json:"notes"`
	Locked              bool   `gorm:"not null" json:"locked"`
	DisablePassword     bool   `gorm:"not null" json:"disablePassword"`
	ConfirmGuestActions bool   `gorm:"not null" json:"confirmGuestActions"`
}

func (SylveUser) TableName() string { return "sylve_users" }

type SylveUserRename struct {
	From string `json:"from"`
	To   string `json:"to"`
}

const sylveRealmSuffix = "@sylve"

// A node's own users rows never fail these commands: a name it holds for someone else is skipped
// there, so every node applies the same change to sylve_users.
func RegisterSylveUserHandlers(f *FSMDispatcher) {
	// A taken name stays with whoever shared it first.
	OnSince(f, aclProtocol, "sylve_user", "create", func(db *gorm.DB, user *SylveUser) error {
		if user.Username == "" || user.Password == "" {
			return fmt.Errorf("invalid_sylve_user")
		}
		user.ID = 0
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(user).Error; err != nil {
			return err
		}
		return mirrorSylveUser(db, user.Username)
	})
	OnSince(f, aclProtocol, "sylve_user", "update", func(db *gorm.DB, user *SylveUser) error {
		result := db.Model(&SylveUser{}).Where("username = ?", user.Username).
			Select("password", "full_name", "email", "notes", "locked", "disable_password", "confirm_guest_actions").
			Updates(user)
		if err := requireRowsAffected(result, 1); err != nil {
			return err
		}
		return mirrorSylveUser(db, user.Username)
	})
	OnSince(f, aclProtocol, "sylve_user", "rename", func(db *gorm.DB, rename *SylveUserRename) error {
		if rename.To == "" {
			return fmt.Errorf("invalid_sylve_user")
		}
		result := db.Model(&SylveUser{}).Where("username = ?", rename.From).Update("username", rename.To)
		if err := requireRowsAffected(result, 1); err != nil {
			return err
		}
		if err := renameGrantSubject(db, ACLSubjectUser, rename.From+sylveRealmSuffix, rename.To+sylveRealmSuffix); err != nil {
			return err
		}
		if err := renameGroupMember(db, rename.From+sylveRealmSuffix, rename.To+sylveRealmSuffix); err != nil {
			return err
		}
		var row models.User
		err := db.Where("username = ? AND source = ? AND shared", rename.From, "local").First(&row).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
		case err != nil:
			return err
		default:
			var taken int64
			if err := db.Model(&models.User{}).Where("username = ?", rename.To).Count(&taken).Error; err != nil {
				return err
			}
			if taken > 0 {
				if err := DeleteUserRowTx(db, row.ID); err != nil {
					return err
				}
				break
			}
			if err := db.Where("user_id = ?", row.ID).Delete(&models.Token{}).Error; err != nil {
				return err
			}
			if err := db.Model(&row).Update("username", rename.To).Error; err != nil {
				return err
			}
		}
		return mirrorSylveUser(db, rename.To)
	})
	OnSince(f, aclProtocol, "sylve_user", "delete", func(db *gorm.DB, user *SylveUser) error {
		if err := db.Where("username = ?", user.Username).Delete(&SylveUser{}).Error; err != nil {
			return err
		}
		if err := db.Where("subject_type = ? AND subject = ?", ACLSubjectUser, user.Username+sylveRealmSuffix).
			Delete(&ACLGrant{}).Error; err != nil {
			return err
		}
		if err := renameGroupMember(db, user.Username+sylveRealmSuffix, ""); err != nil {
			return err
		}
		var row models.User
		err := db.Where("username = ? AND source = ? AND shared", user.Username, "local").First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return DeleteUserRowTx(db, row.ID)
	})
}

// A row the node holds for someone else, a PAM user or a local user it hasn't shared, keeps the
// name. Only the node that shared a local user has its exact password hash, so that row is adopted.
func mirrorSylveUser(db *gorm.DB, username string) error {
	var shared SylveUser
	if err := db.Where("username = ?", username).First(&shared).Error; err != nil {
		return err
	}
	clusterID, err := ReadClusterID(db)
	if err != nil {
		return err
	}
	var row models.User
	err = db.Where("username = ?", username).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Create(&models.User{
			Username: shared.Username, Password: shared.Password, FullName: shared.FullName,
			Email: shared.Email, Notes: shared.Notes, Locked: shared.Locked,
			DisablePassword: shared.DisablePassword, ConfirmGuestActions: shared.ConfirmGuestActions,
			Source: "local", Shared: true, SharedClusterID: clusterID, SharedUserID: shared.ID,
		}).Error
	}
	if err != nil {
		return err
	}
	if row.Source != "local" || (!row.Shared && row.Password != shared.Password) {
		return nil
	}
	if row.Password != shared.Password || row.Locked != shared.Locked || row.DisablePassword != shared.DisablePassword {
		if err := db.Where("user_id = ?", row.ID).Delete(&models.Token{}).Error; err != nil {
			return err
		}
	}
	updates := map[string]any{
		"password": shared.Password, "full_name": shared.FullName, "email": shared.Email,
		"notes": shared.Notes, "locked": shared.Locked, "disable_password": shared.DisablePassword,
		"confirm_guest_actions": shared.ConfirmGuestActions, "shared": true,
	}
	// Standalone edits after leaving retain the original reference until the next join.
	if clusterID != "" {
		updates["shared_cluster_id"], updates["shared_user_id"] = clusterID, shared.ID
	}
	return db.Model(&row).Updates(updates).Error
}

func RestoreSylveUserTx(tx *gorm.DB, username string) error {
	err := mirrorSylveUser(tx, username)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

// Only copies from the destination cluster follow its snapshot. Accounts created while
// standalone, or brought from another cluster, remain local until offered for sharing.
func prepareSylveUsersForJoinTx(tx *gorm.DB, clusterID string) error {
	if err := tx.Model(&models.User{}).Where("source = ?", "local").Update("shared", false).Error; err != nil {
		return err
	}
	if clusterID != "" {
		if err := tx.Model(&models.User{}).Where("source = ? AND shared_cluster_id = ? AND shared_user_id <> 0", "local", clusterID).
			Update("shared", true).Error; err != nil {
			return err
		}
	}
	return tx.Model(&models.User{}).Where("source = ? AND NOT shared", "local").
		Updates(map[string]any{"shared_cluster_id": "", "shared_user_id": 0}).Error
}

func reconcileSylveUsersTx(tx *gorm.DB) error {
	var users []SylveUser
	if err := tx.Find(&users).Error; err != nil {
		return err
	}
	clusterID, err := ReadClusterID(tx)
	if err != nil {
		return err
	}
	byName := make(map[string]uint, len(users))
	for _, user := range users {
		byName[user.Username] = user.ID
	}
	var copies []models.User
	if err := tx.Where("source = ? AND shared", "local").Find(&copies).Error; err != nil {
		return err
	}
	for _, row := range copies {
		id, present := byName[row.Username]
		if !present || row.SharedClusterID != "" && (row.SharedClusterID != clusterID || row.SharedUserID != id) {
			if err := DeleteUserRowTx(tx, row.ID); err != nil {
				return err
			}
		}
	}
	for _, user := range users {
		if err := mirrorSylveUser(tx, user.Username); err != nil {
			return err
		}
	}
	return nil
}

func DeleteUserRowTx(tx *gorm.DB, userID uint) error {
	for _, model := range []any{&models.Token{}, &models.WebAuthnCredential{}, &models.WebAuthnChallenge{}} {
		if err := tx.Where("user_id = ?", userID).Delete(model).Error; err != nil {
			return err
		}
	}
	if err := tx.Model(&models.User{ID: userID}).Association("Groups").Clear(); err != nil {
		return err
	}
	return tx.Delete(&models.User{}, userID).Error
}
