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

type GuestType = 'vm' | 'jail' | 'container';

const guestSections = [
	'summary',
	'console',
	'storage',
	'hardware',
	'network',
	'packages',
	'snapshots',
	'backups',
	'options',
	'permissions'
] as const;

export type GuestSection = (typeof guestSections)[number];

const containerSections: GuestSection[] = [
	'summary',
	'console',
	'hardware',
	'network',
	'storage',
	'options',
	'permissions'
];

export function getGuestSections(
	guestType: GuestType,
	jailType?: SimpleJail['type']
): GuestSection[] {
	if (guestType === 'container') return containerSections;
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
	const match = pathname.match(/^\/[^/]+\/(?:jail|vm|container)\/\d+\/([^/]+)\/?$/);
	const section =
		getGuestSections(guestType, jailType).find((section) => section === match?.[1]) ?? 'summary';
	return targetHref.replace(/\/summary$/, `/${section}`);
}
