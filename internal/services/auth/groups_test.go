// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"fmt"
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

func TestExternalGroupsAreReadOnly(t *testing.T) {
	service := newLocalTestService(t)
	group := models.Group{Name: "directory-users", ExternallyManaged: true}
	if err := service.DB.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	forbidExternalPAMSideEffects(t)
	if _, err := service.DeleteGroup(group.ID); err == nil || err.Error() != "external_group_read_only" {
		t.Fatalf("expected external group deletion rejection, got %v", err)
	}
	if _, err := service.SyncGroupMembers(group.ID, nil); err == nil || err.Error() != "external_group_read_only" {
		t.Fatalf("expected external group edit rejection, got %v", err)
	}
	if _, err := service.loadManagedGroupByID(group.ID); err == nil || err.Error() != "external_group_read_only" {
		t.Fatalf("expected managed primary group rejection, got %v", err)
	}
}

func TestManagedGroupsCannotChangeExternalUserMemberships(t *testing.T) {
	for _, member := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing-member=%t", member), func(t *testing.T) {
			service := newLocalTestService(t)
			user := seedUser(t, service, models.User{Username: "directory-user", Source: "pam", ExternallyManaged: true})
			group := seedGroup(t, service, "staff")
			if member {
				if err := service.DB.Model(&group).Association("Users").Append(&user); err != nil {
					t.Fatal(err)
				}
			}
			forbidExternalPAMSideEffects(t)
			members := []string{user.Username}
			if member {
				members = nil
			}
			if _, err := service.SyncGroupMembers(group.ID, members); err == nil || err.Error() != "external_user_read_only" {
				t.Fatalf("expected external membership rejection, got %v", err)
			}
			if member {
				if _, err := service.DeleteGroup(group.ID); err == nil || err.Error() != "external_user_read_only" {
					t.Fatalf("expected external membership deletion rejection, got %v", err)
				}
			}
		})
	}
}

func TestManagedGroupCanPreserveExternalUserMembership(t *testing.T) {
	service := newLocalTestService(t)
	user := seedUser(t, service, models.User{Username: "directory-user", Source: "pam", ExternallyManaged: true})
	group := seedGroup(t, service, "staff")
	if err := service.DB.Model(&group).Association("Users").Append(&user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		if command == "/usr/bin/getent" {
			return "staff:*:1001:", nil
		}
		t.Fatalf("preserving external membership must not run Unix commands: %s %v", command, args)
		return "", nil
	}))
	updated, err := service.SyncGroupMembers(group.ID, []string{user.Username})
	if err != nil || len(updated.Users) != 1 || updated.Users[0].ID != user.ID {
		t.Fatalf("unchanged external membership was not preserved: group=%+v error=%v", updated, err)
	}
}

func TestPrivatePAMGroupCannotAdoptExternalGroup(t *testing.T) {
	for _, recorded := range []bool{false, true} {
		t.Run(fmt.Sprintf("recorded=%t", recorded), func(t *testing.T) {
			service := newLocalTestService(t)
			if recorded {
				if err := service.DB.Create(&models.Group{Name: "alice", ExternallyManaged: true}).Error; err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
				if !recorded && command == "/usr/bin/getent" {
					return "alice:*:344200007:", nil
				}
				if !recorded && command == "/usr/sbin/pw" && len(args) == 5 && args[0] == "-V" {
					return "", nil
				}
				t.Fatalf("unexpected group mutation: %s %v", command, args)
				return "", nil
			}))
			if err := service.validatePrivatePAMGroup("alice"); err == nil || err.Error() != "external_group_read_only" {
				t.Fatalf("expected external group adoption rejection, got %v", err)
			}
		})
	}
}

func TestEditManagedPamUserWithExternalGroups(t *testing.T) {
	for _, test := range []struct {
		name            string
		primaryExternal bool
		auxExternal     bool
		change          func(*EditUserOpts, []models.Group)
	}{
		{name: "preserve managed groups"},
		{name: "preserve external auxiliary group", auxExternal: true},
		{name: "preserve external primary group", primaryExternal: true},
		{name: "preserve both external groups", primaryExternal: true, auxExternal: true},
		{
			name: "remove external auxiliary group", auxExternal: true,
			change: func(opts *EditUserOpts, _ []models.Group) { opts.AuxGroupIDs = nil },
		},
		{
			name: "add external auxiliary group",
			change: func(opts *EditUserOpts, groups []models.Group) {
				opts.AuxGroupIDs = append(opts.AuxGroupIDs, groups[2].ID)
			},
		},
		{
			name:   "assign external primary group",
			change: func(opts *EditUserOpts, groups []models.Group) { opts.PrimaryGroupID = &groups[2].ID },
		},
		{
			name: "promote external auxiliary group", auxExternal: true,
			change: func(opts *EditUserOpts, groups []models.Group) {
				opts.PrimaryGroupID = &groups[1].ID
				opts.AuxGroupIDs = []uint{groups[0].ID}
			},
		},
		{
			name: "demote external primary group", primaryExternal: true,
			change: func(opts *EditUserOpts, groups []models.Group) {
				opts.PrimaryGroupID = &groups[1].ID
				opts.AuxGroupIDs = []uint{groups[0].ID}
			},
		},
		{
			name: "replace external primary with private group", primaryExternal: true,
			change: func(opts *EditUserOpts, _ []models.Group) {
				opts.PrimaryGroupID = nil
				opts.NewPrimaryGroup = true
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newLocalTestService(t)
			groups := []models.Group{
				{Name: "primary", ExternallyManaged: test.primaryExternal},
				{Name: "auxiliary", ExternallyManaged: test.auxExternal},
				{Name: "other-directory-group", ExternallyManaged: true},
			}
			if err := service.DB.Create(&groups).Error; err != nil {
				t.Fatal(err)
			}
			user := seedUser(t, service, models.User{
				Username: "alice", Source: "pam", UID: 1001, Shell: "/bin/sh",
				HomeDirectory: "/nonexistent", PrimaryGroupID: &groups[0].ID,
			})
			if err := service.DB.Model(&user).Association("Groups").Replace(groups[:2]); err != nil {
				t.Fatal(err)
			}
			loaded, err := service.GetUserByID(user.ID)
			if err != nil {
				t.Fatal(err)
			}
			seedUserToken(t, service, user.ID, "existing-session")
			forbidExternalPAMSideEffects(t)
			t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
				if command == "/usr/bin/id" {
					return "uid=1001(alice)", nil
				}
				if command == "/usr/bin/getent" && len(args) == 2 && args[0] == "group" {
					for _, group := range groups {
						if group.Name == args[1] && !group.ExternallyManaged {
							return fmt.Sprintf("%s:*:1001:", group.Name), nil
						}
					}
				}
				t.Fatalf("unexpected Unix operation: %s %v", command, args)
				return "", nil
			}))
			opts := externalPAMEditOpts(loaded)
			opts.Email = "updated@example.com"
			if test.change != nil {
				test.change(&opts, groups)
			} else {
				opts.AuxGroupIDs = append(opts.AuxGroupIDs, groups[1].ID)
			}
			err = service.EditUser(user.ID, opts)
			if test.change != nil {
				if err == nil || err.Error() != "external_group_read_only" {
					t.Fatalf("expected external membership protection, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("metadata edit rejected unchanged memberships: %v", err)
			}
			reloaded, err := service.GetUserByID(user.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (reloaded.Email == opts.Email) != (test.change == nil) {
				t.Fatalf("unexpected metadata update: %+v", reloaded)
			}
			if reloaded.ExternallyManaged || len(reloaded.Groups) != 2 ||
				reloaded.PrimaryGroupID == nil || *reloaded.PrimaryGroupID != groups[0].ID ||
				userTokenCount(t, service, user.ID) != 1 {
				t.Fatalf("edit changed ownership, memberships, or sessions: %+v", reloaded)
			}
			reloadedGroups := groupIDs(reloaded.Groups)
			for _, expected := range groups[:2] {
				actual, exists := reloadedGroups[expected.ID]
				if !exists || actual.ExternallyManaged != expected.ExternallyManaged {
					t.Fatalf("group membership or ownership changed: %+v", reloaded.Groups)
				}
			}
		})
	}
}
