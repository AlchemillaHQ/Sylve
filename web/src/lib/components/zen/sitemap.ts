/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { Need } from '$lib/stores/permissions.svelte';
import type { NavigationItem } from './navigation';

function preview(id: string, label: string, icon: string, need?: Need): NavigationItem {
	return { id, label, icon, need };
}

export function isDatacenterScope(pathname: string): boolean {
	return pathname === '/datacenter' || pathname.startsWith('/datacenter/');
}

export function getNodeNavigation(
	node: string,
	hostPath: string,
	explorer: boolean,
	samba: boolean
): NavigationItem[] {
	const host: Need = ['Sys.Audit', hostPath];
	const audit: Need = ['Sys.Audit', '/'];
	return [
		{
			id: 'overview',
			label: 'Overview',
			icon: 'icon-[lucide--layout-dashboard]',
			href: `/${node}/summary`
		},
		{
			id: 'storage',
			label: 'Storage',
			icon: 'icon-[mdi--storage]',
			need: host,
			children: [
				{
					id: 'storage-dashboard',
					label: 'Dashboard',
					icon: 'icon-[mdi--monitor-dashboard]',
					href: `/${node}/storage/zfs/dashboard`
				},
				{
					id: 'storage-disks',
					label: 'Disks',
					icon: 'icon-[mdi--harddisk]',
					href: `/${node}/storage/disks`
				},
				{
					id: 'storage-pools',
					label: 'Pools',
					icon: 'icon-[bi--hdd-stack-fill]',
					href: `/${node}/storage/zfs/pools`
				},
				{
					id: 'storage-filesystems',
					label: 'File Systems',
					icon: 'icon-[eos-icons--file-system]',
					href: `/${node}/storage/zfs/datasets/fs`
				},
				{
					id: 'storage-volumes',
					label: 'Volumes',
					icon: 'icon-[carbon--volume-block-storage]',
					href: `/${node}/storage/zfs/datasets/volumes`
				},
				{
					id: 'storage-snapshots',
					label: 'Snapshots',
					icon: 'icon-[carbon--ibm-cloud-vpc-block-storage-snapshots]',
					href: `/${node}/storage/zfs/datasets/snapshots`
				},
				...(explorer
					? [
							{
								id: 'storage-explorer',
								label: 'Explorer',
								icon: 'icon-[bxs--folder-open]',
								href: `/${node}/storage/explorer`
							}
						]
					: [])
			]
		},
		{
			id: 'shares',
			label: 'Shares',
			icon: 'icon-[mdi--folder-network]',
			need: host,
			children: [
				...(samba
					? [
							{
								id: 'shares-samba',
								label: 'Samba',
								icon: 'icon-[material-symbols--smb-share]',
								children: [
									{
										id: 'shares-samba-shares',
										label: 'Shares',
										icon: 'icon-[mdi--folder-network]',
										href: `/${node}/storage/samba/shares`
									},
									{
										id: 'shares-samba-audit-log',
										label: 'Audit Log',
										icon: 'icon-[mdi--text-box-search-outline]',
										href: `/${node}/storage/samba/audit-logs`
									},
									{
										id: 'shares-samba-settings',
										label: 'Settings',
										icon: 'icon-[mdi--cog-outline]',
										href: `/${node}/storage/samba/settings`
									}
								]
							}
						]
					: []),
				preview('shares-nfs', 'NFS', 'icon-[mdi--folder-network]'),
				preview('shares-iscsi', 'iSCSI', 'icon-[carbon--block-storage-alt]')
			]
		},
		{
			id: 'network',
			label: 'Network',
			icon: 'icon-[mdi--network]',
			need: host,
			children: [
				preview('network-objects', 'Objects', 'icon-[clarity--objects-solid]'),
				preview('network-interfaces', 'Interfaces', 'icon-[carbon--network-interface]'),
				{
					id: 'network-switches',
					label: 'Switches',
					icon: 'icon-[clarity--network-switch-line]',
					children: [
						preview(
							'network-switches-manual',
							'Manual',
							'icon-[streamline-sharp--router-wifi-network-solid]'
						),
						preview('network-switches-standard', 'Standard', 'icon-[mdi--router-network]')
					]
				},
				preview('network-routes', 'Routes', 'icon-[mdi--routes]'),
				{
					id: 'network-dhcp',
					label: 'DHCP & DNS',
					icon: 'icon-[solar--server-path-bold]',
					children: [
						preview('network-dhcp-ranges', 'Ranges', 'icon-[memory--range]'),
						preview('network-dhcp-leases', 'Leases', 'icon-[mdi--clipboard-list]'),
						preview('network-dhcp-settings', 'Settings', 'icon-[mdi--cog-outline]')
					]
				},
				{
					id: 'network-firewall',
					label: 'Firewall',
					icon: 'icon-[mdi--firewall]',
					children: [
						preview('network-firewall-logs', 'Logs', 'icon-[mdi--text-box-search-outline]'),
						preview(
							'network-firewall-traffic',
							'Traffic Rules',
							'icon-[mdi--transit-connection-horizontal]'
						),
						preview('network-firewall-nat', 'NAT Rules', 'icon-[mdi--swap-horizontal-bold]'),
						preview('network-firewall-advanced', 'Advanced', 'icon-[mdi--cog-outline]')
					]
				},
				preview('network-mdns', 'mDNS', 'icon-[mdi--broadcast]'),
				{
					id: 'network-vpn',
					label: 'VPN',
					icon: 'icon-[mdi--vpn]',
					defaultOpen: true,
					children: [
						{
							id: 'network-wireguard',
							label: 'WireGuard',
							icon: 'icon-[simple-icons--wireguard]',
							defaultOpen: true,
							children: [
								preview('network-wireguard-servers', 'Servers', 'icon-[mdi--dns]'),
								preview('network-wireguard-clients', 'Clients', 'icon-[mdi--account]')
							]
						}
					]
				},
				preview('network-dynamic-dns', 'Dynamic DNS', 'icon-[mdi--dns]'),
				preview('network-settings', 'Global Configuration', 'icon-[mdi--cog-outline]')
			]
		},
		preview('vms', 'Virtual Machines', 'icon-[material-symbols--monitor-outline]'),
		preview('jails', 'Jails', 'icon-[hugeicons--prison]'),
		{
			id: 'containers',
			label: 'Containers',
			icon: 'icon-[lucide--container]',
			children: [
				preview('containers-instances', 'Instances', 'icon-[lucide--container]'),
				preview('containers-images', 'Images', 'icon-[mdi--layers-triple-outline]', host),
				preview('containers-networks', 'Networks', 'icon-[mdi--lan]', host),
				preview('containers-registries', 'Registries', 'icon-[mdi--key-chain-variant]', host)
			]
		},
		{
			id: 'host-access',
			label: 'Host Access',
			icon: 'icon-[mdi--shield-key]',
			children: [
				{
					id: 'host-accounts',
					label: 'Host Accounts',
					icon: 'icon-[mdi--account]',
					need: ['User.Modify', '/access'],
					children: [
						preview('host-users', 'Users', 'icon-[mdi--account]', host),
						preview('host-groups', 'Unix Groups', 'icon-[mdi--account-group]', host)
					]
				},
				{
					id: 'host-directory',
					label: 'Directory Services',
					icon: 'icon-[carbon--directory-domain]',
					need: host,
					children: [
						preview('host-directory-ad', 'Active Directory', 'icon-[mdi--server-network]'),
						{
							id: 'host-kerberos',
							label: 'Kerberos',
							icon: 'icon-[mdi--key-chain]',
							children: [
								preview('host-kerberos-realms', 'Realms', 'icon-[mdi--earth]'),
								preview('host-kerberos-keytabs', 'Keytabs', 'icon-[mdi--key-chain]'),
								preview('host-kerberos-settings', 'Settings', 'icon-[mdi--cog-outline]')
							]
						}
					]
				},
				preview('host-certificates', 'Certificates', 'icon-[mdi--certificate-outline]', host)
			]
		},
		{
			id: 'system',
			label: 'System',
			icon: 'icon-[mdi--desktop-classic]',
			need: host,
			children: [
				preview(
					'system-services',
					'Services',
					'icon-[material-symbols--design-services-outline-rounded]'
				),
				{
					id: 'system-notifications',
					label: 'Notifications',
					icon: 'icon-[mdi--bell-ring-outline]',
					need: audit,
					children: [
						preview('system-transports', 'Transports', 'icon-[mdi--swap-horizontal-bold]'),
						preview('system-notification-rules', 'Rules', 'icon-[mdi--format-list-checks]')
					]
				},
				{
					id: 'system-updates',
					label: 'Updates',
					icon: 'icon-[mdi--update]',
					children: [
						preview('system-update-packages', 'Packages', 'icon-[mdi--package-variant]'),
						preview('system-update-sylve', 'Sylve', 'sylve'),
						preview('system-update-audit', 'Audit', 'icon-[mdi--shield-search]'),
						preview('system-update-history', 'History', 'icon-[mdi--history]')
					]
				},
				preview('system-tunables', 'Tunables', 'icon-[mdi--tune-variant]'),
				preview('system-passthrough', 'PCI Passthrough', 'icon-[eos-icons--hardware-circuit]')
			]
		},
		{
			id: 'tools',
			label: 'Tools',
			icon: 'icon-[mdi--tools]',
			children: [
				{
					id: 'notes',
					label: 'Notes',
					icon: 'icon-[lucide--sticky-note]',
					href: `/${node}/notes`,
					need: host
				},
				{
					id: 'terminal',
					label: 'Terminal',
					icon: 'icon-[lucide--terminal]',
					href: `/${node}/terminal`,
					need: ['Sys.Console', hostPath]
				},
				preview('tools-downloader', 'Downloader', 'icon-[material-symbols--download]', audit),
				preview(
					'tools-cloud-init',
					'Cloud Init Templates',
					'icon-[mdi--cloud-upload-outline]',
					audit
				)
			]
		}
	];
}

export function getDatacenterNavigation(): NavigationItem[] {
	const audit: Need = ['Sys.Audit', '/'];
	const access: Need = ['Sys.Audit', '/access'];
	return [
		{
			id: 'dc-overview',
			label: 'Overview',
			icon: 'icon-[basil--document-outline]',
			href: '/datacenter/summary'
		},
		preview('dc-cluster', 'Cluster', 'icon-[carbon--assembly-cluster]', audit),
		{
			id: 'dc-protection',
			label: 'Data Protection',
			icon: 'icon-[mdi--backup-restore]',
			need: audit,
			children: [
				{
					id: 'dc-backups',
					label: 'Backups',
					icon: 'icon-[mdi--backup-restore]',
					children: [
						preview('dc-backup-targets', 'Targets', 'icon-[mdi--server-network]'),
						preview('dc-backup-jobs', 'Jobs', 'icon-[mdi--calendar-sync]'),
						preview('dc-backup-events', 'Events', 'icon-[mdi--history]')
					]
				},
				{
					id: 'dc-replication',
					label: 'Replication',
					icon: 'icon-[carbon--ibm-data-replication]',
					children: [
						preview('dc-replication-policies', 'Policies', 'icon-[mdi--clipboard-list-outline]'),
						preview('dc-replication-events', 'Events', 'icon-[mdi--history]')
					]
				}
			]
		},
		{
			id: 'dc-access',
			label: 'Access',
			icon: 'icon-[mdi--shield-account]',
			children: [
				preview('dc-access-users', 'Users', 'icon-[mdi--account]', ['User.Modify', '/access']),
				preview('dc-access-groups', 'Groups', 'icon-[mdi--account-multiple]', access),
				preview('dc-access-grants', 'Grants', 'icon-[mdi--account-key]', access),
				preview('dc-access-roles', 'Roles', 'icon-[mdi--card-account-details-outline]', access),
				preview('dc-access-realms', 'Login Realms', 'icon-[mdi--login-variant]', access)
			]
		},
		preview('dc-notes', 'Notes', 'icon-[mdi--notes]', audit)
	];
}
