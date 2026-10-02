// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package iface

import "C"

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/alchemillahq/sylve/pkg/utils/sysctl"
)

var deviceUnitPattern = regexp.MustCompile(`^(.+?)(\d+)$`)

func parseFlags(flags uint32, descriptors []FlagDescriptor) ([]string, uint32) {
	var descriptions []string
	remaining := flags

	for _, desc := range descriptors {
		if flags&desc.Mask != 0 {
			descriptions = append(descriptions, desc.Name)
			remaining &^= desc.Mask
		}
	}

	if remaining != 0 {
		descriptions = append(descriptions, fmt.Sprintf("UNKNOWN_0x%x", remaining))
	}

	return descriptions, remaining
}

func getSysctlProperty(iface, prop string) string {
	if strings.HasPrefix(iface, "lo") {
		if prop == "driver" {
			return "lo"
		}
		return "Loopback"
	}

	matches := deviceUnitPattern.FindStringSubmatch(iface)
	if len(matches) != 3 {
		return ""
	}

	key := fmt.Sprintf("dev.%s.%s.%%%s", matches[1], matches[2], prop)
	value, err := sysctl.GetString(key)
	if err != nil {
		return ""
	}

	return strings.TrimRight(value, "\x00")
}

func parseFlagsDesc(fl uint32) []string {
	descriptors := []FlagDescriptor{
		{Mask: 0x1, Name: "UP"},
		{Mask: 0x2, Name: "BROADCAST"},
		{Mask: 0x8, Name: "LOOPBACK"},
		{Mask: 0x10, Name: "POINTOPOINT"},
		{Mask: 0x40, Name: "RUNNING"},
		{Mask: 0x80, Name: "NOARP"},
		{Mask: 0x100, Name: "PROMISC"},
		{Mask: 0x200, Name: "ALLMULTI"},
		{Mask: 0x400, Name: "OACTIVE"},
		{Mask: 0x800, Name: "SIMPLEX"},
		{Mask: 0x1000, Name: "LINK0"},
		{Mask: 0x2000, Name: "LINK1"},
		{Mask: 0x4000, Name: "LINK2"},
		{Mask: 0x8000, Name: "MULTICAST"},
		{Mask: 0x01000000, Name: "LOWER_UP"},
	}

	descriptions, _ := parseFlags(fl, descriptors)
	return descriptions
}

func parseCapabilitiesDesc(caps uint32) []string {
	descriptors := []FlagDescriptor{
		{Mask: 1 << 0, Name: "RXCSUM"},
		{Mask: 1 << 1, Name: "TXCSUM"},
		{Mask: 1 << 2, Name: "NETCONS"},
		{Mask: 1 << 3, Name: "VLAN_MTU"},
		{Mask: 1 << 4, Name: "VLAN_HWTAGGING"},
		{Mask: 1 << 5, Name: "JUMBO_MTU"},
		{Mask: 1 << 6, Name: "POLLING"},
		{Mask: 1 << 7, Name: "VLAN_HWCSUM"},
		{Mask: 1 << 8, Name: "TSO4"},
		{Mask: 1 << 9, Name: "TSO6"},
		{Mask: 1 << 10, Name: "LRO"},
		{Mask: 1 << 11, Name: "WOL_UCAST"},
		{Mask: 1 << 12, Name: "WOL_MCAST"},
		{Mask: 1 << 13, Name: "WOL_MAGIC"},
		{Mask: 1 << 14, Name: "TOE4"},
		{Mask: 1 << 15, Name: "TOE6"},
		{Mask: 1 << 16, Name: "VLAN_HWFILTER"},
		{Mask: 1 << 17, Name: "NV"},
		{Mask: 1 << 18, Name: "VLAN_HWTSO"},
		{Mask: 1 << 19, Name: "LINKSTATE"},
		{Mask: 1 << 20, Name: "NETMAP"},
		{Mask: 1 << 21, Name: "RXCSUM_IPV6"},
		{Mask: 1 << 22, Name: "TXCSUM_IPV6"},
		{Mask: 1 << 23, Name: "HWSTATS"},
		{Mask: 1 << 24, Name: "TXRTLMT"},
		{Mask: 1 << 25, Name: "HWRXTSTMP"},
		{Mask: 1 << 26, Name: "MEXTPG"},
		{Mask: 1 << 27, Name: "TXTLS4"},
		{Mask: 1 << 28, Name: "TXTLS6"},
		{Mask: 1 << 29, Name: "VXLAN_HWCSUM"},
		{Mask: 1 << 30, Name: "VXLAN_HWTSO"},
		{Mask: 1 << 31, Name: "TXTLS_RTLMT"},
	}

	descriptions, _ := parseFlags(caps, descriptors)
	return descriptions
}

func knownFlagMask() uint32 {
	return 0x1 | 0x2 | 0x8 | 0x10 | 0x40 | 0x80 | 0x100 | 0x200 | 0x400 |
		0x800 | 0x1000 | 0x2000 | 0x4000 | 0x8000 | 0x01000000
}

func parseSTPProto(p uint8) string {
	switch p {
	case 0:
		return "stp"
	case 1:
		return "-"
	case 2:
		return "rstp"
	default:
		return fmt.Sprintf("%d", p)
	}
}

func parseMediaOptions(active int) []string {
	var opts []string
	if active&0x00100000 != 0 {
		opts = append(opts, "full-duplex")
	}
	if active&0x00200000 != 0 {
		opts = append(opts, "half-duplex")
	}
	return opts
}

func parseMediaTypeBase(active int) string {
	switch active & 0xe0 {
	case 0x20:
		return "Ethernet"
	case 0x40:
		return "Token Ring"
	case 0x60:
		return "FDDI"
	case 0x80:
		return "Wi-Fi"
	case 0xa0:
		return "ATM"
	default:
		return fmt.Sprintf("Unknown (0x%x)", active&0xe0)
	}
}

func parseMediaSubtype(active int) string {
	subtypeMap := map[int]string{
		3: "10baseT/UTP", 6: "100baseTX", 16: "1000baseT", 26: "10Gbase-T",
		27: "40Gbase-CR4", 28: "40Gbase-SR4", 29: "40Gbase-LR4",
	}
	return subtypeMap[active&0x1f]
}

func parseMediaMode(current int) string {
	switch current & 0x1f {
	case 0:
		return "autoselect"
	case 1:
		return "manual"
	case 2:
		return "none"
	default:
		return fmt.Sprintf("mode-0x%x", current&0x1f)
	}
}

func parseMediaStatus(status C.int, mediaType int) string {
	const (
		IFM_AVALID = 0x00000001
		IFM_ACTIVE = 0x00000002
	)

	type statusDesc struct {
		validMask int
		activeBit int
		inactive  string
		active    string
	}

	descs := map[int]statusDesc{
		0x00000020: {IFM_AVALID, IFM_ACTIVE, "no carrier", "active"},
		0x00000040: {IFM_AVALID, IFM_ACTIVE, "no ring", "inserted"},
		0x00000060: {IFM_AVALID, IFM_ACTIVE, "no ring", "inserted"},
		0x00000080: {IFM_AVALID, IFM_ACTIVE, "no network", "active"},
		0x000000a0: {IFM_AVALID, IFM_ACTIVE, "no network", "active"},
	}

	st := int(status)

	if desc, ok := descs[mediaType]; ok {
		if st&desc.validMask != 0 {
			if st&desc.activeBit != 0 {
				return desc.active
			}
			return desc.inactive
		}
	}

	return "unknown"
}

func parseND6Options(flags uint32) []string {
	const (
		ND6_IFF_PERFORMNUD       = 0x01
		ND6_IFF_ACCEPT_RTADV     = 0x02
		ND6_IFF_PREFER_SOURCE    = 0x04
		ND6_IFF_IFDISABLED       = 0x08
		ND6_IFF_DONT_SET_IFROUTE = 0x10
		ND6_IFF_AUTO_LINKLOCAL   = 0x20
		ND6_IFF_NO_RADR          = 0x40
		ND6_IFF_NO_PREFER_IFACE  = 0x80
	)

	opts := []struct {
		mask uint32
		name string
	}{
		{ND6_IFF_PERFORMNUD, "PERFORMNUD"},
		{ND6_IFF_ACCEPT_RTADV, "ACCEPT_RTADV"},
		{ND6_IFF_PREFER_SOURCE, "PREFER_SOURCE"},
		{ND6_IFF_IFDISABLED, "IFDISABLED"},
		{ND6_IFF_DONT_SET_IFROUTE, "DONT_SET_IFROUTE"},
		{ND6_IFF_AUTO_LINKLOCAL, "AUTO_LINKLOCAL"},
		{ND6_IFF_NO_RADR, "NO_RADR"},
		{ND6_IFF_NO_PREFER_IFACE, "NO_PREFER_IFACE"},
	}

	var out []string
	for _, opt := range opts {
		if flags&opt.mask != 0 {
			out = append(out, opt.name)
		}
	}
	return out
}
