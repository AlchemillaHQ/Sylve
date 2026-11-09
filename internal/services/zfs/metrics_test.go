// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	gzfstest "github.com/alchemillahq/gzfs/testutil"
	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
)

const metricsDatasetCommand = " -r -t filesystem,volume managed -j"

func newZFSMetricsTestService(t *testing.T) (*Service, *gzfstest.MockRunner) {
	t.Helper()
	previous := config.ParsedConfig
	config.ParsedConfig = &internal.SylveConfig{Metrics: internal.MetricsConfig{Enabled: true, Token: "secret"}}
	interval := datasetMetricsInterval
	datasetMetricsInterval = 0
	t.Cleanup(func() { config.ParsedConfig, datasetMetricsInterval = previous, interval })
	service, _ := newManagedPoolDashboardTestService(t)
	runner := gzfstest.NewMockRunner()
	service.GZFS = gzfs.NewClient(gzfs.Options{Runner: runner})
	runner.AddCommand(metricsDatasetCommand, `{"datasets":{}}`, "", nil)
	service.listHostPools = func(context.Context) ([]*gzfs.ZPool, error) {
		return []*gzfs.ZPool{
			{Name: "managed", PoolGUID: "managed-guid", State: gzfs.ZPoolStateDegraded, Properties: map[string]gzfs.ZFSProperty{
				"size": {Value: "10000"}, "allocated": {Value: "2000"}, "free": {Value: "8000"},
				"fragmentation": {Value: "25%"}, "dedupratio": {Value: "1.50x"},
			}},
			{Name: "unmanaged", PoolGUID: "unmanaged-guid"},
		}, nil
	}
	service.readARCStats = func(now time.Time) (infoModels.ZFSARCHistorical, error) {
		return infoModels.ZFSARCHistorical{Size: 1000, TargetSize: 2000, MinSize: 500, MaxSize: 4000, Hits: 90, Misses: 10, CreatedAt: now}, nil
	}
	return service, runner
}

func TestZFSMetricsUseManagedPoolsAndFreshDatasetMeasurements(t *testing.T) {
	service, runner := newZFSMetricsTestService(t)
	filesystem := &gzfs.Dataset{Name: "managed/fs", Type: gzfs.DatasetTypeFilesystem, Properties: map[string]gzfs.ZFSProperty{
		"guid": {Value: "fs-guid"}, "used": {Value: "100"}, "available": {Value: "8000"},
		"referenced": {Value: "75"}, "logicalused": {Value: "200"}, "quota": {Value: "1000"}, "compressratio": {Value: "2.00x"},
	}}
	volume := &gzfs.Dataset{Name: "managed/vol", Type: gzfs.DatasetTypeVolume, Properties: map[string]gzfs.ZFSProperty{
		"guid": {Value: "vol-guid"}, "used": {Value: "0"}, "available": {Value: "8000"},
		"referenced": {Value: "0"}, "logicalused": {Value: "-"}, "quota": {Value: "-"}, "volsize": {Value: "4096"},
	}}
	runner.AddCommand(metricsDatasetCommand, snapshotIdentityResponse(t, filesystem, volume,
		&gzfs.Dataset{Name: "managed/sylve/containers/images", Type: gzfs.DatasetTypeFilesystem},
		&gzfs.Dataset{Name: "managed/sylve/containers/images/3f9a0c", Type: gzfs.DatasetTypeFilesystem},
		&gzfs.Dataset{Name: "unmanaged/fs", Type: gzfs.DatasetTypeFilesystem},
		&gzfs.Dataset{Name: "managed/fs@snapshot", Type: gzfs.DatasetTypeSnapshot},
	), "", nil)
	service.poolIOStats["managed"] = poolIOStat{
		ReadIOPS: 12, WriteIOPS: 34, ReadBytesPerSecond: 1024, WriteBytesPerSecond: 2048,
		ReadLatencyNanos: 20000, ReadLatencyAvailable: true, SampledAt: time.Now(),
	}
	service.StoreStats()
	snapshot := service.MetricsSnapshot()
	if !snapshot.Pools.Success || !snapshot.Datasets.Success || !snapshot.ARC.Success || len(snapshot.Pools.Items) != 1 || len(snapshot.Datasets.Items) != 3 || len(snapshot.ARC.Items) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if images := snapshot.Datasets.Items[1].Name; images != "managed/sylve/containers/images" {
		t.Fatalf("container images dataset = %q, want it kept and its layers skipped", images)
	}
	pool := snapshot.Pools.Items[0]
	if pool.Name != "managed" || pool.Health != "DEGRADED" || pool.Size.Value != 10000 || !pool.Size.Available || pool.FragmentationRatio.Value != .25 || pool.DedupRatio.Value != 1.5 || pool.ReadLatencySeconds.Value != .00002 || pool.WriteLatencySeconds.Available || !pool.IOExpiresAt.After(time.Now()) {
		t.Fatalf("pool measurements: %+v", pool)
	}
	fs, vol := snapshot.Datasets.Items[0], snapshot.Datasets.Items[2]
	if fs.GUID != "fs-guid" || fs.Type != "filesystem" || fs.Used.Value != 100 || fs.Quota.Value != 1000 || !fs.Quota.Available || fs.CompressionRatio.Value != 2 || fs.VolumeSize.Available {
		t.Fatalf("filesystem measurements: %+v", fs)
	}
	if vol.Type != "volume" || vol.VolumeSize.Value != 4096 || !vol.VolumeSize.Available || vol.LogicalUsed.Available || vol.Quota.Available || !vol.Used.Available || vol.Used.Value != 0 {
		t.Fatalf("volume measurements: %+v", vol)
	}
	if snapshot.ARC.Items[0].Hits != 90 || snapshot.ARC.Items[0].Size != 1000 {
		t.Fatalf("ARC measurements: %+v", snapshot.ARC)
	}

	// A later collection must see changed space and remove deleted datasets.
	filesystem.Properties["used"] = gzfs.ZFSProperty{Value: "150"}
	filesystem.Properties["quota"] = gzfs.ZFSProperty{Value: "0"}
	runner.AddCommand(metricsDatasetCommand, snapshotIdentityResponse(t, filesystem), "", nil)
	service.poolIOStats["managed"] = poolIOStat{ReadIOPS: 99, SampledAt: time.Now().Add(-time.Minute)}
	service.StoreStats()
	snapshot = service.MetricsSnapshot()
	if len(snapshot.Datasets.Items) != 1 || snapshot.Datasets.Items[0].Used.Value != 150 || snapshot.Datasets.Items[0].Quota.Available || !snapshot.Pools.Items[0].IOExpiresAt.IsZero() {
		t.Fatalf("old dataset or I/O measurements survived: %+v", snapshot)
	}
}

func TestZFSMetricsFailuresAreIndependent(t *testing.T) {
	service, runner := newZFSMetricsTestService(t)
	service.StoreStats()
	previous := service.MetricsSnapshot()

	// History persistence failures must not hide successfully sampled metrics.
	if err := service.TelemetryDB.Migrator().DropTable(&infoModels.ZPoolHistorical{}); err != nil {
		t.Fatal(err)
	}
	runner.AddCommand(metricsDatasetCommand, "", "", errors.New("dataset listing failed"))
	service.StoreStats()
	snapshot := service.MetricsSnapshot()
	if !snapshot.Pools.Success || !snapshot.ARC.Success || snapshot.Datasets.Success || len(snapshot.Datasets.Items) != 0 || snapshot.Datasets.LastSuccessAt != previous.Datasets.LastSuccessAt {
		t.Fatalf("dataset failure affected other collectors: %+v", snapshot)
	}
	previous = snapshot
	service.listHostPools = func(context.Context) ([]*gzfs.ZPool, error) { return nil, errors.New("pool listing failed") }
	service.readARCStats = func(time.Time) (infoModels.ZFSARCHistorical, error) {
		return infoModels.ZFSARCHistorical{}, errors.New("ARC unavailable")
	}
	service.StoreStats()
	snapshot = service.MetricsSnapshot()
	if snapshot.Pools.Success || snapshot.ARC.Success || snapshot.Datasets.Success || len(snapshot.Pools.Items) != 1 || len(snapshot.ARC.Items) != 1 ||
		snapshot.Pools.CollectedAt != previous.Pools.CollectedAt || snapshot.ARC.CollectedAt != previous.ARC.CollectedAt ||
		snapshot.Pools.LastSuccessAt != previous.Pools.LastSuccessAt || snapshot.ARC.LastSuccessAt != previous.ARC.LastSuccessAt {
		t.Fatalf("failed collectors did not keep the last good measurements: %+v", snapshot)
	}
	service.listHostPools = func(context.Context) ([]*gzfs.ZPool, error) { return nil, nil }
	service.StoreStats()
	snapshot = service.MetricsSnapshot()
	if !snapshot.Pools.Success || !snapshot.Datasets.Success || len(snapshot.Pools.Items) != 0 || len(snapshot.Datasets.Items) != 0 {
		t.Fatalf("empty inventory should be a successful collection: %+v", snapshot)
	}
}

func TestZFSDatasetMetricsAreThrottled(t *testing.T) {
	service, runner := newZFSMetricsTestService(t)
	datasetMetricsInterval = time.Minute
	service.StoreStats()
	service.StoreStats()
	if len(runner.CallHistory) != 1 {
		t.Fatalf("dataset listings = %d, want 1 within the polling interval", len(runner.CallHistory))
	}
}

func TestDisabledMetricsDoNotPollDatasets(t *testing.T) {
	service, runner := newZFSMetricsTestService(t)
	config.ParsedConfig.Metrics.Enabled = false
	service.StoreStats()
	if len(runner.CallHistory) != 0 || !service.MetricsSnapshot().Datasets.CollectedAt.IsZero() {
		t.Fatal("disabled metrics performed dataset collection")
	}
}

func TestZFSMetricsUnavailablePropertiesAreNotZeroMeasurements(t *testing.T) {
	pool := poolMetricsSample(&gzfs.ZPool{Properties: map[string]gzfs.ZFSProperty{
		"size": {Value: "-"}, "free": {Value: "invalid"}, "allocated": {Value: "0"},
		"fragmentation": {Value: "-"}, "dedupratio": {Value: "NaN"},
	}}, poolIOStat{})
	if pool.Size.Available || pool.Free.Available || pool.FragmentationRatio.Available || pool.DedupRatio.Available || !pool.Allocated.Available || pool.Allocated.Value != 0 {
		t.Fatalf("unavailable and zero values were conflated: %+v", pool)
	}
}
