// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package auth

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/pkg/system"
)

func stubImportIdentity(t *testing.T, username string, uid int, local, enumerate bool) {
	t.Helper()
	info := fmt.Sprintf("%s:*:%d:%d::0:0:Directory User:/nonexistent:/bin/sh", username, uid, uid)
	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		switch command {
		case "/usr/bin/id":
			if len(args) == 2 && args[0] == "-Gn" {
				return username + " staff", nil
			}
			return fmt.Sprintf("uid=%d(%s) gid=%d(%s)", uid, username, uid, username), nil
		case "/usr/bin/getent":
			return fmt.Sprintf("%s:*:%d:", username, uid), nil
		case "/usr/sbin/pw":
			if len(args) == 5 && args[0] == "-V" && args[1] == "/etc" {
				if local || (args[2] == "groupshow" && args[4] == "staff") {
					return info, nil
				}
				if args[2] == "groupshow" {
					return fmt.Sprintf("pw: unknown group `%s'", args[4]), errors.New("exit status 65")
				}
				return fmt.Sprintf("pw: no such user `%s'", args[4]), errors.New("exit status 67")
			}
			if len(args) == 2 && args[0] == "usershow" && args[1] == "-a" {
				if enumerate {
					return info, nil
				}
				return "", nil
			}
			if len(args) == 3 && args[0] == "usershow" && args[1] == "-n" && args[2] == username {
				return info, nil
			}
		}
		t.Fatalf("unexpected Unix command: %s %v", command, args)
		return "", nil
	}))
	t.Cleanup(system.SetRunCommandWithInput(func(command, input string, args ...string) (string, error) {
		t.Fatal("import must not write a Unix password")
		return "", nil
	}))
}

func externalPAMEditOpts(user *models.User) EditUserOpts {
	opts := EditUserOpts{
		Username:        user.Username,
		FullName:        user.FullName,
		Email:           user.Email,
		Admin:           user.Admin,
		UID:             user.UID,
		Shell:           user.Shell,
		HomeDirectory:   user.HomeDirectory,
		HomeDirPerms:    user.HomeDirPerms,
		SSHPublicKey:    user.SSHPublicKey,
		DoasEnabled:     user.DoasEnabled,
		PrimaryGroupID:  user.PrimaryGroupID,
		Locked:          user.Locked,
		DisablePassword: user.DisablePassword,
	}
	for _, group := range user.Groups {
		if user.PrimaryGroupID == nil || group.ID != *user.PrimaryGroupID {
			opts.AuxGroupIDs = append(opts.AuxGroupIDs, group.ID)
		}
	}
	return opts
}

func forbidExternalPAMSideEffects(t *testing.T) {
	t.Helper()
	stubPAMIntegrations(t)
	t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
		t.Fatalf("external registration must not run Unix commands: %s %v", command, args)
		return "", nil
	}))
	t.Cleanup(system.SetRunCommandWithInput(func(command, input string, args ...string) (string, error) {
		t.Fatal("external registration must not write a Unix password")
		return "", nil
	}))
	removeDoasPermFn = func(string) error {
		t.Fatal("unexpected doas cleanup")
		return nil
	}
	sambaToolsAvailableFn = func() bool {
		t.Fatal("unexpected Samba integration")
		return false
	}
}

func TestValidateImportedPAMUID(t *testing.T) {
	for _, test := range []struct {
		uid  int
		code string
	}{
		{-1, "invalid_uid"}, {0, "protected_system_user"}, {999, "protected_system_user"},
		{1000, ""}, {65533, ""}, {65534, "protected_system_user"}, {65535, ""},
		{344200007, ""}, {4294967294, ""}, {4294967295, "invalid_uid"}, {4294967296, "invalid_uid"},
	} {
		t.Run(fmt.Sprint(test.uid), func(t *testing.T) {
			err := validateImportedPAMUID(test.uid)
			if test.code == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != test.code {
				t.Fatalf("error=%v want=%s", err, test.code)
			}
		})
	}
	if err := validateManagedPAMUID(344200007); err == nil {
		t.Fatal("managed account allocation must retain its UID range")
	}
}

func TestImportPamUserDeterminesOwnershipFromLocalDatabase(t *testing.T) {
	for _, uid := range []int{1001, 344200007} {
		for _, local := range []bool{false, true} {
			t.Run(fmt.Sprintf("uid=%d/local=%t", uid, local), func(t *testing.T) {
				service := newLocalTestService(t)
				stubImportIdentity(t, "mhahl-a", uid, local, false)
				users, err := service.ListImportableUnixUsers()
				if err != nil || len(users) != 0 {
					t.Fatalf("expected disabled enumeration: users=%v error=%v", users, err)
				}
				user, err := service.ImportUser("mhahl-a", "", true, false)
				if err != nil {
					t.Fatal(err)
				}
				if user.UID != uid || user.ExternallyManaged == local || user.Password != "" || user.Source != "pam" {
					t.Fatalf("unexpected imported identity: %+v", user)
				}
				if len(user.Groups) != 2 {
					t.Fatalf("expected distinct primary and supplementary groups, got %+v", user.Groups)
				}
				for _, group := range user.Groups {
					wantExternal := !local && group.Name == "mhahl-a"
					if group.ExternallyManaged != wantExternal {
						t.Fatalf("unexpected group ownership: %+v", group)
					}
				}
				reloaded, err := service.GetUserByID(user.ID)
				if err != nil || reloaded.ExternallyManaged != user.ExternallyManaged {
					t.Fatalf("ownership was not persisted: user=%+v error=%v", reloaded, err)
				}
			})
		}
	}
}

func TestListImportablePamUsersIncludesHighUIDsAndFiltersAliases(t *testing.T) {
	service := newLocalTestService(t)
	stubImportIdentity(t, "mhahl-a", 344200007, false, true)
	users, err := service.ListImportableUnixUsers()
	if err != nil || len(users) != 1 || users[0].UID != 344200007 || users[0].GID != 344200007 {
		t.Fatalf("missing high UID candidate: users=%v error=%v", users, err)
	}
	seedUser(t, service, models.User{Username: "alias", UID: 344200007, Source: "pam"})
	users, err = service.ListImportableUnixUsers()
	if err != nil || len(users) != 0 {
		t.Fatalf("managed UID alias was not filtered: users=%v error=%v", users, err)
	}
	if _, err := service.ImportUser("mhahl-a", "", true, false); err == nil || err.Error() != "uid_already_in_use" {
		t.Fatalf("expected high UID conflict, got %v", err)
	}
}

func TestImportPamUserMarksPreviouslyRecordedDirectoryGroupsExternal(t *testing.T) {
	service := newLocalTestService(t)
	group := seedGroup(t, service, "mhahl-a")
	stubImportIdentity(t, "mhahl-a", 344200007, false, false)
	if _, err := service.ImportUser("mhahl-a", "", true, false); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.First(&group, group.ID).Error; err != nil || !group.ExternallyManaged {
		t.Fatalf("directory group ownership was not updated: group=%+v error=%v", group, err)
	}
}

func TestEditImportedLocalPamUserKeepsHighUID(t *testing.T) {
	service := newLocalTestService(t)
	stubImportIdentity(t, "local-user", 344200007, true, false)
	user, err := service.ImportUser("local-user", "", true, false)
	if err != nil {
		t.Fatal(err)
	}
	opts := externalPAMEditOpts(user)
	opts.Email = "local@example.com"
	if err := service.EditUser(user.ID, opts); err != nil {
		t.Fatalf("metadata edit rejected the existing local UID: %v", err)
	}
	user, err = service.GetUserByID(user.ID)
	if err != nil || user.ExternallyManaged || user.UID != 344200007 || user.Email != opts.Email {
		t.Fatalf("local ownership or UID changed: user=%+v error=%v", user, err)
	}
}

func TestImportPamUserFailsClosedOnOwnershipLookupErrors(t *testing.T) {
	for _, operation := range []string{"usershow", "groupshow"} {
		t.Run(operation, func(t *testing.T) {
			service := newLocalTestService(t)
			t.Cleanup(system.SetRunCommand(func(command string, args ...string) (string, error) {
				switch command {
				case "/usr/bin/id":
					return "directory-user", nil
				case "/usr/bin/getent":
					return "directory-user:*:344200007:", nil
				case "/usr/sbin/pw":
					if len(args) == 5 && args[0] == "-V" && args[2] == operation {
						return "", errors.New("local database unavailable")
					}
					return "directory-user:*:344200007:344200007::0:0:Directory User:/nonexistent:/bin/sh", nil
				}
				t.Fatalf("unexpected Unix command: %s %v", command, args)
				return "", nil
			}))
			if _, err := service.ImportUser("directory-user", "", true, false); err == nil {
				t.Fatal("import ignored an ownership lookup failure")
			}
			for _, model := range []any{&models.User{}, &models.Group{}} {
				var count int64
				if err := service.DB.Model(model).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("failed import retained database state: count=%d error=%v", count, err)
				}
			}
		})
	}
}

func TestEditExternalPamUserOnlyUpdatesSylveState(t *testing.T) {
	service := newLocalTestService(t)
	stubImportIdentity(t, "mhahl-a", 344200007, false, false)
	user, err := service.ImportUser("mhahl-a", "", true, false)
	if err != nil {
		t.Fatal(err)
	}
	forbidExternalPAMSideEffects(t)
	seedUserToken(t, service, user.ID, "session")
	opts := externalPAMEditOpts(user)
	opts.Email = "admin@example.com"
	confirm := true
	opts.ConfirmGuestActions = &confirm
	if err := service.EditUser(user.ID, opts); err != nil {
		t.Fatal(err)
	}
	if userTokenCount(t, service, user.ID) != 1 {
		t.Fatal("cosmetic edit revoked sessions")
	}
	opts.Password = "sylve-only-password"
	if err := service.EditUser(user.ID, opts); err != nil {
		t.Fatal(err)
	}
	reloaded, err := service.GetUserByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !service.passwordHasher.Verify(opts.Password, reloaded.Password) || reloaded.Email != opts.Email ||
		!reloaded.ConfirmGuestActions || !reloaded.ExternallyManaged || userTokenCount(t, service, user.ID) != 0 {
		t.Fatalf("unexpected Sylve-only edit result: %+v", reloaded)
	}
	for _, change := range []func(*EditUserOpts){
		func(o *EditUserOpts) { o.Locked = true },
		func(o *EditUserOpts) { o.Admin = false },
		func(o *EditUserOpts) { o.DisablePassword = true },
	} {
		seedUserToken(t, service, user.ID, "next-session")
		opts = externalPAMEditOpts(reloaded)
		change(&opts)
		if err := service.EditUser(user.ID, opts); err != nil {
			t.Fatal(err)
		}
		if userTokenCount(t, service, user.ID) != 0 {
			t.Fatal("security edit did not revoke sessions")
		}
	}
}

func TestEditExternalPamUserRejectsUnixMutations(t *testing.T) {
	service := newLocalTestService(t)
	stubImportIdentity(t, "mhahl-a", 344200007, false, false)
	user, err := service.ImportUser("mhahl-a", "", true, false)
	if err != nil {
		t.Fatal(err)
	}
	forbidExternalPAMSideEffects(t)
	seedUserToken(t, service, user.ID, "session")
	for _, test := range []struct {
		name   string
		change func(*EditUserOpts)
	}{
		{"UID", func(o *EditUserOpts) { o.UID = 1001 }},
		{"shell", func(o *EditUserOpts) { o.Shell = "/bin/csh" }},
		{"home", func(o *EditUserOpts) { o.HomeDirectory = "/home/changed" }},
		{"permissions", func(o *EditUserOpts) { o.HomeDirPerms = 0o700 }},
		{"SSH key", func(o *EditUserOpts) { o.SSHPublicKey = "ssh-ed25519 example" }},
		{"doas", func(o *EditUserOpts) { o.DoasEnabled = true }},
		{"new group", func(o *EditUserOpts) { o.NewPrimaryGroup = true }},
		{"primary group", func(o *EditUserOpts) { o.PrimaryGroupID = nil }},
		{"auxiliary groups", func(o *EditUserOpts) { o.AuxGroupIDs = nil }},
		{"Samba", func(o *EditUserOpts) { o.SambaAction = SambaActionUpsert }},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts := externalPAMEditOpts(user)
			test.change(&opts)
			if err := service.EditUser(user.ID, opts); err == nil || err.Error() != "external_user_read_only" {
				t.Fatalf("expected read-only rejection, got %v", err)
			}
			if userTokenCount(t, service, user.ID) != 1 {
				t.Fatal("failed validation changed sessions")
			}
		})
	}
}

func TestDeleteExternalPamUserOnlyUnregisters(t *testing.T) {
	service := newLocalTestService(t)
	stubImportIdentity(t, "mhahl-a", 344200007, false, false)
	user, err := service.ImportUser("mhahl-a", "", true, false)
	if err != nil {
		t.Fatal(err)
	}
	forbidExternalPAMSideEffects(t)
	seedUserToken(t, service, user.ID, "session")
	if err := service.DeleteUser(user.ID, true); err == nil || !strings.Contains(err.Error(), "external_user_read_only") {
		t.Fatalf("expected home-removal rejection, got %v", err)
	}
	if userTokenCount(t, service, user.ID) != 1 {
		t.Fatal("failed home-removal request revoked sessions")
	}
	if err := service.DeleteUser(user.ID, false); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"users", "tokens", "user_groups"} {
		var count int64
		if err := service.DB.Table(table).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%s retained registration state: count=%d error=%v", table, count, err)
		}
	}
	var count int64
	service.DB.Model(&models.Group{}).Count(&count)
	if count != 2 {
		t.Fatal("unregistering removed group records")
	}
}
