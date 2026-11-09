// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package metrics

import (
	"cmp"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func gather(t *testing.T, c *collector) map[string]*dto.MetricFamily {
	t.Helper()
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(c)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]*dto.MetricFamily, len(families))
	for _, family := range families {
		result[family.GetName()] = family
	}
	return result
}

// assertValue checks the series of name that carries every label name/value pair.
func assertValue(t *testing.T, families map[string]*dto.MetricFamily, name string, want float64, labels ...string) {
	t.Helper()
	for _, metric := range families[name].GetMetric() {
		values := make(map[string]string)
		for _, pair := range metric.GetLabel() {
			values[pair.GetName()] = pair.GetValue()
		}
		matches := true
		for i := 0; i < len(labels); i += 2 {
			matches = matches && values[labels[i]] == labels[i+1]
		}
		if !matches {
			continue
		}
		got := metric.GetGauge().GetValue()
		if metric.Counter != nil {
			got = metric.GetCounter().GetValue()
		}
		if got != want {
			t.Fatalf("%s%v = %v, want %v", name, labels, got, want)
		}
		return
	}
	t.Fatalf("missing %s%v", name, labels)
}

func TestMetricsAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		token   string
		method  string
		headers []string
		status  int
	}{
		{name: "disabled", token: "secret", headers: []string{"Bearer secret"}, status: 404},
		{name: "empty token allows unauthenticated scrapes", enabled: true, status: 200},
		{name: "post", enabled: true, method: http.MethodPost, status: 405},
		{name: "missing", enabled: true, token: "secret", status: 401},
		{name: "wrong", enabled: true, token: "secret", headers: []string{"Bearer other"}, status: 401},
		{name: "basic", enabled: true, token: "secret", headers: []string{"Basic secret"}, status: 401},
		{name: "duplicate", enabled: true, token: "secret", headers: []string{"Bearer secret", "Bearer secret"}, status: 401},
		{name: "extra field", enabled: true, token: "secret", headers: []string{"Bearer secret extra"}, status: 401},
		{name: "valid", enabled: true, token: "secret", headers: []string{"Bearer secret"}, status: 200},
		{name: "case insensitive scheme", enabled: true, token: "secret", headers: []string{"bearer secret"}, status: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads atomic.Int32
			handler := NewHandler(internal.MetricsConfig{Enabled: tc.enabled, Token: tc.token}, Sources{
				Jails: func() Snapshot[Jail] { reads.Add(1); return Snapshot[Jail]{} },
				VMs:   func() Snapshot[VM] { reads.Add(1); return Snapshot[VM]{} },
				ZFS:   func() ZFSSnapshot { reads.Add(1); return ZFSSnapshot{} },
			})
			req := httptest.NewRequest(cmp.Or(tc.method, http.MethodGet), "/metrics", nil)
			for _, value := range tc.headers {
				req.Header.Add("Authorization", value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
			if tc.status == http.StatusOK {
				if reads.Load() != 3 || !strings.Contains(response.Body.String(), "sylve_collector_success") {
					t.Fatalf("missing metrics: reads=%d body=%s", reads.Load(), response.Body.String())
				}
				if !strings.HasPrefix(response.Header().Get("Content-Type"), "text/plain") || response.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("unexpected headers: %v", response.Header())
				}
			} else if reads.Load() != 0 {
				t.Fatalf("rejected request collected metrics: %d", reads.Load())
			}
			if tc.status == 401 && response.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("missing bearer challenge")
			}
		})
	}
}

func TestMetricsLifecycleAndPartialFailure(t *testing.T) {
	now := time.Now()
	jails := Snapshot[Jail]{CollectedAt: now, LastSuccessAt: now, Success: true, Items: []Jail{
		{CTID: 101, Name: "web\"\\\n", Hostname: "web.local", Running: true, CPUUsageRatio: .25, CPUCores: 2, MemoryRSS: 512 << 20, ResourceLimits: true, MemoryLimit: 1 << 30},
		{CTID: 102, Name: "stopped\xff", CPUCores: 8},
	}}
	vms := Snapshot[VM]{CollectedAt: now, LastSuccessAt: now, Success: true, Items: []VM{
		{RID: 201, Name: "database", State: "running", CPUSeconds: OptionalValue{12.5, true}, MemoryRSS: OptionalValue{1 << 30, true}, MemoryConfigured: 4 << 30, VCPUs: 4, Success: true},
		{RID: 202, Name: "stopped", State: "shutoff", MemoryConfigured: 1 << 30, VCPUs: 1, Success: true},
	}}
	containers := Snapshot[Container]{CollectedAt: now, LastSuccessAt: now, Success: true, Items: []Container{
		{OCID: 301, Name: "web", Image: "docker.io/library/nginx", Pool: "tank", Stack: "site", Running: true,
			CPUSeconds: OptionalValue{45, true}, MemoryRSS: OptionalValue{128 << 20, true}, CPUCores: 2, MemoryLimit: 1 << 30, UpdateAvailable: true},
		{OCID: 302, Name: "idle", Image: "docker.io/library/redis", Pool: "tank", CPUCores: 8},
	}}
	c := &collector{Sources{
		Jails:      func() Snapshot[Jail] { return jails },
		VMs:        func() Snapshot[VM] { return vms },
		Containers: func() Snapshot[Container] { return containers },
	}}
	families := gather(t, c)
	assertValue(t, families, "sylve_jail_info", 1, "name", "web\"\\\n")
	assertValue(t, families, "sylve_jail_info", 1, "name", "stopped�")
	assertValue(t, families, "sylve_jail_cpu_usage_ratio", .25, "ctid", "101")
	assertValue(t, families, "sylve_jail_memory_rss_bytes", 512<<20, "ctid", "101")
	assertValue(t, families, "sylve_jail_running", 0, "ctid", "102")
	if len(families["sylve_jail_memory_limit_bytes"].Metric) != 1 {
		t.Fatal("unlimited jail exported a memory limit")
	}
	assertValue(t, families, "sylve_vm_cpu_seconds_total", 12.5, "rid", "201")
	if families["sylve_vm_cpu_seconds_total"].GetType() != dto.MetricType_COUNTER || len(families["sylve_vm_cpu_seconds_total"].Metric) != 1 {
		t.Fatal("CPU must be a counter emitted only when available")
	}
	assertValue(t, families, "sylve_vm_running", 0, "rid", "202")
	for _, rid := range []string{"201", "202"} {
		states, active := 0, 0
		for _, metric := range families["sylve_vm_state"].Metric {
			if metric.Label[0].GetValue() == rid {
				states++
				active += int(metric.GetGauge().GetValue())
			}
		}
		if states != len(vmStates) || active != 1 {
			t.Fatalf("VM %s state set is not one-hot: states=%d active=%d", rid, states, active)
		}
	}

	assertValue(t, families, "sylve_container_info", 1, "ocid", "301", "image", "docker.io/library/nginx", "stack", "site")
	assertValue(t, families, "sylve_container_cpu_seconds_total", 45, "ocid", "301")
	assertValue(t, families, "sylve_container_image_update_available", 1, "ocid", "301")
	assertValue(t, families, "sylve_container_running", 0, "ocid", "302")
	assertValue(t, families, "sylve_container_cpu_cores", 8, "ocid", "302")
	if len(families["sylve_container_cpu_seconds_total"].Metric) != 1 || len(families["sylve_container_memory_rss_bytes"].Metric) != 1 || len(families["sylve_container_memory_limit_bytes"].Metric) != 1 {
		t.Fatal("stopped or unlimited container exported usage or a limit")
	}

	// Missing RSS must not hide a valid CPU reading or turn it into zero memory.
	vms.Items[0].Success = false
	vms.Items[0].MemoryRSS.Available = false
	families = gather(t, c)
	assertValue(t, families, "sylve_vm_collection_success", 0, "rid", "201")
	assertValue(t, families, "sylve_vm_cpu_seconds_total", 12.5, "rid", "201")
	if families["sylve_vm_memory_rss_bytes"] != nil {
		t.Fatal("unavailable RSS was exported")
	}

	// Expired snapshots and deleted guests must disappear rather than freeze.
	jails.CollectedAt = now.Add(-2 * time.Minute)
	vms.Items = nil
	families = gather(t, c)
	if families["sylve_jail_running"] != nil || families["sylve_vm_running"] != nil {
		t.Fatal("stale/deleted guest metrics were retained")
	}
	assertValue(t, families, "sylve_collector_success", 0, "collector", "jails")
	assertValue(t, families, "sylve_collector_last_success_timestamp_seconds", float64(now.Unix()), "collector", "jails")
}

func TestSnapshotIsolationAndConcurrentScrapes(t *testing.T) {
	var cache Cache[Jail]
	guests := []Jail{{CTID: 101, Name: "original"}}
	cache.Store(guests, true)
	guests[0].Name = "changed"
	snapshot := cache.Snapshot()
	if snapshot.Items[0].Name != "original" {
		t.Fatal("cache retained the caller's slice")
	}
	snapshot.Items[0].Name = "changed"
	if cache.Snapshot().Items[0].Name != "original" {
		t.Fatal("reader mutated the cache")
	}
	cache.Fail()
	failed := cache.Snapshot()
	if failed.Success || len(failed.Items) != 1 || failed.CollectedAt != snapshot.CollectedAt || failed.LastSuccessAt != snapshot.LastSuccessAt {
		t.Fatalf("failed refresh did not keep the last good items: %+v", failed)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				cache.Store([]Jail{{CTID: 101}}, true)
				cache.Snapshot()
			}
		})
	}
	wg.Wait()
}
