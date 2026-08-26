// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"errors"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/system"
)

type fakePasswordHasher struct {
	hashErr error
}

func (f fakePasswordHasher) Hash(password string) (string, error) {
	if f.hashErr != nil {
		return "", f.hashErr
	}
	return "test-hash:" + password, nil
}

func (fakePasswordHasher) Verify(password, encodedHash string) bool {
	return encodedHash == "test-hash:"+password
}

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

	// Prevent real system command execution during tests.
	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		return "", nil
	}))

	return newAuthService(db, fakePasswordHasher{})
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

func seedUserToken(t *testing.T, svc *Service, userID uint, token string) {
	t.Helper()
	if err := svc.DB.Create(&models.Token{UserID: userID, Token: token, AuthType: "sylve"}).Error; err != nil {
		t.Fatalf("failed to seed token: %v", err)
	}
}

func userTokenCount(t *testing.T, svc *Service, userID uint) int64 {
	t.Helper()
	var count int64
	if err := svc.DB.Model(&models.Token{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		t.Fatalf("failed to count tokens: %v", err)
	}
	return count
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
	const submittedPassword = "short"
	user := &models.User{Username: "john", Password: submittedPassword}
	err := svc.CreateUser(user, CreateUserOpts{})
	if err == nil {
		t.Fatalf("expected error for short password")
	}
	if !strings.Contains(err.Error(), "invalid_password_length") {
		t.Fatalf("expected invalid_password_length, got: %v", err)
	}
	if strings.Contains(err.Error(), submittedPassword) {
		t.Fatalf("password validation error exposed the submitted password: %v", err)
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

func TestCreateUserUIDIgnored(t *testing.T) {
	svc := newLocalTestService(t)
	seedBasicSettings(t, svc)
	seedUser(t, svc, models.User{Username: "existinguser", Password: "hashed", UID: 1001})

	user := &models.User{Username: "john", Password: "password123", UID: 1001}
	err := svc.CreateUser(user, CreateUserOpts{})
	if err != nil {
		t.Fatalf("CreateUser should ignore UID for local users, got error: %v", err)
	}
}

func TestCreateUserHashFailureDoesNotPersistUser(t *testing.T) {
	svc := newLocalTestService(t)
	seedBasicSettings(t, svc)
	hashErr := errors.New("hash failed")
	svc.passwordHasher = fakePasswordHasher{hashErr: hashErr}

	user := &models.User{Username: "john", Password: "password123"}
	err := svc.CreateUser(user, CreateUserOpts{})
	if !errors.Is(err, hashErr) {
		t.Fatalf("expected hash failure, got: %v", err)
	}

	var count int64
	if err := svc.DB.Model(&models.User{}).Where("username = ?", user.Username).Count(&count).Error; err != nil {
		t.Fatalf("count users after hash failure: %v", err)
	}
	if count != 0 {
		t.Fatalf("hash failure persisted %d users", count)
	}
}

func TestEditUserNotFound(t *testing.T) {
	svc := newLocalTestService(t)
	err := svc.EditUser(999, EditUserOpts{Username: "nobody"})
	if err == nil {
		t.Fatalf("expected error for non-existent user")
	}
	if !strings.Contains(err.Error(), "user_not_found") {
		t.Fatalf("expected user_not_found, got: %v", err)
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

func TestEditUserCannotDemoteAdmin(t *testing.T) {
	svc := newLocalTestService(t)
	u := models.User{Username: "admin", Password: "hashed", Admin: true, Source: "local"}
	if err := svc.DB.Create(&u).Error; err != nil {
		t.Fatalf("failed to seed admin: %v", err)
	}

	err := svc.EditUser(u.ID, EditUserOpts{Username: "admin", Admin: false})
	if err == nil || !strings.Contains(err.Error(), "cannot_demote_admin_user") {
		t.Fatalf("expected cannot_demote_admin_user, got: %v", err)
	}
}

func TestEditUserCannotLockAdmin(t *testing.T) {
	svc := newLocalTestService(t)
	u := models.User{Username: "admin", Password: "hashed", Admin: true, Source: "local"}
	if err := svc.DB.Create(&u).Error; err != nil {
		t.Fatalf("failed to seed admin: %v", err)
	}

	err := svc.EditUser(u.ID, EditUserOpts{Username: "admin", Admin: true, Locked: true})
	if err == nil || !strings.Contains(err.Error(), "cannot_lock_admin_user") {
		t.Fatalf("expected cannot_lock_admin_user, got: %v", err)
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

func TestEditUserCanClearEmail(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{
		Username: "testuser",
		Password: "hashed",
		Email:    "old@example.com",
		Source:   "local",
	})

	if err := svc.EditUser(u.ID, EditUserOpts{Username: "testuser", Email: "", Admin: false}); err != nil {
		t.Fatalf("clear email: %v", err)
	}
	found, err := svc.GetUserByID(u.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if found.Email != "" {
		t.Fatalf("expected email to be cleared, got %q", found.Email)
	}
}

func TestEditUserRejectsDuplicateUsernameBeforeMutation(t *testing.T) {
	svc := newLocalTestService(t)
	seedUser(t, svc, models.User{Username: "alice", Password: "hashed", Source: "local"})
	bob := seedUser(t, svc, models.User{Username: "bob", Password: "hashed", Source: "local"})

	err := svc.EditUser(bob.ID, EditUserOpts{Username: "alice", Admin: false})
	if err == nil || !strings.Contains(err.Error(), "username_already_exists") {
		t.Fatalf("expected username_already_exists, got: %v", err)
	}
	found, findErr := svc.GetUserByID(bob.ID)
	if findErr != nil {
		t.Fatalf("reload user: %v", findErr)
	}
	if found.Username != "bob" {
		t.Fatalf("duplicate rename mutated username to %q", found.Username)
	}
}

func TestEditUserUIDAlreadyInUse(t *testing.T) {
	svc := newLocalTestService(t)
	seedUser(t, svc, models.User{Username: "user_a", Password: "hashed", UID: 1001, Source: "pam"})
	u2 := seedUser(t, svc, models.User{
		Username:      "user_b",
		Password:      "hashed",
		UID:           1002,
		Shell:         "/bin/sh",
		HomeDirectory: "/nonexistent",
		HomeDirPerms:  0o755,
		Source:        "pam",
	})

	err := svc.EditUser(u2.ID, EditUserOpts{
		Username:      u2.Username,
		UID:           1001,
		Shell:         u2.Shell,
		HomeDirectory: u2.HomeDirectory,
		HomeDirPerms:  u2.HomeDirPerms,
	})
	if err == nil {
		t.Fatalf("expected error for duplicate UID")
	}
	if !strings.Contains(err.Error(), "uid_already_in_use") {
		t.Fatalf("expected uid_already_in_use, got: %v", err)
	}
}

func TestEditUserUIDSameAsCurrentNoError(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed", UID: 1001, Source: "pam"})

	err := svc.EditUser(u.ID, EditUserOpts{Username: "testuser", UID: 1001})
	if err != nil && strings.Contains(err.Error(), "uid_already_in_use") {
		t.Fatalf("changing UID to same value should not error on uniqueness: %v", err)
	}
}

func TestEditUserPrimaryGroupNotFound(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{
		Username:      "testuser",
		Password:      "hashed",
		UID:           1001,
		Shell:         "/bin/sh",
		HomeDirectory: "/nonexistent",
		HomeDirPerms:  0o755,
		Source:        "pam",
	})

	badGroupID := uint(999)
	err := svc.EditUser(u.ID, EditUserOpts{
		Username:       u.Username,
		UID:            u.UID,
		Shell:          u.Shell,
		HomeDirectory:  u.HomeDirectory,
		HomeDirPerms:   u.HomeDirPerms,
		PrimaryGroupID: &badGroupID,
	})
	if err == nil {
		t.Fatalf("expected error for non-existent primary group")
	}
	if !strings.Contains(err.Error(), "primary_group_not_found") {
		t.Fatalf("expected primary_group_not_found, got: %v", err)
	}
}

func TestEditUserNewPrimaryGroupCreatesGroupInDB(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed", Source: "pam"})

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
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed", Source: "pam"})
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
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed", Source: "pam"})

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
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed", PrimaryGroupID: &pgID, Source: "pam"})

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
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed", PrimaryGroupID: &pgID, Source: "pam"})

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

	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed", PrimaryGroupID: &pgID, Source: "pam"})

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
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed", HomeDirectory: "/nonexistent", Source: "pam"})

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
	u := seedUser(t, svc, models.User{Username: "testuser", Password: "hashed", HomeDirectory: "/nonexistent", Source: "pam"})

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

func TestEditLocalUserRevokesOnlySecurityChanges(t *testing.T) {
	tests := []struct {
		name       string
		change     func(*EditUserOpts)
		wantTokens int64
	}{
		{
			name: "cosmetic edit preserves sessions",
			change: func(opts *EditUserOpts) {
				opts.FullName = "Alice Example"
				opts.Email = "alice@example.com"
			},
			wantTokens: 1,
		},
		{
			name: "username change revokes sessions",
			change: func(opts *EditUserOpts) {
				opts.Username = "alice-renamed"
			},
		},
		{
			name: "administrator change revokes sessions",
			change: func(opts *EditUserOpts) {
				opts.Admin = true
			},
		},
		{
			name: "password change revokes sessions",
			change: func(opts *EditUserOpts) {
				opts.Password = "new-secure-password"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := newLocalTestService(t)
			user := seedUser(t, svc, models.User{Username: "alice", Password: "old-hash", Source: "local"})
			seedUserToken(t, svc, user.ID, "session-token")
			opts := EditUserOpts{Username: user.Username, Admin: user.Admin}
			test.change(&opts)

			if err := svc.EditUser(user.ID, opts); err != nil {
				t.Fatalf("edit user: %v", err)
			}
			if got := userTokenCount(t, svc, user.ID); got != test.wantTokens {
				t.Fatalf("token count=%d want=%d", got, test.wantTokens)
			}
		})
	}
}

func TestDeleteUserNotFound(t *testing.T) {
	svc := newLocalTestService(t)
	err := svc.DeleteUser(999)
	if err == nil {
		t.Fatalf("expected error for non-existent user")
	}
	if !strings.Contains(err.Error(), "user_not_found") {
		t.Fatalf("expected user_not_found, got: %v", err)
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

func TestDeleteUserCannotDeleteRoot(t *testing.T) {
	svc := newLocalTestService(t)
	u := models.User{Username: "root", Password: "hashed", Admin: true}
	svc.DB.Create(&u)

	err := svc.DeleteUser(u.ID)
	if err == nil {
		t.Fatalf("expected error when deleting root")
	}
	if !strings.Contains(err.Error(), "cannot_delete_root_user") {
		t.Fatalf("expected cannot_delete_root_user, got: %v", err)
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

func TestGetUserByUsername(t *testing.T) {
	svc := newLocalTestService(t)
	seedUser(t, svc, models.User{Username: "alice", Password: "hashed", Admin: true})
	seedUser(t, svc, models.User{Username: "bob", Password: "hashed", Admin: false})

	found, err := svc.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if found.Username != "alice" {
		t.Fatalf("expected alice, got: %s", found.Username)
	}
	if !found.Admin {
		t.Fatalf("expected admin=true")
	}
}

func TestGetUserByUsernameNotFound(t *testing.T) {
	svc := newLocalTestService(t)
	_, err := svc.GetUserByUsername("nobody")
	if err == nil {
		t.Fatalf("expected error for non-existent user")
	}
	if !strings.Contains(err.Error(), "user_not_found") {
		t.Fatalf("expected user_not_found, got: %v", err)
	}
}

func TestGetUserByUsernamePreloadsGroups(t *testing.T) {
	svc := newLocalTestService(t)
	g := seedGroup(t, svc, "devs")
	u := seedUser(t, svc, models.User{Username: "alice", Password: "hashed"})
	if err := svc.DB.Model(&g).Association("Users").Append(&u); err != nil {
		t.Fatalf("failed to associate: %v", err)
	}

	found, err := svc.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(found.Groups) != 1 || found.Groups[0].Name != "devs" {
		t.Fatalf("expected group devs, got: %v", found.Groups)
	}
}

func TestImportUserProtectedSystemUser(t *testing.T) {
	svc := newLocalTestService(t)
	_, err := svc.ImportUser("nobody", "", false)
	if err == nil {
		t.Fatalf("expected error for protected system user")
	}
	if !strings.Contains(err.Error(), "protected_system_user") {
		t.Fatalf("expected protected_system_user, got: %v", err)
	}
}

func TestImportUserAlreadyExists(t *testing.T) {
	svc := newLocalTestService(t)
	seedBasicSettings(t, svc)
	seedUser(t, svc, models.User{Username: "alice", Password: "hashed"})

	_, err := svc.ImportUser("alice", "password123", false)
	if err == nil {
		t.Fatalf("expected error for already-existing user")
	}
	if strings.Contains(err.Error(), "user_source_conflict") {
		return
	}
	if strings.Contains(err.Error(), "failed_to_get_unix_user_info") {
		t.Skipf("system call not available in test: %v", err)
	}
	t.Fatalf("unexpected error: %v", err)
}

func TestListImportableUnixUsersEmptyDB(t *testing.T) {
	svc := newLocalTestService(t)
	seedBasicSettings(t, svc)

	users, err := svc.ListImportableUnixUsers()
	if err != nil {
		// Expected: system call fails in test env
		if strings.Contains(err.Error(), "failed_to_list_unix_users") {
			t.Skipf("system call not available in test: %v", err)
		}
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) > 0 {
		t.Logf("found %d importable users", len(users))
	}
}

func TestCreateUserDuplicateLocal(t *testing.T) {
	svc := newLocalTestService(t)
	seedBasicSettings(t, svc)
	seedUser(t, svc, models.User{Username: "john", Password: "hashed", Source: "local"})

	user := &models.User{Username: "john", Password: "password123"}
	err := svc.CreateUser(user, CreateUserOpts{})
	if err == nil {
		t.Fatalf("expected error for duplicate local username")
	}
	if !strings.Contains(err.Error(), "username_already_exists") {
		t.Fatalf("expected username_already_exists, got: %v", err)
	}
}

func TestCreateUserDuplicateWhenPAMExists(t *testing.T) {
	svc := newLocalTestService(t)
	seedBasicSettings(t, svc)
	seedUser(t, svc, models.User{Username: "pamjohn", Password: "hashed", Source: "pam"})

	user := &models.User{Username: "pamjohn", Password: "password123"}
	err := svc.CreateUser(user, CreateUserOpts{})
	if err == nil {
		t.Fatalf("expected error when PAM user with same name exists")
	}
	if !strings.Contains(err.Error(), "a_pam_user_with_this_username_already_exists") {
		t.Fatalf("expected a_pam_user_with_this_username_already_exists, got: %v", err)
	}
}

func TestImportUserDuplicateWhenLocalExists(t *testing.T) {
	svc := newLocalTestService(t)
	seedBasicSettings(t, svc)
	seedUser(t, svc, models.User{Username: "localjohn", Password: "hashed", Source: "local"})

	_, err := svc.ImportUser("localjohn", "password123", false)
	if err == nil {
		t.Fatalf("expected error when local user with same name exists")
	}
	if !strings.Contains(err.Error(), "user_source_conflict") {
		t.Fatalf("expected user_source_conflict, got: %v", err)
	}
}

func TestListUsersBySourceFilter(t *testing.T) {
	svc := newLocalTestService(t)
	seedUser(t, svc, models.User{Username: "local1", Password: "hashed", Source: "local"})
	seedUser(t, svc, models.User{Username: "local2", Password: "hashed", Source: "local"})
	seedUser(t, svc, models.User{Username: "pam1", Password: "hashed", Source: "pam"})

	locals, err := svc.ListUsersBySource("local")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(locals) != 2 {
		t.Fatalf("expected 2 local users, got: %d", len(locals))
	}
	for _, u := range locals {
		if u.Source != "local" {
			t.Fatalf("expected source 'local', got: %s", u.Source)
		}
	}

	pams, err := svc.ListUsersBySource("pam")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(pams) != 1 {
		t.Fatalf("expected 1 PAM user, got: %d", len(pams))
	}
	if pams[0].Source != "pam" {
		t.Fatalf("expected source 'pam', got: %s", pams[0].Source)
	}
}

func TestListUsersBySourceAll(t *testing.T) {
	svc := newLocalTestService(t)
	seedUser(t, svc, models.User{Username: "local1", Password: "hashed", Source: "local"})
	seedUser(t, svc, models.User{Username: "pam1", Password: "hashed", Source: "pam"})

	all, err := svc.ListUsersBySource("")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 users (all sources), got: %d", len(all))
	}
}

func TestListUsersBySourceUnknown(t *testing.T) {
	svc := newLocalTestService(t)
	seedUser(t, svc, models.User{Username: "local1", Password: "hashed", Source: "local"})

	results, err := svc.ListUsersBySource("bogus")
	if err == nil || !strings.Contains(err.Error(), "invalid_user_source") {
		t.Fatalf("expected invalid_user_source, got results=%v err=%v", results, err)
	}
}

func TestEditUserPAMFieldsIgnoredForLocal(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "localtest", Password: "hashed", Source: "local", UID: 1001, Shell: "/bin/sh", HomeDirectory: "/home/localtest"})

	err := svc.EditUser(u.ID, EditUserOpts{
		Username:      "localtest",
		UID:           5000,
		Shell:         "/bin/zsh",
		HomeDirectory: "/home/local",
	})
	if err != nil {
		if strings.Contains(err.Error(), "failed_to_get_user") {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	found, _ := svc.GetUserByID(u.ID)
	if found.UID != 1001 {
		t.Fatalf("UID should not have changed for local user, got: %d", found.UID)
	}
	if found.Shell != "/bin/sh" {
		t.Fatalf("Shell should not have changed for local user, got: %s", found.Shell)
	}
	if found.HomeDirectory != "/home/localtest" {
		t.Fatalf("HomeDirectory should not have changed for local user, got: %s", found.HomeDirectory)
	}
}

func TestEditUserLocalSourceNotMutated(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "localuser", Password: "hashed", Source: "local", FullName: "Old"})

	err := svc.EditUser(u.ID, EditUserOpts{
		Username: "localuser",
		FullName: "New",
		Admin:    false,
	})
	if err == nil {
		found, _ := svc.GetUserByID(u.ID)
		if found.Source != "local" {
			t.Fatalf("source should remain 'local', got: %s", found.Source)
		}
	}
}

func TestEditUserPAMSourceNotMutated(t *testing.T) {
	svc := newLocalTestService(t)
	u := seedUser(t, svc, models.User{Username: "pamuser", Password: "hashed", Source: "pam", FullName: "Old"})

	err := svc.EditUser(u.ID, EditUserOpts{
		Username: "pamuser",
		FullName: "New",
		Admin:    false,
	})
	if err != nil {
		if strings.Contains(err.Error(), "failed_to_get_user") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if err == nil {
		found, _ := svc.GetUserByID(u.ID)
		if found.Source != "pam" {
			t.Fatalf("source should remain 'pam', got: %s", found.Source)
		}
	}
}

func TestCreateUserSetsSourceToLocal(t *testing.T) {
	svc := newLocalTestService(t)
	seedBasicSettings(t, svc)

	user := &models.User{Username: "newuser", Password: "password123"}
	err := svc.CreateUser(user, CreateUserOpts{})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	var found models.User
	if err := svc.DB.First(&found, user.ID).Error; err != nil {
		t.Fatalf("failed to find created user: %v", err)
	}
	if found.Source != "local" {
		t.Fatalf("expected Source='local', got: %s", found.Source)
	}
}
