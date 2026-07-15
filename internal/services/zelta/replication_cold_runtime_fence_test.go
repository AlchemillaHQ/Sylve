// SPDX-License-Identifier: BSD-2-Clause

package zelta

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	jailService "github.com/alchemillahq/sylve/internal/services/jail"
	libvirtService "github.com/alchemillahq/sylve/internal/services/libvirt"
)

type emergencyVMRuntimeFenceSpy struct {
	libvirtServiceInterfaces.LibvirtServiceInterface
	order *[]string
	err   error
}

func (s *emergencyVMRuntimeFenceSpy) EmergencyStopAllManagedVMs(context.Context) error {
	*s.order = append(*s.order, "vm")
	return s.err
}

type emergencyJailRuntimeFenceSpy struct {
	jailServiceInterfaces.JailServiceInterface
	order *[]string
	err   error
}

func (s *emergencyJailRuntimeFenceSpy) EmergencyStopAllManagedJails(context.Context) error {
	*s.order = append(*s.order, "jail")
	return s.err
}

type noEmergencyVMRuntimeCapability struct {
	libvirtServiceInterfaces.LibvirtServiceInterface
}

type noEmergencyJailRuntimeCapability struct {
	jailServiceInterfaces.JailServiceInterface
}

func TestConcreteServicesExposeEmergencyRuntimeCapabilities(t *testing.T) {
	if _, ok := any(&libvirtService.Service{}).(emergencyVMRuntimeFencer); !ok {
		t.Fatal("concrete libvirt service does not expose emergency VM runtime fencing")
	}
	if _, ok := any(&jailService.Service{}).(emergencyJailRuntimeFencer); !ok {
		t.Fatal("concrete jail service does not expose emergency jail runtime fencing")
	}
}

func TestEmergencyStopAllManagedGuestRuntimesCallsBothAndJoinsErrors(t *testing.T) {
	vmErr := errors.New("vm fence failed")
	jailErr := errors.New("jail fence failed")
	order := []string{}
	svc := &Service{
		VM:   &emergencyVMRuntimeFenceSpy{order: &order, err: vmErr},
		Jail: &emergencyJailRuntimeFenceSpy{order: &order, err: jailErr},
	}

	err := svc.emergencyStopAllManagedGuestRuntimes(t.Context())
	if !errors.Is(err, vmErr) || !errors.Is(err, jailErr) {
		t.Fatalf("expected joined runtime errors, got %v", err)
	}
	if got := strings.Join(order, ","); got != "vm,jail" {
		t.Fatalf("runtime fence order = %q, want vm,jail", got)
	}
}

func TestEmergencyStopAllManagedGuestRuntimesMissingCapabilitiesFailsClosed(t *testing.T) {
	svc := &Service{
		VM:   &noEmergencyVMRuntimeCapability{},
		Jail: &noEmergencyJailRuntimeCapability{},
	}
	err := svc.emergencyStopAllManagedGuestRuntimes(t.Context())
	if err == nil || !strings.Contains(err.Error(), "replication_emergency_vm_runtime_fencer_unavailable") ||
		!strings.Contains(err.Error(), "replication_emergency_jail_runtime_fencer_unavailable") {
		t.Fatalf("missing capabilities did not fail closed: %v", err)
	}
}

func TestCanonicalReplicationGuestDatasetRootRequiresExactSylveBoundary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dataset   string
		guestType string
		guestID   uint
		root      string
	}{
		{
			name: "vm root", dataset: "tank/sylve/virtual-machines/42",
			guestType: clusterModels.ReplicationGuestTypeVM, guestID: 42,
			root: "tank/sylve/virtual-machines/42",
		},
		{
			name: "jail descendant", dataset: "tank/sylve/jails/7/root/data",
			guestType: clusterModels.ReplicationGuestTypeJail, guestID: 7,
			root: "tank/sylve/jails/7",
		},
		{name: "backup prefix", dataset: "tank/backups/sylve/virtual-machines/42"},
		{name: "backup namespace", dataset: "tank/sylve-backups/virtual-machines/42"},
		{name: "nested sylve namespace", dataset: "tank/tenant/sylve/jails/7"},
		{name: "suffixed vm", dataset: "tank/sylve/virtual-machines/42_backup"},
		{name: "leading-zero vm alias", dataset: "tank/sylve/virtual-machines/0042"},
		{name: "rotated vm", dataset: "tank/sylve/virtual-machines/42_gen-run"},
		{name: "snapshot", dataset: "tank/sylve/jails/7@snap"},
		{name: "zero id", dataset: "tank/sylve/jails/0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guestType, guestID, root, ok := parseCanonicalReplicationGuestDataset(tc.dataset)
			if tc.root == "" {
				if ok || guestType != "" || guestID != 0 || root != "" {
					t.Fatalf("noncanonical dataset parsed as type=%q id=%d root=%q", guestType, guestID, root)
				}
				return
			}
			if !ok || guestType != tc.guestType || guestID != tc.guestID || root != tc.root {
				t.Fatalf("canonical parse = (%q,%d,%q,%t), want (%q,%d,%q,true)",
					guestType, guestID, root, ok, tc.guestType, tc.guestID, tc.root)
			}
			if got := canonicalReplicationGuestDatasetRoot(tc.dataset, tc.guestType); got != tc.root {
				t.Fatalf("canonical root = %q, want %q", got, tc.root)
			}
		})
	}
	if got := canonicalReplicationGuestDatasetRoot(
		"tank/sylve/virtual-machines/42", clusterModels.ReplicationGuestTypeJail,
	); got != "" {
		t.Fatalf("wrong guest type returned canonical root %q", got)
	}
}

func TestSelfFenceExpiredLeasesEmptyLocalNodeFailsClosed(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	order := []string{}
	svc := &Service{
		VM:   &emergencyVMRuntimeFenceSpy{order: &order},
		Jail: &emergencyJailRuntimeFenceSpy{order: &order},
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := svc.selfFenceExpiredLeasesForLocalNode(ctx, "")
	if err == nil || !strings.Contains(err.Error(), "replication_local_node_id_unavailable") {
		t.Fatalf("empty local node identity did not fail closed: %v", err)
	}
	if got := strings.Join(order, ","); got != "vm,jail" {
		t.Fatalf("runtime fence order = %q, want vm,jail", got)
	}
}

func TestReplicationPolicyReadFailureFencesAllRuntimesWithNonemptyCache(t *testing.T) {
	policyErr := errors.New("policy database unavailable")
	vmErr := errors.New("vm host fence failed")
	jailErr := errors.New("jail host fence failed")
	order := []string{}
	svc := &Service{
		VM:   &emergencyVMRuntimeFenceSpy{order: &order, err: vmErr},
		Jail: &emergencyJailRuntimeFenceSpy{order: &order, err: jailErr},
	}
	now := time.Now().UTC()
	observations := map[uint]replicationFenceObservation{
		1: {
			Policy: clusterModels.ReplicationPolicy{
				ID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 101,
				ActiveNodeID: "node-a", OwnerEpoch: 1, Enabled: true,
			},
			LeaseOwner: "node-a", LeaseEpoch: 1, LeaseExpiresAt: now.Add(time.Minute),
		},
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := svc.handleReplicationPolicyReadFailure(ctx, policyErr, "node-a", now, observations)
	if !errors.Is(err, policyErr) || !errors.Is(err, vmErr) || !errors.Is(err, jailErr) {
		t.Fatalf("policy/runtime errors were not aggregated: %v", err)
	}
	if got := strings.Join(order, ","); got != "vm,jail" {
		t.Fatalf("runtime fence order = %q, want vm,jail", got)
	}
	message := err.Error()
	policyIndex := strings.Index(message, policyErr.Error())
	vmIndex := strings.Index(message, vmErr.Error())
	jailIndex := strings.Index(message, jailErr.Error())
	if policyIndex < 0 || vmIndex < policyIndex || jailIndex < vmIndex {
		t.Fatalf("unexpected aggregate error order: %q", message)
	}
}

func TestReplicationPolicyReadFailureFencesRuntimesWithMissingEmptyOrCorruptObservationFile(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contents    []byte
		writeFile   bool
		wantLoadErr bool
	}{
		{name: "missing"},
		{name: "empty", contents: []byte{}, writeFile: true},
		{name: "corrupt", contents: []byte("{"), writeFile: true, wantLoadErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SYLVE_DATA_PATH", t.TempDir())
			if tc.writeFile {
				path, err := replicationFenceObservationPath()
				if err != nil {
					t.Fatalf("observation path: %v", err)
				}
				if err := os.WriteFile(path, tc.contents, 0600); err != nil {
					t.Fatalf("write observation fixture: %v", err)
				}
			}

			observations, loadErr := loadDurableReplicationFenceObservations()
			if (loadErr != nil) != tc.wantLoadErr {
				t.Fatalf("load error = %v, want error=%t", loadErr, tc.wantLoadErr)
			}
			if loadErr != nil {
				observations = map[uint]replicationFenceObservation{}
			}
			if len(observations) != 0 {
				t.Fatalf("cold observation fixture unexpectedly loaded entries: %#v", observations)
			}

			order := []string{}
			svc := &Service{
				VM:   &emergencyVMRuntimeFenceSpy{order: &order},
				Jail: &emergencyJailRuntimeFenceSpy{order: &order},
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			policyErr := errors.New("policy read failed")
			if err := svc.handleReplicationPolicyReadFailure(
				ctx, policyErr, "node-a", time.Now().UTC(), observations,
			); !errors.Is(err, policyErr) {
				t.Fatalf("policy read error not preserved: %v", err)
			}
			if got := strings.Join(order, ","); got != "vm,jail" {
				t.Fatalf("runtime fence order = %q, want vm,jail", got)
			}
		})
	}
}
