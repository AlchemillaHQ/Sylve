/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { AvailableService } from '$lib/types/system/settings';

interface InitializationServiceDefinition {
	id: AvailableService;
	label: string;
	defaultEnabled: boolean;
}

interface InitializationServiceGroupDefinition {
	label: string;
	services: readonly InitializationServiceDefinition[];
}

export const INITIALIZATION_SERVICE_GROUPS = [
	{
		label: 'Compute',
		services: [
			{ id: 'jails', label: 'Jails', defaultEnabled: true },
			{ id: 'virtualization', label: 'Virtualization', defaultEnabled: true }
		]
	},
	{
		label: 'Storage & Sharing',
		services: [
			{ id: 'iscsi', label: 'iSCSI', defaultEnabled: true },
			{ id: 'samba-server', label: 'Samba Server', defaultEnabled: false }
		]
	},
	{
		label: 'Network Services',
		services: [
			{ id: 'dhcp-server', label: 'DHCP Server', defaultEnabled: true },
			{ id: 'mdns', label: 'mDNS Discovery', defaultEnabled: true },
			{ id: 'wol-server', label: 'WoL Server', defaultEnabled: true }
		]
	},
	{
		label: 'Connectivity',
		services: [
			{ id: 'firewall', label: 'Firewall', defaultEnabled: true },
			{ id: 'wireguard', label: 'WireGuard', defaultEnabled: true }
		]
	}
] as const satisfies readonly InitializationServiceGroupDefinition[];

export const INITIALIZATION_SERVICES =
	INITIALIZATION_SERVICE_GROUPS.flatMap<InitializationServiceDefinition>((group) => group.services);
