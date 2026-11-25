/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { format } from 'date-fns';

const day = 24 * 60 * 60 * 1000;

export function axisTime(date: number, span: number): string {
	return format(date, span >= day ? 'MMM d, HH:mm' : 'HH:mm');
}

export function readoutTime(date: number): string {
	return format(date, 'MMM d, HH:mm:ss');
}

export function niceCeil(value: number): number {
	if (value <= 0) return 1;
	const magnitude = 10 ** Math.floor(Math.log10(value));
	return [1, 2, 5, 10].find((step) => value <= step * magnitude)! * magnitude;
}

export function runs<T extends { date: number }>(points: T[]): T[][] {
	const steps = points.slice(1).map((point, index) => point.date - points[index].date);
	const result: T[][] = [];
	let run: T[] = [];

	points.forEach((point, index) => {
		const nearby = [
			...steps.slice(Math.max(0, index - 4), Math.max(0, index - 1)),
			...steps.slice(index, index + 3)
		].sort((left, right) => left - right);

		if (nearby.length > 0 && steps[index - 1] > 3 * nearby[Math.floor(nearby.length / 2)]) {
			result.push(run);
			run = [];
		}
		run.push(point);
	});

	result.push(run);
	return result.filter((entry) => entry.length > 0);
}

export function processorName(name: string): string {
	return name
		.replace(/\((R|TM)\)/gi, '')
		.replace(/@.*$/, '')
		.replace(/\b(\d+-Core\s+)?Processor\b|\bCPU\b/gi, '')
		.replace(/\s+/g, ' ')
		.trim();
}

export function systemName(os: string): string {
	return os.split(/\s+/).slice(0, 2).join(' ');
}

export function hardwareName(vendor = '', product = ''): string {
	return product.toLowerCase().startsWith(vendor.toLowerCase())
		? product
		: `${vendor} ${product}`.trim();
}

export function linkSpeed(media: string): string {
	const match = /^(\d+(?:\.\d+)?)(G?)base/i.exec(media);
	if (!match) return '';
	const megabits = Number(match[1]) * (match[2] ? 1000 : 1);
	return megabits >= 1000 ? `${megabits / 1000} Gbps` : `${megabits} Mbps`;
}
