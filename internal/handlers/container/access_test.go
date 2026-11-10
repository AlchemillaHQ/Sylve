// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package containerHandlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestContainerUpdateNeedsWhatChanged(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.User{}, &models.Migrations{},
		&clusterModels.ACLGrant{}, &clusterModels.ACLRole{}, &clusterModels.ACLGroup{})
	alice := models.User{Username: "alice", Source: "local"}
	for _, record := range []any{&alice, &models.Migrations{Name: "rbac_admin_grants"},
		&clusterModels.ACLGrant{Path: "/container/7", SubjectType: clusterModels.ACLSubjectUser, Subject: "alice@sylve", Role: "GuestUser", Propagate: true},
		&clusterModels.ACLGrant{Path: "/container/8", SubjectType: clusterModels.ACLSubjectUser, Subject: "alice@sylve", Role: "GuestAdmin", Propagate: true},
	} {
		if err := db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	service := &auth.Service{DB: db}
	gin.SetMode(gin.TestMode)

	base := containerServiceInterfaces.Spec{
		Image:  "nginx:1",
		Env:    []containerModels.EnvVar{{Name: "A", Value: "1"}},
		Mounts: []containerModels.Mount{{Type: containerModels.MountPath, Source: "/srv/www", Target: "/www"}},
	}
	withEnv := base
	withEnv.Env = []containerModels.EnvVar{{Name: "A", Value: "2"}}
	withMount := withEnv
	withMount.Mounts = append(append([]containerModels.Mount{}, base.Mounts...), containerModels.Mount{Type: containerModels.MountPath, Source: "/etc", Target: "/host-etc"})

	for _, tt := range []struct {
		name      string
		ocid      uint
		old, spec containerServiceInterfaces.Spec
		want      int
	}{
		{"nothing changed", 7, base, base, http.StatusOK},
		{"env without Guest.Config.Options", 7, base, withEnv, http.StatusForbidden},
		{"env, keeping an admin's host mount", 8, base, withEnv, http.StatusOK},
		{"adding a host mount", 8, base, withMount, http.StatusForbidden},
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPut, "/", nil)
		c.Set("UserID", alice.ID)
		got := http.StatusOK
		if !allowContainerUpdate(c, service, nil, tt.ocid, tt.old, tt.spec) {
			got = recorder.Code
		}
		if got != tt.want {
			t.Errorf("%s: status %d, want %d", tt.name, got, tt.want)
		}
	}
}
