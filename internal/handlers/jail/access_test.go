// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestJailCreateCheck(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.User{}, &models.Migrations{},
		&clusterModels.ACLGrant{}, &clusterModels.ACLRole{}, &clusterModels.ACLGroup{})
	alice := models.User{Username: "alice", Source: "local"}
	records := []any{&alice, &models.Migrations{Name: "rbac_admin_grants"}}
	for _, path := range []string{"/jail/100", "/storage/tank", "/switch/lan"} {
		records = append(records, &clusterModels.ACLGrant{Path: path, SubjectType: clusterModels.ACLSubjectUser, Subject: "alice@sylve", Role: "GuestAdmin", Propagate: true})
	}
	for _, record := range records {
		if err := db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	service := &auth.Service{DB: db}
	gin.SetMode(gin.TestMode)

	ctid := uint(100)
	plain := jailServiceInterfaces.CreateJailRequest{CTID: &ctid, Pool: "tank", SwitchName: "lan",
		Type: jailModels.JailTypeFreeBSD, AllowedOptions: []string{"allow.chflags", "allow.sysvipc", "allow.mount.devfs"}}
	extraOption := plain
	extraOption.AllowedOptions = []string{"allow.chflags", "allow.mount.zfs"}
	linuxOptionOnFreeBSD := plain
	linuxOptionOnFreeBSD.AllowedOptions = []string{"allow.mount.linprocfs"}
	noSwitch := plain
	on := true
	noSwitch.SwitchName, noSwitch.InheritIPv4, noSwitch.InheritIPv6 = "None", &on, &on
	withHook := plain
	withHook.Hooks.Prestart = jailServiceInterfaces.HookPhase{Enabled: true, Script: "id"}
	inheriting := plain
	inheriting.SwitchName = "inherit"
	withSource := plain
	withSource.ZFSSource = &jailServiceInterfaces.ZFSSource{Dataset: "zroot/other"}
	otherSwitch := plain
	otherSwitch.SwitchName = "wan"

	for _, tt := range []struct {
		name string
		req  jailServiceInterfaces.CreateJailRequest
		want int
	}{
		{"its own ID, pool and switch", plain, http.StatusOK},
		{"no switch", noSwitch, http.StatusOK},
		{"an allowed option beyond the defaults", extraOption, http.StatusForbidden},
		{"another type's default allowed option", linuxOptionOnFreeBSD, http.StatusForbidden},
		{"another switch", otherSwitch, http.StatusForbidden},
		{"a lifecycle hook", withHook, http.StatusForbidden},
		{"host networking", inheriting, http.StatusForbidden},
		{"an existing dataset as the root", withSource, http.StatusForbidden},
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		c.Set("UserID", alice.ID)
		got := http.StatusOK
		if !allowJailCreate(c, service, tt.req) {
			got = recorder.Code
		}
		if got != tt.want {
			t.Errorf("%s: status %d, want %d", tt.name, got, tt.want)
		}
	}
}
