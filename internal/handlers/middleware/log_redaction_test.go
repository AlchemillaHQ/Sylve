// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package middleware

import "testing"

func TestShouldRedactAuditPayload(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{path: "/api/auth/login", want: true},
		{path: "/api/cluster/join", want: true},
		{path: "/api/utilities/downloads/signed-url", want: true},
		{path: "/api/zfs/pools", want: false},
	}

	for _, tc := range cases {
		if got := shouldRedactAuditPayload(tc.path); got != tc.want {
			t.Fatalf("path=%s expected=%v got=%v", tc.path, tc.want, got)
		}
	}
}

func TestSanitizeAuditPayloadNested(t *testing.T) {
	input := map[string]interface{}{
		"username": "admin",
		"password": "super-secret",
		"nested": map[string]interface{}{
			"token": "abc",
			"safe":  "ok",
		},
		"array": []interface{}{
			map[string]interface{}{"clusterKey": "k1"},
			map[string]interface{}{"value": "keep"},
		},
	}

	outAny := sanitizeAuditPayload(input)
	out, ok := outAny.(map[string]interface{})
	if !ok {
		t.Fatal("expected_map_output")
	}

	if out["password"] != "[REDACTED]" {
		t.Fatal("expected_password_to_be_redacted")
	}

	nested, ok := out["nested"].(map[string]interface{})
	if !ok {
		t.Fatal("expected_nested_map")
	}
	if nested["token"] != "[REDACTED]" {
		t.Fatal("expected_nested_token_to_be_redacted")
	}
	if nested["safe"] != "ok" {
		t.Fatal("expected_safe_nested_field_to_be_preserved")
	}

	arr, ok := out["array"].([]interface{})
	if !ok || len(arr) != 2 {
		t.Fatal("expected_two_array_entries")
	}
	firstMap, ok := arr[0].(map[string]interface{})
	if !ok {
		t.Fatal("expected_first_array_entry_map")
	}
	if firstMap["clusterKey"] != "[REDACTED]" {
		t.Fatal("expected_cluster_key_to_be_redacted")
	}
	secondMap, ok := arr[1].(map[string]interface{})
	if !ok || secondMap["value"] != "keep" {
		t.Fatal("expected_safe_array_value_to_be_preserved")
	}
}
