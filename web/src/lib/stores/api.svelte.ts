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
	auditLogHostname: null as string | null,
	clusterDetails: false,
	notifications: false,
	datacenterNodesPulse: 0,
	datacenterDetailsPulse: 0
});

export const connection = $state({
	sseConnected: null as boolean | null,
	plannedRestart: null as null | {
		disconnectObserved: boolean;
	}
});

export function planClusterLeaveRestart(): boolean {
	const ownsRestartPlan = connection.plannedRestart === null;
	connection.plannedRestart ??= { disconnectObserved: connection.sseConnected === false };
	return ownsRestartPlan;
}

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
