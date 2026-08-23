/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { browser } from '$app/environment';
import { storage } from '$lib';
import { connection } from '$lib/stores/api.svelte';
import type { AvailableService } from '$lib/types/system/settings';
import { setMode } from 'mode-watcher';

export const isDemoMode = __SYLVE_DEMO__;

const services: AvailableService[] = [
	'virtualization',
	'jails',
	'dhcp-server',
	'samba-server',
	'wol-server',
	'firewall',
	'wireguard',
	'iscsi',
	'mdns'
];

function createDemoToken(): string {
	const header = btoa(JSON.stringify({ alg: 'none', typ: 'JWT' }));
	const payload = btoa(
		JSON.stringify({
			exp: 4102444800,
			jti: 'sylve-public-demo',
			custom_claims: {
				userId: 1,
				username: 'admin',
				authType: 'demo'
			}
		})
	);

	return `${header}.${payload}.demo`;
}

export function initializeDemoRuntime(): void {
	if (!browser || !isDemoMode) return;

	storage.token = createDemoToken();
	storage.oldToken = null;
	storage.language = 'en';
	storage.localHostname = 'leto';
	storage.hostname = 'leto';
	storage.nodeId = 'node-leto';
	storage.enabledServices = services;
	storage.enabledServicesByHostname = {
		leto: services,
		paul: services,
		alia: services
	};
	storage.showReplication = false;
	storage.openAbout = false;
	storage.openCommands = false;
	connection.sseConnected = true;

	if (!localStorage.getItem('mode-watcher-mode')) {
		setMode('dark');
	}
}
