// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package iface

import (
	"net"
	"strings"
	"testing"
)

func TestInterfaceStringIncludesCoreFields(t *testing.T) {
	i := &Interface{
		Name:        "em0",
		Flags:       Flags{Raw: 0x41, Desc: []string{"UP", "RUNNING"}},
		Metric:      5,
		MTU:         1500,
		Ether:       "aa:bb:cc:dd:ee:ff",
		HWAddr:      "11:22:33:44:55:66",
		Description: "uplink",
		Model:       "Intel NIC",
		Capabilities: Capabilities{
			Enabled:   Flags{Raw: 0x3, Desc: []string{"RXCSUM", "TXCSUM"}},
			Supported: Flags{Raw: 0x7, Desc: []string{"RXCSUM", "TXCSUM", "NETCONS"}},
		},
		IPv4: []IPv4{{
			IP:        net.ParseIP("192.0.2.10"),
			Netmask:   "255.255.255.0",
			Broadcast: net.ParseIP("192.0.2.255"),
		}},
		IPv6: []IPv6{{
			IP:           net.ParseIP("2001:db8::10"),
			PrefixLength: 64,
			ScopeID:      2,
			AutoConf:     true,
			Detached:     true,
			Deprecated:   true,
			LifeTimes: struct {
				Preferred uint32 `json:"preferred"`
				Valid     uint32 `json:"valid"`
			}{
				Preferred: 7200,
				Valid:     14400,
			},
		}},
		Media: &Media{
			Type:    "Ethernet",
			Subtype: "1000baseT",
			Mode:    "autoselect",
			Options: []string{"full-duplex"},
			Status:  "active",
		},
		BridgeID: "00:11:22:33:44:55",
		STP: &STP{
			Priority:     32768,
			HelloTime:    2,
			FwdDelay:     15,
			MaxAge:       20,
			HoldCnt:      6,
			Proto:        "rstp",
			RootID:       "66:77:88:99:aa:bb",
			RootPriority: 32768,
			RootPathCost: 10,
			RootPort:     2,
		},
		MaxAddr: 100,
		Timeout: 1200,
		BridgeMembers: []BridgeMember{{
			Name:      "tap0",
			Flags:     Flags{Raw: 0x5, Desc: []string{"LEARNING", "STP"}},
			IfMaxAddr: 100,
			Port:      1,
			Priority:  128,
			PathCost:  4,
		}},
		Groups: []string{"bridge", "vnet"},
		Driver: "bridge",
		ND6:    ND6{Flags: Flags{Raw: 0x21, Desc: []string{"PERFORMNUD", "AUTO_LINKLOCAL"}}},
	}

	out := i.String()

	checks := []string{
		"em0: flags=41<UP,RUNNING> metric 5 mtu 1500",
		"\tether aa:bb:cc:dd:ee:ff",
		"\thwaddr 11:22:33:44:55:66",
		"\tdescription: uplink",
		"\tmodel: Intel NIC",
		"\toptions=3<RXCSUM,TXCSUM>",
		"\tcapabilities=7<RXCSUM,TXCSUM,NETCONS>",
		"\tinet 192.0.2.10 netmask 255.255.255.0 broadcast 192.0.2.255",
		"\tinet6 2001:db8::10 prefixlen 64 scopeid 0x2 autoconf detached deprecated pltime 7200 vltime 14400",
		"\tmedia: Ethernet 1000baseT autoselect <full-duplex>",
		"\tstatus: active",
		"\tid 00:11:22:33:44:55 priority 32768 hellotime 2 fwddelay 15",
		"\tmaxage 20 holdcnt 6 proto rstp maxaddr 100 timeout 1200",
		"\troot id 66:77:88:99:aa:bb priority 32768 ifcost 10 port 2",
		"\tmember: tap0 flags=5<LEARNING,STP>",
		"\t\tifmaxaddr 100 port 1 priority 128 path cost 4",
		"\tgroups: bridge,vnet",
		"\tdrivername: bridge",
		"\tnd6 options=21<PERFORMNUD,AUTO_LINKLOCAL>",
	}

	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Fatalf("expected String() output to contain %q, got:\n%s", check, out)
		}
	}
}

func TestInterfaceStringOmitsInfiniteIPv6Lifetimes(t *testing.T) {
	i := &Interface{
		Name:  "em1",
		Flags: Flags{Raw: 0x1, Desc: []string{"UP"}},
		IPv6: []IPv6{{
			IP:           net.ParseIP("2001:db8::20"),
			PrefixLength: 64,
			LifeTimes: struct {
				Preferred uint32 `json:"preferred"`
				Valid     uint32 `json:"valid"`
			}{
				Preferred: 0xffffffff,
				Valid:     0xffffffff,
			},
		}},
	}

	out := i.String()
	if strings.Contains(out, "pltime") {
		t.Fatalf("did not expect pltime for infinite preferred lifetime, got:\n%s", out)
	}
	if strings.Contains(out, "vltime") {
		t.Fatalf("did not expect vltime for infinite valid lifetime, got:\n%s", out)
	}
}

func TestGetReturnsErrorForMissingInterfaceOnFreeBSD(t *testing.T) {
	name := "sylve-non-existent-iface"
	iface, err := Get(name)
	if err == nil {
		t.Fatal("expected error for missing interface, got nil")
	}
	if iface != nil {
		t.Fatalf("expected nil interface for missing lookup, got %+v", iface)
	}
	if !strings.Contains(err.Error(), name) {
		t.Fatalf("expected error to include interface name %q, got %q", name, err.Error())
	}
}
