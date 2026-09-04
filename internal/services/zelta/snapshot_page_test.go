// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"fmt"
	"testing"
	"time"
)

func snapshotPageFixture(count int) []SnapshotInfo {
	items := make([]SnapshotInfo, 0, count)
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < count; i++ {
		items = append(items, SnapshotInfo{
			Name:      fmt.Sprintf("backup/root@bk_j1_c1_%04d", i),
			ShortName: fmt.Sprintf("@bk_j1_c1_%04d", i),
			Creation:  base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
		})
	}
	return items
}

func TestPaginateSnapshotCandidatesWalksNewestToOldest(t *testing.T) {
	candidates := snapshotPageFixture(250)
	request, err := NewSnapshotPageRequest(100, "")
	if err != nil {
		t.Fatal(err)
	}

	first, cursor, hasMore, err := paginateSnapshotCandidates(candidates, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 100 || first[0].Name != candidates[150].Name || first[99].Name != candidates[249].Name {
		t.Fatalf("unexpected first page: len=%d first=%q last=%q", len(first), first[0].Name, first[len(first)-1].Name)
	}
	if !hasMore || cursor == "" {
		t.Fatalf("first page continuation: hasMore=%v cursor=%q", hasMore, cursor)
	}

	request, err = NewSnapshotPageRequest(100, cursor)
	if err != nil {
		t.Fatal(err)
	}
	second, cursor, hasMore, err := paginateSnapshotCandidates(candidates, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 100 || second[0].Name != candidates[50].Name || second[99].Name != candidates[149].Name {
		t.Fatalf("unexpected second page: len=%d first=%q last=%q", len(second), second[0].Name, second[len(second)-1].Name)
	}
	if !hasMore || cursor == "" {
		t.Fatalf("second page continuation: hasMore=%v cursor=%q", hasMore, cursor)
	}

	request, err = NewSnapshotPageRequest(100, cursor)
	if err != nil {
		t.Fatal(err)
	}
	third, cursor, hasMore, err := paginateSnapshotCandidates(candidates, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != 50 || third[0].Name != candidates[0].Name || third[49].Name != candidates[49].Name {
		t.Fatalf("unexpected third page: len=%d first=%q last=%q", len(third), third[0].Name, third[len(third)-1].Name)
	}
	if hasMore || cursor != "" {
		t.Fatalf("third page continuation: hasMore=%v cursor=%q", hasMore, cursor)
	}
}

func TestPaginateSnapshotCandidatesCursorSurvivesConcurrentChanges(t *testing.T) {
	candidates := snapshotPageFixture(3)
	first, cursor, hasMore, err := paginateSnapshotCandidates(candidates, SnapshotPageRequest{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || !hasMore {
		t.Fatalf("unexpected first page: len=%d hasMore=%v", len(first), hasMore)
	}

	changed := append(snapshotPageFixture(1), candidates[1:]...)
	changed[0].Name = "backup/root@bk_j1_c1_older"
	changed[0].Creation = "2025-12-31T23:59:00Z"
	changed = append(changed, SnapshotInfo{
		Name: "backup/root@bk_j1_c1_new", Creation: "2026-01-02T00:00:00Z",
	})
	second, _, _, err := paginateSnapshotCandidates(changed, SnapshotPageRequest{Limit: 2, Cursor: cursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].Name != "backup/root@bk_j1_c1_older" {
		t.Fatalf("cursor did not remain an exclusive chronological boundary: %+v", second)
	}
}

func TestNewSnapshotPageRequestRejectsInvalidInput(t *testing.T) {
	for _, request := range []SnapshotPageRequest{
		{Limit: -1},
		{Limit: MaxSnapshotPageLimit + 1},
		{Limit: 10, Cursor: "not-a-cursor"},
	} {
		if _, err := NewSnapshotPageRequest(request.Limit, request.Cursor); err == nil {
			t.Fatalf("expected request rejection: %+v", request)
		}
	}

	request, err := NewSnapshotPageRequest(0, "")
	if err != nil || request.Limit != DefaultSnapshotPageLimit {
		t.Fatalf("default request = %+v, %v", request, err)
	}
}
