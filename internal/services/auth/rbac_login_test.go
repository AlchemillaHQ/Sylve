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
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestLoginFollowsGrantsOnceAdminsAreMigrated(t *testing.T) {
	service := newLocalTestService(t)
	if err := service.DB.AutoMigrate(&models.Migrations{}, &clusterModels.ACLGrant{}, &clusterModels.ACLRole{}, &clusterModels.ACLGroup{}); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.Create(&models.SystemSecrets{Name: "JWTSecret", Data: "test-secret"}).Error; err != nil {
		t.Fatal(err)
	}
	createLoginTestUser(t, service, "bob", "pw", true)
	createLoginTestUser(t, service, "carol", "pw", false)
	createLoginTestUser(t, service, "admin", "pw", true)
	login := func(username string) (string, error) {
		_, token, err := service.CreateJWT(username, "pw", "sylve", false)
		return token, err
	}

	if _, err := login("bob"); err != nil {
		t.Fatalf("admin before migration: %v", err)
	}
	if _, err := login("carol"); err == nil || err.Error() != "only_admin_allowed" {
		t.Fatalf("non-admin before migration: %v", err)
	}

	var proposed []clusterModels.ACLGrant
	service.SetProposer(func(_ context.Context, commandType, action string, payload any) error {
		grant := payload.(clusterModels.ACLGrant)
		proposed = append(proposed, grant)
		return service.DB.Create(&grant).Error
	})
	for range 2 {
		if err := service.migrateAdminGrants(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(proposed) != 1 || proposed[0].Subject != "bob@sylve" || proposed[0].Path != "/" ||
		proposed[0].Role != RoleAdministrator || !proposed[0].Propagate {
		t.Fatalf("proposed = %+v; want Administrator on / for bob@sylve only, once", proposed)
	}
	if !service.RBACActive() {
		t.Fatal("RBAC isn't active after the migration")
	}

	for _, username := range []string{"bob", "admin"} {
		if _, err := login(username); err != nil {
			t.Fatalf("%s after migration: %v", username, err)
		}
	}
	if _, err := login("carol"); err == nil || err.Error() != "no_permissions" {
		t.Fatalf("user without grants: %v", err)
	}

	grant := clusterModels.ACLGrant{Path: "/vm/100", SubjectType: clusterModels.ACLSubjectUser, Subject: "carol@sylve", Role: "GuestUser"}
	if err := service.DB.Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	token, err := login("carol")
	if err != nil {
		t.Fatalf("user with a grant: %v", err)
	}
	if !service.VerifyTokenInDb(token) {
		t.Fatal("session of a user with a grant isn't valid")
	}
	if err := service.DB.Delete(&grant).Error; err != nil {
		t.Fatal(err)
	}
	if service.VerifyTokenInDb(token) {
		t.Fatal("session outlived the user's last grant")
	}
}
