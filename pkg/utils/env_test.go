// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"reflect"
	"testing"
)

func TestFilterEnvRemovesMatchingNamesIncludingEmptyValues(t *testing.T) {
	env := []string{
		"PATH=/sbin:/bin",
		"INSTALL_AS_USER=yes",
		"INSTALL_AS_USER=",
		"ASSUME_ALWAYS_YES=yes",
		"ASSUME_ALWAYS_YES=",
		"HOME=/root",
		"NOT_INSTALL_AS_USER=1",
	}

	got := FilterEnv(env, "INSTALL_AS_USER", "ASSUME_ALWAYS_YES")
	want := []string{"PATH=/sbin:/bin", "HOME=/root", "NOT_INSTALL_AS_USER=1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FilterEnv = %#v, want %#v", got, want)
	}
}

func TestFilterEnvDoesNotAliasInput(t *testing.T) {
	env := []string{"PATH=/sbin", "INSTALL_AS_USER=yes"}
	got := FilterEnv(env, "INSTALL_AS_USER")
	if len(got) != 1 || got[0] != "PATH=/sbin" {
		t.Fatalf("FilterEnv = %#v", got)
	}
	got[0] = "PATH=changed"
	if env[0] != "PATH=/sbin" {
		t.Fatal("FilterEnv aliased its input")
	}
}

func TestFilterEnvEmptyInput(t *testing.T) {
	if got := FilterEnv(nil, "INSTALL_AS_USER"); got != nil {
		t.Fatalf("FilterEnv(nil) = %#v, want nil", got)
	}
}
