/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

export const reload = $state({
    leftPanel: false,
    auditLog: false,
    clusterDetails: false
});

export const jailPowerSignal = $state({
    token: 0,
    ctId: 0,
    action: '' as '' | 'start' | 'stop'
});

export const vmPowerSignal = $state({
    token: 0,
    rid: 0,
    action: '' as '' | 'start' | 'stop' | 'shutdown' | 'reboot'
});
