// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func TestInitSecretKeepsARandomSecretAndReplacesTheHostDerivedOne(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.SystemSecrets{})
	service := &Service{DB: db}

	if err := service.InitSecret("JWTSecret"); err != nil {
		t.Fatalf("init secret: %v", err)
	}
	first, err := service.GetJWTSecret()
	if err != nil || len(first) != 64 {
		t.Fatalf("secret = %q, %v; want 64 random characters", first, err)
	}
	if err := service.InitSecret("JWTSecret"); err != nil {
		t.Fatalf("init secret again: %v", err)
	}
	if again, _ := service.GetJWTSecret(); again != first {
		t.Fatalf("secret changed across restarts")
	}

	uuid, err := utils.GetSystemUUID()
	if err != nil {
		t.Skipf("no kern.hostuuid: %v", err)
	}
	derived := utils.SHA256(uuid, 6)
	if err := db.Model(&models.SystemSecrets{}).Where("name = ?", "JWTSecret").Update("data", derived).Error; err != nil {
		t.Fatalf("seed host-derived secret: %v", err)
	}
	if err := service.InitSecret("JWTSecret"); err != nil {
		t.Fatalf("init secret over host-derived one: %v", err)
	}
	if replaced, _ := service.GetJWTSecret(); replaced == derived || len(replaced) != 64 {
		t.Fatalf("host-derived secret was kept: %q", replaced)
	}
}
