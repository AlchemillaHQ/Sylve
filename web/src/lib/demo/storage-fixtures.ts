/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { Group, Passkey, User } from '$lib/types/auth';
import type { Disk, SmartSelfTestSchedule } from '$lib/types/disk/disk';
import type { ISCSIInitiator } from '$lib/types/iscsi/initiator';
import type { ISCSITarget } from '$lib/types/iscsi/target';
import type { SambaConfig } from '$lib/types/samba/config';
import type { SambaShare } from '$lib/types/samba/shares';
import type { AvailableService } from '$lib/types/system/settings';

type DemoStorageRequestConfig = {
	url: string;
	method?: string;
	headers?: Record<string, string>;
	data?: unknown;
};

export type DemoStorageResponse<T = unknown> = {
	status: number;
	data: T;
	headers: Record<string, string>;
	ok: boolean;
};

type DatasetType = 'FILESYSTEM' | 'VOLUME' | 'SNAPSHOT';

type DemoDataset = {
	name: string;
	guid: string;
	used: number;
	pool: string;
	available: number;
	mountpoint: string;
	type: DatasetType;
	referenced: number;
	properties: Record<string, string>;
};

type DemoPeriodicSnapshot = {
	id: number;
	guid: string;
	prefix: string;
	recursive: boolean;
	interval: number;
	cronExpr: string;
	pool: string;
	keepLast?: number;
	maxAgeDays?: number;
	keepHourly?: number;
	keepDaily?: number;
	keepWeekly?: number;
	keepMonthly?: number;
	keepYearly?: number;
	createdAt: string;
	lastRunAt: string;
	lastExecutedAt: string | null;
};

type DemoPoolProperty = {
	value: string;
	source: { type: string; data: string };
};

type DemoPoolVdev = {
	name: string;
	vdev_type: string;
	guid: string;
	path?: string;
	phys_path?: string | null;
	class: string;
	state: string;
	size: number;
	free: number;
	allocated: number;
	fragmentation?: number;
	properties?: Record<string, DemoPoolProperty> | null;
	vdevs?: Record<string, DemoPoolVdev> | null;
};

type DemoPool = {
	name: string;
	type: string;
	state: string;
	size: number;
	free: number;
	allocated: number;
	fragmentation: number;
	dedup_ratio: number;
	pool_guid: string;
	txg: string;
	spa_version: string;
	zpl_version: string;
	properties: Record<string, DemoPoolProperty>;
	vdevs: Record<string, DemoPoolVdev>;
	spares: Record<string, DemoPoolVdev> | null;
	logs: Record<string, DemoPoolVdev> | null;
	l2cache: Record<string, DemoPoolVdev> | null;
	special: Record<string, DemoPoolVdev> | null;
	dedup: Record<string, DemoPoolVdev> | null;
};

type DemoFileEntry = {
	id: string;
	date: string;
	type: 'folder' | 'file';
	lazy?: boolean;
	size?: number;
};

type DemoSambaAuditRow = {
	id: number;
	share: string;
	action: string;
	path: string;
	target: string;
	occurrences?: number;
	createdAt: string;
};

type DemoStorageState = {
	services: AvailableService[];
	usablePools: string[];
	disks: Disk[];
	smartTests: Record<string, Record<string, unknown>>;
	smartSchedules: SmartSelfTestSchedule[];
	pools: DemoPool[];
	datasets: DemoDataset[];
	periodicSnapshots: DemoPeriodicSnapshot[];
	scrubbingPools: Set<string>;
	files: Record<string, DemoFileEntry>;
	sambaConfig: SambaConfig;
	sambaShares: SambaShare[];
	sambaAuditRows: DemoSambaAuditRow[];
	users: User[];
	groups: Group[];
	passkeys: Record<number, Passkey[]>;
	pendingPasskeyUser: Record<string, number>;
	iscsiTargets: ISCSITarget[];
	targetSessions: Record<string, number>;
	iscsiInitiators: ISCSIInitiator[];
	iscsiStatus: Record<string, string>;
};

const GIB = 1024 ** 3;
const TIB = 1024 ** 4;
const createdAt = '2026-04-18T08:30:00.000Z';
const updatedAt = '2026-08-14T11:45:00.000Z';
const storageStates = new Map<string, DemoStorageState>();
const defaultServices: AvailableService[] = [
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

function success<T>(
	data: T,
	message = 'demo_fixture_loaded',
	status = 200
): DemoStorageResponse<{
	status: 'success';
	message: string;
	error: '';
	data: T;
}> {
	return {
		status,
		data: { status: 'success', message, error: '', data },
		headers: { 'content-type': 'application/json' },
		ok: true
	};
}

function mutationSuccess(message: string, status = 200): DemoStorageResponse {
	return success(null, message, status);
}

function failure(message: string, error: string, status = 404): DemoStorageResponse {
	return {
		status,
		data: { status: 'error', message, error },
		headers: { 'content-type': 'application/json' },
		ok: false
	};
}

function payload(config: DemoStorageRequestConfig): Record<string, unknown> {
	return typeof config.data === 'object' && config.data !== null
		? (config.data as Record<string, unknown>)
		: {};
}

function stringValue(body: Record<string, unknown>, key: string, fallback = ''): string {
	return typeof body[key] === 'string' ? body[key] : fallback;
}

function numberValue(body: Record<string, unknown>, key: string, fallback = 0): number {
	const value = Number(body[key]);
	return Number.isFinite(value) ? value : fallback;
}

function byteSizeValue(value: unknown, fallback: number): number {
	if (typeof value === 'number' && Number.isFinite(value)) return value;
	if (typeof value !== 'string') return fallback;
	const match = value.trim().match(/^(\d+(?:\.\d+)?)\s*([kmgtpe]?)(?:i?b)?$/i);
	if (!match) return fallback;
	const amount = Number(match[1]);
	const power = ['', 'k', 'm', 'g', 't', 'p', 'e'].indexOf(match[2].toLowerCase());
	return power >= 0 ? amount * 1024 ** power : fallback;
}

function booleanValue(body: Record<string, unknown>, key: string, fallback = false): boolean {
	return typeof body[key] === 'boolean' ? body[key] : fallback;
}

function stringArray(body: Record<string, unknown>, key: string): string[] {
	return Array.isArray(body[key])
		? (body[key] as unknown[]).filter((value): value is string => typeof value === 'string')
		: [];
}

function numberArray(value: unknown): number[] {
	return Array.isArray(value)
		? value.map(Number).filter((item) => Number.isFinite(item) && item > 0)
		: [];
}

function syncAuthRelationships(state: DemoStorageState): void {
	for (const user of state.users) {
		user.groups = state.groups
			.filter((group) => group.users?.some((member) => member.id === user.id))
			.map(({ id, name, notes }) => ({ id, name, notes }));
	}
	for (const group of state.groups) {
		const memberIDs = new Set(group.users?.map((user) => user.id) ?? []);
		group.users = state.users.filter((user) => memberIDs.has(user.id));
	}
}

function userFromPayload(
	id: number,
	body: Record<string, unknown>,
	source: User['source'],
	previous?: User
): User {
	const now = new Date().toISOString();
	return {
		id,
		username: stringValue(body, 'username', previous?.username ?? `user${id}`),
		fullName: stringValue(body, 'fullName', previous?.fullName ?? ''),
		email: stringValue(body, 'email', previous?.email ?? ''),
		notes: stringValue(body, 'notes', previous?.notes ?? ''),
		admin: booleanValue(body, 'admin', previous?.admin ?? false),
		uid: Math.trunc(numberValue(body, 'uid', previous?.uid ?? 1000 + id)),
		shell: stringValue(body, 'shell', previous?.shell ?? '/usr/sbin/nologin'),
		homeDirectory: stringValue(body, 'homeDirectory', previous?.homeDirectory ?? '/nonexistent'),
		homeDirPerms: Math.trunc(numberValue(body, 'homeDirPerms', previous?.homeDirPerms ?? 493)),
		sshPublicKey: stringValue(body, 'sshPublicKey', previous?.sshPublicKey ?? ''),
		disablePassword: booleanValue(body, 'disablePassword', previous?.disablePassword ?? false),
		locked: booleanValue(body, 'locked', previous?.locked ?? false),
		doasEnabled: booleanValue(body, 'doasEnabled', previous?.doasEnabled ?? false),
		primaryGroupId:
			body.primaryGroupId === null
				? null
				: Math.trunc(numberValue(body, 'primaryGroupId', previous?.primaryGroupId ?? 0)) || null,
		source,
		passkeyEligible: source === 'local' && booleanValue(body, 'admin', previous?.admin ?? false),
		createdAt: previous?.createdAt ?? now,
		updatedAt: now,
		lastLoginTime: previous?.lastLoginTime ?? '',
		groups: previous?.groups ?? []
	};
}

function applyUserGroups(state: DemoStorageState, user: User, body: Record<string, unknown>): void {
	if (!Array.isArray(body.auxGroupIds) && body.primaryGroupId === undefined) return;
	const selected = new Set(numberArray(body.auxGroupIds));
	if (user.primaryGroupId) selected.add(user.primaryGroupId);
	for (const group of state.groups) {
		const members = group.users ?? [];
		group.users = selected.has(group.id)
			? [...members.filter((member) => member.id !== user.id), user]
			: members.filter((member) => member.id !== user.id);
	}
	syncAuthRelationships(state);
}

function nextID(items: Array<{ id: number }>): number {
	return Math.max(0, ...items.map((item) => item.id)) + 1;
}

function nextGuid(prefix: string, items: Array<{ guid: string }>): string {
	return `${prefix}-${items.length + 1}-${Date.now().toString(36)}`;
}

function diskDeviceInfo(device: string, protocol: string) {
	return { name: device, info_name: `/dev/${device}`, type: 'disk', protocol };
}

function ataSmart(device: string, temperature: number, hours: number) {
	return {
		device: diskDeviceInfo(device, 'ATA'),
		passed: true,
		health_known: true,
		checksum_valid: true,
		power_on_hours: hours,
		power_cycle_count: 38,
		temperature,
		smart_capability: 3,
		attributes: [
			{
				page: 0,
				id: 5,
				name: 'Reallocated_Sector_Ct',
				value: 100,
				worst: 100,
				thresh: 10,
				raw_value: 0,
				raw_string: '0',
				state: 0,
				when_failed: '',
				pre_failure: true,
				online: true,
				performance: false,
				error_rate: false,
				event_count: false,
				auto_keep: true
			},
			{
				page: 0,
				id: 194,
				name: 'Temperature_Celsius',
				value: temperature,
				worst: temperature,
				thresh: 0,
				raw_value: temperature,
				raw_string: String(temperature),
				state: 0,
				when_failed: '',
				pre_failure: false,
				online: true,
				performance: false,
				error_rate: false,
				event_count: false,
				auto_keep: true
			}
		]
	};
}

function nvmeSmart(device: string, temperature: number, hours: number, percentageUsed: number) {
	return {
		device: diskDeviceInfo(device, 'NVMe'),
		passed: true,
		health_known: true,
		power_on_hours: hours,
		power_on_hours_exact: String(hours),
		power_cycle_count: 24,
		power_cycle_count_exact: '24',
		temperature,
		criticalWarning: '0x00',
		criticalWarningState: {
			availableSpare: 0,
			temperature: 0,
			deviceReliability: 0,
			readOnly: 0,
			volatileMemoryBackup: 0
		},
		availableSpare: 100,
		availableSpareThreshold: 10,
		percentageUsed,
		dataUnitsRead: 35_812_452,
		dataUnitsReadExact: '35,812,452',
		dataUnitsWritten: 19_384_102,
		dataUnitsWrittenExact: '19,384,102',
		hostReadCommands: 982_224_187,
		hostReadCommandsExact: '982,224,187',
		hostWriteCommands: 442_981_001,
		hostWriteCommandsExact: '442,981,001',
		controllerBusyTime: 481,
		controllerBusyTimeExact: '481',
		unsafeShutdowns: 1,
		unsafeShutdownsExact: '1',
		mediaErrors: 0,
		mediaErrorsExact: '0',
		errorInfoLogEntries: 0,
		errorInfoLogEntriesExact: '0',
		warningCompositeTempTime: 0,
		errorCompositeTempTime: 0,
		temperature1TransitionCnt: 0,
		temperature2TransitionCnt: 0,
		totalTimeForTemperature1: 0,
		totalTimeForTemperature2: 0
	};
}

function createDisks(hostname: string): Disk[] {
	const serialPrefix = hostname.toUpperCase().slice(0, 3);
	return [
		{
			uuid: `${hostname}-nvme0`,
			identityStable: true,
			device: 'nvme0',
			type: 'NVMe',
			usage: 'ZFS',
			size: 2 * TIB,
			model: 'Samsung PM9A3 1.92TB',
			serial: `${serialPrefix}NVME0001`,
			gpt: true,
			smartData: nvmeSmart('nvme0', 38, 8_412, 7),
			wearOut: '93%',
			partitions: [
				{
					uuid: `${hostname}-nvme0p1`,
					name: 'nvme0p1',
					usage: 'ZFS',
					size: 1.86 * TIB
				}
			]
		},
		{
			uuid: `${hostname}-nvme1`,
			identityStable: true,
			device: 'nvme1',
			type: 'NVMe',
			usage: 'ZFS',
			size: 2 * TIB,
			model: 'Samsung PM9A3 1.92TB',
			serial: `${serialPrefix}NVME0002`,
			gpt: true,
			smartData: nvmeSmart('nvme1', 40, 8_397, 8),
			wearOut: '92%',
			partitions: [
				{
					uuid: `${hostname}-nvme1p1`,
					name: 'nvme1p1',
					usage: 'ZFS',
					size: 1.86 * TIB
				}
			]
		},
		{
			uuid: `${hostname}-ada0`,
			identityStable: true,
			device: 'ada0',
			type: 'HDD',
			usage: 'ZFS',
			size: 4 * TIB,
			model: 'Seagate Exos X18',
			serial: `${serialPrefix}EXOS0001`,
			gpt: true,
			smartData: ataSmart('ada0', 34, 12_804),
			wearOut: 'Healthy',
			partitions: [
				{
					uuid: `${hostname}-ada0p1`,
					name: 'ada0p1',
					usage: 'ZFS',
					size: 3.64 * TIB
				}
			]
		},
		{
			uuid: `${hostname}-ada1`,
			identityStable: true,
			device: 'ada1',
			type: 'HDD',
			usage: 'Unused',
			size: 4 * TIB,
			model: 'Seagate Exos X18',
			serial: `${serialPrefix}EXOS0002`,
			gpt: false,
			smartData: ataSmart('ada1', 31, 2_240),
			wearOut: 'Healthy',
			partitions: []
		},
		{
			uuid: `${hostname}-nda0`,
			identityStable: true,
			device: 'nda0',
			type: 'SSD',
			usage: 'Partitions',
			size: 960 * GIB,
			model: 'Micron 5400 PRO',
			serial: `${serialPrefix}MICRON01`,
			gpt: true,
			smartData: ataSmart('nda0', 36, 4_820),
			wearOut: '96%',
			partitions: [
				{
					uuid: `${hostname}-nda0p1`,
					name: 'nda0p1',
					usage: 'FreeBSD UFS',
					size: 200 * GIB
				}
			]
		},
		{
			uuid: `${hostname}-da0`,
			identityStable: true,
			device: 'da0',
			type: 'Virtual',
			usage: 'Unused',
			size: 128 * GIB,
			model: 'VirtIO Block Device',
			serial: `${serialPrefix}VIRT0001`,
			gpt: true,
			smartData: null,
			wearOut: 'N/A',
			partitions: []
		}
	];
}

function property(value: string, type = 'LOCAL'): DemoPoolProperty {
	return { value, source: { type, data: type === 'DEFAULT' ? '-' : value } };
}

function leafVdev(name: string, size: number, allocated: number): DemoPoolVdev {
	return {
		name,
		vdev_type: 'disk',
		guid: `vdev-${name}`,
		path: `/dev/${name}`,
		phys_path: null,
		class: 'normal',
		state: 'ONLINE',
		size,
		free: Math.max(0, size - allocated),
		allocated,
		fragmentation: 0,
		properties: null,
		vdevs: null
	};
}

function createPools(hostname: string): DemoPool[] {
	const atlasSize = 1.86 * TIB;
	const atlasAllocated =
		hostname === 'alia' ? 0.72 * TIB : hostname === 'paul' ? 0.91 * TIB : 0.82 * TIB;
	const vaultSize = 3.64 * TIB;
	const vaultAllocated = hostname === 'alia' ? 1.12 * TIB : 1.48 * TIB;
	return [
		{
			name: 'atlas',
			type: 'zpool',
			state: 'ONLINE',
			size: atlasSize,
			free: atlasSize - atlasAllocated,
			allocated: atlasAllocated,
			fragmentation: 9,
			dedup_ratio: 1,
			pool_guid: `${hostname}-atlas-guid`,
			txg: '284391',
			spa_version: '5000',
			zpl_version: '5',
			properties: {
				ashift: property('12'),
				autoexpand: property('off'),
				autotrim: property('on'),
				comment: property('Primary virtualisation storage')
			},
			vdevs: {
				'mirror-0': {
					name: 'mirror-0',
					vdev_type: 'mirror',
					guid: `${hostname}-atlas-mirror`,
					class: 'normal',
					state: 'ONLINE',
					size: atlasSize,
					free: atlasSize - atlasAllocated,
					allocated: atlasAllocated,
					fragmentation: 9,
					properties: null,
					vdevs: {
						nvme0p1: leafVdev('nvme0p1', atlasSize, atlasAllocated),
						nvme1p1: leafVdev('nvme1p1', atlasSize, atlasAllocated)
					}
				}
			},
			spares: null,
			logs: null,
			l2cache: null,
			special: null,
			dedup: null
		},
		{
			name: 'vault',
			type: 'zpool',
			state: 'ONLINE',
			size: vaultSize,
			free: vaultSize - vaultAllocated,
			allocated: vaultAllocated,
			fragmentation: 4,
			dedup_ratio: 1,
			pool_guid: `${hostname}-vault-guid`,
			txg: '108441',
			spa_version: '5000',
			zpl_version: '5',
			properties: {
				ashift: property('12'),
				autoexpand: property('off'),
				autotrim: property('off'),
				comment: property('Local backup and archive pool')
			},
			vdevs: {
				ada0p1: leafVdev('ada0p1', vaultSize, vaultAllocated)
			},
			spares: null,
			logs: null,
			l2cache: null,
			special: null,
			dedup: null
		}
	];
}

function dataset(
	name: string,
	guid: string,
	type: DatasetType,
	used: number,
	available: number,
	properties: Record<string, string> = {}
): DemoDataset {
	const pool = name.split('/')[0].split('@')[0];
	const isSnapshot = type === 'SNAPSHOT';
	const isVolume = type === 'VOLUME';
	return {
		name,
		guid,
		used,
		pool,
		available,
		mountpoint: isSnapshot ? '-' : isVolume ? '-' : `/${name}`,
		type,
		referenced: Math.max(0, Math.round(used * 0.82)),
		properties: {
			compression: 'zstd',
			compressratio: '1.37x',
			encryption: 'off',
			readonly: 'off',
			...properties
		}
	};
}

function createDatasets(hostname: string): DemoDataset[] {
	const prefix = `demo-${hostname}`;
	return [
		dataset('atlas', `${prefix}-atlas-root`, 'FILESYSTEM', 1.4 * GIB, 1.04 * TIB, {
			mountpoint: '/atlas',
			recordsize: '128K',
			mounted: 'yes'
		}),
		dataset('atlas/apps', `${prefix}-apps`, 'FILESYSTEM', 84 * GIB, 1.04 * TIB, {
			recordsize: '16K',
			mounted: 'yes'
		}),
		dataset('atlas/data', `${prefix}-data`, 'FILESYSTEM', 312 * GIB, 1.04 * TIB, {
			recordsize: '128K',
			mounted: 'yes'
		}),
		dataset('atlas/data/projects', `${prefix}-projects`, 'FILESYSTEM', 118 * GIB, 1.04 * TIB, {
			recordsize: '128K',
			mounted: 'yes'
		}),
		dataset('atlas/media', `${prefix}-media`, 'FILESYSTEM', 226 * GIB, 1.04 * TIB, {
			recordsize: '1M',
			mounted: 'yes'
		}),
		dataset('atlas/volumes', `${prefix}-volumes`, 'FILESYSTEM', 192 * 1024 ** 2, 1.04 * TIB, {
			mounted: 'yes'
		}),
		dataset('vault', `${prefix}-vault-root`, 'FILESYSTEM', 768 * 1024 ** 2, 2.16 * TIB, {
			mountpoint: '/vault',
			mounted: 'yes'
		}),
		dataset('vault/backups', `${prefix}-backups`, 'FILESYSTEM', 1.12 * TIB, 2.16 * TIB, {
			recordsize: '1M',
			mounted: 'yes'
		}),
		dataset('vault/archive', `${prefix}-archive`, 'FILESYSTEM', 188 * GIB, 2.16 * TIB, {
			recordsize: '1M',
			mounted: 'yes'
		}),
		dataset('atlas/volumes/postgres', `${prefix}-zvol-postgres`, 'VOLUME', 64 * GIB, 1.04 * TIB, {
			volsize: String(64 * GIB),
			volblocksize: '16K',
			volmode: 'dev',
			refreservation: 'none'
		}),
		dataset(
			'atlas/volumes/build-cache',
			`${prefix}-zvol-build-cache`,
			'VOLUME',
			96 * GIB,
			1.04 * TIB,
			{
				volsize: String(96 * GIB),
				volblocksize: '16K',
				volmode: 'dev',
				refreservation: 'none'
			}
		),
		dataset(
			'atlas/data@manual-before-upgrade',
			`${prefix}-snap-upgrade`,
			'SNAPSHOT',
			8.4 * GIB,
			0,
			{
				readonly: 'on',
				written: String(8.4 * GIB)
			}
		),
		dataset('atlas/apps@hourly-2026-08-14-1100', `${prefix}-snap-apps`, 'SNAPSHOT', 1.8 * GIB, 0, {
			readonly: 'on',
			written: String(1.8 * GIB)
		}),
		dataset('vault/backups@daily-2026-08-14', `${prefix}-snap-backups`, 'SNAPSHOT', 14.2 * GIB, 0, {
			readonly: 'on',
			written: String(14.2 * GIB)
		})
	];
}

function createUsersAndGroups(): { users: User[]; groups: Group[] } {
	const operators = { id: 10, name: 'operators', notes: 'Infrastructure operators' };
	const media = { id: 11, name: 'media', notes: 'Media library users' };
	const users: User[] = [
		{
			id: 1,
			username: 'admin',
			fullName: 'Sylve Administrator',
			email: 'admin@sylve.local',
			notes: 'Demo administrator',
			admin: true,
			uid: 1001,
			shell: '/bin/sh',
			homeDirectory: '/home/admin',
			homeDirPerms: 493,
			sshPublicKey: '',
			disablePassword: false,
			locked: false,
			doasEnabled: true,
			primaryGroupId: 10,
			source: 'local',
			passkeyEligible: true,
			createdAt,
			updatedAt,
			lastLoginTime: '2026-08-14T11:38:00.000Z',
			groups: [operators]
		},
		{
			id: 2,
			username: 'alice',
			fullName: 'Alice Morgan',
			email: 'alice@sylve.local',
			notes: 'Application owner',
			admin: false,
			uid: 1002,
			shell: '/usr/sbin/nologin',
			homeDirectory: '/nonexistent',
			homeDirPerms: 493,
			sshPublicKey: '',
			disablePassword: false,
			locked: false,
			doasEnabled: false,
			primaryGroupId: 10,
			source: 'local',
			passkeyEligible: false,
			createdAt,
			updatedAt,
			lastLoginTime: '2026-08-13T16:14:00.000Z',
			groups: [operators]
		},
		{
			id: 3,
			username: 'media',
			fullName: 'Media Service',
			email: '',
			notes: 'Service account for media workflows',
			admin: false,
			uid: 1003,
			shell: '/usr/sbin/nologin',
			homeDirectory: '/nonexistent',
			homeDirPerms: 493,
			sshPublicKey: '',
			disablePassword: false,
			locked: false,
			doasEnabled: false,
			primaryGroupId: 11,
			source: 'local',
			passkeyEligible: false,
			createdAt,
			updatedAt,
			lastLoginTime: '2026-08-12T09:05:00.000Z',
			groups: [media]
		},
		{
			id: 4,
			username: 'deploy',
			fullName: 'Deployment Operator',
			email: 'deploy@sylve.local',
			notes: 'FreeBSD account imported for deployment work',
			admin: false,
			uid: 1201,
			shell: '/bin/sh',
			homeDirectory: '/home/deploy',
			homeDirPerms: 493,
			sshPublicKey: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDemoKey sylve-demo',
			disablePassword: true,
			locked: false,
			doasEnabled: true,
			primaryGroupId: 10,
			source: 'pam',
			passkeyEligible: false,
			createdAt,
			updatedAt,
			lastLoginTime: '2026-08-14T10:22:00.000Z',
			groups: [operators]
		}
	];
	const groups: Group[] = [
		{ ...operators, createdAt, updatedAt, users: [users[0], users[1], users[3]] },
		{ ...media, createdAt, updatedAt, users: [users[2]] }
	];
	return { users, groups };
}

function createFiles(hostname: string): Record<string, DemoFileEntry> {
	const file = (id: string, size: number, date: string): DemoFileEntry => ({
		id,
		date,
		type: 'file',
		size
	});
	const folder = (id: string, date: string): DemoFileEntry => ({
		id,
		date,
		type: 'folder',
		lazy: true
	});
	return {
		'/atlas': folder('/atlas', '2026-04-18T08:30:00.000Z'),
		'/vault': folder('/vault', '2026-04-18T08:32:00.000Z'),
		'/tmp': folder('/tmp', '2026-08-14T05:00:00.000Z'),
		'/atlas/apps': folder('/atlas/apps', '2026-04-20T10:20:00.000Z'),
		'/atlas/data': folder('/atlas/data', '2026-04-19T08:14:00.000Z'),
		'/atlas/media': folder('/atlas/media', '2026-05-01T12:00:00.000Z'),
		'/atlas/volumes': folder('/atlas/volumes', '2026-04-22T06:40:00.000Z'),
		'/atlas/data/projects': folder('/atlas/data/projects', '2026-06-02T14:10:00.000Z'),
		'/atlas/data/incoming': folder('/atlas/data/incoming', '2026-08-13T17:50:00.000Z'),
		'/atlas/data/README.md': file('/atlas/data/README.md', 4_862, '2026-08-14T08:30:00.000Z'),
		'/atlas/data/inventory.csv': file(
			'/atlas/data/inventory.csv',
			186_420,
			'2026-08-14T09:12:00.000Z'
		),
		'/atlas/data/projects/sylve': folder('/atlas/data/projects/sylve', '2026-07-12T07:35:00.000Z'),
		'/atlas/data/projects/release-notes.md': file(
			'/atlas/data/projects/release-notes.md',
			18_904,
			'2026-08-13T11:42:00.000Z'
		),
		'/atlas/media/library': folder('/atlas/media/library', '2026-05-01T12:02:00.000Z'),
		'/atlas/media/cover.jpg': file('/atlas/media/cover.jpg', 842_221, '2026-08-10T18:42:00.000Z'),
		'/vault/backups': folder('/vault/backups', '2026-04-21T01:00:00.000Z'),
		'/vault/archive': folder('/vault/archive', '2026-05-11T07:15:00.000Z'),
		'/vault/backups/latest.txt': file('/vault/backups/latest.txt', 128, '2026-08-14T02:00:00.000Z'),
		[`/tmp/${hostname}-diagnostics.txt`]: file(
			`/tmp/${hostname}-diagnostics.txt`,
			32_842,
			'2026-08-14T10:45:00.000Z'
		)
	};
}

function createSmartTest(device: string, protocol: string): Record<string, unknown> {
	return {
		device,
		capabilities: {
			protocol,
			scope: protocol === 'NVMe' ? 'controller' : 'device',
			namespace_id: protocol === 'NVMe' ? 1 : 0,
			supported: true,
			single_operation: true,
			execution_support_known: true,
			offline: true,
			default: true,
			short: true,
			extended: true,
			conveyance: protocol !== 'NVMe',
			selective: protocol !== 'NVMe',
			short_captive: false,
			extended_captive: false,
			conveyance_captive: false,
			selective_captive: false,
			abort: true,
			result_log: true,
			progress: true,
			offline_duration_minutes: 2,
			short_duration_minutes: 3,
			extended_duration_minutes: protocol === 'NVMe' ? 18 : 310,
			conveyance_duration_minutes: protocol === 'NVMe' ? 0 : 5
		},
		status: {
			protocol,
			namespace_id: protocol === 'NVMe' ? 1 : 0,
			state: 'idle',
			execution_status: 'Completed without error',
			type: 'short',
			running: false,
			progress_pct: 100,
			progress_known: true,
			remaining_pct: 0,
			remaining_known: true,
			estimated_duration_minutes: 3,
			offline_collection_status: 'idle',
			offline_collection_running: false,
			checksum_valid: true,
			results: [
				{
					protocol,
					type: 'short',
					mode: 'background',
					status: 'completed',
					outcome: 'passed',
					remaining_pct: 0,
					lifetime_hours: 8_412,
					lifetime_hours_exact: '8,412',
					lba: 0,
					lba_exact: '0',
					lba_valid: false,
					nsid: protocol === 'NVMe' ? 1 : 0,
					nsid_valid: protocol === 'NVMe',
					segment_num: 0,
					sense_key: 0,
					asc: 0,
					ascq: 0,
					status_code_type: 0,
					status_code_type_valid: false,
					status_code: 0,
					status_code_valid: false,
					checkpoint: 0,
					parameter_code: 0,
					vendor_specific: 0,
					started_at: '2026-08-13T03:30:00.000Z'
				}
			]
		}
	};
}

function seedStorageState(hostname: string): DemoStorageState {
	const disks = createDisks(hostname);
	const pools = createPools(hostname);
	const datasets = createDatasets(hostname);
	const { users, groups } = createUsersAndGroups();
	const postgresVolume = datasets.find((item) => item.name === 'atlas/volumes/postgres')!;
	const apps = datasets.find((item) => item.name === 'atlas/apps')!;
	const mediaDataset = datasets.find((item) => item.name === 'atlas/media')!;
	const iscsiTargetName = `iqn.2026-04.io.sylve:${hostname}.postgres`;
	const smartTests = Object.fromEntries(
		disks
			.filter((disk) => disk.smartData)
			.map((disk) => [
				disk.device,
				createSmartTest(disk.device, disk.type === 'NVMe' ? 'NVMe' : 'ATA')
			])
	);
	return {
		services: [...defaultServices],
		usablePools: pools.map((pool) => pool.name),
		disks,
		smartTests,
		smartSchedules: [
			{
				id: 1,
				diskKey: `${hostname}-nvme0`,
				device: 'nvme0',
				model: 'Samsung PM9A3 1.92TB',
				serial: `${hostname.toUpperCase().slice(0, 3)}NVME0001`,
				testType: 'short',
				cronExpr: '0 3 * * 0',
				enabled: true,
				queuedAt: null,
				lastRunAt: '2026-08-10T03:00:00.000Z',
				nextRunAt: '2026-08-17T03:00:00.000Z',
				lastStatus: 'passed',
				lastError: '',
				progressPct: 100,
				progressKnown: true,
				estimatedMinutes: 3
			},
			{
				id: 2,
				diskKey: `${hostname}-ada0`,
				device: 'ada0',
				model: 'Seagate Exos X18',
				serial: `${hostname.toUpperCase().slice(0, 3)}EXOS0001`,
				testType: 'extended',
				cronExpr: '30 2 1 * *',
				enabled: true,
				queuedAt: null,
				lastRunAt: '2026-08-01T02:30:00.000Z',
				nextRunAt: '2026-09-01T02:30:00.000Z',
				lastStatus: 'passed',
				lastError: '',
				progressPct: 100,
				progressKnown: true,
				estimatedMinutes: 310
			}
		],
		pools,
		datasets,
		periodicSnapshots: [
			{
				id: 1,
				guid: apps.guid,
				prefix: 'hourly',
				recursive: true,
				interval: 3600,
				cronExpr: '0 * * * *',
				pool: 'atlas',
				keepHourly: 24,
				keepDaily: 7,
				keepWeekly: 4,
				createdAt: '2026-06-12T07:00:00.000Z',
				lastRunAt: '2026-08-14T11:00:00.000Z',
				lastExecutedAt: '2026-08-14T11:00:03.000Z'
			},
			{
				id: 2,
				guid: datasets.find((item) => item.name === 'vault/backups')!.guid,
				prefix: 'daily',
				recursive: false,
				interval: 86400,
				cronExpr: '15 2 * * *',
				pool: 'vault',
				keepLast: 14,
				maxAgeDays: 30,
				createdAt: '2026-05-01T02:15:00.000Z',
				lastRunAt: '2026-08-14T02:15:00.000Z',
				lastExecutedAt: '2026-08-14T02:15:04.000Z'
			}
		],
		scrubbingPools: new Set<string>(),
		files: createFiles(hostname),
		sambaConfig: {
			id: 1,
			unixCharset: 'UTF-8',
			workgroup: 'SYLVE',
			serverString: `Sylve storage on ${hostname}`,
			interfaces: 'vm-production,bridge-lab',
			bindInterfacesOnly: true,
			appleExtensions: true,
			advertiseMdns: true
		},
		sambaShares: [
			{
				id: 1,
				name: 'apps',
				dataset: apps.guid,
				enabled: true,
				permissions: {
					read: { users: [users[1]], groups: [groups[0]] },
					write: { users: [users[1]], groups: [groups[0]] }
				},
				guest: { enabled: false, writeable: false },
				createMask: '0664',
				directoryMask: '0775',
				timeMachine: false,
				timeMachineMaxSize: 0,
				auditEnabled: true,
				auditRetentionDays: 70,
				auditedOperations: ['create_file', 'mkdirat', 'renameat', 'unlinkat'],
				createdAt,
				updatedAt
			},
			{
				id: 2,
				name: 'media',
				dataset: mediaDataset.guid,
				enabled: true,
				permissions: {
					read: { users: [users[2]], groups: [groups[1]] },
					write: { users: [users[2]], groups: [groups[1]] }
				},
				guest: { enabled: true, writeable: false },
				createMask: '0664',
				directoryMask: '0775',
				timeMachine: true,
				timeMachineMaxSize: 500 * GIB,
				auditEnabled: true,
				auditRetentionDays: 70,
				auditedOperations: ['create_file', 'renameat', 'unlinkat'],
				createdAt,
				updatedAt
			}
		],
		sambaAuditRows: [
			{
				id: 1,
				share: 'apps',
				action: 'create_file',
				path: '/atlas/apps/releases/0.3.0.txt',
				target: '',
				createdAt: '2026-08-14T11:32:14.000Z'
			},
			{
				id: 2,
				share: 'apps',
				action: 'renameat',
				path: '/atlas/apps/releases/candidate.txt',
				target: '/atlas/apps/releases/0.3.0.txt',
				createdAt: '2026-08-14T11:31:58.000Z'
			},
			{
				id: 3,
				share: 'media',
				action: 'mkdirat',
				path: '/atlas/media/library/documentaries',
				target: '',
				createdAt: '2026-08-14T10:48:22.000Z'
			},
			{
				id: 4,
				share: 'media',
				action: 'unlinkat',
				path: '/atlas/media/incoming/duplicate.mkv',
				target: '',
				createdAt: '2026-08-14T09:15:03.000Z'
			}
		],
		users,
		groups,
		passkeys: {
			1: [
				{
					id: 1,
					userId: 1,
					credentialId: 'demo-admin-macbook-passkey',
					label: 'Admin MacBook',
					createdAt: '2026-07-18T06:40:00.000Z',
					updatedAt: '2026-07-18T06:40:00.000Z'
				}
			]
		},
		pendingPasskeyUser: {},
		iscsiTargets: [
			{
				id: 1,
				targetName: iscsiTargetName,
				alias: 'PostgreSQL primary',
				authMethod: 'CHAP',
				chapName: 'postgres-node',
				chapSecret: 'demo-chap-secret',
				mutualChapName: '',
				mutualChapSecret: '',
				portals: [
					{
						id: 1,
						targetId: 1,
						address: '10.20.0.10',
						port: 3260,
						createdAt,
						updatedAt
					}
				],
				luns: [
					{
						id: 1,
						targetId: 1,
						lunNumber: 0,
						zvol: postgresVolume.name,
						createdAt,
						updatedAt
					}
				],
				createdAt,
				updatedAt
			}
		],
		targetSessions: { [iscsiTargetName]: 2 },
		iscsiInitiators: [
			{
				id: 1,
				nickname: 'offsite-backup',
				targetAddress: '10.40.0.12:3260',
				targetName: 'iqn.2026-02.io.sylve:backup.vault',
				initiatorName: `iqn.2026-04.io.sylve:${hostname}`,
				authMethod: 'CHAP',
				chapName: 'sylve-backup',
				chapSecret: 'demo-backup-secret',
				tgtChapName: '',
				tgtChapSecret: '',
				createdAt,
				updatedAt
			}
		],
		iscsiStatus: {
			'iqn.2026-02.io.sylve:backup.vault': 'Connected — session 4, 10.40.0.12:3260'
		}
	};
}

function stateFor(hostname: string): DemoStorageState {
	let state = storageStates.get(hostname);
	if (!state) {
		state = seedStorageState(hostname);
		storageStates.set(hostname, state);
	}
	return state;
}

function normalizePath(path: string): string {
	if (!path || path === '/') return '/';
	return `/${path.split('/').filter(Boolean).join('/')}`;
}

function parentPath(path: string): string {
	const normalized = normalizePath(path);
	const index = normalized.lastIndexOf('/');
	return index <= 0 ? '/' : normalized.slice(0, index);
}

function baseName(path: string): string {
	return normalizePath(path).split('/').filter(Boolean).pop() ?? '';
}

function joinPath(parent: string, name: string): string {
	const normalizedParent = normalizePath(parent);
	return normalizedParent === '/' ? `/${name}` : `${normalizedParent}/${name}`;
}

function directoryEntries(state: DemoStorageState, directory: string): DemoFileEntry[] {
	const normalized = normalizePath(directory);
	return Object.values(state.files)
		.filter((entry) => parentPath(entry.id) === normalized)
		.sort((left, right) => {
			if (left.type !== right.type) return left.type === 'folder' ? -1 : 1;
			return left.id.localeCompare(right.id);
		});
}

function deleteFileTree(state: DemoStorageState, path: string): void {
	const normalized = normalizePath(path);
	for (const key of Object.keys(state.files)) {
		if (key === normalized || key.startsWith(`${normalized}/`)) delete state.files[key];
	}
}

function moveFileTree(
	state: DemoStorageState,
	source: string,
	destination: string,
	copy: boolean
): void {
	const normalizedSource = normalizePath(source);
	const targetRoot = joinPath(destination, baseName(normalizedSource));
	const matches = Object.entries(state.files).filter(
		([key]) => key === normalizedSource || key.startsWith(`${normalizedSource}/`)
	);
	for (const [key, entry] of matches) {
		const nextPath = `${targetRoot}${key.slice(normalizedSource.length)}`;
		state.files[nextPath] = {
			...entry,
			id: nextPath,
			date: new Date().toISOString()
		};
	}
	if (!copy) deleteFileTree(state, normalizedSource);
}

function renameFileTree(state: DemoStorageState, source: string, destination: string): void {
	const normalizedSource = normalizePath(source);
	const normalizedDestination = normalizePath(destination);
	const matches = Object.entries(state.files).filter(
		([key]) => key === normalizedSource || key.startsWith(`${normalizedSource}/`)
	);
	const renamed = matches.map(([key, entry]) => {
		const nextPath = `${normalizedDestination}${key.slice(normalizedSource.length)}`;
		return [
			nextPath,
			{
				...entry,
				id: nextPath,
				date: new Date().toISOString()
			}
		] as const;
	});

	deleteFileTree(state, normalizedSource);
	for (const [nextPath, entry] of renamed) state.files[nextPath] = entry;
}

function updateDiskUsage(state: DemoStorageState): void {
	const usedDevices = new Set<string>();
	const collect = (vdevs: Record<string, DemoPoolVdev> | null | undefined) => {
		for (const vdev of Object.values(vdevs ?? {})) {
			if (vdev.vdevs) collect(vdev.vdevs);
			else usedDevices.add(vdev.name);
		}
	};
	for (const pool of state.pools) {
		collect(pool.vdevs);
		collect(pool.spares);
	}
	for (const disk of state.disks) {
		const diskUsed = usedDevices.has(disk.device);
		let partitionUsed = false;
		for (const partition of disk.partitions) {
			const used = usedDevices.has(partition.name);
			if (used) {
				partition.usage = 'ZFS';
				partitionUsed = true;
			} else if (partition.usage === 'ZFS') {
				partition.usage = 'Unused';
			}
		}
		disk.usage =
			diskUsed || partitionUsed ? 'ZFS' : disk.partitions.length > 0 ? 'Partitions' : 'Unused';
	}
}

function replaceVdevName(
	vdevs: Record<string, DemoPoolVdev> | null | undefined,
	oldName: string,
	newName: string,
	size: number
): boolean {
	if (!vdevs) return false;
	for (const [key, vdev] of Object.entries(vdevs)) {
		if (vdev.name === oldName) {
			const replacement = leafVdev(newName, size, vdev.allocated);
			delete vdevs[key];
			vdevs[newName] = replacement;
			return true;
		}
		if (replaceVdevName(vdev.vdevs, oldName, newName, size)) return true;
	}
	return false;
}

function detachVdev(vdevs: Record<string, DemoPoolVdev> | null | undefined, name: string): boolean {
	if (!vdevs) return false;
	for (const [key, vdev] of Object.entries(vdevs)) {
		if (vdev.name === name) {
			delete vdevs[key];
			return true;
		}
		if (detachVdev(vdev.vdevs, name)) return true;
	}
	return false;
}

function poolStatus(pool: DemoPool, scrubbing: boolean) {
	const statusVdev = (vdev: DemoPoolVdev): Record<string, unknown> => ({
		name: vdev.name,
		vdev_type: vdev.vdev_type,
		guid: vdev.guid,
		path: vdev.path ?? null,
		class: vdev.class,
		state: vdev.state,
		alloc_space: String(vdev.allocated),
		total_space: String(vdev.size),
		def_space: String(vdev.free),
		rep_dev_size: '0',
		read_errors: '0',
		write_errors: '0',
		checksum_errors: '0',
		properties: null,
		vdevs: vdev.vdevs
			? Object.fromEntries(
					Object.entries(vdev.vdevs).map(([key, child]) => [key, statusVdev(child)])
				)
			: null
	});
	return {
		name: pool.name,
		state: pool.state,
		pool_guid: pool.pool_guid,
		txg: pool.txg,
		spa_version: pool.spa_version,
		zpl_version: pool.zpl_version,
		status: 'All devices are healthy.',
		action: 'No action is required.',
		scan_stats: scrubbing
			? {
					function: 'SCRUB',
					state: 'SCANNING',
					start_time: String(Math.floor(Date.now() / 1000) - 180),
					end_time: '0',
					to_examine: String(pool.size),
					examined: String(pool.size * 0.31),
					skipped: '0',
					processed: String(pool.size * 0.31),
					errors: '0',
					bytes_per_scan: String(420 * 1024 ** 2),
					pass_start: '0',
					scrub_pause: '0',
					scrub_spent_paused: '0',
					issued_bytes_per_scan: String(420 * 1024 ** 2),
					issued: String(pool.size * 0.31)
				}
			: null,
		vdevs: Object.fromEntries(
			Object.entries(pool.vdevs).map(([key, vdev]) => [key, statusVdev(vdev)])
		),
		logs: null,
		spares: pool.spares
			? Object.fromEntries(
					Object.entries(pool.spares).map(([key, vdev]) => [key, statusVdev(vdev)])
				)
			: null,
		l2cache: null,
		special: null,
		dedup: null
	};
}

function dashboardSnapshot(state: DemoStorageState) {
	const now = Date.now();
	return {
		pools: state.pools.map((pool, index) => ({
			guid: pool.pool_guid,
			name: pool.name,
			state: pool.state,
			size: pool.size,
			allocated: pool.allocated,
			free: pool.free,
			fragmentation: pool.fragmentation,
			dedupRatio: pool.dedup_ratio,
			statusAvailable: true,
			status: 'All devices are healthy.',
			action: 'No action is required.',
			errors: { read: 0, write: 0, checksum: 0, scan: 0 },
			scan: state.scrubbingPools.has(pool.pool_guid)
				? {
						function: 'SCRUB',
						state: 'SCANNING',
						startTime: new Date(now - 180_000).toISOString(),
						endTime: '',
						examined: pool.size * 0.31,
						toExamine: pool.size,
						issued: pool.size * 0.31,
						processed: pool.size * 0.31,
						errors: 0,
						progressPercent: 31
					}
				: null,
			topology: {
				dataVdevs: Object.keys(pool.vdevs).length,
				disks: pool.name === 'atlas' ? 2 : 1,
				logs: 0,
				cache: 0,
				spares: Object.keys(pool.spares ?? {}).length,
				special: 0,
				dedup: 0
			},
			io: {
				sampledAt: now,
				intervalSeconds: 10,
				valid: true,
				latencyAvailable: true,
				readIOPS: 820 + index * 110,
				writeIOPS: 410 + index * 70,
				readBytesPerSecond: (92 + index * 18) * 1024 ** 2,
				writeBytesPerSecond: (48 + index * 12) * 1024 ** 2,
				readLatencyNanos: 420_000 + index * 90_000,
				writeLatencyNanos: 710_000 + index * 120_000
			}
		})),
		arc: {
			id: Math.floor(now / 10_000),
			time: now,
			size: 18.4 * GIB,
			targetSize: 24 * GIB,
			minSize: 2 * GIB,
			maxSize: 32 * GIB,
			dataSize: 14.8 * GIB,
			metadataSize: 2.7 * GIB,
			otherSize: 0.9 * GIB,
			headerSize: 320 * 1024 ** 2,
			compressedSize: 14.1 * GIB,
			uncompressedSize: 19.6 * GIB,
			hitRatio: 94.7,
			demandHitRatio: 97.2,
			prefetchHitRatio: 71.4,
			l2HitRatio: null,
			evictionsPerSecond: 8,
			l2ReadBytesPerSecond: 0,
			l2WriteBytesPerSecond: 0,
			memoryThrottleEvents: 0,
			evictNotEnoughEvents: 0,
			l2DeviceCount: 0,
			l2Size: 0,
			l2Allocated: 0
		},
		sampledAt: now,
		generatedAt: now,
		stale: false
	};
}

function dashboardHistory(state: DemoStorageState, maxPoints: number) {
	const now = Date.now();
	const count = Math.min(Math.max(12, maxPoints), 96);
	const points = (pool: DemoPool, poolIndex: number) =>
		Array.from({ length: count }, (_, index) => {
			const phase = index / 4 + poolIndex;
			return {
				id: index + 1,
				health: 'ONLINE',
				worstHealth: 'ONLINE',
				allocated: pool.allocated + Math.sin(phase) * 1.2 * GIB,
				free: pool.free - Math.sin(phase) * 1.2 * GIB,
				size: pool.size,
				fragmentation: pool.fragmentation,
				dedupRatio: pool.dedup_ratio,
				readIOPS: 620 + poolIndex * 180 + Math.sin(phase) * 190,
				writeIOPS: 340 + poolIndex * 90 + Math.cos(phase) * 120,
				readBytesPerSecond: (68 + poolIndex * 22 + Math.sin(phase) * 18) * 1024 ** 2,
				writeBytesPerSecond: (38 + poolIndex * 15 + Math.cos(phase) * 12) * 1024 ** 2,
				readLatencyNanos: 410_000 + Math.abs(Math.sin(phase)) * 280_000,
				writeLatencyNanos: 680_000 + Math.abs(Math.cos(phase)) * 360_000,
				maxReadIOPS: 1_240,
				maxWriteIOPS: 780,
				maxReadBytesPerSecond: 160 * 1024 ** 2,
				maxWriteBytesPerSecond: 96 * 1024 ** 2,
				maxReadLatencyNanos: 1_200_000,
				maxWriteLatencyNanos: 1_850_000,
				sampleCount: 1,
				intervalSeconds: 10,
				time: now - (count - 1 - index) * 10_000
			};
		});
	const arc = Array.from({ length: count }, (_, index) => ({
		id: index + 1,
		time: now - (count - 1 - index) * 10_000,
		size: (18 + Math.sin(index / 7) * 1.4) * GIB,
		targetSize: 24 * GIB,
		minSize: 2 * GIB,
		maxSize: 32 * GIB,
		dataSize: (14.5 + Math.sin(index / 7) * 1.1) * GIB,
		metadataSize: 2.7 * GIB,
		otherSize: 0.8 * GIB,
		headerSize: 320 * 1024 ** 2,
		compressedSize: 14.1 * GIB,
		uncompressedSize: 19.6 * GIB,
		hitRatio: 93.8 + Math.sin(index / 5) * 1.4,
		demandHitRatio: 96.7 + Math.sin(index / 6) * 0.8,
		prefetchHitRatio: 70 + Math.cos(index / 4) * 5,
		l2HitRatio: null,
		evictionsPerSecond: Math.max(0, 8 + Math.sin(index / 3) * 4),
		l2ReadBytesPerSecond: 0,
		l2WriteBytesPerSecond: 0,
		memoryThrottleEvents: 0,
		evictNotEnoughEvents: 0,
		l2DeviceCount: 0,
		l2Size: 0,
		l2Allocated: 0
	}));
	return {
		pools: state.pools.map((pool, index) => ({
			guid: pool.pool_guid,
			name: pool.name,
			points: points(pool, index)
		})),
		arc,
		cursors: { pool: count, arc: count },
		resolutionSeconds: 10,
		generatedAt: now,
		resetRequired: false
	};
}

function principals(
	state: DemoStorageState,
	value: unknown
): { users: Array<{ id: number; username: string }>; groups: Array<{ id: number; name: string }> } {
	const input =
		typeof value === 'object' && value !== null ? (value as Record<string, unknown>) : {};
	const userIds = numberArray(input.userIds);
	const groupIds = numberArray(input.groupIds);
	return {
		users: state.users
			.filter((user) => userIds.includes(user.id))
			.map((user) => ({ id: user.id, username: user.username })),
		groups: state.groups
			.filter((group) => groupIds.includes(group.id))
			.map((group) => ({ id: group.id, name: group.name }))
	};
}

function buildSambaShare(
	state: DemoStorageState,
	id: number,
	body: Record<string, unknown>,
	existing?: SambaShare
): SambaShare {
	const permissions =
		typeof body.permissions === 'object' && body.permissions !== null
			? (body.permissions as Record<string, unknown>)
			: {};
	const guest =
		typeof body.guest === 'object' && body.guest !== null
			? (body.guest as Record<string, unknown>)
			: {};
	return {
		id,
		name: stringValue(body, 'name', existing?.name ?? `share-${id}`),
		dataset: stringValue(body, 'dataset', existing?.dataset ?? ''),
		enabled: booleanValue(body, 'enabled', existing?.enabled ?? true),
		permissions: {
			read: principals(state, permissions.read),
			write: principals(state, permissions.write)
		},
		guest: {
			enabled: booleanValue(guest, 'enabled', existing?.guest.enabled ?? false),
			writeable: booleanValue(guest, 'writeable', existing?.guest.writeable ?? false)
		},
		createMask: stringValue(body, 'createMask', existing?.createMask ?? '0664'),
		directoryMask: stringValue(body, 'directoryMask', existing?.directoryMask ?? '0775'),
		timeMachine: booleanValue(body, 'timeMachine', existing?.timeMachine ?? false),
		timeMachineMaxSize: numberValue(body, 'timeMachineMaxSize', existing?.timeMachineMaxSize ?? 0),
		auditEnabled: booleanValue(body, 'auditEnabled', existing?.auditEnabled ?? false),
		auditRetentionDays: numberValue(body, 'auditRetentionDays', existing?.auditRetentionDays ?? 70),
		auditedOperations: stringArray(body, 'auditedOperations'),
		createdAt: existing?.createdAt ?? new Date().toISOString(),
		updatedAt: new Date().toISOString()
	};
}

function buildISCSITarget(
	id: number,
	body: Record<string, unknown>,
	existing?: ISCSITarget
): ISCSITarget {
	return {
		id,
		targetName: stringValue(body, 'targetName', existing?.targetName ?? ''),
		alias: stringValue(body, 'alias', existing?.alias ?? ''),
		authMethod: stringValue(body, 'authMethod', existing?.authMethod ?? 'None'),
		chapName: stringValue(body, 'chapName', existing?.chapName ?? ''),
		chapSecret: stringValue(body, 'chapSecret', existing?.chapSecret ?? ''),
		mutualChapName: stringValue(body, 'mutualChapName', existing?.mutualChapName ?? ''),
		mutualChapSecret: stringValue(body, 'mutualChapSecret', existing?.mutualChapSecret ?? ''),
		portals: existing?.portals ?? [],
		luns: existing?.luns ?? [],
		createdAt: existing?.createdAt ?? new Date().toISOString(),
		updatedAt: new Date().toISOString()
	};
}

function buildISCSIInitiator(
	id: number,
	body: Record<string, unknown>,
	existing?: ISCSIInitiator
): ISCSIInitiator {
	return {
		id,
		nickname: stringValue(body, 'nickname', existing?.nickname ?? ''),
		targetAddress: stringValue(body, 'targetAddress', existing?.targetAddress ?? ''),
		targetName: stringValue(body, 'targetName', existing?.targetName ?? ''),
		initiatorName: stringValue(body, 'initiatorName', existing?.initiatorName ?? ''),
		authMethod: stringValue(body, 'authMethod', existing?.authMethod ?? 'None'),
		chapName: stringValue(body, 'chapName', existing?.chapName ?? ''),
		chapSecret: stringValue(body, 'chapSecret', existing?.chapSecret ?? ''),
		tgtChapName: stringValue(body, 'tgtChapName', existing?.tgtChapName ?? ''),
		tgtChapSecret: stringValue(body, 'tgtChapSecret', existing?.tgtChapSecret ?? ''),
		createdAt: existing?.createdAt ?? new Date().toISOString(),
		updatedAt: new Date().toISOString()
	};
}

function datasetList(state: DemoStorageState, parsed: URL): DemoDataset[] {
	const requestedType =
		parsed.searchParams.get('type') ?? parsed.searchParams.get('datasetType') ?? 'ALL';
	let datasets =
		requestedType === 'ALL'
			? [...state.datasets]
			: state.datasets.filter((item) => item.type === requestedType);
	const excluded = (parsed.searchParams.get('nameFilter') ?? '')
		.split(',')
		.map((value) => value.trim())
		.filter(Boolean);
	if (excluded.length > 0) {
		datasets = datasets.filter((item) => !excluded.some((value) => item.name.includes(value)));
	}
	const search = (parsed.searchParams.get('search') ?? '').trim().toLowerCase();
	if (search) datasets = datasets.filter((item) => item.name.toLowerCase().includes(search));
	return datasets;
}

function paginated<T>(items: T[], parsed: URL): { last_page: number; data: T[] } {
	const page = Math.max(1, Number(parsed.searchParams.get('page') || 1));
	const size = Math.max(1, Math.min(100, Number(parsed.searchParams.get('size') || 25)));
	const offset = (page - 1) * size;
	return {
		last_page: Math.max(1, Math.ceil(items.length / size)),
		data: items.slice(offset, offset + size)
	};
}

function removeDataset(state: DemoStorageState, guid: string): boolean {
	const target = state.datasets.find((item) => item.guid === guid);
	if (!target) return false;
	state.datasets = state.datasets.filter(
		(item) =>
			item.guid !== guid &&
			!item.name.startsWith(`${target.name}/`) &&
			!item.name.startsWith(`${target.name}@`)
	);
	state.periodicSnapshots = state.periodicSnapshots.filter((item) => item.guid !== guid);
	state.sambaShares = state.sambaShares.filter((share) => share.dataset !== guid);
	for (const iscsiTarget of state.iscsiTargets) {
		iscsiTarget.luns = iscsiTarget.luns.filter((lun) => lun.zvol !== target.name);
	}
	return true;
}

export function handleDemoStorageRequest<T = unknown>(
	config: DemoStorageRequestConfig,
	parsed: URL,
	path: string,
	method: string,
	hostname: string
): DemoStorageResponse<T> | null {
	const state = stateFor(hostname);
	const body = payload(config);

	if ((path === '/basic/settings' || path === '/system/basic-settings') && method === 'GET') {
		return success({
			pools: state.usablePools,
			services: state.services,
			initialized: true
		}) as DemoStorageResponse<T>;
	}

	const serviceMatch = path.match(/^\/system\/basic-settings\/services\/([^/]+)$/);
	if (serviceMatch && method === 'PATCH') {
		const service = decodeURIComponent(serviceMatch[1]) as AvailableService;
		if (!defaultServices.includes(service)) {
			return failure(
				'service_not_found',
				`Service ${service} is unavailable`,
				404
			) as DemoStorageResponse<T>;
		}
		const enabled = booleanValue(body, 'enabled');
		state.services = enabled
			? [...new Set([...state.services, service])]
			: state.services.filter((item) => item !== service);
		return mutationSuccess(
			enabled ? 'service_enabled' : 'service_disabled'
		) as DemoStorageResponse<T>;
	}
	if (path === '/system/basic-settings/pools' && method === 'PUT') {
		const requested = Array.isArray(config.data)
			? config.data.filter((item): item is string => typeof item === 'string')
			: [];
		const available = new Set(state.pools.map((pool) => pool.name));
		state.usablePools = requested.filter((pool) => available.has(pool));
		return mutationSuccess('usable_pools_updated') as DemoStorageResponse<T>;
	}

	if (path === '/auth/users' && method === 'GET') {
		const source = parsed.searchParams.get('source');
		return success(
			source === 'local' || source === 'pam'
				? state.users.filter((user) => user.source === source)
				: state.users
		) as DemoStorageResponse<T>;
	}
	if (path === '/auth/users/uid/next' && method === 'GET') {
		return success({
			nextUID: Math.max(999, ...state.users.map((user) => user.uid ?? 999)) + 1
		}) as DemoStorageResponse<T>;
	}
	if (path === '/auth/users/capabilities' && method === 'GET') {
		return success({ doasAvailable: true }) as DemoStorageResponse<T>;
	}
	if (path === '/auth/users/importable' && method === 'GET') {
		return success([
			{
				username: 'backup',
				fullName: 'Backup Operator',
				uid: 1202,
				gid: 1202,
				shell: '/bin/sh',
				homeDirectory: '/home/backup'
			},
			{
				username: 'observability',
				fullName: 'Observability Service',
				uid: 1203,
				gid: 1203,
				shell: '/usr/sbin/nologin',
				homeDirectory: '/nonexistent'
			}
		]) as DemoStorageResponse<T>;
	}
	if (path === '/auth/users' && method === 'POST') {
		const user = userFromPayload(nextID(state.users), body, 'local');
		state.users.push(user);
		applyUserGroups(state, user, body);
		return success(
			{ id: user.id, username: user.username },
			'user_created',
			201
		) as DemoStorageResponse<T>;
	}
	if (path === '/auth/users/pam' && method === 'POST') {
		const user = userFromPayload(nextID(state.users), body, 'pam');
		state.users.push(user);
		applyUserGroups(state, user, body);
		return success(
			{ id: user.id, username: user.username },
			'user_created',
			201
		) as DemoStorageResponse<T>;
	}
	if (path === '/auth/users/import' && method === 'POST') {
		const username = stringValue(body, 'username');
		const id = nextID(state.users);
		const user = userFromPayload(
			id,
			{
				...body,
				username,
				fullName: username === 'backup' ? 'Backup Operator' : 'Observability Service',
				uid: username === 'backup' ? 1202 : 1203,
				homeDirectory: username === 'backup' ? '/home/backup' : '/nonexistent',
				shell: username === 'backup' ? '/bin/sh' : '/usr/sbin/nologin'
			},
			'pam'
		);
		state.users.push(user);
		return success({ id, username }, 'user_imported', 201) as DemoStorageResponse<T>;
	}
	if (path === '/auth/groups' && method === 'GET') {
		return success(state.groups) as DemoStorageResponse<T>;
	}
	if (path === '/auth/groups' && method === 'POST') {
		const id = nextID(state.groups);
		const name = stringValue(body, 'name', `group${id}`);
		const members = new Set(stringArray(body, 'members'));
		state.groups.push({
			id,
			name,
			notes: '',
			createdAt: new Date().toISOString(),
			updatedAt: new Date().toISOString(),
			users: state.users.filter((user) => members.has(user.username))
		});
		syncAuthRelationships(state);
		return success({ id, name }, 'group_created', 201) as DemoStorageResponse<T>;
	}
	let authMatch = path.match(/^\/auth\/groups\/(\d+)\/members$/);
	if (authMatch && method === 'PUT') {
		const group = state.groups.find((item) => item.id === Number(authMatch?.[1]));
		if (!group) return failure('group_not_found', 'group_not_found') as DemoStorageResponse<T>;
		const members = new Set(stringArray(body, 'usernames'));
		group.users = state.users.filter((user) => members.has(user.username));
		group.updatedAt = new Date().toISOString();
		syncAuthRelationships(state);
		return success(
			{ id: group.id, name: group.name },
			'group_members_updated'
		) as DemoStorageResponse<T>;
	}
	authMatch = path.match(/^\/auth\/groups\/(\d+)$/);
	if (authMatch && method === 'DELETE') {
		const index = state.groups.findIndex((item) => item.id === Number(authMatch?.[1]));
		if (index < 0) return failure('group_not_found', 'group_not_found') as DemoStorageResponse<T>;
		const [group] = state.groups.splice(index, 1);
		syncAuthRelationships(state);
		return success({ id: group.id, name: group.name }, 'group_deleted') as DemoStorageResponse<T>;
	}
	authMatch = path.match(/^\/auth\/users\/(\d+)$/);
	if (authMatch && method === 'PUT') {
		const index = state.users.findIndex((item) => item.id === Number(authMatch?.[1]));
		if (index < 0) return failure('user_not_found', 'user_not_found') as DemoStorageResponse<T>;
		const previous = state.users[index];
		const user = userFromPayload(previous.id, body, previous.source, previous);
		state.users[index] = user;
		for (const group of state.groups) {
			if (group.users?.some((member) => member.id === user.id))
				group.users = group.users.map((member) => (member.id === user.id ? user : member));
		}
		applyUserGroups(state, user, body);
		return success(
			{ id: user.id, username: user.username },
			'user_updated'
		) as DemoStorageResponse<T>;
	}
	if (authMatch && method === 'DELETE') {
		const index = state.users.findIndex((item) => item.id === Number(authMatch?.[1]));
		if (index < 0) return failure('user_not_found', 'user_not_found') as DemoStorageResponse<T>;
		const [user] = state.users.splice(index, 1);
		for (const group of state.groups)
			group.users = group.users?.filter((member) => member.id !== user.id);
		delete state.passkeys[user.id];
		syncAuthRelationships(state);
		return success(
			{ id: user.id, username: user.username },
			'user_deleted'
		) as DemoStorageResponse<T>;
	}
	authMatch = path.match(/^\/auth\/users\/(\d+)\/passkeys$/);
	if (authMatch && method === 'GET') {
		return success(state.passkeys[Number(authMatch[1])] ?? []) as DemoStorageResponse<T>;
	}
	authMatch = path.match(/^\/auth\/users\/(\d+)\/passkeys\/([^/]+)$/);
	if (authMatch && method === 'DELETE') {
		const userID = Number(authMatch[1]);
		const credentialID = decodeURIComponent(authMatch[2]);
		const passkeys = state.passkeys[userID] ?? [];
		const index = passkeys.findIndex((passkey) => passkey.credentialId === credentialID);
		if (index < 0)
			return failure('passkey_not_found', 'passkey_not_found') as DemoStorageResponse<T>;
		const [passkey] = passkeys.splice(index, 1);
		return success(passkey, 'passkey_deleted') as DemoStorageResponse<T>;
	}
	if (path === '/auth/passkeys/register/begin' && method === 'POST') {
		const requestId = `demo-passkey-${Date.now().toString(36)}`;
		state.pendingPasskeyUser[requestId] = Math.trunc(numberValue(body, 'userId'));
		return success({ requestId, publicKey: {} }) as DemoStorageResponse<T>;
	}
	if (path === '/auth/passkeys/register/finish' && method === 'POST') {
		const requestId = stringValue(body, 'requestId');
		const userID = state.pendingPasskeyUser[requestId];
		if (!userID)
			return failure(
				'passkey_request_not_found',
				'passkey_request_not_found'
			) as DemoStorageResponse<T>;
		const now = new Date().toISOString();
		const passkey: Passkey = {
			id:
				Math.max(
					0,
					...Object.values(state.passkeys)
						.flat()
						.map((item) => item.id)
				) + 1,
			userId: userID,
			credentialId: `demo-credential-${Date.now().toString(36)}`,
			label: stringValue(body, 'label', 'Demo passkey'),
			createdAt: now,
			updatedAt: now
		};
		(state.passkeys[userID] ??= []).push(passkey);
		delete state.pendingPasskeyUser[requestId];
		return success(passkey, 'passkey_registered', 201) as DemoStorageResponse<T>;
	}

	if (path === '/disk' && method === 'GET') {
		return success(state.disks) as DemoStorageResponse<T>;
	}
	if (path === '/disk/smart/self-test' && method === 'GET') {
		const device = parsed.searchParams.get('device') ?? '';
		const test = state.smartTests[device];
		return (
			test ? success(test) : failure('smart_test_unavailable', `No SMART data for ${device}`)
		) as DemoStorageResponse<T>;
	}
	if (path === '/disk/smart/self-test' && method === 'POST') {
		const device = stringValue(body, 'device');
		const test = state.smartTests[device];
		if (!test)
			return failure(
				'smart_test_unavailable',
				`No SMART data for ${device}`
			) as DemoStorageResponse<T>;
		const status = test.status as Record<string, unknown>;
		status.state = 'running';
		status.running = true;
		status.type = stringValue(body, 'testType', 'short');
		status.progress_pct = 4;
		status.progress_known = true;
		status.remaining_pct = 96;
		status.remaining_known = true;
		status.execution_status = 'Self-test routine in progress';
		return success(test, 'smart_test_started') as DemoStorageResponse<T>;
	}
	if (path === '/disk/smart/self-test/abort' && method === 'POST') {
		const device = stringValue(body, 'device');
		const test = state.smartTests[device];
		if (!test)
			return failure(
				'smart_test_unavailable',
				`No SMART data for ${device}`
			) as DemoStorageResponse<T>;
		const status = test.status as Record<string, unknown>;
		status.state = 'idle';
		status.running = false;
		status.progress_pct = -1;
		status.progress_known = false;
		status.remaining_pct = -1;
		status.remaining_known = false;
		status.execution_status = 'Aborted by user';
		return success(test, 'smart_test_aborted') as DemoStorageResponse<T>;
	}
	if (path === '/disk/smart/self-test/schedules' && method === 'GET') {
		return success(state.smartSchedules) as DemoStorageResponse<T>;
	}
	if (path === '/disk/smart/self-test/schedules' && method === 'POST') {
		const device = stringValue(body, 'device');
		const disk = state.disks.find((item) => item.device === device);
		if (!disk)
			return failure('disk_not_found', `Disk ${device} was not found`) as DemoStorageResponse<T>;
		const schedule: SmartSelfTestSchedule = {
			id: nextID(state.smartSchedules),
			diskKey: disk.uuid,
			device,
			model: disk.model,
			serial: disk.serial,
			testType: stringValue(body, 'testType') === 'extended' ? 'extended' : 'short',
			cronExpr: stringValue(body, 'cronExpr', '0 3 * * 0'),
			enabled: booleanValue(body, 'enabled', true),
			queuedAt: null,
			lastRunAt: null,
			nextRunAt: '2026-08-17T03:00:00.000Z',
			lastStatus: 'idle',
			lastError: '',
			progressPct: -1,
			progressKnown: false,
			estimatedMinutes: stringValue(body, 'testType') === 'extended' ? 310 : 3
		};
		state.smartSchedules.push(schedule);
		return success(schedule, 'smart_schedule_created', 201) as DemoStorageResponse<T>;
	}
	let match = path.match(/^\/disk\/smart\/self-test\/schedules\/(\d+)$/);
	if (match && method === 'PUT') {
		const id = Number(match[1]);
		const schedule = state.smartSchedules.find((item) => item.id === id);
		if (!schedule)
			return failure(
				'smart_schedule_not_found',
				`Schedule ${id} was not found`
			) as DemoStorageResponse<T>;
		schedule.testType = stringValue(body, 'testType') === 'extended' ? 'extended' : 'short';
		schedule.cronExpr = stringValue(body, 'cronExpr', schedule.cronExpr);
		schedule.enabled = booleanValue(body, 'enabled', schedule.enabled);
		return success(schedule, 'smart_schedule_updated') as DemoStorageResponse<T>;
	}
	if (match && method === 'DELETE') {
		const id = Number(match[1]);
		const index = state.smartSchedules.findIndex((item) => item.id === id);
		if (index < 0)
			return failure(
				'smart_schedule_not_found',
				`Schedule ${id} was not found`
			) as DemoStorageResponse<T>;
		state.smartSchedules.splice(index, 1);
		return mutationSuccess('smart_schedule_deleted') as DemoStorageResponse<T>;
	}
	match = path.match(/^\/disk\/([^/]+)\/partition-table$/);
	if (match && method === 'POST') {
		const device = decodeURIComponent(match[1]);
		const disk = state.disks.find((item) => item.device === device);
		if (!disk)
			return failure('disk_not_found', `Disk ${device} was not found`) as DemoStorageResponse<T>;
		disk.gpt = true;
		return mutationSuccess('partition_table_initialized') as DemoStorageResponse<T>;
	}
	if (match && method === 'DELETE') {
		const device = decodeURIComponent(match[1]);
		const disk = state.disks.find((item) => item.device === device);
		if (!disk)
			return failure('disk_not_found', `Disk ${device} was not found`) as DemoStorageResponse<T>;
		if (disk.usage === 'ZFS')
			return failure(
				'disk_busy',
				`${device} belongs to an active pool`,
				409
			) as DemoStorageResponse<T>;
		disk.partitions = [];
		disk.gpt = false;
		disk.usage = 'Unused';
		return mutationSuccess('partition_table_cleared') as DemoStorageResponse<T>;
	}
	match = path.match(/^\/disk\/([^/]+)\/partitions$/);
	if (match && method === 'POST') {
		const device = decodeURIComponent(match[1]);
		const disk = state.disks.find((item) => item.device === device);
		if (!disk)
			return failure('disk_not_found', `Disk ${device} was not found`) as DemoStorageResponse<T>;
		const sizes = Array.isArray(body.sizes) ? body.sizes.map(Number).filter(Number.isFinite) : [];
		for (const size of sizes) {
			const partNumber = disk.partitions.length + 1;
			disk.partitions.push({
				uuid: `${hostname}-${device}p${partNumber}-${Date.now().toString(36)}`,
				name: `${device}p${partNumber}`,
				usage: 'Unused',
				size
			});
		}
		disk.gpt = true;
		disk.usage = disk.partitions.length > 0 ? 'Partitions' : 'Unused';
		return mutationSuccess('partitions_created') as DemoStorageResponse<T>;
	}
	match = path.match(/^\/disk\/partitions\/([^/]+)$/);
	if (match && method === 'DELETE') {
		const partitionName = decodeURIComponent(match[1]);
		for (const disk of state.disks) {
			const index = disk.partitions.findIndex((item) => item.name === partitionName);
			if (index < 0) continue;
			if (disk.partitions[index].usage === 'ZFS')
				return failure(
					'partition_busy',
					`${partitionName} belongs to an active pool`,
					409
				) as DemoStorageResponse<T>;
			disk.partitions.splice(index, 1);
			disk.usage = disk.partitions.length > 0 ? 'Partitions' : 'Unused';
			return mutationSuccess('partition_deleted') as DemoStorageResponse<T>;
		}
		return failure(
			'partition_not_found',
			`Partition ${partitionName} was not found`
		) as DemoStorageResponse<T>;
	}

	if (path === '/zfs/pools' && method === 'GET') {
		return success(state.pools) as DemoStorageResponse<T>;
	}
	if (path === '/zfs/pools/disks-usage' && method === 'GET') {
		const total = state.pools.reduce((sum, pool) => sum + pool.size, 0);
		const allocated = state.pools.reduce((sum, pool) => sum + pool.allocated, 0);
		return success({
			total,
			usage: total > 0 ? (allocated / total) * 100 : 0
		}) as DemoStorageResponse<T>;
	}
	if (path === '/zfs/pools' && method === 'POST') {
		const name = stringValue(body, 'name').trim();
		if (!name)
			return failure('invalid_pool_name', 'Pool name is required', 400) as DemoStorageResponse<T>;
		if (state.pools.some((pool) => pool.name.toLowerCase() === name.toLowerCase())) {
			return failure('pool_exists', `Pool ${name} already exists`, 409) as DemoStorageResponse<T>;
		}
		const vdevInput = Array.isArray(body.vdevs) ? body.vdevs : [];
		const vdevs: Record<string, DemoPoolVdev> = {};
		let poolSize = 0;
		for (const [index, raw] of vdevInput.entries()) {
			const value = typeof raw === 'object' && raw !== null ? (raw as Record<string, unknown>) : {};
			const devices = stringArray(value, 'devices');
			const raidType = stringValue(value, 'raidType', 'stripe');
			const sizes = devices.map((device) => {
				const disk = state.disks.find((item) => item.device === device);
				if (disk) return disk.size;
				for (const candidate of state.disks) {
					const partition = candidate.partitions.find((item) => item.name === device);
					if (partition) return partition.size;
				}
				return 64 * GIB;
			});
			const total = sizes.reduce((sum, size) => sum + size, 0);
			const usable = raidType === 'mirror' ? Math.min(...sizes) : total;
			poolSize += Number.isFinite(usable) ? usable : 0;
			const key =
				raidType === 'stripe' && devices.length === 1 ? devices[0] : `${raidType}-${index}`;
			if (raidType === 'stripe' && devices.length === 1) {
				vdevs[key] = leafVdev(devices[0], usable, 0);
			} else {
				vdevs[key] = {
					name: key,
					vdev_type: raidType,
					guid: `${hostname}-${name}-${key}`,
					class: 'normal',
					state: 'ONLINE',
					size: usable,
					free: usable,
					allocated: 0,
					fragmentation: 0,
					properties: null,
					vdevs: Object.fromEntries(
						devices.map((device, deviceIndex) => [device, leafVdev(device, sizes[deviceIndex], 0)])
					)
				};
			}
		}
		if (Object.keys(vdevs).length === 0)
			return failure('no_vdevs', 'At least one device is required', 400) as DemoStorageResponse<T>;
		const properties =
			typeof body.properties === 'object' && body.properties !== null
				? (body.properties as Record<string, unknown>)
				: {};
		const pool: DemoPool = {
			name,
			type: 'zpool',
			state: 'ONLINE',
			size: poolSize,
			free: poolSize,
			allocated: 0,
			fragmentation: 0,
			dedup_ratio: 1,
			pool_guid: `${hostname}-${name}-${Date.now().toString(36)}`,
			txg: '1',
			spa_version: '5000',
			zpl_version: '5',
			properties: Object.fromEntries(
				Object.entries(properties).map(([key, value]) => [key, property(String(value))])
			),
			vdevs,
			spares: null,
			logs: null,
			l2cache: null,
			special: null,
			dedup: null
		};
		state.pools.push(pool);
		state.datasets.push(
			dataset(name, `${pool.pool_guid}-root`, 'FILESYSTEM', 256 * 1024 ** 2, poolSize, {
				mountpoint: `/${name}`,
				mounted: 'yes'
			})
		);
		updateDiskUsage(state);
		return mutationSuccess('pool_created', 201) as DemoStorageResponse<T>;
	}
	match = path.match(/^\/zfs\/pools\/([^/]+)\/status$/);
	if (match && method === 'GET') {
		const guid = decodeURIComponent(match[1]);
		const pool = state.pools.find((item) => item.pool_guid === guid);
		return (
			pool
				? success(poolStatus(pool, state.scrubbingPools.has(guid)))
				: failure('pool_not_found', `Pool ${guid} was not found`)
		) as DemoStorageResponse<T>;
	}
	match = path.match(/^\/zfs\/pools\/([^/]+)\/scrub$/);
	if (match && method === 'POST') {
		const guid = decodeURIComponent(match[1]);
		if (!state.pools.some((pool) => pool.pool_guid === guid))
			return failure('pool_not_found', `Pool ${guid} was not found`) as DemoStorageResponse<T>;
		state.scrubbingPools.add(guid);
		return mutationSuccess('pool_scrub_started') as DemoStorageResponse<T>;
	}
	match = path.match(/^\/zfs\/pools\/([^/]+)\/replace-device$/);
	if (match && method === 'POST') {
		const guid = decodeURIComponent(match[1]);
		const pool = state.pools.find((item) => item.pool_guid === guid);
		const oldName = stringValue(body, 'old');
		const newName = stringValue(body, 'new');
		const replacementDisk = state.disks.find((item) => item.device === newName);
		const replacementPartition = state.disks
			.flatMap((item) => item.partitions)
			.find((item) => item.name === newName);
		if (
			!pool ||
			!newName ||
			!replaceVdevName(
				pool.vdevs,
				oldName,
				newName,
				replacementDisk?.size ?? replacementPartition?.size ?? 64 * GIB
			)
		) {
			return failure(
				'device_replace_failed',
				'Unable to replace the selected device',
				409
			) as DemoStorageResponse<T>;
		}
		updateDiskUsage(state);
		return mutationSuccess('pool_device_replaced') as DemoStorageResponse<T>;
	}
	match = path.match(/^\/zfs\/pools\/([^/]+)\/detach$/);
	if (match && method === 'POST') {
		const guid = decodeURIComponent(match[1]);
		const pool = state.pools.find((item) => item.pool_guid === guid);
		if (!pool || !detachVdev(pool.vdevs, stringValue(body, 'device')))
			return failure(
				'device_detach_failed',
				'Unable to detach the selected device',
				409
			) as DemoStorageResponse<T>;
		updateDiskUsage(state);
		return mutationSuccess('pool_device_detached') as DemoStorageResponse<T>;
	}
	match = path.match(/^\/zfs\/pools\/([^/]+)$/);
	if (match && method === 'PATCH') {
		const guid = decodeURIComponent(match[1]);
		const pool = state.pools.find((item) => item.pool_guid === guid);
		if (!pool)
			return failure('pool_not_found', `Pool ${guid} was not found`) as DemoStorageResponse<T>;
		const properties =
			typeof body.properties === 'object' && body.properties !== null
				? (body.properties as Record<string, unknown>)
				: {};
		for (const [key, value] of Object.entries(properties))
			pool.properties[key] = property(String(value));
		const spareNames = stringArray(body, 'spares');
		pool.spares = spareNames.length
			? Object.fromEntries(
					spareNames.map((name) => {
						const disk = state.disks.find((item) => item.device === name);
						const partition = state.disks
							.flatMap((item) => item.partitions)
							.find((item) => item.name === name);
						return [name, leafVdev(name, disk?.size ?? partition?.size ?? 64 * GIB, 0)];
					})
				)
			: null;
		updateDiskUsage(state);
		return mutationSuccess('pool_updated') as DemoStorageResponse<T>;
	}
	if (match && method === 'DELETE') {
		const guid = decodeURIComponent(match[1]);
		const index = state.pools.findIndex((item) => item.pool_guid === guid);
		if (index < 0)
			return failure('pool_not_found', `Pool ${guid} was not found`) as DemoStorageResponse<T>;
		const [pool] = state.pools.splice(index, 1);
		state.datasets = state.datasets.filter((item) => item.pool !== pool.name);
		state.periodicSnapshots = state.periodicSnapshots.filter((item) => item.pool !== pool.name);
		state.sambaShares = state.sambaShares.filter((share) =>
			state.datasets.some((item) => item.guid === share.dataset)
		);
		updateDiskUsage(state);
		return mutationSuccess('pool_deleted') as DemoStorageResponse<T>;
	}

	if (path === '/zfs/dashboard/snapshot' && method === 'GET') {
		return success(dashboardSnapshot(state)) as DemoStorageResponse<T>;
	}
	if (
		(path === '/zfs/dashboard/history' || path === '/zfs/dashboard/history/delta') &&
		method === 'GET'
	) {
		const maxPoints = Math.max(12, Number(parsed.searchParams.get('maxPoints') || 72));
		const history = dashboardHistory(state, maxPoints);
		const poolGuid = parsed.searchParams.get('poolGuid') ?? '';
		if (poolGuid) history.pools = history.pools.filter((pool) => pool.guid === poolGuid);
		return success(history) as DemoStorageResponse<T>;
	}

	if ((path === '/zfs/datasets' || path === '/zfs/datasets/paginated') && method === 'GET') {
		const items = datasetList(state, parsed);
		const remote =
			path.endsWith('/paginated') ||
			parsed.searchParams.has('datasetType') ||
			parsed.searchParams.has('page') ||
			parsed.searchParams.has('size');
		return success(remote ? paginated(items, parsed) : items) as DemoStorageResponse<T>;
	}
	if (path === '/zfs/datasets' && method === 'DELETE') {
		const guids = parsed.searchParams.getAll('guid');
		let deleted = 0;
		for (const guid of guids) if (removeDataset(state, guid)) deleted += 1;
		return success({ deleted }, 'datasets_deleted') as DemoStorageResponse<T>;
	}
	if (path === '/zfs/datasets/filesystem' && method === 'POST') {
		const parent = stringValue(body, 'parent');
		const name = stringValue(body, 'name').trim();
		const parentDataset = state.datasets.find(
			(item) => item.name === parent && item.type === 'FILESYSTEM'
		);
		if (!parentDataset || !name)
			return failure(
				'invalid_dataset',
				'A parent and name are required',
				400
			) as DemoStorageResponse<T>;
		const fullName = `${parent}/${name}`;
		if (state.datasets.some((item) => item.name === fullName))
			return failure('dataset_exists', `${fullName} already exists`, 409) as DemoStorageResponse<T>;
		const properties =
			typeof body.properties === 'object' && body.properties !== null
				? Object.fromEntries(
						Object.entries(body.properties as Record<string, unknown>)
							.filter(([, value]) => value !== undefined)
							.map(([key, value]) => [key, String(value)])
					)
				: {};
		state.datasets.push(
			dataset(
				fullName,
				nextGuid(`${hostname}-filesystem`, state.datasets),
				'FILESYSTEM',
				0,
				parentDataset.available,
				{ mounted: 'yes', ...properties }
			)
		);
		return mutationSuccess('filesystem_created', 201) as DemoStorageResponse<T>;
	}
	match = path.match(/^\/zfs\/datasets\/filesystem\/([^/]+)$/);
	if (match && method === 'PATCH') {
		const guid = decodeURIComponent(match[1]);
		const item = state.datasets.find(
			(dataset) => dataset.guid === guid && dataset.type === 'FILESYSTEM'
		);
		if (!item)
			return failure(
				'filesystem_not_found',
				`Filesystem ${guid} was not found`
			) as DemoStorageResponse<T>;
		const properties =
			typeof body.properties === 'object' && body.properties !== null
				? (body.properties as Record<string, unknown>)
				: {};
		for (const [key, value] of Object.entries(properties)) {
			if (value !== undefined) item.properties[key] = String(value);
		}
		return mutationSuccess('filesystem_updated') as DemoStorageResponse<T>;
	}
	if (match && method === 'DELETE') {
		const guid = decodeURIComponent(match[1]);
		return (
			removeDataset(state, guid)
				? mutationSuccess('filesystem_deleted')
				: failure('filesystem_not_found', `Filesystem ${guid} was not found`)
		) as DemoStorageResponse<T>;
	}
	if (path === '/zfs/datasets/volume' && method === 'POST') {
		const parent = stringValue(body, 'parent');
		const name = stringValue(body, 'name').trim();
		const parentDataset = state.datasets.find((item) => item.name === parent);
		if (!parentDataset || !name)
			return failure(
				'invalid_volume',
				'A parent and name are required',
				400
			) as DemoStorageResponse<T>;
		const fullName = `${parent}/${name}`;
		if (state.datasets.some((item) => item.name === fullName))
			return failure('dataset_exists', `${fullName} already exists`, 409) as DemoStorageResponse<T>;
		const properties =
			typeof body.properties === 'object' && body.properties !== null
				? Object.fromEntries(
						Object.entries(body.properties as Record<string, unknown>).map(([key, value]) => [
							key,
							String(value)
						])
					)
				: {};
		const size = byteSizeValue(properties.volsize, 16 * GIB);
		state.datasets.push(
			dataset(
				fullName,
				nextGuid(`${hostname}-volume`, state.datasets),
				'VOLUME',
				size,
				parentDataset.available,
				{
					volsize: String(size),
					volblocksize: '16K',
					volmode: 'dev',
					...properties
				}
			)
		);
		return mutationSuccess('volume_created', 201) as DemoStorageResponse<T>;
	}
	match = path.match(/^\/zfs\/datasets\/volume\/([^/]+)\/flash$/);
	if (match && method === 'POST') {
		const guid = decodeURIComponent(match[1]);
		const item = state.datasets.find(
			(dataset) => dataset.guid === guid && dataset.type === 'VOLUME'
		);
		return (
			item
				? mutationSuccess('volume_flashed')
				: failure('volume_not_found', `Volume ${guid} was not found`)
		) as DemoStorageResponse<T>;
	}
	match = path.match(/^\/zfs\/datasets\/volume\/([^/]+)$/);
	if (match && method === 'PATCH') {
		const guid = decodeURIComponent(match[1]);
		const item = state.datasets.find(
			(dataset) => dataset.guid === guid && dataset.type === 'VOLUME'
		);
		if (!item)
			return failure('volume_not_found', `Volume ${guid} was not found`) as DemoStorageResponse<T>;
		const properties =
			typeof body.properties === 'object' && body.properties !== null
				? (body.properties as Record<string, unknown>)
				: {};
		for (const [key, value] of Object.entries(properties)) item.properties[key] = String(value);
		if (properties.volsize) item.used = byteSizeValue(properties.volsize, item.used);
		return mutationSuccess('volume_updated') as DemoStorageResponse<T>;
	}
	if (match && method === 'DELETE') {
		const guid = decodeURIComponent(match[1]);
		return (
			removeDataset(state, guid)
				? mutationSuccess('volume_deleted')
				: failure('volume_not_found', `Volume ${guid} was not found`)
		) as DemoStorageResponse<T>;
	}
	if (path === '/zfs/datasets/snapshot' && method === 'POST') {
		const guid = stringValue(body, 'guid');
		const source = state.datasets.find((item) => item.guid === guid);
		const name = stringValue(body, 'name').trim();
		if (!source || !name)
			return failure(
				'invalid_snapshot',
				'A dataset and snapshot name are required',
				400
			) as DemoStorageResponse<T>;
		const createFor = booleanValue(body, 'recursive')
			? state.datasets.filter(
					(item) =>
						item.type !== 'SNAPSHOT' &&
						(item.name === source.name || item.name.startsWith(`${source.name}/`))
				)
			: [source];
		for (const item of createFor) {
			state.datasets.push(
				dataset(
					`${item.name}@${name}`,
					nextGuid(`${hostname}-snapshot`, state.datasets),
					'SNAPSHOT',
					Math.max(64 * 1024 ** 2, item.used * 0.03),
					0,
					{ readonly: 'on', written: String(Math.max(64 * 1024 ** 2, item.used * 0.03)) }
				)
			);
		}
		return mutationSuccess('snapshot_created', 201) as DemoStorageResponse<T>;
	}
	match = path.match(/^\/zfs\/datasets\/snapshot\/([^/]+)\/rollback$/);
	if (match && method === 'POST') {
		const guid = decodeURIComponent(match[1]);
		return (
			state.datasets.some((item) => item.guid === guid && item.type === 'SNAPSHOT')
				? mutationSuccess('snapshot_rolled_back')
				: failure('snapshot_not_found', `Snapshot ${guid} was not found`)
		) as DemoStorageResponse<T>;
	}
	match = path.match(/^\/zfs\/datasets\/snapshot\/([^/]+)$/);
	if (match && method === 'DELETE') {
		const guid = decodeURIComponent(match[1]);
		const index = state.datasets.findIndex(
			(item) => item.guid === guid && item.type === 'SNAPSHOT'
		);
		if (index < 0)
			return failure(
				'snapshot_not_found',
				`Snapshot ${guid} was not found`
			) as DemoStorageResponse<T>;
		state.datasets.splice(index, 1);
		return mutationSuccess('snapshot_deleted') as DemoStorageResponse<T>;
	}
	if (path === '/zfs/datasets/snapshot/periodic' && method === 'GET') {
		return success(state.periodicSnapshots) as DemoStorageResponse<T>;
	}
	if (path === '/zfs/datasets/snapshot/periodic' && method === 'POST') {
		const guid = stringValue(body, 'guid');
		const source = state.datasets.find((item) => item.guid === guid);
		if (!source)
			return failure(
				'dataset_not_found',
				`Dataset ${guid} was not found`
			) as DemoStorageResponse<T>;
		const periodic: DemoPeriodicSnapshot = {
			id: nextID(state.periodicSnapshots),
			guid,
			prefix: stringValue(body, 'prefix', 'auto'),
			recursive: booleanValue(body, 'recursive'),
			interval: numberValue(body, 'interval', 86400),
			cronExpr: stringValue(body, 'cronExpr', '0 2 * * *'),
			pool: source.pool,
			keepLast: numberValue(body, 'keepLast') || undefined,
			maxAgeDays: numberValue(body, 'maxAgeDays') || undefined,
			keepHourly: numberValue(body, 'keepHourly') || undefined,
			keepDaily: numberValue(body, 'keepDaily') || undefined,
			keepWeekly: numberValue(body, 'keepWeekly') || undefined,
			keepMonthly: numberValue(body, 'keepMonthly') || undefined,
			keepYearly: numberValue(body, 'keepYearly') || undefined,
			createdAt: new Date().toISOString(),
			lastRunAt: new Date().toISOString(),
			lastExecutedAt: new Date().toISOString()
		};
		state.periodicSnapshots.push(periodic);
		return mutationSuccess('periodic_snapshot_created', 201) as DemoStorageResponse<T>;
	}
	match = path.match(/^\/zfs\/datasets\/snapshot\/periodic\/(\d+)$/);
	if (match && method === 'PATCH') {
		const id = Number(match[1]);
		const periodic = state.periodicSnapshots.find((item) => item.id === id);
		if (!periodic)
			return failure(
				'periodic_snapshot_not_found',
				`Schedule ${id} was not found`
			) as DemoStorageResponse<T>;
		for (const key of [
			'keepLast',
			'maxAgeDays',
			'keepHourly',
			'keepDaily',
			'keepWeekly',
			'keepMonthly',
			'keepYearly'
		] as const) {
			const value = numberValue(body, key);
			periodic[key] = value || undefined;
		}
		return mutationSuccess('periodic_snapshot_updated') as DemoStorageResponse<T>;
	}
	if (match && method === 'DELETE') {
		const id = Number(match[1]);
		const index = state.periodicSnapshots.findIndex((item) => item.id === id);
		if (index < 0)
			return failure(
				'periodic_snapshot_not_found',
				`Schedule ${id} was not found`
			) as DemoStorageResponse<T>;
		state.periodicSnapshots.splice(index, 1);
		return mutationSuccess('periodic_snapshot_deleted') as DemoStorageResponse<T>;
	}

	if (path === '/system/file-explorer' && method === 'GET') {
		const id = parsed.searchParams.get('id') ?? '/';
		return success(directoryEntries(state, id)) as DemoStorageResponse<T>;
	}
	if (path === '/system/file-explorer' && method === 'POST') {
		const parent = normalizePath(stringValue(body, 'path', '/'));
		const name = stringValue(body, 'name').trim();
		if (!name || name.includes('/'))
			return failure(
				'invalid_file_name',
				'Enter a valid file or folder name',
				400
			) as DemoStorageResponse<T>;
		const id = joinPath(parent, name);
		if (state.files[id])
			return failure('file_exists', `${id} already exists`, 409) as DemoStorageResponse<T>;
		state.files[id] = {
			id,
			date: new Date().toISOString(),
			type: booleanValue(body, 'isFolder') ? 'folder' : 'file',
			...(booleanValue(body, 'isFolder') ? { lazy: true } : { size: numberValue(body, 'size', 0) })
		};
		return mutationSuccess('file_created', 201) as DemoStorageResponse<T>;
	}
	if (path === '/system/file-explorer/upload' && method === 'POST') {
		return success(
			`demo-upload-${Date.now().toString(36)}`,
			'upload_complete',
			201
		) as DemoStorageResponse<T>;
	}
	if (path === '/system/file-explorer/upload' && method === 'DELETE') {
		return mutationSuccess('upload_reverted') as DemoStorageResponse<T>;
	}
	if (path === '/system/file-explorer/rename' && method === 'POST') {
		const source = normalizePath(stringValue(body, 'id'));
		const newName = stringValue(body, 'newName').trim();
		const entry = state.files[source];
		if (!entry)
			return failure('file_not_found', `${source} was not found`) as DemoStorageResponse<T>;
		const destination = joinPath(parentPath(source), newName);
		if (state.files[destination])
			return failure('file_exists', `${destination} already exists`, 409) as DemoStorageResponse<T>;
		renameFileTree(state, source, destination);
		return mutationSuccess('file_renamed') as DemoStorageResponse<T>;
	}
	if (path === '/system/file-explorer/delete' && method === 'POST') {
		const paths = stringArray(body, 'paths');
		for (const item of paths) deleteFileTree(state, item);
		return success({ deleted: paths.length }, 'files_deleted') as DemoStorageResponse<T>;
	}
	if (path === '/system/file-explorer/copy-or-move-batch' && method === 'POST') {
		const items = Array.isArray(body.items) ? body.items : [];
		const move = booleanValue(body, 'move');
		for (const value of items) {
			if (typeof value !== 'object' || value === null) continue;
			const item = value as Record<string, unknown>;
			const source = stringValue(item, 'source');
			const destination = stringValue(item, 'destination', '/');
			if (state.files[normalizePath(source)]) moveFileTree(state, source, destination, !move);
		}
		return mutationSuccess(move ? 'files_moved' : 'files_copied') as DemoStorageResponse<T>;
	}

	if (path === '/samba/config' && method === 'GET') {
		return success(state.sambaConfig) as DemoStorageResponse<T>;
	}
	if (path === '/samba/config' && method === 'PUT') {
		state.sambaConfig = {
			id: state.sambaConfig.id,
			unixCharset: stringValue(body, 'unixCharset', state.sambaConfig.unixCharset),
			workgroup: stringValue(body, 'workgroup', state.sambaConfig.workgroup),
			serverString: stringValue(body, 'serverString', state.sambaConfig.serverString),
			interfaces: stringValue(body, 'interfaces', state.sambaConfig.interfaces),
			bindInterfacesOnly: booleanValue(
				body,
				'bindInterfacesOnly',
				state.sambaConfig.bindInterfacesOnly
			),
			appleExtensions: booleanValue(body, 'appleExtensions', state.sambaConfig.appleExtensions),
			advertiseMdns: booleanValue(body, 'advertiseMdns', state.sambaConfig.advertiseMdns)
		};
		return mutationSuccess('samba_config_updated') as DemoStorageResponse<T>;
	}
	if (path === '/samba/shares' && method === 'GET') {
		return success(state.sambaShares) as DemoStorageResponse<T>;
	}
	if (path === '/samba/shares' && method === 'POST') {
		const share = buildSambaShare(state, nextID(state.sambaShares), body);
		state.sambaShares.push(share);
		return mutationSuccess('samba_share_created', 201) as DemoStorageResponse<T>;
	}
	match = path.match(/^\/samba\/shares\/(\d+)\/enabled$/);
	if (match && method === 'PUT') {
		const id = Number(match[1]);
		const share = state.sambaShares.find((item) => item.id === id);
		if (!share)
			return failure(
				'samba_share_not_found',
				`Share ${id} was not found`
			) as DemoStorageResponse<T>;
		share.enabled = booleanValue(body, 'enabled', share.enabled);
		share.updatedAt = new Date().toISOString();
		return mutationSuccess('samba_share_state_updated') as DemoStorageResponse<T>;
	}
	match = path.match(/^\/samba\/shares\/(\d+)$/);
	if (match && method === 'PUT') {
		const id = Number(match[1]);
		const index = state.sambaShares.findIndex((item) => item.id === id);
		if (index < 0)
			return failure(
				'samba_share_not_found',
				`Share ${id} was not found`
			) as DemoStorageResponse<T>;
		state.sambaShares[index] = buildSambaShare(state, id, body, state.sambaShares[index]);
		return mutationSuccess('samba_share_updated') as DemoStorageResponse<T>;
	}
	if (match && method === 'DELETE') {
		const id = Number(match[1]);
		const index = state.sambaShares.findIndex((item) => item.id === id);
		if (index < 0)
			return failure(
				'samba_share_not_found',
				`Share ${id} was not found`
			) as DemoStorageResponse<T>;
		state.sambaShares.splice(index, 1);
		return mutationSuccess('samba_share_deleted') as DemoStorageResponse<T>;
	}
	if (path === '/samba/audit-logs' && method === 'GET') {
		const search = (parsed.searchParams.get('search') ?? '').toLowerCase();
		const rows = state.sambaAuditRows
			.filter((row) => !search || Object.values(row).join(' ').toLowerCase().includes(search))
			.sort((left, right) => right.createdAt.localeCompare(left.createdAt));
		return success(paginated(rows, parsed)) as DemoStorageResponse<T>;
	}

	if (path === '/iscsi/targets' && method === 'GET') {
		return success(state.iscsiTargets) as DemoStorageResponse<T>;
	}
	if (path === '/iscsi/target-sessions' && method === 'GET') {
		return success(state.targetSessions) as DemoStorageResponse<T>;
	}
	if (path === '/iscsi/targets' && method === 'POST') {
		const target = buildISCSITarget(nextID(state.iscsiTargets), body);
		state.iscsiTargets.push(target);
		state.targetSessions[target.targetName] = 0;
		return mutationSuccess('iscsi_target_created', 201) as DemoStorageResponse<T>;
	}
	match = path.match(/^\/iscsi\/targets\/(\d+)\/portals$/);
	if (match && method === 'POST') {
		const targetId = Number(match[1]);
		const target = state.iscsiTargets.find((item) => item.id === targetId);
		if (!target)
			return failure(
				'iscsi_target_not_found',
				`Target ${targetId} was not found`
			) as DemoStorageResponse<T>;
		target.portals.push({
			id: nextID(target.portals),
			targetId,
			address: stringValue(body, 'address'),
			port: Math.trunc(numberValue(body, 'port', 3260)),
			createdAt: new Date().toISOString(),
			updatedAt: new Date().toISOString()
		});
		return mutationSuccess('iscsi_portal_added', 201) as DemoStorageResponse<T>;
	}
	match = path.match(/^\/iscsi\/targets\/(\d+)\/portals\/(\d+)$/);
	if (match && method === 'DELETE') {
		const targetId = Number(match[1]);
		const portalId = Number(match[2]);
		const target = state.iscsiTargets.find((item) => item.id === targetId);
		const portalIndex = target?.portals.findIndex((item) => item.id === portalId) ?? -1;
		if (!target || portalIndex < 0)
			return failure('iscsi_portal_not_found', 'Portal was not found') as DemoStorageResponse<T>;
		target.portals.splice(portalIndex, 1);
		return mutationSuccess('iscsi_portal_removed') as DemoStorageResponse<T>;
	}
	match = path.match(/^\/iscsi\/targets\/(\d+)\/luns$/);
	if (match && method === 'POST') {
		const targetId = Number(match[1]);
		const target = state.iscsiTargets.find((item) => item.id === targetId);
		if (!target)
			return failure(
				'iscsi_target_not_found',
				`Target ${targetId} was not found`
			) as DemoStorageResponse<T>;
		target.luns.push({
			id: nextID(target.luns),
			targetId,
			lunNumber: Math.trunc(numberValue(body, 'lunNumber')),
			zvol: stringValue(body, 'zvol'),
			createdAt: new Date().toISOString(),
			updatedAt: new Date().toISOString()
		});
		return mutationSuccess('iscsi_lun_added', 201) as DemoStorageResponse<T>;
	}
	match = path.match(/^\/iscsi\/targets\/(\d+)\/luns\/(\d+)$/);
	if (match && method === 'DELETE') {
		const targetId = Number(match[1]);
		const lunId = Number(match[2]);
		const target = state.iscsiTargets.find((item) => item.id === targetId);
		const lunIndex = target?.luns.findIndex((item) => item.id === lunId) ?? -1;
		if (!target || lunIndex < 0)
			return failure('iscsi_lun_not_found', 'LUN was not found') as DemoStorageResponse<T>;
		target.luns.splice(lunIndex, 1);
		return mutationSuccess('iscsi_lun_removed') as DemoStorageResponse<T>;
	}
	match = path.match(/^\/iscsi\/targets\/(\d+)$/);
	if (match && method === 'PUT') {
		const id = Number(match[1]);
		const index = state.iscsiTargets.findIndex((item) => item.id === id);
		if (index < 0)
			return failure(
				'iscsi_target_not_found',
				`Target ${id} was not found`
			) as DemoStorageResponse<T>;
		const previousName = state.iscsiTargets[index].targetName;
		state.iscsiTargets[index] = buildISCSITarget(id, body, state.iscsiTargets[index]);
		if (previousName !== state.iscsiTargets[index].targetName) {
			state.targetSessions[state.iscsiTargets[index].targetName] =
				state.targetSessions[previousName] ?? 0;
			delete state.targetSessions[previousName];
		}
		return mutationSuccess('iscsi_target_updated') as DemoStorageResponse<T>;
	}
	if (match && method === 'DELETE') {
		const id = Number(match[1]);
		const index = state.iscsiTargets.findIndex((item) => item.id === id);
		if (index < 0)
			return failure(
				'iscsi_target_not_found',
				`Target ${id} was not found`
			) as DemoStorageResponse<T>;
		const [target] = state.iscsiTargets.splice(index, 1);
		delete state.targetSessions[target.targetName];
		return mutationSuccess('iscsi_target_deleted') as DemoStorageResponse<T>;
	}

	if (path === '/iscsi/initiators' && method === 'GET') {
		return success(state.iscsiInitiators) as DemoStorageResponse<T>;
	}
	if (path === '/iscsi/status' && method === 'GET') {
		return success(state.iscsiStatus) as DemoStorageResponse<T>;
	}
	if (path === '/iscsi/initiators' && method === 'POST') {
		const initiator = buildISCSIInitiator(nextID(state.iscsiInitiators), body);
		state.iscsiInitiators.push(initiator);
		state.iscsiStatus[initiator.targetName] = 'Disconnected';
		return mutationSuccess('iscsi_initiator_created', 201) as DemoStorageResponse<T>;
	}
	match = path.match(/^\/iscsi\/initiators\/(\d+)\/connect$/);
	if (match && method === 'POST') {
		const id = Number(match[1]);
		const initiator = state.iscsiInitiators.find((item) => item.id === id);
		if (!initiator)
			return failure(
				'iscsi_initiator_not_found',
				`Initiator ${id} was not found`
			) as DemoStorageResponse<T>;
		state.iscsiStatus[initiator.targetName] =
			`Connected — session ${id + 3}, ${initiator.targetAddress}`;
		return mutationSuccess('iscsi_initiator_connected') as DemoStorageResponse<T>;
	}
	match = path.match(/^\/iscsi\/initiators\/(\d+)$/);
	if (match && method === 'PUT') {
		const id = Number(match[1]);
		const index = state.iscsiInitiators.findIndex((item) => item.id === id);
		if (index < 0)
			return failure(
				'iscsi_initiator_not_found',
				`Initiator ${id} was not found`
			) as DemoStorageResponse<T>;
		const previousTargetName = state.iscsiInitiators[index].targetName;
		state.iscsiInitiators[index] = buildISCSIInitiator(id, body, state.iscsiInitiators[index]);
		if (previousTargetName !== state.iscsiInitiators[index].targetName) {
			state.iscsiStatus[state.iscsiInitiators[index].targetName] =
				state.iscsiStatus[previousTargetName] ?? 'Disconnected';
			delete state.iscsiStatus[previousTargetName];
		}
		return mutationSuccess('iscsi_initiator_updated') as DemoStorageResponse<T>;
	}
	if (match && method === 'DELETE') {
		const id = Number(match[1]);
		const index = state.iscsiInitiators.findIndex((item) => item.id === id);
		if (index < 0)
			return failure(
				'iscsi_initiator_not_found',
				`Initiator ${id} was not found`
			) as DemoStorageResponse<T>;
		const [initiator] = state.iscsiInitiators.splice(index, 1);
		delete state.iscsiStatus[initiator.targetName];
		return mutationSuccess('iscsi_initiator_deleted') as DemoStorageResponse<T>;
	}

	return null;
}
