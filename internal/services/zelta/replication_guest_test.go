// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"errors"
	"reflect"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
)

type stubVMService struct {
	libvirtServiceInterfaces.LibvirtServiceInterface
	shutOff    bool
	shutOffErr error
}

func (s stubVMService) IsDomainShutOff(_ uint) (bool, error) {
	return s.shutOff, s.shutOffErr
}

type retirementVMStub struct {
	libvirtServiceInterfaces.LibvirtServiceInterface
	retired bool
}

func (s *retirementVMStub) RetireVMLocalMetadata(_ uint, _ bool) error {
	s.retired = true
	return nil
}

type stubJailService struct {
	jailServiceInterfaces.JailServiceInterface
	running    bool
	runningErr error
}

func (s stubJailService) IsJailRunning(_ uint) (bool, error) {
	return s.running, s.runningErr
}

func TestReplicationGuestDriver(t *testing.T) {
	s := &Service{}

	driver, err := s.replicationGuestDriver(clusterModels.ReplicationGuestTypeVM)
	if err != nil {
		t.Fatalf("vm driver: %v", err)
	}
	if _, ok := driver.(vmReplicationGuestDriver); !ok {
		t.Fatalf("expected vmReplicationGuestDriver, got %T", driver)
	}

	driver, err = s.replicationGuestDriver(clusterModels.ReplicationGuestTypeJail)
	if err != nil {
		t.Fatalf("jail driver: %v", err)
	}
	if _, ok := driver.(jailReplicationGuestDriver); !ok {
		t.Fatalf("expected jailReplicationGuestDriver, got %T", driver)
	}

	_, err = s.replicationGuestDriver("invalid")
	if err == nil {
		t.Fatal("expected error for invalid guest type")
	}
}

func TestRequireSupportedReplicationVMStorages(t *testing.T) {
	if err := requireSupportedReplicationVMStorages([]vmModels.Storage{
		{Type: vmModels.VMStorageTypeZVol, Enable: true},
		{Type: vmModels.VMStorageTypeFilesystem, Enable: false},
	}); err != nil {
		t.Fatalf("disabled filesystem storage was rejected: %v", err)
	}
	if err := requireSupportedReplicationVMStorages([]vmModels.Storage{
		{Type: vmModels.VMStorageTypeFilesystem, Enable: true},
	}); !errors.Is(err, errReplicationVMFilesystemStorageUnsupported) {
		t.Fatalf("enabled filesystem storage returned %v", err)
	}
}

func TestVMReplicationSourcesIgnoreLegacyISOPool(t *testing.T) {
	service := newTestZeltaService(newZeltaServiceTestDB(t))
	service.localFilesystemDatasetLister = func(context.Context) ([]string, error) {
		return []string{
			"tank/sylve/virtual-machines/107",
			"stale/sylve/virtual-machines/107",
		}, nil
	}
	driver := vmReplicationGuestDriver{service: service}
	sources, err := driver.replicationSourceDatasets(context.Background(), &vmModels.VM{
		RID: 107,
		Storages: []vmModels.Storage{
			{Type: vmModels.VMStorageTypeRaw, Pool: "tank", Enable: true},
			{
				Type: vmModels.VMStorageTypeDiskImage, Pool: "stale", Enable: true,
				DownloadUUID: "iso-107", Emulation: vmModels.AHCICDStorageEmulation,
			},
		},
	})
	if err != nil {
		t.Fatalf("discover VM replication sources: %v", err)
	}
	want := []string{"tank/sylve/virtual-machines/107"}
	if !reflect.DeepEqual(sources, want) {
		t.Fatalf("replication sources = %v, want %v", sources, want)
	}
}

func TestVMReplicationSourcesIgnoreRetainedGenerations(t *testing.T) {
	service := newTestZeltaService(newZeltaServiceTestDB(t))
	service.localFilesystemDatasetLister = func(context.Context) ([]string, error) {
		return []string{
			"zroot/sylve/virtual-machines/107",
			"zroot/sylve/virtual-machines/107_previous-replication-6633921964961922-mrmb3i63-2",
			"zroot/sylve/virtual-machines/107_previous-replication-6633921964961922-mrmb9yr1-2",
		}, nil
	}
	driver := vmReplicationGuestDriver{service: service}
	sources, err := driver.replicationSourceDatasets(context.Background(), &vmModels.VM{
		RID: 107,
		Storages: []vmModels.Storage{
			{Type: vmModels.VMStorageTypeZVol, Pool: "zroot", Enable: true},
		},
	})
	if err != nil {
		t.Fatalf("discover VM replication sources: %v", err)
	}
	want := []string{"zroot/sylve/virtual-machines/107"}
	if !reflect.DeepEqual(sources, want) {
		t.Fatalf("replication sources = %v, want %v", sources, want)
	}
}

func TestSelfFencePreservesMigrationCutoverTargetRegistration(t *testing.T) {
	db := newZeltaServiceTestDB(t, &vmModels.VM{}, &clusterModels.ReplicationGuestOperation{})
	if err := db.Create(&vmModels.VM{RID: 107, Name: "migration-target"}).Error; err != nil {
		t.Fatalf("create VM registration: %v", err)
	}
	if err := db.Create(&clusterModels.ReplicationGuestOperation{
		GuestType:    clusterModels.ReplicationGuestTypeVM,
		GuestID:      107,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		State:        clusterModels.ReplicationGuestOperationCutover,
		Token:        "migration:source:1",
		OwnerNodeID:  "node-a",
		TargetNodeID: "node-b",
	}).Error; err != nil {
		t.Fatalf("create migration cutover operation: %v", err)
	}

	vm := &retirementVMStub{}
	service := &Service{DB: db, VM: vm}
	service.selfFenceReplicationPolicy(context.Background(), &clusterModels.ReplicationPolicy{
		ID:        1,
		GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID:   107,
	}, "node-b", "node-a", replicationFenceReasonPolicyOwnerMismatch, true)

	if vm.retired {
		t.Fatal("migration cutover target VM registration was retired")
	}
	var count int64
	if err := db.Model(&vmModels.VM{}).Where("rid = ?", 107).Count(&count).Error; err != nil {
		t.Fatalf("count VM registrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("VM registration count = %d, want 1", count)
	}
}

func TestIsReplicationGuestRunning(t *testing.T) {
	t.Run("vm running", func(t *testing.T) {
		s := &Service{VM: stubVMService{shutOff: false}}
		running, err := s.isReplicationGuestRunning(clusterModels.ReplicationGuestTypeVM, 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !running {
			t.Fatal("expected running when domain is not shut off")
		}
	})

	t.Run("vm shutoff", func(t *testing.T) {
		s := &Service{VM: stubVMService{shutOff: true}}
		running, err := s.isReplicationGuestRunning(clusterModels.ReplicationGuestTypeVM, 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if running {
			t.Fatal("expected not running when domain is shut off")
		}
	})

	t.Run("vm service nil", func(t *testing.T) {
		s := &Service{VM: nil}
		running, err := s.isReplicationGuestRunning(clusterModels.ReplicationGuestTypeVM, 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if running {
			t.Fatal("expected not running when VM service is nil")
		}
	})

	t.Run("vm domain not found treated as not running", func(t *testing.T) {
		err := errors.New("virDomainGetInfo: Domain not found: no domain with matching id 100")
		s := &Service{VM: stubVMService{shutOffErr: err}}
		running, checkErr := s.isReplicationGuestRunning(clusterModels.ReplicationGuestTypeVM, 100)
		if checkErr != nil {
			t.Fatalf("domain not found should not error: %v", checkErr)
		}
		if running {
			t.Fatal("domain not found should not be running")
		}
	})

	t.Run("vm real error propagated", func(t *testing.T) {
		s := &Service{VM: stubVMService{shutOffErr: errors.New("connection refused")}}
		_, err := s.isReplicationGuestRunning(clusterModels.ReplicationGuestTypeVM, 100)
		if err == nil {
			t.Fatal("expected error propagation for non-domain-not-found errors")
		}
	})

	t.Run("jail running", func(t *testing.T) {
		s := &Service{Jail: stubJailService{running: true}}
		running, err := s.isReplicationGuestRunning(clusterModels.ReplicationGuestTypeJail, 200)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !running {
			t.Fatal("expected running")
		}
	})

	t.Run("jail not running", func(t *testing.T) {
		s := &Service{Jail: stubJailService{running: false}}
		running, err := s.isReplicationGuestRunning(clusterModels.ReplicationGuestTypeJail, 200)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if running {
			t.Fatal("expected not running")
		}
	})

	t.Run("jail service nil", func(t *testing.T) {
		s := &Service{Jail: nil}
		running, err := s.isReplicationGuestRunning(clusterModels.ReplicationGuestTypeJail, 200)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if running {
			t.Fatal("expected not running when jail service is nil")
		}
	})

	t.Run("unknown guest type", func(t *testing.T) {
		s := &Service{}
		running, err := s.isReplicationGuestRunning("unknown", 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if running {
			t.Fatal("unknown guest type should not be running")
		}
	})
}

func TestIsReplicationGuestIntentionallyStopped(t *testing.T) {
	t.Run("vm intentionally stopped", func(t *testing.T) {
		db := newZeltaServiceTestDB(t, &vmModels.VM{})
		s := &Service{DB: db}

		db.Create(&vmModels.VM{RID: 100, IntentionallyStopped: true})

		stopped, err := s.isReplicationGuestIntentionallyStopped(clusterModels.ReplicationGuestTypeVM, 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !stopped {
			t.Fatal("expected intentionally stopped")
		}
	})

	t.Run("vm not intentionally stopped", func(t *testing.T) {
		db := newZeltaServiceTestDB(t, &vmModels.VM{})
		s := &Service{DB: db}

		db.Create(&vmModels.VM{RID: 100, IntentionallyStopped: false})

		stopped, err := s.isReplicationGuestIntentionallyStopped(clusterModels.ReplicationGuestTypeVM, 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if stopped {
			t.Fatal("expected not intentionally stopped")
		}
	})

	t.Run("vm not found returns false", func(t *testing.T) {
		db := newZeltaServiceTestDB(t, &vmModels.VM{})
		s := &Service{DB: db}

		stopped, err := s.isReplicationGuestIntentionallyStopped(clusterModels.ReplicationGuestTypeVM, 999)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if stopped {
			t.Fatal("non-existent VM should not be stopped")
		}
	})

	t.Run("jail intentionally stopped", func(t *testing.T) {
		db := newZeltaServiceTestDB(t, &jailModels.Jail{})
		s := &Service{DB: db}

		db.Create(&jailModels.Jail{CTID: 50, IntentionallyStopped: true})

		stopped, err := s.isReplicationGuestIntentionallyStopped(clusterModels.ReplicationGuestTypeJail, 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !stopped {
			t.Fatal("expected intentionally stopped jail")
		}
	})

	t.Run("unknown guest type", func(t *testing.T) {
		s := &Service{DB: newZeltaServiceTestDB(t)}
		stopped, err := s.isReplicationGuestIntentionallyStopped("unknown", 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if stopped {
			t.Fatal("unknown guest type should not be intentionally stopped")
		}
	})
}

func TestReplicationGuestExistsLocally(t *testing.T) {
	t.Run("vm exists", func(t *testing.T) {
		db := newZeltaServiceTestDB(t, &vmModels.VM{})
		s := &Service{DB: db}

		db.Create(&vmModels.VM{RID: 100})

		if !s.replicationGuestExistsLocally(clusterModels.ReplicationGuestTypeVM, 100) {
			t.Fatal("expected vm to exist")
		}
	})

	t.Run("vm does not exist", func(t *testing.T) {
		db := newZeltaServiceTestDB(t, &vmModels.VM{})
		s := &Service{DB: db}

		if s.replicationGuestExistsLocally(clusterModels.ReplicationGuestTypeVM, 999) {
			t.Fatal("expected vm to not exist")
		}
	})

	t.Run("jail exists", func(t *testing.T) {
		db := newZeltaServiceTestDB(t, &jailModels.Jail{})
		s := &Service{DB: db}

		db.Create(&jailModels.Jail{CTID: 50})

		if !s.replicationGuestExistsLocally(clusterModels.ReplicationGuestTypeJail, 50) {
			t.Fatal("expected jail to exist")
		}
	})

	t.Run("nil service returns false", func(t *testing.T) {
		var s *Service
		if s.replicationGuestExistsLocally(clusterModels.ReplicationGuestTypeVM, 100) {
			t.Fatal("nil service should return false")
		}
	})

	t.Run("zero guestID returns false", func(t *testing.T) {
		s := &Service{DB: newZeltaServiceTestDB(t)}
		if s.replicationGuestExistsLocally(clusterModels.ReplicationGuestTypeVM, 0) {
			t.Fatal("zero guestID should return false")
		}
	})
}
