// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package auth

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/pkg/system"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func stubPAMIntegrations(t *testing.T) {
	t.Helper()
	originalSambaExists := sambaUserExistsFn
	originalSambaCreate := createSambaUserFn
	originalSambaEdit := editSambaUserFn
	originalSambaDelete := deleteSambaUserFn
	originalSambaAvailable := sambaToolsAvailableFn
	originalDoasAvailable := doasAvailableFn
	originalDoasAdd := addDoasPermFn
	originalDoasRemove := removeDoasPermFn
	t.Cleanup(func() {
		sambaUserExistsFn = originalSambaExists
		createSambaUserFn = originalSambaCreate
		editSambaUserFn = originalSambaEdit
		deleteSambaUserFn = originalSambaDelete
		sambaToolsAvailableFn = originalSambaAvailable
		doasAvailableFn = originalDoasAvailable
		addDoasPermFn = originalDoasAdd
		removeDoasPermFn = originalDoasRemove
	})

	sambaUserExistsFn = func(string) (bool, error) { return false, nil }
	createSambaUserFn = func(string, string) error { return nil }
	editSambaUserFn = func(string, string) error { return nil }
	deleteSambaUserFn = func(string) error { return nil }
	sambaToolsAvailableFn = func() bool { return false }
	doasAvailableFn = func() bool { return false }
	addDoasPermFn = func(string) error { return nil }
	removeDoasPermFn = func(string) error { return nil }
}

func seedSylveGroup(t *testing.T, service *Service) models.Group {
	t.Helper()
	return seedGroup(t, service, "sylve_g")
}

func TestCreatePamUserSynchronizesUnixSylveAndSambaPasswords(t *testing.T) {
	service := newLocalTestService(t)
	stubPAMIntegrations(t)
	sylveGroup := seedSylveGroup(t, service)

	unixUserCreated := false
	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		switch command {
		case "/usr/bin/id":
			if unixUserCreated {
				return "uid=1001(alice) gid=1001(sylve_g)", nil
			}
			return "id: alice: no such user", errors.New("exit status 1")
		case "/usr/bin/getent":
			return "sylve_g:*:1001:", nil
		case "/usr/sbin/pw":
			if len(args) >= 2 && args[0] == "usershow" && args[1] == "-u" {
				return "no such user", errors.New("exit status 67")
			}
			if len(args) >= 2 && args[0] == "user" && args[1] == "add" {
				unixUserCreated = true
				return "", nil
			}
		}
		return "", nil
	}))

	var unixPassword string
	t.Cleanup(system.SetRunCommandWithInput(func(command, input string, args ...string) (string, error) {
		unixPassword = strings.TrimSuffix(input, "\n")
		return "", nil
	}))

	var sambaPassword string
	sambaToolsAvailableFn = func() bool { return true }
	sambaUserExistsFn = func(string) (bool, error) { return false, nil }
	createSambaUserFn = func(_ string, password string) error {
		sambaPassword = password
		return nil
	}

	const password = "correct-horse-battery"
	user := &models.User{
		Username:      "alice",
		Password:      password,
		UID:           1001,
		Shell:         "/bin/sh",
		HomeDirectory: "/nonexistent",
		HomeDirPerms:  0o755,
	}
	if err := service.CreatePamUser(user, CreateUserOpts{CreateSamba: true}); err != nil {
		t.Fatalf("create PAM user: %v", err)
	}
	if unixPassword != password || sambaPassword != password {
		t.Fatalf("credential integrations did not receive the submitted plaintext")
	}
	if user.Password == password || !utils.CheckPasswordHash(password, user.Password) {
		t.Fatalf("Sylve credential was not independently hashed")
	}
	if user.Source != "pam" || user.ID == 0 {
		t.Fatalf("unexpected managed user identity: %+v", user)
	}
	if len(user.Groups) != 1 || user.Groups[0].ID != sylveGroup.ID {
		t.Fatalf("unexpected managed groups: %+v", user.Groups)
	}
}

func TestEditPamUserPasswordAndExplicitSambaIntent(t *testing.T) {
	service := newLocalTestService(t)
	stubPAMIntegrations(t)
	sylveGroup := seedSylveGroup(t, service)
	user := seedUser(t, service, models.User{
		Username:      "alice",
		Password:      "old-hash",
		UID:           1001,
		Shell:         "/bin/sh",
		HomeDirectory: "/nonexistent",
		HomeDirPerms:  0o755,
		Source:        "pam",
	})
	if err := service.DB.Model(&sylveGroup).Association("Users").Append(&user); err != nil {
		t.Fatalf("associate sylve group: %v", err)
	}
	seedUserToken(t, service, user.ID, "password-session")

	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		switch command {
		case "/usr/bin/id":
			return "uid=1001(alice) gid=1001(sylve_g)", nil
		case "/usr/bin/getent":
			return "sylve_g:*:1001:", nil
		}
		return "", nil
	}))

	unixPasswordUpdates := 0
	var unixPassword string
	t.Cleanup(system.SetRunCommandWithInput(func(command, input string, args ...string) (string, error) {
		unixPasswordUpdates++
		unixPassword = strings.TrimSuffix(input, "\n")
		return "", nil
	}))

	var sambaPassword string
	sambaToolsAvailableFn = func() bool { return true }
	editSambaUserFn = func(_ string, password string) error {
		sambaPassword = password
		return nil
	}

	const password = "new-secure-password"
	fullOptions := EditUserOpts{
		Username:      "alice",
		Password:      password,
		UID:           1001,
		Shell:         "/bin/sh",
		HomeDirectory: "/nonexistent",
		HomeDirPerms:  0o755,
		SambaAction:   SambaActionUpsert,
	}
	if err := service.EditUser(user.ID, fullOptions); err != nil {
		t.Fatalf("edit PAM user: %v", err)
	}
	if unixPassword != password || sambaPassword != password {
		t.Fatalf("edit did not synchronize Unix and explicit Samba credentials")
	}

	reloaded, err := service.GetUserByID(user.ID)
	if err != nil {
		t.Fatalf("reload PAM user: %v", err)
	}
	if !utils.CheckPasswordHash(password, reloaded.Password) {
		t.Fatalf("updated Sylve credential does not match submitted password")
	}
	if got := userTokenCount(t, service, user.ID); got != 0 {
		t.Fatalf("password edit left %d sessions", got)
	}
	updatedHash := reloaded.Password

	seedUserToken(t, service, user.ID, "cosmetic-session")
	fullOptions.Password = ""
	fullOptions.SambaAction = SambaActionKeep
	if err := service.EditUser(user.ID, fullOptions); err != nil {
		t.Fatalf("blank-password edit: %v", err)
	}
	reloaded, err = service.GetUserByID(user.ID)
	if err != nil {
		t.Fatalf("reload PAM user: %v", err)
	}
	if unixPasswordUpdates != 1 || reloaded.Password != updatedHash {
		t.Fatalf("blank edit changed a Unix or Sylve credential")
	}

	removed := false
	sambaUserExistsFn = func(string) (bool, error) { return true, nil }
	deleteSambaUserFn = func(string) error {
		removed = true
		return nil
	}
	fullOptions.SambaAction = SambaActionRemove
	if err := service.EditUser(user.ID, fullOptions); err != nil {
		t.Fatalf("remove Samba user: %v", err)
	}
	if !removed {
		t.Fatal("explicit Samba remove intent was not applied")
	}
	if got := userTokenCount(t, service, user.ID); got != 1 {
		t.Fatalf("integration-only edits changed session count: %d", got)
	}

	fullOptions.SambaAction = SambaActionKeep
	fullOptions.Locked = true
	if err := service.EditUser(user.ID, fullOptions); err != nil {
		t.Fatalf("lock PAM user: %v", err)
	}
	if got := userTokenCount(t, service, user.ID); got != 0 {
		t.Fatalf("lock edit left %d sessions", got)
	}

	seedUserToken(t, service, user.ID, "disable-session")
	fullOptions.DisablePassword = true
	if err := service.EditUser(user.ID, fullOptions); err != nil {
		t.Fatalf("disable PAM password: %v", err)
	}
	if got := userTokenCount(t, service, user.ID); got != 0 {
		t.Fatalf("password-disable edit left %d sessions", got)
	}

	seedUserToken(t, service, user.ID, "admin-session")
	fullOptions.Admin = true
	if err := service.EditUser(user.ID, fullOptions); err != nil {
		t.Fatalf("promote PAM user: %v", err)
	}
	if got := userTokenCount(t, service, user.ID); got != 0 {
		t.Fatalf("administrator edit left %d sessions", got)
	}
}

func TestEditPamUserRequiresPasswordToReenableAuthentication(t *testing.T) {
	service := newLocalTestService(t)
	stubPAMIntegrations(t)
	user := seedUser(t, service, models.User{
		Username:        "alice",
		Password:        "old-hash",
		UID:             1001,
		Shell:           "/bin/sh",
		HomeDirectory:   "/nonexistent",
		HomeDirPerms:    0o755,
		DisablePassword: true,
		Source:          "pam",
	})
	seedUserToken(t, service, user.ID, "validation-session")

	err := service.EditUser(user.ID, EditUserOpts{
		Username:        user.Username,
		UID:             user.UID,
		Shell:           user.Shell,
		HomeDirectory:   user.HomeDirectory,
		HomeDirPerms:    user.HomeDirPerms,
		DisablePassword: false,
	})
	if err == nil || !strings.Contains(err.Error(), "password_required_to_enable") {
		t.Fatalf("expected password-required error, got: %v", err)
	}
	if got := userTokenCount(t, service, user.ID); got != 1 {
		t.Fatalf("failed validation changed session count: %d", got)
	}
}

func TestEditPamUserPrimaryGroupChangeDoesNotRemoveOldPrimaryAsAuxiliary(t *testing.T) {
	service := newLocalTestService(t)
	stubPAMIntegrations(t)
	oldPrimary := seedGroup(t, service, "old-primary")
	newPrimary := seedGroup(t, service, "new-primary")
	sylveGroup := seedSylveGroup(t, service)
	user := seedUser(t, service, models.User{
		Username:       "alice",
		Password:       "old-hash",
		UID:            1001,
		Shell:          "/bin/sh",
		HomeDirectory:  "/nonexistent",
		HomeDirPerms:   0o755,
		PrimaryGroupID: &oldPrimary.ID,
		Source:         "pam",
	})
	if err := service.DB.Model(&user).Association("Groups").Replace(&[]models.Group{oldPrimary, sylveGroup}); err != nil {
		t.Fatalf("associate groups: %v", err)
	}

	var removedGroups []string
	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		switch command {
		case "/usr/bin/id":
			if len(args) > 0 && args[0] == "-nG" {
				return "old-primary sylve_g", nil
			}
			return "uid=1001(alice) gid=1001(old-primary)", nil
		case "/usr/bin/getent":
			group := args[len(args)-1]
			return fmt.Sprintf("%s:*:1001:", group), nil
		case "/usr/sbin/pw":
			if len(args) >= 4 && args[0] == "groupmod" && args[2] == "-d" {
				removedGroups = append(removedGroups, args[1])
			}
		}
		return "", nil
	}))

	err := service.EditUser(user.ID, EditUserOpts{
		Username:       user.Username,
		UID:            user.UID,
		Shell:          user.Shell,
		HomeDirectory:  user.HomeDirectory,
		HomeDirPerms:   user.HomeDirPerms,
		PrimaryGroupID: &newPrimary.ID,
	})
	if err != nil {
		t.Fatalf("change primary group: %v", err)
	}
	for _, group := range removedGroups {
		if group == oldPrimary.Name {
			t.Fatalf("old primary group was treated as auxiliary membership: %v", removedGroups)
		}
	}
}

func TestEditPamUserHomeMoveReappliesRequestedPermissions(t *testing.T) {
	service := newLocalTestService(t)
	stubPAMIntegrations(t)
	sylveGroup := seedSylveGroup(t, service)
	user := seedUser(t, service, models.User{
		Username:      "alice",
		Password:      "old-hash",
		UID:           1001,
		Shell:         "/bin/sh",
		HomeDirectory: "/nonexistent",
		HomeDirPerms:  0o755,
		Source:        "pam",
	})
	if err := service.DB.Model(&user).Association("Groups").Replace(&[]models.Group{sylveGroup}); err != nil {
		t.Fatalf("associate sylve group: %v", err)
	}

	home := t.TempDir()
	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		switch command {
		case "/usr/bin/id":
			return "uid=1001(alice) gid=1001(sylve_g)", nil
		case "/usr/bin/getent":
			return "sylve_g:*:1001:", nil
		}
		return "", nil
	}))

	err := service.EditUser(user.ID, EditUserOpts{
		Username:      user.Username,
		UID:           user.UID,
		Shell:         user.Shell,
		HomeDirectory: home,
		HomeDirPerms:  user.HomeDirPerms,
	})
	if err != nil {
		t.Fatalf("move PAM home: %v", err)
	}
	info, err := os.Stat(home)
	if err != nil {
		t.Fatalf("stat moved home: %v", err)
	}
	if got := info.Mode().Perm(); got != os.FileMode(0o755) {
		t.Fatalf("expected moved home mode 0755, got %04o", got)
	}
}

func TestImportPamUserMirrorsIdentityWithoutUnixMutation(t *testing.T) {
	service := newLocalTestService(t)
	stubPAMIntegrations(t)

	mutatingCommand := false
	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		switch command {
		case "/usr/bin/id":
			if len(args) == 2 && args[0] == "-Gn" {
				return args[1] + " staff", nil
			}
			return "uid=1001(" + args[0] + ") gid=1001(" + args[0] + ")", nil
		case "/usr/bin/getent":
			return "alice:*:1001:", nil
		case "/usr/sbin/pw":
			if len(args) >= 3 && args[0] == "usershow" && args[1] == "-n" {
				username := args[2]
				return fmt.Sprintf("%s:*:1001:1001::0:0:Alice Example:/home/%s:/bin/sh", username, username), nil
			}
			mutatingCommand = true
		}
		return "", nil
	}))
	passwordInputUsed := false
	t.Cleanup(system.SetRunCommandWithInput(func(command, input string, args ...string) (string, error) {
		passwordInputUsed = true
		return "", nil
	}))

	const sylvePassword = "sylve-only-password"
	user, err := service.ImportUser("alice", sylvePassword, true)
	if err != nil {
		t.Fatalf("import PAM user: %v", err)
	}
	if mutatingCommand || passwordInputUsed {
		t.Fatal("import mutated the existing Unix identity or password")
	}
	if user.Source != "pam" || !user.Admin || !utils.CheckPasswordHash(sylvePassword, user.Password) {
		t.Fatalf("unexpected imported identity: %+v", user)
	}
	groupNames := make(map[string]bool, len(user.Groups))
	for _, group := range user.Groups {
		groupNames[group.Name] = true
	}
	if !groupNames["alice"] || !groupNames["staff"] || groupNames["sylve_g"] {
		t.Fatalf("import did not mirror Unix groups exactly: %+v", user.Groups)
	}
}

func TestImportPamUserRejectsUIDAlreadyManagedBySylve(t *testing.T) {
	service := newLocalTestService(t)
	stubPAMIntegrations(t)
	seedUser(t, service, models.User{Username: "managed", UID: 1001, Source: "pam"})

	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		switch command {
		case "/usr/bin/id":
			return "uid=1001(alias) gid=1001(alias)", nil
		case "/usr/sbin/pw":
			return "alias:*:1001:1001::0:0:Alias:/home/alias:/bin/sh", nil
		}
		return "", nil
	}))

	_, err := service.ImportUser("alias", "", false)
	if err == nil || !strings.Contains(err.Error(), "uid_already_in_use") {
		t.Fatalf("expected managed UID conflict, got: %v", err)
	}
}

func TestListImportablePamUsersFiltersManagedUIDAliases(t *testing.T) {
	service := newLocalTestService(t)
	stubPAMIntegrations(t)
	seedUser(t, service, models.User{Username: "managed", UID: 1001, Source: "pam"})

	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		if command == "/usr/sbin/pw" {
			return strings.Join([]string{
				"alias:*:1001:1001::0:0:Alias:/home/alias:/bin/sh",
				"fresh:*:1002:1002::0:0:Fresh:/home/fresh:/bin/sh",
			}, "\n"), nil
		}
		return "", nil
	}))

	users, err := service.ListImportableUnixUsers()
	if err != nil {
		t.Fatalf("list importable users: %v", err)
	}
	if len(users) != 1 || users[0].Username != "fresh" {
		t.Fatalf("expected only fresh UID candidate, got: %+v", users)
	}
}

func TestDeletePamUserCleansDatabaseWhenUnixUserIsAlreadyMissing(t *testing.T) {
	service := newLocalTestService(t)
	stubPAMIntegrations(t)
	group := seedSylveGroup(t, service)
	user := seedUser(t, service, models.User{
		Username:      "alice",
		Password:      "hashed",
		UID:           1001,
		HomeDirectory: "/home/alice",
		Source:        "pam",
	})
	if err := service.DB.Model(&group).Association("Users").Append(&user); err != nil {
		t.Fatalf("associate group: %v", err)
	}
	if err := service.DB.Create(&models.Token{UserID: user.ID, Token: "token", AuthType: "local"}).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	doasCleaned := false
	removeDoasPermFn = func(username string) error {
		doasCleaned = username == "alice"
		return nil
	}
	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		return "id: alice: no such user", errors.New("exit status 1")
	}))

	if err := service.DeleteUser(user.ID); err != nil {
		t.Fatalf("delete managed PAM user: %v", err)
	}
	if !doasCleaned {
		t.Fatal("stale doas state was not cleaned")
	}
	var userCount, tokenCount, membershipCount int64
	service.DB.Model(&models.User{}).Where("id = ?", user.ID).Count(&userCount)
	service.DB.Model(&models.Token{}).Where("user_id = ?", user.ID).Count(&tokenCount)
	service.DB.Table("user_groups").Where("user_id = ?", user.ID).Count(&membershipCount)
	if userCount != 0 || tokenCount != 0 || membershipCount != 0 {
		t.Fatalf("database state remained after deletion: users=%d tokens=%d memberships=%d", userCount, tokenCount, membershipCount)
	}
}

func TestDeletePamUserRejectsUnsafeCurrentUnixHome(t *testing.T) {
	service := newLocalTestService(t)
	stubPAMIntegrations(t)
	user := seedUser(t, service, models.User{
		Username:      "alice",
		Password:      "hashed",
		UID:           1001,
		HomeDirectory: "/home/alice",
		Source:        "pam",
	})
	seedUserToken(t, service, user.ID, "unsafe-preflight-session")
	deletedUnixUser := false
	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		if command == "/usr/bin/id" {
			return "uid=1001(alice) gid=1001(alice)", nil
		}
		if command == "/usr/sbin/pw" && len(args) >= 3 && args[0] == "usershow" {
			return "alice:*:1001:1001::0:0:Alice:/etc:/bin/sh", nil
		}
		if command == "/usr/sbin/pw" && len(args) > 0 && args[0] == "userdel" {
			deletedUnixUser = true
		}
		return "", nil
	}))

	err := service.DeleteUser(user.ID)
	if err == nil || !strings.Contains(err.Error(), "unsafe_home_directory") {
		t.Fatalf("expected unsafe home rejection, got: %v", err)
	}
	if deletedUnixUser {
		t.Fatal("unsafe Unix account was deleted")
	}
	if got := userTokenCount(t, service, user.ID); got != 1 {
		t.Fatalf("failed preflight changed session count: %d", got)
	}
}

func TestDeletePamUserKeepsRevocationAfterLaterFailure(t *testing.T) {
	service := newLocalTestService(t)
	stubPAMIntegrations(t)
	user := seedUser(t, service, models.User{
		Username:      "alice",
		Password:      "hashed",
		UID:           1001,
		HomeDirectory: "/home/alice",
		Source:        "pam",
	})
	seedUserToken(t, service, user.ID, "delete-session")

	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		switch command {
		case "/usr/bin/id":
			return "uid=1001(alice) gid=1001(alice)", nil
		case "/usr/sbin/pw":
			if len(args) >= 3 && args[0] == "usershow" {
				return "alice:*:1001:1001::0:0:Alice:/home/alice:/bin/sh", nil
			}
		}
		return "", nil
	}))
	removeDoasPermFn = func(string) error { return errors.New("doas cleanup failed") }

	err := service.DeleteUser(user.ID)
	if err == nil || !strings.Contains(err.Error(), "doas_cleanup_failed") {
		t.Fatalf("expected later cleanup failure, got: %v", err)
	}
	if got := userTokenCount(t, service, user.ID); got != 0 {
		t.Fatalf("later failure restored %d sessions", got)
	}
}
