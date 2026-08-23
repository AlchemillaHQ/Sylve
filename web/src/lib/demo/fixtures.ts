/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { demoVMProfiles, getDemoVMProfile, getDemoVMProfileByMedia } from './vm-profiles';
import { handleDemoAdminRequest } from './admin-fixtures';
import { handleDemoNetworkRequest } from './network-fixtures';
import { handleDemoStorageRequest } from './storage-fixtures';

type DemoRequestConfig = {
	url: string;
	method?: string;
	headers?: Record<string, string>;
	data?: unknown;
};

type DemoClientResponse<T = unknown> = {
	status: number;
	data: T;
	headers: Record<string, string>;
	ok: boolean;
};

type DemoVM = {
	id: number;
	name: string;
	rid: number;
	vncPort: number;
	state: number;
	cpuPinning: Record<string, unknown>[] | null;
	description?: string;
	cpuSockets?: number;
	cpuCores?: number;
	cpuThreads?: number;
	ram?: number;
	serial?: boolean;
	vncEnabled?: boolean;
	vncBind?: string;
	vncPassword?: string;
	vncResolution?: string;
	vncWait?: boolean;
	startAtBoot?: boolean;
	startOrder?: number;
	timeOffset?: 'utc' | 'localtime';
	bootRom?: 'uefi' | 'uboot' | 'none';
	pciDevices?: number[];
	tpmEmulation?: boolean;
	ignoreUMSR?: boolean;
	qemuGuestAgent?: boolean;
	iso?: string;
	wol?: boolean;
	shutdownWaitTime?: number;
	cloudInitData?: string | null;
	cloudInitMetaData?: string | null;
	cloudInitNetworkConfig?: string | null;
	extraBhyveOptions?: string[] | null;
	storages?: Record<string, unknown>[];
	networks?: Record<string, unknown>[];
	snapshots?: Record<string, unknown>[];
};

type DemoJail = {
	id: number;
	name: string;
	ctId: number;
	state: 'ACTIVE' | 'INACTIVE';
	description?: string;
	startAtBoot?: boolean;
	startOrder?: number;
	inheritIPv4?: boolean;
	inheritIPv6?: boolean;
	type?: 'freebsd' | 'linux';
	fstab?: string;
	resolvConf?: string;
	devfsRuleset?: string;
	execTimeout?: number;
	additionalOptions?: string;
	allowedOptions?: string[];
	metadataMeta?: string;
	metadataEnv?: string;
	cores?: number;
	memory?: number;
	resourceLimits?: boolean;
	wol?: boolean;
	cleanEnvironment?: boolean;
	jailHooks?: Record<string, unknown>[];
	networks?: Record<string, unknown>[];
	storages?: Record<string, unknown>[];
	snapshots?: Record<string, unknown>[];
};

type DemoNote = {
	id: number;
	title: string;
	content: string;
	createdAt: string;
	updatedAt: string;
};

type DemoNotification = {
	id: number;
	kind: string;
	title: string;
	body: string;
	severity: 'info' | 'warning' | 'error' | 'critical';
	source: string;
	fingerprint: string;
	metadata: Record<string, string>;
	occurrenceCount: number;
	firstOccurredAt: string;
	lastOccurredAt: string;
	dismissedAt: string | null;
	createdAt: string;
	updatedAt: string;
};

type DemoBackupReadiness = {
	targetId: number;
	nodeId: string;
	validationSucceeded: boolean;
	lastVerifiedAt: string | null;
	readyUntil: string | null;
	lastError: string;
	revision: number;
	ready: boolean;
	currentVoter: boolean;
	expired: boolean;
	configurationCurrent: boolean;
};

type DemoBackupTarget = {
	id: number;
	name: string;
	sshHost: string;
	sshPort: number;
	sshKeyPath: string;
	backupRoot: string;
	createBackupRoot: boolean;
	description: string;
	enabled: boolean;
	readiness: DemoBackupReadiness[];
	createdAt: string;
	updatedAt: string;
};

type DemoBackupJob = {
	id: number;
	name: string;
	targetId: number;
	runnerNodeId: string;
	mode: 'dataset' | 'jail' | 'vm';
	sourceDataset: string;
	jailRootDataset: string;
	friendlySrc: string;
	destSuffix: string;
	pruneKeepLast: number;
	pruneTarget: boolean;
	stopBeforeBackup: boolean;
	recursive: boolean;
	encrypted: boolean;
	cronExpr: string;
	enabled: boolean;
	lastRunAt: string | null;
	nextRunAt: string | null;
	lastStatus: string;
	lastError: string;
	createdAt: string;
	updatedAt: string;
};

type DemoBackupEvent = {
	id: number;
	nodeId: string;
	jobId: number | null;
	sourceDataset: string;
	targetEndpoint: string;
	mode: string;
	status: string;
	error: string;
	output: string;
	startedAt: string;
	completedAt: string | null;
};

type DemoLifecycleTask = {
	id: number;
	guestType: 'vm' | 'jail';
	guestId: number;
	action: 'migrate';
	source: string;
	status: 'queued' | 'running' | 'success' | 'failed';
	requestedBy: string | null;
	message: string | null;
	error: string | null;
	payload: string | null;
	overrideRequested: boolean;
	startedAt: string | null;
	finishedAt: string | null;
	createdAt: string;
	updatedAt: string;
	sourceHostname: string;
	targetNodeUuid: string;
	startedAtMs: number;
	applied: boolean;
};

const GIB = 1024 ** 3;
const TIB = 1024 ** 4;
const generatedAt = new Date();
const freeBSDProfile = getDemoVMProfile('freebsd-i386')!;
const tinyCoreProfile = getDemoVMProfile('tinycore-x86')!;

function demoMetricPhase(identity: string): number {
	let hash = 0;
	for (const character of identity) hash = (hash * 31 + character.charCodeAt(0)) >>> 0;
	return (hash % 628) / 100;
}

function demoUsagePercent(identity: string, metric: 'cpu' | 'memory'): number {
	const elapsed = Date.now() / 1000;
	const phase = demoMetricPhase(`${identity}:${metric}`);
	const base = metric === 'cpu' ? 4.7 : 6.4;
	const primary = Math.sin(elapsed / 2.4 + phase) * (metric === 'cpu' ? 2.1 : 1.5);
	const secondary = Math.sin(elapsed / 5.8 + phase * 0.7) * 0.45;
	return Number(Math.min(9.4, Math.max(1.2, base + primary + secondary)).toFixed(2));
}

function demoCapacityPercent(identity: string, base: number): number {
	const elapsed = Date.now() / 1000;
	const phase = demoMetricPhase(`${identity}:capacity`);
	return Number((base + Math.sin(elapsed / 4.6 + phase) * 1.1).toFixed(2));
}

function liveNodes() {
	return nodes.map((node) => ({
		...node,
		cpuUsage: demoUsagePercent(node.hostname, 'cpu'),
		memoryUsage: demoUsagePercent(node.hostname, 'memory'),
		diskUsage: demoCapacityPercent(node.hostname, node.diskUsage)
	}));
}

const demoNotifications: DemoNotification[] = [];

const virtualMachines: Record<string, DemoVM[]> = {
	leto: [
		{
			id: 1,
			name: 'control-plane',
			rid: 100,
			vncPort: 5900,
			state: 1,
			cpuPinning: null,
			iso: freeBSDProfile.media.uuid,
			description: 'FreeBSD control plane for the production cluster',
			cpuCores: 4,
			ram: 4 * GIB
		}
	],
	paul: [
		{
			id: 2,
			name: 'storage-worker',
			rid: 110,
			vncPort: 5910,
			state: 1,
			cpuPinning: null,
			iso: freeBSDProfile.media.uuid,
			description: 'FreeBSD storage and backup worker',
			cpuCores: 4,
			ram: 6 * GIB
		}
	],
	alia: [
		{
			id: 3,
			name: 'edge-relay',
			rid: 120,
			vncPort: 5920,
			state: 1,
			cpuPinning: null,
			iso: tinyCoreProfile.media.uuid,
			description: 'Tiny Core Linux edge relay and diagnostics appliance',
			cpuCores: 2,
			ram: 256 * 1024 ** 2
		}
	]
};

const jails: Record<string, DemoJail[]> = {
	leto: [
		{
			id: 4,
			name: 'Gitea',
			ctId: 200,
			state: 'ACTIVE',
			description: 'Git service and package registry'
		},
		{
			id: 5,
			name: 'Forgejo',
			ctId: 201,
			state: 'ACTIVE',
			description: 'Self-hosted software forge'
		}
	],
	paul: [
		{
			id: 6,
			name: 'Nextcloud',
			ctId: 210,
			state: 'ACTIVE',
			description: 'Files, calendars, and collaboration'
		},
		{
			id: 7,
			name: 'Grafana',
			ctId: 211,
			state: 'ACTIVE',
			description: 'Cluster metrics and dashboards'
		}
	],
	alia: [
		{
			id: 8,
			name: 'Caddy',
			ctId: 220,
			state: 'ACTIVE',
			description: 'TLS edge proxy for public services'
		},
		{
			id: 9,
			name: 'Postgres',
			ctId: 221,
			state: 'ACTIVE',
			description: 'Primary application database'
		}
	]
};

const clusterNotes: DemoNote[] = [
	{
		id: 1,
		title: 'Maintenance window',
		content: 'Upgrade the leto boot environment after the Friday snapshot completes.',
		createdAt: '2026-08-08T09:30:00.000Z',
		updatedAt: '2026-08-08T09:30:00.000Z'
	},
	{
		id: 2,
		title: 'Control plane maintenance',
		content: 'Restart control-plane on leto after its Friday snapshot completes.',
		createdAt: '2026-08-10T14:15:00.000Z',
		updatedAt: '2026-08-10T14:15:00.000Z'
	}
];

function backupReadiness(targetId: number): DemoBackupReadiness[] {
	return ['node-leto', 'node-paul', 'node-alia'].map((nodeId, index) => ({
		targetId,
		nodeId,
		validationSucceeded: true,
		lastVerifiedAt: `2026-08-14T0${7 + index}:20:00.000Z`,
		readyUntil: `2026-08-15T0${7 + index}:20:00.000Z`,
		lastError: '',
		revision: 1,
		ready: true,
		currentVoter: true,
		expired: false,
		configurationCurrent: true
	}));
}

const backupTargets: DemoBackupTarget[] = [
	{
		id: 1,
		name: 'North vault',
		sshHost: 'backup@10.0.10.20',
		sshPort: 22,
		sshKeyPath: '/var/db/sylve/backup-targets/1/id_ed25519',
		backupRoot: 'tank/sylve',
		createBackupRoot: false,
		description: 'Primary off-host ZFS backup target',
		enabled: true,
		readiness: backupReadiness(1),
		createdAt: '2026-05-18T08:15:00.000Z',
		updatedAt: '2026-08-14T09:20:00.000Z'
	},
	{
		id: 2,
		name: 'Cold archive',
		sshHost: 'archive@10.0.20.30',
		sshPort: 2222,
		sshKeyPath: '/var/db/sylve/backup-targets/2/id_ed25519',
		backupRoot: 'archive/freebsd',
		createBackupRoot: false,
		description: 'Weekly retention on the secondary storage host',
		enabled: true,
		readiness: backupReadiness(2),
		createdAt: '2026-06-02T12:40:00.000Z',
		updatedAt: '2026-08-14T09:24:00.000Z'
	}
];

const backupJobs: DemoBackupJob[] = [
	{
		id: 1,
		name: 'control-plane nightly',
		targetId: 1,
		runnerNodeId: 'node-leto',
		mode: 'vm',
		sourceDataset: 'atlas/sylve/virtual-machines/100',
		jailRootDataset: '',
		friendlySrc: 'control-plane',
		destSuffix: 'leto/vm-100',
		pruneKeepLast: 14,
		pruneTarget: true,
		stopBeforeBackup: false,
		recursive: true,
		encrypted: false,
		cronExpr: '0 2 * * *',
		enabled: true,
		lastRunAt: '2026-08-14T02:00:00.000Z',
		nextRunAt: '2026-08-15T02:00:00.000Z',
		lastStatus: 'success',
		lastError: '',
		createdAt: '2026-06-10T09:00:00.000Z',
		updatedAt: '2026-08-14T02:08:31.000Z'
	},
	{
		id: 2,
		name: 'Gitea hourly',
		targetId: 1,
		runnerNodeId: 'node-leto',
		mode: 'jail',
		sourceDataset: '',
		jailRootDataset: 'atlas/sylve/jails/200',
		friendlySrc: 'Gitea',
		destSuffix: 'leto/jail-200',
		pruneKeepLast: 24,
		pruneTarget: true,
		stopBeforeBackup: false,
		recursive: true,
		encrypted: false,
		cronExpr: '15 * * * *',
		enabled: true,
		lastRunAt: '2026-08-14T09:15:00.000Z',
		nextRunAt: '2026-08-14T10:15:00.000Z',
		lastStatus: 'running',
		lastError: '',
		createdAt: '2026-06-12T10:30:00.000Z',
		updatedAt: '2026-08-14T09:15:00.000Z'
	},
	{
		id: 3,
		name: 'storage-worker weekly',
		targetId: 2,
		runnerNodeId: 'node-paul',
		mode: 'vm',
		sourceDataset: 'atlas/sylve/virtual-machines/110',
		jailRootDataset: '',
		friendlySrc: 'storage-worker',
		destSuffix: 'paul/vm-110',
		pruneKeepLast: 8,
		pruneTarget: true,
		stopBeforeBackup: false,
		recursive: true,
		encrypted: false,
		cronExpr: '30 3 * * 0',
		enabled: true,
		lastRunAt: '2026-08-09T03:30:00.000Z',
		nextRunAt: '2026-08-16T03:30:00.000Z',
		lastStatus: 'success',
		lastError: '',
		createdAt: '2026-06-18T14:10:00.000Z',
		updatedAt: '2026-08-09T04:12:44.000Z'
	},
	{
		id: 4,
		name: 'edge-relay daily',
		targetId: 1,
		runnerNodeId: 'node-alia',
		mode: 'vm',
		sourceDataset: 'atlas/sylve/virtual-machines/120',
		jailRootDataset: '',
		friendlySrc: 'edge-relay',
		destSuffix: 'alia/vm-120',
		pruneKeepLast: 10,
		pruneTarget: true,
		stopBeforeBackup: false,
		recursive: true,
		encrypted: true,
		cronExpr: '45 1 * * *',
		enabled: true,
		lastRunAt: '2026-08-14T01:45:00.000Z',
		nextRunAt: '2026-08-15T01:45:00.000Z',
		lastStatus: 'success',
		lastError: '',
		createdAt: '2026-07-05T16:20:00.000Z',
		updatedAt: '2026-08-14T01:51:10.000Z'
	}
];

const backupEvents: DemoBackupEvent[] = [
	{
		id: 504,
		nodeId: 'node-leto',
		jobId: 2,
		sourceDataset: 'atlas/sylve/jails/200@gen-mssqe6g0',
		targetEndpoint: 'backup@10.0.10.20:tank/sylve/leto/jail-200',
		mode: 'jail',
		status: 'running',
		error: '',
		output: 'Sending the Gitea jail snapshot stream',
		startedAt: '2026-08-14T09:15:00.000Z',
		completedAt: null
	},
	{
		id: 503,
		nodeId: 'node-leto',
		jobId: 1,
		sourceDataset: 'atlas/sylve/virtual-machines/100@gen-mssaurk0',
		targetEndpoint: 'backup@10.0.10.20:tank/sylve/leto/vm-100',
		mode: 'vm',
		status: 'success',
		error: '',
		output: 'Transferred 18.6 GiB; snapshot committed',
		startedAt: '2026-08-14T02:00:00.000Z',
		completedAt: '2026-08-14T02:08:31.000Z'
	},
	{
		id: 502,
		nodeId: 'node-leto',
		jobId: 2,
		sourceDataset: 'atlas/sylve/jails/200@gen-msso90o0',
		targetEndpoint: 'backup@10.0.10.20:tank/sylve/leto/jail-200',
		mode: 'jail',
		status: 'success',
		error: '',
		output: 'Incremental stream committed',
		startedAt: '2026-08-14T08:15:00.000Z',
		completedAt: '2026-08-14T08:15:42.000Z'
	},
	{
		id: 501,
		nodeId: 'node-paul',
		jobId: 3,
		sourceDataset: 'atlas/sylve/virtual-machines/110@gen-msl8v8w0',
		targetEndpoint: 'archive@10.0.20.30:archive/freebsd/paul/vm-110',
		mode: 'vm',
		status: 'success',
		error: '',
		output: 'Transferred 42.1 GiB; retention applied',
		startedAt: '2026-08-09T03:30:00.000Z',
		completedAt: '2026-08-09T04:12:44.000Z'
	},
	{
		id: 500,
		nodeId: 'node-paul',
		jobId: 3,
		sourceDataset: 'atlas/sylve/virtual-machines/110@gen-msb8sa80',
		targetEndpoint: 'archive@10.0.20.30:archive/freebsd/paul/vm-110',
		mode: 'vm',
		status: 'failed',
		error: 'backup_target_connection_interrupted',
		output: 'SSH transport closed during send',
		startedAt: '2026-08-02T03:30:00.000Z',
		completedAt: '2026-08-02T03:34:12.000Z'
	},
	{
		id: 499,
		nodeId: 'node-alia',
		jobId: 4,
		sourceDataset: 'atlas/sylve/virtual-machines/120@gen-mssabh40',
		targetEndpoint: 'backup@10.0.10.20:tank/sylve/alia/vm-120',
		mode: 'vm',
		status: 'success',
		error: '',
		output: 'Encrypted replication stream committed',
		startedAt: '2026-08-14T01:45:00.000Z',
		completedAt: '2026-08-14T01:51:10.000Z'
	}
];

const nodes = [
	{
		id: 1,
		nodeUUID: 'node-leto',
		status: 'online',
		hostname: 'leto',
		api: 'https://10.0.0.11:8181',
		cpu: 16,
		cpuUsage: 4.6,
		memory: 64 * GIB,
		memoryUsage: 6.8,
		disk: 8 * TIB,
		diskUsage: 52.1,
		createdAt: '2026-01-08T08:14:00.000Z',
		updatedAt: generatedAt.toISOString(),
		guestIDs: [100, 200, 201]
	},
	{
		id: 2,
		nodeUUID: 'node-paul',
		status: 'online',
		hostname: 'paul',
		api: 'https://10.0.0.12:8181',
		cpu: 12,
		cpuUsage: 5.4,
		memory: 48 * GIB,
		memoryUsage: 7.2,
		disk: 4 * TIB,
		diskUsage: 46.3,
		createdAt: '2026-02-03T11:32:00.000Z',
		updatedAt: generatedAt.toISOString(),
		guestIDs: [110, 210, 211]
	},
	{
		id: 3,
		nodeUUID: 'node-alia',
		status: 'online',
		hostname: 'alia',
		api: 'https://10.0.0.13:8181',
		cpu: 24,
		cpuUsage: 3.9,
		memory: 96 * GIB,
		memoryUsage: 6.3,
		disk: 12 * TIB,
		diskUsage: 38.6,
		createdAt: '2026-03-12T06:45:00.000Z',
		updatedAt: generatedAt.toISOString(),
		guestIDs: [120, 220, 221]
	}
];

const demoLifecycleTasks: DemoLifecycleTask[] = [];
let nextDemoLifecycleTaskId = 3000;

const clusterDetails = {
	cluster: {
		id: 1,
		enabled: true,
		raftBootstrap: true,
		raftIP: '10.0.0.11',
		raftPort: 8300
	},
	nodeId: 'node-leto',
	nodes: [
		{
			id: 'node-leto',
			address: '10.0.0.11:8300',
			suffrage: 'voter',
			isLeader: true,
			guestIDs: nodes[0].guestIDs
		},
		{
			id: 'node-paul',
			address: '10.0.0.12:8300',
			suffrage: 'voter',
			isLeader: false,
			guestIDs: nodes[1].guestIDs
		},
		{
			id: 'node-alia',
			address: '10.0.0.13:8300',
			suffrage: 'voter',
			isLeader: false,
			guestIDs: nodes[2].guestIDs
		}
	],
	leaderId: 'node-leto',
	leaderAddress: '10.0.0.11:8300',
	partial: false
};

const demoPCIDevices = [
	{
		name: 'ppt0',
		unit: 0,
		domain: 0,
		bus: 1,
		device: 0,
		function: 0,
		class: 3,
		rev: 161,
		hdr: 0,
		vendor: 4318,
		subVendor: 4318,
		subDevice: 5256,
		names: {
			vendor: 'NVIDIA Corporation',
			device: 'GeForce RTX 4060',
			class: 'Display controller',
			subclass: 'VGA compatible controller'
		}
	}
];

const demoPPTDevices = [{ id: 1, domain: 0, oldDriver: 'vgapci', deviceID: '1/0/0' }];

const demoDownloads = [
	...demoVMProfiles.map((profile, index) => ({
		id: index + 1,
		uuid: profile.media.uuid,
		path: `/var/sylve/downloads/${profile.media.fileName}`,
		name: profile.media.fileName,
		type: 'http',
		url: profile.media.url,
		progress: 100,
		size: profile.media.size,
		files: [],
		uType: 'uncategorized',
		status: 'done',
		automaticExtraction: false,
		automaticRawConversion: profile.media.fileName.endsWith('.img'),
		ignoreTLS: false,
		createdAt: `2026-08-${10 + index}T09:00:00.000Z`,
		updatedAt: `2026-08-${10 + index}T09:04:00.000Z`
	})),
	{
		id: 4,
		uuid: 'demo-freebsd-base-rootfs',
		path: '/var/sylve/downloads/base.txz',
		name: 'FreeBSD 15.0 base.txz',
		type: 'http',
		url: 'https://download.freebsd.org/releases/amd64/15.0-RELEASE/base.txz',
		progress: 100,
		size: 210 * 1024 ** 2,
		files: [],
		uType: 'base-rootfs',
		status: 'done',
		automaticExtraction: true,
		automaticRawConversion: false,
		ignoreTLS: false,
		createdAt: '2026-08-12T06:00:00.000Z',
		updatedAt: '2026-08-12T06:01:00.000Z'
	}
];

const demoPools = [
	{
		name: 'atlas',
		type: 'zpool',
		state: 'ONLINE',
		size: 8 * TIB,
		free: 3.8 * TIB,
		allocated: 4.2 * TIB,
		fragmentation: 12,
		dedup_ratio: 1,
		pool_guid: 'demo-atlas-pool-guid',
		txg: '184329',
		spa_version: '5000',
		zpl_version: '5',
		properties: {},
		vdevs: {},
		spares: null,
		logs: null,
		l2cache: null,
		special: null,
		dedup: null
	}
];

const demoBootstraps = [
	{
		pool: 'atlas',
		name: 'freebsd-15.0',
		label: 'FreeBSD 15.0-RELEASE',
		dataset: 'atlas/sylve/bootstrap/freebsd-15.0',
		mountPoint: '/atlas/sylve/bootstrap/freebsd-15.0',
		major: 15,
		minor: 0,
		type: 'freebsd',
		exists: true,
		status: 'completed',
		phase: '',
		error: ''
	},
	{
		pool: 'atlas',
		name: 'freebsd-14.3',
		label: 'FreeBSD 14.3-RELEASE',
		dataset: 'atlas/sylve/bootstrap/freebsd-14.3',
		mountPoint: '/atlas/sylve/bootstrap/freebsd-14.3',
		major: 14,
		minor: 3,
		type: 'freebsd',
		exists: false,
		status: '',
		phase: '',
		error: ''
	}
];

function success<T>(data: T): DemoClientResponse<{ status: 'success'; data: T }> {
	return {
		status: 200,
		data: { status: 'success', data },
		headers: { 'content-type': 'application/json' },
		ok: true
	};
}

function mutationSuccess(
	message: string,
	status = 200
): DemoClientResponse<{
	status: 'success';
	message: string;
	error: '';
	data: null;
}> {
	return {
		status,
		data: { status: 'success', message, error: '', data: null },
		headers: { 'content-type': 'application/json' },
		ok: true
	};
}

function failure(message: string, error: string, status = 409): DemoClientResponse {
	return {
		status,
		data: { status: 'error', message, error },
		headers: { 'content-type': 'application/json' },
		ok: false
	};
}

function missing(path: string): DemoClientResponse {
	console.warn(`[Sylve demo] No fixture is registered for ${path}`);
	return {
		status: 404,
		data: { status: 'error', message: 'Demo fixture unavailable', error: path },
		headers: { 'content-type': 'application/json' },
		ok: false
	};
}

function evictVMRuntime(rid: number): void {
	void import('$lib/demo/vm-runtime')
		.then(({ evictDemoVMRuntime }) => evictDemoVMRuntime(String(rid)))
		.catch(() => {});
}

function hostnameFor(config: DemoRequestConfig): string {
	const requested = config.headers?.['X-Current-Hostname']?.trim().toLowerCase();
	return requested && requested in virtualMachines ? requested : 'leto';
}

function nodeCPU(hostname: string) {
	const profile = {
		leto: {
			name: 'AMD EPYC 7313P 16-Core Processor',
			cores: 16,
			model: 1,
			l3: 134217728,
			frequency: 3000,
			usage: 4.6
		},
		paul: {
			name: 'AMD Ryzen 9 7900',
			cores: 12,
			model: 97,
			l3: 67108864,
			frequency: 3700,
			usage: 5.4
		},
		alia: {
			name: 'AMD EPYC 7443P 24-Core Processor',
			cores: 24,
			model: 1,
			l3: 134217728,
			frequency: 2850,
			usage: 3.9
		}
	}[hostname as 'leto' | 'paul' | 'alia'];
	return {
		name: profile.name,
		sockets: 1,
		architecture: 'amd64',
		physicalCores: profile.cores,
		threadsPerCore: 1,
		logicalCores: profile.cores,
		family: 25,
		model: profile.model,
		features: ['aes', 'avx', 'avx2', 'sse4.2', 'svm'],
		cacheLine: 64,
		cache: { l1d: 32768, l1i: 32768, l2: 1048576, l3: profile.l3 },
		frequency: profile.frequency,
		usage: demoUsagePercent(hostname, 'cpu')
	};
}

function nodeRAM(hostname: string) {
	const total = hostname === 'paul' ? 48 * GIB : hostname === 'alia' ? 96 * GIB : 64 * GIB;
	const usedPercent = demoUsagePercent(hostname, 'memory');
	return { total, free: total * (1 - usedPercent / 100), usedPercent };
}

function historyPoints(hostname: string) {
	const offset = hostname === 'paul' ? 0.5 : hostname === 'alia' ? -0.4 : 0;
	const cpu = [];
	const ram = [];
	const network = [];

	for (let index = 0; index < 36; index += 1) {
		const createdAt = new Date(generatedAt.getTime() - (35 - index) * 10 * 60 * 1000).toISOString();
		cpu.push({ id: index + 1, usage: 4.5 + offset + Math.sin(index / 3) * 2, createdAt });
		ram.push({ id: index + 1, usage: 6.2 + offset / 2 + Math.cos(index / 5) * 1.3, createdAt });
		network.push({
			id: index + 1,
			receivedBytes: Math.round((8 + Math.sin(index / 2) * 3) * 1024 ** 2),
			sentBytes: Math.round((4 + Math.cos(index / 2.7) * 2) * 1024 ** 2),
			createdAt
		});
	}

	return { cpu, ram, network, cursors: { cpu: 36, ram: 36, network: 36 } };
}

function findVM(rid: number): { hostname: string; vm: DemoVM } | null {
	for (const [hostname, list] of Object.entries(virtualMachines)) {
		const vm = list.find((candidate) => candidate.rid === rid);
		if (vm) return { hostname, vm };
	}
	return null;
}

function fullVM(vm: DemoVM) {
	const running = vm.state === 1;
	const profile = getDemoVMProfileByMedia(vm.iso) ?? freeBSDProfile;
	vm.storages ??= [
		{
			id: vm.id * 10,
			vmId: vm.id,
			name: 'disk0',
			type: 'zvol',
			enable: true,
			pool: 'atlas',
			datasetId: vm.id * 10,
			dataset: {
				id: vm.id * 10,
				pool: 'atlas',
				name: `atlas/sylve/virtual-machines/${vm.rid}/disk0`,
				guid: `demo-vm-disk-${vm.rid}`
			},
			size: profile.diskBytes,
			emulation: 'ahci-hd',
			filesystemTarget: '',
			readOnly: false,
			bootOrder: 1
		},
		{
			id: vm.id * 10 + 1,
			vmId: vm.id,
			name: profile.media.fileName,
			type: 'image',
			enable: true,
			uuid: profile.media.uuid,
			pool: '',
			datasetId: null,
			dataset: null,
			size: profile.media.size,
			emulation: 'ahci-cd',
			filesystemTarget: '',
			readOnly: true,
			bootOrder: 2
		}
	];
	vm.networks ??= [
		{
			id: vm.id * 10,
			mac: `02:53:59:4c:${vm.rid.toString(16).padStart(2, '0')}:01`,
			macId: null,
			macObj: null,
			switchId: 1,
			switchType: 'standard',
			emulation: 'virtio',
			enable: true,
			vmId: vm.id
		}
	];
	vm.snapshots ??= [
		{
			id: vm.id * 100,
			vmId: vm.id,
			rid: vm.rid,
			parentSnapshotId: null,
			name: 'known-good',
			description: 'Pre-upgrade checkpoint',
			snapshotName: `sylve-${vm.rid}-known-good`,
			rootDatasets: [`atlas/sylve/virtual-machines/${vm.rid}`],
			createdAt: '2026-08-10T06:30:00.000Z',
			updatedAt: '2026-08-10T06:30:00.000Z'
		}
	];
	return {
		...vm,
		description: vm.description ?? `${profile.label} ${profile.release} virtual machine`,
		cpuSockets: vm.cpuSockets ?? 1,
		cpuCores: vm.cpuCores ?? 1,
		cpuThreads: vm.cpuThreads ?? 1,
		ram: vm.ram ?? profile.memoryBytes,
		serial: vm.serial ?? true,
		vncEnabled: vm.vncEnabled ?? true,
		vncBind: vm.vncBind ?? '0.0.0.0',
		vncPassword: vm.vncPassword ?? '',
		vncResolution: vm.vncResolution ?? '1920x1080',
		vncWait: vm.vncWait ?? false,
		startAtBoot: vm.startAtBoot ?? true,
		startOrder: vm.startOrder ?? vm.rid - 99,
		wol: vm.wol ?? true,
		timeOffset: vm.timeOffset ?? 'utc',
		storages: vm.storages,
		networks: vm.networks,
		pciDevices: vm.pciDevices ?? null,
		shutdownWaitTime: vm.shutdownWaitTime ?? 90,
		cloudInitData: vm.cloudInitData ?? null,
		cloudInitMetaData: vm.cloudInitMetaData ?? null,
		cloudInitNetworkConfig: vm.cloudInitNetworkConfig ?? null,
		bootRom: vm.bootRom ?? 'none',
		extraBhyveOptions: vm.extraBhyveOptions ?? null,
		ignoreUMSR: vm.ignoreUMSR ?? false,
		qemuGuestAgent: vm.qemuGuestAgent ?? false,
		tpmEmulation: vm.tpmEmulation ?? false,
		createdAt: '2026-03-16T09:00:00.000Z',
		updatedAt: generatedAt.toISOString(),
		startedAt: running ? '2026-08-06T04:22:00.000Z' : null,
		stoppedAt: running ? null : '2026-08-12T17:41:00.000Z'
	};
}

function vmStats(rid: number) {
	return Array.from({ length: 32 }, (_, index) => {
		const createdAt = new Date(generatedAt.getTime() - (31 - index) * 10 * 60 * 1000).toISOString();
		const cpuUsage =
			index === 31
				? demoUsagePercent(`vm-${rid}`, 'cpu')
				: 4.1 + (rid % 4) * 0.35 + Math.sin(index / 2.6) * 1.8;
		const memoryUsage =
			index === 31
				? demoUsagePercent(`vm-${rid}`, 'memory')
				: 6.1 + (rid % 3) * 0.25 + Math.cos(index / 5) * 1.15;
		const totalMemory = findVM(rid)?.vm.ram ?? 4 * GIB;
		return {
			vmId: rid,
			cpuUsage,
			memoryUsage,
			memoryUsed: totalMemory * (memoryUsage / 100),
			createdAt
		};
	});
}

function findJail(ctId: number): { hostname: string; jail: DemoJail } | null {
	for (const [hostname, list] of Object.entries(jails)) {
		const jail = list.find((candidate) => candidate.ctId === ctId);
		if (jail) return { hostname, jail };
	}
	return null;
}

function fullJail(jail: DemoJail) {
	const running = jail.state === 'ACTIVE';
	jail.networks ??= [
		{
			id: jail.id * 10,
			jid: jail.id,
			name: 'epair0',
			switchId: 1,
			switchType: 'standard',
			macId: null,
			macObj: null,
			ipv4Id: null,
			ipv4GwId: null,
			ipv6Id: null,
			ipv6GwId: null,
			dhcp: true,
			slaac: false,
			defaultGateway: true,
			vlan: 30
		}
	];
	jail.storages ??= [
		{
			id: jail.id * 10,
			jid: jail.id,
			pool: 'atlas',
			guid: `demo-jail-${jail.ctId}`,
			name: `atlas/sylve/jails/${jail.ctId}`,
			isBase: true
		}
	];
	jail.snapshots ??= [
		{
			id: jail.id * 100,
			jid: jail.id,
			ctId: jail.ctId,
			parentSnapshotId: null,
			name: 'pre-update',
			description: 'Known-good application state',
			snapshotName: `sylve-${jail.ctId}-pre-update`,
			rootDataset: `atlas/sylve/jails/${jail.ctId}`,
			createdAt: '2026-08-11T04:15:00.000Z',
			updatedAt: '2026-08-11T04:15:00.000Z'
		}
	];
	return {
		...jail,
		description: jail.description ?? `${jail.name} service jail`,
		startAtBoot: jail.startAtBoot ?? true,
		startOrder: jail.startOrder ?? jail.ctId - 199,
		wol: jail.wol ?? true,
		inheritIPv4: jail.inheritIPv4 ?? false,
		inheritIPv6: jail.inheritIPv6 ?? false,
		networks: jail.networks,
		storages: jail.storages,
		type: jail.type ?? 'freebsd',
		fstab: jail.fstab ?? '',
		resolvConf: jail.resolvConf ?? 'nameserver 1.1.1.1',
		devfsRuleset: jail.devfsRuleset ?? '4',
		execTimeout: jail.execTimeout ?? 120,
		additionalOptions: jail.additionalOptions ?? '',
		allowedOptions: jail.allowedOptions ?? [],
		jailHooks: jail.jailHooks ?? [],
		metadataMeta: jail.metadataMeta ?? '',
		metadataEnv: jail.metadataEnv ?? '',
		cores: jail.cores ?? 4,
		memory: jail.memory ?? 4 * GIB,
		startedAt: running ? '2026-08-09T12:20:00.000Z' : null,
		stoppedAt: running ? null : '2026-08-13T07:10:00.000Z',
		resourceLimits: jail.resourceLimits ?? true
	};
}

function jailStats(ctId: number) {
	return Array.from({ length: 32 }, (_, index) => ({
		id: index + 1,
		jid: ctId,
		cpuUsage:
			index === 31
				? demoUsagePercent(`jail-${ctId}`, 'cpu')
				: 3.8 + (ctId % 4) * 0.3 + Math.sin(index / 2.3) * 1.7,
		memoryUsage:
			index === 31
				? demoUsagePercent(`jail-${ctId}`, 'memory')
				: 6 + (ctId % 3) * 0.25 + Math.cos(index / 4.5) * 1.2,
		createdAt: new Date(generatedAt.getTime() - (31 - index) * 10 * 60 * 1000).toISOString()
	}));
}

function auditRecords(hostname: string) {
	return [
		{
			id: 83,
			userId: 1,
			user: 'admin',
			authType: 'local',
			node: hostname,
			started: '2026-08-14T08:42:13.000Z',
			ended: '2026-08-14T08:42:14.000Z',
			action: { method: 'POST', path: '/api/vm/start', body: { rid: 100 } },
			status: 'success',
			createdAt: '2026-08-14T08:42:13.000Z',
			updatedAt: '2026-08-14T08:42:14.000Z'
		},
		{
			id: 82,
			userId: 1,
			user: 'admin',
			authType: 'local',
			node: hostname,
			started: '2026-08-14T08:36:51.000Z',
			ended: '2026-08-14T08:36:52.000Z',
			action: { method: 'POST', path: '/api/zfs/datasets/snapshot' },
			status: 'success',
			createdAt: '2026-08-14T08:36:51.000Z',
			updatedAt: '2026-08-14T08:36:52.000Z'
		}
	];
}

function updateVmState(rid: number, action: string) {
	const found = findVM(rid);
	if (!found) return null;
	found.vm.state = action === 'stop' || action === 'shutdown' ? 5 : 1;
	return {
		taskId: 900 + rid,
		rid,
		action,
		outcome: action === 'reboot' ? 'restarted' : found.vm.state === 1 ? 'started' : 'stopped'
	};
}

function updateJailState(ctId: number, action: string) {
	const found = findJail(ctId);
	if (!found) return null;
	found.jail.state = action === 'stop' ? 'INACTIVE' : 'ACTIVE';
	return {
		taskId: 1200 + ctId,
		ctId,
		action,
		outcome:
			action === 'restart' ? 'restarted' : found.jail.state === 'ACTIVE' ? 'started' : 'stopped'
	};
}

function requestPayload(config: DemoRequestConfig): Record<string, unknown> {
	return typeof config.data === 'object' && config.data !== null
		? (config.data as Record<string, unknown>)
		: {};
}

function stringField(payload: Record<string, unknown>, key: string, fallback = ''): string {
	return typeof payload[key] === 'string' ? payload[key] : fallback;
}

function numberField(payload: Record<string, unknown>, key: string, fallback = 0): number {
	const value = Number(payload[key]);
	return Number.isFinite(value) ? value : fallback;
}

function booleanField(payload: Record<string, unknown>, key: string, fallback = false): boolean {
	return typeof payload[key] === 'boolean' ? payload[key] : fallback;
}

function nextId(items: Array<{ id: number }>): number {
	return Math.max(0, ...items.map((item) => item.id)) + 1;
}

function publicBackupJob(job: DemoBackupJob) {
	return {
		...job,
		target: backupTargets.find((target) => target.id === job.targetId)
	};
}

function publicBackupEvent(event: DemoBackupEvent) {
	const { nodeId: _nodeId, ...publicEvent } = event;
	return publicEvent;
}

function createDemoBackupTarget(config: DemoRequestConfig): DemoClientResponse {
	const payload = requestPayload(config);
	const name = stringField(payload, 'name').trim();
	const sshHost = stringField(payload, 'sshHost').trim();
	const backupRoot = stringField(payload, 'backupRoot').trim();

	if (!name || !sshHost || !backupRoot) {
		return failure('invalid_backup_target', 'Name, SSH host, and backup root are required.', 400);
	}
	if (backupTargets.some((target) => target.name.toLowerCase() === name.toLowerCase())) {
		return failure('backup_target_name_conflict', 'A backup target with this name already exists.');
	}

	const id = nextId(backupTargets);
	const timestamp = new Date().toISOString();
	backupTargets.unshift({
		id,
		name,
		sshHost,
		sshPort: Math.trunc(numberField(payload, 'sshPort', 22)),
		sshKeyPath: `/var/db/sylve/backup-targets/${id}/id_ed25519`,
		backupRoot,
		createBackupRoot: booleanField(payload, 'createBackupRoot'),
		description: stringField(payload, 'description').trim(),
		enabled: booleanField(payload, 'enabled', true),
		readiness: backupReadiness(id),
		createdAt: timestamp,
		updatedAt: timestamp
	});

	return mutationSuccess('backup_target_created', 201);
}

function updateDemoBackupTarget(id: number, config: DemoRequestConfig): DemoClientResponse {
	const target = backupTargets.find((entry) => entry.id === id);
	if (!target) return failure('backup_target_not_found', 'backup_target_not_found', 404);

	const payload = requestPayload(config);
	const name = stringField(payload, 'name').trim();
	if (!name) return failure('invalid_backup_target', 'Name is required.', 400);
	if (
		backupTargets.some(
			(entry) => entry.id !== id && entry.name.toLowerCase() === name.toLowerCase()
		)
	) {
		return failure('backup_target_name_conflict', 'A backup target with this name already exists.');
	}

	target.name = name;
	target.description = stringField(payload, 'description').trim();
	target.enabled = booleanField(payload, 'enabled', target.enabled);
	target.updatedAt = new Date().toISOString();
	return mutationSuccess('backup_target_updated');
}

function createDemoBackupJob(config: DemoRequestConfig): DemoClientResponse {
	const payload = requestPayload(config);
	const name = stringField(payload, 'name').trim();
	const targetId = Math.trunc(numberField(payload, 'targetId'));
	const runnerNodeId = stringField(payload, 'runnerNodeId').trim();
	const mode = payload.mode === 'jail' || payload.mode === 'vm' ? payload.mode : 'dataset';
	const sourceDataset = stringField(payload, 'sourceDataset').trim();
	const jailRootDataset = stringField(payload, 'jailRootDataset').trim();

	if (!name || !backupTargets.some((target) => target.id === targetId)) {
		return failure('invalid_backup_job', 'A job name and valid target are required.', 400);
	}
	if (!nodes.some((node) => node.nodeUUID === runnerNodeId)) {
		return failure('backup_runner_not_raft_voter', 'Select a current cluster voter.', 400);
	}
	if ((mode === 'dataset' || mode === 'vm') && !sourceDataset) {
		return failure('source_dataset_required', 'A source dataset is required.', 400);
	}
	if (mode === 'jail' && !jailRootDataset) {
		return failure('jail_root_dataset_required', 'A jail root dataset is required.', 400);
	}

	const timestamp = new Date().toISOString();
	const runner = nodes.find((node) => node.nodeUUID === runnerNodeId)?.hostname || runnerNodeId;
	const job: DemoBackupJob = {
		id: nextId(backupJobs),
		name,
		targetId,
		runnerNodeId,
		mode,
		sourceDataset,
		jailRootDataset,
		friendlySrc: '',
		destSuffix: `${runner}/${name
			.toLowerCase()
			.replace(/[^a-z0-9]+/g, '-')
			.replace(/^-|-$/g, '')}`,
		pruneKeepLast: Math.max(0, Math.trunc(numberField(payload, 'pruneKeepLast'))),
		pruneTarget: booleanField(payload, 'pruneTarget'),
		stopBeforeBackup: booleanField(payload, 'stopBeforeBackup'),
		recursive: booleanField(payload, 'recursive'),
		encrypted: false,
		cronExpr: stringField(payload, 'cronExpr', '0 * * * *'),
		enabled: booleanField(payload, 'enabled', true),
		lastRunAt: null,
		nextRunAt: null,
		lastStatus: '',
		lastError: '',
		createdAt: timestamp,
		updatedAt: timestamp
	};
	backupJobs.unshift(job);
	return mutationSuccess('backup_job_created', 201);
}

function updateDemoBackupJob(id: number, config: DemoRequestConfig): DemoClientResponse {
	const job = backupJobs.find((entry) => entry.id === id);
	if (!job) return failure('backup_job_not_found', 'backup_job_not_found', 404);

	const payload = requestPayload(config);
	const name = stringField(payload, 'name').trim();
	const targetId = Math.trunc(numberField(payload, 'targetId'));
	const runnerNodeId = stringField(payload, 'runnerNodeId').trim();
	const mode = payload.mode === 'jail' || payload.mode === 'vm' ? payload.mode : 'dataset';
	if (!name || !backupTargets.some((target) => target.id === targetId)) {
		return failure('invalid_backup_job', 'A job name and valid target are required.', 400);
	}
	if (!nodes.some((node) => node.nodeUUID === runnerNodeId)) {
		return failure('backup_runner_not_raft_voter', 'Select a current cluster voter.', 400);
	}

	job.name = name;
	job.targetId = targetId;
	job.runnerNodeId = runnerNodeId;
	job.mode = mode;
	job.sourceDataset = stringField(payload, 'sourceDataset').trim();
	job.jailRootDataset = stringField(payload, 'jailRootDataset').trim();
	job.pruneKeepLast = Math.max(0, Math.trunc(numberField(payload, 'pruneKeepLast')));
	job.pruneTarget = booleanField(payload, 'pruneTarget');
	job.stopBeforeBackup = booleanField(payload, 'stopBeforeBackup');
	job.recursive = booleanField(payload, 'recursive');
	job.cronExpr = stringField(payload, 'cronExpr', job.cronExpr);
	job.enabled = booleanField(payload, 'enabled', job.enabled);
	job.updatedAt = new Date().toISOString();
	return mutationSuccess('backup_job_updated');
}

function runDemoBackupJob(id: number): DemoClientResponse {
	const job = backupJobs.find((entry) => entry.id === id);
	if (!job) return failure('backup_job_not_found', 'backup_job_not_found', 404);
	const target = backupTargets.find((entry) => entry.id === job.targetId);
	if (!target) return failure('backup_target_not_found', 'backup_target_not_found', 404);

	const timestamp = new Date().toISOString();
	const source = job.mode === 'jail' ? job.jailRootDataset : job.sourceDataset;
	const event: DemoBackupEvent = {
		id: nextId(backupEvents),
		nodeId: job.runnerNodeId,
		jobId: job.id,
		sourceDataset: `${source}@gen-${Date.now().toString(36)}`,
		targetEndpoint: `${target.sshHost}:${target.backupRoot}/${job.destSuffix}`,
		mode: job.mode,
		status: 'running',
		error: '',
		output: 'Backup queued in the demo runner',
		startedAt: timestamp,
		completedAt: null
	};
	backupEvents.unshift(event);
	job.lastRunAt = timestamp;
	job.lastStatus = 'running';
	job.lastError = '';
	job.updatedAt = timestamp;
	return mutationSuccess('backup_job_run_started', 202);
}

function filteredBackupEvents(parsed: URL): DemoBackupEvent[] {
	const nodeId = parsed.searchParams.get('nodeId')?.trim() || clusterDetails.nodeId;
	const jobId = Number(parsed.searchParams.get('jobId') || 0);
	const search = (parsed.searchParams.get('search') || '').trim().toLowerCase();

	return backupEvents.filter((event) => {
		if (event.nodeId !== nodeId) return false;
		if (jobId > 0 && event.jobId !== jobId) return false;
		if (!search) return true;
		return [event.sourceDataset, event.targetEndpoint, event.status, event.error]
			.join(' ')
			.toLowerCase()
			.includes(search);
	});
}

function demoDatasets(hostname: string) {
	const common = ['atlas/data', 'atlas/media'];
	const guestRoots = [
		...virtualMachines[hostname].map((vm) => `atlas/sylve/virtual-machines/${vm.rid}`),
		...jails[hostname].map((jail) => `atlas/sylve/jails/${jail.ctId}`)
	];
	return [...common, ...guestRoots].map((name, index) => ({
		name,
		guid: `demo-dataset-${hostname}-${index + 1}`,
		used: (index + 1) * 8 * GIB,
		pool: 'atlas',
		available: 5 * TIB,
		mountpoint: `/${name}`,
		type: 'FILESYSTEM',
		referenced: (index + 1) * 6 * GIB,
		properties: {
			compression: 'zstd',
			encryption: name.includes('/120') ? 'aes-256-gcm' : 'off',
			mounted: 'yes'
		}
	}));
}

function nextGuestDatabaseId(): number {
	const ids = [
		...Object.values(virtualMachines).flatMap((items) => items.map((item) => item.id)),
		...Object.values(jails).flatMap((items) => items.map((item) => item.id))
	];
	return Math.max(0, ...ids) + 1;
}

function guestIdExists(hostname: string, guestId: number): boolean {
	return (
		virtualMachines[hostname].some((vm) => vm.rid === guestId) ||
		jails[hostname].some((jail) => jail.ctId === guestId)
	);
}

function registerGuestId(hostname: string, guestId: number): void {
	const node = nodes.find((candidate) => candidate.hostname === hostname);
	if (!node || node.guestIDs.includes(guestId)) return;
	node.guestIDs.push(guestId);
	node.updatedAt = new Date().toISOString();
}

function unregisterGuestId(hostname: string, guestId: number): void {
	const node = nodes.find((candidate) => candidate.hostname === hostname);
	if (!node) return;
	const index = node.guestIDs.indexOf(guestId);
	if (index !== -1) node.guestIDs.splice(index, 1);
	node.updatedAt = new Date().toISOString();
}

function publicLifecycleTask(task: DemoLifecycleTask) {
	const {
		sourceHostname: _sourceHostname,
		targetNodeUuid: _targetNodeUuid,
		startedAtMs: _startedAtMs,
		applied: _applied,
		...publicTask
	} = task;
	return publicTask;
}

function migrationPayload(task: DemoLifecycleTask, phase: string, warnings: string[] = []): string {
	return JSON.stringify({
		targetNodeUuid: task.targetNodeUuid,
		phase,
		warnings
	});
}

function applyDemoMigration(task: DemoLifecycleTask): void {
	if (task.applied) return;
	const target = nodes.find((candidate) => candidate.nodeUUID === task.targetNodeUuid);
	if (!target) return;

	if (task.guestType === 'vm') {
		const found = findVM(task.guestId);
		if (!found) return;
		const sourceList = virtualMachines[found.hostname];
		const sourceIndex = sourceList.indexOf(found.vm);
		if (sourceIndex !== -1) sourceList.splice(sourceIndex, 1);
		virtualMachines[target.hostname].push(found.vm);
		unregisterGuestId(found.hostname, task.guestId);
		registerGuestId(target.hostname, task.guestId);
	} else {
		const found = findJail(task.guestId);
		if (!found) return;
		const sourceList = jails[found.hostname];
		const sourceIndex = sourceList.indexOf(found.jail);
		if (sourceIndex !== -1) sourceList.splice(sourceIndex, 1);
		jails[target.hostname].push(found.jail);
		unregisterGuestId(found.hostname, task.guestId);
		registerGuestId(target.hostname, task.guestId);
	}

	task.applied = true;
}

function refreshDemoMigrationTask(task: DemoLifecycleTask): void {
	if (task.status !== 'queued' && task.status !== 'running') return;

	const elapsed = Date.now() - task.startedAtMs;
	let phase = 'preflight';
	let message = 'validating_migration_prerequisites';

	if (elapsed >= 10_500) {
		task.status = 'success';
		task.message = 'migration_completed';
		task.finishedAt = new Date().toISOString();
		task.updatedAt = task.finishedAt;
		task.payload = migrationPayload(task, 'finalize');
		applyDemoMigration(task);
		return;
	}

	task.status = elapsed < 700 ? 'queued' : 'running';
	if (elapsed >= 9_500) {
		phase = 'finalize';
		message = 'finalizing_migration';
	} else if (elapsed >= 8_600) {
		phase = 'cleanup_source';
		message = 'cleaning_up_source_guest';
	} else if (elapsed >= 7_700) {
		phase = 'policy_adjustment';
		message = 'adjusting_cluster_policies';
	} else if (elapsed >= 6_600) {
		phase = 'start_target';
		message = 'starting_guest_on_target';
	} else if (elapsed >= 4_800) {
		phase = 'final_sync';
		message = 'performing_final_incremental_sync: disk0 (88%)';
	} else if (elapsed >= 3_600) {
		phase = 'stop_source';
		message = 'stopping_guest_on_source';
	} else if (elapsed >= 1_200) {
		phase = 'initial_replication';
		const percent = Math.min(76, 30 + Math.floor((elapsed - 1_200) / 75));
		message = `replicating_datasets_to_target: disk0 (${percent}%)`;
	}

	task.message = message;
	task.payload = migrationPayload(task, phase);
	task.updatedAt = new Date().toISOString();
}

function activeDemoMigrationTask(
	guestType: 'vm' | 'jail',
	guestId: number,
	hostname?: string
): DemoLifecycleTask | null {
	const task = demoLifecycleTasks.find(
		(candidate) =>
			candidate.guestType === guestType &&
			candidate.guestId === guestId &&
			(!hostname || candidate.sourceHostname === hostname) &&
			(candidate.status === 'queued' || candidate.status === 'running')
	);
	if (!task) return null;
	refreshDemoMigrationTask(task);
	return task.status === 'queued' || task.status === 'running' ? task : null;
}

function migrationValidation(
	guestType: 'vm' | 'jail',
	guestId: number,
	targetNodeUuid: string,
	sourceHostname: string
) {
	const target = nodes.find((candidate) => candidate.nodeUUID === targetNodeUuid);
	const found = guestType === 'vm' ? findVM(guestId) : findJail(guestId);
	const reasons: string[] = [];

	if (!found || found.hostname !== sourceHostname) reasons.push('source_guest_not_found');
	if (!target || target.status !== 'online') reasons.push('target_node_unavailable');
	if (target?.hostname === sourceHostname) reasons.push('target_node_is_source');
	if (target && guestIdExists(target.hostname, guestId)) reasons.push('guest_id_already_exists');
	if (activeDemoMigrationTask(guestType, guestId)) reasons.push('migration_in_progress');

	return { allowed: reasons.length === 0, reasons, warnings: [] as string[] };
}

function createDemoMigration(
	guestType: 'vm' | 'jail',
	guestId: number,
	targetNodeUuid: string,
	sourceHostname: string
): DemoClientResponse {
	const validation = migrationValidation(guestType, guestId, targetNodeUuid, sourceHostname);
	if (!validation.allowed) {
		return failure('migration_not_allowed', validation.reasons.join(','), 409);
	}

	const timestamp = new Date().toISOString();
	const task: DemoLifecycleTask = {
		id: nextDemoLifecycleTaskId++,
		guestType,
		guestId,
		action: 'migrate',
		source: 'api',
		status: 'queued',
		requestedBy: 'admin@local',
		message: 'validating_migration_prerequisites',
		error: null,
		payload: null,
		overrideRequested: false,
		startedAt: timestamp,
		finishedAt: null,
		createdAt: timestamp,
		updatedAt: timestamp,
		sourceHostname,
		targetNodeUuid,
		startedAtMs: Date.now(),
		applied: false
	};
	task.payload = migrationPayload(task, 'preflight');
	demoLifecycleTasks.unshift(task);

	return success({ taskId: task.id, guestId, outcome: 'queued' });
}

function nextRecordId(items: Record<string, unknown>[]): number {
	return Math.max(0, ...items.map((item) => Number(item.id) || 0)) + 1;
}

function demoSwitch(switchName: string): { id: number; type: 'standard' | 'manual' } {
	if (switchName === 'storage') return { id: 2, type: 'standard' };
	if (switchName === 'lab') return { id: 10, type: 'manual' };
	return { id: 1, type: 'standard' };
}

function updateDemoVMDetails(vm: DemoVM, path: string, payload: Record<string, unknown>) {
	if (path === 'name') vm.name = stringField(payload, 'name', vm.name).trim() || vm.name;
	else if (path === 'description') vm.description = stringField(payload, 'description');
	else if (path === 'hardware/cpu') {
		vm.cpuSockets = Math.max(1, Math.trunc(numberField(payload, 'cpuSockets', vm.cpuSockets ?? 1)));
		vm.cpuCores = Math.max(1, Math.trunc(numberField(payload, 'cpuCores', vm.cpuCores ?? 1)));
		vm.cpuThreads = Math.max(1, Math.trunc(numberField(payload, 'cpuThreads', vm.cpuThreads ?? 1)));
		vm.cpuPinning = Array.isArray(payload.cpuPinning)
			? payload.cpuPinning.map((entry, index) => {
					const pin =
						typeof entry === 'object' && entry !== null ? (entry as Record<string, unknown>) : {};
					return {
						id: index + 1,
						vmId: vm.id,
						hostSocket: Math.max(0, Math.trunc(numberField(pin, 'socket'))),
						hostCpu: Array.isArray(pin.cores)
							? pin.cores.filter((value): value is number => typeof value === 'number')
							: []
					};
				})
			: null;
	} else if (path === 'hardware/ram') {
		vm.ram = Math.max(64 * 1024 ** 2, Math.trunc(numberField(payload, 'ram', vm.ram ?? GIB)));
	} else if (path === 'hardware/vnc') {
		vm.vncEnabled = booleanField(payload, 'vncEnabled', vm.vncEnabled ?? true);
		vm.vncPort = Math.trunc(numberField(payload, 'vncPort', vm.vncPort));
		vm.vncBind = stringField(payload, 'vncBind', vm.vncBind ?? '0.0.0.0');
		vm.vncResolution = stringField(payload, 'vncResolution', vm.vncResolution ?? '1920x1080');
		vm.vncPassword = stringField(payload, 'vncPassword', vm.vncPassword ?? '');
		vm.vncWait = booleanField(payload, 'vncWait', vm.vncWait ?? false);
	} else if (path === 'hardware/pci-devices') {
		vm.pciDevices = Array.isArray(payload.pciDevices)
			? payload.pciDevices.filter((value): value is number => typeof value === 'number')
			: [];
	} else if (path === 'options/wol') vm.wol = booleanField(payload, 'enabled', vm.wol ?? true);
	else if (path === 'options/ignore-umsrs') {
		vm.ignoreUMSR = booleanField(payload, 'ignoreUMSRs', vm.ignoreUMSR ?? false);
	} else if (path === 'options/qemu-guest-agent') {
		vm.qemuGuestAgent = booleanField(payload, 'enabled', vm.qemuGuestAgent ?? false);
	} else if (path === 'options/tpm') {
		vm.tpmEmulation = booleanField(payload, 'enabled', vm.tpmEmulation ?? false);
	} else if (path === 'options/boot-order') {
		vm.startAtBoot = booleanField(payload, 'startAtBoot', vm.startAtBoot ?? true);
		vm.startOrder = Math.max(0, Math.trunc(numberField(payload, 'bootOrder', vm.startOrder ?? 0)));
	} else if (path === 'options/clock') {
		vm.timeOffset = payload.timeOffset === 'localtime' ? 'localtime' : 'utc';
	} else if (path === 'options/boot-rom') {
		vm.bootRom =
			payload.bootRom === 'uefi' || payload.bootRom === 'uboot' ? payload.bootRom : 'none';
	} else if (path === 'options/serial-console') {
		vm.serial = booleanField(payload, 'enabled', vm.serial ?? true);
	} else if (path === 'options/shutdown-wait-time') {
		vm.shutdownWaitTime = Math.max(
			0,
			Math.trunc(numberField(payload, 'waitTime', vm.shutdownWaitTime ?? 90))
		);
	} else if (path === 'options/cloud-init') {
		vm.cloudInitData = stringField(payload, 'data');
		vm.cloudInitMetaData = stringField(payload, 'metadata');
		vm.cloudInitNetworkConfig = stringField(payload, 'networkConfig');
	} else if (path === 'options/extra-bhyve-options') {
		vm.extraBhyveOptions = Array.isArray(payload.extraBhyveOptions)
			? payload.extraBhyveOptions.filter((value): value is string => typeof value === 'string')
			: [];
	}
}

function attachDemoVMStorage(vm: DemoVM, payload: Record<string, unknown>) {
	fullVM(vm);
	const storages = vm.storages!;
	const id = nextRecordId(storages);
	const storageType =
		payload.storageType === 'raw' ||
		payload.storageType === 'filesystem' ||
		payload.storageType === 'image'
			? payload.storageType
			: 'zvol';
	const profile = getDemoVMProfileByMedia(stringField(payload, 'downloadUUID'));
	const pool = stringField(payload, 'pool', 'atlas');
	const datasetName =
		stringField(payload, 'dataset') ||
		`atlas/sylve/virtual-machines/${vm.rid}/${stringField(payload, 'name', `disk${id}`)}`;
	const hasDataset = storageType === 'zvol' || storageType === 'filesystem';
	const storage: Record<string, unknown> = {
		id,
		vmId: vm.id,
		name: stringField(payload, 'name', profile?.media.fileName ?? `disk${id}`),
		type: storageType,
		enable: true,
		uuid: storageType === 'image' ? stringField(payload, 'downloadUUID') : undefined,
		pool: storageType === 'image' ? '' : pool,
		datasetId: hasDataset ? id : null,
		dataset: hasDataset
			? { id, pool, name: datasetName, guid: `demo-vm-disk-${vm.rid}-${id}` }
			: null,
		size: Math.trunc(numberField(payload, 'size', profile?.media.size ?? 10 * GIB)),
		emulation: stringField(
			payload,
			'emulation',
			storageType === 'image' ? 'ahci-cd' : 'virtio-blk'
		),
		filesystemTarget: stringField(payload, 'filesystemTarget'),
		readOnly: booleanField(payload, 'readOnly', storageType === 'image'),
		bootOrder: Math.trunc(numberField(payload, 'bootOrder', storages.length + 1))
	};
	storages.push(storage);
	return storage;
}

function attachDemoVMNetwork(vm: DemoVM, payload: Record<string, unknown>) {
	fullVM(vm);
	const networks = vm.networks!;
	const id = nextRecordId(networks);
	const selectedSwitch = demoSwitch(stringField(payload, 'switchName', 'production'));
	const network: Record<string, unknown> = {
		id,
		mac: `02:53:59:4c:${vm.rid.toString(16).padStart(2, '0')}:${id.toString(16).padStart(2, '0')}`,
		macId: numberField(payload, 'macId') || null,
		macObj: null,
		switchId: selectedSwitch.id,
		switchType: selectedSwitch.type,
		emulation: stringField(payload, 'emulation', 'virtio'),
		enable: true,
		vmId: vm.id
	};
	networks.push(network);
	return network;
}

function createDemoVMSnapshot(vm: DemoVM, payload: Record<string, unknown>) {
	fullVM(vm);
	const snapshots = vm.snapshots!;
	const id = nextRecordId(snapshots);
	const timestamp = new Date().toISOString();
	const name = stringField(payload, 'name', `snapshot-${id}`).trim() || `snapshot-${id}`;
	const snapshot = {
		id,
		vmId: vm.id,
		rid: vm.rid,
		parentSnapshotId: null,
		name,
		description: stringField(payload, 'description'),
		snapshotName: `sylve-${vm.rid}-${name.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`,
		rootDatasets: [`atlas/sylve/virtual-machines/${vm.rid}`],
		createdAt: timestamp,
		updatedAt: timestamp
	};
	snapshots.unshift(snapshot);
	return snapshot;
}

function createDemoVM(config: DemoRequestConfig, hostname: string): DemoClientResponse {
	const payload = requestPayload(config);
	const rid = Math.trunc(numberField(payload, 'rid'));
	const name = stringField(payload, 'name').trim();
	const iso = stringField(payload, 'iso').trim();
	const profile = getDemoVMProfileByMedia(iso);

	if (rid < 1 || rid > 9999 || name === '') {
		return failure('invalid_vm', 'A valid VM name and ID are required.', 400);
	}
	if (
		guestIdExists(hostname, rid) ||
		virtualMachines[hostname].some((vm) => vm.name.toLowerCase() === name.toLowerCase())
	) {
		return failure('rid_or_name_already_in_use', 'The VM name or guest ID is already in use.');
	}
	if (!profile) {
		return failure(
			'demo_vm_profile_unavailable',
			'Select one of the browser-compatible demo images.',
			400
		);
	}

	const pciDevices = Array.isArray(payload.pciDevices)
		? payload.pciDevices.filter((value): value is number => typeof value === 'number')
		: [];
	const vm: DemoVM = {
		id: nextGuestDatabaseId(),
		name,
		rid,
		vncPort: numberField(payload, 'vncPort', 5900),
		state: 5,
		cpuPinning: null,
		iso: profile.media.uuid,
		description: stringField(payload, 'description'),
		cpuSockets: Math.max(1, Math.trunc(numberField(payload, 'cpuSockets', 1))),
		cpuCores: Math.max(1, Math.trunc(numberField(payload, 'cpuCores', 1))),
		cpuThreads: Math.max(1, Math.trunc(numberField(payload, 'cpuThreads', 1))),
		ram: Math.max(64 * 1024 ** 2, Math.trunc(numberField(payload, 'ram', profile.memoryBytes))),
		serial: booleanField(payload, 'serial'),
		vncEnabled: booleanField(payload, 'vncEnabled', true),
		vncBind: stringField(payload, 'vncBind', '127.0.0.1'),
		vncPassword: stringField(payload, 'vncPassword'),
		vncResolution: stringField(payload, 'vncResolution', '640x480'),
		vncWait: booleanField(payload, 'vncWait'),
		startAtBoot: booleanField(payload, 'startAtBoot'),
		startOrder: Math.trunc(numberField(payload, 'startOrder')),
		timeOffset: payload.timeOffset === 'localtime' ? 'localtime' : 'utc',
		bootRom: payload.bootRom === 'uefi' || payload.bootRom === 'uboot' ? payload.bootRom : 'none',
		pciDevices,
		tpmEmulation: booleanField(payload, 'tpmEmulation'),
		ignoreUMSR: booleanField(payload, 'ignoreUMSR'),
		qemuGuestAgent: booleanField(payload, 'qemuGuestAgent'),
		shutdownWaitTime: 90,
		cloudInitData: booleanField(payload, 'cloudInit')
			? stringField(payload, 'cloudInitData')
			: null,
		cloudInitMetaData: booleanField(payload, 'cloudInit')
			? stringField(payload, 'cloudInitMetadata')
			: null,
		cloudInitNetworkConfig: booleanField(payload, 'cloudInit')
			? stringField(payload, 'cloudInitNetworkConfig')
			: null,
		extraBhyveOptions: Array.isArray(payload.extraBhyveOptions)
			? payload.extraBhyveOptions.filter((value): value is string => typeof value === 'string')
			: null
	};
	updateDemoVMDetails(vm, 'hardware/cpu', payload);
	fullVM(vm);
	const disk = vm.storages?.[0];
	if (disk) {
		const pool = stringField(payload, 'storagePool', 'atlas');
		disk.pool = pool;
		disk.type = payload.storageType === 'raw' ? 'raw' : 'zvol';
		disk.size = Math.trunc(numberField(payload, 'storageSize', profile.diskBytes));
		disk.emulation = stringField(payload, 'storageEmulationType', 'ahci-hd');
		if (typeof disk.dataset === 'object' && disk.dataset !== null) {
			(disk.dataset as Record<string, unknown>).pool = pool;
			(disk.dataset as Record<string, unknown>).name =
				`${pool}/sylve/virtual-machines/${rid}/disk0`;
		}
	}
	const primaryNetwork = vm.networks?.[0];
	if (primaryNetwork) {
		const selectedSwitch = demoSwitch(stringField(payload, 'switchName', 'production'));
		primaryNetwork.switchId = selectedSwitch.id;
		primaryNetwork.switchType = selectedSwitch.type;
		primaryNetwork.emulation = stringField(payload, 'switchEmulationType', 'virtio');
		primaryNetwork.macId = numberField(payload, 'macId') || null;
	}

	virtualMachines[hostname].push(vm);
	registerGuestId(hostname, rid);
	return success({ rid, name, node: hostname, outcome: 'created' });
}

function createDemoJail(config: DemoRequestConfig, hostname: string): DemoClientResponse {
	const payload = requestPayload(config);
	const ctId = Math.trunc(numberField(payload, 'ctId'));
	const name = stringField(payload, 'name').trim();

	if (ctId < 1 || ctId > 9999 || name === '') {
		return failure('invalid_jail', 'A valid jail name and ID are required.', 400);
	}
	if (
		guestIdExists(hostname, ctId) ||
		jails[hostname].some((jail) => jail.name.toLowerCase() === name.toLowerCase())
	) {
		return failure('jail_with_ctid_already_exists', 'The jail name or guest ID is already in use.');
	}

	const allowedOptions = Array.isArray(payload.allowedOptions)
		? payload.allowedOptions.filter((value): value is string => typeof value === 'string')
		: [];
	const jail: DemoJail = {
		id: nextGuestDatabaseId(),
		name,
		ctId,
		state: 'INACTIVE',
		description: stringField(payload, 'description'),
		startAtBoot: booleanField(payload, 'startAtBoot'),
		startOrder: Math.trunc(numberField(payload, 'startOrder')),
		inheritIPv4: booleanField(payload, 'inheritIPv4', true),
		inheritIPv6: booleanField(payload, 'inheritIPv6', true),
		type: payload.type === 'linux' ? 'linux' : 'freebsd',
		fstab: stringField(payload, 'fstab'),
		resolvConf: stringField(payload, 'resolvConf'),
		devfsRuleset: stringField(payload, 'devfsRuleset'),
		execTimeout: 120,
		additionalOptions: stringField(payload, 'additionalOptions'),
		allowedOptions,
		metadataMeta: stringField(payload, 'metadataMeta'),
		metadataEnv: stringField(payload, 'metadataEnv'),
		cores: Math.trunc(numberField(payload, 'cores', 1)),
		memory: Math.trunc(numberField(payload, 'memory', GIB)),
		resourceLimits: booleanField(payload, 'resourceLimits', true)
	};
	fullJail(jail);
	const primaryNetwork = jail.networks?.[0];
	if (primaryNetwork) {
		const selectedSwitch = demoSwitch(stringField(payload, 'switchName', 'production'));
		primaryNetwork.switchId = selectedSwitch.id;
		primaryNetwork.switchType = selectedSwitch.type;
		primaryNetwork.macId = numberField(payload, 'mac') || null;
		primaryNetwork.ipv4Id = numberField(payload, 'ipv4') || null;
		primaryNetwork.ipv4GwId = numberField(payload, 'ipv4Gw') || null;
		primaryNetwork.ipv6Id = numberField(payload, 'ipv6') || null;
		primaryNetwork.ipv6GwId = numberField(payload, 'ipv6Gw') || null;
		primaryNetwork.dhcp = booleanField(payload, 'dhcp');
		primaryNetwork.slaac = booleanField(payload, 'slaac');
		primaryNetwork.defaultGateway = true;
		primaryNetwork.vlan = Math.max(0, Math.min(4095, Math.trunc(numberField(payload, 'vlan'))));
	}

	jails[hostname].push(jail);
	registerGuestId(hostname, ctId);
	return success({ ctId, name, node: hostname, outcome: 'created' });
}

function updateDemoJailDetails(jail: DemoJail, path: string, payload: Record<string, unknown>) {
	if (path === 'name') jail.name = stringField(payload, 'name', jail.name).trim() || jail.name;
	else if (path === 'description') jail.description = stringField(payload, 'description');
	else if (path === 'hardware/cpu') {
		jail.cores = Math.max(1, Math.trunc(numberField(payload, 'cores', jail.cores ?? 1)));
	} else if (path === 'hardware/ram') {
		jail.memory = Math.max(
			64 * 1024 ** 2,
			Math.trunc(numberField(payload, 'memory', jail.memory ?? GIB))
		);
	} else if (path === 'hardware/resource-limits') {
		jail.resourceLimits = booleanField(payload, 'enabled', jail.resourceLimits ?? true);
	} else if (path === 'options/boot-order') {
		jail.startAtBoot = booleanField(payload, 'startAtBoot', jail.startAtBoot ?? true);
		jail.startOrder = Math.max(
			0,
			Math.trunc(numberField(payload, 'bootOrder', jail.startOrder ?? 0))
		);
	} else if (path === 'options/wol') jail.wol = booleanField(payload, 'enabled', jail.wol ?? true);
	else if (path === 'options/execution-timeout') {
		jail.execTimeout = Math.max(
			1,
			Math.trunc(numberField(payload, 'execTimeout', jail.execTimeout ?? 120))
		);
	} else if (path === 'options/fstab') jail.fstab = stringField(payload, 'fstab');
	else if (path === 'options/resolv-conf') jail.resolvConf = stringField(payload, 'resolvConf');
	else if (path === 'options/devfs-rules') jail.devfsRuleset = stringField(payload, 'devFSRules');
	else if (path === 'options/additional-options') {
		jail.additionalOptions = stringField(payload, 'additionalOptions');
	} else if (path === 'options/allowed-options') {
		jail.allowedOptions = Array.isArray(payload.allowedOptions)
			? payload.allowedOptions.filter((value): value is string => typeof value === 'string')
			: [];
	} else if (path === 'options/metadata') {
		jail.metadataMeta = stringField(payload, 'metadata');
		jail.metadataEnv = stringField(payload, 'env');
	} else if (path === 'options/lifecycle-hooks') {
		const hooks =
			typeof payload.hooks === 'object' && payload.hooks !== null
				? (payload.hooks as Record<string, Record<string, unknown>>)
				: {};
		jail.jailHooks = Object.entries(hooks).map(([phase, value]) => ({
			phase,
			enabled: booleanField(value, 'enabled'),
			script: stringField(value, 'script')
		}));
	}
}

function attachDemoJailNetwork(jail: DemoJail, payload: Record<string, unknown>) {
	fullJail(jail);
	const networks = jail.networks!;
	const id = nextRecordId(networks);
	const selectedSwitch = demoSwitch(stringField(payload, 'switchName', 'production'));
	const network: Record<string, unknown> = {
		id,
		jid: jail.id,
		name: stringField(payload, 'name', `epair${networks.length}`),
		switchId: selectedSwitch.id,
		switchType: selectedSwitch.type,
		macId: numberField(payload, 'macId') || null,
		macObj: null,
		ipv4Id: numberField(payload, 'ip4') || null,
		ipv4GwId: numberField(payload, 'ip4gw') || null,
		ipv6Id: numberField(payload, 'ip6') || null,
		ipv6GwId: numberField(payload, 'ip6gw') || null,
		dhcp: booleanField(payload, 'dhcp'),
		slaac: booleanField(payload, 'slaac'),
		defaultGateway: booleanField(payload, 'defaultGateway'),
		vlan: Math.max(0, Math.min(4095, Math.trunc(numberField(payload, 'vlan'))))
	};
	networks.push(network);
	return network;
}

function createDemoJailSnapshot(jail: DemoJail, payload: Record<string, unknown>) {
	fullJail(jail);
	const snapshots = jail.snapshots!;
	const id = nextRecordId(snapshots);
	const timestamp = new Date().toISOString();
	const name = stringField(payload, 'name', `snapshot-${id}`).trim() || `snapshot-${id}`;
	const snapshot = {
		id,
		jid: jail.id,
		ctId: jail.ctId,
		parentSnapshotId: null,
		name,
		description: stringField(payload, 'description'),
		snapshotName: `sylve-${jail.ctId}-${name.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`,
		rootDataset: `atlas/sylve/jails/${jail.ctId}`,
		createdAt: timestamp,
		updatedAt: timestamp
	};
	snapshots.unshift(snapshot);
	return snapshot;
}

export async function handleDemoRequest<T = unknown>(
	config: DemoRequestConfig
): Promise<DemoClientResponse<T>> {
	const parsed = new URL(config.url, 'https://demo.sylve.invalid');
	const path = parsed.pathname.startsWith('/api/') ? parsed.pathname.slice(4) : parsed.pathname;
	const method = (config.method || 'GET').toUpperCase();
	const hostname = hostnameFor(config);
	const networkResponse = handleDemoNetworkRequest<T>(config, parsed, path, method, hostname);
	if (networkResponse) return networkResponse;
	const storageResponse = handleDemoStorageRequest<T>(config, parsed, path, method, hostname);
	if (storageResponse) return storageResponse;
	const adminResponse = handleDemoAdminRequest<T>(config, parsed, path, method, hostname);
	if (adminResponse) return adminResponse;

	if (path === '/cluster') return success(clusterDetails) as DemoClientResponse<T>;
	if (path === '/cluster/join-key' && method === 'GET') {
		return success({ key: 'sylve-demo-cluster-key-7F4C2A91' }) as DemoClientResponse<T>;
	}
	if (path === '/cluster/reset-node' && method === 'DELETE') {
		return mutationSuccess('raft_node_reset') as DemoClientResponse<T>;
	}
	if (path === '/cluster/nodes') return success(liveNodes()) as DemoClientResponse<T>;
	if (path === '/cluster/resources') {
		return success(
			nodes.map((node) => ({
				nodeUUID: node.nodeUUID,
				hostname: node.hostname,
				jails: jails[node.hostname],
				jailTemplates: [],
				vms: virtualMachines[node.hostname],
				vmTemplates: []
			}))
		) as DemoClientResponse<T>;
	}
	if (path === '/cluster/notes' && method === 'GET') {
		return success(clusterNotes) as DemoClientResponse<T>;
	}
	if (path === '/cluster/notes' && method === 'POST') {
		const payload = requestPayload(config);
		const title = stringField(payload, 'title').trim();
		const content = stringField(payload, 'content').trim();

		if (!title || !content) {
			return failure(
				'invalid_note',
				'A note name and content are required.',
				400
			) as DemoClientResponse<T>;
		}

		const timestamp = new Date().toISOString();
		const note: DemoNote = {
			id: Math.max(0, ...clusterNotes.map((entry) => entry.id)) + 1,
			title,
			content,
			createdAt: timestamp,
			updatedAt: timestamp
		};

		clusterNotes.unshift(note);
		return mutationSuccess('note_created', 201) as DemoClientResponse<T>;
	}
	if (path === '/cluster/notes/bulk-delete' && method === 'POST') {
		const payload = requestPayload(config);
		const ids = new Set(
			(Array.isArray(payload.ids) ? payload.ids : [])
				.map((id) => Number(id))
				.filter((id) => Number.isInteger(id))
		);

		for (let index = clusterNotes.length - 1; index >= 0; index -= 1) {
			if (ids.has(clusterNotes[index].id)) {
				clusterNotes.splice(index, 1);
			}
		}

		return mutationSuccess('notes_deleted') as DemoClientResponse<T>;
	}

	const clusterNoteMatch = path.match(/^\/cluster\/notes\/(\d+)$/);
	if (clusterNoteMatch && method === 'PUT') {
		const note = clusterNotes.find((entry) => entry.id === Number(clusterNoteMatch[1]));

		if (!note) {
			return failure(
				'note_not_found',
				'The selected note no longer exists.',
				404
			) as DemoClientResponse<T>;
		}

		const payload = requestPayload(config);
		const title = stringField(payload, 'title').trim();
		const content = stringField(payload, 'content').trim();

		if (!title || !content) {
			return failure(
				'invalid_note',
				'A note name and content are required.',
				400
			) as DemoClientResponse<T>;
		}

		note.title = title;
		note.content = content;
		note.updatedAt = new Date().toISOString();
		return mutationSuccess('note_updated') as DemoClientResponse<T>;
	}
	if (clusterNoteMatch && method === 'DELETE') {
		const index = clusterNotes.findIndex((entry) => entry.id === Number(clusterNoteMatch[1]));

		if (index === -1) {
			return failure(
				'note_not_found',
				'The selected note no longer exists.',
				404
			) as DemoClientResponse<T>;
		}

		clusterNotes.splice(index, 1);
		return mutationSuccess('note_deleted') as DemoClientResponse<T>;
	}

	if (path === '/cluster/backups/targets' && method === 'GET') {
		return success(backupTargets) as DemoClientResponse<T>;
	}
	if (path === '/cluster/backups/targets' && method === 'POST') {
		return createDemoBackupTarget(config) as DemoClientResponse<T>;
	}
	const backupTargetValidateMatch = path.match(/^\/cluster\/backups\/targets\/(\d+)\/validate$/);
	if (backupTargetValidateMatch && method === 'POST') {
		const target = backupTargets.find((entry) => entry.id === Number(backupTargetValidateMatch[1]));
		if (!target) {
			return failure(
				'backup_target_not_found',
				'backup_target_not_found',
				404
			) as DemoClientResponse<T>;
		}

		const nodeId = parsed.searchParams.get('nodeId')?.trim() || clusterDetails.nodeId;
		const readiness = target.readiness.find((entry) => entry.nodeId === nodeId);
		if (!readiness) {
			return failure(
				'validation_node_invalid',
				'Select a current cluster voter.',
				400
			) as DemoClientResponse<T>;
		}
		const verifiedAt = new Date();
		readiness.validationSucceeded = true;
		readiness.lastVerifiedAt = verifiedAt.toISOString();
		readiness.readyUntil = new Date(verifiedAt.getTime() + 24 * 60 * 60 * 1000).toISOString();
		readiness.lastError = '';
		readiness.ready = true;
		readiness.expired = false;
		readiness.configurationCurrent = true;
		readiness.revision += 1;
		return mutationSuccess('target_validated') as DemoClientResponse<T>;
	}
	const backupTargetRunningMatch = path.match(/^\/cluster\/backups\/targets\/(\d+)\/running-jobs$/);
	if (backupTargetRunningMatch && method === 'GET') {
		const targetId = Number(backupTargetRunningMatch[1]);
		return success(
			backupJobs
				.filter((job) => job.targetId === targetId && job.lastStatus === 'running')
				.map((job) => job.id)
		) as DemoClientResponse<T>;
	}
	const backupTargetMatch = path.match(/^\/cluster\/backups\/targets\/(\d+)$/);
	if (backupTargetMatch && method === 'PUT') {
		return updateDemoBackupTarget(Number(backupTargetMatch[1]), config) as DemoClientResponse<T>;
	}
	if (backupTargetMatch && method === 'DELETE') {
		const targetId = Number(backupTargetMatch[1]);
		const index = backupTargets.findIndex((target) => target.id === targetId);
		if (index === -1) {
			return failure(
				'backup_target_not_found',
				'backup_target_not_found',
				404
			) as DemoClientResponse<T>;
		}
		backupTargets.splice(index, 1);
		for (let jobIndex = backupJobs.length - 1; jobIndex >= 0; jobIndex -= 1) {
			if (backupJobs[jobIndex].targetId === targetId) backupJobs.splice(jobIndex, 1);
		}
		return mutationSuccess('backup_target_deleted') as DemoClientResponse<T>;
	}

	if (path === '/cluster/backups/jobs' && method === 'GET') {
		const targetId = Number(parsed.searchParams.get('targetId') || 0);
		const guestType = parsed.searchParams.get('guestType') || '';
		const guestId = Number(parsed.searchParams.get('guestId') || 0);
		const filtered = backupJobs.filter((job) => {
			if (targetId > 0 && job.targetId !== targetId) return false;
			if (!guestType || guestId <= 0) return true;
			const source = job.mode === 'jail' ? job.jailRootDataset : job.sourceDataset;
			return job.mode === guestType && source.includes(`/${guestId}`);
		});
		return success(filtered.map(publicBackupJob)) as DemoClientResponse<T>;
	}
	if (path === '/cluster/backups/jobs' && method === 'POST') {
		return createDemoBackupJob(config) as DemoClientResponse<T>;
	}
	const backupJobRunMatch = path.match(/^\/cluster\/backups\/jobs\/(\d+)\/run$/);
	if (backupJobRunMatch && method === 'POST') {
		return runDemoBackupJob(Number(backupJobRunMatch[1])) as DemoClientResponse<T>;
	}
	const backupJobSnapshotsMatch = path.match(/^\/cluster\/backups\/jobs\/(\d+)\/snapshots$/);
	if (backupJobSnapshotsMatch && method === 'GET') {
		const job = backupJobs.find((entry) => entry.id === Number(backupJobSnapshotsMatch[1]));
		if (!job) {
			return failure('backup_job_not_found', 'backup_job_not_found', 404) as DemoClientResponse<T>;
		}
		const dataset = job.mode === 'jail' ? job.jailRootDataset : job.sourceDataset;
		return success([
			{
				name: `${dataset}@gen-mssaurk0`,
				shortName: '@gen-mssaurk0',
				dataset,
				encrypted: job.encrypted,
				creation: '2026-08-14T02:00:00.000Z',
				used: '18.6G',
				refer: '42.3G',
				lineage: 'active',
				outOfBand: false,
				committed: true,
				legacy: false,
				childCount: 0
			},
			{
				name: `${dataset}@gen-msqveww0`,
				shortName: '@gen-msqveww0',
				dataset,
				encrypted: job.encrypted,
				creation: '2026-08-13T02:00:00.000Z',
				used: '3.2G',
				refer: '41.7G',
				lineage: 'active',
				outOfBand: false,
				committed: true,
				legacy: false,
				childCount: 0
			}
		]) as DemoClientResponse<T>;
	}
	const backupJobRestoreMatch = path.match(/^\/cluster\/backups\/jobs\/(\d+)\/restore$/);
	if (backupJobRestoreMatch && method === 'POST') {
		return mutationSuccess('restore_job_started', 202) as DemoClientResponse<T>;
	}
	const backupJobMatch = path.match(/^\/cluster\/backups\/jobs\/(\d+)$/);
	if (backupJobMatch && method === 'PUT') {
		return updateDemoBackupJob(Number(backupJobMatch[1]), config) as DemoClientResponse<T>;
	}
	if (backupJobMatch && method === 'DELETE') {
		const index = backupJobs.findIndex((job) => job.id === Number(backupJobMatch[1]));
		if (index === -1) {
			return failure('backup_job_not_found', 'backup_job_not_found', 404) as DemoClientResponse<T>;
		}
		backupJobs.splice(index, 1);
		return mutationSuccess('backup_job_deleted') as DemoClientResponse<T>;
	}

	if (path === '/cluster/backups/events/remote' && method === 'GET') {
		const page = Math.max(1, Number(parsed.searchParams.get('page') || 1));
		const size = Math.max(1, Math.min(100, Number(parsed.searchParams.get('size') || 25)));
		const sortField = parsed.searchParams.get('sort[0][field]') || 'startedAt';
		const sortDirection = parsed.searchParams.get('sort[0][dir]') === 'asc' ? 1 : -1;
		const events = [...filteredBackupEvents(parsed)].sort((left, right) => {
			const leftValue = String(left[sortField as keyof DemoBackupEvent] ?? '');
			const rightValue = String(right[sortField as keyof DemoBackupEvent] ?? '');
			return leftValue.localeCompare(rightValue) * sortDirection;
		});
		const offset = (page - 1) * size;
		return success({
			last_page: Math.max(1, Math.ceil(events.length / size)),
			data: events.slice(offset, offset + size).map(publicBackupEvent)
		}) as DemoClientResponse<T>;
	}
	if (path === '/cluster/backups/events' && method === 'GET') {
		const limit = Math.max(1, Math.min(500, Number(parsed.searchParams.get('limit') || 200)));
		return success(
			filteredBackupEvents(parsed).slice(0, limit).map(publicBackupEvent)
		) as DemoClientResponse<T>;
	}
	const backupEventProgressMatch = path.match(/^\/cluster\/backups\/events\/(\d+)\/progress$/);
	if (backupEventProgressMatch && method === 'GET') {
		const event = backupEvents.find((entry) => entry.id === Number(backupEventProgressMatch[1]));
		if (!event) {
			return failure(
				'backup_event_not_found',
				'backup_event_not_found',
				404
			) as DemoClientResponse<T>;
		}
		const complete = event.status === 'success';
		return success({
			event: publicBackupEvent(event),
			progressDataset: event.sourceDataset.split('@')[0],
			phase: complete ? 'complete' : 'sending',
			movedBytes: complete ? 18.6 * GIB : 11.8 * GIB,
			totalBytes: 18.6 * GIB,
			progressPercent: complete ? 100 : 63.4
		}) as DemoClientResponse<T>;
	}
	const backupEventMatch = path.match(/^\/cluster\/backups\/events\/(\d+)$/);
	if (backupEventMatch && method === 'GET') {
		const event = backupEvents.find((entry) => entry.id === Number(backupEventMatch[1]));
		return (
			event
				? success(publicBackupEvent(event))
				: failure('backup_event_not_found', 'backup_event_not_found', 404)
		) as DemoClientResponse<T>;
	}

	if (path === '/basic/settings' || path === '/system/basic-settings') {
		return success({
			pools: ['atlas'],
			services: [
				'virtualization',
				'jails',
				'dhcp-server',
				'samba-server',
				'wol-server',
				'firewall',
				'wireguard',
				'iscsi',
				'mdns'
			],
			initialized: true
		}) as DemoClientResponse<T>;
	}
	if (path === '/system/pci-devices' && method === 'GET') {
		return success(demoPCIDevices) as DemoClientResponse<T>;
	}
	if (path === '/system/ppt-devices' && method === 'GET') {
		return success(demoPPTDevices) as DemoClientResponse<T>;
	}
	if (path === '/utilities/downloads/utype' && method === 'GET') {
		return success(
			demoDownloads.map((download) => ({
				uuid: download.uuid,
				label: download.name,
				uType: download.uType
			}))
		) as DemoClientResponse<T>;
	}
	if (path === '/utilities/downloads' && method === 'GET') {
		return success(demoDownloads) as DemoClientResponse<T>;
	}
	if (path === '/utilities/cloud-init/templates' && method === 'GET') {
		return success([
			{
				id: 1,
				name: 'Demo server',
				user: '#cloud-config\nusers:\n  - name: sylve',
				meta: 'instance-id: sylve-demo\nlocal-hostname: demo-vm',
				networkConfig: '',
				createdAt: '2026-08-11T10:04:00.000Z',
				updatedAt: '2026-08-11T10:04:00.000Z'
			}
		]) as DemoClientResponse<T>;
	}
	if (path === '/zfs/datasets' && method === 'GET') {
		const type = parsed.searchParams.get('type') || 'ALL';
		const datasets = demoDatasets(hostname);
		return success(
			type === 'ALL' || type === 'FILESYSTEM' ? datasets : []
		) as DemoClientResponse<T>;
	}
	if (path === '/zfs/pools' && method === 'GET') {
		return success(demoPools) as DemoClientResponse<T>;
	}
	if (path === '/jail/bootstraps' && method === 'GET') {
		const pool = parsed.searchParams.get('pool') || 'atlas';
		return success(demoBootstraps.filter((entry) => entry.pool === pool)) as DemoClientResponse<T>;
	}
	if (path === '/jail/bootstraps' && method === 'POST') {
		const payload = requestPayload(config);
		const pool = stringField(payload, 'pool', 'atlas');
		const major = Math.trunc(numberField(payload, 'major', 15));
		const minor = Math.trunc(numberField(payload, 'minor'));
		const type = stringField(payload, 'type', 'freebsd');
		const name = `${type}-${major}.${minor}`;
		let entry = demoBootstraps.find(
			(candidate) => candidate.pool === pool && candidate.name === name
		);
		const alreadyCompleted = entry?.status === 'completed';

		if (!entry) {
			entry = {
				pool,
				name,
				label: `${type === 'freebsd' ? 'FreeBSD' : type} ${major}.${minor}`,
				dataset: `${pool}/sylve/bootstrap/${name}`,
				mountPoint: `/${pool}/sylve/bootstrap/${name}`,
				major,
				minor,
				type,
				exists: true,
				status: 'completed',
				phase: '',
				error: ''
			};
			demoBootstraps.push(entry);
		} else {
			entry.exists = true;
			entry.status = 'completed';
			entry.phase = '';
			entry.error = '';
		}

		return success({
			pool,
			name,
			status: 'completed',
			outcome: alreadyCompleted ? 'already_completed' : 'queued'
		}) as DemoClientResponse<T>;
	}
	const bootstrapMatch = path.match(/^\/jail\/bootstraps\/([^/]+)$/);
	if (bootstrapMatch && method === 'DELETE') {
		const pool = parsed.searchParams.get('pool') || 'atlas';
		const name = decodeURIComponent(bootstrapMatch[1]);
		const entry = demoBootstraps.find(
			(candidate) => candidate.pool === pool && candidate.name === name
		);
		if (entry) {
			entry.exists = false;
			entry.status = '';
			entry.phase = '';
			entry.error = '';
		}
		return success({
			pool,
			name,
			outcome: entry ? 'deleted' : 'already_absent',
			datasetDeleted: Boolean(entry),
			recordDeleted: Boolean(entry)
		}) as DemoClientResponse<T>;
	}

	if (path === '/info/basic') {
		const nodeIndex = nodes.findIndex((node) => node.hostname === hostname);
		return success({
			hostname,
			os: 'FreeBSD 15.0-RELEASE',
			uptime: [1573884, 830244, 1248060][nodeIndex] || 1573884,
			loadAverage: ['0.31, 0.38, 0.35', '0.42, 0.51, 0.48', '0.19, 0.24, 0.21'][nodeIndex],
			bootMode: 'UEFI',
			sylveVersion: '0.3.0',
			sylveCommit: 'public-demo',
			devFSDisabled: false
		}) as DemoClientResponse<T>;
	}
	if (path === '/info/cpu') return success(nodeCPU(hostname)) as DemoClientResponse<T>;
	if (path === '/info/ram') return success(nodeRAM(hostname)) as DemoClientResponse<T>;
	if (path === '/info/swap') {
		const total = 8 * GIB;
		const usedPercent = demoUsagePercent(`${hostname}:swap`, 'memory');
		return success({
			total,
			free: total * (1 - usedPercent / 100),
			usedPercent
		}) as DemoClientResponse<T>;
	}
	if (path === '/info/summary/history' || path === '/info/summary/history/delta') {
		return success(historyPoints(hostname)) as DemoClientResponse<T>;
	}
	if (path === '/zfs/pools/disks-usage') {
		return success({
			total: hostname === 'paul' ? 4 * TIB : hostname === 'alia' ? 12 * TIB : 8 * TIB,
			usage: demoCapacityPercent(
				hostname,
				hostname === 'alia' ? 38.6 : hostname === 'paul' ? 46.3 : 52.1
			)
		}) as DemoClientResponse<T>;
	}
	if (path === '/info/audit-records')
		return success(auditRecords(hostname)) as DemoClientResponse<T>;
	if (path === '/tasks/migration/validate' && method === 'GET') {
		const guestType = parsed.searchParams.get('guestType');
		const guestId = Number(parsed.searchParams.get('guestId'));
		const targetNodeUuid = parsed.searchParams.get('targetNodeUuid') || '';
		if ((guestType !== 'vm' && guestType !== 'jail') || !Number.isInteger(guestId)) {
			return failure(
				'invalid_migration_request',
				'invalid_migration_request',
				400
			) as DemoClientResponse<T>;
		}
		return success(
			migrationValidation(guestType, guestId, targetNodeUuid, hostname)
		) as DemoClientResponse<T>;
	}
	if (path === '/tasks/lifecycle/recent' && method === 'GET') {
		const guestType = parsed.searchParams.get('guestType');
		const guestId = Number(parsed.searchParams.get('guestId'));
		const limit = Math.max(1, Number(parsed.searchParams.get('limit')) || 50);
		const filtered = demoLifecycleTasks.filter(
			(task) =>
				task.sourceHostname === hostname &&
				(!guestType || task.guestType === guestType) &&
				(!guestId || task.guestId === guestId)
		);
		for (const task of filtered) refreshDemoMigrationTask(task);
		return success(filtered.slice(0, limit).map(publicLifecycleTask)) as DemoClientResponse<T>;
	}
	if (path === '/tasks/lifecycle/active' && method === 'GET') {
		const guestType = parsed.searchParams.get('guestType');
		const guestId = Number(parsed.searchParams.get('guestId'));
		const filtered = demoLifecycleTasks.filter(
			(task) =>
				task.sourceHostname === hostname &&
				(!guestType || task.guestType === guestType) &&
				(!guestId || task.guestId === guestId)
		);
		for (const task of filtered) refreshDemoMigrationTask(task);
		return success(
			filtered
				.filter((task) => task.status === 'queued' || task.status === 'running')
				.map(publicLifecycleTask)
		) as DemoClientResponse<T>;
	}
	const activeLifecycleMatch = path.match(/^\/tasks\/lifecycle\/active\/(vm|jail)\/(\d+)$/);
	if (activeLifecycleMatch && method === 'GET') {
		const task = activeDemoMigrationTask(
			activeLifecycleMatch[1] as 'vm' | 'jail',
			Number(activeLifecycleMatch[2]),
			hostname
		);
		return success(task ? publicLifecycleTask(task) : null) as DemoClientResponse<T>;
	}
	const cancelMigrationMatch = path.match(/^\/tasks\/migration\/(\d+)\/cancel$/);
	if (cancelMigrationMatch && method === 'POST') {
		const task = demoLifecycleTasks.find(
			(candidate) =>
				candidate.id === Number(cancelMigrationMatch[1]) && candidate.sourceHostname === hostname
		);
		if (!task)
			return failure('migration_not_found', 'migration_not_found', 404) as DemoClientResponse<T>;
		refreshDemoMigrationTask(task);
		if (task.status !== 'queued' && task.status !== 'running') {
			return failure(
				'cancel_not_allowed_in_current_phase',
				'cancel_not_allowed_in_current_phase',
				409
			) as DemoClientResponse<T>;
		}
		task.status = 'failed';
		task.message = 'migration_cancelled';
		task.error = 'cancelled_by_user';
		task.overrideRequested = true;
		task.finishedAt = new Date().toISOString();
		task.updatedAt = task.finishedAt;
		return mutationSuccess('migration_cancelled') as DemoClientResponse<T>;
	}

	if (path === '/notifications/count' && method === 'GET') {
		return success({
			active: demoNotifications.filter((notification) => !notification.dismissedAt).length
		}) as DemoClientResponse<T>;
	}
	if (path === '/notifications' && method === 'GET') {
		const scope = parsed.searchParams.get('scope') === 'all' ? 'all' : 'active';
		const limit = Math.max(1, Number(parsed.searchParams.get('limit')) || 50);
		const offset = Math.max(0, Number(parsed.searchParams.get('offset')) || 0);
		const filtered =
			scope === 'all'
				? demoNotifications
				: demoNotifications.filter((notification) => !notification.dismissedAt);

		return success({
			items: filtered.slice(offset, offset + limit),
			total: filtered.length
		}) as DemoClientResponse<T>;
	}
	if (path === '/notifications/dismiss-all' && method === 'POST') {
		const dismissedAt = new Date().toISOString();
		let dismissed = 0;

		for (const notification of demoNotifications) {
			if (notification.dismissedAt) continue;
			notification.dismissedAt = dismissedAt;
			notification.updatedAt = dismissedAt;
			dismissed += 1;
		}

		return success({ dismissed }) as DemoClientResponse<T>;
	}
	const notificationDismissMatch = path.match(/^\/notifications\/(\d+)\/dismiss$/);
	if (notificationDismissMatch && method === 'POST') {
		const notification = demoNotifications.find(
			(candidate) => candidate.id === Number(notificationDismissMatch[1])
		);
		if (!notification) {
			return failure(
				'notification_not_found',
				'notification_not_found',
				404
			) as DemoClientResponse<T>;
		}

		const dismissedAt = new Date().toISOString();
		notification.dismissedAt = dismissedAt;
		notification.updatedAt = dismissedAt;
		return mutationSuccess('notification_dismissed') as DemoClientResponse<T>;
	}

	if (path === '/vm/simple') return success(virtualMachines[hostname]) as DemoClientResponse<T>;
	if (path === '/vm/templates') return success([]) as DemoClientResponse<T>;
	if (path === '/vm' && method === 'POST') {
		return createDemoVM(config, hostname) as DemoClientResponse<T>;
	}
	if (path === '/vm' && method === 'GET') {
		return success(virtualMachines[hostname].map(fullVM)) as DemoClientResponse<T>;
	}

	let match = path.match(/^\/vm\/simple\/(\d+)$/);
	if (match) {
		const found = findVM(Number(match[1]));
		return (found ? success(found.vm) : missing(path)) as DemoClientResponse<T>;
	}
	match = path.match(/^\/vm\/(\d+)\/domain$/);
	if (match) {
		const rid = Number(match[1]);
		const found = findVM(rid);
		const lifecycleTask = activeDemoMigrationTask('vm', rid, hostname);
		return (
			found
				? success({
						id: found.vm.state === 1 ? found.vm.id : -1,
						uuid: `demo-vm-${found.vm.rid}`,
						name: found.vm.name,
						status: found.vm.state === 1 ? 'Running' : 'Shutoff',
						pendingAction: lifecycleTask?.action || '',
						overrideRequested: lifecycleTask?.overrideRequested || false
					})
				: missing(path)
		) as DemoClientResponse<T>;
	}
	match = path.match(/^\/vm\/(\d+)\/stats(?:\/[^/]+)?$/);
	if (match) {
		const rid = Number(match[1]);
		const points = vmStats(rid);
		return success(
			path.split('/').length > 4
				? points
				: {
						points,
						resolvedStep: 'hourly',
						lastSampleAt: points.at(-1)?.createdAt ?? null,
						historyState: 'available'
					}
		) as DemoClientResponse<T>;
	}
	match = path.match(/^\/vm\/(\d+)\/logs$/);
	if (match) {
		return success({
			logs: 'Aug 14 08:42:13 bhyve: guest started\nAug 14 08:42:14 tap0: link state changed to UP'
		}) as DemoClientResponse<T>;
	}
	match = path.match(/^\/vm\/(\d+)\/migrations$/);
	if (match && method === 'POST') {
		return createDemoMigration(
			'vm',
			Number(match[1]),
			stringField(requestPayload(config), 'targetNodeUuid'),
			hostname
		) as DemoClientResponse<T>;
	}
	match = path.match(/^\/vm\/(\d+)\/actions\/(start|stop|shutdown|reboot)$/);
	if (match && method === 'POST') {
		const result = updateVmState(Number(match[1]), match[2]);
		return (result ? success(result) : missing(path)) as DemoClientResponse<T>;
	}
	match = path.match(/^\/vm\/(\d+)\/(name|description)$/);
	if (match && method === 'PATCH') {
		const found = findVM(Number(match[1]));
		if (!found) return missing(path) as DemoClientResponse<T>;
		const payload = requestPayload(config);
		if (
			match[2] === 'name' &&
			virtualMachines[found.hostname].some(
				(vm) =>
					vm.rid !== found.vm.rid &&
					vm.name.toLowerCase() === stringField(payload, 'name').trim().toLowerCase()
			)
		) {
			return failure(
				'vm_name_already_in_use',
				'A VM with this name already exists.'
			) as DemoClientResponse<T>;
		}
		updateDemoVMDetails(found.vm, match[2], payload);
		return mutationSuccess(`vm_${match[2]}_updated`) as DemoClientResponse<T>;
	}
	match = path.match(
		/^\/vm\/(\d+)\/(hardware\/(?:cpu|ram|vnc|pci-devices)|options\/(?:wol|ignore-umsrs|qemu-guest-agent|tpm|boot-order|clock|boot-rom|serial-console|shutdown-wait-time|cloud-init|extra-bhyve-options))$/
	);
	if (match && method === 'PUT') {
		const found = findVM(Number(match[1]));
		if (!found) return missing(path) as DemoClientResponse<T>;
		updateDemoVMDetails(found.vm, match[2], requestPayload(config));
		return mutationSuccess('vm_configuration_updated') as DemoClientResponse<T>;
	}
	match = path.match(/^\/vm\/(\d+)\/storage$/);
	if (match && method === 'POST') {
		const found = findVM(Number(match[1]));
		return (
			found ? success(attachDemoVMStorage(found.vm, requestPayload(config))) : missing(path)
		) as DemoClientResponse<T>;
	}
	match = path.match(/^\/vm\/(\d+)\/storage\/(\d+)$/);
	if (match && (method === 'PATCH' || method === 'DELETE')) {
		const found = findVM(Number(match[1]));
		if (!found) return missing(path) as DemoClientResponse<T>;
		fullVM(found.vm);
		const storageId = Number(match[2]);
		const index = found.vm.storages!.findIndex((storage) => Number(storage.id) === storageId);
		if (index === -1) return missing(path) as DemoClientResponse<T>;
		if (method === 'DELETE') {
			found.vm.storages!.splice(index, 1);
			return mutationSuccess('vm_storage_detached') as DemoClientResponse<T>;
		}
		const storage = found.vm.storages![index];
		const payload = requestPayload(config);
		for (const key of [
			'name',
			'size',
			'emulation',
			'bootOrder',
			'enable',
			'filesystemTarget',
			'readOnly'
		]) {
			if (payload[key] !== undefined) storage[key] = payload[key];
		}
		return success(storage) as DemoClientResponse<T>;
	}
	match = path.match(/^\/vm\/(\d+)\/networks$/);
	if (match && method === 'POST') {
		const found = findVM(Number(match[1]));
		return (
			found ? success(attachDemoVMNetwork(found.vm, requestPayload(config))) : missing(path)
		) as DemoClientResponse<T>;
	}
	match = path.match(/^\/vm\/(\d+)\/networks\/(\d+)$/);
	if (match && (method === 'PATCH' || method === 'DELETE')) {
		const found = findVM(Number(match[1]));
		if (!found) return missing(path) as DemoClientResponse<T>;
		fullVM(found.vm);
		const networkId = Number(match[2]);
		const index = found.vm.networks!.findIndex((network) => Number(network.id) === networkId);
		if (index === -1) return missing(path) as DemoClientResponse<T>;
		if (method === 'DELETE') {
			found.vm.networks!.splice(index, 1);
			return mutationSuccess('vm_network_detached') as DemoClientResponse<T>;
		}
		const network = found.vm.networks![index];
		const payload = requestPayload(config);
		if (payload.switchName !== undefined) {
			const selectedSwitch = demoSwitch(stringField(payload, 'switchName'));
			network.switchId = selectedSwitch.id;
			network.switchType = selectedSwitch.type;
		}
		if (payload.emulation !== undefined) network.emulation = payload.emulation;
		if (payload.macId !== undefined) network.macId = numberField(payload, 'macId') || null;
		if (payload.enable !== undefined) network.enable = booleanField(payload, 'enable');
		return success(network) as DemoClientResponse<T>;
	}
	match = path.match(/^\/vm\/(\d+)\/snapshots$/);
	if (match && (method === 'GET' || method === 'POST')) {
		const found = findVM(Number(match[1]));
		if (!found) return missing(path) as DemoClientResponse<T>;
		fullVM(found.vm);
		return success(
			method === 'POST'
				? createDemoVMSnapshot(found.vm, requestPayload(config))
				: found.vm.snapshots!
		) as DemoClientResponse<T>;
	}
	match = path.match(/^\/vm\/(\d+)\/snapshots\/(\d+)\/(rollback)$/);
	if (match && method === 'POST') {
		const found = findVM(Number(match[1]));
		if (!found) return missing(path) as DemoClientResponse<T>;
		const wasRunning = found.vm.state === 1;
		return success({ wasRunning, restarted: wasRunning, warnings: [] }) as DemoClientResponse<T>;
	}
	match = path.match(/^\/vm\/(\d+)\/snapshots\/(\d+)$/);
	if (match && method === 'DELETE') {
		const found = findVM(Number(match[1]));
		if (!found) return missing(path) as DemoClientResponse<T>;
		fullVM(found.vm);
		const snapshotId = Number(match[2]);
		const index = found.vm.snapshots!.findIndex((snapshot) => Number(snapshot.id) === snapshotId);
		if (index === -1) return missing(path) as DemoClientResponse<T>;
		found.vm.snapshots!.splice(index, 1);
		return mutationSuccess('vm_snapshot_deleted') as DemoClientResponse<T>;
	}
	match = path.match(/^\/vm\/(\d+)\/guest-agent$/);
	if (match && method === 'GET') {
		const found = findVM(Number(match[1]));
		if (!found) return missing(path) as DemoClientResponse<T>;
		return success({
			osInfo: {
				name: found.vm.rid === 120 ? 'Tiny Core Linux' : 'FreeBSD',
				'kernel-release': found.vm.rid === 120 ? '6.6.8-tinycore' : '15.0-RELEASE',
				version: found.vm.rid === 120 ? '15.0' : '15.0-RELEASE',
				'pretty-name': found.vm.rid === 120 ? 'Tiny Core Linux 15' : 'FreeBSD 15.0-RELEASE',
				'version-id': '15.0',
				'kernel-version': found.vm.rid === 120 ? '6.6.8' : 'FreeBSD 15.0',
				machine: 'i386',
				id: found.vm.rid === 120 ? 'tinycore' : 'freebsd'
			},
			interfaces: []
		}) as DemoClientResponse<T>;
	}
	match = path.match(/^\/vm\/(\d+)\/registration$/);
	if (match && method === 'DELETE') {
		const found = findVM(Number(match[1]));
		if (!found) return missing(path) as DemoClientResponse<T>;
		evictVMRuntime(found.vm.rid);
		virtualMachines[found.hostname].splice(virtualMachines[found.hostname].indexOf(found.vm), 1);
		unregisterGuestId(found.hostname, found.vm.rid);
		return success({
			warnings: [],
			retainedDatasets: [`atlas/sylve/virtual-machines/${found.vm.rid}`]
		}) as DemoClientResponse<T>;
	}
	match = path.match(/^\/vm\/(\d+)$/);
	if (match && method === 'GET') {
		const found = findVM(Number(match[1]));
		return (found ? success(fullVM(found.vm)) : missing(path)) as DemoClientResponse<T>;
	}
	if (match && method === 'DELETE') {
		const found = findVM(Number(match[1]));
		if (!found) return missing(path) as DemoClientResponse<T>;
		evictVMRuntime(found.vm.rid);
		virtualMachines[found.hostname].splice(virtualMachines[found.hostname].indexOf(found.vm), 1);
		unregisterGuestId(found.hostname, found.vm.rid);
		return success({ warnings: [], retainedDatasets: [] }) as DemoClientResponse<T>;
	}

	if (path === '/jail/simple') return success(jails[hostname]) as DemoClientResponse<T>;
	if (path === '/jail/templates') return success([]) as DemoClientResponse<T>;
	if (path === '/jail' && method === 'POST') {
		return createDemoJail(config, hostname) as DemoClientResponse<T>;
	}
	if (path === '/jail' && method === 'GET') {
		return success(jails[hostname].map(fullJail)) as DemoClientResponse<T>;
	}
	match = path.match(/^\/jail\/simple\/(\d+)$/);
	if (match) {
		const found = findJail(Number(match[1]));
		return (found ? success(found.jail) : missing(path)) as DemoClientResponse<T>;
	}
	match = path.match(/^\/jail\/(\d+)\/state$/);
	if (match) {
		const ctId = Number(match[1]);
		const found = findJail(ctId);
		const lifecycleTask = activeDemoMigrationTask('jail', ctId, hostname);
		const cpuUsage = demoUsagePercent(`jail-${ctId}`, 'cpu');
		const memoryUsage = demoUsagePercent(`jail-${ctId}`, 'memory');
		return (
			found
				? success({
						ctId: found.jail.ctId,
						state: found.jail.state,
						pcpu: found.jail.state === 'ACTIVE' ? cpuUsage : 0,
						memory:
							found.jail.state === 'ACTIVE'
								? (found.jail.memory ?? 4 * GIB) * (memoryUsage / 100)
								: 0,
						pendingAction: lifecycleTask?.action || '',
						overrideRequested: lifecycleTask?.overrideRequested || false
					})
				: missing(path)
		) as DemoClientResponse<T>;
	}
	match = path.match(/^\/jail\/(\d+)\/stats(?:\/[^/]+)?$/);
	if (match) {
		const ctId = Number(match[1]);
		const points = jailStats(ctId);
		return success(
			path.split('/').length > 4
				? points
				: {
						points,
						resolvedStep: 'hourly',
						lastSampleAt: points.at(-1)?.createdAt ?? null,
						historyState: 'available'
					}
		) as DemoClientResponse<T>;
	}
	match = path.match(/^\/jail\/(\d+)\/logs$/);
	if (match) {
		return success({
			logs: 'jail: created VNET stack\njail: started /etc/rc\njail: ready'
		}) as DemoClientResponse<T>;
	}
	match = path.match(/^\/jail\/(\d+)\/migrations$/);
	if (match && method === 'POST') {
		return createDemoMigration(
			'jail',
			Number(match[1]),
			stringField(requestPayload(config), 'targetNodeUuid'),
			hostname
		) as DemoClientResponse<T>;
	}
	match = path.match(/^\/jail\/(\d+)\/actions\/(start|stop|restart)$/);
	if (match && method === 'POST') {
		const result = updateJailState(Number(match[1]), match[2]);
		return (result ? success(result) : missing(path)) as DemoClientResponse<T>;
	}
	match = path.match(/^\/jail\/(\d+)\/(name|description)$/);
	if (match && method === 'PATCH') {
		const found = findJail(Number(match[1]));
		if (!found) return missing(path) as DemoClientResponse<T>;
		const payload = requestPayload(config);
		if (
			match[2] === 'name' &&
			jails[found.hostname].some(
				(jail) =>
					jail.ctId !== found.jail.ctId &&
					jail.name.toLowerCase() === stringField(payload, 'name').trim().toLowerCase()
			)
		) {
			return failure(
				'jail_name_already_in_use',
				'A jail with this name already exists.'
			) as DemoClientResponse<T>;
		}
		updateDemoJailDetails(found.jail, match[2], payload);
		return mutationSuccess(`jail_${match[2]}_updated`) as DemoClientResponse<T>;
	}
	match = path.match(
		/^\/jail\/(\d+)\/(hardware\/(?:cpu|ram|resource-limits)|options\/(?:boot-order|wol|fstab|resolv-conf|devfs-rules|additional-options|allowed-options|metadata|lifecycle-hooks))$/
	);
	if (match && method === 'PUT') {
		const found = findJail(Number(match[1]));
		if (!found) return missing(path) as DemoClientResponse<T>;
		updateDemoJailDetails(found.jail, match[2], requestPayload(config));
		return mutationSuccess('jail_configuration_updated') as DemoClientResponse<T>;
	}
	match = path.match(/^\/jail\/(\d+)\/network\/inheritance$/);
	if (match && method === 'PUT') {
		const found = findJail(Number(match[1]));
		if (!found) return missing(path) as DemoClientResponse<T>;
		const payload = requestPayload(config);
		const ipv4 = booleanField(payload, 'ipv4');
		const ipv6 = booleanField(payload, 'ipv6');
		found.jail.inheritIPv4 = ipv4;
		found.jail.inheritIPv6 = ipv6;
		fullJail(found.jail);
		const removedNetworkIds =
			ipv4 && ipv6 ? found.jail.networks!.map((network) => Number(network.id)) : [];
		if (removedNetworkIds.length) found.jail.networks = [];
		return success({
			ctId: found.jail.ctId,
			inheritIPv4: ipv4,
			inheritIPv6: ipv6,
			removedNetworkIds
		}) as DemoClientResponse<T>;
	}
	match = path.match(/^\/jail\/(\d+)\/networks$/);
	if (match && method === 'POST') {
		const found = findJail(Number(match[1]));
		return (
			found ? success(attachDemoJailNetwork(found.jail, requestPayload(config))) : missing(path)
		) as DemoClientResponse<T>;
	}
	match = path.match(/^\/jail\/(\d+)\/networks\/(\d+)$/);
	if (match && (method === 'PATCH' || method === 'DELETE')) {
		const found = findJail(Number(match[1]));
		if (!found) return missing(path) as DemoClientResponse<T>;
		fullJail(found.jail);
		const networkId = Number(match[2]);
		const index = found.jail.networks!.findIndex((network) => Number(network.id) === networkId);
		if (index === -1) return missing(path) as DemoClientResponse<T>;
		if (method === 'DELETE') {
			found.jail.networks!.splice(index, 1);
			return mutationSuccess('jail_network_deleted') as DemoClientResponse<T>;
		}
		const network = found.jail.networks![index];
		const payload = requestPayload(config);
		if (payload.switchName !== undefined) {
			const selectedSwitch = demoSwitch(stringField(payload, 'switchName'));
			network.switchId = selectedSwitch.id;
			network.switchType = selectedSwitch.type;
		}
		for (const [payloadKey, recordKey] of [
			['name', 'name'],
			['macId', 'macId'],
			['ip4', 'ipv4Id'],
			['ip4gw', 'ipv4GwId'],
			['ip6', 'ipv6Id'],
			['ip6gw', 'ipv6GwId'],
			['dhcp', 'dhcp'],
			['slaac', 'slaac'],
			['defaultGateway', 'defaultGateway'],
			['vlan', 'vlan']
		]) {
			if (payload[payloadKey] !== undefined) network[recordKey] = payload[payloadKey];
		}
		return success(network) as DemoClientResponse<T>;
	}
	match = path.match(/^\/jail\/(\d+)\/snapshots$/);
	if (match && (method === 'GET' || method === 'POST')) {
		const found = findJail(Number(match[1]));
		if (!found) return missing(path) as DemoClientResponse<T>;
		fullJail(found.jail);
		return success(
			method === 'POST'
				? createDemoJailSnapshot(found.jail, requestPayload(config))
				: found.jail.snapshots!
		) as DemoClientResponse<T>;
	}
	match = path.match(/^\/jail\/(\d+)\/snapshots\/(\d+)\/rollback$/);
	if (match && method === 'POST') {
		const found = findJail(Number(match[1]));
		if (!found) return missing(path) as DemoClientResponse<T>;
		const wasRunning = found.jail.state === 'ACTIVE';
		return success({ wasRunning, restarted: wasRunning, warnings: [] }) as DemoClientResponse<T>;
	}
	match = path.match(/^\/jail\/(\d+)\/snapshots\/(\d+)$/);
	if (match && method === 'DELETE') {
		const found = findJail(Number(match[1]));
		if (!found) return missing(path) as DemoClientResponse<T>;
		fullJail(found.jail);
		const snapshotId = Number(match[2]);
		const index = found.jail.snapshots!.findIndex((snapshot) => Number(snapshot.id) === snapshotId);
		if (index === -1) return missing(path) as DemoClientResponse<T>;
		found.jail.snapshots!.splice(index, 1);
		return mutationSuccess('jail_snapshot_deleted') as DemoClientResponse<T>;
	}
	match = path.match(/^\/jail\/(\d+)$/);
	if (match && method === 'GET') {
		const found = findJail(Number(match[1]));
		return (found ? success(fullJail(found.jail)) : missing(path)) as DemoClientResponse<T>;
	}
	if (match && method === 'DELETE') {
		const found = findJail(Number(match[1]));
		if (!found) return missing(path) as DemoClientResponse<T>;
		jails[found.hostname].splice(jails[found.hostname].indexOf(found.jail), 1);
		unregisterGuestId(found.hostname, found.jail.ctId);
		return success({ warnings: [], retainedDatasets: [] }) as DemoClientResponse<T>;
	}

	return missing(`${method} ${path}`) as DemoClientResponse<T>;
}
