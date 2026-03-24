// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"context"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gopkg.in/yaml.v3"
)

type fakeVMTemplateSystemService struct {
	systemServiceInterfaces.SystemServiceInterface
	pools []*gzfs.ZPool
	err   error
}

func (f fakeVMTemplateSystemService) GetUsablePools(_ context.Context) ([]*gzfs.ZPool, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.pools, nil
}

func TestRewriteCloudInitMetadataIdentity_RewritesExistingKeys(t *testing.T) {
	in := "instance-id: old-id\nlocal-hostname: old-host\nzone: test\n"
	out, err := rewriteCloudInitMetadataIdentity(in, "prod", "ignored-vm", 401)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded map[string]any
	if err := yaml.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("failed to parse rewritten yaml: %v", err)
	}

	if decoded["instance-id"] != "prod-401" {
		t.Fatalf("expected rewritten instance-id prod-401, got %#v", decoded["instance-id"])
	}
	if decoded["local-hostname"] != "prod-401" {
		t.Fatalf("expected rewritten local-hostname prod-401, got %#v", decoded["local-hostname"])
	}
	if decoded["zone"] != "test" {
		t.Fatalf("expected unrelated metadata preserved, got %#v", decoded["zone"])
	}
}

func TestRewriteCloudInitMetadataIdentity_InsertsMissingKeysAndFallbackPrefix(t *testing.T) {
	in := "zone: staging\n"
	out, err := rewriteCloudInitMetadataIdentity(in, "", "vm-blue", 512)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded map[string]any
	if err := yaml.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("failed to parse rewritten yaml: %v", err)
	}

	if decoded["instance-id"] != "vm-blue-512" {
		t.Fatalf("expected fallback instance-id vm-blue-512, got %#v", decoded["instance-id"])
	}
	if decoded["local-hostname"] != "vm-blue-512" {
		t.Fatalf("expected fallback local-hostname vm-blue-512, got %#v", decoded["local-hostname"])
	}
}

func TestRewriteCloudInitMetadataIdentity_InvalidYAML(t *testing.T) {
	_, err := rewriteCloudInitMetadataIdentity("local-hostname: [broken", "pref", "vm-a", 600)
	if err == nil || !strings.Contains(err.Error(), "invalid_cloud_init_metadata_yaml") {
		t.Fatalf("expected invalid_cloud_init_metadata_yaml, got %v", err)
	}
}

func TestRewriteCloudInitMetadataIdentity_FallbacksToDefaultVMPrefix(t *testing.T) {
	out, err := rewriteCloudInitMetadataIdentity("", "", "", 700)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded map[string]any
	if err := yaml.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("failed to parse rewritten yaml: %v", err)
	}

	if decoded["instance-id"] != "vm-700" || decoded["local-hostname"] != "vm-700" {
		t.Fatalf("expected vm-700 identity values, got %#v", decoded)
	}
}

func TestBuildVMTemplateTargets(t *testing.T) {
	svc := &Service{}
	template := vmModels.VMTemplate{
		SourceVMName: "webvm",
	}

	t.Run("single invalid rid", func(t *testing.T) {
		_, err := svc.buildVMTemplateTargets(template, libvirtServiceInterfaces.CreateFromTemplateRequest{
			Mode: "single",
			RID:  10000,
		})
		if err == nil || !strings.Contains(err.Error(), "invalid_rid") {
			t.Fatalf("expected invalid_rid, got %v", err)
		}
	})

	t.Run("single defaults name", func(t *testing.T) {
		targets, err := svc.buildVMTemplateTargets(template, libvirtServiceInterfaces.CreateFromTemplateRequest{
			Mode: "single",
			RID:  220,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(targets) != 1 {
			t.Fatalf("expected 1 target, got %d", len(targets))
		}
		if targets[0].Name != "webvm" {
			t.Fatalf("expected default name webvm, got %q", targets[0].Name)
		}
	})

	t.Run("multiple invalid range", func(t *testing.T) {
		_, err := svc.buildVMTemplateTargets(template, libvirtServiceInterfaces.CreateFromTemplateRequest{
			Mode:     "multiple",
			StartRID: 9999,
			Count:    2,
		})
		if err == nil || !strings.Contains(err.Error(), "invalid_rid_range") {
			t.Fatalf("expected invalid_rid_range, got %v", err)
		}
	})

	t.Run("multiple mode", func(t *testing.T) {
		targets, err := svc.buildVMTemplateTargets(template, libvirtServiceInterfaces.CreateFromTemplateRequest{
			Mode:       "multiple",
			StartRID:   300,
			Count:      2,
			NamePrefix: "api",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(targets) != 2 {
			t.Fatalf("expected 2 targets, got %d", len(targets))
		}
		if targets[0].Name != "api-300" || targets[1].Name != "api-301" {
			t.Fatalf("unexpected generated names: %#v", targets)
		}
	})
}

func TestPreflightVMTemplateTargets(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t,
		&vmModels.VM{},
		&jailModels.Jail{},
		&clusterModels.Cluster{},
		&clusterModels.ClusterNode{},
	)
	svc := &Service{DB: dbConn}

	if err := dbConn.Create(&vmModels.VM{RID: 200, Name: "existing-vm"}).Error; err != nil {
		t.Fatalf("failed to seed vm: %v", err)
	}
	if err := dbConn.Create(&jailModels.Jail{CTID: 201, Name: "existing-jail", Type: jailModels.JailTypeFreeBSD}).Error; err != nil {
		t.Fatalf("failed to seed jail: %v", err)
	}
	if err := dbConn.Create(&clusterModels.Cluster{Enabled: true}).Error; err != nil {
		t.Fatalf("failed to seed cluster: %v", err)
	}
	if err := dbConn.Create(&clusterModels.ClusterNode{
		NodeUUID: "node-1",
		GuestIDs: []uint{202},
	}).Error; err != nil {
		t.Fatalf("failed to seed cluster node: %v", err)
	}

	t.Run("vm rid conflict", func(t *testing.T) {
		err := svc.preflightVMTemplateTargets([]vmTemplateCreateTarget{{RID: 200, Name: "vm-200"}})
		if err == nil || !strings.Contains(err.Error(), "rid_range_contains_used_values") {
			t.Fatalf("expected rid_range_contains_used_values, got %v", err)
		}
	})

	t.Run("jail ctid conflict", func(t *testing.T) {
		err := svc.preflightVMTemplateTargets([]vmTemplateCreateTarget{{RID: 201, Name: "vm-201"}})
		if err == nil || !strings.Contains(err.Error(), "rid_range_contains_used_values") {
			t.Fatalf("expected rid_range_contains_used_values, got %v", err)
		}
	})

	t.Run("cluster guest id conflict", func(t *testing.T) {
		err := svc.preflightVMTemplateTargets([]vmTemplateCreateTarget{{RID: 202, Name: "vm-202"}})
		if err == nil || !strings.Contains(err.Error(), "rid_range_contains_used_values") {
			t.Fatalf("expected rid_range_contains_used_values, got %v", err)
		}
	})

	t.Run("name conflict", func(t *testing.T) {
		err := svc.preflightVMTemplateTargets([]vmTemplateCreateTarget{{RID: 203, Name: "existing-vm"}})
		if err == nil || !strings.Contains(err.Error(), "vm_name_already_in_use") {
			t.Fatalf("expected vm_name_already_in_use, got %v", err)
		}
	})

	t.Run("valid targets", func(t *testing.T) {
		err := svc.preflightVMTemplateTargets([]vmTemplateCreateTarget{
			{RID: 303, Name: "api-303"},
			{RID: 304, Name: "api-304"},
		})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})
}

func TestResolveVMTemplateStoragePools(t *testing.T) {
	svc := &Service{
		System: fakeVMTemplateSystemService{
			pools: []*gzfs.ZPool{{Name: "zroot"}, {Name: "tank"}},
		},
	}
	template := vmModels.VMTemplate{
		Storages: []vmModels.VMTemplateStorage{
			{SourceStorageID: 11, Pool: "zroot"},
			{SourceStorageID: 12, Pool: "tank"},
		},
	}

	t.Run("defaults to template pools", func(t *testing.T) {
		out, err := svc.resolveVMTemplateStoragePools(context.Background(), template, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out[11] != "zroot" || out[12] != "tank" {
			t.Fatalf("unexpected pool mapping: %#v", out)
		}
	})

	t.Run("allows per-storage override", func(t *testing.T) {
		out, err := svc.resolveVMTemplateStoragePools(context.Background(), template, []libvirtServiceInterfaces.VMTemplateStoragePoolAssignment{
			{SourceStorageID: 12, Pool: "zroot"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out[12] != "zroot" {
			t.Fatalf("expected source storage 12 mapped to zroot, got %q", out[12])
		}
	})

	t.Run("rejects invalid mapping source", func(t *testing.T) {
		_, err := svc.resolveVMTemplateStoragePools(context.Background(), template, []libvirtServiceInterfaces.VMTemplateStoragePoolAssignment{
			{SourceStorageID: 99, Pool: "zroot"},
		})
		if err == nil || !strings.Contains(err.Error(), "invalid_storage_mapping_source") {
			t.Fatalf("expected invalid_storage_mapping_source, got %v", err)
		}
	})

	t.Run("rejects duplicate assignment source", func(t *testing.T) {
		_, err := svc.resolveVMTemplateStoragePools(context.Background(), template, []libvirtServiceInterfaces.VMTemplateStoragePoolAssignment{
			{SourceStorageID: 11, Pool: "tank"},
			{SourceStorageID: 11, Pool: "zroot"},
		})
		if err == nil || !strings.Contains(err.Error(), "duplicate_storage_mapping_source") {
			t.Fatalf("expected duplicate_storage_mapping_source, got %v", err)
		}
	})
}

func TestSourceVMStoragesForTemplate(t *testing.T) {
	svc := &Service{}
	vm := vmModels.VM{
		Storages: []vmModels.Storage{
			{ID: 1, Type: vmModels.VMStorageTypeRaw, BootOrder: 4},
			{ID: 2, Type: vmModels.VMStorageTypeDiskImage, BootOrder: 1},
			{ID: 3, Type: vmModels.VMStorageTypeZVol, BootOrder: 2},
		},
	}

	out := svc.sourceVMStoragesForTemplate(vm)
	if len(out) != 2 {
		t.Fatalf("expected 2 cloneable storages, got %d", len(out))
	}
	if out[0].ID != 3 || out[1].ID != 1 {
		t.Fatalf("expected zvol/raw sorted by boot order, got %#v", out)
	}
}

func TestCreateVMsFromTemplateStopsOnFirstFailure(t *testing.T) {
	svc := &Service{}
	calls := make([]uint, 0, 2)

	svc.preflightCreateVMTemplateFn = func(
		context.Context,
		uint,
		libvirtServiceInterfaces.CreateFromTemplateRequest,
	) (vmTemplateCreatePlan, error) {
		return vmTemplateCreatePlan{
			Template: vmModels.VMTemplate{ID: 77},
			Targets: []vmTemplateCreateTarget{
				{RID: 501, Name: "vm-501"},
				{RID: 502, Name: "vm-502"},
			},
			StoragePools: map[uint]string{},
		}, nil
	}

	svc.createVMTemplateTargetFn = func(
		context.Context,
		vmModels.VMTemplate,
		vmTemplateCreateTarget,
		map[uint]string,
		libvirtServiceInterfaces.CreateFromTemplateRequest,
	) error {
		calls = append(calls, 1)
		if len(calls) == 1 {
			return templateTestError{msg: "boom"}
		}
		return nil
	}

	err := svc.CreateVMsFromTemplate(context.Background(), 77, libvirtServiceInterfaces.CreateFromTemplateRequest{
		Mode: "multiple",
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected first target failure, got %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected create to stop on first failure, calls=%d", len(calls))
	}
}

type templateTestError struct {
	msg string
}

func (e templateTestError) Error() string {
	return e.msg
}
