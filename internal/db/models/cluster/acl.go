// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"fmt"
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ACLSubjectUser  = "user"
	ACLSubjectGroup = "group"
)

// Built-in roles live in code; this table holds custom ones.
type ACLRole struct {
	ID         uint     `gorm:"primaryKey" json:"id"`
	Name       string   `gorm:"uniqueIndex;not null" json:"name"`
	Privileges []string `gorm:"serializer:json;not null" json:"privileges"`
}

func (ACLRole) TableName() string { return "acl_roles" }

// Subject is "<username>@<realm>" for a user and the group name for a group.
type ACLGrant struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Path        string `gorm:"not null;uniqueIndex:idx_acl_grants_entry" json:"path"`
	SubjectType string `gorm:"not null;uniqueIndex:idx_acl_grants_entry" json:"subjectType"`
	Subject     string `gorm:"not null;uniqueIndex:idx_acl_grants_entry" json:"subject"`
	Role        string `gorm:"not null;uniqueIndex:idx_acl_grants_entry" json:"role"`
	Propagate   bool   `gorm:"not null" json:"propagate"`
}

func (ACLGrant) TableName() string { return "acl_grants" }

// Sylve's own cluster-wide permission groups, not Unix groups.
type ACLGroup struct {
	ID      uint     `gorm:"primaryKey" json:"id"`
	Name    string   `gorm:"uniqueIndex;not null" json:"name"`
	Members []string `gorm:"serializer:json;not null" json:"members"`
}

func (ACLGroup) TableName() string { return "acl_groups" }

const aclProtocol = 2

func RegisterACLHandlers(f *FSMDispatcher) {
	OnSince(f, aclProtocol, "acl_grant", "set", func(db *gorm.DB, grant *ACLGrant) error {
		if grant.Path == "" || grant.Subject == "" || grant.Role == "" ||
			(grant.SubjectType != ACLSubjectUser && grant.SubjectType != ACLSubjectGroup) {
			return fmt.Errorf("invalid_acl_grant")
		}
		grant.ID = 0
		return db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "path"}, {Name: "subject_type"}, {Name: "subject"}, {Name: "role"}},
			DoUpdates: clause.AssignmentColumns([]string{"propagate"}),
		}).Create(grant).Error
	})
	OnSince(f, aclProtocol, "acl_grant", "delete", func(db *gorm.DB, grant *ACLGrant) error {
		return db.Where("path = ? AND subject_type = ? AND subject = ? AND role = ?",
			grant.Path, grant.SubjectType, grant.Subject, grant.Role).Delete(&ACLGrant{}).Error
	})
	OnSince(f, aclProtocol, "acl_role", "set", func(db *gorm.DB, role *ACLRole) error {
		if role.Name == "" {
			return fmt.Errorf("invalid_acl_role")
		}
		role.ID = 0
		return db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "name"}},
			DoUpdates: clause.AssignmentColumns([]string{"privileges"}),
		}).Create(role).Error
	})
	OnSince(f, aclProtocol, "acl_role", "delete", func(db *gorm.DB, role *ACLRole) error {
		if err := db.Where("role = ?", role.Name).Delete(&ACLGrant{}).Error; err != nil {
			return err
		}
		return db.Where("name = ?", role.Name).Delete(&ACLRole{}).Error
	})
	OnSince(f, aclProtocol, "acl_group", "set", func(db *gorm.DB, group *ACLGroup) error {
		if group.Name == "" {
			return fmt.Errorf("invalid_acl_group")
		}
		group.ID = 0
		if group.Members == nil {
			group.Members = []string{}
		}
		return db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "name"}},
			DoUpdates: clause.AssignmentColumns([]string{"members"}),
		}).Create(group).Error
	})
	OnSince(f, aclProtocol, "acl_group", "delete", func(db *gorm.DB, group *ACLGroup) error {
		if err := db.Where("subject_type = ? AND subject = ?", ACLSubjectGroup, group.Name).Delete(&ACLGrant{}).Error; err != nil {
			return err
		}
		if err := pruneRealmMappings(db, group.Name); err != nil {
			return err
		}
		return db.Where("name = ?", group.Name).Delete(&ACLGroup{}).Error
	})
	OnSince(f, aclProtocol, "acl_subject", "delete", func(db *gorm.DB, subject *ACLSubject) error {
		if err := db.Where("subject_type = ? AND subject = ?", subject.Type, subject.Name).Delete(&ACLGrant{}).Error; err != nil {
			return err
		}
		if subject.Type != ACLSubjectUser {
			return nil
		}
		return renameGroupMember(db, subject.Name, "")
	})
	OnSince(f, aclProtocol, "acl_subject", "rename", func(db *gorm.DB, rename *ACLSubjectRename) error {
		if rename.To == "" {
			return fmt.Errorf("invalid_acl_subject")
		}
		if err := renameGrantSubject(db, rename.Type, rename.From, rename.To); err != nil {
			return err
		}
		if rename.Type != ACLSubjectUser {
			return nil
		}
		return renameGroupMember(db, rename.From, rename.To)
	})
}

func renameGroupMember(db *gorm.DB, from, to string) error {
	var groups []ACLGroup
	if err := db.Find(&groups).Error; err != nil {
		return err
	}
	for _, group := range groups {
		if !slices.Contains(group.Members, from) {
			continue
		}
		group.Members = slices.DeleteFunc(group.Members, func(member string) bool { return member == from })
		if to != "" && !slices.Contains(group.Members, to) {
			group.Members = append(group.Members, to)
		}
		slices.Sort(group.Members)
		if err := db.Save(&group).Error; err != nil {
			return err
		}
	}
	return nil
}

func renameGrantSubject(db *gorm.DB, subjectType, from, to string) error {
	if err := db.Exec("INSERT OR IGNORE INTO acl_grants (path, subject_type, subject, role, propagate) "+
		"SELECT path, subject_type, ?, role, propagate FROM acl_grants WHERE subject_type = ? AND subject = ? ORDER BY id",
		to, subjectType, from).Error; err != nil {
		return err
	}
	return db.Where("subject_type = ? AND subject = ?", subjectType, from).Delete(&ACLGrant{}).Error
}

type ACLSubject struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type ACLSubjectRename struct {
	Type string `json:"type"`
	From string `json:"from"`
	To   string `json:"to"`
}
