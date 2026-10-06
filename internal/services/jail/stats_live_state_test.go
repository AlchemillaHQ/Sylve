// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func saveJailStatsReaders(t *testing.T) {
	t.Helper()
	previousJIDs := jailStatsReadJIDsByName
	previousUsage := jailStatsReadPSUsageByJID
	t.Cleanup(func() {
		jailStatsReadJIDsByName = previousJIDs
		jailStatsReadPSUsageByJID = previousUsage
	})
}

func TestGetStatesCachesEmptyResultWithoutReadingHost(t *testing.T) {
	saveJailStatsReaders(t)
	jailStatsReadJIDsByName = func(*Service) (map[string]int, error) {
		t.Fatal("empty managed jail list must not run jls")
		return nil, nil
	}
	jailStatsReadPSUsageByJID = func(*Service) (map[int]psUsage, error) {
		t.Fatal("empty managed jail list must not run ps")
		return nil, nil
	}

	database := testutil.NewSQLiteTestDB(t, &jailModels.Jail{})
	service := &Service{DB: database, liveStateByCTID: make(map[uint]jailServiceInterfaces.State)}
	if states := service.getCachedStates(); states != nil {
		t.Fatalf("uninitialized cache = %v, want nil", states)
	}
	queries := 0
	if err := database.Callback().Query().Before("gorm:query").Register("test:count_state_queries", func(*gorm.DB) {
		queries++
	}); err != nil {
		t.Fatal(err)
	}

	for range 3 {
		states, err := service.GetStates()
		if err != nil || states == nil || len(states) != 0 {
			t.Fatalf("empty states = %v, err=%v", states, err)
		}
	}
	if queries != 1 || service.liveStateUpdatedAt.IsZero() {
		t.Fatalf("empty cache was not initialized: queries=%d updatedAt=%v", queries, service.liveStateUpdatedAt)
	}
}

func TestRefreshLiveStatesUpdatesPreviouslyEmptyCache(t *testing.T) {
	saveJailStatsReaders(t)
	database := testutil.NewSQLiteTestDB(t, &jailModels.Jail{})
	service := &Service{DB: database, ctidHashByCTID: map[uint]string{104: "managed"}}
	if _, err := service.GetStates(); err != nil {
		t.Fatal(err)
	}
	limited := true
	if err := database.Create(&jailModels.Jail{CTID: 104, ResourceLimits: &limited, Cores: 2}).Error; err != nil {
		t.Fatal(err)
	}
	jailStatsReadJIDsByName = func(*Service) (map[string]int, error) {
		return map[string]int{"managed": 7}, nil
	}
	jailStatsReadPSUsageByJID = func(*Service) (map[int]psUsage, error) {
		return map[int]psUsage{7: {TotalCPU: 40, TotalRSS: 512}}, nil
	}

	states, err := service.refreshLiveStates()
	if err != nil || len(states) != 1 {
		t.Fatalf("refresh states = %v, err=%v", states, err)
	}
	if got := states[0]; got.CTID != 104 || got.State != "ACTIVE" || got.PCPU != 20 || got.Memory != 512*1024 {
		t.Fatalf("refreshed state = %+v", got)
	}
	states[0].State = "INACTIVE"
	cached, err := service.GetStates()
	if err != nil || len(cached) != 1 || cached[0].State != "ACTIVE" {
		t.Fatalf("cached state was not preserved: %v, err=%v", cached, err)
	}
}

func TestGetStatesSharesInitialRefresh(t *testing.T) {
	saveJailStatsReaders(t)
	database := testutil.NewSQLiteTestDB(t, &jailModels.Jail{})
	if err := database.Create(&jailModels.Jail{CTID: 104}).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: database, ctidHashByCTID: map[uint]string{104: "managed"}}
	var jidReads, usageReads atomic.Int32
	jailStatsReadJIDsByName = func(*Service) (map[string]int, error) {
		jidReads.Add(1)
		return map[string]int{"managed": 7}, nil
	}
	jailStatsReadPSUsageByJID = func(*Service) (map[int]psUsage, error) {
		usageReads.Add(1)
		return map[int]psUsage{7: {TotalRSS: 512}}, nil
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			states, err := service.GetStates()
			if err != nil || len(states) != 1 || states[0].State != "ACTIVE" {
				t.Errorf("concurrent states = %v, err=%v", states, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if jidReads.Load() != 1 || usageReads.Load() != 1 {
		t.Fatalf("initial refresh was duplicated: jls=%d ps=%d", jidReads.Load(), usageReads.Load())
	}
}

func TestRefreshLiveStatesSkipsUsageWhenNoJailsAreRunning(t *testing.T) {
	saveJailStatsReaders(t)
	database := testutil.NewSQLiteTestDB(t, &jailModels.Jail{})
	if err := database.Create(&jailModels.Jail{CTID: 104}).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: database, ctidHashByCTID: map[uint]string{104: "managed"}}
	jailStatsReadJIDsByName = func(*Service) (map[string]int, error) { return nil, nil }
	jailStatsReadPSUsageByJID = func(*Service) (map[int]psUsage, error) {
		t.Fatal("no running jails must not run ps")
		return nil, nil
	}
	states, err := service.GetStates()
	if err != nil || len(states) != 1 || states[0].State != "INACTIVE" || states[0].PCPU != 0 || states[0].Memory != 0 {
		t.Fatalf("inactive states = %v, err=%v", states, err)
	}
}

func TestRefreshLiveStatesFailureDoesNotInitializeOrEraseCache(t *testing.T) {
	saveJailStatsReaders(t)
	database := testutil.NewSQLiteTestDB(t, &jailModels.Jail{})
	if err := database.Create(&jailModels.Jail{CTID: 104}).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: database, ctidHashByCTID: map[uint]string{104: "managed"}}
	readErr := errors.New("jls failed")
	jailStatsReadJIDsByName = func(*Service) (map[string]int, error) { return nil, readErr }
	if _, err := service.GetStates(); !errors.Is(err, readErr) {
		t.Fatalf("initial read error = %v, want %v", err, readErr)
	}
	if service.getCachedStates() != nil || !service.liveStateUpdatedAt.IsZero() {
		t.Fatal("failed refresh initialized the cache")
	}

	jailStatsReadJIDsByName = func(*Service) (map[string]int, error) {
		return map[string]int{"managed": 7}, nil
	}
	jailStatsReadPSUsageByJID = func(*Service) (map[int]psUsage, error) {
		return map[int]psUsage{7: {TotalRSS: 512}}, nil
	}
	if _, err := service.GetStates(); err != nil {
		t.Fatal(err)
	}
	jailStatsReadPSUsageByJID = func(*Service) (map[int]psUsage, error) { return nil, readErr }
	if _, err := service.refreshLiveStates(); !errors.Is(err, readErr) {
		t.Fatalf("refresh error = %v, want %v", err, readErr)
	}
	states, err := service.GetStates()
	if err != nil || len(states) != 1 || states[0].State != "ACTIVE" {
		t.Fatalf("failed refresh erased the cached state: %v, err=%v", states, err)
	}
}
