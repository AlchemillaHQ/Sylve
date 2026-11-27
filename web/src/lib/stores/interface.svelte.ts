/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { browser } from '$app/environment';
import { page } from '$app/state';
import { storage } from '$lib';
import { useSafeGoto } from '$lib/hooks/navigation.svelte';
import { sleep } from '$lib/utils';

const mobile = browser && window.matchMedia('(max-width: 767px), (pointer: coarse)').matches;

export const zenRoutes = [
	'/',
	'/[node]',
	'/[node]/summary',
	'/[node]/notes',
	'/[node]/terminal',
	'/[node]/storage/disks',
	'/[node]/storage/zfs/dashboard',
	'/[node]/storage/zfs/pools',
	'/[node]/storage/zfs/datasets/fs',
	'/[node]/storage/zfs/datasets/volumes',
	'/[node]/storage/zfs/datasets/snapshots',
	'/[node]/storage/explorer',
	'/[node]/storage/samba/shares',
	'/[node]/storage/samba/settings',
	'/[node]/storage/samba/audit-logs',
	'/[node]/storage/nfs/shares',
	'/[node]/storage/nfs/settings',
	'/datacenter/summary'
];

export const ui = {
	get zen() {
		return (storage.interface ?? (mobile ? 'zen' : 'classic')) === 'zen';
	}
};

export const alerts = $state({ count: 0, open: false });
export const entering = $state({ zen: false });

export function homePath(): string {
	return ui.zen ? `/${storage.localHostname || storage.hostname}/summary` : '/datacenter/summary';
}

export async function setInterface(value: 'zen' | 'classic') {
	if (value === 'classic') {
		storage.interface = value;
		return;
	}

	entering.zen = true;
	await sleep(350);
	storage.interface = value;
	if (page.route.id && !zenRoutes.includes(page.route.id)) await useSafeGoto(homePath());
	await sleep(1400);
	entering.zen = false;
}
