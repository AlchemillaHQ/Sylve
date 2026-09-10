// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	"gorm.io/gorm"
)

type replicationNetworkAuthStub struct {
	serviceInterfaces.AuthServiceInterface
}

func (replicationNetworkAuthStub) CreateInternalClusterJWT(string) (string, error) {
	return "replication-network-token", nil
}

func TestValidateReplicationVMTargetNetworkCompatibility(t *testing.T) {
	var received replicationVMTargetProbe
	responseBody := `{"status":"success","data":{"missingSwitches":[],"incompatibleSwitches":["WAN: vm_network_default_access_vlan_mismatch"],"networkCompatibilityChecked":true}}`
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/intra-cluster/migration/check-vm-target" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("X-Cluster-Token") != "Bearer replication-network-token" {
			t.Errorf("cluster token = %q", r.Header.Get("X-Cluster-Token"))
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responseBody))
	}))
	defer server.Close()

	db := testutil.NewSQLiteTestDB(t,
		&clusterModels.ClusterNode{},
		&networkModels.ManualSwitch{},
		&vmModels.VM{},
		&vmModels.Network{},
	)
	switchModel := networkModels.ManualSwitch{Name: "WAN", Bridge: "bridge0"}
	if err := db.Create(&switchModel).Error; err != nil {
		t.Fatalf("seed switch: %v", err)
	}
	vm := vmModels.VM{Name: "vm-91", RID: 91}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("seed vm: %v", err)
	}
	if err := db.Create(&vmModels.Network{
		VMID: vm.ID, SwitchID: switchModel.ID, SwitchType: "manual", Enable: true,
	}).Error; err != nil {
		t.Fatalf("seed vm network: %v", err)
	}
	if err := db.Create(&clusterModels.ClusterNode{
		NodeUUID: "target-node", API: strings.TrimPrefix(server.URL, "https://"),
	}).Error; err != nil {
		t.Fatalf("seed target node: %v", err)
	}

	originalResolve := resolveReplicationVMNetworkAttachment
	defaultVLAN := 20
	resolveReplicationVMNetworkAttachment = func(
		_ *gorm.DB, switchType string, switchID uint,
	) (networkAttachment.Contract, error) {
		if switchType != "manual" || switchID != switchModel.ID {
			t.Fatalf("unexpected resolver input: %s:%d", switchType, switchID)
		}
		return networkAttachment.Normalize(networkAttachment.Contract{
			Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindVM,
			SwitchName: "WAN", SwitchType: "manual", VLANFiltering: true,
			DefaultAccessVLAN: &defaultVLAN,
		})
	}
	t.Cleanup(func() { resolveReplicationVMNetworkAttachment = originalResolve })

	clusterSvc := &clusterService.Service{DB: db, AuthService: replicationNetworkAuthStub{}}
	svc := &Service{DB: db, Cluster: clusterSvc}
	check, err := svc.buildReplicationTargetNetworkCheck(&clusterModels.ReplicationPolicy{
		ID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: vm.RID,
	})
	if err != nil {
		t.Fatalf("build target network check: %v", err)
	}
	err = svc.validateReplicationTargetNetworkCheck(context.Background(), "target-node", check)
	if err == nil || !strings.Contains(err.Error(), "replication_target_network_incompatible") {
		t.Fatalf("compatibility error = %v", err)
	}
	if received.RID != vm.RID || len(received.Switches) != 1 {
		t.Fatalf("received probe = %+v", received)
	}
	got := received.Switches[0]
	if got.Name != "WAN" || got.Attachment.Version != networkAttachment.CurrentVersion ||
		got.Attachment.Kind != networkAttachment.KindVM || got.Attachment.SwitchType != "manual" ||
		!got.Attachment.VLANFiltering || got.Attachment.DefaultAccessVLAN == nil ||
		*got.Attachment.DefaultAccessVLAN != defaultVLAN {
		t.Fatalf("received switch = %+v", got)
	}

	responseBody = `{"status":"success","data":{"missingSwitches":[],"incompatibleSwitches":[]}}`
	err = svc.validateReplicationTargetNetworkCheck(context.Background(), "target-node", check)
	if err == nil || !strings.Contains(err.Error(), "replication_target_network_check_unsupported") {
		t.Fatalf("legacy target response was not rejected: %v", err)
	}
}

func TestBuildReplicationVMTargetProbeIncludesDisabledSwitchIdentity(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Network{})
	vm := vmModels.VM{Name: "vm-93", RID: 93}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("seed vm: %v", err)
	}
	for _, network := range []vmModels.Network{
		{VMID: vm.ID, SwitchID: 1, SwitchType: "standard", Enable: false},
		{VMID: vm.ID, SwitchID: 1, SwitchType: "standard", Enable: true},
		{VMID: vm.ID, SwitchID: 2, SwitchType: "manual", Enable: false},
	} {
		if err := db.Create(&network).Error; err != nil {
			t.Fatalf("seed VM network: %v", err)
		}
	}

	originalResolve := resolveReplicationVMNetworkAttachment
	originalResolveIdentity := resolveReplicationVMNetworkIdentity
	t.Cleanup(func() {
		resolveReplicationVMNetworkAttachment = originalResolve
		resolveReplicationVMNetworkIdentity = originalResolveIdentity
	})

	defaultVLAN := 30
	resolveReplicationVMNetworkAttachment = func(
		_ *gorm.DB, switchType string, switchID uint,
	) (networkAttachment.Contract, error) {
		if switchType != "standard" || switchID != 1 {
			t.Fatalf("unexpected desired resolver input: %s:%d", switchType, switchID)
		}
		return networkAttachment.Normalize(networkAttachment.Contract{
			Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindVM,
			SwitchName: "tenant", SwitchType: "standard", VLANFiltering: true,
			DefaultAccessVLAN: &defaultVLAN,
		})
	}
	identityCalls := 0
	resolveReplicationVMNetworkIdentity = func(
		_ *gorm.DB, switchType string, switchID uint,
	) (networkAttachment.Contract, error) {
		identityCalls++
		name := "tenant"
		if switchID == 2 {
			name = "dormant"
		}
		return networkAttachment.LegacyUnfiltered(
			networkAttachment.KindVM, name, switchType,
		)
	}

	probe, err := (&Service{DB: db}).buildReplicationVMTargetProbe(vm.RID)
	if err != nil {
		t.Fatalf("build probe: %v", err)
	}
	if identityCalls != 2 {
		t.Fatalf("identity resolver calls = %d, want 2", identityCalls)
	}
	if len(probe.Switches) != 2 {
		t.Fatalf("switches = %+v", probe.Switches)
	}
	switchesByName := make(map[string]networkAttachment.NamedContract, len(probe.Switches))
	for _, entry := range probe.Switches {
		switchesByName[entry.Attachment.SwitchName] = entry
	}
	if got, ok := switchesByName["tenant"]; !ok || got.IdentityOnly ||
		!got.Attachment.VLANFiltering || got.Attachment.DefaultAccessVLAN == nil ||
		*got.Attachment.DefaultAccessVLAN != defaultVLAN {
		t.Fatalf("enabled switch did not replace identity-only entry: %+v", got)
	}
	if got, ok := switchesByName["dormant"]; !ok || !got.IdentityOnly ||
		got.Attachment.SwitchType != "manual" {
		t.Fatalf("disabled switch identity = %+v", got)
	}
}

func TestBuildReplicationJailTargetProbePreservesVLANPolicy(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&jailModels.Jail{},
		&jailModels.Network{},
		&networkModels.StandardSwitch{},
	)
	defaultVLAN := 10
	switchModel := networkModels.StandardSwitch{
		Name: "LAN", BridgeName: "bridge0", VLANFiltering: true, DefaultAccessVLAN: &defaultVLAN,
	}
	if err := db.Create(&switchModel).Error; err != nil {
		t.Fatalf("seed switch: %v", err)
	}
	jail := jailModels.Jail{CTID: 92, Name: "jail-92", Type: jailModels.JailTypeFreeBSD}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("seed jail: %v", err)
	}
	untagged := 10
	if err := db.Create(&jailModels.Network{
		JailID: jail.ID, Name: "vnet0", SwitchID: switchModel.ID, SwitchType: "standard",
		VLANPolicy: bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: &untagged},
	}).Error; err != nil {
		t.Fatalf("seed jail network: %v", err)
	}

	originalResolve := resolveReplicationJailNetworkAttachment
	resolverCalled := false
	resolveReplicationJailNetworkAttachment = func(
		_ *gorm.DB, network *jailModels.Network,
	) (networkAttachment.Contract, error) {
		resolverCalled = true
		policy := network.VLANPolicy
		return networkAttachment.Normalize(networkAttachment.Contract{
			Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindJail,
			SwitchName: "LAN", SwitchType: "standard", VLANFiltering: true,
			VLANPolicy: &policy,
		})
	}
	t.Cleanup(func() { resolveReplicationJailNetworkAttachment = originalResolve })

	probe, err := (&Service{DB: db}).buildReplicationJailTargetProbe(jail.CTID)
	if err != nil {
		t.Fatalf("build probe: %v", err)
	}
	if !resolverCalled || len(probe.Networks) != 1 {
		t.Fatalf("resolverCalled=%t probe=%+v", resolverCalled, probe)
	}
	got := probe.Networks[0]
	if got.Name != "vnet0" || got.Attachment.SwitchName != "LAN" ||
		got.Attachment.Version != networkAttachment.CurrentVersion || got.Attachment.VLANPolicy == nil ||
		got.Attachment.VLANPolicy.Mode != bridgevlan.ModeAccess ||
		got.Attachment.VLANPolicy.UntaggedVLAN == nil ||
		*got.Attachment.VLANPolicy.UntaggedVLAN != 10 {
		t.Fatalf("network probe = %+v", got)
	}
}

func TestBuildReplicationVMTargetNetworkCheckFromCapturedMetadata(t *testing.T) {
	defaultVLAN := 40
	contract, err := networkAttachment.Normalize(networkAttachment.Contract{
		Version:           networkAttachment.CurrentVersion,
		Kind:              networkAttachment.KindVM,
		SwitchName:        "tenant-40",
		SwitchType:        "standard",
		VLANFiltering:     true,
		DefaultAccessVLAN: &defaultVLAN,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(vmModels.VM{
		RID: 140,
		Networks: []vmModels.Network{{
			Enable: true, Attachment: &contract,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	check, err := replicationVMTargetNetworkCheckFromMetadata(raw, 140)
	if err != nil {
		t.Fatalf("build captured VM check: %v", err)
	}
	probe, ok := check.payload.(replicationVMTargetProbe)
	if !ok || check.action != "migration/check-vm-target" || len(probe.Switches) != 1 {
		t.Fatalf("captured VM check = action=%q payload=%#v", check.action, check.payload)
	}
	got := probe.Switches[0]
	if got.IdentityOnly || got.Attachment.DefaultAccessVLAN == nil ||
		*got.Attachment.DefaultAccessVLAN != defaultVLAN {
		t.Fatalf("captured VM attachment = %+v", got)
	}
}

func TestBuildReplicationJailTargetNetworkCheckFromCapturedMetadata(t *testing.T) {
	untagged := 25
	policy := bridgevlan.PortPolicy{
		Mode: bridgevlan.ModeTrunk, UntaggedVLAN: &untagged, TaggedVLANs: []int{30, 40},
	}
	contract, err := networkAttachment.Normalize(networkAttachment.Contract{
		Version:       networkAttachment.CurrentVersion,
		Kind:          networkAttachment.KindJail,
		SwitchName:    "jail-trunk",
		SwitchType:    "manual",
		VLANFiltering: true,
		VLANPolicy:    &policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(jailModels.Jail{
		CTID: 141,
		Networks: []jailModels.Network{{
			Name: "vnet0", Attachment: &contract,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	check, err := replicationJailTargetNetworkCheckFromMetadata(raw, 141)
	if err != nil {
		t.Fatalf("build captured jail check: %v", err)
	}
	probe, ok := check.payload.(replicationJailTargetProbe)
	if !ok || check.action != "migration/check-jail-target" || len(probe.Networks) != 1 {
		t.Fatalf("captured jail check = action=%q payload=%#v", check.action, check.payload)
	}
	got := probe.Networks[0]
	if got.Name != "vnet0" || got.Attachment.VLANPolicy == nil ||
		!slices.Equal(got.Attachment.VLANPolicy.TaggedVLANs, []int{30, 40}) {
		t.Fatalf("captured jail attachment = %+v", got)
	}
}

func TestBuildReplicationTargetNetworkCheckFromSnapshotRejectsCrossRootMismatch(t *testing.T) {
	metadata := func(vlan int) []byte {
		contract, err := networkAttachment.Normalize(networkAttachment.Contract{
			Version:           networkAttachment.CurrentVersion,
			Kind:              networkAttachment.KindVM,
			SwitchName:        "tenant",
			SwitchType:        "standard",
			VLANFiltering:     true,
			DefaultAccessVLAN: &vlan,
		})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(vmModels.VM{
			RID:      142,
			Networks: []vmModels.Network{{Enable: true, Attachment: &contract}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	payloads := map[string][]byte{
		"pool-a/sylve/virtual-machines/142": metadata(10),
		"pool-b/sylve/virtual-machines/142": metadata(20),
	}
	svc := &Service{
		replicationSnapshotMetadataReader: func(
			_ context.Context, dataset, snapshotName, relativePath string,
		) ([]byte, bool, error) {
			if snapshotName != "ha_replication-1-generation" || relativePath != ".sylve/vm.json" {
				t.Fatalf("snapshot metadata request = %s %s", snapshotName, relativePath)
			}
			raw, found := payloads[dataset]
			return raw, found, nil
		},
	}
	_, err := svc.buildReplicationTargetNetworkCheckFromSnapshot(
		context.Background(),
		&clusterModels.ReplicationPolicy{
			ID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 142,
		},
		[]string{
			"pool-b/sylve/virtual-machines/142",
			"pool-a/sylve/virtual-machines/142",
		},
		"ha_replication-1-generation",
	)
	if err == nil || !strings.Contains(err.Error(), "replication_snapshot_network_metadata_mismatch") {
		t.Fatalf("cross-root metadata mismatch = %v", err)
	}
}

func TestBuildReplicationTargetNetworkCheckFromSnapshotRequiresMetadata(t *testing.T) {
	raw, err := json.Marshal(vmModels.VM{RID: 143})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		sources  []string
		metadata map[string][]byte
	}{
		{
			name:    "only root is missing",
			sources: []string{"tank/sylve/virtual-machines/143"},
		},
		{
			name: "one of multiple roots is missing",
			sources: []string{
				"pool-a/sylve/virtual-machines/143",
				"pool-b/sylve/virtual-machines/143",
			},
			metadata: map[string][]byte{"pool-a/sylve/virtual-machines/143": raw},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc := &Service{
				replicationSnapshotMetadataReader: func(
					_ context.Context, dataset, _, _ string,
				) ([]byte, bool, error) {
					raw, found := test.metadata[dataset]
					return raw, found, nil
				},
			}
			_, err := svc.buildReplicationTargetNetworkCheckFromSnapshot(
				context.Background(),
				&clusterModels.ReplicationPolicy{
					ID: 2, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 143,
				},
				test.sources,
				"ha_replication-2-generation",
			)
			if err == nil || !strings.Contains(err.Error(), "replication_snapshot_network_metadata_not_found") {
				t.Fatalf("missing snapshot metadata = %v", err)
			}
		})
	}
}

func TestBackupSnapshotRejectsCapturedVLANDrift(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Network{})
	vm := vmModels.VM{Name: "vm-144", RID: 144}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}
	if err := db.Create(&vmModels.Network{
		VMID: vm.ID, SwitchID: 1, SwitchType: "standard", Enable: true,
	}).Error; err != nil {
		t.Fatalf("seed VM network: %v", err)
	}

	capturedVLAN := 10
	capturedContract, err := networkAttachment.Normalize(networkAttachment.Contract{
		Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindVM,
		SwitchName: "tenant", SwitchType: "standard", VLANFiltering: true,
		DefaultAccessVLAN: &capturedVLAN,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(vmModels.VM{
		RID: vm.RID, Networks: []vmModels.Network{{Enable: true, Attachment: &capturedContract}},
	})
	if err != nil {
		t.Fatal(err)
	}

	currentVLAN := capturedVLAN
	originalResolve := resolveReplicationVMNetworkAttachment
	resolveReplicationVMNetworkAttachment = func(*gorm.DB, string, uint) (networkAttachment.Contract, error) {
		return networkAttachment.Normalize(networkAttachment.Contract{
			Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindVM,
			SwitchName: "tenant", SwitchType: "standard", VLANFiltering: true,
			DefaultAccessVLAN: &currentVLAN,
		})
	}
	t.Cleanup(func() { resolveReplicationVMNetworkAttachment = originalResolve })

	svc := &Service{
		DB: db,
		replicationSnapshotMetadataReader: func(
			context.Context, string, string, string,
		) ([]byte, bool, error) {
			return raw, true, nil
		},
	}
	if err := svc.validateBackupSnapshotNetworkMetadata(
		context.Background(), clusterModels.BackupJobModeVM, vm.RID,
		[]string{"tank/sylve/virtual-machines/144"}, "backup-1",
	); err != nil {
		t.Fatalf("matching backup metadata was rejected: %v", err)
	}

	currentVLAN = 20
	err = svc.validateBackupSnapshotNetworkMetadata(
		context.Background(), clusterModels.BackupJobModeVM, vm.RID,
		[]string{"tank/sylve/virtual-machines/144"}, "backup-1",
	)
	if err == nil || !strings.Contains(err.Error(), "backup_snapshot_network_metadata_stale") {
		t.Fatalf("stale backup metadata error = %v", err)
	}
}

func TestReadReplicationSnapshotMetadataRestoresSourceMountState(t *testing.T) {
	mountpoint := t.TempDir()
	snapshotName := "ha_replication-1-generation"
	metadataPath := filepath.Join(mountpoint, ".zfs", "snapshot", snapshotName, ".sylve", "vm.json")
	if err := os.MkdirAll(filepath.Dir(metadataPath), 0o755); err != nil {
		t.Fatalf("create snapshot metadata directory: %v", err)
	}
	want := []byte(`{"rid":101}`)
	if err := os.WriteFile(metadataPath, want, 0o600); err != nil {
		t.Fatalf("write snapshot metadata: %v", err)
	}

	for _, test := range []struct {
		name          string
		mounted       string
		wantMounts    int
		wantUnmounts  int
		unmountErr    error
		wantReadError bool
	}{
		{name: "already mounted", mounted: "yes"},
		{name: "mounted for inspection", mounted: "no", wantMounts: 1, wantUnmounts: 1},
		{
			name: "cleanup failure is returned", mounted: "no", wantMounts: 1, wantUnmounts: 1,
			unmountErr: errors.New("unmount failed"), wantReadError: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mounts := 0
			unmounts := 0
			svc := &Service{
				replicationSnapshotDatasetState: func(context.Context, string) (string, string, error) {
					return test.mounted, mountpoint, nil
				},
				localDatasetMounter: func(context.Context, string) error {
					mounts++
					return nil
				},
				localDatasetUnmounter: func(_ context.Context, _ string, force bool) error {
					unmounts++
					if force {
						t.Fatal("replication inspection forced the source unmount")
					}
					return test.unmountErr
				},
			}

			got, found, err := svc.readReplicationSnapshotMetadata(
				context.Background(), "tank/vm-101", snapshotName, ".sylve/vm.json",
			)
			if test.wantReadError {
				if !errors.Is(err, test.unmountErr) {
					t.Fatalf("expected cleanup error, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("read snapshot metadata: %v", err)
			}
			if !found || string(got) != string(want) {
				t.Fatalf("metadata = %q, found=%t; want %q, true", got, found, want)
			}
			if mounts != test.wantMounts || unmounts != test.wantUnmounts {
				t.Fatalf("mount calls = %d, unmount calls = %d; want %d, %d", mounts, unmounts, test.wantMounts, test.wantUnmounts)
			}
		})
	}
}
