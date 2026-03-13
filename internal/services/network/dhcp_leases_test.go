// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"fmt"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
)

func TestMapDBErrMapsDuplicateConstraintErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "sqlite mac unique",
			err:  fmt.Errorf("UNIQUE constraint failed: dhcp_static_leases.mac_object_id, dhcp_static_leases.dhcp_range_id"),
			want: "duplicate_mac_in_range",
		},
		{
			name: "named duid constraint",
			err:  fmt.Errorf(`duplicate key value violates unique constraint "uniq_l_duid_per_range"`),
			want: "duplicate_duid_in_range",
		},
		{
			name: "named ip constraint",
			err:  fmt.Errorf(`duplicate key value violates unique constraint "uniq_l_ip_per_range"`),
			want: "duplicate_ip_in_range",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapDBErr(tt.err)
			if got == nil {
				t.Fatal("expected mapped error, got nil")
			}
			if got.Error() != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got.Error())
			}
		})
	}
}

func TestCreateStaticMapRejectsIPFamilyMismatch(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.DHCPRange{},
		&networkModels.DHCPStaticLease{},
	)

	ipv6Host := networkModels.Object{
		Name: "host-v6",
		Type: "Host",
		Entries: []networkModels.ObjectEntry{
			{Value: "2001:db8::10"},
		},
	}
	if err := db.Create(&ipv6Host).Error; err != nil {
		t.Fatalf("failed to seed IPv6 host object: %v", err)
	}

	macObj := networkModels.Object{
		Name: "mac-1",
		Type: "Mac",
		Entries: []networkModels.ObjectEntry{
			{Value: "aa:bb:cc:dd:ee:ff"},
		},
	}
	if err := db.Create(&macObj).Error; err != nil {
		t.Fatalf("failed to seed mac object: %v", err)
	}

	rng := networkModels.DHCPRange{
		Type:    "ipv4",
		StartIP: "10.0.0.10",
		EndIP:   "10.0.0.20",
	}
	if err := db.Create(&rng).Error; err != nil {
		t.Fatalf("failed to seed dhcp range: %v", err)
	}

	err := svc.CreateStaticMap(&networkServiceInterfaces.CreateStaticMapRequest{
		Hostname:    "client-a",
		DHCPRangeID: rng.ID,
		IPObjectID:  &ipv6Host.ID,
		MACObjectID: &macObj.ID,
	})
	if err == nil {
		t.Fatal("expected invalid_ip_object_family error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid_ip_object_family") {
		t.Fatalf("expected invalid_ip_object_family in error, got %q", err.Error())
	}
}
