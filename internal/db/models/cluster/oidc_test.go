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
	"slices"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	"gorm.io/gorm"
)

func newOIDCTestFSM(t *testing.T) (*gorm.DB, func(commandType, action string, payload any) error) {
	t.Helper()
	db := newClusterModelTestDB(t)
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)
	return db, func(commandType, action string, payload any) error {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		return applyFSMCommand(t, fsm, Command{Type: commandType, Action: action, Data: raw})
	}
}

func testRealm(name string) AuthRealm {
	return AuthRealm{Name: name, Label: name, Issuer: "https://idp.example/" + name, ClientID: "sylve", Enabled: true}
}

func realmRow(t *testing.T, db *gorm.DB, name string) AuthRealm {
	t.Helper()
	var realm AuthRealm
	if err := db.Where("name = ?", name).First(&realm).Error; err != nil {
		t.Fatal(err)
	}
	return realm
}

func oidcUserRow(t *testing.T, db *gorm.DB, realm, username string) *OIDCUser {
	t.Helper()
	var user OIDCUser
	if err := db.Where("realm = ? AND username = ?", realm, username).Limit(1).Find(&user).Error; err != nil {
		t.Fatal(err)
	}
	if user.ID == 0 {
		return nil
	}
	return &user
}

func TestAuthRealmSet(t *testing.T) {
	db, apply := newOIDCTestFSM(t)
	secret, empty := "s3cret", ""
	company, lab := testRealm("company"), testRealm("lab")
	company.IsDefault = true
	if err := apply("auth_realm", "set", AuthRealmSet{AuthRealm: company, ClientSecret: &secret}); err != nil {
		t.Fatal(err)
	}
	company.Label = "Company SSO"
	if err := apply("auth_realm", "set", AuthRealmSet{AuthRealm: company}); err != nil {
		t.Fatal(err)
	}
	if realm := realmRow(t, db, "company"); realm.ClientSecret != secret || realm.Label != "Company SSO" {
		t.Fatalf("realm = %+v, want the new label and the stored secret", realm)
	}
	if err := apply("auth_realm", "set", AuthRealmSet{AuthRealm: company, ClientSecret: &empty}); err != nil {
		t.Fatal(err)
	}
	if realm := realmRow(t, db, "company"); realm.ClientSecret != "" {
		t.Fatal("an empty secret didn't clear the stored one")
	}

	moved := company
	moved.Issuer = "https://elsewhere.example"
	if err := apply("auth_realm", "set", AuthRealmSet{AuthRealm: moved}); err == nil {
		t.Fatal("a realm moved to another issuer")
	}

	lab.IsDefault = true
	if err := apply("auth_realm", "set", AuthRealmSet{AuthRealm: lab}); err != nil {
		t.Fatal(err)
	}
	if realmRow(t, db, "company").IsDefault || !realmRow(t, db, "lab").IsDefault {
		t.Fatal("two realms are the default")
	}
}

func TestOIDCUserLogin(t *testing.T) {
	db, apply := newOIDCTestFSM(t)
	realm := testRealm("company")
	if err := apply("auth_realm", "set", AuthRealmSet{AuthRealm: realm}); err != nil {
		t.Fatal(err)
	}

	if err := apply("oidc_user", "login", OIDCUser{Realm: "company", Username: "eve", Subject: "sub-eve"}); err == nil {
		t.Fatal("an unknown user signed in without auto-create")
	}
	if oidcUserRow(t, db, "company", "eve") != nil || userRow(t, db, "eve@company") != nil {
		t.Fatal("a refused login stored a user")
	}

	if err := apply("oidc_user", "create", OIDCUser{Realm: "company", Username: "alice", Notes: "ops"}); err != nil {
		t.Fatal(err)
	}
	if err := apply("oidc_user", "login", OIDCUser{
		Realm: "company", Username: "alice", Subject: "sub-alice", FullName: "Alice", Groups: []string{"ops"}, IssuedAt: 100,
	}); err != nil {
		t.Fatal(err)
	}
	alice := oidcUserRow(t, db, "company", "alice")
	if alice == nil || alice.Subject != "sub-alice" || alice.FullName != "Alice" || alice.Notes != "ops" || !slices.Equal(alice.Groups, []string{"ops"}) {
		t.Fatalf("alice = %+v, want the pre-created user bound to her subject", alice)
	}
	row := userRow(t, db, "alice@company")
	if row == nil || row.Source != UserSourceOIDC || !row.DisablePassword || row.Password != "" || row.SharedUserID != alice.ID || row.FullName != "Alice" {
		t.Fatalf("users row = %+v", row)
	}

	if err := apply("oidc_user", "login", OIDCUser{Realm: "company", Username: "alice", Subject: "sub-mallory"}); err == nil {
		t.Fatal("a second subject took a bound name")
	}
	if err := apply("oidc_user", "login", OIDCUser{Realm: "company", Username: "renamed", Subject: "sub-alice", IssuedAt: 99}); err == nil {
		t.Fatal("an older ID token was accepted")
	}
	if err := apply("oidc_user", "login", OIDCUser{Realm: "company", Username: "renamed", Subject: "sub-alice", IssuedAt: 101}); err != nil {
		t.Fatal(err)
	}
	if alice := oidcUserRow(t, db, "company", "alice"); alice == nil || len(alice.Groups) != 0 {
		t.Fatalf("alice = %+v, want the same name and her groups cleared", alice)
	}

	realm.AutoCreate = true
	if err := apply("auth_realm", "set", AuthRealmSet{AuthRealm: realm}); err != nil {
		t.Fatal(err)
	}
	if err := apply("oidc_user", "login", OIDCUser{Realm: "company", Username: "eve", Subject: "sub-eve"}); err != nil {
		t.Fatal(err)
	}
	if eve := oidcUserRow(t, db, "company", "eve"); eve == nil || eve.Subject != "sub-eve" || userRow(t, db, "eve@company") == nil {
		t.Fatal("auto-create didn't create eve")
	}
}

func TestOIDCUserLockAndDelete(t *testing.T) {
	db, apply := newOIDCTestFSM(t)
	realm := testRealm("company")
	realm.AutoCreate = true
	if err := apply("auth_realm", "set", AuthRealmSet{AuthRealm: realm}); err != nil {
		t.Fatal(err)
	}
	if err := apply("oidc_user", "login", OIDCUser{Realm: "company", Username: "alice", Subject: "sub-alice"}); err != nil {
		t.Fatal(err)
	}
	row := userRow(t, db, "alice@company")
	seedRows(t, db, &models.Token{UserID: row.ID, Token: "t1"},
		&ACLGrant{Path: "/", SubjectType: ACLSubjectUser, Subject: "alice@company", Role: "Auditor"},
		&ACLGroup{Name: "ops", Members: []string{"alice@company", "bob@sylve"}})

	if err := apply("oidc_user", "update", OIDCUser{Realm: "company", Username: "alice", Locked: true, Notes: "away"}); err != nil {
		t.Fatal(err)
	}
	if row := userRow(t, db, "alice@company"); !row.Locked || row.Notes != "away" || tokenCount(t, db, row.ID) != 0 {
		t.Fatalf("locked row = %+v, want locked with no sessions", row)
	}

	if err := apply("oidc_user", "delete", OIDCUser{Realm: "company", Username: "alice"}); err != nil {
		t.Fatal(err)
	}
	var grants int64
	if err := db.Model(&ACLGrant{}).Where("subject = ?", "alice@company").Count(&grants).Error; err != nil || grants != 0 {
		t.Fatalf("grants left = %d, %v", grants, err)
	}
	var group ACLGroup
	if err := db.Where("name = ?", "ops").First(&group).Error; err != nil || !slices.Equal(group.Members, []string{"bob@sylve"}) {
		t.Fatalf("group = %+v, %v", group, err)
	}
	if oidcUserRow(t, db, "company", "alice") != nil || userRow(t, db, "alice@company") != nil {
		t.Fatal("alice survived her deletion")
	}
}

func TestAuthRealmDeleteRemovesItsUsers(t *testing.T) {
	db, apply := newOIDCTestFSM(t)
	for _, name := range []string{"company", "lab"} {
		realm := testRealm(name)
		realm.AutoCreate = true
		if err := apply("auth_realm", "set", AuthRealmSet{AuthRealm: realm}); err != nil {
			t.Fatal(err)
		}
		if err := apply("oidc_user", "login", OIDCUser{Realm: name, Username: "alice", Subject: "sub-" + name}); err != nil {
			t.Fatal(err)
		}
	}
	seedRows(t, db, &ACLGrant{Path: "/", SubjectType: ACLSubjectUser, Subject: "alice@company", Role: "Auditor"})

	if err := apply("auth_realm", "delete", AuthRealm{Name: "company"}); err != nil {
		t.Fatal(err)
	}
	var grants int64
	if err := db.Model(&ACLGrant{}).Count(&grants).Error; err != nil || grants != 0 {
		t.Fatalf("grants left = %d, %v", grants, err)
	}
	if oidcUserRow(t, db, "company", "alice") != nil || userRow(t, db, "alice@company") != nil {
		t.Fatal("the deleted realm's user survived")
	}
	if oidcUserRow(t, db, "lab", "alice") == nil || userRow(t, db, "alice@lab") == nil {
		t.Fatal("another realm's user went with it")
	}
}

func TestACLGroupDeletePrunesRealmMappings(t *testing.T) {
	db, apply := newOIDCTestFSM(t)
	realm := testRealm("company")
	realm.GroupMappings = []OIDCGroupMapping{{Claim: "infra", Group: "ops"}, {Claim: "audit", Group: "auditors"}}
	if err := apply("auth_realm", "set", AuthRealmSet{AuthRealm: realm}); err != nil {
		t.Fatal(err)
	}
	if err := apply("acl_group", "delete", ACLGroup{Name: "ops"}); err != nil {
		t.Fatal(err)
	}
	if got := realmRow(t, db, "company").GroupMappings; !slices.Equal(got, []OIDCGroupMapping{{Claim: "audit", Group: "auditors"}}) {
		t.Fatalf("mappings = %+v", got)
	}
}

func TestSnapshotRestoreReconcilesOIDCUsers(t *testing.T) {
	source, apply := newOIDCTestFSM(t)
	realm := testRealm("company")
	realm.AutoCreate = true
	if err := apply("auth_realm", "set", AuthRealmSet{AuthRealm: realm}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"kept", "added"} {
		if err := apply("oidc_user", "login", OIDCUser{Realm: "company", Username: name, Subject: "sub-" + name}); err != nil {
			t.Fatal(err)
		}
	}
	keptID := oidcUserRow(t, source, "company", "kept").ID
	snapshot, err := captureClusterSnapshot(source)
	if err != nil {
		t.Fatal(err)
	}

	destination := newClusterModelTestDB(t)
	kept := models.User{Username: "kept@company", Source: UserSourceOIDC, SharedUserID: keptID}
	replaced := models.User{Username: "added@company", Source: UserSourceOIDC, SharedUserID: 999}
	gone := models.User{Username: "gone@company", Source: UserSourceOIDC, SharedUserID: 998}
	seedRows(t, destination, &kept, &replaced, &gone, &models.Token{UserID: replaced.ID, Token: "old-session"})

	if err := NewFSMDispatcher(destination).Restore(snapshotReader(t, snapshot)); err != nil {
		t.Fatal(err)
	}
	if row := userRow(t, destination, "kept@company"); row == nil || row.ID != kept.ID {
		t.Fatalf("kept = %+v, want the same row", row)
	}
	if row := userRow(t, destination, "added@company"); row == nil || row.ID == replaced.ID || tokenCount(t, destination, replaced.ID) != 0 {
		t.Fatalf("added = %+v, want a fresh row without the old user's sessions", row)
	}
	if userRow(t, destination, "gone@company") != nil {
		t.Fatal("a user deleted while the node was behind kept its row")
	}
}

func TestOIDCUserNeverReplacesANativeRow(t *testing.T) {
	db, apply := newOIDCTestFSM(t)
	realm := testRealm("company")
	realm.AutoCreate = true
	if err := apply("auth_realm", "set", AuthRealmSet{AuthRealm: realm}); err != nil {
		t.Fatal(err)
	}
	native := models.User{Username: "alice@company", Password: "hash", Source: "pam"}
	seedRows(t, db, &native)
	seedRows(t, db, &models.Token{UserID: native.ID, Token: "pam-session"})

	if err := apply("oidc_user", "login", OIDCUser{Realm: "company", Username: "alice", Subject: "sub-alice"}); err == nil {
		t.Fatal("a realm user took a native row's name")
	}
	if row := userRow(t, db, "alice@company"); row == nil || row.ID != native.ID || row.Source != "pam" || tokenCount(t, db, native.ID) != 1 {
		t.Fatalf("native row = %+v, want it untouched with its session", row)
	}
}

func TestJoinDropsLocalOIDCUsers(t *testing.T) {
	db, apply := newOIDCTestFSM(t)
	realm := testRealm("company")
	realm.AutoCreate = true
	if err := apply("auth_realm", "set", AuthRealmSet{AuthRealm: realm}); err != nil {
		t.Fatal(err)
	}
	if err := apply("oidc_user", "login", OIDCUser{Realm: "company", Username: "alice", Subject: "sub-alice"}); err != nil {
		t.Fatal(err)
	}
	if err := NewFSMDispatcher(db).ClearReplicatedState("destination"); err != nil {
		t.Fatal(err)
	}
	if userRow(t, db, "alice@company") != nil {
		t.Fatal("a joining node kept its realm users")
	}
}
