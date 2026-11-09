// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"context"
	"errors"
	"runtime"
	"testing"

	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
)

func TestContainerMetricsUseRacctAndKeepHistoryPerMinute(t *testing.T) {
	service := newTestService(t)
	records := []containerModels.Container{
		{OCID: 7, Name: "web", Image: "docker.io/library/nginx", OS: "linux", Pool: "tank", Stack: "site", RuntimeID: "web", Cores: 2, Memory: 1 << 30, UpdateDigest: "sha256:new"},
		{OCID: 8, Name: "idle", Image: "docker.io/library/redis", OS: "linux", Pool: "tank", RuntimeID: "idle"},
		{OCID: 9, Name: "pending", Image: "docker.io/library/redis", OS: "linux", Pool: "tank", RuntimeID: "pending", CreationPending: true},
	}
	if err := service.DB.Create(&records).Error; err != nil {
		t.Fatal(err)
	}
	previous := runRctl
	t.Cleanup(func() { runRctl = previous })
	runRctl = func(_ context.Context, runtimeID string) ([]byte, error) {
		if runtimeID == "web" {
			return []byte("cputime=45\nmemoryuse=134217728\npcpu=50\n"), nil
		}
		return []byte("cputime=0\nmemoryuse=0\npcpu=0\n"), nil
	}
	historyRows := func() int64 {
		var count int64
		if err := service.DB.Model(&containerModels.Stats{}).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		return count
	}

	if err := service.storeUsage(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	snapshot := service.MetricsSnapshot()
	if !snapshot.Success || len(snapshot.Items) != 2 || historyRows() != 0 {
		t.Fatalf("unexpected snapshot or history between minutes: %+v", snapshot)
	}
	web, idle := snapshot.Items[0], snapshot.Items[1]
	if !web.Running || web.CPUSeconds.Value != 45 || web.MemoryRSS.Value != 128<<20 || web.CPUCores != 2 || web.MemoryLimit != 1<<30 || !web.UpdateAvailable || web.Stack != "site" {
		t.Fatalf("running container: %+v", web)
	}
	if idle.Running || idle.CPUSeconds.Available || idle.MemoryRSS.Available || idle.CPUCores != float64(runtime.NumCPU()) || idle.UpdateAvailable {
		t.Fatalf("stopped container: %+v", idle)
	}

	if err := service.storeUsage(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	var stats []containerModels.Stats
	if err := service.DB.Find(&stats).Error; err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].ContainerID != records[0].ID || stats[0].CPUUsage != 25 || stats[0].MemoryUsage != 12.5 {
		t.Fatalf("history should record only the running container once: %+v", stats)
	}

	lastGood := service.MetricsSnapshot()
	runRctl = func(context.Context, string) ([]byte, error) { return nil, errors.New("RACCT disabled") }
	if err := service.storeUsage(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	failed := service.MetricsSnapshot()
	if failed.Success || len(failed.Items) != 2 || failed.CollectedAt != lastGood.CollectedAt || historyRows() != 1 {
		t.Fatalf("rctl failure did not keep the last good metrics: %+v", failed)
	}
}
