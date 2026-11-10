// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"slices"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func sharedUser(t *testing.T, svc *Service, username string) *clusterModels.SylveUser {
	t.Helper()
	var user clusterModels.SylveUser
	if err := svc.DB.Where("username = ?", username).Limit(1).Find(&user).Error; err != nil {
		t.Fatal(err)
	}
	if user.ID == 0 {
		return nil
	}
	return &user
}

func TestLocalUsersAreSharedWithTheCluster(t *testing.T) {
	svc := newLocalTestService(t)
	alice := models.User{Username: "alice", Password: "password-1", Email: "a@example.com"}
	if err := svc.CreateUser(&alice, CreateUserOpts{}); err != nil {
		t.Fatal(err)
	}
	if shared := sharedUser(t, svc, "alice"); shared == nil || shared.Password != "test-hash:password-1" || shared.Email != "a@example.com" {
		t.Fatalf("cluster user = %+v", shared)
	}
	if alice.ID == 0 || !alice.Shared || alice.Source != "local" {
		t.Fatalf("created = %+v, want this node's shared copy", alice)
	}

	if err := svc.DB.Create(&clusterModels.SylveUser{Username: "bob", Password: "elsewhere"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.CreateUser(&models.User{Username: "bob", Password: "password-1"}, CreateUserOpts{}); err == nil {
		t.Fatal("created a user whose name the cluster already has")
	}

	seedUserToken(t, svc, alice.ID, "session")
	if err := svc.EditUser(alice.ID, EditUserOpts{Username: "alice", FullName: "Alice"}); err != nil {
		t.Fatal(err)
	}
	if shared := sharedUser(t, svc, "alice"); shared.FullName != "Alice" || userTokenCount(t, svc, alice.ID) != 1 {
		t.Fatalf("after a profile edit: %+v, want it shared and the session kept", shared)
	}
	if err := svc.EditUser(alice.ID, EditUserOpts{Username: "alice", Password: "password-2"}); err != nil {
		t.Fatal(err)
	}
	if shared := sharedUser(t, svc, "alice"); shared.Password != "test-hash:password-2" || userTokenCount(t, svc, alice.ID) != 0 {
		t.Fatalf("after a password change: %+v, want it shared and the session ended", shared)
	}

	if err := svc.SetGrant(t.Context(), clusterModels.ACLGrant{Path: "/vm/100", SubjectType: clusterModels.ACLSubjectUser, Subject: "alice@sylve", Role: "GuestUser"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.EditUser(alice.ID, EditUserOpts{Username: "bob"}); err == nil {
		t.Fatal("renamed onto a name the cluster already has")
	}
	if err := svc.EditUser(alice.ID, EditUserOpts{Username: "alicia"}); err != nil {
		t.Fatal(err)
	}
	if sharedUser(t, svc, "alice") != nil || sharedUser(t, svc, "alicia") == nil || !slices.Equal(grantSubjects(t, svc), []string{"alicia@sylve"}) {
		t.Fatalf("after rename: grants %v", grantSubjects(t, svc))
	}
	if row, err := svc.GetUserByID(alice.ID); err != nil || row.Username != "alicia" {
		t.Fatalf("this node's copy = %+v, %v; want it renamed in place", row, err)
	}

	if err := svc.DeleteUser(alice.ID, false); err != nil {
		t.Fatal(err)
	}
	if sharedUser(t, svc, "alicia") != nil || len(grantSubjects(t, svc)) != 0 {
		t.Fatalf("after delete: grants %v", grantSubjects(t, svc))
	}
	if _, err := svc.GetUserByID(alice.ID); err == nil {
		t.Fatal("this node's copy survived the delete")
	}
}

func TestUnsharedAccountRestoresSharedUser(t *testing.T) {
	for _, action := range []string{"rename", "delete"} {
		t.Run(action, func(t *testing.T) {
			svc := newLocalTestService(t)
			shared := clusterModels.SylveUser{Username: "carol", Password: "elsewhere", Email: "shared@example.com", Locked: true}
			own := seedUser(t, svc, models.User{Username: "carol", Password: "here", Source: "local", TOTP: "local-secret"})
			for _, row := range []any{
				&shared, &clusterModels.ClusterOption{ID: 1, ClusterID: "cluster"},
				&models.WebAuthnCredential{UserID: own.ID, CredentialID: "local-key", Data: []byte("key")},
			} {
				if err := svc.DB.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			seedUserToken(t, svc, own.ID, "local-session")
			if err := svc.SetGrant(t.Context(), clusterModels.ACLGrant{Path: "/", SubjectType: clusterModels.ACLSubjectUser, Subject: "carol@sylve", Role: "Auditor"}); err != nil {
				t.Fatal(err)
			}
			mutate := func() error {
				if action == "rename" {
					return svc.EditUser(own.ID, EditUserOpts{Username: "carol-here"})
				}
				return svc.DeleteUser(own.ID, false)
			}
			// A failed restoration must also roll back the rename/deletion and credential cleanup.
			if err := svc.DB.Exec(`CREATE TRIGGER fail_shared_restore BEFORE INSERT ON users
				WHEN NEW.shared = 1 BEGIN SELECT RAISE(ABORT, 'restore_failed'); END`).Error; err != nil {
				t.Fatal(err)
			}
			if err := mutate(); err == nil {
				t.Fatal("mutation succeeded despite failed restoration")
			}
			if row, err := svc.GetUserByUsername("carol"); err != nil || row.ID != own.ID || row.Shared || userTokenCount(t, svc, own.ID) != 1 {
				t.Fatalf("local account after rollback = %+v, %v", row, err)
			}
			var keyCount int64
			if err := svc.DB.Model(&models.WebAuthnCredential{}).Where("user_id = ?", own.ID).Count(&keyCount).Error; err != nil || keyCount != 1 {
				t.Fatalf("local passkeys after rollback = %d, %v", keyCount, err)
			}
			if err := svc.DB.Exec("DROP TRIGGER fail_shared_restore").Error; err != nil {
				t.Fatal(err)
			}
			if err := mutate(); err != nil {
				t.Fatal(err)
			}
			restored, err := svc.GetUserByUsername("carol")
			if err != nil || restored.ID == own.ID || !restored.Shared || restored.Source != "local" ||
				restored.Password != shared.Password || restored.Email != shared.Email || !restored.Locked ||
				restored.SharedClusterID != "cluster" || restored.SharedUserID != shared.ID || restored.TOTP != "" {
				t.Fatalf("restored shared user = %+v, %v", restored, err)
			}
			if !slices.Equal(grantSubjects(t, svc), []string{"carol@sylve"}) {
				t.Fatalf("shared grants after %s = %v", action, grantSubjects(t, svc))
			}
			if userTokenCount(t, svc, own.ID) != 0 || userTokenCount(t, svc, restored.ID) != 0 {
				t.Fatal("duplicate's session survived")
			}
			var keys []models.WebAuthnCredential
			if err := svc.DB.Find(&keys).Error; err != nil {
				t.Fatal(err)
			}
			if action == "rename" {
				if len(keys) != 1 || keys[0].UserID != own.ID {
					t.Fatalf("renamed account's passkeys = %+v", keys)
				}
				if row, err := svc.GetUserByID(own.ID); err != nil || row.Username != "carol-here" || row.Password != own.Password || row.Shared {
					t.Fatalf("renamed local account = %+v, %v", row, err)
				}
			} else if len(keys) != 0 {
				t.Fatalf("deleted account's passkeys survived: %+v", keys)
			}
		})
	}
}

func TestShareLocalUsersSharesWhatTheClusterLacks(t *testing.T) {
	svc := newLocalTestService(t)
	carol := seedUser(t, svc, models.User{Username: "carol", Password: "hash-carol", Email: "c@example.com", Source: "local"})
	dave := seedUser(t, svc, models.User{Username: "dave", Password: "hash-dave-here", Source: "local"})
	seedUser(t, svc, models.User{Username: "admin", Password: "hash-admin", Source: "local"})
	seedUser(t, svc, models.User{Username: "nopass", Source: "local"})
	seedUser(t, svc, models.User{Username: "bob", Password: "hash-bob", Source: "pam"})
	if err := svc.DB.Create(&clusterModels.SylveUser{Username: "dave", Password: "hash-dave-elsewhere"}).Error; err != nil {
		t.Fatal(err)
	}
	seedUserToken(t, svc, carol.ID, "session")

	for range 2 {
		if err := svc.shareLocalUsers(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if shared := sharedUser(t, svc, "carol"); shared == nil || shared.Password != "hash-carol" || shared.Email != "c@example.com" {
		t.Fatalf("carol in the cluster = %+v", shared)
	}
	if row, err := svc.GetUserByID(carol.ID); err != nil || !row.Shared || userTokenCount(t, svc, carol.ID) != 1 {
		t.Fatalf("carol here = %+v, %v; want the same row shared with its session kept", row, err)
	}
	if row, err := svc.GetUserByID(dave.ID); err != nil || row.Shared || row.Password != "hash-dave-here" {
		t.Fatalf("dave here = %+v, %v; want this node's own account kept", row, err)
	}
	for _, name := range []string{"admin", "nopass", "bob"} {
		if sharedUser(t, svc, name) != nil {
			t.Fatalf("%s was shared", name)
		}
	}
}
