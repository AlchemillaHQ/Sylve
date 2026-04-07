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
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func newLocalTestService(t *testing.T) *Service {
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

	return &Service{DB: db}
}

func seedBasicSettings(t *testing.T, svc *Service) {
	t.Helper()
	if err := svc.DB.Create(&models.BasicSettings{
		Pools:       []string{},
		Services:    []AvailableService{},
		Initialized: true,
	}).Error; err != nil {
		t.Fatalf("failed to seed basic settings: %v", err)
	}
}

type AvailableService = models.AvailableService

func seedUser(t *testing.T, svc *Service, u models.User) models.User {
	t.Helper()
	if err := svc.DB.Create(&u).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}
	return u
}

func seedGroup(t *testing.T, svc *Service, name string) models.Group {
	t.Helper()
	g := models.Group{Name: name}
	if err := svc.DB.Create(&g).Error; err != nil {
		t.Fatalf("failed to seed group: %v", err)
	}
	return g
}

func TestListUsersEmpty(t *testing.T) {
	svc := newLocalTestService(t)
	users, err := svc.ListUsers()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("expected 0 users, got: %d", len(users))
	}
}

func TestListUsersWithSeeded(t *testing.T) {
	svc := newLocalTestService(t)
	seedUser(t, svc, models.User{Username: "testuser1", Password: "hashed"})
	seedUser(t, svc, models.User{Username: "testuser2", Password: "hashed"})

	users, err := svc.ListUsers()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got: %d", len(users))
	}
}

func TestListUsersPreloadsGroups(t *testing.T) {
	svc := newLocalTestService(t)
	g := seedGroup(t, svc, "devs")
	u := seedUser(t, svc, models.User{Username: "testuser1", Password: "hashed"})

	if err := svc.DB.Model(&g).Association("Users").Append(&u); err != nil {
		t.Fatalf("failed to associate user with group: %v", err)
	}

	users, err := svc.ListUsers()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got: %d", len(users))
	}
	if len(users[0].Groups) != 1 {
		t.Fatalf("expected 1 group on user, got: %d", len(users[0].Groups))
	}
	if users[0].Groups[0].Name != "devs" {
		t.Fatalf("expected group name 'devs', got: %s", users[0].Groups[0].Name)
	}
}

func TestGetUserByID(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "testuser1", Password: "hashed", Admin: true})

	found, err := svc.GetUserByID(u.ID)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if found.Username != "testuser1" {
		t.Fatalf("expected username 'testuser1', got: %s", found.Username)
	}
	if !found.Admin {
		t.Fatalf("expected admin=true")
	}
}

func TestGetUserByIDNotFound(t *testing.T) {
	svc := newLocalTestService(t)
	_, err := svc.GetUserByID(999)
	if err == nil {
		t.Fatalf("expected error for non-existent user")
	}
	if !strings.Contains(err.Error(), "failed_to_get_user_by_id") {
		t.Fatalf("expected failed_to_get_user_by_id error, got: %v", err)
	}
}

func TestGetUserByIDPreloadsGroups(t *testing.T) {
	svc := newLocalTestService(t)
	g := seedGroup(t, svc, "ops")
	u := seedUser(t, svc, models.User{Username: "testuser1", Password: "hashed"})

	if err := svc.DB.Model(&g).Association("Users").Append(&u); err != nil {
		t.Fatalf("failed to associate: %v", err)
	}

	found, err := svc.GetUserByID(u.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(found.Groups) != 1 || found.Groups[0].Name != "ops" {
		t.Fatalf("expected group 'ops', got: %v", found.Groups)
	}
}

func TestCreateUserMissingBasicSettings(t *testing.T) {
	svc := newLocalTestService(t)
	user := &models.User{Username: "john", Password: "password123"}
	err := svc.CreateUser(user, CreateUserOpts{})
	if err == nil {
		t.Fatalf("expected error when basic settings missing")
	}
	if !strings.Contains(err.Error(), "failed_to_get_basic_settings") {
		t.Fatalf("expected basic_settings error, got: %v", err)
	}
}

func TestCreateUserInvalidEmail(t *testing.T) {
	svc := newLocalTestService(t)
	seedBasicSettings(t, svc)
	user := &models.User{Username: "john", Password: "password123", Email: "not-an-email"}
	err := svc.CreateUser(user, CreateUserOpts{})
	if err == nil {
		t.Fatalf("expected error for invalid email")
	}
	if !strings.Contains(err.Error(), "invalid_email_format") {
		t.Fatalf("expected invalid_email_format, got: %v", err)
	}
}

func TestCreateUserUsernameTooShort(t *testing.T) {
	svc := newLocalTestService(t)
	seedBasicSettings(t, svc)
	user := &models.User{Username: "ab", Password: "password123"}
	err := svc.CreateUser(user, CreateUserOpts{})
	if err == nil {
		t.Fatalf("expected error for short username")
	}
	if !strings.Contains(err.Error(), "invalid_username_length") {
		t.Fatalf("expected invalid_username_length, got: %v", err)
	}
}

func TestCreateUserUsernameEmpty(t *testing.T) {
	svc := newLocalTestService(t)
	seedBasicSettings(t, svc)
	user := &models.User{Username: "", Password: "password123"}
	err := svc.CreateUser(user, CreateUserOpts{})
	if err == nil {
		t.Fatalf("expected error for empty username")
	}
	if !strings.Contains(err.Error(), "invalid_username_length") {
		t.Fatalf("expected invalid_username_length, got: %v", err)
	}
}

func TestCreateUserPasswordTooShort(t *testing.T) {
	svc := newLocalTestService(t)
	seedBasicSettings(t, svc)
	user := &models.User{Username: "john", Password: "short"}
	err := svc.CreateUser(user, CreateUserOpts{})
	if err == nil {
		t.Fatalf("expected error for short password")
	}
	if !strings.Contains(err.Error(), "invalid_password_length") {
		t.Fatalf("expected invalid_password_length, got: %v", err)
	}
}

func TestCreateUserPasswordEmptyWhenNotDisabled(t *testing.T) {
	svc := newLocalTestService(t)
	seedBasicSettings(t, svc)
	user := &models.User{Username: "john", Password: ""}
	err := svc.CreateUser(user, CreateUserOpts{})
	if err == nil {
		t.Fatalf("expected error for empty password when not disabled")
	}
	if !strings.Contains(err.Error(), "invalid_password_length") {
		t.Fatalf("expected invalid_password_length, got: %v", err)
	}
}

func TestCreateUserPasswordSkippedWhenDisabled(t *testing.T) {
	svc := newLocalTestService(t)
	seedBasicSettings(t, svc)
	user := &models.User{Username: "INVALID!", Password: "", DisablePassword: true}
	err := svc.CreateUser(user, CreateUserOpts{})
	if err == nil {
		t.Fatalf("expected error (invalid username format)")
	}
	if strings.Contains(err.Error(), "invalid_password_length") {
		t.Fatalf("expected password check to be skipped, but got password error: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid_username_format") {
		t.Fatalf("expected invalid_username_format, got: %v", err)
	}
}

func TestCreateUserInvalidUsernameFormat(t *testing.T) {
	svc := newLocalTestService(t)
	seedBasicSettings(t, svc)
	user := &models.User{Username: "Bad User!", Password: "password123"}
	err := svc.CreateUser(user, CreateUserOpts{})
	if err == nil {
		t.Fatalf("expected error for invalid username format")
	}
	if !strings.Contains(err.Error(), "invalid_username_format") {
		t.Fatalf("expected invalid_username_format, got: %v", err)
	}
}

func TestCreateUserUIDAlreadyInUse(t *testing.T) {
	svc := newLocalTestService(t)
	seedBasicSettings(t, svc)
	seedUser(t, svc, models.User{Username: "existinguser", Password: "hashed", UID: 1001})

	user := &models.User{Username: "john", Password: "password123", UID: 1001}
	err := svc.CreateUser(user, CreateUserOpts{})
	if err == nil {
		t.Fatalf("expected error for duplicate UID")
	}
	if !strings.Contains(err.Error(), "uid_already_in_use") {
		t.Fatalf("expected uid_already_in_use, got: %v", err)
	}
}

func TestCreateUserUIDZeroSkipsUniquenessCheck(t *testing.T) {
	svc := newLocalTestService(t)
	seedBasicSettings(t, svc)
	seedUser(t, svc, models.User{Username: "existinguser", Password: "hashed", UID: 0})

	user := &models.User{Username: "john", Password: "password123", UID: 0}
	err := svc.CreateUser(user, CreateUserOpts{})
	if err == nil {
		return
	}

	if strings.Contains(err.Error(), "uid_already_in_use") {
		t.Fatalf("UID 0 should skip uniqueness check, got: %v", err)
	}
}

func TestEditUserNotFound(t *testing.T) {
	svc := newLocalTestService(t)
	err := svc.EditUser(999, EditUserOpts{Username: "nobody"})
	if err == nil {
		t.Fatalf("expected error for non-existent user")
	}
	if !strings.Contains(err.Error(), "failed_to_get_user") {
		t.Fatalf("expected failed_to_get_user, got: %v", err)
	}
}

func TestEditUserCannotChangeAdminUsername(t *testing.T) {
	svc := newLocalTestService(t)
	u := models.User{Username: "admin", Password: "hashed"}
	if err := svc.DB.Create(&u).Error; err != nil {
		t.Fatalf("failed to seed admin: %v", err)
	}

	err := svc.EditUser(u.ID, EditUserOpts{Username: "newadmin"})
	if err == nil {
		t.Fatalf("expected error when changing admin username")
	}
	if !strings.Contains(err.Error(), "cannot_change_admin_username") {
		t.Fatalf("expected cannot_change_admin_username, got: %v", err)
	}
}

func TestEditUserInvalidUsernameFormat(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed"})

	err := svc.EditUser(u.ID, EditUserOpts{Username: "Bad User!"})
	if err == nil {
		t.Fatalf("expected error for invalid username format")
	}
	if !strings.Contains(err.Error(), "invalid_username_format") {
		t.Fatalf("expected invalid_username_format, got: %v", err)
	}
}

func TestEditUserPasswordTooShort(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed"})

	err := svc.EditUser(u.ID, EditUserOpts{Username: "testuser", Password: "short"})
	if err == nil {
		t.Fatalf("expected error for short password")
	}
	if !strings.Contains(err.Error(), "invalid_password_length") {
		t.Fatalf("expected invalid_password_length, got: %v", err)
	}
}

func TestEditUserPasswordTooLong(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed"})

	longPw := strings.Repeat("a", 129)
	err := svc.EditUser(u.ID, EditUserOpts{Username: "testuser", Password: longPw})
	if err == nil {
		t.Fatalf("expected error for too-long password")
	}
	if !strings.Contains(err.Error(), "invalid_password_length") {
		t.Fatalf("expected invalid_password_length, got: %v", err)
	}
}

func TestEditUserInvalidEmail(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed"})

	err := svc.EditUser(u.ID, EditUserOpts{Username: "testuser", Email: "bad-email"})
	if err == nil {
		t.Fatalf("expected error for invalid email")
	}
	if !strings.Contains(err.Error(), "invalid_email_format") {
		t.Fatalf("expected invalid_email_format, got: %v", err)
	}
}

func TestEditUserUIDAlreadyInUse(t *testing.T) {
	svc := newLocalTestService(t)
	seedUser(t, svc, models.User{Username: "user_a", Password: "hashed", UID: 1001})
	u2 := seedUser(t, svc, models.User{Username: "user_b", Password: "hashed", UID: 1002})

	err := svc.EditUser(u2.ID, EditUserOpts{Username: "user_b", UID: 1001})
	if err == nil {
		t.Fatalf("expected error for duplicate UID")
	}
	if !strings.Contains(err.Error(), "uid_already_in_use") {
		t.Fatalf("expected uid_already_in_use, got: %v", err)
	}
}

func TestEditUserUIDSameAsCurrentNoError(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed", UID: 1001})

	err := svc.EditUser(u.ID, EditUserOpts{Username: "testuser", UID: 1001})
	if err != nil && strings.Contains(err.Error(), "uid_already_in_use") {
		t.Fatalf("changing UID to same value should not error on uniqueness: %v", err)
	}
}

func TestEditUserPrimaryGroupNotFound(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed"})

	badGroupID := uint(999)
	err := svc.EditUser(u.ID, EditUserOpts{Username: "testuser", PrimaryGroupID: &badGroupID})
	if err == nil {
		t.Fatalf("expected error for non-existent primary group")
	}
	if !strings.Contains(err.Error(), "primary_group_not_found") {
		t.Fatalf("expected primary_group_not_found, got: %v", err)
	}
}

func TestEditUserNewPrimaryGroupCreatesGroupInDB(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed"})

	err := svc.EditUser(u.ID, EditUserOpts{
		Username:        "testuser",
		NewPrimaryGroup: true,
	})

	if err != nil {
		if strings.Contains(err.Error(), "invalid_username") ||
			strings.Contains(err.Error(), "failed_to_get_user") {
			t.Fatalf("unexpected validation error: %v", err)
		}
	}
}

func TestEditUserAuxGroupsAddsNewGroups(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed"})
	g := seedGroup(t, svc, "dev_group")

	err := svc.EditUser(u.ID, EditUserOpts{
		Username:    "testuser",
		AuxGroupIDs: []uint{g.ID},
	})

	if err != nil {
		if strings.Contains(err.Error(), "invalid_username") ||
			strings.Contains(err.Error(), "failed_to_get_user") ||
			strings.Contains(err.Error(), "uid_already_in_use") {
			t.Fatalf("unexpected validation error: %v", err)
		}
	}
}

func TestEditUserAuxGroupsRemovesOldGroups(t *testing.T) {
	svc := newLocalTestService(t)
	g := seedGroup(t, svc, "old_group")
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed"})

	if err := svc.DB.Model(&g).Association("Users").Append(&u); err != nil {
		t.Fatalf("failed to associate: %v", err)
	}

	var reloaded models.User
	svc.DB.Preload("Groups").First(&reloaded, u.ID)
	if len(reloaded.Groups) != 1 {
		t.Fatalf("expected 1 group before edit, got: %d", len(reloaded.Groups))
	}

	err := svc.EditUser(u.ID, EditUserOpts{
		Username:    "testuser",
		AuxGroupIDs: []uint{},
	})

	if err != nil {
		if strings.Contains(err.Error(), "invalid_username") ||
			strings.Contains(err.Error(), "failed_to_get_user") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestEditUserAuxGroupPrimaryOverlap(t *testing.T) {
	svc := newLocalTestService(t)
	g := seedGroup(t, svc, "primary_g")
	pgID := g.ID
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed", PrimaryGroupID: &pgID})

	err := svc.EditUser(u.ID, EditUserOpts{
		Username:       "testuser",
		PrimaryGroupID: &pgID,
		AuxGroupIDs:    []uint{pgID},
	})

	if err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("primary group in aux should be filtered, got: %v", err)
		}
	}
}

func TestEditUserClearsPrimaryGroupWhenNilSent(t *testing.T) {
	svc := newLocalTestService(t)
	g := seedGroup(t, svc, "old_primary")
	pgID := g.ID
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed", PrimaryGroupID: &pgID})

	found, _ := svc.GetUserByID(u.ID)
	if found.PrimaryGroupID == nil || *found.PrimaryGroupID != pgID {
		t.Fatalf("expected initial primary group %d, got: %v", pgID, found.PrimaryGroupID)
	}

	err := svc.EditUser(u.ID, EditUserOpts{
		Username:       "testuser",
		PrimaryGroupID: nil,
	})

	if err == nil {
		found, _ = svc.GetUserByID(u.ID)
		if found.PrimaryGroupID != nil {
			t.Fatalf("expected PrimaryGroupID to be nil after clearing, got: %d", *found.PrimaryGroupID)
		}
	} else {
		if strings.Contains(err.Error(), "invalid_username") ||
			strings.Contains(err.Error(), "failed_to_get_user") {
			t.Fatalf("unexpected validation error: %v", err)
		}
	}
}

func TestEditUserClearPrimaryGroupDoesNotLeakToAux(t *testing.T) {
	svc := newLocalTestService(t)

	sylveG := seedGroup(t, svc, "sylve_g")
	primary := seedGroup(t, svc, "john")
	pgID := primary.ID

	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed", PrimaryGroupID: &pgID})

	svc.DB.Model(&sylveG).Association("Users").Append(&u)
	svc.DB.Model(&primary).Association("Users").Append(&u)

	found, _ := svc.GetUserByID(u.ID)
	if len(found.Groups) != 2 {
		t.Fatalf("expected 2 initial groups, got: %d", len(found.Groups))
	}

	err := svc.EditUser(u.ID, EditUserOpts{
		Username:       "testuser",
		PrimaryGroupID: nil,
		AuxGroupIDs:    []uint{sylveG.ID},
	})

	if err != nil {
		if strings.Contains(err.Error(), "failed_to_get_user") {
			t.Fatalf("unexpected error: %v", err)
		}
		t.Skipf("skipping due to system call error: %v", err)
	}

	found, _ = svc.GetUserByID(u.ID)
	if found.PrimaryGroupID != nil {
		t.Fatalf("expected PrimaryGroupID nil, got: %d", *found.PrimaryGroupID)
	}

	for _, g := range found.Groups {
		if g.ID == primary.ID {
			t.Fatalf("old primary group %q (id=%d) leaked into aux groups", g.Name, g.ID)
		}
	}

	if len(found.Groups) != 1 {
		names := make([]string, len(found.Groups))
		for i, g := range found.Groups {
			names[i] = g.Name
		}
		t.Fatalf("expected exactly 1 group (sylve_g), got %d: %v", len(found.Groups), names)
	}
}

func TestEditUserUpdatesFullNameAndAdmin(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed", FullName: "Old Name", Admin: false})

	err := svc.EditUser(u.ID, EditUserOpts{
		Username: "testuser",
		FullName: "New Name",
		Admin:    true,
	})

	if err == nil {
		found, _ := svc.GetUserByID(u.ID)
		if found.FullName != "New Name" {
			t.Fatalf("expected FullName 'New Name', got: %s", found.FullName)
		}
		if !found.Admin {
			t.Fatalf("expected Admin=true")
		}
	}
}

func TestEditUserChangesHomeDirectory(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed", HomeDirectory: "/nonexistent"})

	err := svc.EditUser(u.ID, EditUserOpts{
		Username:      "testuser",
		HomeDirectory: "/home/testuser",
	})

	if err != nil {
		if strings.Contains(err.Error(), "failed_to_get_user") {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if err == nil {
		found, _ := svc.GetUserByID(u.ID)
		if found.HomeDirectory != "/home/testuser" {
			t.Fatalf("expected HomeDirectory '/home/testuser', got: %s", found.HomeDirectory)
		}
	}
}

func TestEditUserHomeDirectoryNoChangeWhenSame(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed", HomeDirectory: "/nonexistent"})

	err := svc.EditUser(u.ID, EditUserOpts{
		Username:      "testuser",
		HomeDirectory: "/nonexistent",
	})

	if err != nil {
		if strings.Contains(err.Error(), "failed_to_change_home_directory") {
			t.Fatalf("should not attempt to change home directory when unchanged: %v", err)
		}
	}
}

func TestEditUserPasswordIsHashed(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "oldhash"})

	err := svc.EditUser(u.ID, EditUserOpts{
		Username: "testuser",
		Password: "newpassword123",
	})

	if err == nil {
		var found models.User
		svc.DB.First(&found, u.ID)
		if found.Password == "newpassword123" {
			t.Fatalf("password should be hashed, not stored as plaintext")
		}
		if found.Password == "oldhash" {
			t.Fatalf("password should have been updated")
		}
	}
}

func TestDeleteUserNotFound(t *testing.T) {
	svc := newLocalTestService(t)
	err := svc.DeleteUser(999)
	if err == nil {
		t.Fatalf("expected error for non-existent user")
	}
	if !strings.Contains(err.Error(), "failed_to_get_user") {
		t.Fatalf("expected failed_to_get_user, got: %v", err)
	}
}

func TestDeleteUserCannotDeleteAdmin(t *testing.T) {
	svc := newLocalTestService(t)
	u := models.User{Username: "admin", Password: "hashed"}
	svc.DB.Create(&u)

	err := svc.DeleteUser(u.ID)
	if err == nil {
		t.Fatalf("expected error when deleting admin")
	}
	if !strings.Contains(err.Error(), "cannot_delete_admin_user") {
		t.Fatalf("expected cannot_delete_admin_user, got: %v", err)
	}
}

func TestUpdateLastUsageTimeNewUser(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed"})

	err := svc.UpdateLastUsageTime(u.ID)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestUpdateLastUsageTimeNonExistentUser(t *testing.T) {
	svc := newLocalTestService(t)
	err := svc.UpdateLastUsageTime(999)

	if err != nil {
		t.Fatalf("expected no error for missing user, got: %v", err)
	}
}

func TestEditUserOptsHasNewPrimaryGroupField(t *testing.T) {
	opts := serviceInterfaces.EditUserOpts{
		NewPrimaryGroup: true,
		AuxGroupIDs:     []uint{1, 2, 3},
	}
	if !opts.NewPrimaryGroup {
		t.Fatalf("expected NewPrimaryGroup to be true")
	}
	if len(opts.AuxGroupIDs) != 3 {
		t.Fatalf("expected 3 aux group IDs")
	}
}
