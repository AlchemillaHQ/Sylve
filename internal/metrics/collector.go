// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package metrics

import (
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Service monitors refresh more frequently than this expiry window.
const maxSnapshotAge = time.Minute

type metric struct {
	desc *prometheus.Desc
	kind prometheus.ValueType
}

func gauge(name, help string, labels ...string) metric {
	return metric{prometheus.NewDesc("sylve_"+name, help, labels, nil), prometheus.GaugeValue}
}

func counter(name, help string, labels ...string) metric {
	return metric{prometheus.NewDesc("sylve_"+name, help, labels, nil), prometheus.CounterValue}
}

// Invalid UTF-8 would panic in a registry goroutine and take down the daemon.
func (m metric) emit(ch chan<- prometheus.Metric, value float64, labels ...string) {
	for i, label := range labels {
		labels[i] = strings.ToValidUTF8(label, "�")
	}
	ch <- prometheus.MustNewConstMetric(m.desc, m.kind, value, labels...)
}

func (m metric) emitOptional(ch chan<- prometheus.Metric, value OptionalValue, labels ...string) {
	if value.Available {
		m.emit(ch, value.Value, labels...)
	}
}

var (
	jailInfo             = gauge("jail_info", "Information about a local Sylve-managed jail.", "ctid", "name", "hostname")
	jailRunning          = gauge("jail_running", "Whether the jail is running.", "ctid")
	jailCPU              = gauge("jail_cpu_usage_ratio", "Summed ps CPU utilization including descendants, normalized to available cores and capped at 1.", "ctid")
	jailCores            = gauge("jail_cpu_cores", "CPU cores used to normalize jail CPU utilization: configured affinity or host logical cores.", "ctid")
	jailMemory           = gauge("jail_memory_rss_bytes", "Summed process RSS including descendant jails; shared pages may be counted more than once.", "ctid")
	jailLimits           = gauge("jail_resource_limits_enabled", "Whether resource limits are configured for the jail.", "ctid")
	jailMemoryLimit      = gauge("jail_memory_limit_bytes", "Configured jail memory limit in bytes; omitted when unlimited.", "ctid")
	vmInfo               = gauge("vm_info", "Information about a local Sylve-managed VM.", "rid", "name")
	vmRunning            = gauge("vm_running", "Whether libvirt reports the VM as running.", "rid")
	vmState              = gauge("vm_state", "VM state reported by libvirt (one-hot).", "rid", "state")
	vmCPU                = counter("vm_cpu_seconds_total", "Cumulative VM CPU time in seconds; resets on guest restart.", "rid")
	vmMemory             = gauge("vm_memory_rss_bytes", "Host bhyve process RSS in bytes, not guest application memory usage.", "rid")
	vmMemoryConfigured   = gauge("vm_memory_configured_bytes", "VM memory configured in Sylve, in bytes.", "rid")
	vmVCPUs              = gauge("vm_vcpus", "VM vCPUs configured in Sylve.", "rid")
	vmSuccess            = gauge("vm_collection_success", "Whether the latest VM state and supported usage measurements were collected successfully.", "rid")
	containerInfo        = gauge("container_info", "Information about a local Sylve-managed container.", "ocid", "name", "image", "pool", "stack")
	containerRunning     = gauge("container_running", "Whether the container is running.", "ocid")
	containerCPU         = counter("container_cpu_seconds_total", "Cumulative CPU time of the container's jail from RACCT, in whole seconds; can reset when the container restarts.", "ocid")
	containerCores       = gauge("container_cpu_cores", "CPU cores available to the container: its configured limit or host logical cores.", "ocid")
	containerMemory      = gauge("container_memory_rss_bytes", "Resident memory of the container's jail from RACCT, in bytes.", "ocid")
	containerMemoryLimit = gauge("container_memory_limit_bytes", "Configured container memory limit in bytes; omitted when unlimited.", "ocid")
	containerUpdate      = gauge("container_image_update_available", "Whether the registry has a newer digest for the container's image tag.", "ocid")
	collectorSuccess     = gauge("collector_success", "Whether the latest collection succeeded and is less than 60 seconds old; 0 before monitoring starts.", "collector")
	collectorLastSuccess = gauge("collector_last_success_timestamp_seconds", "Unix timestamp of the last fully successful collection; 0 before the first success.", "collector")
)

var descriptors = []metric{
	jailInfo, jailRunning, jailCPU, jailCores, jailMemory, jailLimits, jailMemoryLimit,
	vmInfo, vmRunning, vmState, vmCPU, vmMemory, vmMemoryConfigured, vmVCPUs, vmSuccess,
	containerInfo, containerRunning, containerCPU, containerCores, containerMemory, containerMemoryLimit, containerUpdate,
	collectorSuccess, collectorLastSuccess,
}

var vmStates = []string{"nostate", "running", "blocked", "paused", "shutdown", "shutoff", "crashed", "pmsuspended"}

// VMStateName maps libvirt's domain state values; unknown states are omitted.
func VMStateName(state int) string {
	if state < 0 || state >= len(vmStates) {
		return ""
	}
	return vmStates[state]
}

// Sources supplies the latest snapshots; a nil source reports as never collected.
type Sources struct {
	Jails      func() Snapshot[Jail]
	VMs        func() Snapshot[VM]
	Containers func() Snapshot[Container]
	ZFS        func() ZFSSnapshot
}

func read[T any](source func() T) T {
	var zero T
	if source == nil {
		return zero
	}
	return source()
}

type collector struct{ Sources }

func (c *collector) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range descriptors {
		ch <- m.desc
	}
	for _, m := range zfsDescriptors {
		ch <- m.desc
	}
}

func boolValue(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func collectStatus[T any](ch chan<- prometheus.Metric, name string, snapshot Snapshot[T], now time.Time) bool {
	fresh := !snapshot.CollectedAt.IsZero() && now.Sub(snapshot.CollectedAt) <= maxSnapshotAge
	collectorSuccess.emit(ch, boolValue(fresh && snapshot.Success), name)
	lastSuccess := float64(0)
	if !snapshot.LastSuccessAt.IsZero() {
		lastSuccess = float64(snapshot.LastSuccessAt.Unix())
	}
	collectorLastSuccess.emit(ch, lastSuccess, name)
	return fresh
}

func (c *collector) Collect(ch chan<- prometheus.Metric) {
	now := time.Now()
	jails := read(c.Jails)
	if collectStatus(ch, "jails", jails, now) {
		for _, j := range jails.Items {
			id := strconv.FormatUint(uint64(j.CTID), 10)
			jailInfo.emit(ch, 1, id, j.Name, j.Hostname)
			jailRunning.emit(ch, boolValue(j.Running), id)
			jailCPU.emit(ch, j.CPUUsageRatio, id)
			jailCores.emit(ch, j.CPUCores, id)
			jailMemory.emit(ch, float64(j.MemoryRSS), id)
			jailLimits.emit(ch, boolValue(j.ResourceLimits), id)
			if j.ResourceLimits && j.MemoryLimit > 0 {
				jailMemoryLimit.emit(ch, float64(j.MemoryLimit), id)
			}
		}
	}
	vms := read(c.VMs)
	if collectStatus(ch, "vms", vms, now) {
		for _, vm := range vms.Items {
			id := strconv.FormatUint(uint64(vm.RID), 10)
			vmInfo.emit(ch, 1, id, vm.Name)
			vmMemoryConfigured.emit(ch, float64(vm.MemoryConfigured), id)
			vmVCPUs.emit(ch, float64(vm.VCPUs), id)
			vmSuccess.emit(ch, boolValue(vm.Success), id)
			if vm.State != "" {
				vmRunning.emit(ch, boolValue(vm.State == "running"), id)
				for _, state := range vmStates {
					vmState.emit(ch, boolValue(state == vm.State), id, state)
				}
			}
			vmCPU.emitOptional(ch, vm.CPUSeconds, id)
			vmMemory.emitOptional(ch, vm.MemoryRSS, id)
		}
	}
	containers := read(c.Containers)
	if collectStatus(ch, "containers", containers, now) {
		for _, ct := range containers.Items {
			id := strconv.FormatUint(uint64(ct.OCID), 10)
			containerInfo.emit(ch, 1, id, ct.Name, ct.Image, ct.Pool, ct.Stack)
			containerRunning.emit(ch, boolValue(ct.Running), id)
			containerCPU.emitOptional(ch, ct.CPUSeconds, id)
			containerCores.emit(ch, ct.CPUCores, id)
			containerMemory.emitOptional(ch, ct.MemoryRSS, id)
			if ct.MemoryLimit > 0 {
				containerMemoryLimit.emit(ch, float64(ct.MemoryLimit), id)
			}
			containerUpdate.emit(ch, boolValue(ct.UpdateAvailable), id)
		}
	}
	collectZFS(ch, read(c.ZFS), now)
}
