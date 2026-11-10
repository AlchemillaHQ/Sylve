// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"encoding/json"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	"gorm.io/gorm"
)

func newSylveUserTestFSM(t *testing.T) (*gorm.DB, func(action string, payload any) error) {
	t.Helper()
	db := newClusterModelTestDB(t)
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)
	return db, func(action string, payload any) error {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		return applyFSMCommand(t, fsm, Command{Type: "sylve_user", Action: action, Data: raw})
	}
}

func seedRows(t *testing.T, db *gorm.DB, values ...any) {
	t.Helper()
	for _, value := range values {
		if err := db.Create(value).Error; err != nil {
			t.Fatalf("seed %T: %v", value, err)
		}
	}
}

func userRow(t *testing.T, db *gorm.DB, username string) *models.User {
	t.Helper()
	var row models.User
	if err := db.Where("username = ?", username).Limit(1).Find(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.ID == 0 {
		return nil
	}
	return &row
}

func tokenCount(t *testing.T, db *gorm.DB, userID uint) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&models.Token{}).Where("user_id = ?", userID).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSylveUserCreateMirrorsWithoutTakingOtherAccounts(t *testing.T) {
	db, apply := newSylveUserTestFSM(t)
	own := models.User{Username: "carol", Password: "hash-carol", Source: "local"}
	other := models.User{Username: "dave", Password: "hash-dave-here", Source: "local"}
	seedRows(t, db, &own, &other, &models.User{Username: "bob", Password: "pam-hash", Source: "pam"})

	for _, user := range []SylveUser{
		{Username: "alice", Password: "hash-alice", FullName: "Alice"},
		{Username: "carol", Password: "hash-carol"},
		{Username: "dave", Password: "hash-dave-elsewhere"},
		{Username: "bob", Password: "hash-bob"},
	} {
		if err := apply("create", user); err != nil {
			t.Fatalf("create %s: %v", user.Username, err)
		}
	}
	if row := userRow(t, db, "alice"); row == nil || !row.Shared || row.Source != "local" || row.FullName != "Alice" {
		t.Fatalf("alice = %+v, want a shared local copy", row)
	}
	if row := userRow(t, db, "carol"); row == nil || !row.Shared || row.ID != own.ID {
		t.Fatalf("carol = %+v, want this node's own row adopted", row)
	}
	if row := userRow(t, db, "dave"); row == nil || row.Shared || row.Password != "hash-dave-here" {
		t.Fatalf("dave = %+v, want this node's account untouched", row)
	}
	if row := userRow(t, db, "bob"); row == nil || row.Shared || row.Source != "pam" {
		t.Fatalf("bob = %+v, want the PAM user untouched", row)
	}

	if err := apply("create", SylveUser{Username: "alice", Password: "hash-second"}); err != nil {
		t.Fatal(err)
	}
	var shared SylveUser
	if err := db.Where("username = ?", "alice").First(&shared).Error; err != nil || shared.Password != "hash-alice" {
		t.Fatalf("alice in the cluster = %+v, %v; want the first one kept", shared, err)
	}
}

func TestSylveUserUpdateRenameDelete(t *testing.T) {
	db, apply := newSylveUserTestFSM(t)
	if err := apply("create", SylveUser{Username: "alice", Password: "hash-1"}); err != nil {
		t.Fatal(err)
	}
	alice := userRow(t, db, "alice")
	seedRows(t, db,
		&models.Token{UserID: alice.ID, Token: "t1"},
		&models.WebAuthnCredential{UserID: alice.ID, CredentialID: "c1", Data: []byte("x")},
		&ACLGrant{Path: "/vm/100", SubjectType: ACLSubjectUser, Subject: "alice@sylve", Role: "GuestUser"},
	)

	if err := apply("update", SylveUser{Username: "alice", Password: "hash-1", Email: "a@example.com"}); err != nil {
		t.Fatal(err)
	}
	if row := userRow(t, db, "alice"); row.Email != "a@example.com" || tokenCount(t, db, alice.ID) != 1 {
		t.Fatalf("after a profile edit: %+v, want the session kept", row)
	}
	if err := apply("update", SylveUser{Username: "alice", Password: "hash-2"}); err != nil {
		t.Fatal(err)
	}
	if tokenCount(t, db, alice.ID) != 0 {
		t.Fatal("a password change kept the session")
	}
	if err := apply("update", SylveUser{Username: "nobody", Password: "x"}); err == nil {
		t.Fatal("updated a user the cluster doesn't have")
	}

	seedRows(t, db, &models.Token{UserID: alice.ID, Token: "t2"})
	if err := apply("rename", SylveUserRename{From: "alice", To: "alicia"}); err != nil {
		t.Fatal(err)
	}
	row := userRow(t, db, "alicia")
	if row == nil || row.ID != alice.ID || tokenCount(t, db, alice.ID) != 0 {
		t.Fatalf("after rename: %+v, want the same row with its sessions ended", row)
	}
	var grant ACLGrant
	if err := db.First(&grant).Error; err != nil || grant.Subject != "alicia@sylve" {
		t.Fatalf("grant = %+v, %v; want it renamed", grant, err)
	}

	seedRows(t, db, &models.User{Username: "eve", Password: "pam", Source: "pam"})
	if err := apply("create", SylveUser{Username: "frank", Password: "hash-f"}); err != nil {
		t.Fatal(err)
	}
	frank := userRow(t, db, "frank")
	if err := apply("rename", SylveUserRename{From: "frank", To: "eve"}); err != nil {
		t.Fatal(err)
	}
	if userRow(t, db, "frank") != nil || userRow(t, db, "eve").ID == frank.ID {
		t.Fatal("renaming onto a name this node holds for someone else kept the copy or took the name")
	}

	if err := apply("delete", SylveUser{Username: "alicia"}); err != nil {
		t.Fatal(err)
	}
	var left, credentials, grants int64
	db.Model(&SylveUser{}).Where("username = ?", "alicia").Count(&left)
	db.Model(&models.WebAuthnCredential{}).Count(&credentials)
	db.Model(&ACLGrant{}).Count(&grants)
	if userRow(t, db, "alicia") != nil || left != 0 || credentials != 0 || grants != 0 {
		t.Fatalf("after delete: shared=%d credentials=%d grants=%d", left, credentials, grants)
	}
}

func TestSnapshotRestoreReconcilesSharedUsers(t *testing.T) {
	source, apply := newSylveUserTestFSM(t)
	for _, user := range []SylveUser{{Username: "kept", Password: "hash-new"}, {Username: "added", Password: "hash-a"}} {
		if err := apply("create", user); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := captureClusterSnapshot(source)
	if err != nil {
		t.Fatal(err)
	}

	destination := newClusterModelTestDB(t)
	kept := models.User{Username: "kept", Password: "hash-old", Source: "local", Shared: true}
	gone := models.User{Username: "gone", Password: "hash-g", Source: "local", Shared: true}
	mine := models.User{Username: "mine", Password: "hash-m", Source: "local"}
	seedRows(t, destination, &kept, &gone, &mine, &SylveUser{Username: "kept", Password: "hash-old"},
		&SylveUser{Username: "gone", Password: "hash-g"}, &models.Token{UserID: kept.ID, Token: "t1"})

	if err := NewFSMDispatcher(destination).Restore(snapshotReader(t, snapshot)); err != nil {
		t.Fatal(err)
	}
	if row := userRow(t, destination, "kept"); row == nil || row.ID != kept.ID || row.Password != "hash-new" || tokenCount(t, destination, kept.ID) != 0 {
		t.Fatalf("kept = %+v, want the same row with the new password and no sessions", row)
	}
	if userRow(t, destination, "gone") != nil {
		t.Fatal("a user deleted while the node was behind kept its copy")
	}
	if row := userRow(t, destination, "added"); row == nil || !row.Shared {
		t.Fatalf("added = %+v, want a shared copy", row)
	}
	if row := userRow(t, destination, "mine"); row == nil || row.Shared {
		t.Fatalf("mine = %+v, want this node's own account untouched", row)
	}
}

func TestJoinSnapshotKeepsUsersFromAnotherCluster(t *testing.T) {
	source, apply := newSylveUserTestFSM(t)
	seedRows(t, source, &ClusterOption{ID: 1, ClusterID: "destination"})
	if err := apply("create", SylveUser{Username: "alice", Password: "hash-a"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := captureClusterSnapshot(source)
	if err != nil {
		t.Fatal(err)
	}
	destination := newClusterModelTestDB(t)
	own := models.User{Username: "bob", Password: "hash-b", Source: "local", Shared: true,
		SharedClusterID: "another-cluster", SharedUserID: 1}
	seedRows(t, destination, &own)
	seedRows(t, destination, &models.WebAuthnCredential{UserID: own.ID, CredentialID: "own-key", Data: []byte("x")})
	fsm := NewFSMDispatcher(destination)
	if err := fsm.ClearReplicatedState("destination"); err != nil {
		t.Fatal(err)
	}
	if err := fsm.Restore(snapshotReader(t, snapshot)); err != nil {
		t.Fatal(err)
	}
	if row := userRow(t, destination, "bob"); row == nil || row.ID != own.ID || row.Password != own.Password || row.Shared || row.SharedClusterID != "" {
		t.Fatalf("other cluster's user = %+v; want a retained node-local account", row)
	}
	var credentials int64
	if err := destination.Model(&models.WebAuthnCredential{}).Where("user_id = ?", own.ID).Count(&credentials).Error; err != nil || credentials != 1 {
		t.Fatalf("retained credentials = %d, %v", credentials, err)
	}
	if row := userRow(t, destination, "alice"); row == nil || !row.Shared || row.SharedClusterID != "destination" {
		t.Fatalf("destination cluster's user = %+v", row)
	}
}

func TestRejoinSnapshotRemovesTheLastDeletedUser(t *testing.T) {
	source, apply := newSylveUserTestFSM(t)
	seedRows(t, source, &ClusterOption{ID: 1, ClusterID: "original"})
	if err := apply("create", SylveUser{Username: "alice", Password: "hash-a"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := captureClusterSnapshot(source)
	if err != nil {
		t.Fatal(err)
	}
	destination := newClusterModelTestDB(t)
	fsm := NewFSMDispatcher(destination)
	if err := fsm.Restore(snapshotReader(t, snapshot)); err != nil {
		t.Fatal(err)
	}
	if err := destination.Transaction(ClearDeclusteredStateTx); err != nil {
		t.Fatal(err)
	}
	if err := apply("delete", SylveUser{Username: "alice"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = captureClusterSnapshot(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := fsm.ClearReplicatedState("original"); err != nil {
		t.Fatal(err)
	}
	if err := fsm.Restore(snapshotReader(t, snapshot)); err != nil {
		t.Fatal(err)
	}
	if row := userRow(t, destination, "alice"); row != nil {
		t.Fatalf("last deleted user returned: %+v", row)
	}
}
