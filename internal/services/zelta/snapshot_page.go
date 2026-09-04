// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	DefaultSnapshotPageLimit  = 100
	MaxSnapshotPageLimit      = 500
	snapshotPageCursorVersion = 1
)

type SnapshotPageRequest struct {
	Limit  int
	Cursor string
}

type SnapshotPage struct {
	Items      []SnapshotInfo `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
	HasMore    bool           `json:"hasMore"`
}

type snapshotPageCursor struct {
	Version  int    `json:"v"`
	Creation string `json:"c"`
	Name     string `json:"n"`
}

func NewSnapshotPageRequest(limit int, cursor string) (SnapshotPageRequest, error) {
	if limit == 0 {
		limit = DefaultSnapshotPageLimit
	}
	if limit < 1 || limit > MaxSnapshotPageLimit {
		return SnapshotPageRequest{}, fmt.Errorf("invalid_snapshot_page_limit")
	}
	cursor = strings.TrimSpace(cursor)
	if cursor != "" {
		if _, err := decodeSnapshotPageCursor(cursor); err != nil {
			return SnapshotPageRequest{}, err
		}
	}
	return SnapshotPageRequest{Limit: limit, Cursor: cursor}, nil
}

func ParseSnapshotPageLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultSnapshotPageLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid_snapshot_page_limit")
	}
	if limit < 1 || limit > MaxSnapshotPageLimit {
		return 0, fmt.Errorf("invalid_snapshot_page_limit")
	}
	return limit, nil
}

func encodeSnapshotPageCursor(snapshot SnapshotInfo) (string, error) {
	payload, err := json.Marshal(snapshotPageCursor{
		Version:  snapshotPageCursorVersion,
		Creation: strings.TrimSpace(snapshot.Creation),
		Name:     strings.TrimSpace(snapshot.Name),
	})
	if err != nil {
		return "", fmt.Errorf("encode_snapshot_page_cursor_failed: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeSnapshotPageCursor(encoded string) (snapshotPageCursor, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" || len(encoded) > 4096 {
		return snapshotPageCursor{}, fmt.Errorf("invalid_snapshot_page_cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return snapshotPageCursor{}, fmt.Errorf("invalid_snapshot_page_cursor")
	}
	var cursor snapshotPageCursor
	if err := json.Unmarshal(payload, &cursor); err != nil ||
		cursor.Version != snapshotPageCursorVersion ||
		strings.TrimSpace(cursor.Name) == "" {
		return snapshotPageCursor{}, fmt.Errorf("invalid_snapshot_page_cursor")
	}
	cursor.Creation = strings.TrimSpace(cursor.Creation)
	cursor.Name = strings.TrimSpace(cursor.Name)
	return cursor, nil
}

func snapshotChronologicalLess(left, right SnapshotInfo) bool {
	leftTime, leftOK := parseSnapshotCreationTime(left.Creation)
	rightTime, rightOK := parseSnapshotCreationTime(right.Creation)
	if leftOK && rightOK && !leftTime.Equal(rightTime) {
		return leftTime.Before(rightTime)
	}
	if leftOK != rightOK {
		return leftOK
	}
	return strings.TrimSpace(left.Name) < strings.TrimSpace(right.Name)
}

func paginateSnapshotCandidates(
	candidates []SnapshotInfo,
	request SnapshotPageRequest,
) ([]SnapshotInfo, string, bool, error) {
	request, err := NewSnapshotPageRequest(request.Limit, request.Cursor)
	if err != nil {
		return nil, "", false, err
	}

	ordered := append([]SnapshotInfo(nil), candidates...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return snapshotChronologicalLess(ordered[i], ordered[j])
	})

	if request.Cursor != "" {
		cursor, err := decodeSnapshotPageCursor(request.Cursor)
		if err != nil {
			return nil, "", false, err
		}
		boundary := SnapshotInfo{Name: cursor.Name, Creation: cursor.Creation}
		older := ordered[:0]
		for _, candidate := range ordered {
			if snapshotChronologicalLess(candidate, boundary) {
				older = append(older, candidate)
			}
		}
		ordered = older
	}

	start := 0
	if len(ordered) > request.Limit {
		start = len(ordered) - request.Limit
	}
	page := append([]SnapshotInfo(nil), ordered[start:]...)
	hasMore := start > 0
	nextCursor := ""
	if hasMore && len(page) > 0 {
		nextCursor, err = encodeSnapshotPageCursor(page[0])
		if err != nil {
			return nil, "", false, err
		}
	}
	return page, nextCursor, hasMore, nil
}
