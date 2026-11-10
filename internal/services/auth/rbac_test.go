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
	"github.com/alchemillahq/sylve/internal/testutil"
)

func userGrant(path, role string, propagate bool) clusterModels.ACLGrant {
	return clusterModels.ACLGrant{Path: path, SubjectType: clusterModels.ACLSubjectUser, Subject: "alice@sylve", Role: role, Propagate: propagate}
}

func groupGrant(group, path, role string, propagate bool) clusterModels.ACLGrant {
	return clusterModels.ACLGrant{Path: path, SubjectType: clusterModels.ACLSubjectGroup, Subject: group, Role: role, Propagate: propagate}
}

func TestRolesOnFollowsProxmoxInheritance(t *testing.T) {
	tests := []struct {
		name   string
		grants []clusterModels.ACLGrant
		path   string
		want   []string
	}{
		{"propagates down", []clusterModels.ACLGrant{userGrant("/", RoleAdministrator, true)}, "/vm/100", []string{RoleAdministrator}},
		{"doesn't propagate when unset", []clusterModels.ACLGrant{userGrant("/", RoleAdministrator, false)}, "/vm/100", nil},
		{"applies on its own path when unset", []clusterModels.ACLGrant{userGrant("/", RoleAdministrator, false)}, "/", []string{RoleAdministrator}},
		{"a deeper grant replaces an inherited one", []clusterModels.ACLGrant{
			userGrant("/", RoleAdministrator, true), userGrant("/vm/100", "Auditor", true),
		}, "/vm/100", []string{"Auditor"}},
		{"a sibling keeps the inherited one", []clusterModels.ACLGrant{
			userGrant("/", RoleAdministrator, true), userGrant("/vm/100", "Auditor", true),
		}, "/vm/101", []string{RoleAdministrator}},
		{"the user's grant replaces their groups' on one path", []clusterModels.ACLGrant{
			groupGrant("ops", "/vm/100", "GuestAdmin", true), userGrant("/vm/100", "GuestUser", true),
		}, "/vm/100", []string{"GuestUser"}},
		{"a deeper group grant replaces the user's inherited one", []clusterModels.ACLGrant{
			userGrant("/", RoleAdministrator, true), groupGrant("ops", "/vm/100", "GuestUser", true),
		}, "/vm/100", []string{"GuestUser"}},
		{"groups' grants add up", []clusterModels.ACLGrant{
			groupGrant("ops", "/vm", "GuestUser", true), groupGrant("devs", "/vm", "DatastoreUser", true),
		}, "/vm/100", []string{"GuestUser", "DatastoreUser"}},
		{"NoAccess removes everything", []clusterModels.ACLGrant{
			groupGrant("ops", "/", RoleAdministrator, true), userGrant("/vm/100", RoleNoAccess, true),
		}, "/vm/100", nil},
		{"NoAccess next to another role still wins", []clusterModels.ACLGrant{
			groupGrant("ops", "/", RoleAdministrator, true), groupGrant("blocked", "/", RoleNoAccess, true),
		}, "/vm/100", nil},
	}
	for _, tt := range tests {
		if got := rolesOn(tt.grants, tt.path); !slices.Equal(got, tt.want) {
			t.Errorf("%s: roles = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestCanAndHasAccess(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.User{}, &clusterModels.ACLGrant{}, &clusterModels.ACLRole{}, &clusterModels.ACLGroup{})
	service := &Service{DB: db}
	for _, value := range []any{
		&clusterModels.ACLRole{Name: "Restarter", Privileges: []string{GuestPowerMgmt}},
		&clusterModels.ACLGroup{Name: "ops", Members: []string{"alice@sylve", "bob@pam"}},
	} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, grant := range []clusterModels.ACLGrant{
		userGrant("/vm/100", "Restarter", true),
		groupGrant("ops", "/jail", "GuestUser", true),
		{Path: "/", SubjectType: clusterModels.ACLSubjectUser, Subject: "blocked@pam", Role: RoleNoAccess, Propagate: true},
	} {
		if err := db.Create(&grant).Error; err != nil {
			t.Fatal(err)
		}
	}
	alice := Principal{Username: "alice", Realm: RealmSylve}
	alicePAM := Principal{Username: "alice", Realm: RealmPAM}
	root := Principal{Username: "root", Realm: RealmPAM, Superuser: true}
	blocked := Principal{Username: "blocked", Realm: RealmPAM}

	for _, tt := range []struct {
		name       string
		principal  Principal
		path       string
		privileges []string
		want       bool
	}{
		{"custom role", alice, "/vm/100", []string{GuestPowerMgmt}, true},
		{"privilege the role lacks", alice, "/vm/100", []string{GuestConsole}, false},
		{"another guest", alice, "/vm/101", []string{GuestPowerMgmt}, false},
		{"through a group", alice, "/jail/105", []string{GuestConsole}, true},
		{"all privileges must be held", alice, "/jail/105", []string{GuestConsole, GuestAllocate}, false},
		{"same name, other realm", alicePAM, "/vm/100", []string{GuestPowerMgmt}, false},
		{"superuser", root, "/nodes/x", []string{SysModify}, true},
	} {
		if got, err := service.Can(tt.principal, tt.path, tt.privileges...); err != nil || got != tt.want {
			t.Errorf("%s: Can = %v, %v; want %v", tt.name, got, err, tt.want)
		}
	}

	for _, tt := range []struct {
		name      string
		principal Principal
		want      bool
	}{
		{"user with a grant", alice, true},
		{"group member", Principal{Username: "bob", Realm: RealmPAM}, true},
		{"no grants", alicePAM, false},
		{"only NoAccess", blocked, false},
		{"superuser", root, true},
	} {
		if got, err := service.HasAccess(tt.principal); err != nil || got != tt.want {
			t.Errorf("%s: HasAccess = %v, %v; want %v", tt.name, got, err, tt.want)
		}
	}
}

func TestPrincipalOfTakesTheRealmFromTheAccount(t *testing.T) {
	for _, tt := range []struct {
		user models.User
		want Principal
	}{
		{models.User{Username: "root", Source: "pam"}, Principal{Username: "root", Realm: RealmPAM, Superuser: true}},
		{models.User{Username: "admin", Source: "local"}, Principal{Username: "admin", Realm: RealmSylve, Superuser: true}},
		{models.User{Username: "bob", Source: "pam"}, Principal{Username: "bob", Realm: RealmPAM}},
		{models.User{Username: "alice", Source: "local"}, Principal{Username: "alice", Realm: RealmSylve}},
	} {
		if got := PrincipalOf(&tt.user); got != tt.want {
			t.Errorf("principal for %s = %+v, want %+v", tt.user.Username, got, tt.want)
		}
	}
}

func TestCoversNeedsEverythingTheOtherHolds(t *testing.T) {
	grant := func(subject, path, role string, propagate bool) clusterModels.ACLGrant {
		return clusterModels.ACLGrant{Path: path, SubjectType: clusterModels.ACLSubjectUser, Subject: subject, Role: role, Propagate: propagate}
	}
	for _, tt := range []struct {
		name               string
		grants             []clusterModels.ACLGrant
		wantPath, wantPriv string
	}{
		{"less, below", []clusterModels.ACLGrant{
			grant("p@sylve", "/vm", RoleAdministrator, true), grant("q@sylve", "/vm/127", "GuestUser", true),
		}, "", ""},
		{"somewhere else", []clusterModels.ACLGrant{
			grant("p@sylve", "/vm", RoleAdministrator, true), grant("q@sylve", "/jail", "GuestUser", true),
		}, "/jail", GuestAudit},
		{"where p has NoAccess", []clusterModels.ACLGrant{
			grant("p@sylve", "/vm", RoleAdministrator, true), grant("p@sylve", "/vm/127", RoleNoAccess, true),
			grant("q@sylve", "/vm", "GuestUser", true),
		}, "/vm/127", GuestAudit},
		{"below a grant p doesn't propagate", []clusterModels.ACLGrant{
			grant("p@sylve", "/vm", RoleAdministrator, false), grant("q@sylve", "/vm", "GuestUser", true),
		}, "/vm/*", GuestAudit},
		{"through a group", []clusterModels.ACLGrant{
			grant("p@sylve", "/vm", RoleAdministrator, true),
			{Path: "/vm", SubjectType: clusterModels.ACLSubjectGroup, Subject: "ops", Role: "GuestAdmin", Propagate: true},
		}, "", ""},
		{"a group grant p lacks", []clusterModels.ACLGrant{
			{Path: "/storage/tank", SubjectType: clusterModels.ACLSubjectGroup, Subject: "ops", Role: "DatastoreUser", Propagate: true},
		}, "/storage/tank", DatastoreAudit},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db := testutil.NewSQLiteTestDB(t, &models.User{}, &clusterModels.ACLGrant{}, &clusterModels.ACLRole{}, &clusterModels.ACLGroup{})
			if err := db.Create(&clusterModels.ACLGroup{Name: "ops", Members: []string{"q@sylve"}}).Error; err != nil {
				t.Fatal(err)
			}
			for _, g := range tt.grants {
				if err := db.Create(&g).Error; err != nil {
					t.Fatal(err)
				}
			}
			service := &Service{DB: db}
			p := Principal{Username: "p", Realm: RealmSylve}
			q := Principal{Username: "q", Realm: RealmSylve}
			path, privilege, err := service.Covers(p, q)
			if err != nil || path != tt.wantPath || privilege != tt.wantPriv {
				t.Fatalf("Covers = %q %q %v, want %q %q", path, privilege, err, tt.wantPath, tt.wantPriv)
			}
		})
	}

	db := testutil.NewSQLiteTestDB(t, &models.User{}, &clusterModels.ACLGrant{}, &clusterModels.ACLRole{}, &clusterModels.ACLGroup{})
	service := &Service{DB: db}
	root := Principal{Username: "root", Realm: RealmPAM, Superuser: true}
	nobody := Principal{Username: "nobody", Realm: RealmSylve}
	if path, _, _ := service.Covers(root, nobody); path != "" {
		t.Errorf("a superuser covers everyone, got %q", path)
	}
	if path, _, _ := service.Covers(nobody, root); path != "/" {
		t.Errorf("no one but a superuser covers one, got %q", path)
	}
}
