// SPDX-License-Identifier: BSD-2-Clause

package libvirt

import (
	"errors"
	"strings"
	"testing"

	golibvirt "github.com/digitalocean/go-libvirt"
)

type fakeEmergencyVMRuntimeOps struct {
	listings    [][]golibvirt.Domain
	listErrors  map[int]error
	listCalls   int
	destroyErrs map[string]error
	destroyed   []string
}

func (f *fakeEmergencyVMRuntimeOps) ListActiveDomains() ([]golibvirt.Domain, error) {
	call := f.listCalls
	f.listCalls++
	if err := f.listErrors[call]; err != nil {
		return nil, err
	}
	if len(f.listings) == 0 {
		return []golibvirt.Domain{}, nil
	}
	if call >= len(f.listings) {
		call = len(f.listings) - 1
	}
	return append([]golibvirt.Domain{}, f.listings[call]...), nil
}

func (f *fakeEmergencyVMRuntimeOps) DestroyDomain(domain golibvirt.Domain) error {
	f.destroyed = append(f.destroyed, domain.Name)
	return f.destroyErrs[domain.Name]
}

func TestManagedVMRuntimeRID(t *testing.T) {
	for _, tc := range []struct {
		name string
		rid  uint
		ok   bool
	}{
		{name: "1", rid: 1, ok: true},
		{name: "9999", rid: 9999, ok: true},
		{name: "0"},
		{name: "10000"},
		{name: "01"},
		{name: " 1 "},
		{name: "vm-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rid, ok := managedVMRuntimeRID(tc.name)
			if rid != tc.rid || ok != tc.ok {
				t.Fatalf("managedVMRuntimeRID(%q) = (%d, %t), want (%d, %t)", tc.name, rid, ok, tc.rid, tc.ok)
			}
		})
	}
}

func TestEmergencyStopAllManagedVMsWithOpsStopsOnlyManagedAndDrainsNewRuntime(t *testing.T) {
	ops := &fakeEmergencyVMRuntimeOps{
		listings: [][]golibvirt.Domain{
			{{Name: "1"}, {Name: "9999"}, {Name: "external"}, {Name: "0"}, {Name: "10000"}},
			{{Name: "2"}, {Name: "external"}},
			{{Name: "external"}},
		},
	}

	if err := emergencyStopAllManagedVMsWithOps(ops); err != nil {
		t.Fatalf("emergency fence failed: %v", err)
	}
	if got, want := strings.Join(ops.destroyed, ","), "1,9999,2"; got != want {
		t.Fatalf("destroyed domains = %q, want %q", got, want)
	}
}

func TestEmergencyStopAllManagedVMsWithOpsContinuesAndReportsResidual(t *testing.T) {
	destroyErr := errors.New("destroy refused")
	ops := &fakeEmergencyVMRuntimeOps{
		listings: [][]golibvirt.Domain{
			{{Name: "1"}, {Name: "2"}},
			{{Name: "1"}},
			{{Name: "1"}},
			{{Name: "1"}},
		},
		destroyErrs: map[string]error{"1": destroyErr},
	}

	err := emergencyStopAllManagedVMsWithOps(ops)
	if !errors.Is(err, destroyErr) {
		t.Fatalf("destroy error not preserved: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "managed_vm_runtimes_still_active: 1") {
		t.Fatalf("residual runtime not reported: %v", err)
	}
	if len(ops.destroyed) < 2 || ops.destroyed[0] != "1" || ops.destroyed[1] != "2" {
		t.Fatalf("destroy failure suppressed later domain: %v", ops.destroyed)
	}
}

func TestEmergencyStopAllManagedVMsWithOpsAggregatesVerificationFailure(t *testing.T) {
	destroyErr := errors.New("destroy failed")
	listErr := errors.New("libvirt list failed")
	ops := &fakeEmergencyVMRuntimeOps{
		listings:    [][]golibvirt.Domain{{{Name: "1"}}},
		listErrors:  map[int]error{1: listErr},
		destroyErrs: map[string]error{"1": destroyErr},
	}

	err := emergencyStopAllManagedVMsWithOps(ops)
	if !errors.Is(err, destroyErr) || !errors.Is(err, listErr) {
		t.Fatalf("expected joined destroy/list errors, got %v", err)
	}
}
