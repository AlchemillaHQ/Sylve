// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package nfs

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
	"gorm.io/gorm"
)

func TestStatusUsesObservationWithoutHostWorkOrMutationLocks(t *testing.T) {
	service, driver, _ := configuredRuntime(t)
	driver.events = nil
	service.listDatasets = func(context.Context) ([]*gzfs.Dataset, error) {
		t.Fatal("status discovered filesystems")
		return nil, nil
	}
	service.inspectDatasets = func(context.Context, []string) ([]*gzfs.Dataset, error) {
		t.Fatal("status inspected filesystems")
		return nil, nil
	}
	service.KerberosState = func(context.Context, string) (string, error) {
		t.Fatal("status inspected credentials")
		return "", nil
	}
	service.topologyMu.Lock()
	service.mutationMu.Lock()
	done := make(chan error, 1)
	go func() {
		for range 20 {
			status, err := service.GetStatus(t.Context())
			if err != nil {
				done <- err
				return
			}
			if status.ApplyStatus != "applied" || status.CheckedAt == nil || status.Running == nil || !*status.Running {
				done <- fmt.Errorf("missing current observation: %#v", status)
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(2 * time.Second):
		t.Error("status waited for native mutation locks")
	}
	service.mutationMu.Unlock()
	service.topologyMu.Unlock()
	if len(driver.events) != 0 {
		t.Fatalf("status called the native runtime: %v", driver.events)
	}
	service.observationMu.Lock()
	service.observation.checkedAt = time.Now().Add(-observationTTL - time.Second)
	service.observationMu.Unlock()
	status, err := service.GetStatus(t.Context())
	if err != nil || status.ApplyStatus != "pending" || status.CheckedAt != nil || status.Running != nil || status.AppliedExports != 0 {
		t.Fatalf("expired observation was presented as current: %#v, %v", status, err)
	}
}

func TestPolicyNeutralEditsDoNotDiscoverOrRestart(t *testing.T) {
	service, driver, share := configuredRuntime(t)
	driver.events = nil
	discoveries := 0
	service.listDatasets = func(context.Context) ([]*gzfs.Dataset, error) {
		discoveries++
		return []*gzfs.Dataset{testDataset("1", "tank/data", "/mnt/data")}, nil
	}
	share.Description = "New description"
	if _, err := service.SaveShare(t.Context(), share.ID, share.NFSShareConfig); err != nil {
		t.Fatal(err)
	}
	config, err := service.GetConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetConfig(t.Context(), config); err != nil {
		t.Fatal(err)
	}
	if err := setEnabled(t, service, true); err != nil {
		t.Fatal(err)
	}
	if discoveries != 0 || len(driver.events) != 0 {
		t.Fatalf("metadata/no-op edits touched runtime: discovery=%d events=%v", discoveries, driver.events)
	}
	disabled := shareInput("1", "198.51.100.1")
	disabled.Enabled = false
	other, err := service.SaveShare(t.Context(), 0, disabled)
	if err != nil {
		t.Fatal(err)
	}
	other.Clients = []string{"203.0.113.1"}
	if _, err := service.SaveShare(t.Context(), other.ID, other.NFSShareConfig); err != nil {
		t.Fatal(err)
	}
	if err := service.SetShareEnabled(t.Context(), other.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteShare(t.Context(), other.ID); err != nil {
		t.Fatal(err)
	}
	if discoveries != 1 || len(driver.events) != 0 || !driver.running {
		t.Fatalf("disabled rule edits restarted serving: discovery=%d events=%v", discoveries, driver.events)
	}
	if err := service.Apply(t.Context()); err != nil || !slices.Contains(driver.events, "start") {
		t.Fatalf("explicit Apply must still force reconciliation: %v, %v", driver.events, err)
	}
}

func TestChangedPolicyUsesOneDiscoveryAndOneFinalRuntimeCheck(t *testing.T) {
	service, driver, share := configuredRuntime(t)
	driver.events = nil
	discoveries, inspections := 0, 0
	service.listDatasets = func(context.Context) ([]*gzfs.Dataset, error) {
		discoveries++
		return []*gzfs.Dataset{testDataset("1", "tank/data", "/mnt/data")}, nil
	}
	service.inspectDatasets = func(_ context.Context, names []string) ([]*gzfs.Dataset, error) {
		inspections++
		if !slices.Equal(names, []string{"tank/data"}) {
			t.Errorf("publication inspected unrelated filesystems: %v", names)
		}
		return []*gzfs.Dataset{testDataset("1", "tank/data", "/mnt/data")}, nil
	}
	share.ReadOnly = false
	if _, err := service.SaveShare(t.Context(), share.ID, share.NFSShareConfig); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetStatus(t.Context()); err != nil {
		t.Fatal(err)
	}
	if discoveries != 1 || inspections != 1 || !reflect.DeepEqual(driver.events, []string{"stop", "publish", "publish", "start", "check"}) {
		t.Fatalf("duplicate apply work: discovery=%d inspection=%d events=%v", discoveries, inspections, driver.events)
	}
}

func TestMonitorTargetsAppliedNamesAndResolvesEachCredentialOnce(t *testing.T) {
	service, driver, share := configuredRuntime(t)
	service.resolveMapping = func(context.Context, string, string) (string, error) { return "1001:1002", nil }
	service.KerberosState = func(context.Context, string) (string, error) { return "key", nil }
	if err := service.SetConfig(t.Context(), nfsModels.NFSConfig{Protocol: "v3", KerberosPrincipal: "nfs/server@REALM"}); err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= 64; i++ {
		input := shareInput("1", fmt.Sprintf("192.0.2.%d", i))
		input.MapallUser = "DOMAIN\\user"
		if err := service.DB.Create(&nfsModels.NFSShare{NFSShareConfig: input}).Error; err != nil {
			t.Fatal(err)
		}
	}
	share.MapallUser, share.Security = "DOMAIN\\user", []string{"krb5p"}
	if _, err := service.SaveShare(t.Context(), share.ID, share.NFSShareConfig); err != nil {
		t.Fatal(err)
	}
	service.listDatasets = func(context.Context) ([]*gzfs.Dataset, error) {
		t.Fatal("monitor enumerated host filesystems")
		return nil, nil
	}
	inspections, mappings, keyReads := 0, 0, 0
	service.inspectDatasets = func(_ context.Context, names []string) ([]*gzfs.Dataset, error) {
		inspections++
		if !slices.Equal(names, []string{"tank/data"}) {
			t.Errorf("unexpected health-check targets: %v", names)
		}
		return []*gzfs.Dataset{testDataset("1", "tank/data", "/mnt/data")}, nil
	}
	service.resolveMapping = func(context.Context, string, string) (string, error) { mappings++; return "1001:1002", nil }
	service.KerberosState = func(context.Context, string) (string, error) { keyReads++; return "key", nil }
	driver.events = nil
	if err := service.checkRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if inspections != 1 || mappings != 1 || keyReads != 1 || !reflect.DeepEqual(driver.events, []string{"check"}) {
		t.Fatalf("repeated monitor work: fs=%d NSS=%d keys=%d events=%v", inspections, mappings, keyReads, driver.events)
	}
}

func TestMonitorHostReadsDoNotBlockApplyOrRevokeNewerRuntime(t *testing.T) {
	service, driver, _ := configuredRuntime(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var inspections atomic.Int32
	service.inspectDatasets = func(ctx context.Context, _ []string) ([]*gzfs.Dataset, error) {
		if inspections.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
				return nil, errors.New("obsolete filesystem lookup failed")
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return []*gzfs.Dataset{testDataset("1", "tank/data", "/mnt/data")}, nil
	}
	monitorDone := make(chan error, 1)
	go func() { monitorDone <- service.checkRuntime(t.Context()) }()
	<-entered
	applyDone := make(chan error, 1)
	go func() { applyDone <- service.SetConfig(t.Context(), nfsModels.NFSConfig{Protocol: "v4"}) }()
	select {
	case err := <-applyDone:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(2 * time.Second):
		t.Error("monitor held mutation locks during host discovery")
	}
	close(release)
	if err := <-monitorDone; err != nil {
		t.Fatal(err)
	}
	status, err := service.GetStatus(t.Context())
	if err != nil || !driver.running || status.ApplyStatus != "applied" {
		t.Fatalf("obsolete monitor result withdrew the new runtime: %#v, %v", status, err)
	}
}

func TestConcurrentDisableDuringStartupCannotLeaveServing(t *testing.T) {
	service, driver, _ := configuredRuntime(t)
	driver.onStart = func(ctx context.Context) error {
		return service.DB.WithContext(ctx).Model(&models.BasicSettings{}).Where("1 = 1").Update("services", `[]`).Error
	}
	if err := service.Apply(t.Context()); err != nil {
		t.Fatal(err)
	}
	status, err := service.GetStatus(t.Context())
	if err != nil || driver.running || status.Managed || status.Enabled || status.ApplyStatus != "stopped" {
		t.Fatalf("a newer disable intent was overwritten: %#v, %v", status, err)
	}
}

func TestCancellationAfterSavingRestrictionStillWithdrawsServing(t *testing.T) {
	service, driver, share := configuredRuntime(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := service.DB.Callback().Update().After("gorm:commit_or_rollback_transaction").Register("cancel_saved_nfs_rule", func(tx *gorm.DB) {
		if tx.Statement.Schema.Table == "nfs_shares" {
			cancel()
		}
	}); err != nil {
		t.Fatal(err)
	}
	share.Enabled = false
	if _, err := service.SaveShare(ctx, share.ID, share.NFSShareConfig); !errors.Is(err, ErrApplyFailed) || driver.running {
		t.Fatalf("client cancellation bypassed revocation: running=%t, %v", driver.running, err)
	}
	shares, err := service.GetShares(t.Context())
	if err != nil || shares[0].Enabled {
		t.Fatalf("saved restriction was rolled back: %#v, %v", shares, err)
	}
}

func TestPreviewAndDatasetReadsDoNotWaitForMutationLock(t *testing.T) {
	service, _, _ := configuredRuntime(t)
	service.mutationMu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := service.GetDatasets(t.Context())
		if err == nil {
			_, err = service.PreviewExports(t.Context())
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(2 * time.Second):
		t.Error("read-only discovery waited for runtime mutation lock")
	}
	service.mutationMu.Unlock()
}
