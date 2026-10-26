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
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
)

type fakeRuntime struct {
	events  []string
	exports string
	running bool
	fail    string
	onStart func(context.Context) error
	onStop  func(context.Context) error
	service *Service
}

func (d *fakeRuntime) Available() bool { return true }
func (d *fakeRuntime) event(event string) error {
	d.events = append(d.events, event)
	if d.fail == event {
		return errors.New(event + " failed")
	}
	return nil
}
func (d *fakeRuntime) Publish(_ context.Context, exports string) error {
	if err := d.event("publish"); err != nil {
		return err
	}
	d.exports = exports
	return nil
}
func (d *fakeRuntime) Stop(ctx context.Context, state *nfsModels.NFSRuntime) error {
	if err := d.event("stop"); err != nil {
		return err
	}
	if d.onStop != nil {
		if err := d.onStop(ctx); err != nil {
			return err
		}
	}
	d.running = false
	state.NFSd, state.Mountd = nfsModels.ProcessIdentity{}, nfsModels.ProcessIdentity{}
	return nil
}
func (d *fakeRuntime) Start(ctx context.Context, _ nfsModels.NFSConfig, state *nfsModels.NFSRuntime, checkpoint func() error) error {
	// Intent and filesystem identities must survive a process crash before start.
	persisted, err := d.service.runtimeState(context.WithoutCancel(ctx))
	if err != nil || persisted.ApplyStatus != "starting" || !reflect.DeepEqual(persisted.Filesystems, state.Filesystems) {
		return errors.New("start was not preceded by a durable runtime intent")
	}
	d.running = true
	state.NFSd = nfsModels.ProcessIdentity{PID: 123, Birth: "nfsd birth"}
	state.Mountd = nfsModels.ProcessIdentity{PID: 124, Birth: "mountd birth"}
	if err := checkpoint(); err != nil {
		return err
	}
	if d.onStart != nil {
		if err := d.onStart(ctx); err != nil {
			return err
		}
	}
	return d.event("start")
}
func (d *fakeRuntime) Check(context.Context, nfsModels.NFSConfig, *nfsModels.NFSRuntime) error {
	return d.event("check")
}
func (d *fakeRuntime) Running(context.Context, *nfsModels.NFSRuntime) (bool, error) {
	return d.running, nil
}

func setEnabled(t *testing.T, service *Service, enabled bool) error {
	t.Helper()
	services := []models.AvailableService{}
	if enabled {
		services = append(services, models.NFS)
	}
	if err := service.DB.WithContext(t.Context()).Model(&models.BasicSettings{ID: 1}).Select("Services").Updates(&models.BasicSettings{Services: services}).Error; err != nil {
		return err
	}
	return service.Reconcile(t.Context())
}

func configuredRuntime(t *testing.T) (*Service, *fakeRuntime, nfsModels.NFSShare) {
	t.Helper()
	service := newTestService(t, testDataset("1", "tank/data", "/mnt/data"))
	driver := &fakeRuntime{service: service}
	service.driver = driver
	share, err := service.SaveShare(t.Context(), 0, shareInput("1", "192.0.2.1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(driver.events) != 0 {
		t.Fatalf("saving disabled NFS configuration touched the host: %v", driver.events)
	}
	if err := setEnabled(t, service, true); err != nil {
		t.Fatal(err)
	}
	return service, driver, share
}

func TestEnableAppliesAndDisableReleasesControl(t *testing.T) {
	service, driver, _ := configuredRuntime(t)
	want := []string{"stop", "publish", "publish", "start", "check"}
	if !reflect.DeepEqual(driver.events, want) || !driver.running || !strings.Contains(driver.exports, "192.0.2.1") {
		t.Fatalf("apply events=%v running=%t exports=%q", driver.events, driver.running, driver.exports)
	}
	status, err := service.GetStatus(t.Context())
	if err != nil || !status.Managed || !status.Enabled || status.ApplyStatus != "applied" || status.AppliedExports != 1 || status.Running == nil || !*status.Running || status.PolicyVerified {
		t.Fatalf("runtime status = %#v, %v", status, err)
	}
	if err := setEnabled(t, service, false); err != nil {
		t.Fatal(err)
	}
	if driver.running || driver.exports != exportsMarker+"\n" {
		t.Fatal("disable left serving state or republished original exports")
	}
	shares, err := service.GetShares(t.Context())
	if err != nil || len(shares) != 1 || !shares[0].Enabled {
		t.Fatalf("global disable changed per-share intent: %#v, %v", shares, err)
	}
	state, err := service.runtimeState(t.Context())
	if err != nil || state.Managed || state.ApplyStatus != "stopped" || len(state.Filesystems) != 0 || state.NFSd.PID != 0 || state.Mountd.PID != 0 {
		t.Fatalf("disabled runtime = %#v, %v", state, err)
	}
}

func TestApplyFailuresRevokeInsteadOfRestoringOlderAccess(t *testing.T) {
	for _, stage := range []string{"start", "check"} {
		t.Run(stage, func(t *testing.T) {
			service, driver, share := configuredRuntime(t)
			driver.fail = stage
			share.Clients = []string{"198.51.100.1"}
			if _, err := service.SaveShare(t.Context(), share.ID, share.NFSShareConfig); !errors.Is(err, ErrApplyFailed) {
				t.Fatalf("apply failure was not reported as saved/pending: %v", err)
			}
			if driver.running || driver.exports != exportsMarker+"\n" {
				t.Fatal("failed policy update restored or retained previous access")
			}
			shares, err := service.GetShares(t.Context())
			if err != nil || !reflect.DeepEqual(shares[0].Clients, share.Clients) {
				t.Fatalf("new restrictions were rolled back: %#v, %v", shares, err)
			}
			state, err := service.runtimeState(t.Context())
			if err != nil || state.ApplyStatus != "pending" || !strings.Contains(state.LastError, stage+" failed") || len(state.Filesystems) != 0 {
				t.Fatalf("pending state = %#v, %v", state, err)
			}
			driver.fail = ""
			if err := service.checkRuntime(t.Context()); err != nil || driver.running {
				t.Fatal("monitor automatically restarted a failed policy")
			}
			if err := service.Apply(t.Context()); err != nil || !driver.running || strings.Contains(driver.exports, "192.0.2.1") {
				t.Fatalf("explicit retry failed: %v", err)
			}
		})
	}
}

func TestPublicationFailureStopsServingWithoutRestoringOlderAccess(t *testing.T) {
	service, driver, _ := configuredRuntime(t)
	driver.fail = "publish"
	if err := service.Apply(t.Context()); !errors.Is(err, ErrApplyFailed) || driver.running {
		t.Fatalf("failed exports write retained serving: %t, %v", driver.running, err)
	}
	state, err := service.runtimeState(t.Context())
	if err != nil || !state.Managed || state.ApplyStatus != "pending" || !strings.Contains(state.LastError, "publish failed") {
		t.Fatalf("failed publication lost pending state: %#v, %v", state, err)
	}
}

func TestFailedDisableRetainsDisabledIntentAndCanBeRetried(t *testing.T) {
	service, driver, _ := configuredRuntime(t)
	driver.fail = "stop"
	if err := setEnabled(t, service, false); !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("stop failure = %v", err)
	}
	status, err := service.GetStatus(t.Context())
	if err != nil || status.Enabled || status.ApplyStatus != "pending" || status.Running == nil || !*status.Running || status.AppliedExports != 0 {
		t.Fatalf("failed stop was reported as stopped or enabled: %#v, %v", status, err)
	}
	driver.fail = ""
	if err := setEnabled(t, service, false); err != nil || driver.running {
		t.Fatalf("same-state disable did not retry revocation: %v", err)
	}
}

func TestMonitorRetriesFailedWithdrawalWithoutStartingServing(t *testing.T) {
	service, driver, _ := configuredRuntime(t)
	driver.fail = "stop"
	if err := setEnabled(t, service, false); !errors.Is(err, ErrApplyFailed) {
		t.Fatal(err)
	}
	driver.fail, driver.events = "", nil
	if err := service.checkRuntime(t.Context()); err != nil || driver.running || slices.Contains(driver.events, "start") {
		t.Fatalf("pending withdrawal was not retried safely: %v, %v", driver.events, err)
	}
}

func TestMonitorReconcilesOutOfBandServiceDisable(t *testing.T) {
	service, driver, _ := configuredRuntime(t)
	if err := service.DB.Model(&models.BasicSettings{}).Where("1 = 1").Update("services", `[]`).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.checkRuntime(t.Context()); err != nil || driver.running || driver.exports != exportsMarker+"\n" {
		t.Fatalf("disabled intent retained active runtime: %v", err)
	}
}

func TestCancelledApplyStillRevokesAndPersistsFailure(t *testing.T) {
	service, driver, _ := configuredRuntime(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	driver.onStart = func(context.Context) error { cancel(); return context.Canceled }
	driver.onStop = func(ctx context.Context) error { return ctx.Err() }
	if err := service.Apply(ctx); !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("cancelled apply = %v", err)
	}
	if driver.running || driver.exports != exportsMarker+"\n" {
		t.Fatal("request cancellation cancelled safety cleanup")
	}
	state, err := service.runtimeState(t.Context())
	if err != nil || state.ApplyStatus != "pending" || state.LastError == "" {
		t.Fatalf("cancelled apply lost failure metadata: %#v, %v", state, err)
	}
}

func TestTopologyDriftWithdrawsExportsAndRequiresExplicitRetry(t *testing.T) {
	service, driver, _ := configuredRuntime(t)
	service.listDatasets = func(context.Context) ([]*gzfs.Dataset, error) {
		return []*gzfs.Dataset{testDataset("1", "tank/data", "/mnt/new-path")}, nil
	}
	if err := service.checkRuntime(t.Context()); !errors.Is(err, ErrApplyFailed) || driver.running || driver.exports != exportsMarker+"\n" {
		t.Fatalf("topology drift was not withdrawn: %v", err)
	}
	if err := service.Apply(t.Context()); err != nil || !strings.Contains(driver.exports, "/mnt/new-path") {
		t.Fatalf("explicit topology retry = %v", err)
	}
}

func TestSavedListenerSettingsDriftCannotRemainApplied(t *testing.T) {
	service, driver, _ := configuredRuntime(t)
	settings := nfsModels.NFSSettings{ID: 1, NFSConfig: nfsModels.NFSConfig{Protocol: "v3", BindAddresses: []string{"127.0.0.1"}}}
	if err := service.DB.Save(&settings).Error; err != nil {
		t.Fatal(err)
	}
	status, err := service.GetStatus(t.Context())
	if err != nil || status.ApplyStatus != "pending" || status.AppliedExports != 0 {
		t.Fatalf("unapplied listener settings reported as applied: %#v, %v", status, err)
	}
	if err := service.checkRuntime(t.Context()); !errors.Is(err, ErrApplyFailed) || driver.running {
		t.Fatalf("listener drift was not withdrawn: %v", err)
	}
}

func TestStorageFenceRequiresRevocationAndSerializesEnable(t *testing.T) {
	service, _, _ := configuredRuntime(t)
	if release, err := service.GuardStorageMutation(t.Context()); !errors.Is(err, ErrConflict) || release != nil {
		t.Fatalf("allowed mutation with active exports: %v", err)
	}
	if err := setEnabled(t, service, false); err != nil {
		t.Fatal(err)
	}
	release, err := service.GuardStorageMutation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if nested, err := service.GuardStorageMutation(t.Context()); err == nil || nested != nil {
		t.Fatal("nested storage fence should return a conflict, not wait")
	}
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		done <- setEnabled(t, service, true)
	}()
	<-started
	select {
	case err := <-done:
		release()
		t.Fatalf("NFS enable crossed an active storage mutation fence: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestStartupReconcilesEnabledRuntime(t *testing.T) {
	service, driver, _ := configuredRuntime(t)
	driver.events = nil
	if err := service.Apply(t.Context()); err != nil || len(driver.events) == 0 || driver.events[0] != "stop" {
		t.Fatalf("owned startup did not reconcile: %v, %v", driver.events, err)
	}
	var basic models.BasicSettings
	if err := service.DB.First(&basic).Error; err != nil || !slices.Contains(basic.Services, models.NFS) {
		t.Fatalf("startup lost desired enablement: %#v, %v", basic, err)
	}
}

func TestDisabledNFSLeavesManualConfigurationAndServicesAlone(t *testing.T) {
	for _, previouslyEnabled := range []bool{false, true} {
		t.Run(fmt.Sprint(previouslyEnabled), func(t *testing.T) {
			service := newTestService(t, testDataset("1", "tank/data", "/mnt/data"))
			driver := &fakeRuntime{service: service}
			service.driver = driver
			if previouslyEnabled {
				if err := setEnabled(t, service, true); err != nil {
					t.Fatal(err)
				}
				if err := setEnabled(t, service, false); err != nil {
					t.Fatal(err)
				}
			}
			driver.events, driver.exports, driver.running = nil, "manual exports", true
			if err := service.Apply(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := setEnabled(t, service, false); err != nil {
				t.Fatal(err)
			}
			if err := service.SetConfig(t.Context(), nfsModels.NFSConfig{Protocol: "v3"}); err != nil {
				t.Fatal(err)
			}
			if _, err := service.SaveShare(t.Context(), 0, shareInput("1", "192.0.2.1")); err != nil {
				t.Fatal(err)
			}
			if err := service.checkRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			status, err := service.GetStatus(t.Context())
			if err != nil || status.Managed || status.Running != nil {
				t.Fatalf("manual runtime was claimed: %#v, %v", status, err)
			}
			if len(driver.events) != 0 || !driver.running || driver.exports != "manual exports" {
				t.Fatalf("disabled Sylve NFS changed manual runtime: %v, %q, %t", driver.events, driver.exports, driver.running)
			}
			if err := setEnabled(t, service, true); err != nil || !driver.running || driver.exports == "manual exports" {
				t.Fatalf("enabling NFS did not take full control: %v", err)
			}
		})
	}
}

func TestEmptyPolicyDoesNotExportV4Root(t *testing.T) {
	for _, protocol := range []string{"v3", "v4", "v3v4"} {
		t.Run(protocol, func(t *testing.T) {
			service := newTestService(t)
			driver := &fakeRuntime{service: service}
			service.driver = driver
			service.listDatasets = func(context.Context) ([]*gzfs.Dataset, error) {
				t.Fatal("empty policy must not discover filesystems")
				return nil, nil
			}
			if err := service.SetConfig(t.Context(), nfsModels.NFSConfig{Protocol: protocol}); err != nil {
				t.Fatal(err)
			}
			if err := setEnabled(t, service, true); err != nil {
				t.Fatal(err)
			}
			status, err := service.GetStatus(t.Context())
			if err != nil || status.ApplyStatus != "idle" || status.AppliedExports != 0 || driver.running || driver.exports != exportsMarker+"\n" || slices.Contains(driver.events, "start") {
				t.Fatalf("empty policy started serving: %#v, exports=%q events=%v, %v", status, driver.exports, driver.events, err)
			}
		})
	}
}

func TestAutomaticV4RootAppliesWithMultipleClientNetworks(t *testing.T) {
	for _, protocol := range []string{"v4", "v3v4"} {
		t.Run(protocol, func(t *testing.T) {
			service := newTestService(t, testDataset("1", "tank/data", "/home/data"))
			driver := &fakeRuntime{service: service}
			service.driver = driver
			if err := service.SetConfig(t.Context(), nfsModels.NFSConfig{Protocol: protocol}); err != nil {
				t.Fatal(err)
			}
			if _, err := service.SaveShare(t.Context(), 0, shareInput("1", "192.0.2.0/24", "198.51.100.0/24")); err != nil {
				t.Fatal(err)
			}
			if err := setEnabled(t, service, true); err != nil {
				t.Fatal(err)
			}
			want := exportsMarker + "\n\nV4: / -sec=sys\n\n# Share 1\n" +
				"/home/data -sec=sys -ro -network=192.0.2.0/24\n" +
				"/home/data -sec=sys -ro -network=198.51.100.0/24\n\n"
			if !driver.running || driver.exports != want {
				t.Fatalf("automatic root apply: running=%t exports=%q; want %q", driver.running, driver.exports, want)
			}
			status, err := service.GetStatus(t.Context())
			if err != nil || status.ApplyStatus != "applied" || status.AppliedExports != 1 {
				t.Fatalf("automatic root runtime status = %#v, %v", status, err)
			}
		})
	}
}
