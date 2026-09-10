/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { VLANPortPolicy } from '$lib/types/network/switch';

export type VLANPolicyMode = '' | 'access' | 'trunk';

export function isValidVLANID(value: number): boolean {
	return Number.isInteger(value) && value >= 1 && value <= 4094;
}

export function parseOptionalVLAN(value: string | number): number | null {
	const raw = String(value ?? '').trim();
	if (raw === '') return null;
	const vlan = Number(raw);
	return isValidVLANID(vlan) ? vlan : Number.NaN;
}

export function parseTaggedVLANs(value: string): number[] | null {
	const raw = value.trim();
	if (!raw) return [];

	const vlans = new Set<number>();
	for (const entry of raw.split(',')) {
		const item = entry.trim();
		if (!item) return null;
		const match = /^(\d+)(?:\s*-\s*(\d+))?$/.exec(item);
		if (!match) return null;
		const first = Number(match[1]);
		const last = match[2] === undefined ? first : Number(match[2]);
		if (!isValidVLANID(first) || !isValidVLANID(last) || last < first) return null;
		for (let vlan = first; vlan <= last; vlan++) vlans.add(vlan);
	}

	return [...vlans].sort((left, right) => left - right);
}

export function parseVLANPolicyDraft(
	mode: VLANPolicyMode,
	untaggedInput: string | number,
	taggedInput: string
): VLANPortPolicy | null {
	const untaggedVlan = parseOptionalVLAN(untaggedInput);
	if (Number.isNaN(untaggedVlan)) return null;

	if (mode === 'access') {
		if (untaggedVlan === null) return null;
		return { mode, untaggedVlan, taggedVlans: [] };
	}
	if (mode !== 'trunk') return null;

	const taggedVlans = parseTaggedVLANs(taggedInput);
	if (!taggedVlans || taggedVlans.length === 0) return null;
	if (untaggedVlan !== null && taggedVlans.includes(untaggedVlan)) return null;
	return {
		mode,
		...(untaggedVlan === null ? {} : { untaggedVlan }),
		taggedVlans
	};
}
