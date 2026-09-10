// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

#include <sys/types.h>
#include <sys/ioctl.h>
#include <sys/socket.h>

#include <errno.h>
#include <limits.h>
#include <stdint.h>
#include <string.h>
#include <unistd.h>

#include <net/if.h>
#include <net/if_bridgevar.h>

#include "backend_freebsd.h"

_Static_assert(sizeof(ifbr_flags_t) == sizeof(uint32_t),
    "ifbr_flags_t must remain 32 bits");
_Static_assert(sizeof(((struct ifbrparam *)0)->ifbrp_flags) ==
    sizeof(uint32_t), "ifbrp_flags must remain 32 bits");
_Static_assert(sizeof(((struct ifbrparam *)0)->ifbrp_defpvid) ==
    sizeof(uint16_t), "ifbrp_defpvid must remain 16 bits");
_Static_assert(sizeof(((struct ifbreq *)0)->ifbr_ifsflags) ==
    sizeof(uint32_t), "ifbr_ifsflags must remain 32 bits");
_Static_assert(sizeof(((struct ifbreq *)0)->ifbr_pvid) ==
    sizeof(uint16_t), "ifbr_pvid must remain 16 bits");
_Static_assert(sizeof(((struct ifbreq *)0)->ifbr_vlanproto) ==
    sizeof(uint16_t), "ifbr_vlanproto must remain 16 bits");
_Static_assert(sizeof(ifbvlan_set_t) * CHAR_BIT == BRVLAN_SETSIZE,
    "ifbvlan_set_t must contain exactly BRVLAN_SETSIZE bits");

static int
sylve_bridge_validate_name(const char *name)
{
	size_t length;

	if (name == NULL) {
		errno = EINVAL;
		return (-1);
	}
	length = strnlen(name, IFNAMSIZ);
	if (length == 0 || length >= IFNAMSIZ) {
		errno = EINVAL;
		return (-1);
	}
	return (0);
}

static int
sylve_bridge_cmd(const char *bridge, unsigned long command, void *data,
    size_t length, int set)
{
	struct ifdrv ifd;
	int error, fd, saved_errno;

	if (sylve_bridge_validate_name(bridge) < 0 || data == NULL ||
	    length == 0) {
		errno = EINVAL;
		return (-1);
	}

	fd = socket(AF_LOCAL, SOCK_DGRAM, 0);
	if (fd < 0)
		return (-1);

	memset(&ifd, 0, sizeof(ifd));
	strlcpy(ifd.ifd_name, bridge, sizeof(ifd.ifd_name));
	ifd.ifd_cmd = command;
	ifd.ifd_len = length;
	ifd.ifd_data = data;

	error = ioctl(fd, set ? SIOCSDRVSPEC : SIOCGDRVSPEC, &ifd);
	if (error < 0) {
		saved_errno = errno;
		(void)close(fd);
		errno = saved_errno;
		return (-1);
	}
	(void)close(fd);
	return (error);
}

int
sylve_bridge_get_state(const char *bridge, uint32_t *flags, uint16_t *pvid)
{
	struct ifbrparam parameter;

	if (flags == NULL || pvid == NULL) {
		errno = EINVAL;
		return (-1);
	}
	memset(&parameter, 0, sizeof(parameter));
	if (sylve_bridge_cmd(bridge, BRDGGFLAGS, &parameter,
	    sizeof(parameter), 0) < 0)
		return (-1);
	*flags = parameter.ifbrp_flags;

	memset(&parameter, 0, sizeof(parameter));
	if (sylve_bridge_cmd(bridge, BRDGGDEFPVID, &parameter,
	    sizeof(parameter), 0) < 0)
		return (-1);
	*pvid = parameter.ifbrp_defpvid;
	return (0);
}

int
sylve_bridge_set_filtering(const char *bridge, int enabled)
{
	struct ifbrparam parameter;

	memset(&parameter, 0, sizeof(parameter));
	if (sylve_bridge_cmd(bridge, BRDGGFLAGS, &parameter,
	    sizeof(parameter), 0) < 0)
		return (-1);
	if (enabled)
		parameter.ifbrp_flags |= IFBRF_VLANFILTER;
	else
		parameter.ifbrp_flags &= ~IFBRF_VLANFILTER;
	return (sylve_bridge_cmd(bridge, BRDGSFLAGS, &parameter,
	    sizeof(parameter), 1));
}

int
sylve_bridge_set_default_qinq(const char *bridge, int enabled)
{
	struct ifbrparam parameter;

	memset(&parameter, 0, sizeof(parameter));
	if (sylve_bridge_cmd(bridge, BRDGGFLAGS, &parameter,
	    sizeof(parameter), 0) < 0)
		return (-1);
	if (enabled)
		parameter.ifbrp_flags |= IFBRF_DEFQINQ;
	else
		parameter.ifbrp_flags &= ~IFBRF_DEFQINQ;
	return (sylve_bridge_cmd(bridge, BRDGSFLAGS, &parameter,
	    sizeof(parameter), 1));
}

int
sylve_bridge_set_default_pvid(const char *bridge, uint16_t pvid)
{
	struct ifbrparam parameter;

	memset(&parameter, 0, sizeof(parameter));
	parameter.ifbrp_defpvid = pvid;
	return (sylve_bridge_cmd(bridge, BRDGSDEFPVID, &parameter,
	    sizeof(parameter), 1));
}

int
sylve_bridge_get_member(const char *bridge, const char *member,
    uint32_t *flags, uint16_t *pvid, uint16_t *protocol,
    uint8_t allowed[BRVLAN_SETSIZE])
{
	struct ifbif_vlan_req vlans;
	struct ifbreq request;
	int vlan;

	if (sylve_bridge_validate_name(member) < 0 || flags == NULL ||
	    pvid == NULL || protocol == NULL || allowed == NULL) {
		errno = EINVAL;
		return (-1);
	}

	memset(&request, 0, sizeof(request));
	strlcpy(request.ifbr_ifsname, member, sizeof(request.ifbr_ifsname));
	if (sylve_bridge_cmd(bridge, BRDGGIFFLGS, &request,
	    sizeof(request), 0) < 0)
		return (-1);
	*flags = request.ifbr_ifsflags;
	*pvid = request.ifbr_pvid;
	*protocol = request.ifbr_vlanproto;

	memset(&vlans, 0, sizeof(vlans));
	strlcpy(vlans.bv_ifname, member, sizeof(vlans.bv_ifname));
	if (sylve_bridge_cmd(bridge, BRDGGIFVLANSET, &vlans,
	    sizeof(vlans), 0) < 0)
		return (-1);
	memset(allowed, 0, BRVLAN_SETSIZE * sizeof(*allowed));
	for (vlan = DOT1Q_VID_MIN; vlan <= DOT1Q_VID_MAX; vlan++) {
		if (BRVLAN_TEST(&vlans.bv_set, vlan))
			allowed[vlan] = 1;
	}
	return (0);
}

int
sylve_bridge_add_member(const char *bridge, const char *member)
{
	struct ifbreq request;

	if (sylve_bridge_validate_name(member) < 0)
		return (-1);
	memset(&request, 0, sizeof(request));
	strlcpy(request.ifbr_ifsname, member, sizeof(request.ifbr_ifsname));
	return (sylve_bridge_cmd(bridge, BRDGADD, &request,
	    sizeof(request), 1));
}

int
sylve_bridge_remove_member(const char *bridge, const char *member)
{
	struct ifbreq request;

	if (sylve_bridge_validate_name(member) < 0)
		return (-1);
	memset(&request, 0, sizeof(request));
	strlcpy(request.ifbr_ifsname, member, sizeof(request.ifbr_ifsname));
	return (sylve_bridge_cmd(bridge, BRDGDEL, &request,
	    sizeof(request), 1));
}

int
sylve_bridge_set_member_pvid(const char *bridge, const char *member,
    uint16_t pvid)
{
	struct ifbreq request;

	if (sylve_bridge_validate_name(member) < 0)
		return (-1);
	memset(&request, 0, sizeof(request));
	strlcpy(request.ifbr_ifsname, member, sizeof(request.ifbr_ifsname));
	request.ifbr_pvid = pvid;
	return (sylve_bridge_cmd(bridge, BRDGSIFPVID, &request,
	    sizeof(request), 1));
}

int
sylve_bridge_set_member_vlans(const char *bridge, const char *member,
    const uint16_t *values, size_t count)
{
	struct ifbif_vlan_req request;
	size_t index;

	if (sylve_bridge_validate_name(member) < 0 ||
	    (count != 0 && values == NULL) || count > DOT1Q_VID_MAX) {
		errno = EINVAL;
		return (-1);
	}
	memset(&request, 0, sizeof(request));
	strlcpy(request.bv_ifname, member, sizeof(request.bv_ifname));
	request.bv_op = BRDG_VLAN_OP_SET;
	for (index = 0; index < count; index++) {
		if (values[index] < DOT1Q_VID_MIN ||
		    values[index] > DOT1Q_VID_MAX) {
			errno = EINVAL;
			return (-1);
		}
		BRVLAN_SET(&request.bv_set, values[index]);
	}
	return (sylve_bridge_cmd(bridge, BRDGSIFVLANSET, &request,
	    sizeof(request), 1));
}

int
sylve_bridge_set_member_protocol(const char *bridge, const char *member,
    uint16_t protocol)
{
	struct ifbreq request;

	if (sylve_bridge_validate_name(member) < 0)
		return (-1);
	memset(&request, 0, sizeof(request));
	strlcpy(request.ifbr_ifsname, member, sizeof(request.ifbr_ifsname));
	request.ifbr_vlanproto = protocol;
	return (sylve_bridge_cmd(bridge, BRDGSIFVLANPROTO, &request,
	    sizeof(request), 1));
}

int
sylve_bridge_set_member_qinq(const char *bridge, const char *member,
    int enabled)
{
	struct ifbreq request;

	if (sylve_bridge_validate_name(member) < 0)
		return (-1);
	memset(&request, 0, sizeof(request));
	strlcpy(request.ifbr_ifsname, member, sizeof(request.ifbr_ifsname));
	if (sylve_bridge_cmd(bridge, BRDGGIFFLGS, &request,
	    sizeof(request), 0) < 0)
		return (-1);
	if (enabled)
		request.ifbr_ifsflags |= IFBIF_QINQ;
	else
		request.ifbr_ifsflags &= ~IFBIF_QINQ;
	return (sylve_bridge_cmd(bridge, BRDGSIFFLGS, &request,
	    sizeof(request), 1));
}

int
sylve_bridge_set_member_private(const char *bridge, const char *member,
    int enabled)
{
	struct ifbreq request;

	if (sylve_bridge_validate_name(member) < 0)
		return (-1);
	memset(&request, 0, sizeof(request));
	strlcpy(request.ifbr_ifsname, member, sizeof(request.ifbr_ifsname));
	if (sylve_bridge_cmd(bridge, BRDGGIFFLGS, &request,
	    sizeof(request), 0) < 0)
		return (-1);
	if (enabled)
		request.ifbr_ifsflags |= IFBIF_PRIVATE;
	else
		request.ifbr_ifsflags &= ~IFBIF_PRIVATE;
	return (sylve_bridge_cmd(bridge, BRDGSIFFLGS, &request,
	    sizeof(request), 1));
}

uint32_t
sylve_bridge_vlanfilter_flag(void)
{
	return (IFBRF_VLANFILTER);
}

uint32_t
sylve_bridge_default_qinq_flag(void)
{
	return (IFBRF_DEFQINQ);
}

uint32_t
sylve_bridge_member_qinq_flag(void)
{
	return (IFBIF_QINQ);
}

uint32_t
sylve_bridge_member_private_flag(void)
{
	return (IFBIF_PRIVATE);
}
