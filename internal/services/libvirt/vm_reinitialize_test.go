// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/digitalocean/go-libvirt"
)

type vmConfigConnectionStub struct {
	lookupErrors []error
	lookupNames  []string
	definedXML   string
	defineErr    error
}

func (c *vmConfigConnectionStub) DomainLookupByName(name string) (libvirt.Domain, error) {
	index := len(c.lookupNames)
	c.lookupNames = append(c.lookupNames, name)
	if index < len(c.lookupErrors) {
		return libvirt.Domain{Name: name}, c.lookupErrors[index]
	}
	return libvirt.Domain{}, libvirt.Error{Code: uint32(libvirt.ErrNoDomain), Message: "domain not found"}
}

func (c *vmConfigConnectionStub) DomainDefineXML(xml string) (libvirt.Domain, error) {
	c.definedXML = xml
	return libvirt.Domain{}, c.defineErr
}

func (c *vmConfigConnectionStub) connect() (vmConfigConnection, error) {
	return c, nil
}

func newVMConfigTestService(t *testing.T) (*Service, vmModels.VM) {
	t.Helper()
	requireSystemUUIDOrSkip(t)
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	db := newVMDeleteTestDB(t)
	if err := db.AutoMigrate(&taskModels.GuestLifecycleTask{}, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationLease{}); err != nil {
		t.Fatalf("migrate config recovery guards: %v", err)
	}
	vm := vmModels.VM{
		ID: 44, RID: 916, Name: "orphan", BootROM: vmModels.VMBootROMNone,
		CPUSockets: 1, CPUCores: 2, CPUThreads: 1, RAM: 268435456,
		VNCEnabled: true, VNCPort: 5916, VNCResolution: "1024x768", Serial: true,
	}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}
	return &Service{DB: db}, vm
}

func TestReinitializeVMConfigPreservesStorageAndRuntimeState(t *testing.T) {
	service, vm := newVMConfigTestService(t)
	if hostUsesSplitFirmware() {
		if err := service.DB.Model(&vm).Update("boot_rom", vmModels.VMBootROMUEFI).Error; err != nil {
			t.Fatalf("enable UEFI: %v", err)
		}
	}
	vmPath, err := service.GetVMConfigDirectory(vm.RID)
	if err != nil {
		t.Fatalf("get runtime directory: %v", err)
	}
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatalf("create runtime directory: %v", err)
	}
	diskDir := t.TempDir()
	files := map[string]string{
		filepath.Join(vmPath, "916_vars.fd"):   "existing UEFI variables",
		filepath.Join(vmPath, "916_tpm.state"): "existing TPM state",
		filepath.Join(vmPath, "keep.txt"):      "other runtime state",
		filepath.Join(diskDir, "77.img"):       "existing guest disk data",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}
	rawDataset := vmModels.VMStorageDataset{Pool: "tank", Name: "tank/sylve/virtual-machines/916/raw-77"}
	zvolDataset := vmModels.VMStorageDataset{Pool: "tank", Name: "tank/sylve/virtual-machines/916/zvol-78"}
	for _, dataset := range []*vmModels.VMStorageDataset{&rawDataset, &zvolDataset} {
		if err := service.DB.Create(dataset).Error; err != nil {
			t.Fatalf("seed dataset: %v", err)
		}
	}
	for _, storage := range []vmModels.Storage{
		{ID: 77, VMID: vm.ID, Type: vmModels.VMStorageTypeRaw, Enable: true, Emulation: vmModels.VirtIOStorageEmulation, DatasetID: &rawDataset.ID},
		{ID: 78, VMID: vm.ID, Type: vmModels.VMStorageTypeZVol, Enable: true, Emulation: vmModels.NVMEStorageEmulation, DatasetID: &zvolDataset.ID},
	} {
		if err := service.DB.Create(&storage).Error; err != nil {
			t.Fatalf("seed storage: %v", err)
		}
	}
	runner := &storageTestZFSRunner{datasets: map[string]storageTestDataset{
		rawDataset.Name: {name: rawDataset.Name, pool: "tank", kind: gzfs.DatasetTypeFilesystem, mountpoint: diskDir},
	}}
	service.GZFS = gzfs.NewClient(gzfs.Options{Runner: runner})
	before, err := service.GetVMByRID(vm.RID)
	if err != nil {
		t.Fatalf("read saved VM: %v", err)
	}
	conn := &vmConfigConnectionStub{}
	if err := service.reinitializeVMConfig(vm.RID, t.Context(), conn.connect); err != nil {
		t.Fatalf("reinitialize VM config: %v", err)
	}
	for _, fragment := range []string{
		"<name>916</name>", "<vcpu>2</vcpu>", "<memory unit=\"B\">268435456</memory>",
		filepath.Join(diskDir, "77.img"), "/dev/zvol/" + zvolDataset.Name, "/dev/nmdm916A",
	} {
		if !strings.Contains(conn.definedXML, fragment) {
			t.Fatalf("restored XML is missing %q: %s", fragment, conn.definedXML)
		}
	}
	if !reflect.DeepEqual(conn.lookupNames, []string{"916", "916"}) {
		t.Fatalf("domain lookups = %v, want RID checked twice", conn.lookupNames)
	}
	for path, content := range files {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != content {
			t.Fatalf("existing state changed at %s: %q, %v", path, data, err)
		}
	}
	for _, command := range runner.commands {
		if len(command) > 0 && command[0] != "list" && command[0] != "get" {
			t.Fatalf("recovery mutated ZFS: %v", command)
		}
	}
	after, err := service.GetVMByRID(vm.RID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("saved VM metadata changed: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestReinitializeVMConfigRebuildsCloudInitWithoutFlashingOrDeletingMedia(t *testing.T) {
	if _, err := os.Stat("/usr/sbin/makefs"); err != nil {
		t.Skip("makefs unavailable")
	}
	service, vm := newVMConfigTestService(t)
	if err := service.DB.Model(&vm).Updates(map[string]any{
		"cloud_init_data": "#cloud-config\nusers: []", "cloud_init_meta_data": "instance-id: vm-916",
	}).Error; err != nil {
		t.Fatalf("seed cloud-init data: %v", err)
	}
	media := vmModels.Storage{VMID: vm.ID, Type: vmModels.VMStorageTypeDiskImage, DownloadUUID: "missing-media", Enable: true}
	if err := service.DB.Create(&media).Error; err != nil {
		t.Fatalf("seed installation media: %v", err)
	}
	conn := &vmConfigConnectionStub{}
	if err := service.reinitializeVMConfig(vm.RID, t.Context(), conn.connect); err != nil {
		t.Fatalf("restore cloud-init config: %v", err)
	}
	isoPath, err := service.GetCloudInitISOPath(vm.RID)
	if err != nil || !strings.Contains(conn.definedXML, isoPath) {
		t.Fatalf("cloud-init ISO was not restored: path=%q err=%v XML=%s", isoPath, err, conn.definedXML)
	}
	var savedMedia vmModels.Storage
	if err := service.DB.First(&savedMedia, media.ID).Error; err != nil {
		t.Fatalf("recovery removed installation media: %v", err)
	}
}

func TestReinitializeVMConfigRefusesExistingOrUnverifiableDomain(t *testing.T) {
	absent := libvirt.Error{Code: uint32(libvirt.ErrNoDomain), Message: "domain not found"}
	for _, test := range []struct {
		name         string
		lookupErrors []error
		defineErr    error
		wantCode     string
		wantDefine   bool
	}{
		{name: "existing definition", lookupErrors: []error{nil}, wantCode: "vm_not_orphaned"},
		{name: "lookup unavailable", lookupErrors: []error{errors.New("connection lost")}, wantCode: "vm_orphan_check_unavailable"},
		{name: "restored during recovery", lookupErrors: []error{absent, nil}, wantCode: "vm_not_orphaned"},
		{name: "recheck unavailable", lookupErrors: []error{absent, errors.New("connection lost")}, wantCode: "vm_orphan_check_unavailable"},
		{name: "define failed", defineErr: errors.New("definition rejected"), wantCode: "failed_to_define_vm_domain", wantDefine: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, vm := newVMConfigTestService(t)
			conn := &vmConfigConnectionStub{lookupErrors: test.lookupErrors, defineErr: test.defineErr}
			err := service.reinitializeVMConfig(vm.RID, t.Context(), conn.connect)
			if err == nil || !strings.Contains(err.Error(), test.wantCode) {
				t.Fatalf("recovery error = %v, want %s", err, test.wantCode)
			}
			if (conn.definedXML != "") != test.wantDefine {
				t.Fatalf("definition attempted = %t, want %t", conn.definedXML != "", test.wantDefine)
			}
			if mustCountRows[vmModels.VM](t, service.DB) != 1 {
				t.Fatal("failed recovery removed the VM registration")
			}
		})
	}
}

func TestReinitializeVMConfigPreflightBeforeLibvirt(t *testing.T) {
	for _, test := range []struct {
		name       string
		rid        uint
		taskStatus string
		protected  bool
		wantCode   string
	}{
		{name: "invalid RID", rid: 0, wantCode: "invalid_vm_rid"},
		{name: "missing registration", rid: 999, wantCode: "vm_not_found"},
		{name: "queued action", rid: 916, taskStatus: taskModels.LifecycleTaskStatusQueued, wantCode: "lifecycle_task_in_progress"},
		{name: "running action", rid: 916, taskStatus: taskModels.LifecycleTaskStatusRunning, wantCode: "lifecycle_task_in_progress"},
		{name: "owned by another node", rid: 916, protected: true, wantCode: "replication_lease_not_owned"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, vm := newVMConfigTestService(t)
			if test.taskStatus != "" {
				if err := service.DB.Create(&taskModels.GuestLifecycleTask{
					GuestType: taskModels.GuestTypeVM, GuestID: vm.RID, Action: "start", Status: test.taskStatus,
				}).Error; err != nil {
					t.Fatalf("seed lifecycle task: %v", err)
				}
			}
			if test.protected {
				if err := service.DB.Create(&clusterModels.ReplicationPolicy{
					Name: "protected", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: vm.RID,
					Enabled: true, ActiveNodeID: "other-node", OwnerEpoch: 1,
				}).Error; err != nil {
					t.Fatalf("seed replication protection: %v", err)
				}
			}
			connected := false
			err := service.reinitializeVMConfig(test.rid, t.Context(), func() (vmConfigConnection, error) {
				connected = true
				return nil, errors.New("unexpected connection")
			})
			if err == nil || !strings.Contains(err.Error(), test.wantCode) || connected {
				t.Fatalf("preflight error = %v, connected=%t; want %s before connecting", err, connected, test.wantCode)
			}
		})
	}
}

func TestReinitializeVMConfigRechecksLifecycleTasksBeforeDefining(t *testing.T) {
	service, vm := newVMConfigTestService(t)
	conn := &vmConfigConnectionStub{}
	err := service.reinitializeVMConfig(vm.RID, t.Context(), func() (vmConfigConnection, error) {
		if err := service.DB.Create(&taskModels.GuestLifecycleTask{
			GuestType: taskModels.GuestTypeVM, GuestID: vm.RID, Action: "start", Status: taskModels.LifecycleTaskStatusQueued,
		}).Error; err != nil {
			t.Fatalf("queue competing action: %v", err)
		}
		return conn, nil
	})
	if err == nil || !strings.Contains(err.Error(), "lifecycle_task_in_progress") || conn.definedXML != "" {
		t.Fatalf("recovery did not reject the newly queued action: error=%v XML=%s", err, conn.definedXML)
	}
}

func TestReinitializeVMConfigFailsClosedWhenLibvirtIsUnavailable(t *testing.T) {
	service, vm := newVMConfigTestService(t)
	err := service.reinitializeVMConfig(vm.RID, t.Context(), func() (vmConfigConnection, error) {
		return nil, errors.New("connection refused")
	})
	if err == nil || !strings.Contains(err.Error(), "libvirt_connection_unavailable") {
		t.Fatalf("connection error = %v, want libvirt_connection_unavailable", err)
	}
	vmPath, err := service.GetVMConfigDirectory(vm.RID)
	if err != nil {
		t.Fatalf("get runtime path: %v", err)
	}
	if _, err := os.Stat(vmPath); !os.IsNotExist(err) {
		t.Fatalf("unavailable libvirt caused runtime files to be created: %v", err)
	}
}

type vmConfigIdentityCoordinatorStub struct {
	clusterServiceInterfaces.GuestIdentityCoordinator
	claimErr      error
	validationErr error
	validations   int
	cancelled     bool
}

func (c *vmConfigIdentityCoordinatorStub) GuestIdentityClaim(
	_ context.Context, guestKind string, guestID uint, _ string,
) (clusterServiceInterfaces.GuestIdentityReservation, error) {
	return clusterServiceInterfaces.GuestIdentityReservation{
		OwnerNodeID: "local-node", Token: "existing-claim",
		Entries: []clusterServiceInterfaces.GuestIdentityReference{{GuestKind: guestKind, GuestID: guestID}},
	}, c.claimErr
}

func (c *vmConfigIdentityCoordinatorStub) ValidateGuestIdentityClaim(
	context.Context, clusterServiceInterfaces.GuestIdentityReservation,
) error {
	c.validations++
	if c.validations == 2 {
		return c.validationErr
	}
	return nil
}

func (c *vmConfigIdentityCoordinatorStub) CancelGuestIdentityClaim(clusterServiceInterfaces.GuestIdentityReservation) {
	c.cancelled = true
}

func TestReinitializeVMConfigValidatesExistingIdentityClaim(t *testing.T) {
	for _, test := range []struct {
		name          string
		claimErr      error
		validationErr error
		wantDefine    bool
	}{
		{name: "retains existing claim", wantDefine: true},
		{name: "another node owns claim", claimErr: errors.New("guest_identity_claim_conflict")},
		{name: "claim changed during recovery", validationErr: errors.New("guest_identity_claim_conflict")},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, vm := newVMConfigTestService(t)
			coordinator := &vmConfigIdentityCoordinatorStub{claimErr: test.claimErr, validationErr: test.validationErr}
			service.SetGuestIdentityCoordinator(coordinator)
			conn := &vmConfigConnectionStub{}
			err := service.reinitializeVMConfig(vm.RID, t.Context(), conn.connect)
			if test.wantDefine && err != nil || !test.wantDefine && (err == nil || !strings.Contains(err.Error(), "guest_identity_claim_conflict")) {
				t.Fatalf("recovery error = %v, wantDefine=%t", err, test.wantDefine)
			}
			if (conn.definedXML != "") != test.wantDefine {
				t.Fatalf("definition attempted = %t, want %t", conn.definedXML != "", test.wantDefine)
			}
			if test.claimErr == nil && (!coordinator.cancelled || coordinator.validations != 2) {
				t.Fatalf("claim not revalidated and unlocked: %+v", coordinator)
			}
		})
	}
}
