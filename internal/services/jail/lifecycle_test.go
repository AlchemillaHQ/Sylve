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
	"reflect"
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

func TestNormalizeLegacyLifecycleHooksIsNarrow(t *testing.T) {
	input := []jailModels.JailHooks{
		{ID: 1, JailID: 7, Phase: jailModels.JailHookPhaseStart, Enabled: true, Script: defaultFreeBSDJailStartCommand},
		{ID: 2, JailID: 7, Phase: jailModels.JailHookPhaseStop, Enabled: true, Script: defaultFreeBSDJailStopCommand + "\necho custom"},
		{ID: 3, JailID: 7, Phase: jailModels.JailHookPhasePostStart, Enabled: true, Script: "echo retained"},
	}

	normalized, changed := NormalizeLegacyLifecycleHooks(jailModels.JailTypeFreeBSD, input)
	if !changed {
		t.Fatal("expected exact legacy default to be normalized")
	}
	if normalized[0].Enabled || normalized[0].Script != "" {
		t.Fatalf("exact legacy hook was not cleared: %+v", normalized[0])
	}
	if normalized[0].ID != input[0].ID || normalized[0].JailID != input[0].JailID {
		t.Fatalf("hook identity changed: %+v", normalized[0])
	}
	if normalized[1] != input[1] || normalized[2] != input[2] {
		t.Fatalf("custom hooks were changed: got=%+v want=%+v", normalized, input)
	}
	if input[0].Script == "" {
		t.Fatal("normalization mutated its input")
	}

	linux, linuxChanged := NormalizeLegacyLifecycleHooks(jailModels.JailTypeLinux, input)
	if linuxChanged || !reflect.DeepEqual(linux, input) {
		t.Fatalf("Linux hooks must remain untouched: changed=%t got=%+v want=%+v", linuxChanged, linux, input)
	}
}
