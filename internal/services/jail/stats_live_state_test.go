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
	previousRuntimes := jailStatsReadRuntimes
	previousUsage := jailStatsReadPSUsageByJID
	t.Cleanup(func() {
		jailStatsReadRuntimes = previousRuntimes
		jailStatsReadPSUsageByJID = previousUsage
	})
}

func TestGetStatesCachesEmptyResultWithoutReadingHost(t *testing.T) {
	saveJailStatsReaders(t)
	jailStatsReadRuntimes = func(*Service) (map[string]jailRuntime, error) {
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
	jailStatsReadRuntimes = func(*Service) (map[string]jailRuntime, error) {
		return map[string]jailRuntime{"managed": {JID: 7}}, nil
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

func TestRefreshLiveStatesIncludesDescendantUsage(t *testing.T) {
	const runtimeJSON = `{"jail-information":{"jail":[
		{"jid":"59","parent":"0","name":"aaaex"},
		{"jid":"111","parent":"0","name":"aaafh"},
		{"jid":"174","parent":"59","name":"aaaex.151amd64-fastp"},
		{"jid":"175","parent":"59","name":"aaaex.151amd64-fastp-n"},
		{"jid":"176","parent":"174","name":"aaaex.151amd64-fastp.worker"},
		{"jid":"177","parent":"111","name":"aaafh.worker"}
	]}}`

	for _, tc := range []struct {
		name            string
		usage           map[int]psUsage
		wantCPU         float64
		wantRSS         int64
		wantMemoryUsage float64
	}{
		{
			name: "parent processes only",
			usage: map[int]psUsage{
				59: {TotalCPU: 40, TotalRSS: 512},
			},
			wantCPU: 10, wantRSS: 512, wantMemoryUsage: 13,
		},
		{
			name: "parent children and grandchild",
			usage: map[int]psUsage{
				59:  {TotalCPU: 20, TotalRSS: 128},
				174: {TotalCPU: 96.4, TotalRSS: 1024},
				175: {TotalCPU: 40, TotalRSS: 512},
				176: {TotalCPU: 43.6, TotalRSS: 256},
			},
			wantCPU: 50, wantRSS: 1920, wantMemoryUsage: 47,
		},
		{
			name: "no parent processes",
			usage: map[int]psUsage{
				174: {TotalCPU: 100, TotalRSS: 1024},
				176: {TotalCPU: 100, TotalRSS: 512},
			},
			wantCPU: 50, wantRSS: 1536, wantMemoryUsage: 38,
		},
		{
			name: "grandchild processes only",
			usage: map[int]psUsage{
				176: {TotalCPU: 200, TotalRSS: 1536},
			},
			wantCPU: 50, wantRSS: 1536, wantMemoryUsage: 38,
		},
		{
			name: "CPU capped after aggregation",
			usage: map[int]psUsage{
				174: {TotalCPU: 1000, TotalRSS: 1024},
			},
			wantCPU: 100, wantRSS: 1024, wantMemoryUsage: 25,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saveJailStatsReaders(t)
			database := testutil.NewSQLiteTestDB(t, &jailModels.Jail{}, &jailModels.JailStats{})
			limited := true
			names := map[uint]string{104: "aaaex", 105: "aaafh"}
			var parentID uint
			for ctid, name := range names {
				jail := jailModels.Jail{
					CTID: ctid, Name: name, ResourceLimits: &limited, Cores: 2,
					CPUSet: []int{0, 2, 4, 6}, Memory: 4 * 1024 * 1024,
				}
				if err := database.Create(&jail).Error; err != nil {
					t.Fatal(err)
				}
				if ctid == 104 {
					parentID = jail.ID
				}
			}
			service := &Service{DB: database, ctidHashByCTID: names}
			jailStatsReadRuntimes = func(*Service) (map[string]jailRuntime, error) {
				return parseJailRuntimesJSON([]byte(runtimeJSON))
			}
			tc.usage[0] = psUsage{TotalCPU: 10000, TotalRSS: 10000}
			tc.usage[111] = psUsage{TotalCPU: 60, TotalRSS: 256}
			tc.usage[177] = psUsage{TotalCPU: 20, TotalRSS: 256}
			tc.usage[999] = psUsage{TotalCPU: 10000, TotalRSS: 10000}
			jailStatsReadPSUsageByJID = func(*Service) (map[int]psUsage, error) {
				return tc.usage, nil
			}

			for range 2 {
				states, err := service.refreshLiveStates()
				if err != nil || len(states) != 2 {
					t.Fatalf("refresh states = %v, err=%v", states, err)
				}
				if got := states[0]; got.CTID != 104 || got.State != "ACTIVE" || got.PCPU != tc.wantCPU || got.Memory != tc.wantRSS*1024 {
					t.Fatalf("parent state = %+v, want CPU=%v memory=%d", got, tc.wantCPU, tc.wantRSS*1024)
				}
				if got := states[1]; got.CTID != 105 || got.State != "ACTIVE" || got.PCPU != 20 || got.Memory != 512*1024 {
					t.Fatalf("unrelated jail state = %+v, want CPU=20 memory=%d", got, 512*1024)
				}
			}

			if err := service.StoreJailUsage(); err != nil {
				t.Fatal(err)
			}
			var stat jailModels.JailStats
			if err := database.First(&stat, "jid = ?", parentID).Error; err != nil {
				t.Fatal(err)
			}
			if stat.CPUUsage != tc.wantCPU || stat.MemoryUsage != tc.wantMemoryUsage {
				t.Fatalf("persisted usage = %+v, want CPU=%v memory=%v", stat, tc.wantCPU, tc.wantMemoryUsage)
			}
		})
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
	jailStatsReadRuntimes = func(*Service) (map[string]jailRuntime, error) {
		jidReads.Add(1)
		return map[string]jailRuntime{"managed": {JID: 7}}, nil
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
	jailStatsReadRuntimes = func(*Service) (map[string]jailRuntime, error) { return nil, nil }
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
	jailStatsReadRuntimes = func(*Service) (map[string]jailRuntime, error) { return nil, readErr }
	if _, err := service.GetStates(); !errors.Is(err, readErr) {
		t.Fatalf("initial read error = %v, want %v", err, readErr)
	}
	if service.getCachedStates() != nil || !service.liveStateUpdatedAt.IsZero() {
		t.Fatal("failed refresh initialized the cache")
	}

	jailStatsReadRuntimes = func(*Service) (map[string]jailRuntime, error) {
		return map[string]jailRuntime{"managed": {JID: 7}}, nil
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
