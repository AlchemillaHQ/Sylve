// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package iface

/*
#include <ifaddrs.h>
#include <net/if.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <stdlib.h>
#include <sys/ioctl.h>
#include <sys/sysctl.h>
#include <unistd.h>
#include <string.h>
#include <net/if_types.h>
#include <net/if_dl.h>
#include <net/if_var.h>
#include <netinet6/in6_var.h>
#include <net/if_media.h>
#include <netinet6/nd6.h>
#include <sys/sockio.h>
#include <errno.h>
#include <net/if_bridgevar.h>

static int
get_stp_op_params(int fd, const char *ifname, struct ifbropreq *opr)
{
    struct ifdrv dr;

    memset(&dr, 0, sizeof(dr));
    strncpy(dr.ifd_name, ifname, IFNAMSIZ-1);
    dr.ifd_cmd  = BRDGPARAM;
    dr.ifd_len  = sizeof(*opr);
    dr.ifd_data = opr;

    return ioctl(fd, SIOCGDRVSPEC, &dr);
}

static int
get_bridge_param(int fd, const char *ifname, int cmd, struct ifbrparam *bp)
{
    struct ifdrv dr;
    memset(&dr, 0, sizeof(dr));
    strncpy(dr.ifd_name, ifname, IFNAMSIZ-1);
    dr.ifd_cmd  = cmd;
    dr.ifd_len  = sizeof(*bp);
    dr.ifd_data = bp;
    return ioctl(fd, SIOCGDRVSPEC, &dr);
}

static void set_ifname(char *dst, const char *src) {
	strncpy(dst, src, IFNAMSIZ);
	dst[IFNAMSIZ - 1] = '\0';
}

static int get_nd6_flags(int fd, const char *name) {
	struct in6_ndireq nd;
	memset(&nd, 0, sizeof(nd));
	strncpy(nd.ifname, name, IFNAMSIZ - 1);
	if (ioctl(fd, SIOCGIFINFO_IN6, &nd) < 0)
		return -1;
	return nd.ndi.flags;
}

static int get_in6_flags(int fd, const char *name, struct in6_addr addr, uint32_t scope_id) {
	struct in6_ifreq ifr6;
	memset(&ifr6, 0, sizeof(ifr6));
	set_ifname(ifr6.ifr_name, name);
	ifr6.ifr_addr.sin6_family = AF_INET6;
	ifr6.ifr_addr.sin6_len = sizeof(struct sockaddr_in6);
	ifr6.ifr_addr.sin6_scope_id = scope_id;
	memcpy(&ifr6.ifr_addr.sin6_addr, &addr, sizeof(struct in6_addr));

	if (ioctl(fd, SIOCGIFAFLAG_IN6, &ifr6) < 0)
		return -1;

	return ifr6.ifr_ifru.ifru_flags6;
}

static void get_lifetimes(int fd, const char *name, struct in6_addr addr, uint32_t scope_id, uint32_t *pltime, uint32_t *vltime) {
	struct in6_ifreq ifr6;
	memset(&ifr6, 0, sizeof(ifr6));
	set_ifname(ifr6.ifr_name, name);
	ifr6.ifr_addr.sin6_family = AF_INET6;
	ifr6.ifr_addr.sin6_len = sizeof(struct sockaddr_in6);
	ifr6.ifr_addr.sin6_scope_id = scope_id;
	memcpy(&ifr6.ifr_addr.sin6_addr, &addr, sizeof(struct in6_addr));

	if (ioctl(fd, SIOCGIFALIFETIME_IN6, &ifr6) < 0) {
		*pltime = 0;
		*vltime = 0;
		return;
	}
	*pltime = ifr6.ifr_ifru.ifru_lifetime.ia6t_pltime;
	*vltime = ifr6.ifr_ifru.ifru_lifetime.ia6t_vltime;
}

static uint32_t get_flagshigh(const struct ifreq *req) {
	union {
		const struct sockaddr *sa;
		const uint32_t *u32;
	} pun;

	pun.sa = &req->ifr_addr;
	return *pun.u32;
}

#ifndef SIOCGIFCAP
#define SIOCGIFCAP 1
#endif

static void get_capabilities(int fd, const char* name, uint32_t *enabled, uint32_t *supported) {
	struct ifreq req;
	memset(&req, 0, sizeof(req));
	strncpy(req.ifr_name, name, IFNAMSIZ - 1);

	if (ioctl(fd, SIOCGIFCAP, &req) < 0) {
		*enabled = 0;
		*supported = 0;
		return;
	}

	*enabled = req.ifr_curcap;
	*supported = req.ifr_reqcap;
}

static int ioctl_wrap(int fd, unsigned long req, void *arg) {
    return ioctl(fd, req, arg);
}

static uint64_t
get_combined_flags(int fd, const char *name)
{
    struct ifreq ifr;
    uint32_t low = 0, high = 0;

    memset(&ifr, 0, sizeof(ifr));
    strlcpy(ifr.ifr_name, name, sizeof(ifr.ifr_name));

    if (ioctl(fd, SIOCGIFFLAGS, &ifr) < 0) {
		return 0;
	}

    low = ifr.ifr_flags;

    return ((uint64_t)high << 32) | low;
}


static int get_media_info(int fd, const char *name, struct ifmediareq *ifmr) {
	memset(ifmr, 0, sizeof(struct ifmediareq));
	strncpy(ifmr->ifm_name, name, IFNAMSIZ - 1);
	return ioctl(fd, SIOCGIFMEDIA, ifmr);
}

static void* get_broadaddr(struct ifaddrs* a) {
	return a->ifa_broadaddr;
}

int is_bridge(const char *ifname) {
    int sock = socket(AF_INET, SOCK_DGRAM, 0);
    if (sock < 0) {
        return -1;
    }

    struct ifreq ifr;
    struct if_data ifd;

    memset(&ifr, 0, sizeof(ifr));
    strlcpy(ifr.ifr_name, ifname, sizeof(ifr.ifr_name));

    ifr.ifr_data = (caddr_t)&ifd;

    if (ioctl(sock, SIOCGIFDATA, &ifr) < 0) {
        close(sock);
        return -1;
    }

    close(sock);

    return (ifd.ifi_type == IFT_BRIDGE) ? 1 : 0;
}

static int
get_bridge_members(int fd, const char *ifname, struct ifbifconf *bifc)
{
    struct ifdrv dr;

    memset(bifc, 0, sizeof(*bifc));
    memset(&dr, 0, sizeof(dr));


    strlcpy(dr.ifd_name, ifname, IFNAMSIZ);
    dr.ifd_cmd  = BRDGGIFS;
    dr.ifd_len  = sizeof(*bifc);
    dr.ifd_data = bifc;
    if (ioctl(fd, SIOCGDRVSPEC, &dr) < 0)
        return -1;


    bifc->ifbic_buf = malloc(bifc->ifbic_len);
    if (bifc->ifbic_buf == NULL)
        return -1;

    dr.ifd_len  = sizeof(*bifc);
    dr.ifd_data = bifc;
    if (ioctl(fd, SIOCGDRVSPEC, &dr) < 0) {
        free(bifc->ifbic_buf);
        return -1;
    }

    return 0;
}



static int
get_interface_groups(int fd, const char *ifname,
                     struct ifg_req **out_req, int *out_cnt)
{
    struct ifgroupreq ifgr;
    memset(&ifgr, 0, sizeof(ifgr));
    strncpy(ifgr.ifgr_name, ifname, IFNAMSIZ-1);

    if (ioctl(fd, SIOCGIFGROUP, &ifgr) < 0)
        return -1;

    int len = ifgr.ifgr_len;
    struct ifg_req *buf = malloc(len);
    if (buf == NULL)
        return -1;

    ifgr.ifgr_groups = buf;
    ifgr.ifgr_len    = len;
    if (ioctl(fd, SIOCGIFGROUP, &ifgr) < 0) {
        free(buf);
        return -1;
    }

    *out_req = buf;
    *out_cnt = len / sizeof(*buf);
    return 0;
}


// Allocate and return a malloc()’d C‐string containing the
// interface description, or NULL on error or if empty.
static char *
get_ifdescr(int sock, const char *ifname)
{
    struct ifreq ifr;
    char *buf = NULL;
    size_t buflen = 128;

    // initial alloc
    buf = malloc(buflen);
    if (buf == NULL)
        return NULL;

    strncpy(ifr.ifr_name, ifname, IFNAMSIZ-1);
    ifr.ifr_buffer.buffer = buf;
    ifr.ifr_buffer.length = buflen;

    for (;;) {
        if (ioctl(sock, SIOCGIFDESCR, &ifr) < 0) {
            free(buf);
            return NULL;
        }
        // if the kernel wrote into our buffer pointer, we're done
        if (ifr.ifr_buffer.buffer == buf) {
            if (strlen(buf) > 0)
                return buf;
            free(buf);
            return NULL;
        }
        // otherwise it grew the buffer: reallocate and retry
        buflen = ifr.ifr_buffer.length;
        buf = realloc(buf, buflen);
        if (buf == NULL)
            return NULL;
        ifr.ifr_buffer.buffer = buf;
        ifr.ifr_buffer.length = buflen;
    }
}

static int
get_hwaddr(int fd, const char *ifname, unsigned char *out, int *outlen)
{
    struct ifreq ifr;
    memset(&ifr, 0, sizeof(ifr));
    strncpy(ifr.ifr_name, ifname, IFNAMSIZ-1);

    if (ioctl(fd, SIOCGHWADDR, &ifr) < 0)
        return -1;

    struct sockaddr *sa = &ifr.ifr_addr;

    if (sa->sa_family == AF_LINK && sa->sa_len >= sizeof(struct sockaddr)) {
        struct sockaddr_dl *sdl = (struct sockaddr_dl *)sa;
        const unsigned char *ll = (const unsigned char *)sdl->sdl_data + sdl->sdl_nlen;
        if (sdl->sdl_alen > 0 && sdl->sdl_alen <= 32) {
            memcpy(out, ll, sdl->sdl_alen);
            *outlen = sdl->sdl_alen;
            return 0;
        }
    }

    if (sa->sa_len >= sizeof(struct sockaddr)) {
        memcpy(out, sa->sa_data, 6);
        *outlen = 6;
        return 0;
    }

    memcpy(out, sa->sa_data, 6);
    *outlen = 6;
    return 0;
}

static uint32_t get_mtu(const struct ifreq *ifr)    { return ifr->ifr_mtu; }
static uint32_t get_metric(const struct ifreq *ifr) { return ifr->ifr_metric; }

static int get_errno(void) { return errno; }
*/
import "C"

import (
	"fmt"
	"net"
	"strings"
	"unsafe"
)

func (iface *Interface) String() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("%s: flags=%x<%s> metric %d mtu %d\n",
		iface.Name,
		iface.Flags.Raw,
		strings.Join(iface.Flags.Desc, ","),
		iface.Metric,
		iface.MTU,
	))

	if iface.Ether != "" {
		sb.WriteString(fmt.Sprintf("\tether %s\n", iface.Ether))
	}

	if iface.HWAddr != "" {
		sb.WriteString(fmt.Sprintf("\thwaddr %s\n", iface.HWAddr))
	}

	if iface.Description != "" {
		sb.WriteString(fmt.Sprintf("\tdescription: %s\n", iface.Description))
	}

	if iface.Model != "" {
		sb.WriteString(fmt.Sprintf("\tmodel: %s\n", iface.Model))
	}

	if iface.Capabilities.Enabled.Raw != 0 {
		sb.WriteString(fmt.Sprintf("\toptions=%x<%s>\n", iface.Capabilities.Enabled.Raw, strings.Join(iface.Capabilities.Enabled.Desc, ",")))
	}

	if iface.Capabilities.Supported.Raw != 0 {
		sb.WriteString(fmt.Sprintf("\tcapabilities=%x<%s>\n", iface.Capabilities.Supported.Raw, strings.Join(iface.Capabilities.Supported.Desc, ",")))
	}

	for _, a := range iface.IPv4 {
		sb.WriteString(fmt.Sprintf("\tinet %s", a.IP))
		if a.Netmask != "" {
			sb.WriteString(fmt.Sprintf(" netmask %s", a.Netmask))
		}
		if a.Broadcast != nil {
			sb.WriteString(fmt.Sprintf(" broadcast %s", a.Broadcast.String()))
		}
		sb.WriteString("\n")
	}

	for _, a := range iface.IPv6 {
		sb.WriteString(fmt.Sprintf("\tinet6 %s prefixlen %d", a.IP, a.PrefixLength))
		if a.ScopeID != 0 {
			sb.WriteString(fmt.Sprintf(" scopeid 0x%x", a.ScopeID))
		}

		if a.AutoConf {
			sb.WriteString(" autoconf")
		}

		if a.Detached {
			sb.WriteString(" detached")
		}

		if a.Deprecated {
			sb.WriteString(" deprecated")
		}

		if a.LifeTimes.Preferred > 0 && a.LifeTimes.Preferred != 0xffffffff {
			sb.WriteString(fmt.Sprintf(" pltime %d", a.LifeTimes.Preferred))
		}

		if a.LifeTimes.Valid > 0 && a.LifeTimes.Valid != 0xffffffff {
			sb.WriteString(fmt.Sprintf(" vltime %d", a.LifeTimes.Valid))
		}

		sb.WriteString("\n")
	}

	if iface.Media != nil {
		sb.WriteString(fmt.Sprintf("\tmedia: %s %s", iface.Media.Type, iface.Media.Subtype))
		if iface.Media.Mode != "" {
			sb.WriteString(fmt.Sprintf(" %s", iface.Media.Mode))
		}
		if len(iface.Media.Options) > 0 {
			sb.WriteString(fmt.Sprintf(" <%s>", strings.Join(iface.Media.Options, ",")))
		}
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf("\tstatus: %s\n", iface.Media.Status))
	}

	if iface.STP != nil {
		sb.WriteString(fmt.Sprintf("\tid %s priority %d hellotime %d fwddelay %d\n", iface.BridgeID, iface.STP.Priority, iface.STP.HelloTime, iface.STP.FwdDelay))
		sb.WriteString(fmt.Sprintf("\tmaxage %d holdcnt %d proto %s maxaddr %d timeout %d\n", iface.STP.MaxAge, iface.STP.HoldCnt, iface.STP.Proto, iface.MaxAddr, iface.Timeout))
		sb.WriteString(fmt.Sprintf("\troot id %s priority %d ifcost %d port %d\n", iface.STP.RootID, iface.STP.RootPriority, iface.STP.RootPathCost, iface.STP.RootPort))
	}

	if len(iface.BridgeMembers) > 0 {
		for _, member := range iface.BridgeMembers {
			sb.WriteString(fmt.Sprintf("\tmember: %s flags=%x<%s>\n", member.Name, member.Flags.Raw, strings.Join(member.Flags.Desc, ",")))
			sb.WriteString(fmt.Sprintf("\t\tifmaxaddr %d port %d priority %d path cost %d\n", member.IfMaxAddr, member.Port, member.Priority, member.PathCost))
		}
	}

	if len(iface.Groups) > 0 {
		sb.WriteString(fmt.Sprintf("\tgroups: %s\n", strings.Join(iface.Groups, ",")))
	}

	if iface.Driver != "" {
		sb.WriteString(fmt.Sprintf("\tdrivername: %s\n", iface.Driver))
	}

	if iface.ND6.Raw != 0 {
		sb.WriteString(fmt.Sprintf("\tnd6 options=%x<%s>\n",
			iface.ND6.Raw,
			strings.Join(iface.ND6.Desc, ","),
		))
	}

	return sb.String()
}

func parseBridgeFlags(f uint32) []string {
	var bridgeFlagDesc = []FlagDescriptor{
		{Mask: C.IFBIF_LEARNING, Name: "LEARNING"},
		{Mask: C.IFBIF_DISCOVER, Name: "DISCOVER"},
		{Mask: C.IFBIF_STP, Name: "STP"},
		{Mask: C.IFBIF_SPAN, Name: "SPAN"},
		{Mask: C.IFBIF_STICKY, Name: "STICKY"},
		{Mask: C.IFBIF_BSTP_AUTOEDGE, Name: "AUTOEDGE"},
		{Mask: C.IFBIF_BSTP_AUTOPTP, Name: "AUTOPTP"},
	}

	desc, _ := parseFlags(f, bridgeFlagDesc)
	return desc
}

func getInterfaceInfo(name string) (*Interface, error) {
	fd4 := C.socket(C.AF_INET, C.SOCK_DGRAM, 0)
	if fd4 < 0 {
		return nil, fmt.Errorf("socket(AF_INET) failed")
	}
	defer C.close(fd4)

	fd6 := C.socket(C.AF_INET6, C.SOCK_DGRAM, 0)
	if fd6 < 0 {
		return nil, fmt.Errorf("socket(AF_INET6) failed")
	}
	defer C.close(fd6)

	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	var nd6 ND6
	if flags := C.get_nd6_flags(fd6, cname); flags >= 0 {
		nd6.Raw = uint32(flags)
		nd6.Desc = parseND6Options(nd6.Raw)
	}

	actDesc := ""

	cDesc := C.get_ifdescr(fd4, cname)
	if cDesc != nil {
		actDesc = C.GoString(cDesc)
		C.free(unsafe.Pointer(cDesc))
	}

	raw := uint32(C.get_combined_flags(fd4, cname))
	known := raw & knownFlagMask()
	if known == 0 {
		return nil, fmt.Errorf("get_combined_flags failed for %s", name)
	}
	flagDesc := parseFlagsDesc(known)

	var req C.struct_ifreq
	C.strncpy(&req.ifr_name[0], cname, C.IFNAMSIZ-1)

	mtu, metric := 0, 0
	if C.ioctl_wrap(fd4, C.SIOCGIFMTU, unsafe.Pointer(&req)) >= 0 {
		mtu = int(C.get_mtu(&req))
	}
	if C.ioctl_wrap(fd4, C.SIOCGIFMETRIC, unsafe.Pointer(&req)) >= 0 {
		metric = int(C.get_metric(&req))
	}

	var capEn, capSu C.uint32_t
	C.get_capabilities(fd4, cname, &capEn, &capSu)
	capabilities := Capabilities{
		Enabled:   Flags{Raw: uint32(capEn), Desc: parseCapabilitiesDesc(uint32(capEn))},
		Supported: Flags{Raw: uint32(capSu), Desc: parseCapabilitiesDesc(uint32(capSu))},
	}

	iface := &Interface{
		Name:         name,
		Flags:        Flags{Raw: known, Desc: flagDesc},
		MTU:          mtu,
		Metric:       metric,
		Capabilities: capabilities,
		ND6:          nd6,
		Description:  actDesc,
	}

	iface.Media = getMediaInfo(fd4, name)

	if C.is_bridge(cname) != 0 {
		iface.Driver = "bridge"

		var opr C.struct_ifbropreq
		if C.get_stp_op_params(fd4, cname, &opr) >= 0 {
			rawBridge := uint64(opr.ifbop_bridgeid)
			var baddr [6]byte
			for i := 0; i < 6; i++ {
				shift := uint((5 - i) * 8)
				baddr[i] = byte((rawBridge >> shift) & 0xff)
			}
			iface.BridgeID = net.HardwareAddr(baddr[:]).String()
			iface.STP = &STP{
				Priority:  int(opr.ifbop_priority),
				HelloTime: int(opr.ifbop_hellotime),
				FwdDelay:  int(opr.ifbop_fwddelay),
				MaxAge:    int(opr.ifbop_maxage),
				HoldCnt:   int(opr.ifbop_holdcount),
				Proto:     parseSTPProto(uint8(opr.ifbop_protocol)),
			}
			rawRoot := uint64(opr.ifbop_designated_root)
			var raddr [6]byte
			for i := 0; i < 6; i++ {
				shift := uint((5 - i) * 8)
				raddr[i] = byte((rawRoot >> shift) & 0xff)
			}
			iface.STP.RootID = net.HardwareAddr(raddr[:]).String()
			iface.STP.RootPriority = int(rawRoot >> 48)
			iface.STP.RootPathCost = int(opr.ifbop_root_path_cost)
			iface.STP.RootPort = int(opr.ifbop_root_port) & 0xfff
		}

		var bp C.struct_ifbrparam
		if C.get_bridge_param(fd4, cname, C.BRDGGCACHE, &bp) >= 0 {
			raw := *(*C.uint32_t)(unsafe.Pointer(&bp.ifbrp_ifbrpu[0]))
			iface.MaxAddr = int(raw)
		}
		if C.get_bridge_param(fd4, cname, C.BRDGGTO, &bp) >= 0 {
			raw := *(*C.uint32_t)(unsafe.Pointer(&bp.ifbrp_ifbrpu[0]))
			iface.Timeout = int(raw)
		}

		var bifc C.struct_ifbifconf
		if C.get_bridge_members(fd4, cname, &bifc) == 0 {
			unionPtr := unsafe.Pointer(&bifc.ifbic_ifbicu[0])
			bufPtr := *(*unsafe.Pointer)(unionPtr)
			defer C.free(bufPtr)

			elemSize := unsafe.Sizeof(C.struct_ifbreq{})
			count := int(bifc.ifbic_len) / int(elemSize)
			reqPtr := *(**C.struct_ifbifreq)(unionPtr)
			entries := (*[1 << 28]C.struct_ifbreq)(unsafe.Pointer(reqPtr))[:count:count]
			for _, entry := range entries {
				rawFlags := uint32(entry.ifbr_ifsflags)
				member := BridgeMember{
					Name:      C.GoString(&entry.ifbr_ifsname[0]),
					Flags:     Flags{Raw: rawFlags, Desc: parseBridgeFlags(rawFlags)},
					IfMaxAddr: int(entry.ifbr_addrmax),
					State:     int(entry.ifbr_state),
					Priority:  int(entry.ifbr_priority),
					Port:      int(entry.ifbr_portno),
					PathCost:  int(entry.ifbr_path_cost),
				}
				iface.BridgeMembers = append(iface.BridgeMembers, member)
			}
		}
	}

	var grpPtr *C.struct_ifg_req
	var grpCount C.int
	if C.get_interface_groups(fd4, cname, &grpPtr, &grpCount) == 0 {
		defer C.free(unsafe.Pointer(grpPtr))
		cnt := int(grpCount)
		elemSize := unsafe.Sizeof(*grpPtr)
		for i := 0; i < cnt; i++ {
			entryPtr := unsafe.Pointer(uintptr(unsafe.Pointer(grpPtr)) + uintptr(i)*elemSize)
			grp := C.GoString((*C.char)(entryPtr))
			if grp != "all" {
				iface.Groups = append(iface.Groups, grp)
			}
		}
	}

	{
		var buf [32]C.uchar
		var alen C.int
		if C.get_hwaddr(fd4, cname, &buf[0], &alen) == 0 && alen > 0 {
			mac := C.GoBytes(unsafe.Pointer(&buf[0]), alen)
			hw := net.HardwareAddr(mac).String()
			// Only set if different from the current L2 address we’ll parse from AF_LINK
			iface.HWAddr = hw
		}
	}

	return iface, nil
}

func getMediaInfo(fd C.int, name string) *Media {
	var ifmr C.struct_ifmediareq
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	if C.get_media_info(fd, cname, &ifmr) < 0 {
		return nil
	}

	active := int(ifmr.ifm_active)
	current := int(ifmr.ifm_current)

	media := &Media{
		Type:       parseMediaTypeBase(active),
		Subtype:    parseMediaSubtype(active),
		Options:    parseMediaOptions(active),
		Mode:       parseMediaMode(current),
		Status:     parseMediaStatus(ifmr.ifm_status, active&0x000000e0),
		RawCurrent: current,
		RawActive:  active,
	}

	return media
}

func List() ([]*Interface, error) {
	var addrs *C.struct_ifaddrs
	if C.getifaddrs(&addrs) != 0 {
		return nil, fmt.Errorf("getifaddrs failed")
	}
	defer C.freeifaddrs(addrs)

	seen := make(map[string]bool)
	var result []*Interface

	for a := addrs; a != nil; a = a.ifa_next {
		if a.ifa_addr == nil {
			continue
		}

		name := C.GoString(a.ifa_name)
		if seen[name] {
			continue
		}
		seen[name] = true

		iface, err := Get(name)
		if err != nil {
			continue
		}
		result = append(result, iface)
	}

	return result, nil
}

func Get(name string) (*Interface, error) {
	fd6 := C.socket(C.AF_INET6, C.SOCK_DGRAM, 0)
	if fd6 < 0 {
		return nil, fmt.Errorf("socket(AF_INET6) failed")
	}
	defer C.close(fd6)

	var addrs *C.struct_ifaddrs
	if C.getifaddrs(&addrs) != 0 {
		return nil, fmt.Errorf("getifaddrs failed")
	}
	defer C.freeifaddrs(addrs)

	var iface *Interface
	for a := addrs; a != nil; a = a.ifa_next {
		if a.ifa_addr == nil {
			continue
		}

		ifaceName := C.GoString(a.ifa_name)
		if ifaceName != name {
			continue
		}

		if iface == nil {
			var err error
			iface, err = getInterfaceInfo(name)
			if err != nil {
				return nil, err
			}
			iface.Driver = getSysctlProperty(name, "driver")
			iface.Model = getSysctlProperty(name, "desc")
		}

		cname := C.CString(name)
		defer C.free(unsafe.Pointer(cname))

		switch a.ifa_addr.sa_family {
		case C.AF_INET:
			sa := (*C.struct_sockaddr_in)(unsafe.Pointer(a.ifa_addr))
			ip := net.IP(C.GoBytes(unsafe.Pointer(&sa.sin_addr), 4))

			var netmask net.IPMask
			if a.ifa_netmask != nil {
				nm := (*C.struct_sockaddr_in)(unsafe.Pointer(a.ifa_netmask))
				netmask = net.IPMask(C.GoBytes(unsafe.Pointer(&nm.sin_addr), 4))
			}

			var broadcast net.IP
			baPtr := C.get_broadaddr(a)
			if iface.Flags.Raw&C.IFF_BROADCAST != 0 && baPtr != nil {
				ba := (*C.struct_sockaddr_in)(unsafe.Pointer(baPtr))
				broadcast = make(net.IP, 4)
				C.memcpy(unsafe.Pointer(&broadcast[0]), unsafe.Pointer(&ba.sin_addr), 4)
			}

			iface.IPv4 = append(iface.IPv4, IPv4{
				IP:        ip,
				Netmask:   net.IP(netmask).String(),
				Broadcast: broadcast,
			})

		case C.AF_INET6:
			sa := (*C.struct_sockaddr_in6)(unsafe.Pointer(a.ifa_addr))
			ip := net.IP(C.GoBytes(unsafe.Pointer(&sa.sin6_addr), 16))

			prefixLen := 0
			if a.ifa_netmask != nil {
				nm := (*C.struct_sockaddr_in6)(unsafe.Pointer(a.ifa_netmask))
				mask := net.IPMask(C.GoBytes(unsafe.Pointer(&nm.sin6_addr), 16))
				prefixLen, _ = mask.Size()
			}

			scopeID := uint32(sa.sin6_scope_id)
			addr := *(*[16]byte)(unsafe.Pointer(&sa.sin6_addr))
			var caddr C.struct_in6_addr
			C.memcpy(unsafe.Pointer(&caddr), unsafe.Pointer(&addr), C.sizeof_struct_in6_addr)

			raw := C.get_in6_flags(fd6, cname, caddr, C.uint32_t(scopeID))

			plTime := C.uint32_t(0)
			vlTime := C.uint32_t(0)
			C.get_lifetimes(fd6, cname, caddr, C.uint32_t(scopeID), &plTime, &vlTime)

			ipv6 := IPv6{
				IP:           ip,
				PrefixLength: prefixLen,
				ScopeID:      scopeID,
			}

			if raw >= 0 {
				flags := uint32(raw)
				ipv6.AutoConf = flags&C.IN6_IFF_AUTOCONF != 0
				ipv6.Detached = flags&C.IN6_IFF_DETACHED != 0
				ipv6.Deprecated = flags&C.IN6_IFF_DEPRECATED != 0
			}

			ipv6.LifeTimes.Preferred = uint32(plTime)
			ipv6.LifeTimes.Valid = uint32(vlTime)

			iface.IPv6 = append(iface.IPv6, ipv6)

		case C.AF_LINK:
			sdl := (*C.struct_sockaddr_dl)(unsafe.Pointer(a.ifa_addr))
			if sdl.sdl_type == C.IFT_ETHER && sdl.sdl_alen == 6 {
				mac := C.GoBytes(unsafe.Pointer(&sdl.sdl_data[sdl.sdl_nlen]), 6)
				iface.Ether = net.HardwareAddr(mac).String()
			}
		}
	}

	if iface == nil {
		return nil, fmt.Errorf("interface %s not found", name)
	}

	return iface, nil
}
