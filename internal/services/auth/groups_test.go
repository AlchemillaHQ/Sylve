// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/system"
)

func newGroupTestService(t *testing.T) *Service {
	t.Helper()

	db := testutil.NewSQLiteTestDB(
		t,
		&models.User{},
		&models.Group{},
		&models.Token{},
		&models.SystemSecrets{},
		&models.BasicSettings{},
		&models.WebAuthnCredential{},
		&models.WebAuthnChallenge{},
		&models.PAMIdentity{},
	)

	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		return "", nil
	}))

	return &Service{DB: db}
}

func seedGroupTestUser(t *testing.T, svc *Service, username string) models.User {
	t.Helper()
	u := models.User{Username: username, Password: "hashed", Source: "pam"}
	if err := svc.DB.Create(&u).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}
	return u
}

func TestListGroupsEmpty(t *testing.T) {
	svc := newGroupTestService(t)

	groups, err := svc.ListGroups()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("expected 0 groups, got: %d", len(groups))
	}
}

func TestListGroupsPreloadsUsers(t *testing.T) {
	svc := newGroupTestService(t)
	u := seedGroupTestUser(t, svc, "alice")
	g := models.Group{Name: "devs"}
	if err := svc.DB.Create(&g).Error; err != nil {
		t.Fatalf("failed to create group: %v", err)
	}
	if err := svc.DB.Model(&g).Association("Users").Append(&u); err != nil {
		t.Fatalf("failed to associate: %v", err)
	}

	groups, err := svc.ListGroups()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got: %d", len(groups))
	}
	if len(groups[0].Users) != 1 {
		t.Fatalf("expected 1 user in group, got: %d", len(groups[0].Users))
	}
	if groups[0].Users[0].Username != "alice" {
		t.Fatalf("expected user 'alice', got: %s", groups[0].Users[0].Username)
	}
}

func TestCreateGroupInvalidName(t *testing.T) {
	svc := newGroupTestService(t)
	seedGroupTestUser(t, svc, "alice")

	_, err := svc.CreateGroup("Bad Name!", []string{"alice"})
	if err == nil {
		t.Fatalf("expected error for invalid group name")
	}
	if !strings.Contains(err.Error(), "invalid_group_name") {
		t.Fatalf("expected invalid_group_name, got: %v", err)
	}
}

func TestCreateGroupMemberNotFound(t *testing.T) {
	svc := newGroupTestService(t)

	_, err := svc.CreateGroup("devs", []string{"nonexistent"})
	if err == nil {
		t.Fatalf("expected error for unknown member")
	}
	if !strings.Contains(err.Error(), "group_member_not_found") {
		t.Fatalf("expected group_member_not_found, got: %v", err)
	}
}

func TestDeleteGroupNotFound(t *testing.T) {
	svc := newGroupTestService(t)

	_, err := svc.DeleteGroup(999)
	if err == nil {
		t.Fatalf("expected error for non-existent group")
	}
	if !strings.Contains(err.Error(), "group_not_found") {
		t.Fatalf("expected group_not_found, got: %v", err)
	}
}

func TestSyncGroupMembersWheelRootProtected(t *testing.T) {
	svc := newGroupTestService(t)
	restore := system.SetRunCommand(func(command string, args ...string) (string, error) {
		switch command {
		case "/usr/bin/getent":
			return "wheel:*:0:root", nil
		case "/usr/bin/id":
			if len(args) > 0 && args[0] == "-nG" {
				return "wheel", nil
			}
			return "uid=0(root) gid=0(wheel)", nil
		default:
			return "", nil
		}
	})
	defer restore()

	g := models.Group{Name: "wheel"}
	if err := svc.DB.Create(&g).Error; err != nil {
		t.Fatalf("failed to create group: %v", err)
	}
	root := seedGroupTestUser(t, svc, "root")
	if err := svc.DB.Model(&g).Association("Users").Append(&root); err != nil {
		t.Fatalf("failed to associate root with wheel: %v", err)
	}

	_, err := svc.SyncGroupMembers(g.ID, []string{})
	if err == nil || !strings.Contains(err.Error(), "cannot_remove_root_from_wheel") {
		t.Fatalf("expected root protection error, got: %v", err)
	}
}
