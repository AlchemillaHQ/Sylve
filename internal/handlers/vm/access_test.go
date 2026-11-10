// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func aliceAccess(t *testing.T) (*auth.Service, func(check func(*gin.Context) bool) int) {
	t.Helper()
	db := testutil.NewSQLiteTestDB(t, &models.User{}, &models.Migrations{},
		&clusterModels.ACLGrant{}, &clusterModels.ACLRole{}, &clusterModels.ACLGroup{})
	alice := models.User{Username: "alice", Source: "local"}
	records := []any{&alice, &models.Migrations{Name: "rbac_admin_grants"}}
	for _, path := range []string{"/vm/100", "/storage/tank", "/switch/lan"} {
		records = append(records, &clusterModels.ACLGrant{Path: path, SubjectType: clusterModels.ACLSubjectUser, Subject: "alice@sylve", Role: "GuestAdmin", Propagate: true})
	}
	for _, record := range records {
		if err := db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	gin.SetMode(gin.TestMode)
	return &auth.Service{DB: db}, func(check func(*gin.Context) bool) int {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		c.Set("UserID", alice.ID)
		if check(c) {
			return http.StatusOK
		}
		return recorder.Code
	}
}

func TestVMChecksCoverIDsPoolsSwitchesAndRestrictedSettings(t *testing.T) {
	service, as := aliceAccess(t)
	rid := func(id uint) *uint { return &id }
	for _, tt := range []struct {
		name string
		req  libvirtServiceInterfaces.CreateVMRequest
		want int
	}{
		{"its own ID, pool and switch", libvirtServiceInterfaces.CreateVMRequest{RID: rid(100), StorageType: "zvol", StoragePool: "tank", SwitchName: "lan"}, http.StatusOK},
		{"no disk or NIC", libvirtServiceInterfaces.CreateVMRequest{RID: rid(100), StorageType: "none", SwitchName: "none"}, http.StatusOK},
		{"another ID", libvirtServiceInterfaces.CreateVMRequest{RID: rid(101), StorageType: "none"}, http.StatusForbidden},
		{"another pool", libvirtServiceInterfaces.CreateVMRequest{RID: rid(100), StorageType: "zvol", StoragePool: "zroot"}, http.StatusForbidden},
		{"another switch", libvirtServiceInterfaces.CreateVMRequest{RID: rid(100), StorageType: "none", SwitchName: "wan"}, http.StatusForbidden},
		{"PCI passthrough", libvirtServiceInterfaces.CreateVMRequest{RID: rid(100), StorageType: "none", PCIDevices: []int{1}}, http.StatusForbidden},
		{"extra bhyve options", libvirtServiceInterfaces.CreateVMRequest{RID: rid(100), StorageType: "none", ExtraBhyveOptions: []string{"-x"}}, http.StatusForbidden},
	} {
		if got := as(func(c *gin.Context) bool { return allowVMCreate(c, service, tt.req) }); got != tt.want {
			t.Errorf("create, %s: status %d, want %d", tt.name, got, tt.want)
		}
	}

	pool := "tank"
	for _, tt := range []struct {
		name string
		req  libvirtServiceInterfaces.StorageAttachRequest
		want int
	}{
		{"a new disk on its pool", libvirtServiceInterfaces.StorageAttachRequest{RID: 100, AttachType: "new", StorageType: "zvol", Pool: &pool}, http.StatusOK},
		{"importing a host file", libvirtServiceInterfaces.StorageAttachRequest{RID: 100, AttachType: "import", StorageType: "raw", Pool: &pool, RawPath: "/etc/master.passwd"}, http.StatusForbidden},
		{"sharing a host directory", libvirtServiceInterfaces.StorageAttachRequest{RID: 100, AttachType: "new", StorageType: "filesystem"}, http.StatusForbidden},
		{"another VM", libvirtServiceInterfaces.StorageAttachRequest{RID: 101, AttachType: "new", StorageType: "zvol", Pool: &pool}, http.StatusForbidden},
	} {
		if got := as(func(c *gin.Context) bool { return allowStorageAttach(c, service, tt.req) }); got != tt.want {
			t.Errorf("storage, %s: status %d, want %d", tt.name, got, tt.want)
		}
	}

	template := &vmModels.VMTemplate{Storages: []vmModels.VMTemplateStorage{{SourceStorageID: 1, Pool: "zroot"}}}
	clone := libvirtServiceInterfaces.CreateFromTemplateRequest{RID: 100}
	if got := as(func(c *gin.Context) bool { return allowVMTemplateCreate(c, service, template, clone) }); got != http.StatusForbidden {
		t.Errorf("cloning a template without Guest.Clone on /vm: status %d, want 403", got)
	}
}
