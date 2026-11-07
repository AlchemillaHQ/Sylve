/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { InitializeHostState } from '$lib/types/basic';
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
			{ id: 'virtualization', label: 'Virtualization', defaultEnabled: true },
			{ id: 'containers', label: 'Containers', defaultEnabled: false }
		]
	},
	{
		label: 'Storage & Sharing',
		services: [
			{ id: 'iscsi', label: 'iSCSI', defaultEnabled: true },
			{ id: 'samba-server', label: 'Samba Server', defaultEnabled: false },
			{ id: 'nfs', label: 'NFS', defaultEnabled: false }
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

// Lines use backticks around paths and commands.
export interface InitializationHostChange {
	label: string;
	lines: string[];
	warning?: boolean;
	link?: { label: string; href: string };
}

const INITIALIZATION_SERVICE_CHANGES: Partial<Record<AvailableService, string[]>> = {
	jails: ['Adds `/usr/local/etc/newsyslog.conf.d/sylve.conf` to rotate jail logs.'],
	virtualization: ['Starts `libvirtd`.'],
	containers: ['Mounts fdescfs on `/dev/fd` and loads the Linux compatibility modules.'],
	iscsi: [
		'Copies existing `/etc/iscsi.conf` and `/etc/ctl.conf` to `.pre-sylve` files, then manages both.',
		'Starts `iscsid` and `ctld`.'
	],
	'samba-server': [
		'Copies an existing `/usr/local/etc/smb4.conf` to `smb4.conf.pre-sylve`, then manages it.',
		'Adds syslog and newsyslog files for audit logs and starts `samba_server`.'
	],
	nfs: [
		'Copies an existing `/etc/exports` to `/etc/exports.pre-sylve`, then manages it.',
		'Runs `nfsd` and `mountd`.'
	],
	wireguard: ['Loads the `if_wg` kernel module.']
};

export function pfInUse(pf: InitializeHostState['pf']): boolean {
	return pf.conf === 'unmanaged' || (pf.running && pf.conf !== 'managed');
}

function plural(count: number, word: string): string {
	return `${count} ${word}${count === 1 ? '' : 's'}`;
}

function firewallChange(label: string, pf?: InitializeHostState['pf']): InitializationHostChange {
	if (!pf || !pfInUse(pf)) {
		return {
			label,
			lines: [
				'Writes `/etc/pf.conf` and `/etc/pf.sylve/`, sets `pf_enable="NO"` in `/etc/rc.conf`, and turns PF on with rules that allow all traffic.'
			]
		};
	}

	const loaded = `PF is running with ${plural(pf.rules, 'rule')}${pf.natRules ? ` and ${plural(pf.natRules, 'NAT rule')}` : ''}`;
	const lines = [
		pf.conf === 'unmanaged'
			? pf.running
				? `${loaded} from a \`/etc/pf.conf\` that Sylve did not write.`
				: '`/etc/pf.conf` was not written by Sylve.'
			: `${loaded} that Sylve did not load.`,
		'On its next start, Sylve replaces `/etc/pf.conf` and reloads PF with its own rules, which allow all traffic. Your rules, including NAT, are not carried over.'
	];
	if (pf.conf === 'unmanaged') {
		lines.push(
			pf.backupExists
				? '`/etc/pf.conf.pre-sylve` already exists, so the current file is not backed up. Copy it somewhere safe first.'
				: 'The current file is copied to `/etc/pf.conf.pre-sylve`.'
		);
	}
	lines.push(
		pf.enabledInRcConf
			? '`pf_enable` in `/etc/rc.conf` is set to "NO", so PF starts with Sylve instead of at boot.'
			: 'Sets `pf_enable="NO"` in `/etc/rc.conf`; Sylve turns PF on when it starts.',
		'To keep your setup as it is, go back and untick Firewall.'
	);

	return {
		label,
		lines,
		warning: true,
		link: {
			label: 'Adding your own PF rules to Sylve',
			href: 'https://sylve.io/guides/node/network/firewall/advanced/'
		}
	};
}

export function initializationHostChanges(
	services: AvailableService[],
	hostState: InitializeHostState | null
): InitializationHostChange[] {
	const changes: InitializationHostChange[] = [];

	for (const { id, label } of INITIALIZATION_SERVICES) {
		if (!services.includes(id)) continue;

		if (id === 'firewall') {
			changes.push(firewallChange(label, hostState?.pf));
		} else if (id === 'dhcp-server') {
			changes.push(
				hostState?.dhcpConfigCustom
					? {
							label,
							warning: true,
							lines: [
								'`/usr/local/etc/dnsmasq.conf` has settings that Sylve did not write. Sylve starts `dnsmasq` with them.',
								'Once DHCP or DNS is set up in Sylve, the file is overwritten without a backup.'
							]
						}
					: {
							label,
							lines: [
								'Starts `dnsmasq`. `/usr/local/etc/dnsmasq.conf` is rewritten, without a backup, once DHCP or DNS is set up in Sylve.'
							]
						}
			);
		} else {
			const lines = [...(INITIALIZATION_SERVICE_CHANGES[id] ?? [])];
			if (id === 'jails' && hostState?.racctLoaderChange) {
				lines.unshift(
					'Adds `kern.racct.enable=1` to `/boot/loader.conf`. Jail resource limits work after a reboot.'
				);
			}
			if (lines.length > 0) changes.push({ label, lines });
		}
	}

	changes.push({
		label: 'Always',
		lines: [
			'Adds a `devfsrules_jails` ruleset to `/etc/devfs.rules` if it is missing, and restarts devfs.'
		]
	});

	return changes.sort((a, b) => Number(!!b.warning) - Number(!!a.warning));
}
