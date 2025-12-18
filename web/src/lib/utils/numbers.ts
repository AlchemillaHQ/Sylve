/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import humanFormat from 'human-format';

export function floatToNDecimals(value: number | undefined, n: number): number {
	if (!value) return 0.0;
	return parseFloat(value.toFixed(n));
}

export function bytesToHumanReadable(value: number | undefined, highPrecision?: boolean): string {
	if (!value) return '';
	return humanFormat(value, { unit: 'B', decimals: highPrecision ? 10 : undefined });
}

export function parseQuotaToZFSBytes(input: string): string {
	if (!input || input.trim() === '') return '0B';

	try {
		const parsed = humanFormat.parse(input);

		if (parsed === null || isNaN(parsed)) {
			throw new Error('Invalid quota format');
		}

		return `${parsed}B`;
	} catch (e) {
		console.error(`Failed to parse quota "${input}":`, e);
		return '0B';
	}
}

export function generateNumberFromString(str: string): number {
	let hash = 0x811c9dc5;
	for (let i = 0; i < str.length; i++) {
		hash ^= str.charCodeAt(i);
		hash = Math.imul(hash, 0x01000193);
	}
	return hash >>> 0;
}

export function isValidSize(value: string): boolean {
	try {
		const parsed = humanFormat.parse(value);
		return parsed !== null && !isNaN(parsed);
	} catch (e) {
		return false;
	}
}

export function parseFlexibleTimeToSeconds(input: string) {
	if (!input || typeof input !== 'string') return -1;

	const parts = input.split(':').map(Number);
	if (parts.some(isNaN)) return -1;

	while (parts.length < 6) {
		parts.unshift(0);
	}

	const [years, months, days, hours, minutes, seconds] = parts.slice(-6);

	if (
		seconds < 0 ||
		seconds >= 60 ||
		minutes < 0 ||
		minutes >= 60 ||
		hours < 0 ||
		hours >= 24 ||
		days < 0 ||
		days >= 31 ||
		months < 0 ||
		months >= 12
	) {
		return -1;
	}

	const totalDays = years * 365 + months * 30 + days;
	return totalDays * 86400 + hours * 3600 + minutes * 60 + seconds;
}

export function isValidMTU(mtu: number): boolean {
	return mtu >= 68 && mtu <= 65535;
}

export function isValidVLAN(vlan: number): boolean {
	return vlan >= 0 && vlan <= 4095;
}
