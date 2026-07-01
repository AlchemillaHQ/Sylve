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
#include <unistd.h>

#include "_cgo_export.h"

static int watcher_fd = -1;

struct group {
    bool     found;
    uint8_t  error;
    uint16_t family_id;
    uint32_t group_id;
};

static inline struct group get_group_id(struct snl_state *state, const char *family_name, const char *group_name) {
    struct _getfamily_attrs attrs;
    if (!_snl_get_genl_family_info(state, family_name, &attrs)) {
        return (struct group){ .error = 1 };
    }

    for (size_t i = 0; i < attrs.mcast_groups.num_groups; i++) {
        if (!strcmp(group_name, attrs.mcast_groups.groups[i]->mcast_grp_name)) {
            return (struct group){
                .found = true,
                .family_id = attrs.family_id,
                .group_id = attrs.mcast_groups.groups[i]->mcast_grp_id
            };
        }
    }
    return (struct group){ .found = false };
}

int start_ifnet_watcher(void) {
    struct snl_state state[1];
    if (!snl_init(state, NETLINK_GENERIC)) {
        return -1;
    }

    watcher_fd = state->fd;

    struct group grp = get_group_id(state, "nlsysevent", "IFNET");
    if (grp.error || !grp.found) {
        close(watcher_fd);
        watcher_fd = -1;
        return -2;
    }

    uint32_t group_id = grp.group_id;
    if (setsockopt(state->fd, SOL_NETLINK, NETLINK_ADD_MEMBERSHIP, &group_id, sizeof(group_id))) {
        close(watcher_fd);
        watcher_fd = -1;
        return -3;
    }

    struct nlmsghdr *hdr = snl_read_message(state);
    if (!hdr || hdr->nlmsg_type != NLMSG_ERROR) {
        close(watcher_fd);
        watcher_fd = -1;
        return -4;
    }

    while (1) {
        hdr = snl_read_message(state);
        if (!hdr) break;

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

            onIFNETEvent(system, subsystem, type, data);
        }
    }

    watcher_fd = -1;
    return 0;
}

void stop_ifnet_watcher(void) {
    if (watcher_fd >= 0) {
        close(watcher_fd);
        watcher_fd = -1;
    }
}

#else /* FreeBSD < 15 */

int start_ifnet_watcher(void) {
    return -99;
}

void stop_ifnet_watcher(void) { }

#endif /* __FreeBSD_version >= 1500000 */
