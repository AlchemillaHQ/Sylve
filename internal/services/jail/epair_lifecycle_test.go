// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

type jailEpairLifecycleNetworkService struct {
	jailNetworkValidationFakeNetworkService
	ensureCalls []string
	deleteCalls []string
	ensureErrs  map[string]error
	deleteErrs  map[string]error
}

func setJailEpairRuntimeState(t *testing.T, lookup func(*Service, uint) (bool, error)) {
	t.Helper()
	original := jailEpairRuntimeState
	jailEpairRuntimeState = lookup
	t.Cleanup(func() { jailEpairRuntimeState = original })
}

func (s *jailEpairLifecycleNetworkService) EnsureEpair(name string) error {
	s.ensureCalls = append(s.ensureCalls, name)
	return s.ensureErrs[name]
}

func (s *jailEpairLifecycleNetworkService) DeleteEpair(name string) error {
	s.deleteCalls = append(s.deleteCalls, name)
	return s.deleteErrs[name]
}

func seedJailEpairLifecycle(t *testing.T, db *gorm.DB, ctID uint, networkCount int) (jailModels.Jail, []uint) {
	t.Helper()
	jail := jailModels.Jail{CTID: ctID, Name: fmt.Sprintf("jail-%d", ctID)}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("create jail: %v", err)
	}
	for i := 0; i < networkCount; i++ {
		network := jailModels.Network{
			JailID:     jail.ID,
			Name:       fmt.Sprintf("net-%d", i),
			SwitchID:   1,
			SwitchType: "manual",
		}
		if err := db.Create(&network).Error; err != nil {
			t.Fatalf("create jail network: %v", err)
		}
	}
	var ids []uint
	if err := db.Table((jailModels.Network{}).TableName()).
		Where("jid = ?", jail.ID).
		Order("id ASC").
		Pluck("id", &ids).Error; err != nil {
		t.Fatalf("load network ids: %v", err)
	}
	return jail, ids
}

func TestEnsureJailEpairsIsScopedAndOrdered(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &jailModels.Jail{}, &jailModels.Network{})
	jail, ids := seedJailEpairLifecycle(t, db, 7401, 2)
	_, _ = seedJailEpairLifecycle(t, db, 7402, 1)
	network := &jailEpairLifecycleNetworkService{}
	service := &Service{DB: db, NetworkService: network}

	if err := service.ensureJailEpairs(jail); err != nil {
		t.Fatalf("ensure jail epairs: %v", err)
	}
	hash := utils.HashIntToNLetters(int(jail.CTID), 5)
	want := []string{
		fmt.Sprintf("%s_net%d", hash, ids[0]),
		fmt.Sprintf("%s_net%d", hash, ids[1]),
	}
	if fmt.Sprint(network.ensureCalls) != fmt.Sprint(want) {
		t.Fatalf("ensure calls = %v, want %v", network.ensureCalls, want)
	}
}

func TestEnsureJailEpairsStopsAtFirstFailure(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &jailModels.Jail{}, &jailModels.Network{})
	jail, ids := seedJailEpairLifecycle(t, db, 7403, 2)
	hash := utils.HashIntToNLetters(int(jail.CTID), 5)
	first := fmt.Sprintf("%s_net%d", hash, ids[0])
	network := &jailEpairLifecycleNetworkService{ensureErrs: map[string]error{first: errors.New("conflict")}}
	service := &Service{DB: db, NetworkService: network}

	err := service.ensureJailEpairs(jail)
	if err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("ensure jail epairs error = %v", err)
	}
	if fmt.Sprint(network.ensureCalls) != fmt.Sprint([]string{first}) {
		t.Fatalf("ensure calls = %v, want only %s", network.ensureCalls, first)
	}
}

func TestCleanupJailEpairsAttemptsAllAndIgnoresMissing(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &jailModels.Jail{}, &jailModels.Network{})
	jail, ids := seedJailEpairLifecycle(t, db, 7404, 2)
	hash := utils.HashIntToNLetters(int(jail.CTID), 5)
	first := fmt.Sprintf("%s_net%d", hash, ids[0])
	second := fmt.Sprintf("%s_net%d", hash, ids[1])
	network := &jailEpairLifecycleNetworkService{deleteErrs: map[string]error{
		first:  errors.New("epair not found"),
		second: errors.New("destroy failed"),
	}}
	service := &Service{DB: db, NetworkService: network}

	err := service.cleanupJailEpairs(jail)
	if err == nil || !strings.Contains(err.Error(), "destroy failed") || strings.Contains(err.Error(), "not found") {
		t.Fatalf("cleanup jail epairs error = %v", err)
	}
	want := []string{first, second}
	if fmt.Sprint(network.deleteCalls) != fmt.Sprint(want) {
		t.Fatalf("delete calls = %v, want %v", network.deleteCalls, want)
	}
}

func TestCleanupJailEpairsIfStoppedFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		active bool
		err    error
	}{
		{name: "active", active: true},
		{name: "unknown", err: errors.New("jls failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testutil.NewSQLiteTestDB(t, &jailModels.Jail{}, &jailModels.Network{})
			jail, _ := seedJailEpairLifecycle(t, db, 7405, 1)
			network := &jailEpairLifecycleNetworkService{}
			service := &Service{DB: db, NetworkService: network}
			setJailEpairRuntimeState(t, func(*Service, uint) (bool, error) {
				return tc.active, tc.err
			})

			if err := service.cleanupJailEpairsIfStopped(jail); err == nil {
				t.Fatal("expected runtime-state error")
			}
			if len(network.deleteCalls) != 0 {
				t.Fatalf("unsafe state triggered deletes: %v", network.deleteCalls)
			}
		})
	}
}

func TestCleanupJailEpairsIfStoppedDeletesAfterVerifiedStop(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &jailModels.Jail{}, &jailModels.Network{})
	jail, _ := seedJailEpairLifecycle(t, db, 7406, 1)
	network := &jailEpairLifecycleNetworkService{}
	service := &Service{DB: db, NetworkService: network}
	setJailEpairRuntimeState(t, func(*Service, uint) (bool, error) { return false, nil })

	if err := service.cleanupJailEpairsIfStopped(jail); err != nil {
		t.Fatalf("cleanup stopped jail epairs: %v", err)
	}
	if len(network.deleteCalls) != 1 {
		t.Fatalf("delete calls = %v, want one", network.deleteCalls)
	}
}
