// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func useFSMProposer(t *testing.T, service *Service) *[]string {
	t.Helper()
	fsm := clusterModels.NewFSMDispatcher(service.DB)
	clusterModels.RegisterDefaultHandlers(fsm)
	var proposed []string
	service.SetProposer(func(_ context.Context, commandType, action string, payload any) error {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		cmd := clusterModels.Command{Type: commandType, Action: action, Data: data}
		if err := clusterModels.PrepareCommand(&cmd, time.Now()); err != nil {
			return err
		}
		proposed = append(proposed, commandType+"/"+action)
		return fsm.ApplyLocal(cmd)
	})
	return &proposed
}

func grantSubjects(t *testing.T, service *Service) []string {
	t.Helper()
	grants, err := service.ListGrants()
	if err != nil {
		t.Fatal(err)
	}
	subjects := make([]string, len(grants))
	for i, grant := range grants {
		subjects[i] = grant.Subject
	}
	return subjects
}

func TestSetGrantAndRoleValidate(t *testing.T) {
	service := newLocalTestService(t)
	if err := service.DB.Create(&clusterModels.ACLGroup{Name: "ops", Members: []string{}}).Error; err != nil {
		t.Fatal(err)
	}
	proposed := useFSMProposer(t, service)
	ctx := t.Context()
	user := func(path, subject, role string) clusterModels.ACLGrant {
		return clusterModels.ACLGrant{Path: path, SubjectType: clusterModels.ACLSubjectUser, Subject: subject, Role: role}
	}

	for _, tt := range []struct {
		grant clusterModels.ACLGrant
		err   string
	}{
		{user("/vm/100", "alice@sylve", "GuestUser"), ""},
		{user("/", "bob@pam", RoleAdministrator), ""},
		{user("/storage/tank", "bob@pam", "DatastoreUser"), ""},
		{clusterModels.ACLGrant{Path: "/jail", SubjectType: clusterModels.ACLSubjectGroup, Subject: "ops", Role: "GuestUser"}, ""},
		{user("/vm/abc", "alice@sylve", "GuestUser"), "invalid_acl_path"},
		{user("/vm/100/extra", "alice@sylve", "GuestUser"), "invalid_acl_path"},
		{user("/access/users", "alice@sylve", "GuestUser"), "invalid_acl_path"},
		{user("/elsewhere", "alice@sylve", "GuestUser"), "invalid_acl_path"},
		{user("/vm/100", "alice", "GuestUser"), "invalid_acl_subject"},
		{user("/vm/100", "alice@ldap", "GuestUser"), "invalid_acl_subject"},
		{user("/vm/100", "alice@sylve", "Nonexistent"), "unknown_role"},
		{clusterModels.ACLGrant{Path: "/jail", SubjectType: clusterModels.ACLSubjectGroup, Subject: "nobody", Role: "GuestUser"}, "unknown_group"},
	} {
		err := service.SetGrant(ctx, tt.grant)
		if (err == nil && tt.err != "") || (err != nil && err.Error() != tt.err) {
			t.Errorf("SetGrant(%+v) = %v, want %q", tt.grant, err, tt.err)
		}
	}
	if len(*proposed) != 4 {
		t.Fatalf("proposed %d commands, want only the 4 valid grants", len(*proposed))
	}

	for _, tt := range []struct {
		name       string
		privileges []string
		err        string
	}{
		{"Restarter", []string{GuestPowerMgmt, GuestAudit, GuestPowerMgmt}, ""},
		{"GuestUser", []string{GuestAudit}, "builtin_role"},
		{"1bad", []string{GuestAudit}, "invalid_role_name"},
		{"Empty", nil, "role_privileges_required"},
		{"Typo", []string{"Guest.Teleport"}, "unknown_privilege: Guest.Teleport"},
	} {
		err := service.SetRole(ctx, tt.name, tt.privileges)
		if (err == nil && tt.err != "") || (err != nil && err.Error() != tt.err) {
			t.Errorf("SetRole(%s) = %v, want %q", tt.name, err, tt.err)
		}
	}
	roles, err := service.ListRoles()
	if err != nil {
		t.Fatal(err)
	}
	restarter := roles[slices.IndexFunc(roles, func(role RoleInfo) bool { return role.Name == "Restarter" })]
	if restarter.Builtin || !slices.Equal(restarter.Privileges, []string{GuestAudit, GuestPowerMgmt}) {
		t.Fatalf("Restarter = %+v; want custom, sorted and without duplicates", restarter)
	}
	if err := service.DeleteRole(ctx, RoleAdministrator); err == nil || err.Error() != "builtin_role" {
		t.Fatalf("deleting a built-in role: %v", err)
	}
}

func TestPermissionsShowWhatHoldsOnAndBelowEachPath(t *testing.T) {
	service := newLocalTestService(t)
	if err := service.DB.AutoMigrate(&models.Migrations{}); err != nil {
		t.Fatal(err)
	}
	alice := Principal{Username: "alice", Realm: RealmSylve}
	legacy, err := service.Permissions(alice)
	if err != nil || !slices.Equal(legacy["/"].Privileges, Privileges) {
		t.Fatalf("before the migration everyone holds everything: %+v, %v", legacy, err)
	}
	if err := service.DB.Create(&models.Migrations{Name: adminGrantsMigration}).Error; err != nil {
		t.Fatal(err)
	}
	for _, grant := range []clusterModels.ACLGrant{
		userGrant("/vm", "GuestUser", true),
		userGrant("/vm/100", RoleNoAccess, true),
		userGrant("/jail/105", "Auditor", false),
	} {
		if err := service.DB.Create(&grant).Error; err != nil {
			t.Fatal(err)
		}
	}
	paths, err := service.Permissions(alice)
	if err != nil {
		t.Fatal(err)
	}
	guestUser := slices.Sorted(slices.Values(BuiltinRoles["GuestUser"]))
	for path, want := range map[string]PathPrivileges{
		"/":         {Privileges: []string{}, Below: []string{}},
		"/vm":       {Privileges: guestUser, Below: guestUser},
		"/vm/100":   {Privileges: []string{}, Below: []string{}},
		"/jail/105": {Privileges: slices.Sorted(slices.Values(BuiltinRoles["Auditor"])), Below: []string{}},
	} {
		got, ok := paths[path]
		if !ok || !slices.Equal(got.Privileges, want.Privileges) || !slices.Equal(got.Below, want.Below) {
			t.Errorf("%s: %+v, want %+v", path, got, want)
		}
	}
	if len(paths) != 4 {
		t.Errorf("paths = %v; want only / and the granted ones", paths)
	}
}

func TestDeletingOrRenamingASubjectMovesItsGrants(t *testing.T) {
	service := newLocalTestService(t)
	useFSMProposer(t, service)
	carol := createLoginTestUser(t, service, "carol", "password", false)
	dave := createLoginTestUser(t, service, "dave", "password", false)
	if err := service.SetACLGroup(t.Context(), "ops", []string{"carol@sylve", "dave@sylve", "erin@pam"}); err != nil {
		t.Fatal(err)
	}
	for _, grant := range []clusterModels.ACLGrant{
		{Path: "/vm/100", SubjectType: clusterModels.ACLSubjectUser, Subject: "carol@sylve", Role: "GuestUser"},
		{Path: "/vm/101", SubjectType: clusterModels.ACLSubjectUser, Subject: "dave@sylve", Role: "GuestUser"},
		{Path: "/jail", SubjectType: clusterModels.ACLSubjectGroup, Subject: "ops", Role: "GuestUser"},
	} {
		if err := service.SetGrant(t.Context(), grant); err != nil {
			t.Fatal(err)
		}
	}

	if err := service.EditUser(carol.ID, EditUserOpts{Username: "caroline", FullName: carol.FullName}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := service.DeleteUser(dave.ID, false); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	groups, err := service.ListACLGroups()
	if err != nil || len(groups) != 1 || !slices.Equal(groups[0].Members, []string{"caroline@sylve", "erin@pam"}) {
		t.Fatalf("groups after rename and delete = %+v, %v", groups, err)
	}
	if err := service.DeleteACLGroup(t.Context(), "ops"); err != nil {
		t.Fatalf("delete group: %v", err)
	}

	if subjects := grantSubjects(t, service); !slices.Equal(subjects, []string{"caroline@sylve"}) {
		t.Fatalf("grants held by %v; want only the renamed user's", subjects)
	}
}
