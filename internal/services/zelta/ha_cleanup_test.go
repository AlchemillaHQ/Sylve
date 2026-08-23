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
	"fmt"
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
)

type orphanCleanupStubVM struct {
	libvirtServiceInterfaces.LibvirtServiceInterface
	domainErr   error
	purgeCalled bool
	purgedRID   uint
}

func (s *orphanCleanupStubVM) GetLvDomain(rid uint) (*libvirtServiceInterfaces.LvDomain, error) {
	if s.domainErr != nil {
		return nil, s.domainErr
	}
	return &libvirtServiceInterfaces.LvDomain{ID: -1, Name: fmt.Sprintf("%d", rid)}, nil
}

func (s *orphanCleanupStubVM) PurgeVMRegistration(rid uint, _ bool) ([]string, error) {
	s.purgeCalled = true
	s.purgedRID = rid
	return nil, nil
}

func TestCleanupOrphanedVMRegistration(t *testing.T) {
	domainAbsent := errors.New("failed_to_lookup_domain: Domain not found: no domain with matching name '700'")
	libvirtDown := errors.New("failed to connect: dial unix /var/run/libvirt/libvirt-sock: no such file or directory")

	cases := []struct {
		name      string
		seedRID   uint // 0 = no record
		queryRID  uint
		domainErr error
		wantPurge bool
	}{
		{name: "orphan (record + domain absent)", seedRID: 700, queryRID: 700, domainErr: domainAbsent, wantPurge: true},
		{name: "not orphan (record + domain present)", seedRID: 701, queryRID: 701, domainErr: nil, wantPurge: false},
		{name: "no record", seedRID: 0, queryRID: 702, domainErr: domainAbsent, wantPurge: false},
		{name: "libvirt unreachable (cannot confirm)", seedRID: 703, queryRID: 703, domainErr: libvirtDown, wantPurge: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := testutil.NewSQLiteTestDB(t, &vmModels.VM{})
			if tc.seedRID != 0 {
				if err := db.Create(&vmModels.VM{RID: tc.seedRID, Name: "g"}).Error; err != nil {
					t.Fatalf("seed vm: %v", err)
				}
			}

			stub := &orphanCleanupStubVM{domainErr: tc.domainErr}
			s := &Service{DB: db, VM: stub}

			s.cleanupOrphanedVMRegistration(tc.queryRID)

			if stub.purgeCalled != tc.wantPurge {
				t.Fatalf("purgeCalled=%v, want %v", stub.purgeCalled, tc.wantPurge)
			}
			if tc.wantPurge && stub.purgedRID != tc.queryRID {
				t.Fatalf("purged rid=%d, want %d", stub.purgedRID, tc.queryRID)
			}
		})
	}
}

func TestMigrateGuestOwnership_InputGuards(t *testing.T) {
	s := &Service{}

	if err := s.MigrateGuestOwnership(context.Background(), "", 0, ""); err == nil {
		t.Fatal("expected error for empty inputs")
	}

	if err := s.MigrateGuestOwnership(context.Background(), "vm", 100, "node-b"); err == nil {
		t.Fatal("expected error when cluster service is unavailable")
	}
}
