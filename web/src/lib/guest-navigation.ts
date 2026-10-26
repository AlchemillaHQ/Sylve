/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { SimpleJail } from '$lib/types/jail/jail';

type GuestType = 'vm' | 'jail';

const guestSections = [
	'summary',
	'console',
	'storage',
	'hardware',
	'network',
	'packages',
	'snapshots',
	'backups',
	'options'
] as const;

export type GuestSection = (typeof guestSections)[number];

export function getGuestSections(
	guestType: GuestType,
	jailType?: SimpleJail['type']
): GuestSection[] {
	return guestSections.filter((section) => {
		if (section === 'storage') return guestType === 'vm';
		if (section === 'packages') return guestType === 'jail' && jailType === 'freebsd';
		return true;
	});
}

export function getGuestNavigationHref(
	pathname: string,
	targetHref: string,
	guestType: GuestType,
	jailType?: SimpleJail['type']
): string {
	const match = pathname.match(/^\/[^/]+\/(?:jail|vm)\/\d+\/([^/]+)\/?$/);
	const section =
		getGuestSections(guestType, jailType).find((section) => section === match?.[1]) ?? 'summary';
	return targetHref.replace(/\/summary$/, `/${section}`);
}
