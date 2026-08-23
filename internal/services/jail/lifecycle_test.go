// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"context"
	"testing"
	"time"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestJailRuntimeStartsOnlyWhenMonitoringStarts(t *testing.T) {
	database := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.JailStats{},
		&jailModels.JailBootstrap{},
	)
	bootstrap := jailModels.JailBootstrap{
		Name:   "",
		Pool:   "",
		Status: "running",
	}
	if err := database.Create(&bootstrap).Error; err != nil {
		t.Fatalf("seed interrupted bootstrap: %v", err)
	}

	service := NewJailService(
		database,
		&jailNetworkValidationFakeNetworkService{},
		nil,
		nil,
	).(*Service)

	var stored jailModels.JailBootstrap
	if err := database.First(&stored, bootstrap.ID).Error; err != nil {
		t.Fatalf("load bootstrap after construction: %v", err)
	}
	if stored.Status != "running" {
		t.Fatalf("constructor changed bootstrap status to %q", stored.Status)
	}

	ctx, cancel := context.WithCancel(context.Background())
	service.StartStatsMonitoring(ctx)
	t.Cleanup(cancel)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := database.First(&stored, bootstrap.ID).Error; err != nil {
			t.Fatalf("reload bootstrap: %v", err)
		}
		if stored.Status == "failed" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("interrupted bootstrap was not recovered after runtime startup")
}
