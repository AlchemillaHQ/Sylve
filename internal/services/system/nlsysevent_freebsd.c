// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

#include <sys/param.h>

#if __FreeBSD_version >= 1500000

#include <sys/socket.h>
#include <netlink/netlink_snl.h>
#include <netlink/netlink_snl_generic.h>
#include <netlink/netlink_sysevent.h>
#include <string.h>
#include <stdbool.h>

#include "_cgo_export.h"

struct group {
    bool     found;
    uint16_t family_id;
    uint32_t group_id;
};

static inline struct group get_group_id(struct snl_state *state, const char *family_name, const char *group_name) {
    uint16_t family_id = 0;
    uint32_t group_id = snl_get_genl_mcast_group(state, family_name, group_name, &family_id);

    return (struct group){
        .found = group_id != 0,
        .family_id = family_id,
        .group_id = group_id
    };
}

int start_netlink_watcher(void) {
    struct snl_state state[1];
    if (!snl_init(state, NETLINK_GENERIC)) {
        return -1;
    }

    struct group grp = get_group_id(state, "nlsysevent", "ZFS");
    if (!grp.found) return -2;

    uint32_t group_id = grp.group_id;
    if (setsockopt(state->fd, SOL_NETLINK, NETLINK_ADD_MEMBERSHIP, &group_id, sizeof(group_id))) {
        return -3;
    }

    struct nlmsghdr *hdr = snl_read_message(state);
    if (!hdr || hdr->nlmsg_type != NLMSG_ERROR) return -4;

    while (1) {
        hdr = snl_read_message(state);
        if (!hdr) continue;

        if (hdr->nlmsg_type == NLMSG_ERROR) continue;
        if (hdr->nlmsg_type != grp.family_id) continue;

        struct genlmsghdr *gen = (struct genlmsghdr *)NLMSG_DATA(hdr);
        if (gen->cmd == NLSE_CMD_NEWEVENT) {
            struct nlattr *attr;
            struct nlattr *start = (struct nlattr *)(gen + 1);
            size_t len = (size_t)(((char *)hdr) + hdr->nlmsg_len - (char *)start);
            struct nlattr *attrs[__NLSE_ATTR_MAX] = { 0 };

            NLA_FOREACH(attr, start, len) {
                if (attr->nla_type < __NLSE_ATTR_MAX) {
                    attrs[attr->nla_type] = attr;
                }
            }

            char *system    = attrs[NLSE_ATTR_SYSTEM]    ? (char *)NLA_DATA(attrs[NLSE_ATTR_SYSTEM])    : "";
            char *subsystem = attrs[NLSE_ATTR_SUBSYSTEM] ? (char *)NLA_DATA(attrs[NLSE_ATTR_SUBSYSTEM]) : "";
            char *type      = attrs[NLSE_ATTR_TYPE]      ? (char *)NLA_DATA(attrs[NLSE_ATTR_TYPE])      : "";
            char *data      = attrs[NLSE_ATTR_DATA]      ? (char *)NLA_DATA(attrs[NLSE_ATTR_DATA])      : "";

            onZFSEvent(system, subsystem, type, data);
        }
    }

    return 0;
}

#else /* FreeBSD < 15 */

int start_netlink_watcher(void) {
    return -99;
}

#endif /* __FreeBSD_version >= 1500000 */
