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
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestACLCommands(t *testing.T) {
	db := newClusterModelTestDB(t)
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)
	apply := func(commandType, action string, payload any) error {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		return applyFSMCommand(t, fsm, Command{Type: commandType, Action: action, Data: raw})
	}

	grant := ACLGrant{Path: "/vm/100", SubjectType: ACLSubjectUser, Subject: "alice@sylve", Role: "Ops"}
	if err := apply("acl_grant", "set", grant); err != nil {
		t.Fatal(err)
	}
	grant.Propagate = true
	if err := apply("acl_grant", "set", grant); err != nil {
		t.Fatal(err)
	}
	var grants []ACLGrant
	if err := db.Find(&grants).Error; err != nil || len(grants) != 1 || !grants[0].Propagate {
		t.Fatalf("grants = %+v, %v; want one with propagate", grants, err)
	}
	if err := apply("acl_grant", "set", ACLGrant{Path: "/", SubjectType: "nobody", Subject: "x", Role: "Ops"}); err == nil {
		t.Fatal("grant with an unknown subject type was stored")
	}

	for _, privileges := range [][]string{{"Guest.Audit"}, {"Guest.PowerMgmt"}} {
		if err := apply("acl_role", "set", ACLRole{Name: "Ops", Privileges: privileges}); err != nil {
			t.Fatal(err)
		}
	}
	var roles []ACLRole
	if err := db.Find(&roles).Error; err != nil || len(roles) != 1 || !slices.Equal(roles[0].Privileges, []string{"Guest.PowerMgmt"}) {
		t.Fatalf("roles = %+v, %v; want Ops with Guest.PowerMgmt", roles, err)
	}

	if err := apply("acl_role", "delete", ACLRole{Name: "Ops"}); err != nil {
		t.Fatal(err)
	}
	var left int64
	if err := db.Model(&ACLGrant{}).Count(&left).Error; err != nil || left != 0 {
		t.Fatalf("grants of a deleted role = %d, %v", left, err)
	}
	if err := db.Model(&ACLRole{}).Count(&left).Error; err != nil || left != 0 {
		t.Fatalf("roles = %d, %v after delete", left, err)
	}

	for _, subject := range []string{"old@sylve", "new@sylve"} {
		if err := apply("acl_grant", "set", ACLGrant{Path: "/", SubjectType: ACLSubjectUser, Subject: subject, Role: "Auditor"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := apply("acl_grant", "set", ACLGrant{Path: "/vm", SubjectType: ACLSubjectUser, Subject: "old@sylve", Role: "GuestUser"}); err != nil {
		t.Fatal(err)
	}
	if err := apply("acl_subject", "rename", ACLSubjectRename{Type: ACLSubjectUser, From: "old@sylve", To: "new@sylve"}); err != nil {
		t.Fatalf("rename onto a name that holds the same grant: %v", err)
	}
	if err := db.Order("path").Find(&grants).Error; err != nil || len(grants) != 2 ||
		grants[0].Subject != "new@sylve" || grants[1].Subject != "new@sylve" {
		t.Fatalf("grants after rename = %+v, %v", grants, err)
	}
	if err := apply("acl_subject", "delete", ACLSubject{Type: ACLSubjectUser, Name: "new@sylve"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&ACLGrant{}).Count(&left).Error; err != nil || left != 0 {
		t.Fatalf("grants after deleting the subject = %d, %v", left, err)
	}
}

func TestSnapshotRestoreToleratesMissingTablesAndRefusesUnknownOnes(t *testing.T) {
	source := testutil.NewSQLiteTestDB(t, allSnapshotModels()...)
	if err := source.Create(&ClusterNote{Title: "kept"}).Error; err != nil {
		t.Fatal(err)
	}
	snapshot, err := captureClusterSnapshot(source)
	if err != nil {
		t.Fatal(err)
	}

	older := *snapshot
	older.Tables = slices.DeleteFunc(slices.Clone(snapshot.Tables), func(table SnapshotTable) bool {
		return strings.HasPrefix(table.Name, "acl_")
	})
	destination := testutil.NewSQLiteTestDB(t, allSnapshotModels()...)
	if err := destination.Create(&ACLGrant{Path: "/", SubjectType: ACLSubjectUser, Subject: "bob@pam", Role: "Administrator"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := NewFSMDispatcher(destination).Restore(snapshotReader(t, &older)); err != nil {
		t.Fatalf("restore of a snapshot without the ACL tables: %v", err)
	}
	var notes, grants int64
	destination.Model(&ClusterNote{}).Count(&notes)
	destination.Model(&ACLGrant{}).Count(&grants)
	if notes != 1 || grants != 0 {
		t.Fatalf("after restore notes=%d grants=%d; want 1 and 0", notes, grants)
	}

	newer := *snapshot
	newer.Tables = append(slices.Clone(snapshot.Tables), SnapshotTable{Name: "future_table", Columns: []string{"id"}})
	err = NewFSMDispatcher(destination).Restore(snapshotReader(t, &newer))
	if err == nil || !strings.Contains(err.Error(), "cluster_snapshot_table_unknown") {
		t.Fatalf("restore of a snapshot with an unknown table: %v", err)
	}
}

func TestACLGroupCommands(t *testing.T) {
	db := newClusterModelTestDB(t)
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)
	apply := func(commandType, action string, payload any) {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyFSMCommand(t, fsm, Command{Type: commandType, Action: action, Data: raw}); err != nil {
			t.Fatalf("%s/%s: %v", commandType, action, err)
		}
	}
	members := func() []string {
		t.Helper()
		var group ACLGroup
		if err := db.Where("name = ?", "ops").First(&group).Error; err != nil {
			t.Fatal(err)
		}
		return group.Members
	}

	apply("acl_group", "set", ACLGroup{Name: "ops", Members: []string{"alice@sylve", "bob@pam"}})
	apply("acl_grant", "set", ACLGrant{Path: "/vm", SubjectType: ACLSubjectGroup, Subject: "ops", Role: "GuestUser"})

	apply("acl_subject", "rename", ACLSubjectRename{Type: ACLSubjectUser, From: "bob@pam", To: "robert@pam"})
	if got := members(); !slices.Equal(got, []string{"alice@sylve", "robert@pam"}) {
		t.Fatalf("members after rename = %v", got)
	}
	apply("acl_subject", "delete", ACLSubject{Type: ACLSubjectUser, Name: "alice@sylve"})
	if got := members(); !slices.Equal(got, []string{"robert@pam"}) {
		t.Fatalf("members after delete = %v", got)
	}

	apply("acl_group", "delete", ACLGroup{Name: "ops"})
	var groups, grants int64
	db.Model(&ACLGroup{}).Count(&groups)
	db.Model(&ACLGrant{}).Count(&grants)
	if groups != 0 || grants != 0 {
		t.Fatalf("after deleting the group: groups=%d grants=%d", groups, grants)
	}
}
