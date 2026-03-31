// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"fmt"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
)

type jailNetworkValidationFakeNetworkService struct {
	entries        map[uint]string
	syncEpairsCall int
}

func (f *jailNetworkValidationFakeNetworkService) SyncStandardSwitches(_ *networkModels.StandardSwitch, _ string) error {
	return nil
}

func (f *jailNetworkValidationFakeNetworkService) GetStandardSwitches() ([]networkModels.StandardSwitch, error) {
	return nil, nil
}

func (f *jailNetworkValidationFakeNetworkService) NewStandardSwitch(
	_ string,
	_ int,
	_ int,
	_ uint,
	_ uint,
	_ uint,
	_ uint,
	_ []string,
	_ bool,
	_ bool,
	_ bool,
	_ bool,
	_ bool,
) error {
	return nil
}

func (f *jailNetworkValidationFakeNetworkService) EditStandardSwitch(
	_ uint,
	_ int,
	_ int,
	_ uint,
	_ uint,
	_ uint,
	_ uint,
	_ []string,
	_ bool,
	_ bool,
	_ bool,
	_ bool,
	_ bool,
) error {
	return nil
}

func (f *jailNetworkValidationFakeNetworkService) DeleteStandardSwitch(_ int) error {
	return nil
}

func (f *jailNetworkValidationFakeNetworkService) IsObjectUsed(_ uint) (bool, string, error) {
	return false, "", nil
}

func (f *jailNetworkValidationFakeNetworkService) GetObjectEntryByID(id uint) (string, error) {
	entry, ok := f.entries[id]
	if !ok {
		return "", fmt.Errorf("entry_not_found")
	}
	return entry, nil
}

func (f *jailNetworkValidationFakeNetworkService) GetBridgeNameByIDType(_ uint, _ string) (string, error) {
	return "vm-test", nil
}

func (f *jailNetworkValidationFakeNetworkService) CreateEpair(_ string) error {
	return nil
}

func (f *jailNetworkValidationFakeNetworkService) SyncEpairs(_ bool) error {
	f.syncEpairsCall++
	return nil
}

func (f *jailNetworkValidationFakeNetworkService) DeleteEpair(_ string) error {
	return nil
}

func (f *jailNetworkValidationFakeNetworkService) RegisterOnJailObjectUpdateCallback(_ func(jailIDs []uint)) {
}

func TestAddNetworkRejectsUnassignableIPv4CIDRBeforeSync(t *testing.T) {
	requireSystemUUIDOrSkip(t)

	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Network{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
	)

	fakeNetwork := &jailNetworkValidationFakeNetworkService{entries: map[uint]string{}}
	svc := &Service{DB: db, NetworkService: fakeNetwork}

	jail := jailModels.Jail{CTID: 9101, Name: "jail-validation-add", Type: jailModels.JailTypeFreeBSD}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("failed to seed jail: %v", err)
	}

	sw := networkModels.StandardSwitch{Name: "sw-validation-add", BridgeName: "vm-sw-validation-add"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed standard switch: %v", err)
	}

	ip4Obj := networkModels.Object{Name: "jv-ip4", Type: "Network"}
	if err := db.Create(&ip4Obj).Error; err != nil {
		t.Fatalf("failed to seed ipv4 object: %v", err)
	}
	if err := db.Create(&networkModels.ObjectEntry{ObjectID: ip4Obj.ID, Value: "10.80.0.0/24"}).Error; err != nil {
		t.Fatalf("failed to seed ipv4 object entry: %v", err)
	}

	gw4Obj := networkModels.Object{Name: "jv-gw4", Type: "Host"}
	if err := db.Create(&gw4Obj).Error; err != nil {
		t.Fatalf("failed to seed ipv4 gateway object: %v", err)
	}
	if err := db.Create(&networkModels.ObjectEntry{ObjectID: gw4Obj.ID, Value: "10.80.0.1"}).Error; err != nil {
		t.Fatalf("failed to seed ipv4 gateway entry: %v", err)
	}

	fakeNetwork.entries[ip4Obj.ID] = "10.80.0.0/24"
	fakeNetwork.entries[gw4Obj.ID] = "10.80.0.1"

	ip4 := ip4Obj.ID
	gw4 := gw4Obj.ID
	req := jailServiceInterfaces.AddJailNetworkRequest{
		CTID:       jail.CTID,
		Name:       "net-add",
		SwitchName: sw.Name,
		IP4:        &ip4,
		IP4GW:      &gw4,
	}

	err := svc.AddNetwork(req)
	if err == nil {
		t.Fatal("expected add network validation error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid_ip4_cidr_not_assignable") {
		t.Fatalf("expected invalid_ip4_cidr_not_assignable, got %v", err)
	}
	if fakeNetwork.syncEpairsCall != 0 {
		t.Fatalf("expected no SyncEpairs call on validation failure, got %d", fakeNetwork.syncEpairsCall)
	}

	var count int64
	if err := db.Model(&jailModels.Network{}).Count(&count).Error; err != nil {
		t.Fatalf("failed counting jail networks: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no jail networks to be created, found %d", count)
	}
}

func TestEditNetworkRejectsUnassignableIPv6CIDRBeforeSync(t *testing.T) {
	requireSystemUUIDOrSkip(t)

	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Network{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
	)

	fakeNetwork := &jailNetworkValidationFakeNetworkService{entries: map[uint]string{}}
	svc := &Service{DB: db, NetworkService: fakeNetwork}

	jail := jailModels.Jail{CTID: 9102, Name: "jail-validation-edit", Type: jailModels.JailTypeFreeBSD}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("failed to seed jail: %v", err)
	}

	sw := networkModels.StandardSwitch{Name: "sw-validation-edit", BridgeName: "vm-sw-validation-edit"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed standard switch: %v", err)
	}

	existing := jailModels.Network{
		JailID:     jail.ID,
		Name:       "net-edit",
		SwitchID:   sw.ID,
		SwitchType: "standard",
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatalf("failed to seed existing jail network: %v", err)
	}

	ip6Obj := networkModels.Object{Name: "jv-ip6", Type: "Network"}
	if err := db.Create(&ip6Obj).Error; err != nil {
		t.Fatalf("failed to seed ipv6 object: %v", err)
	}
	if err := db.Create(&networkModels.ObjectEntry{ObjectID: ip6Obj.ID, Value: "2001:db8::/64"}).Error; err != nil {
		t.Fatalf("failed to seed ipv6 object entry: %v", err)
	}

	gw6Obj := networkModels.Object{Name: "jv-gw6", Type: "Host"}
	if err := db.Create(&gw6Obj).Error; err != nil {
		t.Fatalf("failed to seed ipv6 gateway object: %v", err)
	}
	if err := db.Create(&networkModels.ObjectEntry{ObjectID: gw6Obj.ID, Value: "2001:db8::1"}).Error; err != nil {
		t.Fatalf("failed to seed ipv6 gateway entry: %v", err)
	}

	fakeNetwork.entries[ip6Obj.ID] = "2001:db8::/64"
	fakeNetwork.entries[gw6Obj.ID] = "2001:db8::1"

	ip6 := ip6Obj.ID
	gw6 := gw6Obj.ID
	req := jailServiceInterfaces.EditJailNetworkRequest{
		NetworkID:  existing.ID,
		Name:       "net-edit-updated",
		SwitchName: sw.Name,
		IP6:        &ip6,
		IP6GW:      &gw6,
	}

	err := svc.EditNetwork(req)
	if err == nil {
		t.Fatal("expected edit network validation error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid_ip6_cidr_not_assignable") {
		t.Fatalf("expected invalid_ip6_cidr_not_assignable, got %v", err)
	}
	if fakeNetwork.syncEpairsCall != 0 {
		t.Fatalf("expected no SyncEpairs call on validation failure, got %d", fakeNetwork.syncEpairsCall)
	}

	var refreshed jailModels.Network
	if err := db.First(&refreshed, existing.ID).Error; err != nil {
		t.Fatalf("failed to reload existing jail network: %v", err)
	}
	if refreshed.Name != "net-edit" {
		t.Fatalf("expected existing jail network to remain unchanged, got %q", refreshed.Name)
	}
}
