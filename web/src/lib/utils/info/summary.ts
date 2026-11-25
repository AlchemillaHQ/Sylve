/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { NodeSummaryHistory } from '$lib/types/info/summary';

function mergeSummaryPoints<T extends { id: number; createdAt: string }>(
	current: T[],
	incoming: T[]
): T[] {
	if (incoming.length === 0) return current;

	const points = new Map<string, T>();
	for (const point of [...current, ...incoming]) {
		const key = point.id > 0 ? `id:${point.id}` : `time:${point.createdAt}`;
		points.set(key, point);
	}

	return [...points.values()].sort((left, right) => {
		const timeDifference = Date.parse(left.createdAt) - Date.parse(right.createdAt);
		return Number.isFinite(timeDifference) && timeDifference !== 0
			? timeDifference
			: left.id - right.id;
	});
}

export function mergeSummaryHistory(
	current: NodeSummaryHistory,
	incoming: NodeSummaryHistory
): NodeSummaryHistory {
	if (incoming.cpu.length === 0 && incoming.ram.length === 0 && incoming.network.length === 0) {
		return current;
	}

	return {
		cpu: mergeSummaryPoints(current.cpu, incoming.cpu),
		ram: mergeSummaryPoints(current.ram, incoming.ram),
		network: mergeSummaryPoints(current.network, incoming.network),
		cursors: incoming.cursors
	};
}
