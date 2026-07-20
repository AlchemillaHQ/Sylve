// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

#include <sys/param.h>

#if __FreeBSD_version >= 1500000

#include <errno.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <netlink/netlink_snl.h>
#include <netlink/netlink_snl_generic.h>
#include <netlink/netlink_sysevent.h>
#include <string.h>
#include <stdbool.h>
#include <stdatomic.h>

#include "_cgo_export.h"

static atomic_bool watcher_stop_requested = ATOMIC_VAR_INIT(false);

void prepare_ifnet_watcher(void) {
    atomic_store(&watcher_stop_requested, false);
}

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
    if (atomic_load(&watcher_stop_requested)) {
        return 0;
    }

    struct snl_state state[1];
    if (!snl_init(state, NETLINK_GENERIC)) {
        return -1;
    }

    int result = 0;
    const struct timeval timeout = {
        .tv_sec = 0,
        .tv_usec = 500000,
    };
    if (setsockopt(state->fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, sizeof(timeout)) == -1) {
        result = -2;
        goto out;
    }

    if (atomic_load(&watcher_stop_requested)) {
        goto out;
    }

    struct group grp = get_group_id(state, "nlsysevent", "IFNET");
    if (grp.error || !grp.found) {
        result = atomic_load(&watcher_stop_requested) ? 0 : -3;
        goto out;
    }

    uint32_t group_id = grp.group_id;
    if (setsockopt(state->fd, SOL_NETLINK, NETLINK_ADD_MEMBERSHIP, &group_id, sizeof(group_id))) {
        result = atomic_load(&watcher_stop_requested) ? 0 : -4;
        goto out;
    }

    while (!atomic_load(&watcher_stop_requested)) {
        errno = 0;
        struct nlmsghdr *hdr = snl_read_message(state);
        int saved_errno = errno;
        if (!hdr) {
            if (atomic_load(&watcher_stop_requested)) {
                break;
            }
            if (saved_errno == EAGAIN || saved_errno == EWOULDBLOCK) {
                continue;
            }
            result = -5;
            break;
        }

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

out:
    snl_free(state);
    return result;
}

void stop_ifnet_watcher(void) {
    atomic_store(&watcher_stop_requested, true);
}

#else /* FreeBSD < 15 */

int start_ifnet_watcher(void) {
    return -99;
}

void prepare_ifnet_watcher(void) { }

void stop_ifnet_watcher(void) { }

#endif /* __FreeBSD_version >= 1500000 */
