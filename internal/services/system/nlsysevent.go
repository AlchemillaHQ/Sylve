// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package system

/*
#include <sys/socket.h>
#include <netlink/netlink_snl.h>
#include <netlink/netlink_snl_generic.h>
#include <netlink/netlink_sysevent.h>
#include <string.h>
#include <stdbool.h>

extern void onZFSEvent(char* system, char* subsystem, char* type, char* data);

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

static int start_netlink_watcher() {
    struct snl_state state[1];
    if (!snl_init(state, NETLINK_GENERIC)) {
        return -1;
    }

    struct group grp = get_group_id(state, "nlsysevent", "ZFS");
    if (grp.error || !grp.found) return -2;

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
*/
import "C"
import (
	"context"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/logger"
)

var zfsEventsChan = make(chan *models.NetlinkEvent, 1024)

//export onZFSEvent
func onZFSEvent(cSystem, cSubsystem, cType, cData *C.char) {
	system := C.GoString(cSystem)
	subsystem := C.GoString(cSubsystem)
	evType := C.GoString(cType)
	data := C.GoString(cData)

	ev := &models.NetlinkEvent{
		System:    system,
		Subsystem: subsystem,
		Type:      evType,
		Attrs:     make(map[string]string),
		Raw:       "!system=" + system + " subsystem=" + subsystem + " type=" + evType + " | " + data,
	}

	logger.L.Debug().Str("system", system).
		Str("subsystem", subsystem).
		Str("type", evType).
		Str("data", data).
		Msg("Received ZFS event")

	for _, field := range strings.Fields(data) {
		kv := strings.SplitN(field, "=", 2)
		if len(kv) != 2 {
			continue
		}

		key := kv[0]
		val := strings.Trim(kv[1], "\"")
		ev.Attrs[key] = val
	}

	select {
	case zfsEventsChan <- ev:
	default:
		// logger.L.Warn().Msg("ZFS event dropped, consumer channel full")
	}
}

func (s *Service) StartNetlinkWatcher(ctx context.Context) {
	go func() {
		logger.L.Info().Msg("Starting FreeBSD Netlink ZFS watcher...")
		res := C.start_netlink_watcher()
		if res != 0 {
			logger.L.Error().Int("code", int(res)).Msg("Netlink watcher exited with error")
		}
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				logger.L.Debug().Msg("Stopped Netlink consumer loop")
				return
			case ev := <-zfsEventsChan:
				if !shouldPersistNetlinkEvent(ev) {
					continue
				}

				if err := s.DB.Create(ev).Error; err != nil {
					logger.L.Error().Err(err).Msg("Failed to insert Netlink ZFS event")
				}
				s.emitPoolStateNotification(ctx, ev)
			}
		}
	}()
}

func (s *Service) NetlinkEventsCleaner(ctx context.Context) {
	cleanup := func() {
		cutoff := time.Now().Add(-24 * time.Hour)

		res := s.DB.Unscoped().
			Where("created_at < ?", cutoff).
			Delete(&models.NetlinkEvent{})

		if res.Error != nil {
			logger.L.Error().
				Err(res.Error).
				Msg("netlink cleanup failed")
		} else if res.RowsAffected > 0 {
			logger.L.Debug().Int64("count", res.RowsAffected).Msg("Pruned old ZFS events")
		}
	}

	go func() {
		cleanup()

		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				logger.L.Debug().Msg("Stopped Netlink events cleaner")
				return
			case <-ticker.C:
				cleanup()
			}
		}
	}()
}
