// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseSizeString(t *testing.T) {
	cases := []struct {
		in      string
		want    uint64
		wantErr bool
	}{
		{"", 0, false},
		{"0", 0, false},
		{"512m", 512 << 20, false},
		{"2G", 2 << 30, false},
		{"1t", 1 << 40, false},
		{"512 MiB", 512 << 20, false},
		{"1.5g", 1610612736, false},
		{"2 kib", 2048, false},
		{"nonsense", 0, true},
		{"2x", 0, true},
		{"9223372036854775808B", 0, true},
		{"18446744073709551616B", 0, true},
		{"9007199254740992TiB", 0, true},
	}
	for _, c := range cases {
		got, err := parseSizeString(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseSizeString(%q): expected error, got %d", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSizeString(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseSizeString(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestZFSPartitionIndices(t *testing.T) {
	for _, c := range []struct {
		name               string
		encrypted          bool
		swap               string
		wantSwap, wantData int
	}{
		{"plain", false, "2g", 3, 4},
		{"plain without swap", false, "0", 0, 3},
		{"encrypted", true, "2g", 4, 5},
		{"encrypted without swap", true, "0", 0, 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := New(Plan{Storage: &Storage{Mode: "zfs", ZFS: ZFSStorage{Encrypt: c.encrypted, Swap: c.swap}}})
			swap, data := e.zfsPartitionIndices()
			if swap != c.wantSwap || data != c.wantData {
				t.Fatalf("partition indices = (%d,%d), want (%d,%d)", swap, data, c.wantSwap, c.wantData)
			}
		})
	}
}

func TestZFSLoaderConf(t *testing.T) {
	for _, tc := range []struct {
		name, pool string
		encrypted  bool
	}{
		{"plain default pool", "", false},
		{"encrypted default pool", "", true},
		{"plain custom pool", "system", false},
		{"encrypted custom pool", "system", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New(Plan{Storage: &Storage{Mode: "zfs", ZFS: ZFSStorage{
				Name: tc.pool, Encrypt: tc.encrypted,
			}}})
			conf := e.zfsLoaderConf()
			for _, line := range []string{
				"zfs_load=\"YES\"\n",
				"autoboot_delay=\"3\"\n",
				"vfs.root.mountfrom=\"zfs:" + e.poolName() + "/ROOT/default\"\n",
				"kern.geom.label.disk_ident.enable=\"0\"\n",
				"kern.geom.label.gptid.enable=\"0\"\n",
			} {
				if strings.Count(conf, line) != 1 {
					t.Fatalf("expected exactly one %q in loader configuration:\n%s", line, conf)
				}
			}
			for _, line := range []string{
				"geom_eli_load=\"YES\"\n",
				"aesni_load=\"YES\"\n",
				"geom_eli_passphrase_prompt=\"YES\"\n",
			} {
				if strings.Contains(conf, line) != tc.encrypted {
					t.Fatalf("incorrect GELI setting %q in loader configuration:\n%s", line, conf)
				}
			}
			for _, liveOnly := range []string{"boot_mute=", "splash=", "init_rc="} {
				if strings.Contains(conf, liveOnly) {
					t.Fatalf("live-only setting %q leaked into installed loader configuration", liveOnly)
				}
			}
		})
	}
}

func TestInvalidStoragePlans(t *testing.T) {
	validationDisks(t,
		Disk{Name: "ada0", SizeBytes: 32 << 30},
		Disk{Name: "da0", SizeBytes: 32 << 30, Boot: true},
	)
	for _, c := range []struct {
		name    string
		storage Storage
		want    string
	}{
		{"unsupported mode", Storage{Mode: "manual"}, "unsupported storage mode"},
		{"missing UFS disk", Storage{Mode: "ufs"}, "choose a disk"},
		{"missing ZFS disks", Storage{Mode: "zfs"}, "choose at least one disk"},
		{"installer media", Storage{Mode: "ufs", UFS: UFSStorage{Disk: "da0"}}, "contains the running installer"},
		{"installer media partition", Storage{Mode: "zfs", ZFS: ZFSStorage{Vdevs: []ZFSVdev{{Type: "stripe", Disks: []string{"ada0", "da0p3"}}}}}, "running installer"},
	} {
		t.Run(c.name, func(t *testing.T) {
			plan := storagePlan(c.storage)
			plan.DryRun = true
			e := New(plan)
			if err := e.Start(); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Start() = %v, want %q", err, c.want)
			}
			if e.Snapshot().Running {
				t.Fatal("rejected plan started the installer")
			}
		})
	}
}

func TestAdminPasswordMinimum(t *testing.T) {
	validationDisks(t, Disk{Name: "ada0", SizeBytes: 32 << 30, SectorSize: 512})
	for _, tc := range []struct {
		name, password, want string
	}{
		{"empty", "", "Sylve admin password is required"},
		{"seven characters", "1234567", "at least 8 characters"},
		{"eight characters", "12345678", ""},
		{"longer password", "123456789", ""},
		{"seven multibyte characters", "ééééééé", "at least 8 characters"},
		{"eight multibyte characters", "éééééééé", ""},
		{"four emoji", "🔒🔒🔒🔒", "at least 8 characters"},
		{"seven letters and an emoji", "abcdefg🔒", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := storagePlan(Storage{Mode: "ufs", UFS: UFSStorage{Disk: "ada0"}})
			plan.AdminPassword = tc.password
			e := New(plan)
			e.mu.Lock()
			err := e.validateLocked()
			e.mu.Unlock()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("valid password rejected: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validation = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestZFSVdevArgs(t *testing.T) {
	devs := []string{"/dev/ada0p4", "/dev/ada1p4", "/dev/ada2p4", "/dev/ada3p4", "/dev/ada4p4"}
	cases := []struct {
		vdev  string
		n     int
		want  []string
		isErr bool
	}{
		{"stripe", 1, []string{devs[0]}, false},
		{"mirror", 2, []string{"mirror", devs[0], devs[1]}, false},
		{"mirror", 1, nil, true},
		{"raidz1", 3, []string{"raidz", devs[0], devs[1], devs[2]}, false},
		{"raidz1", 2, nil, true},
		{"raidz2", 4, []string{"raidz2", devs[0], devs[1], devs[2], devs[3]}, false},
		{"raidz3", 5, []string{"raidz3", devs[0], devs[1], devs[2], devs[3], devs[4]}, false},
		{"raidz3", 3, nil, true},
		{"raid10", 4, []string{"mirror", devs[0], devs[1], "mirror", devs[2], devs[3]}, false},
		{"raid10", 5, nil, true},
		{"draid1", 5, nil, true},
	}
	for _, c := range cases {
		got, err := zfsVdevArgs(c.vdev, devs[:c.n])
		if c.isErr {
			if err == nil {
				t.Errorf("%q/%d: expected an error, got %v", c.vdev, c.n, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q/%d: %v", c.vdev, c.n, err)
			continue
		}
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("%q/%d = %v, want %v", c.vdev, c.n, got, c.want)
		}
	}
}

func TestSylveBootstrapSwitch(t *testing.T) {
	plan := func(uplink string) Plan {
		return Plan{
			Hostname:      "sylve",
			RootPassword:  "x",
			AdminPassword: "testpass",
			Network:       Network{Mode: "dhcp", Uplink: uplink},
			Storage:       &Storage{Mode: "zfs", ZFS: ZFSStorage{Vdevs: []ZFSVdev{{Type: "stripe", Disks: []string{"ada0"}}}}},
		}
	}

	e := New(plan("vtnet0"))
	doc := e.sylveBootstrapDoc()
	if len(doc.Switches) != 1 || doc.Switches[0].Standard == nil {
		t.Fatalf("expected one standard switch, got %+v", doc.Switches)
	}
	sw := doc.Switches[0].Standard
	if len(sw.Ports) != 1 || sw.Ports[0] != "vtnet0" {
		t.Fatalf("ports = %v, want [vtnet0]", sw.Ports)
	}
	if sw.BridgeMAC["mode"] != "port" || sw.BridgeMAC["port"] != "vtnet0" {
		t.Fatalf("bridgeMac = %v, want a port source on vtnet0", sw.BridgeMAC)
	}

	e2 := New(plan(""))
	doc2 := e2.sylveBootstrapDoc()
	if len(doc2.Switches) != 1 || doc2.Switches[0].Standard == nil {
		t.Fatalf("expected one standard switch, got %+v", doc2.Switches)
	}
	sw2 := doc2.Switches[0].Standard
	if len(sw2.Ports) != 0 {
		t.Fatalf("ports = %v, want none", sw2.Ports)
	}
	if sw2.BridgeMAC != nil {
		t.Fatalf("bridgeMac = %v, want omitted", sw2.BridgeMAC)
	}
	blob, err := json.Marshal(sw2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blob), `"ports":[]`) {
		t.Fatalf("ports must serialize as [], got %s", blob)
	}
}

func TestSylveBootstrapIPv6Modes(t *testing.T) {
	for _, tc := range []struct {
		name, mode             string
		slaac, disabled, route bool
	}{
		{"omitted API mode stays off", "", false, true, false},
		{"explicit off", "off", false, true, false},
		{"SLAAC", "slaac", true, false, true},
		{"static", "static", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New(Plan{Network: Network{
				Mode6: tc.mode, Address6: "2001:db8::2/64", Gateway6: "2001:db8::1",
			}})
			sw := e.sylveBootstrapDoc().Switches[0].Standard
			if sw.SLAAC != tc.slaac || sw.DisableIPv6 != tc.disabled || sw.DefaultRoute6 != tc.route {
				t.Fatalf("incorrect IPv6 bootstrap settings: %+v", sw)
			}
			if tc.mode == "static" {
				if sw.Network6Manual != "2001:db8::2/64" || sw.Gateway6Manual != "2001:db8::1" {
					t.Fatal("static IPv6 settings were not preserved")
				}
			} else if sw.Network6Manual != "" || sw.Gateway6Manual != "" {
				t.Fatal("manual IPv6 settings leaked into another mode")
			}
		})
	}
}

func TestZFSUsableBytes(t *testing.T) {
	const GiB = int64(1) << 30
	sizes := map[string]int64{
		"ada0":   8 * GiB,
		"ada1":   8 * GiB,
		"ada2":   2 * GiB,
		"ada3":   2 * GiB,
		"ada4":   2 * GiB,
		"ada5p2": 4 * GiB,
	}
	lookup := func(member string) int64 { return sizes[member] }

	cases := []struct {
		name  string
		vdevs []ZFSVdev
		want  int64
	}{
		{"stripe of disk and partition", []ZFSVdev{{Type: "stripe", Disks: []string{"ada0", "ada5p2"}}}, 12 * GiB},
		{"mirror uses the smallest member", []ZFSVdev{{Type: "mirror", Disks: []string{"ada0", "ada2"}}}, 2 * GiB},
		{"raidz1 loses one member", []ZFSVdev{{Type: "raidz1", Disks: []string{"ada2", "ada3", "ada4"}}}, 4 * GiB},
		{"raid10 uses adjacent pairs", []ZFSVdev{{Type: "raid10", Disks: []string{"ada0", "ada1", "ada2", "ada3"}}}, 10 * GiB},
		{"raid10 does not reorder unequal members", []ZFSVdev{{Type: "raid10", Disks: []string{"ada0", "ada2", "ada1", "ada3"}}}, 4 * GiB},
		{"raidz1 uses smallest member size", []ZFSVdev{{Type: "raidz1", Disks: []string{"ada0", "ada1", "ada2"}}}, 4 * GiB},
		{"raidz2 uses smallest member size", []ZFSVdev{{Type: "raidz2", Disks: []string{"ada0", "ada1", "ada2", "ada3"}}}, 4 * GiB},
		{"raidz3 uses smallest member size", []ZFSVdev{{Type: "raidz3", Disks: []string{"ada0", "ada1", "ada2", "ada3", "ada4"}}}, 4 * GiB},
		{"two vdevs add up", []ZFSVdev{
			{Type: "mirror", Disks: []string{"ada0", "ada1"}},
			{Type: "stripe", Disks: []string{"ada5p2"}},
		}, 12 * GiB},
		{"raidz1 with too few members is zero", []ZFSVdev{{Type: "raidz1", Disks: []string{"ada2", "ada3"}}}, 0},
		{"unknown members fail closed", []ZFSVdev{{Type: "stripe", Disks: []string{"missing", "ada0"}}}, 0},
	}
	for _, c := range cases {
		if got := zfsUsableBytes(c.vdevs, lookup); got != c.want {
			t.Errorf("%s = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestZFSVdevArgsFor(t *testing.T) {
	z := ZFSStorage{Vdevs: []ZFSVdev{
		{Type: "mirror", Disks: []string{"ada0", "ada1"}},
		{Type: "raidz1", Disks: []string{"ada2", "ada3", "ada4"}},
		{Type: "stripe", Disks: []string{"ada5", "ada6p2"}},
	}}
	got, err := zfsVdevArgsFor(z, 3)
	if err != nil {
		t.Fatalf("zfsVdevArgsFor: %v", err)
	}
	want := "mirror /dev/ada0p3 /dev/ada1p3 raidz /dev/ada2p3 /dev/ada3p3 /dev/ada4p3 /dev/ada5p3 /dev/ada6p2"
	if strings.Join(got, " ") != want {
		t.Fatalf("args = %q, want %q", strings.Join(got, " "), want)
	}

	z.Encrypt = true
	got, err = zfsVdevArgsFor(z, 4)
	if err != nil {
		t.Fatalf("zfsVdevArgsFor (encrypted): %v", err)
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "/dev/ada0p4.eli") {
		t.Fatalf("encrypted args missing .eli provider: %v", got)
	}
	if !strings.Contains(joined, "/dev/ada6p2") || strings.Contains(joined, "ada6p2.eli") {
		t.Fatalf("partition member should stay unencrypted: %v", got)
	}
}

func TestValidateVdevs(t *testing.T) {
	ok := []ZFSVdev{
		{Type: "mirror", Disks: []string{"ada0", "ada1"}},
		{Type: "stripe", Disks: []string{"ada2"}},
	}
	if err := validateVdevs(ok); err != nil {
		t.Fatalf("valid vdevs rejected: %v", err)
	}
	cases := [][]ZFSVdev{
		{{Type: "stripe", Disks: nil}},
		{{Type: "mirror", Disks: []string{"ada0"}}},
		{{Type: "stripe", Disks: []string{"ada0"}}, {Type: "stripe", Disks: []string{"ada0"}}},
		{{Type: "log", Disks: []string{"ada0"}}},
	}
	for _, c := range cases {
		if err := validateVdevs(c); err == nil {
			t.Errorf("validateVdevs(%+v): expected an error", c)
		}
	}
}

func TestZFSOptionsValidation(t *testing.T) {
	cases := []struct {
		opt     ZFSStorage
		wantErr bool
	}{
		{ZFSStorage{Compression: "zstd-3"}, false},
		{ZFSStorage{Compression: "gzip 9"}, true},
		{ZFSStorage{Compression: "1bad"}, true},
		{ZFSStorage{Comment: "two\nlines"}, true},
		{ZFSStorage{Properties: map[string]string{"failmode": "continue"}}, false},
		{ZFSStorage{Properties: map[string]string{"feature@lz4_compress": "enabled"}}, false},
		{ZFSStorage{Properties: map[string]string{"altroot": "/mnt"}}, true},
		{ZFSStorage{Properties: map[string]string{"cachefile": "/tmp/cache"}}, true},
		{ZFSStorage{Properties: map[string]string{"bad key": "1"}}, true},
		{ZFSStorage{Properties: map[string]string{"failmode": "two\nlines"}}, true},
		{ZFSStorage{Ashift: "12"}, false},
		{ZFSStorage{Ashift: "8"}, true},
		{ZFSStorage{Ashift: "twelve"}, true},
		{ZFSStorage{Name: "bootpool"}, true},
		{ZFSStorage{Name: "my-pool_1.x"}, false},
		{ZFSStorage{Name: "1tank"}, true},
		{ZFSStorage{Name: "tank/root"}, true},
		{ZFSStorage{Name: "mirror"}, true},
		{ZFSStorage{Name: "spare-pool"}, true},
		{ZFSStorage{Name: "c0t0"}, true},
	}
	for _, c := range cases {
		err := validateZFSOptions(c.opt)
		if c.wantErr && err == nil {
			t.Errorf("%+v: expected an error", c.opt)
		}
		if !c.wantErr && err != nil {
			t.Errorf("%+v: unexpected error %v", c.opt, err)
		}
	}
}
