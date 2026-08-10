<script lang="ts">
	import { storage } from '$lib';
	import { getNodes } from '$lib/api/cluster/cluster';
	import { getAuditRecords } from '$lib/api/info/audit';
	import { getSimpleJails, getSimpleJailTemplates } from '$lib/api/jail/jail';
	import { getActiveLifecycleTasks } from '$lib/api/task/lifecycle';
	import { getSimpleVMs, getSimpleVMTemplates } from '$lib/api/vm/vm';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import AuditDetailModal from '$lib/components/custom/Dialog/AuditDetailModal.svelte';
	import * as Table from '$lib/components/ui/table/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import type { ClusterNode } from '$lib/types/cluster/cluster';
	import type { AuditRecord } from '$lib/types/info/audit';
	import type { SimpleJail, SimpleJailTemplate } from '$lib/types/jail/jail';
	import type { LifecycleTask } from '$lib/types/task/lifecycle';
	import type { SimpleVmTemplate } from '$lib/types/vm/vm';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import { resource, useInterval, watch } from 'runed';
	import { SvelteSet } from 'svelte/reactivity';
	import { fade } from 'svelte/transition';

	interface Props {
		clustered?: boolean;
		onLifecycleActiveChange?: (active: boolean) => void;
	}

	type AuditDetailSection = 'request' | 'response';
	type ResolvedAuditRecord = AuditRecord & { resolvedAction: string };

	let { clustered = false, onLifecycleActiveChange }: Props = $props();

	let selectedHostname = $state(storage.hostname || '');
	const effectiveHostname = $derived(selectedHostname || storage.hostname || '');

	const clusterNodes = resource(
		() => `cluster-nodes-for-audit-${clustered ? 'clustered' : 'single'}`,
		async () => {
			if (!clustered) {
				return [];
			}

			return await getNodes();
		},
		{
			initialValue: [] as ClusterNode[]
		}
	);

	const hostnameOptions = $derived.by(() => {
		const names = new SvelteSet<string>();

		if (storage.hostname) {
			names.add(storage.hostname);
		}

		for (const node of clusterNodes.current) {
			if (node.hostname) {
				names.add(node.hostname);
			}
		}

		return Array.from(names)
			.sort((a, b) => a.localeCompare(b))
			.map((hostname) => ({
				value: hostname,
				label: hostname
			}));
	});

	const auditRecords = resource(
		() => `audit-record-${effectiveHostname || 'default'}`,
		async (key) => {
			const results = await getAuditRecords(effectiveHostname || undefined);
			updateCache(key, results);
			return results;
		}
	);

	const simpleVmList = resource(
		() => `simple-vm-list-${effectiveHostname || 'default'}`,
		async (key) => {
			const results = await getSimpleVMs(effectiveHostname || undefined);
			updateCache(key, results);
			return results;
		}
	);

	const simpleJails = resource(
		() => `simple-jail-list-${effectiveHostname || 'default'}`,
		async (key) => {
			const results = await getSimpleJails(effectiveHostname || undefined);
			updateCache(key, results);
			return results;
		},
		{
			initialValue: [] as SimpleJail[]
		}
	);

	const simpleJailTemplates = resource(
		() => `simple-jail-template-list-${effectiveHostname || 'default'}`,
		async (key) => {
			const results = await getSimpleJailTemplates(effectiveHostname || undefined);
			updateCache(key, results);
			return results;
		},
		{
			initialValue: [] as SimpleJailTemplate[]
		}
	);

	const simpleVMTemplates = resource(
		() => `simple-vm-template-list-${effectiveHostname || 'default'}`,
		async (key) => {
			const results = await getSimpleVMTemplates(effectiveHostname || undefined);
			updateCache(key, results);
			return results;
		},
		{
			initialValue: [] as SimpleVmTemplate[]
		}
	);

	const activeLifecycleTasks = resource(
		() => `active-lifecycle-tasks-${effectiveHostname || 'default'}`,
		async () => {
			return await getActiveLifecycleTasks(undefined, undefined, effectiveHostname || undefined);
		},
		{
			initialValue: [] as LifecycleTask[]
		}
	);

	useInterval(() => 2000, {
		callback: () => {
			if (!storage.visible) return;
			activeLifecycleTasks.refetch();
			if (auditRecords.current?.some((r) => r.status === 'pending')) {
				auditRecords.refetch();
			}
		}
	});

	watch(
		() => reload.auditLog,
		(value) => {
			if (value) {
				auditRecords.refetch().then(() => {
					reload.auditLog = false;
				});
			}
		}
	);

	watch(
		() => storage.hostname,
		(hostname) => {
			if (!selectedHostname && hostname) {
				selectedHostname = hostname;
			}
		}
	);

	const pathToActionMap: Record<string, string> = $derived({
		'/api/auth/passkeys/login/begin': 'Login - Passkey - Begin',
		'/api/auth/passkeys/login/finish': 'Login - Passkey - Finish',
		'/api/auth/passkeys/register/begin': 'Passkey - Register - Begin',
		'/api/auth/passkeys/register/finish': 'Passkey - Register - Finish',
		'/api/auth/passkeys/users': 'Passkey',
		'/api/auth/login': 'Login',
		'/api/info/notes': 'Notes',
		'/api/mdns/config': 'mDNS Config',
		'/api/mdns/records': 'mDNS Record',
		'/api/network/switch/standard': 'Standard Switch',
		'/api/network/switch': 'Network Switches',
		'/api/vnc': 'VNC',
		'/api/info/terminal': 'Host Terminal - Session',
		'/api/network/object': 'Network Object',
		'/api/network/dhcp/config': 'DHCP Config',
		'/api/network/dhcp/range': 'DHCP Range',
		'/api/network/dhcp/lease': 'DHCP Lease',
		'/api/system/file-explorer/delete': 'File Explorer - Delete',
		'/api/system/file-explorer/copy-or-move-batch': 'File Explorer - Batch Copy/Move',
		'/api/system/file-explorer/rename': 'File Explorer - Rename',
		'/api/system/file-explorer/upload': 'File Explorer - Upload',
		'/api/system/file-explorer': 'File Explorer',
		'/api/system/ppt-devices/prepare': 'PCI Passthrough - Prepare',
		'/api/system/ppt-devices/import': 'PCI Passthrough - Import',
		'/api/system/ppt-devices': 'PCI Passthrough',
		'/api/system/tunables': 'System Tunable',
		'/api/zfs/datasets/filesystem': 'ZFS Filesystem',
		'/api/zfs/datasets/volume': 'ZFS Volume',
		'/api/samba/shares': 'Samba Share',
		'/api/auth/groups/users': 'Auth Group - Members',
		'/api/auth/groups': 'Auth Group',
		'/api/auth/users/import': 'Auth User - Import',
		'/api/auth/users/pam': 'Auth User - PAM',
		'/api/auth/users': 'Auth User',
		'/api/samba/config': 'Samba Config - Edit',
		'/api/zfs/datasets/snapshot/periodic': 'ZFS Periodic Snapshot',
		'/api/zfs/datasets/snapshot': 'ZFS Snapshot',
		'/api/vm/start': 'VM - Start',
		'/api/vm/stop': 'VM - Stop',
		'/api/vm/shutdown': 'VM - Shutdown',
		'/api/vm/reboot': 'VM - Reboot',
		'/api/vm/description': 'VM - Update Description',
		'/api/vm/name': 'VM - Update Name',
		'/api/vm/console': 'VM Console - Session',
		'/api/vm/templates/convert': 'VM Template - Convert',
		'/api/vm/templates/create': 'VM Template - Create',
		'/api/vm/templates': 'VM Template',
		'/api/utilities/downloader-uploads': 'Downloader - Upload',
		'/api/utilities/downloads/signed-url': 'Downloader - Create Signed URL',
		'/api/utilities/downloads/bulk-delete': 'Downloader - Bulk Delete',
		'/api/utilities/downloads': 'Downloader',
		'/api/vm/storage/detach': 'VM Storage - Detach',
		'/api/vm/storage/attach': 'VM Storage - Attach',
		'/api/vm/network/detach': 'VM Network - Detach',
		'/api/vm/network/attach': 'VM Network - Attach',
		'/api/vm/snapshots/rollback': 'VM Snapshot - Rollback',
		'/api/vm/snapshots': 'VM Snapshot',
		'/api/vm/storage/update': 'VM Storage - Update',
		'/api/vm/network/update': 'VM Network - Update',
		'/api/vm/hardware/cpu': 'VM Hardware - CPU',
		'/api/vm/hardware/ram': 'VM Hardware - RAM',
		'/api/vm/hardware/vnc': 'VM Hardware - VNC',
		'/api/vm/hardware/ppt': 'VM Hardware - Passthrough',
		'/api/vm/options/wol': 'VM Options - Wake-on-LAN',
		'/api/vm/options/boot-order': 'VM Options - Boot Order',
		'/api/vm/options/clock': 'VM Options - Clock',
		'/api/vm/options/serial-console': 'VM Options - Serial Console',
		'/api/vm/options/shutdown-wait-time': 'VM Options - Shutdown Wait Time',
		'/api/vm/options/cloud-init': 'VM Options - Cloud-Init',
		'/api/vm/options/boot-rom': 'VM Options - Boot ROM',
		'/api/vm/options/extra-bhyve-options': 'VM Options - Extra Bhyve',
		'/api/vm/options/ignore-umsrs': 'VM Options - Ignore UMSRs',
		'/api/vm/options/qemu-guest-agent': 'VM Options - QEMU Guest Agent',
		'/api/vm/options/tpm': 'VM Options - TPM',
		'/api/vm/migrate': 'VM - Migrate',
		'/api/vm': 'VM',
		'/api/network/switch/manual': 'Manual Switch',
		'/api/zfs/pools': 'ZFS Pool',
		'/api/zfs/pools/:id/scrub': 'ZFS Pool - Scrub',
		'/api/zfs/pools/:id/replace-device': 'ZFS Pool - Replace Device',
		'/api/jail/bootstraps': 'Jail Bootstrap',
		'/api/jail': 'Jail',
		'/api/utilities/cloud-init/templates': 'Cloud Init Template',
		'/api/system/basic-settings/pools': 'Basic Settings - ZFS Pools',
		'/api/network/firewall/traffic/counters': 'Firewall Traffic Counters',
		'/api/network/firewall/traffic/reorder': 'Firewall Traffic Rule - Reorder',
		'/api/network/firewall/traffic': 'Firewall Traffic Rule',
		'/api/network/firewall/nat/counters': 'Firewall NAT Counters',
		'/api/network/firewall/nat/reorder': 'Firewall NAT Rule - Reorder',
		'/api/network/firewall/nat': 'Firewall NAT Rule',
		'/api/network/firewall/advanced': 'Firewall - Advanced Rules',
		'/api/network/route': 'Static Route',
		'/api/network/wireguard/server/peer': 'WireGuard Peer',
		'/api/network/wireguard/server': 'WireGuard Server',
		'/api/network/wireguard/clients': 'WireGuard - Client',
		'/api/cluster/notes/bulk-delete': 'DC Notes - Bulk Delete',
		'/api/cluster/notes': 'DC Notes',
		'/api/cluster/reset-node': 'Cluster - Reset Node',
		'/api/cluster/backups/targets/validate': 'DC Backup Target - Validate',
		'/api/cluster/backups/targets/:id/restore': 'DC Backup Target - Restore',
		'/api/cluster/backups/targets': 'DC Backup Target',
		'/api/cluster/backups/jobs/run': 'DC Backup Job - Run',
		'/api/cluster/backups/jobs/:id/restore': 'DC Backup Job - Restore',
		'/api/cluster/backups/jobs': 'DC Backup Job',
		'/api/cluster/replication/policies/run': 'DC Replication Policy - Run',
		'/api/cluster/replication/policies/failover': 'DC Replication Policy - Failover',
		'/api/cluster/replication/policies': 'DC Replication Policy',
		'/api/cluster/join': 'Cluster - Join',
		'/api/cluster/accept-join': 'Cluster - Accept Join',
		'/api/cluster/resync-state': 'Cluster - Resync State',
		'/api/cluster/remove-peer': 'Cluster - Remove Peer',
		'/api/cluster': 'Cluster',
		'/api/iscsi/targets/:id/portals/:id': 'iSCSI Target - Remove Portal',
		'/api/iscsi/targets/:id/portals': 'iSCSI Target - Add Portal',
		'/api/iscsi/targets/:id/luns/:id': 'iSCSI Target - Remove LUN',
		'/api/iscsi/targets/:id/luns': 'iSCSI Target - Add LUN',
		'/api/iscsi/targets': 'iSCSI Target',
		'/api/iscsi/initiators/:id/connect': 'iSCSI Initiator - Connect',
		'/api/iscsi/initiators': 'iSCSI Initiator',
		'/api/iscsi': 'iSCSI',
		'/api/notifications/transports/:id/test': 'Notification Transport - Test',
		'/api/notifications/transports': 'Notification Transport',
		'/api/notifications/rules/bulk-delete': 'Notification Rule - Bulk Delete',
		'/api/notifications/rules/bulk-update': 'Notification Rule - Bulk Update',
		'/api/notifications/rules': 'Notification Rule',
		'/api/notifications/dismiss-all': 'Notification - Dismiss All',
		'/api/notifications/:id/dismiss': 'Notification - Dismiss',
		'/api/notifications': 'Notification',
		'/api/basic/system/reboot': 'System - Reboot',
		'/api/basic/initialize': 'System - Initialize',
		'/api/tasks/migration/cancel': 'Migration - Cancel',
		'/api/basic': 'Basic Settings',
		'/api/health': 'Health Check'
	});

	const methodPathToActionMap: Record<string, Record<string, string>> = {
		DELETE: {
			'/api/vm/templates/:id': 'VM Template - Delete',
			'/api/jail/templates/:id': 'Jail Template - Delete',
			'/api/vm/:id/snapshots/:id': 'VM Snapshot - Delete',
			'/api/vm/:id/storage/:id': 'VM Storage - Detach',
			'/api/vm/:id/networks/:id': 'VM Network - Detach',
			'/api/vm/:id/registration': 'VM - Purge Registration',
			'/api/vm/:id': 'VM - Delete',
			'/api/jail/:id': 'Jail - Delete',
			'/api/jail/:id/networks/:id': 'Jail Network - Delete',
			'/api/info/notes': 'Notes - Bulk Delete',
			'/api/system/file-explorer/upload': 'File Explorer - Upload Revert',
			'/api/system/ppt-devices/:id': 'PCI Passthrough - Disable',
			'/api/zfs/datasets': 'ZFS Dataset - Delete',
			'/api/network/object': 'Network Object - Bulk Delete',
			'/api/network/object/:id': 'Network Object - Delete',
			'/api/network/route/:id': 'Static Route - Delete',
			'/api/network/switch/manual/:id': 'Manual Switch - Delete',
			'/api/network/switch/standard/:id': 'Standard Switch - Delete',
			'/api/network/dhcp/range/:id': 'DHCP Range - Delete',
			'/api/dynamic-dns/entries/:id': 'Dynamic DNS Entry - Delete',
			'/api/certificates/:id': 'TLS Certificate - Delete',
			'/api/certificates/:id/activate': 'TLS Certificate - Cancel Pending Activation',
			'/api/disk/:device/partition-table': 'Disk - Clear Partition Table',
			'/api/disk/partitions/:partition': 'Disk - Delete Partition',
			'/api/disk/smart/self-test/schedules/:id': 'Disk - S.M.A.R.T. Self-Test Schedule - Delete',
			'/api/network/firewall/traffic/:id': 'Firewall Traffic Rule - Delete',
			'/api/network/firewall/nat/:id': 'Firewall NAT Rule - Delete',
			'/api/network/wireguard/server/peer/:id': 'WireGuard Peer - Delete',
			'/api/network/wireguard/server': 'WireGuard Server - Deinitialize',
			'/api/network/wireguard/clients/:id': 'WireGuard Client - Delete',
			'/api/network/dhcp/lease/dynamic': 'DHCP Lease - Delete Dynamic',
			'/api/network/dhcp/lease/:id': 'DHCP Lease - Delete Static'
		},
		POST: {
			'/api/vm': 'VM - Create',
			'/api/jail': 'Jail - Create',
			'/api/jail/:id/networks': 'Jail Network - Create',
			'/api/vm/:id/storage': 'VM Storage - Attach',
			'/api/vm/:id/networks': 'VM Network - Attach',
			'/api/vm/:id/snapshots': 'VM Snapshot - Create',
			'/api/vm/:id/snapshots/:id/rollback': 'VM Snapshot - Rollback',
			'/api/vm/:id/templates': 'VM Template - Capture',
			'/api/vm/templates/:id/vms': 'VM Template - Instantiate',
			'/api/jail/:id/templates': 'Jail Template - Capture',
			'/api/jail/templates/:id/jails': 'Jail Template - Instantiate',
			'/api/vm/:id/actions/start': 'VM - Start',
			'/api/vm/:id/actions/stop': 'VM - Stop',
			'/api/vm/:id/actions/shutdown': 'VM - Shutdown',
			'/api/vm/:id/actions/reboot': 'VM - Reboot',
			'/api/vm/:id/migrations': 'VM - Migrate',
			'/api/jail/:id/actions/start': 'Jail - Start',
			'/api/jail/:id/actions/stop': 'Jail - Stop',
			'/api/jail/:id/actions/restart': 'Jail - Restart',
			'/api/jail/:id/migrations': 'Jail - Migrate',
			'/api/system/ppt-devices': 'PCI Passthrough - Enable',
			'/api/zfs/datasets/snapshot/:id/rollback': 'ZFS Snapshot - Rollback',
			'/api/zfs/datasets/volume/:id/flash': 'ZFS Volume - Flash',
			'/api/network/object': 'Network Object - Create',
			'/api/network/route': 'Static Route - Create',
			'/api/network/switch/manual': 'Manual Switch - Create',
			'/api/network/switch/standard': 'Standard Switch - Create',
			'/api/network/dhcp/range': 'DHCP Range - Create',
			'/api/dynamic-dns/entries': 'Dynamic DNS Entry - Create',
			'/api/dynamic-dns/entries/:id/sync': 'Dynamic DNS Entry - Sync',
			'/api/certificates': 'TLS Certificate - Create',
			'/api/certificates/:id/activate': 'TLS Certificate - Schedule Activation',
			'/api/certificates/:id/renew': 'TLS Certificate - Renew',
			'/api/certificates/:id/retry': 'TLS Certificate - Retry Issuance',
			'/api/disk/:device/partition-table': 'Disk - Initialize GPT',
			'/api/disk/:device/partitions': 'Disk - Create Partitions',
			'/api/disk/smart/self-test': 'Disk - S.M.A.R.T. Self-Test - Start',
			'/api/disk/smart/self-test/abort': 'Disk - S.M.A.R.T. Self-Test - Abort',
			'/api/disk/smart/self-test/schedules': 'Disk - S.M.A.R.T. Self-Test Schedule - Create',
			'/api/network/firewall/traffic': 'Firewall Traffic Rule - Create',
			'/api/network/firewall/nat': 'Firewall NAT Rule - Create',
			'/api/network/wireguard/server/peer': 'WireGuard Peer - Create',
			'/api/network/wireguard/server': 'WireGuard Server - Initialize',
			'/api/network/wireguard/clients': 'WireGuard Client - Create',
			'/api/network/dhcp/lease': 'DHCP Lease - Create'
		},
		PUT: {
			'/api/jail/:id/network/inheritance': 'Jail Network - Update Inheritance',
			'/api/jail/:id/hardware/ram': 'Jail Hardware - RAM',
			'/api/jail/:id/hardware/cpu': 'Jail Hardware - CPU',
			'/api/jail/:id/hardware/resource-limits': 'Jail Hardware - Resource Limits',
			'/api/jail/:id/options/wol': 'Jail Options - Wake-on-LAN',
			'/api/jail/:id/options/boot-order': 'Jail Options - Boot Order',
			'/api/jail/:id/options/fstab': 'Jail Options - FSTab',
			'/api/jail/:id/options/resolv-conf': 'Jail Options - Resolv.conf',
			'/api/jail/:id/options/devfs-rules': 'Jail Options - DevFS Rules',
			'/api/jail/:id/options/additional-options': 'Jail Options - Additional',
			'/api/jail/:id/options/allowed-options': 'Jail Options - Allowed',
			'/api/jail/:id/options/metadata': 'Jail Options - Metadata',
			'/api/jail/:id/options/lifecycle-hooks': 'Jail Options - Lifecycle Hooks',
			'/api/network/object/:id': 'Network Object - Update',
			'/api/network/route/:id': 'Static Route - Update',
			'/api/network/switch/standard/:id': 'Standard Switch - Update',
			'/api/network/dhcp/config': 'DHCP Config - Update',
			'/api/network/dhcp/range/:id': 'DHCP Range - Update',
			'/api/dynamic-dns/entries/:id': 'Dynamic DNS Entry - Update',
			'/api/disk/smart/self-test/schedules/:id': 'Disk - S.M.A.R.T. Self-Test Schedule - Update',
			'/api/network/firewall/traffic/:id': 'Firewall Traffic Rule - Update',
			'/api/network/firewall/traffic/reorder': 'Firewall Traffic Rule - Reorder',
			'/api/network/firewall/nat/:id': 'Firewall NAT Rule - Update',
			'/api/network/firewall/nat/reorder': 'Firewall NAT Rule - Reorder',
			'/api/network/firewall/advanced': 'Firewall Advanced Rules - Update',
			'/api/network/wireguard/server/peer/:id': 'WireGuard Peer - Update',
			'/api/network/wireguard/server': 'WireGuard Server - Update',
			'/api/network/wireguard/clients/:id': 'WireGuard Client - Update',
			'/api/network/dhcp/lease/:id': 'DHCP Lease - Update'
		},
		PATCH: {
			'/api/vm/:id/storage/:id': 'VM Storage - Update',
			'/api/vm/:id/networks/:id': 'VM Network - Update',
			'/api/vm/:id/description': 'VM - Update Description',
			'/api/vm/:id/name': 'VM - Update Name',
			'/api/jail/:id/description': 'Jail - Update Description',
			'/api/jail/:id/name': 'Jail - Update Name',
			'/api/jail/:id/networks/:id': 'Jail Network - Update',
			'/api/certificates/:id': 'TLS Certificate - Update',
			'/api/network/wireguard/server/peer/:id': 'WireGuard Peer - Update State',
			'/api/network/wireguard/server': 'WireGuard Server - Update State',
			'/api/network/wireguard/clients/:id': 'WireGuard Client - Update State'
		},
		GET: {
			'/api/certificates/:id/archive': 'TLS Certificate - Download',
			'/api/disk/smart/self-test': 'Disk - S.M.A.R.T. Self-Test - View',
			'/api/disk/smart/self-test/schedules': 'Disk - S.M.A.R.T. Self-Test Schedule - View'
		}
	};

	const basicSettingsServiceLabels = new Map([
		['dhcp-server', 'DHCP Server'],
		['wol-server', 'WoL Server'],
		['samba-server', 'Samba Server'],
		['jails', 'Jails'],
		['virtualization', 'Virtualization'],
		['firewall', 'Firewall'],
		['wireguard', 'WireGuard'],
		['iscsi', 'iSCSI'],
		['mdns', 'mDNS']
	]);

	const sortedPathToActionEntries = $derived.by(() =>
		Object.entries(pathToActionMap).sort(([a], [b]) => b.length - a.length)
	);

	let vmNameById = $derived.by(() => {
		return new Map((simpleVmList.current || []).map((vm) => [vm.rid, vm.name]));
	});

	function vmIdentityLabel(rid: number, requestName?: unknown): string {
		const bodyName = typeof requestName === 'string' ? requestName.trim() : '';
		const name = bodyName || vmNameById.get(rid) || '';
		return name ? `${name} (RID ${rid})` : `RID ${rid}`;
	}

	type VMSnapshotAuditTarget = {
		rid: number;
		snapshotId?: number;
		action: 'Create' | 'View' | 'Rollback' | 'Delete';
	};

	function vmSnapshotAuditTarget(path: string, method: string): VMSnapshotAuditTarget | null {
		const upperMethod = method.toUpperCase();
		let match = path.match(/^\/api\/vm\/(\d+)\/snapshots$/);
		if (match) {
			const rid = Number(match[1]);
			if (upperMethod === 'POST') return { rid, action: 'Create' };
			if (upperMethod === 'GET') return { rid, action: 'View' };
		}

		match = path.match(/^\/api\/vm\/(\d+)\/snapshots\/(\d+)\/rollback$/);
		if (match && upperMethod === 'POST') {
			return { rid: Number(match[1]), snapshotId: Number(match[2]), action: 'Rollback' };
		}

		match = path.match(/^\/api\/vm\/(\d+)\/snapshots\/(\d+)$/);
		if (match && upperMethod === 'DELETE') {
			return { rid: Number(match[1]), snapshotId: Number(match[2]), action: 'Delete' };
		}

		// Retain identity extraction for audit records written before the route
		// hierarchy was corrected.
		match = path.match(/^\/api\/vm\/snapshots\/rollback\/(\d+)\/(\d+)$/);
		if (match && upperMethod === 'POST') {
			return { rid: Number(match[1]), snapshotId: Number(match[2]), action: 'Rollback' };
		}

		match = path.match(/^\/api\/vm\/snapshots\/(\d+)\/(\d+)$/);
		if (match && upperMethod === 'DELETE') {
			return { rid: Number(match[1]), snapshotId: Number(match[2]), action: 'Delete' };
		}

		match = path.match(/^\/api\/vm\/snapshots\/(\d+)$/);
		if (match) {
			const rid = Number(match[1]);
			if (upperMethod === 'POST') return { rid, action: 'Create' };
			if (upperMethod === 'GET') return { rid, action: 'View' };
		}

		return null;
	}

	type VMStorageAuditTarget = {
		rid?: number;
		storageId?: number;
		action: 'Attach' | 'Update' | 'Detach';
	};

	function vmStorageAuditTarget(
		path: string,
		method: string,
		body: Record<string, unknown> | null | undefined
	): VMStorageAuditTarget | null {
		const upperMethod = method.toUpperCase();
		let match = path.match(/^\/api\/vm\/(\d+)\/storage$/);
		if (match && upperMethod === 'POST') {
			return { rid: Number(match[1]), action: 'Attach' };
		}

		match = path.match(/^\/api\/vm\/(\d+)\/storage\/(\d+)$/);
		if (match && (upperMethod === 'PATCH' || upperMethod === 'DELETE')) {
			return {
				rid: Number(match[1]),
				storageId: Number(match[2]),
				action: upperMethod === 'PATCH' ? 'Update' : 'Detach'
			};
		}

		// Preserve useful identity for records written before storage routes were nested.
		if (path === '/api/vm/storage/attach' && upperMethod === 'POST') {
			const rid = Number(body?.rid);
			return {
				...(Number.isSafeInteger(rid) && rid > 0 ? { rid } : {}),
				action: 'Attach'
			};
		}
		if (path === '/api/vm/storage/detach' && upperMethod === 'POST') {
			const rid = Number(body?.rid);
			const storageId = Number(body?.storageId);
			return {
				...(Number.isSafeInteger(rid) && rid > 0 ? { rid } : {}),
				...(Number.isSafeInteger(storageId) && storageId > 0 ? { storageId } : {}),
				action: 'Detach'
			};
		}
		if (path === '/api/vm/storage/update' && upperMethod === 'PUT') {
			const storageId = Number(body?.id);
			return {
				...(Number.isSafeInteger(storageId) && storageId > 0 ? { storageId } : {}),
				action: 'Update'
			};
		}

		return null;
	}

	type VMNetworkAuditTarget = {
		rid?: number;
		networkId?: number;
		action: 'Attach' | 'Update' | 'Detach';
	};

	function vmNetworkAuditTarget(
		path: string,
		method: string,
		body: Record<string, unknown> | null | undefined
	): VMNetworkAuditTarget | null {
		const upperMethod = method.toUpperCase();
		let match = path.match(/^\/api\/vm\/(\d+)\/networks$/);
		if (match && upperMethod === 'POST') {
			return { rid: Number(match[1]), action: 'Attach' };
		}

		match = path.match(/^\/api\/vm\/(\d+)\/networks\/(\d+)$/);
		if (match && (upperMethod === 'PATCH' || upperMethod === 'DELETE')) {
			return {
				rid: Number(match[1]),
				networkId: Number(match[2]),
				action: upperMethod === 'PATCH' ? 'Update' : 'Detach'
			};
		}

		// Preserve identity for audit records written before network routes were nested.
		if (path === '/api/vm/network/attach' && upperMethod === 'POST') {
			const rid = Number(body?.rid);
			return {
				...(Number.isSafeInteger(rid) && rid > 0 ? { rid } : {}),
				action: 'Attach'
			};
		}
		if (path === '/api/vm/network/detach' && upperMethod === 'POST') {
			const rid = Number(body?.rid);
			const networkId = Number(body?.networkId);
			return {
				...(Number.isSafeInteger(rid) && rid > 0 ? { rid } : {}),
				...(Number.isSafeInteger(networkId) && networkId > 0 ? { networkId } : {}),
				action: 'Detach'
			};
		}
		if (path === '/api/vm/network/update' && upperMethod === 'PUT') {
			const rid = Number(body?.rid);
			const networkId = Number(body?.networkId);
			return {
				...(Number.isSafeInteger(rid) && rid > 0 ? { rid } : {}),
				...(Number.isSafeInteger(networkId) && networkId > 0 ? { networkId } : {}),
				action: 'Update'
			};
		}

		return null;
	}

	type VMHardwareAuditTarget = {
		rid: number;
		component: 'CPU' | 'RAM' | 'VNC' | 'Passthrough';
	};

	type VMOptionAuditTarget = {
		rid: number;
		option: string;
	};

	const vmOptionLabels = new Map([
		['wol', 'Wake-on-LAN'],
		['boot-order', 'Boot Order'],
		['clock', 'Clock'],
		['serial-console', 'Serial Console'],
		['shutdown-wait-time', 'Shutdown Wait Time'],
		['cloud-init', 'Cloud-Init'],
		['boot-rom', 'Boot ROM'],
		['extra-bhyve-options', 'Extra Bhyve'],
		['ignore-umsrs', 'Ignore UMSRs'],
		['qemu-guest-agent', 'QEMU Guest Agent'],
		['tpm', 'TPM']
	]);

	function vmOptionAuditTarget(path: string, method: string): VMOptionAuditTarget | null {
		if (method.toUpperCase() !== 'PUT') return null;

		let match = path.match(/^\/api\/vm\/(\d+)\/options\/([^/]+)$/);
		let ridSegment = match?.[1];
		let optionSegment = match?.[2];
		if (!match) {
			// Preserve VM identity for records written before option routes were nested under the RID.
			match = path.match(/^\/api\/vm\/options\/([^/]+)\/(\d+)$/);
			optionSegment = match?.[1];
			ridSegment = match?.[2];
		}

		if (!ridSegment || !optionSegment) return null;
		const rid = Number(ridSegment);
		const option = vmOptionLabels.get(optionSegment);
		return Number.isSafeInteger(rid) && rid > 0 && option ? { rid, option } : null;
	}

	function vmConsoleAuditRID(path: string, query: string): number | null {
		const currentMatch = path.match(/^\/api\/vm\/(\d+)\/console$/);
		if (currentMatch) {
			const rid = Number(currentMatch[1]);
			return Number.isSafeInteger(rid) && rid > 0 ? rid : null;
		}

		if (path !== '/api/vm/console') return null;
		const rid = Number(new URLSearchParams(query).get('rid'));
		return Number.isSafeInteger(rid) && rid > 0 ? rid : null;
	}

	function jailConsoleAuditCTID(path: string): number | null {
		const match = path.match(/^\/api\/jail\/(\d+)\/console$/);
		if (!match) return null;
		const ctId = Number(match[1]);
		return Number.isSafeInteger(ctId) && ctId > 0 ? ctId : null;
	}

	function vmHardwareComponentLabel(component: string): VMHardwareAuditTarget['component'] | null {
		switch (component) {
			case 'cpu':
				return 'CPU';
			case 'ram':
				return 'RAM';
			case 'vnc':
				return 'VNC';
			case 'pci-devices':
			case 'ppt':
				return 'Passthrough';
			default:
				return null;
		}
	}

	function vmHardwareAuditTarget(path: string, method: string): VMHardwareAuditTarget | null {
		if (method.toUpperCase() !== 'PUT') return null;

		let match = path.match(/^\/api\/vm\/(\d+)\/hardware\/(cpu|ram|vnc|pci-devices)$/);
		if (match) {
			const component = vmHardwareComponentLabel(match[2]);
			return component ? { rid: Number(match[1]), component } : null;
		}

		// Preserve VM identity for audit records written before hardware routes were nested.
		match = path.match(/^\/api\/vm\/hardware\/(cpu|ram|vnc|ppt)\/(\d+)$/);
		if (match) {
			const component = vmHardwareComponentLabel(match[1]);
			return component ? { rid: Number(match[2]), component } : null;
		}

		return null;
	}

	let jailNameByCtId = $derived.by(() => {
		return new Map((simpleJails.current || []).map((jail) => [jail.ctId, jail.name]));
	});

	function jailIdentityLabel(ctId: number, requestName?: unknown): string {
		const bodyName = typeof requestName === 'string' ? requestName.trim() : '';
		const name = bodyName || jailNameByCtId.get(ctId) || '';
		return name ? `${name} (CTID ${ctId})` : `CTID ${ctId}`;
	}

	type JailOptionAuditTarget = {
		ctId: number;
		option: string;
		enabled?: boolean;
	};

	const jailOptionLabels = new Map([
		['wol', 'Wake-on-LAN'],
		['boot-order', 'Boot Order'],
		['fstab', 'FSTab'],
		['resolv-conf', 'Resolv.conf'],
		['devfs-rules', 'DevFS Rules'],
		['additional-options', 'Additional Options'],
		['allowed-options', 'Allowed Options'],
		['metadata', 'Metadata'],
		['lifecycle-hooks', 'Lifecycle Hooks']
	]);

	function jailOptionAuditTarget(
		path: string,
		method: string,
		body: Record<string, unknown> | undefined
	): JailOptionAuditTarget | null {
		if (method.toUpperCase() !== 'PUT') return null;

		const match = path.match(/^\/api\/jail\/(\d+)\/options\/([^/]+)$/);
		if (!match) return null;
		const ctId = Number(match[1]);
		const optionSegment = match[2];
		const option = jailOptionLabels.get(optionSegment);
		if (!Number.isSafeInteger(ctId) || ctId <= 0 || !option) return null;
		return {
			ctId,
			option,
			...(optionSegment === 'wol' && typeof body?.enabled === 'boolean'
				? { enabled: body.enabled }
				: {})
		};
	}

	type JailHardwareAuditTarget = {
		ctId: number;
		component: 'RAM' | 'CPU' | 'Resource Limits';
		enabled?: boolean;
	};

	function jailHardwareAuditTarget(
		path: string,
		method: string,
		body: Record<string, unknown> | undefined
	): JailHardwareAuditTarget | null {
		if (method.toUpperCase() !== 'PUT') return null;

		const match = path.match(/^\/api\/jail\/(\d+)\/hardware\/(ram|cpu|resource-limits)$/);
		if (!match) return null;
		const ctId = Number(match[1]);
		if (!Number.isSafeInteger(ctId) || ctId <= 0) return null;
		const component = match[2] === 'ram' ? 'RAM' : match[2] === 'cpu' ? 'CPU' : 'Resource Limits';
		return {
			ctId,
			component,
			...(component === 'Resource Limits' && typeof body?.enabled === 'boolean'
				? { enabled: body.enabled }
				: {})
		};
	}

	type JailSnapshotAuditTarget = {
		ctId: number;
		snapshotId?: number;
		action: 'Create' | 'Rollback' | 'Delete';
	};

	function jailSnapshotAuditTarget(path: string, method: string): JailSnapshotAuditTarget | null {
		const upperMethod = method.toUpperCase();
		let match = path.match(/^\/api\/jail\/(\d+)\/snapshots$/);
		if (match && upperMethod === 'POST') {
			return { ctId: Number(match[1]), action: 'Create' };
		}

		match = path.match(/^\/api\/jail\/(\d+)\/snapshots\/(\d+)\/rollback$/);
		if (match && upperMethod === 'POST') {
			return { ctId: Number(match[1]), snapshotId: Number(match[2]), action: 'Rollback' };
		}

		match = path.match(/^\/api\/jail\/(\d+)\/snapshots\/(\d+)$/);
		if (match && upperMethod === 'DELETE') {
			return { ctId: Number(match[1]), snapshotId: Number(match[2]), action: 'Delete' };
		}

		return null;
	}

	type JailNetworkAuditTarget = {
		ctId: number;
		networkId?: number;
		action: 'Create' | 'Update' | 'Delete' | 'Update Inheritance';
	};

	function jailNetworkAuditTarget(path: string, method: string): JailNetworkAuditTarget | null {
		const upperMethod = method.toUpperCase();
		let match = path.match(/^\/api\/jail\/(\d+)\/networks$/);
		if (match && upperMethod === 'POST') {
			return { ctId: Number(match[1]), action: 'Create' };
		}

		match = path.match(/^\/api\/jail\/(\d+)\/networks\/(\d+)$/);
		if (match && (upperMethod === 'PATCH' || upperMethod === 'DELETE')) {
			return {
				ctId: Number(match[1]),
				networkId: Number(match[2]),
				action: upperMethod === 'PATCH' ? 'Update' : 'Delete'
			};
		}

		match = path.match(/^\/api\/jail\/(\d+)\/network\/inheritance$/);
		if (match && upperMethod === 'PUT') {
			return { ctId: Number(match[1]), action: 'Update Inheritance' };
		}

		return null;
	}

	function bootstrapNameFromBody(body: Record<string, unknown> | undefined): string {
		const major = Number(body?.major);
		const minor = Number(body?.minor);
		const type = typeof body?.type === 'string' ? body.type.trim() : '';
		if (!Number.isSafeInteger(major) || major < 0 || !Number.isSafeInteger(minor) || minor < 0) {
			return '';
		}
		if (!type) return '';
		return `${major}-${minor}-${type.charAt(0).toUpperCase()}${type.slice(1)}`;
	}

	function bootstrapIdentityLabel(pool: unknown, name: unknown): string {
		const poolName = typeof pool === 'string' ? pool.trim() : '';
		const bootstrapName = typeof name === 'string' ? name.trim() : '';
		if (poolName && bootstrapName) return `${poolName}/${bootstrapName}`;
		return bootstrapName || poolName;
	}

	let templateNameById = $derived.by(() => {
		return new Map(
			(simpleJailTemplates.current || []).map((template) => [template.id, template.name])
		);
	});

	function jailTemplateIdentityLabel(templateId: number): string {
		const name = templateNameById.get(templateId);
		return name ? `${name} (Template ID ${templateId})` : `Template ID ${templateId}`;
	}

	let vmTemplateNameById = $derived.by(() => {
		return new Map(
			(simpleVMTemplates.current || []).map((template) => [template.id, template.name])
		);
	});

	function vmTemplateIdentityLabel(templateId: number): string {
		const name = vmTemplateNameById.get(templateId);
		return name ? `${name} (Template ID ${templateId})` : `Template ID ${templateId}`;
	}

	type DownloaderUploadAuditTarget = {
		action: 'Stage' | 'Complete' | 'Abort';
		uploadId?: string;
		downloadId?: number;
		name?: string;
		alreadyCompleted?: boolean;
	};

	function auditObject(value: unknown): Record<string, unknown> | undefined {
		if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
		return value as Record<string, unknown>;
	}

	function auditText(value: unknown): string {
		return typeof value === 'string' ? value.trim() : '';
	}

	function auditPositiveIDs(value: unknown): number[] {
		if (!Array.isArray(value)) return [];
		const ids: number[] = [];
		for (const item of value) {
			const id = Number(item);
			if (!Number.isSafeInteger(id) || id <= 0 || ids.includes(id)) continue;
			ids.push(id);
		}
		return ids;
	}

	function auditIDListLabel(ids: number[]): string {
		const visible = ids.slice(0, 6).join(', ');
		return ids.length > 6 ? `${visible}, +${ids.length - 6} more` : visible;
	}

	function downloaderUploadAuditTarget(
		path: string,
		method: string,
		responseValue: unknown
	): DownloaderUploadAuditTarget | null {
		const upperMethod = method.toUpperCase();
		const response = auditObject(responseValue);
		const data = auditObject(response?.data);

		if (upperMethod === 'POST' && path === '/api/utilities/downloader-uploads') {
			return {
				action: 'Stage',
				...(auditText(data?.uploadId) ? { uploadId: auditText(data?.uploadId) } : {}),
				...(auditText(data?.name) ? { name: auditText(data?.name) } : {})
			};
		}

		const completionMatch = path.match(/^\/api\/utilities\/downloader-uploads\/([^/]+)\/complete$/);
		if (upperMethod === 'POST' && completionMatch) {
			const downloadId = Number(data?.downloadId);
			return {
				action: 'Complete',
				uploadId: completionMatch[1],
				...(Number.isSafeInteger(downloadId) && downloadId > 0 ? { downloadId } : {})
			};
		}

		const abortMatch = path.match(/^\/api\/utilities\/downloader-uploads\/([^/]+)$/);
		if (upperMethod === 'DELETE' && abortMatch) {
			return {
				action: 'Abort',
				uploadId: abortMatch[1],
				alreadyCompleted: data?.status === 'completed'
			};
		}

		return null;
	}

	function downloaderUploadActionLabel(target: DownloaderUploadAuditTarget): string {
		let label = `Downloader Upload - ${target.action}`;
		if (target.name) label += ` - ${target.name}`;
		if (target.uploadId) label += ` - Upload ID ${target.uploadId}`;
		if (target.downloadId) label += ` - Download ID ${target.downloadId}`;
		if (target.alreadyCompleted) label += ' - Already Completed';
		return label;
	}

	type DownloaderAuditTarget = {
		action: 'Create' | 'Create Link' | 'Update' | 'Delete' | 'Bulk Delete';
		downloadId?: number;
		downloadIds?: number[];
		failedIds?: number[];
		name?: string;
		parentUUID?: string;
		downloadType?: string;
		category?: string;
	};

	function downloaderAuditTarget(
		path: string,
		method: string,
		bodyValue: unknown,
		responseValue: unknown
	): DownloaderAuditTarget | null {
		const upperMethod = method.toUpperCase();
		const body = auditObject(bodyValue);
		const response = auditObject(responseValue);
		const responseData = auditObject(response?.data);
		const deletedItems = Array.isArray(responseData?.deleted) ? responseData.deleted : [];
		const failedItems = Array.isArray(responseData?.failed) ? responseData.failed : [];

		if (upperMethod === 'POST' && path === '/api/utilities/downloads/signed-url') {
			return {
				action: 'Create Link',
				...(auditText(body?.name) ? { name: auditText(body?.name) } : {}),
				...(auditText(body?.parentUUID)
					? { parentUUID: auditText(body?.parentUUID) }
					: {})
			};
		}

		if (upperMethod === 'POST' && path === '/api/utilities/downloads') {
			const responseID = Number(responseData?.id);
			return {
				action: 'Create',
				...(Number.isSafeInteger(responseID) && responseID > 0 ? { downloadId: responseID } : {}),
				...(auditText(body?.filename) ? { name: auditText(body?.filename) } : {}),
				...(auditText(body?.downloadType) ? { category: auditText(body?.downloadType) } : {})
			};
		}

		if (upperMethod === 'POST' && path === '/api/utilities/downloads/bulk-delete') {
			const deletedIds = auditPositiveIDs(
				deletedItems.map((item) => auditObject(item)?.id)
			);
			const failedIds = auditPositiveIDs(failedItems.map((item) => auditObject(item)?.id));
			const requestedIds = auditPositiveIDs(body?.ids);
			return {
				action: 'Bulk Delete',
				downloadIds: deletedIds.length > 0 ? deletedIds : requestedIds,
				...(failedIds.length > 0 ? { failedIds } : {})
			};
		}

		const updateMatch = path.match(/^\/api\/utilities\/downloads\/(\d+)$/);
		if (upperMethod === 'PATCH' && updateMatch) {
			const pathID = Number(updateMatch[1]);
			return {
				action: 'Update',
				downloadId: pathID,
				...(auditText(responseData?.name) || auditText(body?.name)
					? { name: auditText(responseData?.name) || auditText(body?.name) }
					: {}),
				...(auditText(responseData?.type) ? { downloadType: auditText(responseData?.type) } : {}),
				...(auditText(responseData?.uType) || auditText(body?.uType)
					? { category: auditText(responseData?.uType) || auditText(body?.uType) }
					: {})
			};
		}

		if (upperMethod === 'DELETE' && updateMatch) {
			const pathID = Number(updateMatch[1]);
			const identity =
				deletedItems.map(auditObject).find((item) => Number(item?.id) === pathID) ||
				failedItems.map(auditObject).find((item) => Number(item?.id) === pathID);
			return {
				action: 'Delete',
				downloadId: pathID,
				...(auditText(identity?.name) ? { name: auditText(identity?.name) } : {}),
				...(auditText(identity?.type) ? { downloadType: auditText(identity?.type) } : {})
			};
		}

		return null;
	}

	function downloaderActionLabel(target: DownloaderAuditTarget): string {
		let label = `Downloader - ${target.action}`;
		if (target.name) label += ` - ${target.name}`;
		if (target.parentUUID) label += ` - Parent ${target.parentUUID}`;
		if (target.downloadId) label += ` - Download ID ${target.downloadId}`;
		if (target.downloadIds?.length)
			label += ` - Download IDs ${auditIDListLabel(target.downloadIds)}`;
		if (target.failedIds?.length) label += ` - Failed IDs ${auditIDListLabel(target.failedIds)}`;
		if (target.downloadType) label += ` - ${target.downloadType}`;
		if (target.category) label += ` - ${target.category}`;
		return label;
	}

	type CloudInitTemplateAuditTarget = {
		action: 'Create' | 'Replace' | 'Delete';
		templateId?: number;
		name?: string;
	};

	function cloudInitTemplateAuditTarget(
		path: string,
		method: string,
		bodyValue: unknown,
		responseValue: unknown
	): CloudInitTemplateAuditTarget | null {
		const basePath = '/api/utilities/cloud-init/templates';
		const upperMethod = method.toUpperCase();
		const memberMatch = path.match(/^\/api\/utilities\/cloud-init\/templates\/(\d+)$/);
		if (path !== basePath && !memberMatch) return null;

		const body = auditObject(bodyValue);
		const response = auditObject(responseValue);
		const responseData = auditObject(response?.data);
		const pathId = memberMatch ? Number(memberMatch[1]) : undefined;
		const responseId = Number(responseData?.id);
		const templateId =
			Number.isSafeInteger(responseId) && responseId > 0
				? responseId
				: Number.isSafeInteger(pathId) && Number(pathId) > 0
					? pathId
					: undefined;
		const name = auditText(responseData?.name) || auditText(body?.name) || undefined;

		if (upperMethod === 'POST' && path === basePath) {
			return { action: 'Create', ...(templateId ? { templateId } : {}), ...(name ? { name } : {}) };
		}
		if (upperMethod === 'PUT' && memberMatch) {
			return { action: 'Replace', ...(templateId ? { templateId } : {}), ...(name ? { name } : {}) };
		}
		if (upperMethod === 'DELETE' && memberMatch) {
			return { action: 'Delete', ...(templateId ? { templateId } : {}), ...(name ? { name } : {}) };
		}
		return null;
	}

	function cloudInitTemplateActionLabel(target: CloudInitTemplateAuditTarget): string {
		let label = `Cloud-Init Template - ${target.action}`;
		if (target.name) label += ` - ${target.name}`;
		if (target.templateId) label += ` - Template ID ${target.templateId}`;
		return label;
	}

	function normalizeActionPath(path: string): string {
		const segments = path.split('/');
		if (segments[1] === 'api' && segments[2] === 'disk' && segments.length === 5) {
			if (segments[3] === 'partitions') {
				segments[4] = ':partition';
			} else if (segments[4] === 'partition-table' || segments[4] === 'partitions') {
				segments[3] = ':device';
			}
		}
		return segments.map((segment) => (/^\d+$/.test(segment) ? ':id' : segment)).join('/');
	}

	function itemIDFromActionPath(path: string, normalizedPath: string): number | string | undefined {
		if (!normalizedPath.endsWith('/:id')) return undefined;

		const segment = path.split('/').filter(Boolean).at(-1);
		if (!segment || !/^\d+$/.test(segment)) return undefined;

		const numericID = Number(segment);
		return Number.isSafeInteger(numericID) ? numericID : segment;
	}

	let records = $derived.by(() => {
		if (!auditRecords.current) return [];

		return auditRecords.current.map((record) => {
			const recordCopy = $state.snapshot(record);
			const path = recordCopy.action?.path || '';
			const method = recordCopy.action?.method || '';

			let resolvedAction = method;

			const normalizedPath = normalizeActionPath(path);

			const methodPathAction = methodPathToActionMap[method.toUpperCase()]?.[normalizedPath];
			const matchedEntry = methodPathAction
				? undefined
				: sortedPathToActionEntries.find(([prefix]) => normalizedPath.startsWith(prefix));

			if (methodPathAction) {
				resolvedAction = methodPathAction;
			} else if (matchedEntry) {
				const label = matchedEntry[1];
				if (!label.includes('-')) {
					switch (method.toUpperCase()) {
						case 'GET':
							if (path.includes('vnc')) {
								const port = path.split('/').pop();
								const vm = simpleVmList.current?.find((vm) => vm.vncPort === Number(port));

								resolvedAction = `${label} - ${vm ? vm.name : 'Unknown VM'} (${port})`;
							} else {
								resolvedAction = `${label} - View`;
							}
							break;
						case 'POST':
							resolvedAction = `${label} - Create`;
							break;
						case 'PUT':
						case 'PATCH':
							resolvedAction = `${label} - Update`;
							break;
						case 'DELETE':
							resolvedAction = `${label} - Delete`;
							break;
						default:
							resolvedAction = label;
					}
				} else {
					resolvedAction = label;
				}
			}

			const downloaderUploadTarget = downloaderUploadAuditTarget(
				path,
				method,
				recordCopy.action.response
			);
			if (downloaderUploadTarget) {
				resolvedAction = downloaderUploadActionLabel(downloaderUploadTarget);
			}
			const downloaderTarget = downloaderAuditTarget(
				path,
				method,
				recordCopy.action.body,
				recordCopy.action.response
			);
			if (downloaderTarget) {
				resolvedAction = downloaderActionLabel(downloaderTarget);
			}
			const cloudInitTarget = cloudInitTemplateAuditTarget(
				path,
				method,
				recordCopy.action.body,
				recordCopy.action.response
			);
			if (cloudInitTarget) {
				resolvedAction = cloudInitTemplateActionLabel(cloudInitTarget);
			}

			if (
				method.toUpperCase() === 'POST' &&
				normalizedPath === '/api/system/file-explorer/copy-or-move-batch'
			) {
				const move = recordCopy.action.body?.move === true || recordCopy.action.body?.cut === true;
				resolvedAction = `File Explorer - Batch ${move ? 'Move' : 'Copy'}`;
			}

			if (method.toUpperCase() === 'DELETE') {
				const itemID = itemIDFromActionPath(path, normalizedPath);
				if (itemID !== undefined) {
					recordCopy.action.body = { id: itemID };
				}
			}

			if (method.toUpperCase() === 'PATCH') {
				const serviceStateMatch = path.match(/^\/api\/system\/basic-settings\/services\/([^/]+)$/);
				if (serviceStateMatch) {
					const service = serviceStateMatch[1] ?? 'Service';
					const label = basicSettingsServiceLabels.get(service) ?? service;
					if (recordCopy.action.body?.enabled === true) {
						resolvedAction = `${label} - Enable`;
					} else if (recordCopy.action.body?.enabled === false) {
						resolvedAction = `${label} - Disable`;
					} else {
						resolvedAction = `${label} - Update State`;
					}
				}
			}

			if (method.toUpperCase() === 'PATCH' && normalizedPath === '/api/network/wireguard/server') {
				if (recordCopy.action.body?.enabled === true) {
					resolvedAction = 'WireGuard Server - Enable';
				} else if (recordCopy.action.body?.enabled === false) {
					resolvedAction = 'WireGuard Server - Disable';
				}
			}

			if (
				method.toUpperCase() === 'PATCH' &&
				normalizedPath === '/api/network/wireguard/server/peer/:id'
			) {
				if (recordCopy.action.body?.enabled === true) {
					resolvedAction = 'WireGuard Peer - Enable';
				} else if (recordCopy.action.body?.enabled === false) {
					resolvedAction = 'WireGuard Peer - Disable';
				}
			}

			if (
				method.toUpperCase() === 'PATCH' &&
				normalizedPath === '/api/network/wireguard/clients/:id'
			) {
				if (recordCopy.action.body?.enabled === true) {
					resolvedAction = 'WireGuard Client - Enable';
				} else if (recordCopy.action.body?.enabled === false) {
					resolvedAction = 'WireGuard Client - Disable';
				}
			}

			if (resolvedAction === 'Login - Create') {
				resolvedAction = 'Login';
			}

			const vmSnapshotTarget = vmSnapshotAuditTarget(path, method);
			const vmStorageTarget = vmStorageAuditTarget(path, method, recordCopy.action.body);
			const vmNetworkTarget = vmNetworkAuditTarget(path, method, recordCopy.action.body);
			const vmOptionTarget = vmOptionAuditTarget(path, method);
			const vmHardwareTarget = vmHardwareAuditTarget(path, method);
			const vmConsoleRID = vmConsoleAuditRID(path, recordCopy.action?.query || '');
			const jailConsoleCTID = jailConsoleAuditCTID(path);
			const vmActionMatch = path.match(/^\/api\/vm\/(\d+)\/actions\/(start|stop|shutdown|reboot)$/);
			const vmMigrationMatch = path.match(/^\/api\/vm\/(\d+)\/migrations$/);
			const vmTemplateCaptureMatch = path.match(/^\/api\/vm\/(\d+)\/templates$/);
			const vmTemplateInstantiationMatch = path.match(/^\/api\/vm\/templates\/(\d+)\/vms$/);
			const vmCoreMatch = path.match(/^\/api\/vm\/(\d+)(?:\/(description|name|registration))?$/);
			const jailSnapshotTarget = jailSnapshotAuditTarget(path, method);
			const jailNetworkTarget = jailNetworkAuditTarget(path, method);
			const jailOptionTarget = jailOptionAuditTarget(path, method, recordCopy.action.body);
			const jailHardwareTarget = jailHardwareAuditTarget(path, method, recordCopy.action.body);
			const jailActionMatch = path.match(/^\/api\/jail\/(\d+)\/actions\/(start|stop|restart)$/);
			const jailMigrationMatch = path.match(/^\/api\/jail\/(\d+)\/migrations$/);
			const jailTemplateCaptureMatch = path.match(/^\/api\/jail\/(\d+)\/templates$/);
			const jailTemplateInstantiationMatch = path.match(/^\/api\/jail\/templates\/(\d+)\/jails$/);
			const jailTemplateMemberMatch = path.match(/^\/api\/jail\/templates\/(\d+)$/);
			const jailBootstrapMemberMatch = path.match(/^\/api\/jail\/bootstraps\/([^/]+)$/);
			const jailCoreMatch = path.match(/^\/api\/jail\/(\d+)(?:\/(description|name))?$/);
			if (vmOptionTarget) {
				resolvedAction = `VM Options - ${vmOptionTarget.option} - ${vmIdentityLabel(vmOptionTarget.rid)}`;
				recordCopy.action.body = {
					...(recordCopy.action.body || {}),
					...(recordCopy.action.body?.rid === undefined ? { rid: vmOptionTarget.rid } : {})
				};
			} else if (vmHardwareTarget) {
				resolvedAction = `VM Hardware - ${vmHardwareTarget.component} - ${vmIdentityLabel(vmHardwareTarget.rid)}`;
				recordCopy.action.body = {
					...(recordCopy.action.body || {}),
					...(recordCopy.action.body?.rid === undefined ? { rid: vmHardwareTarget.rid } : {})
				};
			} else if (vmNetworkTarget) {
				const identity =
					vmNetworkTarget.rid !== undefined
						? vmIdentityLabel(vmNetworkTarget.rid)
						: vmNetworkTarget.networkId !== undefined
							? `Network ID ${vmNetworkTarget.networkId}`
							: 'Unknown VM';
				resolvedAction = `VM Network - ${vmNetworkTarget.action} - ${identity}`;
				if (vmNetworkTarget.networkId !== undefined && vmNetworkTarget.rid !== undefined) {
					resolvedAction += ` - Network ID ${vmNetworkTarget.networkId}`;
				}
				if (vmNetworkTarget.action === 'Attach') {
					const switchName = recordCopy.action.body?.switchName;
					if (typeof switchName === 'string' && switchName.trim()) {
						resolvedAction += ` - ${switchName.trim()}`;
					}
				}
				recordCopy.action.body = {
					...(recordCopy.action.body || {}),
					...(vmNetworkTarget.rid !== undefined && recordCopy.action.body?.rid === undefined
						? { rid: vmNetworkTarget.rid }
						: {}),
					...(vmNetworkTarget.networkId !== undefined
						? { networkId: vmNetworkTarget.networkId }
						: {})
				};
			} else if (vmStorageTarget) {
				const identity =
					vmStorageTarget.rid !== undefined
						? vmIdentityLabel(vmStorageTarget.rid)
						: vmStorageTarget.storageId !== undefined
							? `Storage ID ${vmStorageTarget.storageId}`
							: 'Unknown VM';
				resolvedAction = `VM Storage - ${vmStorageTarget.action} - ${identity}`;
				if (vmStorageTarget.storageId !== undefined && vmStorageTarget.rid !== undefined) {
					resolvedAction += ` - Storage ID ${vmStorageTarget.storageId}`;
				}
				if (vmStorageTarget.action === 'Attach') {
					const storageName = recordCopy.action.body?.name;
					if (typeof storageName === 'string' && storageName.trim()) {
						resolvedAction += ` - ${storageName.trim()}`;
					}
				}
				recordCopy.action.body = {
					...(recordCopy.action.body || {}),
					...(vmStorageTarget.rid !== undefined && recordCopy.action.body?.rid === undefined
						? { rid: vmStorageTarget.rid }
						: {}),
					...(vmStorageTarget.storageId !== undefined
						? { storageId: vmStorageTarget.storageId }
						: {})
				};
			} else if (vmSnapshotTarget) {
				const identity = vmIdentityLabel(vmSnapshotTarget.rid);
				const snapshotIdentity =
					vmSnapshotTarget.snapshotId !== undefined
						? ' - Snapshot ID ' + vmSnapshotTarget.snapshotId
						: '';
				resolvedAction =
					'VM Snapshot - ' + vmSnapshotTarget.action + ' - ' + identity + snapshotIdentity;

				if (vmSnapshotTarget.action === 'Create') {
					const snapshotName = recordCopy.action.body?.name;
					if (typeof snapshotName === 'string' && snapshotName.trim()) {
						resolvedAction += ' - ' + snapshotName.trim();
					}
				}
				if (vmSnapshotTarget.snapshotId !== undefined) {
					recordCopy.action.body = {
						...(recordCopy.action.body || {}),
						snapshotId: vmSnapshotTarget.snapshotId
					};
				}
			} else if (method.toUpperCase() === 'GET' && vmConsoleRID !== null) {
				resolvedAction = `VM Console - Session - ${vmIdentityLabel(vmConsoleRID)}`;
				recordCopy.action.body = {
					...(recordCopy.action.body || {}),
					rid: vmConsoleRID
				};
			} else if (method.toUpperCase() === 'GET' && jailConsoleCTID !== null) {
				resolvedAction = `Jail Console - Session - ${jailIdentityLabel(jailConsoleCTID)}`;
				recordCopy.action.body = {
					...(recordCopy.action.body || {}),
					ctId: jailConsoleCTID
				};
			} else if (method.toUpperCase() === 'POST' && path === '/api/vm') {
				const bodyRID = Number(recordCopy.action.body?.rid);
				const bodyName = recordCopy.action.body?.name;
				if (Number.isSafeInteger(bodyRID) && bodyRID > 0) {
					resolvedAction = `VM - Create - ${vmIdentityLabel(bodyRID, bodyName)}`;
				} else if (typeof bodyName === 'string' && bodyName.trim()) {
					resolvedAction = `VM - Create - ${bodyName.trim()}`;
				}
			} else if (method.toUpperCase() === 'POST' && vmActionMatch) {
				const actionRID = Number(vmActionMatch[1]);
				const action = toTitleCase(vmActionMatch[2] || 'Action');
				resolvedAction = `VM - ${action} - ${vmIdentityLabel(actionRID)}`;
			} else if (method.toUpperCase() === 'POST' && vmMigrationMatch) {
				const migrationRID = Number(vmMigrationMatch[1]);
				resolvedAction = `VM - Migrate - ${vmIdentityLabel(migrationRID)}`;
			} else if (method.toUpperCase() === 'POST' && vmTemplateCaptureMatch) {
				const sourceRID = Number(vmTemplateCaptureMatch[1]);
				resolvedAction = `VM Template - Capture - ${vmIdentityLabel(sourceRID)}`;
			} else if (method.toUpperCase() === 'POST' && vmTemplateInstantiationMatch) {
				const templateId = Number(vmTemplateInstantiationMatch[1]);
				resolvedAction = `VM Template - Instantiate - ${vmTemplateIdentityLabel(templateId)}`;
			} else if (vmCoreMatch) {
				const coreRID = Number(vmCoreMatch[1]);
				const coreResource = vmCoreMatch[2] || '';
				const identity = vmIdentityLabel(coreRID);
				if (method.toUpperCase() === 'DELETE') {
					const params = new URLSearchParams(recordCopy.action?.query || '');
					const forceDelete = params.get('force')?.toLowerCase() === 'true';
					const legacyPurge = params.get('purgeOnly')?.toLowerCase() === 'true';
					resolvedAction =
						coreResource === 'registration' || legacyPurge
							? `VM - Purge Registration - ${identity}`
							: forceDelete
								? `VM - Force Delete - ${identity}`
								: `VM - Delete - ${identity}`;
				} else if (method.toUpperCase() === 'PATCH' && coreResource === 'description') {
					resolvedAction = `VM - Update Description - ${identity}`;
				} else if (method.toUpperCase() === 'PATCH' && coreResource === 'name') {
					resolvedAction = `VM - Update Name - ${identity}`;
				}
			} else if (
				method.toUpperCase() === 'PUT' &&
				(path === '/api/vm/description' || path === '/api/vm/name')
			) {
				const bodyRID = Number(recordCopy.action.body?.rid);
				if (Number.isSafeInteger(bodyRID) && bodyRID > 0) {
					resolvedAction += ` - ${vmIdentityLabel(bodyRID)}`;
				}
			} else if (jailOptionTarget) {
				const stateLabel =
					typeof jailOptionTarget.enabled === 'boolean'
						? ` - ${jailOptionTarget.enabled ? 'Enable' : 'Disable'}`
						: '';
				resolvedAction = `Jail Options - ${jailOptionTarget.option}${stateLabel} - ${jailIdentityLabel(jailOptionTarget.ctId)}`;
				recordCopy.action.body = {
					...(recordCopy.action.body || {}),
					ctId: jailOptionTarget.ctId
				};
			} else if (jailHardwareTarget) {
				const stateLabel =
					jailHardwareTarget.component === 'Resource Limits' &&
					typeof jailHardwareTarget.enabled === 'boolean'
						? ` - ${jailHardwareTarget.enabled ? 'Enable' : 'Disable'}`
						: '';
				resolvedAction = `Jail Hardware - ${jailHardwareTarget.component}${stateLabel} - ${jailIdentityLabel(jailHardwareTarget.ctId)}`;
				recordCopy.action.body = {
					...(recordCopy.action.body || {}),
					ctId: jailHardwareTarget.ctId,
					...(typeof jailHardwareTarget.enabled === 'boolean'
						? { enabled: jailHardwareTarget.enabled }
						: {})
				};
			} else if (jailNetworkTarget) {
				const identity = jailIdentityLabel(jailNetworkTarget.ctId);
				resolvedAction = `Jail Network - ${jailNetworkTarget.action} - ${identity}`;
				if (jailNetworkTarget.networkId !== undefined) {
					resolvedAction += ` - Network ID ${jailNetworkTarget.networkId}`;
				}
				if (jailNetworkTarget.action === 'Create' || jailNetworkTarget.action === 'Update') {
					const networkName = recordCopy.action.body?.name;
					if (typeof networkName === 'string' && networkName.trim()) {
						resolvedAction += ` - ${networkName.trim()}`;
					}
				} else if (jailNetworkTarget.action === 'Update Inheritance') {
					const ipv4 = recordCopy.action.body?.ipv4 === true;
					const ipv6 = recordCopy.action.body?.ipv6 === true;
					const mode = ipv4 && ipv6 ? 'IPv4 + IPv6' : ipv4 ? 'IPv4' : ipv6 ? 'IPv6' : 'Disabled';
					resolvedAction += ` - ${mode}`;
				}
				const networkBody = { ...(recordCopy.action.body || {}) };
				delete networkBody.id;
				recordCopy.action.body = {
					...networkBody,
					ctId: jailNetworkTarget.ctId,
					...(jailNetworkTarget.networkId !== undefined
						? { networkId: jailNetworkTarget.networkId }
						: {})
				};
			} else if (jailSnapshotTarget) {
				const identity = jailIdentityLabel(jailSnapshotTarget.ctId);
				const snapshotIdentity =
					jailSnapshotTarget.snapshotId !== undefined
						? ` - Snapshot ID ${jailSnapshotTarget.snapshotId}`
						: '';
				resolvedAction = `Jail Snapshot - ${jailSnapshotTarget.action} - ${identity}${snapshotIdentity}`;

				if (jailSnapshotTarget.action === 'Create') {
					const snapshotName = recordCopy.action.body?.name;
					if (typeof snapshotName === 'string' && snapshotName.trim()) {
						resolvedAction += ` - ${snapshotName.trim()}`;
					}
				}

				const snapshotBody = { ...(recordCopy.action.body || {}) };
				delete snapshotBody.id;
				recordCopy.action.body = {
					...snapshotBody,
					ctId: jailSnapshotTarget.ctId,
					...(jailSnapshotTarget.snapshotId !== undefined
						? { snapshotId: jailSnapshotTarget.snapshotId }
						: {})
				};
			} else if (method.toUpperCase() === 'POST' && path === '/api/jail/bootstraps') {
				const pool = recordCopy.action.body?.pool;
				const bootstrapName = bootstrapNameFromBody(recordCopy.action.body);
				const identity = bootstrapIdentityLabel(pool, bootstrapName);
				resolvedAction = `Jail Bootstrap - Create${identity ? ` - ${identity}` : ''}`;
				recordCopy.action.body = {
					...(recordCopy.action.body || {}),
					...(bootstrapName ? { bootstrapName } : {})
				};
			} else if (method.toUpperCase() === 'DELETE' && jailBootstrapMemberMatch) {
				const params = new URLSearchParams(recordCopy.action?.query || '');
				const pool = params.get('pool') || '';
				let bootstrapName = jailBootstrapMemberMatch[1] || '';
				try {
					bootstrapName = decodeURIComponent(bootstrapName);
				} catch {
					// Keep the recorded path segment if it was not valid URI encoding.
				}
				const identity = bootstrapIdentityLabel(pool, bootstrapName);
				resolvedAction = `Jail Bootstrap - Delete${identity ? ` - ${identity}` : ''}`;
				recordCopy.action.body = {
					...(recordCopy.action.body || {}),
					pool,
					bootstrapName
				};
			} else if (method.toUpperCase() === 'POST' && path === '/api/jail') {
				const bodyCTID = Number(recordCopy.action.body?.ctId);
				const bodyName = recordCopy.action.body?.name;
				if (Number.isSafeInteger(bodyCTID) && bodyCTID > 0) {
					resolvedAction = `Jail - Create - ${jailIdentityLabel(bodyCTID, bodyName)}`;
				} else if (typeof bodyName === 'string' && bodyName.trim()) {
					resolvedAction = `Jail - Create - ${bodyName.trim()}`;
				}
			} else if (method.toUpperCase() === 'POST' && jailActionMatch) {
				const actionCTID = Number(jailActionMatch[1]);
				const action = toTitleCase(jailActionMatch[2] || 'Action');
				resolvedAction = `Jail - ${action} - ${jailIdentityLabel(actionCTID)}`;
				recordCopy.action.body = {
					...(recordCopy.action.body || {}),
					ctId: actionCTID
				};
			} else if (method.toUpperCase() === 'POST' && jailMigrationMatch) {
				const migrationCTID = Number(jailMigrationMatch[1]);
				resolvedAction = `Jail - Migrate - ${jailIdentityLabel(migrationCTID)}`;
				recordCopy.action.body = {
					...(recordCopy.action.body || {}),
					ctId: migrationCTID
				};
			} else if (method.toUpperCase() === 'POST' && jailTemplateCaptureMatch) {
				const sourceCTID = Number(jailTemplateCaptureMatch[1]);
				resolvedAction = `Jail Template - Capture - ${jailIdentityLabel(sourceCTID)}`;
				recordCopy.action.body = {
					...(recordCopy.action.body || {}),
					ctId: sourceCTID
				};
			} else if (method.toUpperCase() === 'POST' && jailTemplateInstantiationMatch) {
				const templateId = Number(jailTemplateInstantiationMatch[1]);
				resolvedAction = `Jail Template - Instantiate - ${jailTemplateIdentityLabel(templateId)}`;
				recordCopy.action.body = {
					...(recordCopy.action.body || {}),
					templateId
				};
			} else if (jailTemplateMemberMatch) {
				const templateId = Number(jailTemplateMemberMatch[1]);
				resolvedAction += ` - ${jailTemplateIdentityLabel(templateId)}`;
				recordCopy.action.body = { templateId };
			} else if (jailCoreMatch) {
				const coreCTID = Number(jailCoreMatch[1]);
				const coreResource = jailCoreMatch[2] || '';
				const identity = jailIdentityLabel(
					coreCTID,
					coreResource === 'name' ? recordCopy.action.body?.name : undefined
				);
				if (method.toUpperCase() === 'DELETE') {
					resolvedAction = `Jail - Delete - ${identity}`;
					recordCopy.action.body = { ctId: coreCTID };
				} else if (method.toUpperCase() === 'PATCH' && coreResource === 'description') {
					resolvedAction = `Jail - Update Description - ${identity}`;
					recordCopy.action.body = {
						...(recordCopy.action.body || {}),
						ctId: coreCTID
					};
				} else if (method.toUpperCase() === 'PATCH' && coreResource === 'name') {
					resolvedAction = `Jail - Update Name - ${identity}`;
					recordCopy.action.body = {
						...(recordCopy.action.body || {}),
						ctId: coreCTID
					};
				} else if (method.toUpperCase() === 'GET' && coreResource === '') {
					resolvedAction += ` - ${identity}`;
				}
			} else if (path.startsWith('/api/vm/') && !path.startsWith('/api/vm/templates/')) {
				const last = path.split('/').pop() || '';
				const rid = Number(last);
				if (Number.isFinite(rid) && rid > 0) {
					resolvedAction += ` - ${vmIdentityLabel(rid)}`;
				}
			} else if (path.startsWith('/api/vm/templates/convert/')) {
				const last = path.split('/').pop() || '';
				const rid = Number(last);
				if (Number.isFinite(rid) && rid > 0) {
					resolvedAction += ` - ${vmIdentityLabel(rid)}`;
				}
			} else if (path.startsWith('/api/vm/templates/create/')) {
				const last = path.split('/').pop() || '';
				const templateId = Number(last);
				if (Number.isFinite(templateId) && templateId > 0) {
					resolvedAction += ` - ${vmTemplateIdentityLabel(templateId)}`;
				}
			} else if (path.startsWith('/api/vm/templates/')) {
				const last = path.split('/').pop() || '';
				const templateId = Number(last);
				if (Number.isFinite(templateId) && templateId > 0) {
					resolvedAction += ` - ${vmTemplateIdentityLabel(templateId)}`;
				}
			}

			return {
				...recordCopy,
				resolvedAction
			};
		});
	});

	let activeLifecycleCount = $derived.by(() => {
		if (!activeLifecycleTasks.current) return 0;
		if (Array.isArray(activeLifecycleTasks.current)) {
			return activeLifecycleTasks.current.length;
		}

		return 0;
	});

	let lifecycleActive = $derived(activeLifecycleCount > 0);

	watch(
		() => lifecycleActive,
		(active) => {
			onLifecycleActiveChange?.(active);
		}
	);

	function toTitleCase(value: string): string {
		return value
			.trim()
			.split(/\s+/)
			.filter(Boolean)
			.map((word) => word.charAt(0).toUpperCase() + word.slice(1))
			.join(' ');
	}

	function lifecycleActionLabel(action: string): string {
		return toTitleCase(action.replace(/[_-]+/g, ' ')) || 'Working';
	}

	function lifecycleStatusLabel(status: LifecycleTask['status']): string {
		switch (status) {
			case 'queued':
				return 'Queued';
			case 'running':
				return 'Running';
			case 'success':
				return 'Success';
			case 'failed':
				return 'Failed';
			default:
				return toTitleCase(status);
		}
	}

	function lifecycleGuestLabel(task: LifecycleTask): string {
		if (task.guestType === 'vm') {
			const name = vmNameById.get(task.guestId);
			return name ? `VM ${name} (${task.guestId})` : `VM ${task.guestId}`;
		}

		if (task.guestType === 'jail-template') {
			const templateName = templateNameById.get(task.guestId);
			return templateName
				? `Template ${templateName} (${task.guestId})`
				: `Jail Template ${task.guestId}`;
		}

		if (task.guestType === 'vm-template') {
			const templateName = vmTemplateNameById.get(task.guestId);
			return templateName
				? `Template ${templateName} (${task.guestId})`
				: `VM Template ${task.guestId}`;
		}

		const jailName = jailNameByCtId.get(task.guestId);
		return jailName ? `Jail ${jailName} (${task.guestId})` : `Jail ${task.guestId}`;
	}

	function lifecycleTaskLabel(task: LifecycleTask): string {
		if (task.action === 'migrate') {
			if (task.guestType === 'vm') {
				const name = vmNameById.get(task.guestId);
				return name
					? `Migrate VM - ${name} (RID ${task.guestId})`
					: `Migrate VM - RID ${task.guestId}`;
			}
			if (task.guestType === 'jail') {
				const name = jailNameByCtId.get(task.guestId);
				return name
					? `Migrate Jail - ${name} (CTID ${task.guestId})`
					: `Migrate Jail - CTID ${task.guestId}`;
			}
		}

		if (task.guestType === 'jail-template' && task.action === 'create') {
			const templateName = templateNameById.get(task.guestId);
			return templateName
				? `Create Jail - Template ${templateName} (Template ID ${task.guestId})`
				: `Create Jail - Template ID ${task.guestId}`;
		}

		if (task.guestType === 'jail-template' && task.action === 'convert') {
			const jailName = jailNameByCtId.get(task.guestId);
			return jailName
				? `Create Jail Template - ${jailName} (Jail CTID ${task.guestId})`
				: `Create Jail Template - Jail CTID ${task.guestId}`;
		}

		if (task.guestType === 'vm-template' && task.action === 'create') {
			const templateName = vmTemplateNameById.get(task.guestId);
			return templateName
				? `Create VM - Template ${templateName}`
				: `Create VM - Template ${task.guestId}`;
		}

		if (task.guestType === 'vm-template' && task.action === 'convert') {
			const vmName = vmNameById.get(task.guestId);
			return vmName
				? `Create VM Template - ${vmName} (VM RID ${task.guestId})`
				: `Create VM Template - VM RID ${task.guestId}`;
		}

		return `${lifecycleActionLabel(task.action)} - ${lifecycleGuestLabel(task)}`;
	}

	let auditDetailModal = $state<{
		open: boolean;
		record: ResolvedAuditRecord | null;
		section: AuditDetailSection;
	}>({
		open: false,
		record: null,
		section: 'response'
	});

	function openAuditDetails(record: ResolvedAuditRecord, section: AuditDetailSection) {
		auditDetailModal.record = $state.snapshot(record);
		auditDetailModal.section = section;
		auditDetailModal.open = true;
	}

	export function formatStatus(status: string): string {
		switch (status) {
			case 'started':
				return 'Started';
			case 'success':
				return 'OK';
			case 'client_error':
				return 'Bad Request';
			case 'server_error':
				return 'Error';
			case 'pending':
				return 'In Progress';
			case 'failed':
				return 'Failed';
			default:
				return status;
		}
	}
</script>

<Tabs.Root value="cluster" class="flex h-full w-full flex-col">
	<Tabs.Content value="cluster" class="flex h-full flex-col border-x border-b">
		<div class="relative flex h-full flex-col" transition:fade|global={{ duration: 400 }}>
			{#if activeLifecycleCount > 0}
				<div class="bg-muted/35 border-b px-3 py-1.5 text-xs">
					<div class="flex items-center gap-2 overflow-x-auto whitespace-nowrap">
						<span class="inline-flex items-center gap-1 font-medium">
							<span class="icon-[mdi--loading] h-3.5 w-3.5 animate-spin"></span>
							{activeLifecycleCount}
							active lifecycle task{activeLifecycleCount === 1 ? '' : 's'}
						</span>

						{#if !isAPIResponse(activeLifecycleTasks.current) && Array.isArray(activeLifecycleTasks.current)}
							{#each activeLifecycleTasks.current as task (task.id)}
								<span class="bg-background rounded border px-2 py-0.5">
									{lifecycleTaskLabel(task)} ({lifecycleStatusLabel(task.status)})
								</span>
							{/each}
						{/if}
					</div>
				</div>
			{/if}

			<div class="flex-1 min-h-0 overflow-auto" style="overflow-anchor: none">
				<Table.Root class="w-full table-auto border-collapse">
					<Table.Header class="bg-background sticky top-0 z-10">
						<Table.Row class="dark:hover:bg-background ">
							<Table.Head class="h-10 px-4 py-2 font-semibold text-black dark:text-white"
								>Start Time</Table.Head
							>
							<Table.Head class="h-10 px-4 py-2 font-semibold text-black dark:text-white"
								>End Time</Table.Head
							>
							<Table.Head class="h-10 px-4 py-2 font-semibold text-black dark:text-white">
								{#if clustered && hostnameOptions.length > 0}
									<div class="w-44 max-w-full">
										<SimpleSelect
											placeholder="Node"
											options={hostnameOptions}
											value={effectiveHostname}
											onChange={(value: string) => {
												selectedHostname = value;
											}}
											classes={{
												parent: 'min-w-0 space-y-0',
												trigger:
													'inline-flex h-6 w-full items-center overflow-hidden rounded-sm border-0 bg-transparent px-1.5 text-left text-xs font-medium text-muted-foreground shadow-none ring-0 hover:bg-muted/40 focus:bg-muted/50'
											}}
										/>
									</div>
								{:else}
									Node
								{/if}
							</Table.Head>
							<Table.Head class="h-10 px-4 py-2 font-semibold text-black dark:text-white"
								>User</Table.Head
							>
							<Table.Head class="h-10 px-4 py-2 font-semibold text-black dark:text-white"
								>Action</Table.Head
							>
							<Table.Head class="h-10 px-4 py-2 font-semibold text-black dark:text-white"
								>Status</Table.Head
							>
						</Table.Row>
					</Table.Header>

					<Table.Body class="pb-32">
						{#each records as record, i (i)}
							<Table.Row>
								<Table.Cell class="text-wrap px-4 py-2">{convertDbTime(record.started)}</Table.Cell>
								<Table.Cell class="text-wrap px-4 py-2">{convertDbTime(record.ended)}</Table.Cell>
								<Table.Cell class="text-wrap px-4 py-2">{record.node}</Table.Cell>
								<Table.Cell class="text-wrap px-4 py-2"
									>{`${record.user}@${record.authType || 'cluster'}`}</Table.Cell
								>
								<Table.Cell class="p-0">
									<button
										type="button"
										class="hover:bg-muted/40 focus-visible:ring-ring flex min-h-10 w-full items-center px-4 py-2 text-left text-wrap transition-colors focus-visible:ring-2 focus-visible:ring-inset focus-visible:outline-none"
										aria-label={`View request details for ${record.resolvedAction}`}
										onclick={() => openAuditDetails(record, 'request')}
									>
										{record.resolvedAction}
									</button>
								</Table.Cell>
								<Table.Cell class="p-0">
									<button
										type="button"
										class="hover:bg-muted/40 focus-visible:ring-ring flex min-h-10 w-full items-center px-4 py-2 text-left text-wrap transition-colors focus-visible:ring-2 focus-visible:ring-inset focus-visible:outline-none"
										aria-label={`View response details for ${record.resolvedAction}`}
										onclick={() => openAuditDetails(record, 'response')}
									>
										<div class="flex items-center gap-1">
											{#if record.status === 'pending'}
												<span
													class="icon-[mdi--loading] h-3.5 w-3.5 animate-spin text-muted-foreground"
												></span>
											{:else if record.status === 'failed'}
												<span class="icon-[mdi--alert-circle] h-3.5 w-3.5 text-destructive"></span>
											{/if}
											<span class={record.status === 'failed' ? 'text-destructive' : ''}>
												{formatStatus(record.status)}
											</span>
										</div>
									</button>
								</Table.Cell>
							</Table.Row>
						{/each}
					</Table.Body>
				</Table.Root>
			</div>
		</div>
	</Tabs.Content>
</Tabs.Root>

<AuditDetailModal
	bind:open={auditDetailModal.open}
	record={auditDetailModal.record}
	initialSection={auditDetailModal.section}
/>

<style>
	:global([data-slot='table-container']) {
		overflow: visible;
	}
</style>
