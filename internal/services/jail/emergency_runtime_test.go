// SPDX-License-Identifier: BSD-2-Clause

package jail

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/pkg/utils"
)

type fakeEmergencyJailRuntimeOps struct {
	listings   []map[string]int
	listErrors map[int]error
	listCalls  int
	stopErrs   map[string]error
	stopped    []string
}

func (f *fakeEmergencyJailRuntimeOps) List(_ context.Context) (map[string]int, error) {
	call := f.listCalls
	f.listCalls++
	if err := f.listErrors[call]; err != nil {
		return nil, err
	}
	if len(f.listings) == 0 {
		return map[string]int{}, nil
	}
	if call >= len(f.listings) {
		call = len(f.listings) - 1
	}
	result := make(map[string]int, len(f.listings[call]))
	for name, jid := range f.listings[call] {
		result[name] = jid
	}
	return result, nil
}

func (f *fakeEmergencyJailRuntimeOps) Stop(_ context.Context, jailName string) error {
	f.stopped = append(f.stopped, jailName)
	return f.stopErrs[jailName]
}

func managedJailNameMatcher(name string) bool {
	_, ok := managedJailRuntimeCTID(name)
	return ok
}

func TestManagedJailRuntimeCTID(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctid uint
		ok   bool
	}{
		{name: utils.HashIntToNLetters(1, 5), ctid: 1, ok: true},
		{name: utils.HashIntToNLetters(9999, 5), ctid: 9999, ok: true},
		{name: utils.HashIntToNLetters(0, 5)},
		{name: utils.HashIntToNLetters(10000, 5)},
		{name: "sylve"},
		{name: "AAAAA"},
		{name: " aaaab "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctid, ok := managedJailRuntimeCTID(tc.name)
			if ctid != tc.ctid || ok != tc.ok {
				t.Fatalf("managedJailRuntimeCTID(%q) = (%d, %t), want (%d, %t)", tc.name, ctid, ok, tc.ctid, tc.ok)
			}
		})
	}
}

func TestParseJIDsByNameJSON(t *testing.T) {
	output := []byte(`{"jail-information":{"jail":[` +
		`{"jid":1,"name":"one"},` +
		`{"jid":"2","name":"two"},` +
		`{"jid":"bad","name":"bad"},` +
		`{"jid":3,"name":" "}]}}`)
	got, err := parseJIDsByNameJSON(output)
	if err != nil {
		t.Fatalf("parse jls output: %v", err)
	}
	if len(got) != 2 || got["one"] != 1 || got["two"] != 2 {
		t.Fatalf("parsed jails = %#v", got)
	}
	if _, err := parseJIDsByNameJSON([]byte("{")); err == nil {
		t.Fatal("corrupt jls JSON unexpectedly succeeded")
	}
}

func TestEmergencyStopJailRuntimesWithOpsStopsOnlyManagedAndDrainsNewRuntime(t *testing.T) {
	one := utils.HashIntToNLetters(1, 5)
	two := utils.HashIntToNLetters(2, 5)
	last := utils.HashIntToNLetters(9999, 5)
	ops := &fakeEmergencyJailRuntimeOps{
		listings: []map[string]int{
			{one: 1, last: 2, "external": 3},
			{two: 4, "external": 3},
			{"external": 3},
		},
	}

	if err := emergencyStopJailRuntimesWithOps(t.Context(), ops, managedJailNameMatcher); err != nil {
		t.Fatalf("emergency fence failed: %v", err)
	}
	if got, want := strings.Join(ops.stopped, ","), strings.Join([]string{one, last, two}, ","); got != want {
		t.Fatalf("stopped jails = %q, want %q", got, want)
	}
}

func TestEmergencyStopJailRuntimesWithOpsContinuesAndReportsResidual(t *testing.T) {
	one := utils.HashIntToNLetters(1, 5)
	two := utils.HashIntToNLetters(2, 5)
	stopErr := errors.New("jail remove not found")
	ops := &fakeEmergencyJailRuntimeOps{
		listings: []map[string]int{
			{one: 1, two: 2},
			{one: 1},
			{one: 1},
			{one: 1},
		},
		stopErrs: map[string]error{one: stopErr},
	}

	err := emergencyStopJailRuntimesWithOps(t.Context(), ops, managedJailNameMatcher)
	if !errors.Is(err, stopErr) {
		t.Fatalf("stop error not preserved: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "managed_jail_runtimes_still_active: "+one) {
		t.Fatalf("residual jail not reported: %v", err)
	}
	if len(ops.stopped) < 2 || ops.stopped[0] != one || ops.stopped[1] != two {
		t.Fatalf("stop failure suppressed later jail: %v", ops.stopped)
	}
}

func TestEmergencyStopJailRuntimesWithOpsAggregatesVerificationFailure(t *testing.T) {
	one := utils.HashIntToNLetters(1, 5)
	stopErr := errors.New("jail stop failed")
	listErr := errors.New("jls failed")
	ops := &fakeEmergencyJailRuntimeOps{
		listings:   []map[string]int{{one: 1}},
		listErrors: map[int]error{1: listErr},
		stopErrs:   map[string]error{one: stopErr},
	}

	err := emergencyStopJailRuntimesWithOps(t.Context(), ops, managedJailNameMatcher)
	if !errors.Is(err, stopErr) || !errors.Is(err, listErr) {
		t.Fatalf("expected joined stop/list errors, got %v", err)
	}
}
