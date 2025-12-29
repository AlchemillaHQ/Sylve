/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

export function sampleByInterval(
	data: Array<{ date: Date; value: number }>,
	intervalMin: number
): Array<{ date: Date; value: number }> {
	if (!data.length || intervalMin <= 0) return [];

	const ms = intervalMin * 60 * 1000;
	const sorted = data.slice().sort((a, b) => a.date.getTime() - b.date.getTime());
	const buckets = new Map();

	for (const pt of sorted) {
		const bucketKey = Math.floor(pt.date.getTime() / ms) * ms;
		if (!buckets.has(bucketKey)) {
			buckets.set(bucketKey, pt);
		}
	}

	return Array.from(buckets.values()).sort((a, b) => a.date - b.date);
}

export const switchColor = (color: string, alpha: number = 1): string => {
	const base = (val: string) => val.replace(')', ` / ${alpha})`);
	switch (color) {
		case 'chart-1':
			return base('oklch(0.646 0.222 41.116)');
		case 'chart-2':
			return base('oklch(0.6 0.118 184.704)');
		case 'chart-3':
			return base('oklch(0.398 0.07 227.392)');
		case 'chart-4':
			return base('oklch(0.828 0.189 84.429)');
		case 'chart-5':
			return base('oklch(0.769 0.188 70.08)');
		default:
			return base('oklch(0.646 0.222 41.116)');
	}
};
