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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

func newTestService(t *testing.T, datasets ...*gzfs.Dataset) *Service {
	t.Helper()
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{}, &nfsModels.NFSSettings{}, &nfsModels.NFSShare{}, &nfsModels.NFSRuntime{})
	if err := db.Create(&models.BasicSettings{Pools: []string{"tank"}}).Error; err != nil {
		t.Fatal(err)
	}
	service := NewService(db)
	service.driver.(*nativeRuntime).available = func() bool { return false }
	service.listDatasets = func(context.Context) ([]*gzfs.Dataset, error) { return datasets, nil }
	service.inspectDatasets = func(ctx context.Context, names []string) ([]*gzfs.Dataset, error) {
		all, err := service.listDatasets(ctx)
		var found []*gzfs.Dataset
		for _, dataset := range all {
			if dataset != nil && slices.Contains(names, dataset.Name) {
				found = append(found, dataset)
			}
		}
		return found, err
	}
	service.verifyMount = func(string, string) error { return nil }
	return service
}

func TestDefaultsAndReadsHaveNoSideEffects(t *testing.T) {
	service := newTestService(t)
	service.listDatasets = func(context.Context) ([]*gzfs.Dataset, error) {
		t.Fatal("reading saved settings should not probe the host")
		return nil, nil
	}
	config, err := service.GetConfig(t.Context())
	if err != nil || config.Protocol != "v3" {
		t.Fatalf("default config %#v: %v", config, err)
	}
	shares, err := service.GetShares(t.Context())
	if err != nil || shares == nil || len(shares) != 0 {
		t.Fatalf("empty shares %#v: %v", shares, err)
	}
	status, err := service.GetStatus(t.Context())
	if err != nil || status.Enabled || status.RuntimeAvailable || status.ApplyStatus != "stopped" || status.Running != nil {
		t.Fatalf("status %#v: %v", status, err)
	}
	preview, err := service.PreviewExports(t.Context())
	if err != nil || preview.Exports != exportsMarker+"\n" {
		t.Fatalf("empty preview %#v: %v", preview, err)
	}
	var count int64
	if err := service.DB.Model(&nfsModels.NFSSettings{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("GET initialized persisted settings: %d, %v", count, err)
	}
}

func TestCanceledReadsDoNotQueryOrWithdrawExports(t *testing.T) {
	for _, test := range []struct {
		name string
		read func(*Service, context.Context) error
	}{
		{"settings", func(s *Service, ctx context.Context) error { _, err := s.GetConfig(ctx); return err }},
		{"shares", func(s *Service, ctx context.Context) error { _, err := s.GetShares(ctx); return err }},
		{"status", func(s *Service, ctx context.Context) error { _, err := s.GetStatus(ctx); return err }},
		{"datasets", func(s *Service, ctx context.Context) error { _, err := s.GetDatasets(ctx); return err }},
		{"preview", func(s *Service, ctx context.Context) error { _, err := s.PreviewExports(ctx); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, driver, _ := configuredRuntime(t)
			driver.events = nil
			queries := 0
			if err := service.DB.Callback().Query().Before("gorm:query").Register("nfs_canceled_query", func(*gorm.DB) { queries++ }); err != nil {
				t.Fatal(err)
			}
			service.listDatasets = func(context.Context) ([]*gzfs.Dataset, error) {
				t.Fatal("canceled read inspected the host")
				return nil, nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := test.read(service, ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled read = %v", err)
			}
			if queries != 0 || len(driver.events) != 0 || !driver.running {
				t.Fatalf("canceled read queried or changed serving state: queries=%d, events=%v, running=%t", queries, driver.events, driver.running)
			}
		})
	}
}

func TestNativeScanPreservesCancellationAlongsideCommandFailure(t *testing.T) {
	service := newTestService(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	commandErr := errors.New("zfs list failed: signal: killed")
	service.listDatasets = func(context.Context) ([]*gzfs.Dataset, error) {
		cancel()
		return nil, commandErr
	}
	_, err := service.GetDatasets(ctx)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, commandErr) {
		t.Fatalf("scan lost cancellation or native failure: %v", err)
	}
}

func TestCanceledDatasetReadSkipsDatabaseAfterWaitingForRuntime(t *testing.T) {
	service, driver, _ := configuredRuntime(t)
	driver.events = nil
	queries := 0
	if err := service.DB.Callback().Query().Before("gorm:query").Register("nfs_canceled_query", func(*gorm.DB) { queries++ }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	service.mutationMu.Lock()
	cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := service.GetDatasets(ctx)
		done <- err
	}()
	<-started
	cancel()
	service.mutationMu.Unlock()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || queries != 0 || len(driver.events) != 0 || !driver.running {
			t.Fatalf("canceled queued read touched database or runtime: error=%v, queries=%d, events=%v", err, queries, driver.events)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled dataset read did not finish")
	}
}

func TestNativeScanReadsEffectiveSharenfsWithoutMutatingZFS(t *testing.T) {
	for _, property := range []string{"off", "on", "-", "", "missing", "error"} {
		t.Run(property, func(t *testing.T) {
			service := newTestService(t)
			driver := service.driver.(*nativeRuntime)
			service.listDatasets = func(ctx context.Context) ([]*gzfs.Dataset, error) { return driver.filesystems(ctx, nil) }
			dataset := testDataset("1", "tank/data", "/mnt/data ")
			service.verifyMount = func(path, name string) error {
				if path != dataset.Mountpoint || name != dataset.Name {
					t.Fatalf("mount identity changed: path=%q name=%q", path, name)
				}
				return nil
			}
			dataset.Properties["guid"] = gzfs.ZFSProperty{Value: "1"}
			dataset.Properties["mountpoint"] = gzfs.ZFSProperty{Value: dataset.Mountpoint}
			dataset.Properties["sharenfs"] = gzfs.ZFSProperty{Value: property, Source: gzfs.ZFSPropertySource{Type: "inherited", Data: "tank"}}
			if property == "missing" {
				delete(dataset.Properties, "sharenfs")
			}
			response, err := json.Marshal(gzfs.DatasetList{Datasets: map[string]*gzfs.Dataset{dataset.Name: dataset}})
			if err != nil {
				t.Fatal(err)
			}
			var commands []string
			driver.run = func(_ context.Context, _ []string, command string, args ...string) (utils.CommandResult, error) {
				commands = append(commands, command+" "+strings.Join(args, " "))
				if property == "error" {
					return utils.CommandResult{}, errors.New("dataset lookup failed")
				}
				return utils.CommandResult{Output: string(response)}, nil
			}
			_, err = service.SaveShare(t.Context(), 0, shareInput("1", "192.0.2.1"))
			if property == "off" && err != nil || property != "off" && err == nil {
				t.Fatalf("sharenfs=%s save error=%v", property, err)
			}
			if len(commands) != 1 || !strings.Contains(commands[0], "-t filesystem") || !strings.Contains(commands[0], ",sharenfs,") || strings.Contains(commands[0], "used,") {
				t.Fatalf("unexpected discovery commands: %#v", commands)
			}
		})
	}
}

func TestShareCRUDAndDisableUnavailableDataset(t *testing.T) {
	service := newTestService(t, testDataset("1", "tank/data", "/mnt/data"))
	share, err := service.SaveShare(t.Context(), 0, shareInput("1", "192.0.2.1"))
	if err != nil || share.ID == 0 || !share.ReadOnly {
		t.Fatalf("save share %#v: %v", share, err)
	}
	service.listDatasets = func(context.Context) ([]*gzfs.Dataset, error) { return nil, errors.New("pool unavailable") }
	if err := service.SetShareEnabled(t.Context(), share.ID, false); err != nil {
		t.Fatalf("unable to disable unavailable share: %v", err)
	}
	share.Enabled = false
	share.Description = "Unavailable but still editable"
	if _, err := service.SaveShare(t.Context(), share.ID, share.NFSShareConfig); err != nil {
		t.Fatalf("unable to edit disabled rule: %v", err)
	}
	if err := service.SetShareEnabled(t.Context(), share.ID, true); err == nil {
		t.Fatal("enabled unavailable filesystem")
	}
	if err := service.DeleteShare(t.Context(), share.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteShare(t.Context(), share.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeat delete error = %v", err)
	}
}

func TestSaveShareConflictsDoNotChangeDesiredState(t *testing.T) {
	service := newTestService(t, testDataset("1", "tank/data", "/mnt/data"))
	first, err := service.SaveShare(t.Context(), 0, shareInput("1", "192.0.2.0/24"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveShare(t.Context(), 0, shareInput("1", "192.0.2.1")); !errors.Is(err, ErrConflict) {
		t.Fatalf("overlap accepted: %v", err)
	}
	secondInput := shareInput("1", "198.51.100.0/24")
	second, err := service.SaveShare(t.Context(), 0, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	secondInput.Clients = []string{"192.0.2.2"}
	if _, err := service.SaveShare(t.Context(), second.ID, secondInput); !errors.Is(err, ErrConflict) {
		t.Fatalf("update overlap accepted: %v", err)
	}
	shares, err := service.GetShares(t.Context())
	if err != nil || len(shares) != 2 || shares[0].ID != first.ID || shares[1].Clients[0] != "198.51.100.0/24" {
		t.Fatalf("failed validation changed saved state: %#v, %v", shares, err)
	}
}

func TestConcurrentShareSavesCannotPublishConflictingRules(t *testing.T) {
	service := newTestService(t, testDataset("1", "tank/data", "/mnt/data"))
	results := make(chan error, 8)
	var requests sync.WaitGroup
	for range cap(results) {
		requests.Go(func() {
			_, err := service.SaveShare(t.Context(), 0, shareInput("1", "192.0.2.1"))
			results <- err
		})
	}
	requests.Wait()
	close(results)
	saved := 0
	for err := range results {
		if err == nil {
			saved++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatalf("unexpected concurrent save error: %v", err)
		}
	}
	if saved != 1 {
		t.Fatalf("saved %d conflicting rules, want one", saved)
	}
}

func TestMountIdentityMismatchFailsClosed(t *testing.T) {
	service := newTestService(t, testDataset("1", "tank/data", "/mnt/data"))
	service.verifyMount = func(string, string) error { return errors.New("mounted parent filesystem") }
	if _, err := service.SaveShare(t.Context(), 0, shareInput("1", "192.0.2.1")); !errors.Is(err, ErrConflict) {
		t.Fatalf("export of parent filesystem accepted: %v", err)
	}
}

func TestDiscoveryRejectsIncompleteAndAmbiguousIdentities(t *testing.T) {
	for _, test := range []struct {
		name     string
		datasets []*gzfs.Dataset
	}{
		{"null dataset", []*gzfs.Dataset{nil}},
		{"missing name", []*gzfs.Dataset{testDataset("1", "", "/mnt/data")}},
		{"missing GUID", []*gzfs.Dataset{testDataset("", "tank/data", "/mnt/data")}},
		{"invalid GUID", []*gzfs.Dataset{testDataset("01", "tank/data", "/mnt/data")}},
		{"unexpected type", []*gzfs.Dataset{{Name: "tank/volume", GUID: "1", Type: gzfs.DatasetTypeVolume}}},
		{"duplicate GUID", []*gzfs.Dataset{
			testDataset("1", "tank/data", "/mnt/data"),
			testDataset("1", "tank/other", "/mnt/other"),
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newTestService(t, test.datasets...)
			service.verifyMount = func(string, string) error {
				t.Fatal("incomplete or ambiguous identities must not reach mount verification")
				return nil
			}
			if options, err := service.GetDatasets(t.Context()); err == nil || options != nil {
				t.Fatalf("unsafe discovery returned selectable datasets: %#v, %v", options, err)
			}
			if _, err := service.SaveShare(t.Context(), 0, shareInput("1", "192.0.2.1")); err == nil {
				t.Fatal("saved a share from incomplete or ambiguous discovery")
			}
			share := nfsModels.NFSShare{NFSShareConfig: shareInput("1", "192.0.2.1")}
			if err := service.DB.Create(&share).Error; err != nil {
				t.Fatal(err)
			}
			if preview, err := service.PreviewExports(t.Context()); err == nil || preview.Exports != "" {
				t.Fatalf("unsafe discovery returned a partial preview: %#v, %v", preview, err)
			}
		})
	}
}

func TestDatasetOptionsRetainUnavailableSavedReferences(t *testing.T) {
	service := newTestService(t, testDataset("1", "tank/data", "/mnt/data"), testDataset("2", "tank/ROOT/default", "/bootenv"))
	if err := service.DB.Create(&nfsModels.NFSShare{NFSShareConfig: shareInput("3", "192.0.2.1")}).Error; err != nil {
		t.Fatal(err)
	}
	options, err := service.GetDatasets(t.Context())
	if err != nil || len(options) != 2 || !options[1].Available || options[0].GUID != "3" || options[0].Reason == "" {
		t.Fatalf("dataset options %#v: %v", options, err)
	}
}

func TestConfirmedDeletionDisablesOnlyMatchingShares(t *testing.T) {
	service := newTestService(t)
	shares := []nfsModels.NFSShare{{NFSShareConfig: shareInput("1", "192.0.2.1")}, {NFSShareConfig: shareInput("2", "192.0.2.1")}}
	if err := service.DB.Create(&shares).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.DisableSharesForDatasets(t.Context(), []string{"1", "1"}); err != nil {
		t.Fatal(err)
	}
	got, err := service.GetShares(t.Context())
	if err != nil || got[0].Enabled || !got[1].Enabled {
		t.Fatalf("reconciled shares %#v: %v", got, err)
	}
}

func TestPreviewExportsGoldenAndFreshMountCheck(t *testing.T) {
	service := newTestService(t, testDataset("1", "tank/data", "/mnt/documents and\\files"))
	if _, err := service.SaveShare(t.Context(), 0, shareInput("1", "2001:db8::/64", "192.0.2.1")); err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewExports(t.Context())
	want := exportsMarker + "\n\n# Share 1\n/mnt/documents\\040and\\134files -sec=sys -ro 192.0.2.1\n/mnt/documents\\040and\\134files -sec=sys -ro -network=2001:db8::/64\n\n"
	if err != nil || preview.Exports != want || len(preview.Warnings) < 2 {
		t.Fatalf("preview = %#v, %v; want %q", preview, err, want)
	}
	service.verifyMount = func(string, string) error { return errors.New("dataset was unmounted after saving") }
	if preview, err := service.PreviewExports(t.Context()); err == nil || preview.Exports != "" {
		t.Fatalf("unsafe partial preview %#v: %v", preview, err)
	}
}

func TestOversizedPolicyCannotPublishAnUnreadableExportsFile(t *testing.T) {
	part := strings.Repeat("a", 250)
	service := newTestService(t, testDataset("1", "tank/data", "/mnt/"+strings.Join([]string{part, part, part, part}, "/")))
	for subnet := range 16 {
		clients := make([]string, 64)
		for host := range clients {
			clients[host] = fmt.Sprintf("10.%d.%d.1", subnet, host)
		}
		share := nfsModels.NFSShare{NFSShareConfig: shareInput("1", clients...)}
		if err := service.DB.Create(&share).Error; err != nil {
			t.Fatal(err)
		}
	}
	if preview, err := service.PreviewExports(t.Context()); !errors.Is(err, ErrConflict) || preview.Exports != "" {
		t.Fatalf("oversized policy accepted: size=%d, %v", len(preview.Exports), err)
	}
}

func TestExportsPath(t *testing.T) {
	for _, test := range []struct{ path, want string }{
		{"/mnt/data-1_name.ext", "/mnt/data-1_name.ext"},
		{"/mnt/share ", `/mnt/share\040`},
		{"/mnt/a b\\c#\"é", `/mnt/a\040b\134c\043\042\303\251`},
	} {
		if got := exportsPath(test.path); got != test.want {
			t.Errorf("exportsPath(%q) = %q, want %q", test.path, got, test.want)
		}
	}
}

func TestMultilineDescriptionIsSavedButNeverRenderedInExports(t *testing.T) {
	service := newTestService(t, testDataset("1", "tank/data", "/mnt/data"))
	input := shareInput("1", "192.0.2.1")
	input.Description = "Documents\n/mnt/other -mapall=root"
	share, err := service.SaveShare(t.Context(), 0, input)
	if err != nil || share.Description != input.Description {
		t.Fatalf("save multiline description: %#v, %v", share, err)
	}
	shares, err := service.GetShares(t.Context())
	if err != nil || len(shares) != 1 || shares[0].Description != input.Description {
		t.Fatalf("read multiline description: %#v, %v", shares, err)
	}
	preview, err := service.PreviewExports(t.Context())
	if err != nil || strings.Contains(preview.Exports, "Documents") || strings.Contains(preview.Exports, "/mnt/other") || !strings.Contains(preview.Exports, "/mnt/data -sec=sys -ro 192.0.2.1") {
		t.Fatalf("description changed exports: %q, %v", preview.Exports, err)
	}
}

func TestRenderExportsOrdersWithoutChangingShares(t *testing.T) {
	datasets := map[string]*gzfs.Dataset{
		"1": testDataset("1", "tank/z", "/mnt/z"),
		"2": testDataset("2", "tank/a", "/mnt/a"),
	}
	shares := []nfsModels.NFSShare{
		{ID: 1, NFSShareConfig: shareInput("1", "192.0.2.1")},
		{ID: 3, NFSShareConfig: shareInput("2", "192.0.2.2")},
		{ID: 2, NFSShareConfig: shareInput("2", "192.0.2.3")},
	}
	original := append([]nfsModels.NFSShare(nil), shares...)
	output := renderExports("v3", shares, datasets, nil)
	if !reflect.DeepEqual(shares, original) {
		t.Fatal("rendering reordered the caller's shares")
	}
	first := strings.Index(output, "# Share 2\n")
	second := strings.Index(output, "# Share 3\n")
	third := strings.Index(output, "# Share 1\n")
	if first < 0 || second <= first || third <= second {
		t.Fatalf("exports are not ordered by mountpoint and share ID: %s", output)
	}
	if reordered := renderExports("v3", []nfsModels.NFSShare{shares[2], shares[0], shares[1]}, datasets, nil); reordered != output {
		t.Fatal("exports depend on the input order")
	}
}

func TestPreviewUsesAutomaticV4RootWithoutExportingOtherFilesystems(t *testing.T) {
	for _, protocol := range []string{"v3", "v4", "v3v4"} {
		t.Run(protocol, func(t *testing.T) {
			service := newTestService(t,
				testDataset("1", "tank", "/tank"),
				testDataset("2", "tank/home", "/home"),
				testDataset("3", "tank/home/hayzam", "/home/hayzam"),
				testDataset("4", "tank/home/hayzam/private", "/home/hayzam/private"),
				testDataset("5", "tank/ROOT/default", "/"),
				testDataset("6", "tank/sylve/jails/private", "/tank/sylve/jails/private"),
			)
			if err := service.SetConfig(t.Context(), nfsModels.NFSConfig{Protocol: protocol}); err != nil {
				t.Fatal(err)
			}
			if _, err := service.SaveShare(t.Context(), 0, shareInput("3", "192.0.2.1")); err != nil {
				t.Fatal(err)
			}
			policy, err := service.readPolicy(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			plan, err := service.buildExportPlan(t.Context(), policy, nil, nil)
			want := exportsMarker + "\n\n"
			if protocol != "v3" {
				want += "V4: / -sec=sys\n\n"
			}
			want += "# Share 1\n/home/hayzam -sec=sys -ro 192.0.2.1\n\n"
			if err != nil || plan.Exports != want {
				t.Fatalf("preview = %#v, %v; want %q", plan.Preview, err, want)
			}
			if !reflect.DeepEqual(plan.filesystems, []nfsModels.ExportedFilesystem{{GUID: "3", Name: "tank/home/hayzam", Mountpoint: "/home/hayzam"}}) {
				t.Fatalf("unconfigured filesystems were added to the export plan: %#v", plan.filesystems)
			}
		})
	}
}

func TestPreviewV4KeepsShareClientScopesIndependent(t *testing.T) {
	service := newTestService(t,
		testDataset("1", "tank/data", "/mnt/data"),
		testDataset("2", "tank/data/child", "/mnt/data/child"),
		testDataset("3", "tank/other", "/home/other"),
	)
	if err := service.SetConfig(t.Context(), nfsModels.NFSConfig{Protocol: "v3v4"}); err != nil {
		t.Fatal(err)
	}
	for _, input := range []nfsModels.NFSShareConfig{
		shareInput("1", "192.0.2.0/24"),
		shareInput("1", "198.51.100.0/24"),
		shareInput("2", "203.0.113.1", "2001:db8::/64"),
		{Dataset: "3", Enabled: true, Everyone: true},
	} {
		if _, err := service.SaveShare(t.Context(), 0, input); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := service.PreviewExports(t.Context())
	want := exportsMarker + "\n\nV4: / -sec=sys\n\n" +
		"# Share 4\n/home/other -sec=sys\n\n" +
		"# Share 1\n/mnt/data -sec=sys -ro -network=192.0.2.0/24\n\n" +
		"# Share 2\n/mnt/data -sec=sys -ro -network=198.51.100.0/24\n\n" +
		"# Share 3\n/mnt/data/child -sec=sys -ro -network=2001:db8::/64\n/mnt/data/child -sec=sys -ro 203.0.113.1\n\n"
	if err != nil || preview.Exports != want {
		t.Fatalf("independent share policies = %#v, %v; want %q", preview, err, want)
	}
}

func TestLegacyV4RootSelectionIsIgnored(t *testing.T) {
	service := newTestService(t, testDataset("1", "tank/data", "/home/data"))
	if err := service.DB.Exec("ALTER TABLE nfs_settings ADD COLUMN v4_root_dataset text").Error; err != nil {
		t.Fatal(err)
	}
	if err := service.DB.Exec("INSERT INTO nfs_settings (id, protocol, v4_root_dataset) VALUES (1, 'v3v4', '999')").Error; err != nil {
		t.Fatal(err)
	}
	if err := service.DB.AutoMigrate(&nfsModels.NFSSettings{}); err != nil {
		t.Fatal(err)
	}
	config, err := service.GetConfig(t.Context())
	if err != nil || config.Protocol != "v3v4" {
		t.Fatalf("legacy settings were not preserved: %#v, %v", config, err)
	}
	if _, err := service.SaveShare(t.Context(), 0, shareInput("1", "192.0.2.1")); err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewExports(t.Context())
	if err != nil || !strings.Contains(preview.Exports, "V4: / -sec=sys\n") {
		t.Fatalf("legacy root selection still affects exports: %#v, %v", preview, err)
	}
	options, err := service.GetDatasets(t.Context())
	if err != nil || len(options) != 1 || options[0].GUID != "1" {
		t.Fatalf("legacy root selection still affects dataset options: %#v, %v", options, err)
	}
	driver := &fakeRuntime{service: service}
	service.driver = driver
	state := nfsModels.NFSRuntime{ID: 1, Managed: true, ApplyStatus: "pending", LastError: "The NFSv4 root filesystem must have an enabled share."}
	if err := service.DB.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	if err := setEnabled(t, service, true); err != nil {
		t.Fatal(err)
	}
	status, err := service.GetStatus(t.Context())
	if err != nil || status.ApplyStatus != "applied" || status.LastError != "" || status.AppliedExports != 1 {
		t.Fatalf("legacy root error still prevents apply: %#v, %v", status, err)
	}
}

func TestEveryoneIsExplicitAndNeverImplicitOnEmptyClients(t *testing.T) {
	service := newTestService(t, testDataset("1", "tank/data", "/mnt/data"))
	input := shareInput("1")
	if _, err := service.SaveShare(t.Context(), 0, input); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty restriction accepted: %v", err)
	}
	input.Everyone = true
	if _, err := service.SaveShare(t.Context(), 0, input); err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewExports(t.Context())
	if err != nil || !strings.Contains(preview.Exports, "/mnt/data -sec=sys -ro\n") {
		t.Fatalf("explicit Everyone preview %#v, %v", preview, err)
	}
	shares, err := service.GetShares(t.Context())
	if err != nil || !reflect.DeepEqual(shares[0].Clients, []string{}) {
		t.Fatalf("empty client array was not persisted: %#v, %v", shares, err)
	}
}
