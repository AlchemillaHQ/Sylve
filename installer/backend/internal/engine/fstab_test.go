// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package engine

import (
	"slices"
	"strings"
	"testing"
)

func TestZFSFstab(t *testing.T) {
	members := []ZFSVdev{
		{Type: "mirror", Disks: []string{"nda0", "nda1"}},
		{Type: "stripe", Disks: []string{"nda2p3", "nda3"}},
	}
	cases := []struct {
		name    string
		storage ZFSStorage
		want    string
	}{
		{
			name:    "plain multiple vdevs skip existing partition members",
			storage: ZFSStorage{Swap: "2g", Vdevs: members},
			want: `/dev/gpt/swap0 none swap sw 0 0
/dev/gpt/swap1 none swap sw 0 0
/dev/gpt/swap2 none swap sw 0 0`,
		},
		{
			name:    "encrypted multiple vdevs",
			storage: ZFSStorage{Encrypt: true, Swap: "2g", Vdevs: members},
			want: `/dev/gpt/swap0.eli none swap sw,ealgo=AES-XTS,keylen=256 0 0
/dev/gpt/swap1.eli none swap sw,ealgo=AES-XTS,keylen=256 0 0
/dev/gpt/swap2.eli none swap sw,ealgo=AES-XTS,keylen=256 0 0`,
		},
		{
			name:    "encrypted swap disabled",
			storage: ZFSStorage{Encrypt: true, Swap: "0", Vdevs: members},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := New(Plan{Storage: &Storage{Mode: "zfs", ZFS: c.storage}})
			assertFstabEntries(t, e.zfsFstab(), c.want)
		})
	}
}

func TestUFSFstab(t *testing.T) {
	for _, c := range []struct {
		name, swap, want string
		encrypted        bool
	}{
		{"plain", "2g", "/dev/gpt/swap0 none swap sw 0 0", false},
		{"encrypted", "2g", "/dev/gpt/swap0.eli none swap sw,ealgo=AES-XTS,keylen=256 0 0", true},
		{"disabled", "0", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := New(Plan{Storage: &Storage{Mode: "ufs", UFS: UFSStorage{
				Disk: "nda0", Swap: c.swap, Encrypt: c.encrypted,
			}}})
			assertFstabEntries(t, e.ufsFstab(), "/dev/ufs/rootfs / ufs rw 1 1\n"+c.want)
		})
	}
}

func assertFstabEntries(t *testing.T, got, want string) {
	t.Helper()
	entries := func(text string) []string {
		var rows []string
		for _, line := range strings.Split(text, "\n") {
			fields := strings.Fields(line)
			if len(fields) > 0 && !strings.HasPrefix(fields[0], "#") {
				rows = append(rows, strings.Join(fields, " "))
			}
		}
		return rows
	}
	if !slices.Equal(entries(got), entries(want)) {
		t.Fatalf("fstab entries:\n%s\nwant:\n%s", got, want)
	}
}
