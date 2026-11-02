/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { ContainerPort, UsedPort } from '$lib/types/container/container';

export function formatPort(port: ContainerPort): string {
	return `${port.hostIp ? `${port.hostIp}:` : ''}${port.hostPort} → ${port.containerPort}/${port.protocol}`;
}

export function portConflict(used: UsedPort[], protocol: string, port: number): string {
	const user = used.find((item) => item.protocol === protocol && item.port === Number(port));
	if (!user) return '';
	return `Port ${port}/${protocol} is used by ${user.by}${user.address ? ` on ${user.address}` : ''}.`;
}

export function nextFreePort(
	used: UsedPort[],
	protocol: string,
	port: number,
	taken: number[] = []
): number {
	let next = Number(port) + 1;
	while (next < 65535 && (portConflict(used, protocol, next) || taken.includes(next))) next++;
	return next;
}
