// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

#ifndef SYLVE_BRIDGEVLAN_BACKEND_FREEBSD_H
#define SYLVE_BRIDGEVLAN_BACKEND_FREEBSD_H

#include <stddef.h>
#include <stdint.h>

#include <net/if_bridgevar.h>

int sylve_bridge_get_state(const char *, uint32_t *, uint16_t *);
int sylve_bridge_set_filtering(const char *, int);
int sylve_bridge_set_default_qinq(const char *, int);
int sylve_bridge_set_default_pvid(const char *, uint16_t);
int sylve_bridge_get_member(const char *, const char *, uint32_t *,
    uint16_t *, uint16_t *, uint8_t [BRVLAN_SETSIZE]);
int sylve_bridge_add_member(const char *, const char *);
int sylve_bridge_remove_member(const char *, const char *);
int sylve_bridge_set_member_pvid(const char *, const char *, uint16_t);
int sylve_bridge_set_member_vlans(const char *, const char *,
    const uint16_t *, size_t);
int sylve_bridge_set_member_protocol(const char *, const char *, uint16_t);
int sylve_bridge_set_member_qinq(const char *, const char *, int);
int sylve_bridge_set_member_private(const char *, const char *, int);

uint32_t sylve_bridge_vlanfilter_flag(void);
uint32_t sylve_bridge_default_qinq_flag(void);
uint32_t sylve_bridge_member_qinq_flag(void);
uint32_t sylve_bridge_member_private_flag(void);

#endif
