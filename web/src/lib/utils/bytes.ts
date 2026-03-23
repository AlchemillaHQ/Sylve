/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

const BINARY_UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'] as const;
const BINARY_BASE = 1024;
const MAX_SAFE_BYTES = Number.MAX_SAFE_INTEGER;

const UNIT_TO_MULTIPLIER: Record<string, number> = {
	'': 1,
	b: 1,
	byte: 1,
	bytes: 1,

	k: BINARY_BASE,
	kb: BINARY_BASE,
	ki: BINARY_BASE,
	kib: BINARY_BASE,

	m: BINARY_BASE ** 2,
	mb: BINARY_BASE ** 2,
	mi: BINARY_BASE ** 2,
	mib: BINARY_BASE ** 2,

	g: BINARY_BASE ** 3,
	gb: BINARY_BASE ** 3,
	gi: BINARY_BASE ** 3,
	gib: BINARY_BASE ** 3,

	t: BINARY_BASE ** 4,
	tb: BINARY_BASE ** 4,
	ti: BINARY_BASE ** 4,
	tib: BINARY_BASE ** 4,

	p: BINARY_BASE ** 5,
	pb: BINARY_BASE ** 5,
	pi: BINARY_BASE ** 5,
	pib: BINARY_BASE ** 5
};

const SIZE_INPUT_PATTERN =
	/^([+-]?(?:\d[\d_,\s]*(?:\.\d[\d_,\s]*)?|\.\d[\d_,\s]*)(?:e[+-]?\d+)?)\s*([a-zA-Z]*)$/;

export interface ByteFormatOptions {
	maxDecimals?: number;
	minDecimals?: number;
	fallback?: string;
}

function cleanNumberString(input: string): string {
	return input.replace(/[_\s,]+/g, '');
}

function toSafeRoundedBytes(value: number): number | null {
	if (!Number.isFinite(value) || value < 0) {
		return null;
	}

	const rounded = Math.round(value);
	if (!Number.isFinite(rounded) || rounded < 0 || rounded > MAX_SAFE_BYTES) {
		return null;
	}

	return rounded;
}

function trimTrailingZeros(value: string): string {
	return value.replace(/\.0+$/, '').replace(/(\.\d*?)0+$/, '$1');
}

function normalizeUnit(unit: string): string {
	return unit.toLowerCase();
}

function normalizeNumberishToBytes(input: unknown): number | null {
	if (typeof input === 'number') {
		if (!Number.isFinite(input) || input < 0) {
			return null;
		}

		return input;
	}

	if (typeof input === 'string') {
		const trimmed = input.trim();
		if (!trimmed) {
			return null;
		}

		const numeric = Number(trimmed);
		if (Number.isFinite(numeric) && numeric >= 0) {
			return numeric;
		}

		return parseSizeInputToBytes(trimmed);
	}

	return null;
}

export function parseSizeInputToBytes(input: unknown): number | null {
	if (typeof input === 'number') {
		return toSafeRoundedBytes(input);
	}

	if (typeof input !== 'string') {
		return null;
	}

	const raw = input.trim();
	if (!raw) {
		return null;
	}

	const match = raw.match(SIZE_INPUT_PATTERN);
	if (!match) {
		return null;
	}

	const numericPart = cleanNumberString(match[1] ?? '');
	if (!numericPart) {
		return null;
	}

	const value = Number(numericPart);
	if (!Number.isFinite(value) || value < 0) {
		return null;
	}

	const normalizedUnit = normalizeUnit(match[2] ?? '');
	const multiplier = UNIT_TO_MULTIPLIER[normalizedUnit];
	if (!multiplier) {
		return null;
	}

	return toSafeRoundedBytes(value * multiplier);
}

export function formatBytesBinary(input: unknown, options: ByteFormatOptions = {}): string {
	const { maxDecimals = 2, minDecimals = 0, fallback = '0 B' } = options;
	const bytes = normalizeNumberishToBytes(input);

	if (bytes === null) {
		return fallback;
	}

	if (bytes === 0) {
		return '0 B';
	}

	let unitIndex = 0;
	let scaled = bytes;

	while (scaled >= BINARY_BASE && unitIndex < BINARY_UNITS.length - 1) {
		scaled /= BINARY_BASE;
		unitIndex += 1;
	}

	let rounded: number;
	if (unitIndex === 0) {
		rounded = Math.round(scaled);
	} else {
		const factor = 10 ** Math.max(0, maxDecimals);
		rounded = Math.round(scaled * factor) / factor;
	}

	if (rounded >= BINARY_BASE && unitIndex < BINARY_UNITS.length - 1) {
		rounded /= BINARY_BASE;
		unitIndex += 1;
	}

	const decimalsForFixed =
		unitIndex === 0 ? 0 : Math.max(Math.min(Math.max(maxDecimals, 0), 12), minDecimals);
	const fixed = rounded.toFixed(decimalsForFixed);
	const formatted = unitIndex === 0 ? fixed : trimTrailingZeros(fixed);
	const minFixed =
		minDecimals > 0 && unitIndex !== 0 ? Number(formatted).toFixed(Math.min(minDecimals, 12)) : formatted;

	return `${minFixed} ${BINARY_UNITS[unitIndex]}`;
}

export function formatBytesPerSecondBinary(
	bytesPerSecond: unknown,
	options: ByteFormatOptions = {}
): string {
	const fallback = options.fallback ?? '0 B/s';
	const value = formatBytesBinary(bytesPerSecond, { ...options, fallback: '0 B' });

	if (value === '0 B') {
		return fallback;
	}

	return `${value}/s`;
}

export function normalizeSizeInputExact(input: unknown): string | null {
	const bytes = parseSizeInputToBytes(input);
	if (bytes === null) {
		return null;
	}

	return `${bytes} B`;
}

export function toZfsBytesString(bytes: number): string {
	const parsed = parseSizeInputToBytes(bytes);
	if (parsed === null) {
		return '0B';
	}

	return `${parsed}B`;
}
