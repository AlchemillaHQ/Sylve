/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type {
	NotificationConfig,
	NotificationRule,
	NotificationRuleTemplate,
	NotificationTransportInput
} from '$lib/types/notifications';
import type { Certificate, CertificateInput } from '$lib/types/services/certificates';
import type { PCIDevice, PPTDevice } from '$lib/types/system/pci';
import type { Tunable } from '$lib/types/system/tunables';
import type { CloudInitTemplate, CloudInitTemplateInput } from '$lib/types/utilities/cloud-init';
import type { Download, DownloadType } from '$lib/types/utilities/downloader';
import { demoVMProfiles } from './vm-profiles';

type DemoAdminRequestConfig = {
	url: string;
	method?: string;
	headers?: Record<string, string>;
	data?: unknown;
};

export type DemoAdminResponse<T = unknown> = {
	status: number;
	data: T;
	headers: Record<string, string>;
	ok: boolean;
};

type PendingUpload = { name: string; size: number };

type DemoAdminState = {
	downloads: Download[];
	pendingUploads: Record<string, PendingUpload>;
	cloudInitTemplates: CloudInitTemplate[];
	certificates: Certificate[];
	transports: NotificationConfig['transports'];
	rules: NotificationRule[];
	ruleTemplates: NotificationRuleTemplate[];
	pciDevices: PCIDevice[];
	pptDevices: PPTDevice[];
	tunables: Tunable[];
};

const createdAt = '2026-05-14T08:30:00.000Z';
const updatedAt = '2026-08-14T11:45:00.000Z';
const adminStates = new Map<string, DemoAdminState>();

function success<T>(
	data: T,
	message = 'demo_fixture_loaded',
	status = 200
): DemoAdminResponse<{ status: 'success'; message: string; error: ''; data: T }> {
	return {
		status,
		data: { status: 'success', message, error: '', data },
		headers: { 'content-type': 'application/json' },
		ok: true
	};
}

function mutationSuccess(message: string, status = 200): DemoAdminResponse {
	return success(null, message, status);
}

function failure(message: string, error: string, status = 404): DemoAdminResponse {
	return {
		status,
		data: { status: 'error', message, error },
		headers: { 'content-type': 'application/json' },
		ok: false
	};
}

function payload(config: DemoAdminRequestConfig): Record<string, unknown> {
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

function booleanValue(body: Record<string, unknown>, key: string, fallback = false): boolean {
	return typeof body[key] === 'boolean' ? body[key] : fallback;
}

function nextID(items: Array<{ id: number }>): number {
	return Math.max(0, ...items.map((item) => item.id)) + 1;
}

function createDownloads(): Download[] {
	const media: Download[] = demoVMProfiles.map((profile, index) => ({
		id: index + 1,
		uuid: profile.media.uuid,
		path: `/var/sylve/downloads/${profile.media.fileName}`,
		name: profile.media.fileName,
		type: 'http' as const,
		url: profile.media.url,
		progress: 100,
		size: profile.media.size,
		files: [],
		uType: 'uncategorized' as const,
		status: 'done' as const,
		automaticExtraction: false,
		automaticRawConversion: profile.media.fileName.endsWith('.img'),
		ignoreTLS: false,
		createdAt: `2026-08-${10 + index}T09:00:00.000Z`,
		updatedAt: `2026-08-${10 + index}T09:04:00.000Z`
	}));
	media.push({
		id: media.length + 1,
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
	});
	return media;
}

function createCertificates(hostname: string): Certificate[] {
	return [
		{
			id: 1,
			name: 'Sylve system certificate',
			type: 'system-default',
			domain: `${hostname}.sylve.local`,
			dynamicDnsEntryId: null,
			staging: false,
			fingerprint: '61:2D:91:A4:05:73:6B:70:AB:19:8C:42:59:77:1D:23',
			notBefore: '2026-05-14T08:30:00.000Z',
			notAfter: '2036-05-12T08:30:00.000Z',
			updatedAt,
			active: true,
			pending: false,
			ready: true,
			renewable: false,
			issuanceStatus: 'ready',
			issuanceOperation: '',
			issuanceError: '',
			issuanceRetryAt: null
		},
		{
			id: 2,
			name: 'Public console',
			type: 'lets-encrypt',
			domain: `${hostname}.demo.sylve.io`,
			dynamicDnsEntryId: 1,
			staging: false,
			fingerprint: '9A:C2:48:34:67:EA:28:91:35:BE:8B:71:EE:19:CD:4F',
			notBefore: '2026-07-23T04:10:00.000Z',
			notAfter: '2026-10-21T04:09:59.000Z',
			updatedAt,
			active: false,
			pending: false,
			ready: true,
			renewable: true,
			issuanceStatus: 'ready',
			issuanceOperation: '',
			issuanceError: '',
			issuanceRetryAt: null
		},
		{
			id: 3,
			name: 'Storage API',
			type: 'self-signed',
			domain: `storage-${hostname}.sylve.local`,
			dynamicDnsEntryId: null,
			staging: false,
			fingerprint: '3E:2F:11:5A:A8:44:13:D1:7A:8B:63:C7:16:E2:91:02',
			notBefore: '2026-06-02T09:00:00.000Z',
			notAfter: '2027-06-02T09:00:00.000Z',
			updatedAt,
			active: false,
			pending: false,
			ready: true,
			renewable: false,
			issuanceStatus: 'ready',
			issuanceOperation: '',
			issuanceError: '',
			issuanceRetryAt: null
		}
	];
}

function createRuleTemplates(hostname: string): NotificationRuleTemplate[] {
	return [
		{
			key: 'zfs.pool.health',
			label: 'ZFS pool health',
			description: 'Notify when a pool becomes degraded or unavailable.',
			targetType: 'pool',
			targets: [
				{ key: 'atlas', label: 'atlas' },
				{ key: 'vault', label: 'vault' }
			],
			defaultConfig: '{"conditions":["degraded","faulted"]}'
		},
		{
			key: 'system.disk.smart',
			label: 'Disk health',
			description: 'Notify when SMART reports a disk health problem.',
			targetType: 'node',
			targets: [{ key: hostname, label: hostname }],
			defaultConfig: '{"severity":"warning"}'
		},
		{
			key: 'backup.job.failed',
			label: 'Backup failure',
			description: 'Notify when a scheduled backup fails.',
			targetType: 'node',
			targets: [{ key: hostname, label: hostname }],
			defaultConfig: '{}'
		}
	];
}

function createPCI(hostname: string): { pciDevices: PCIDevice[]; pptDevices: PPTDevice[] } {
	const pciDevices: PCIDevice[] = [
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
		},
		{
			name: 'ix0',
			unit: 0,
			domain: 0,
			bus: 2,
			device: 0,
			function: 0,
			class: 2,
			rev: 2,
			hdr: 0,
			vendor: 32902,
			subVendor: 32902,
			subDevice: 1,
			names: {
				vendor: 'Intel Corporation',
				device: 'X550-T2 10GbE Controller',
				class: 'Network controller',
				subclass: 'Ethernet controller'
			}
		},
		{
			name: 'nvme0',
			unit: 0,
			domain: 0,
			bus: 3,
			device: 0,
			function: 0,
			class: 1,
			rev: 1,
			hdr: 0,
			vendor: 5197,
			subVendor: 5197,
			subDevice: 4253,
			names: {
				vendor: 'Samsung Electronics',
				device: `PM9A3 NVMe (${hostname})`,
				class: 'Mass storage controller',
				subclass: 'NVM controller'
			}
		}
	];
	return { pciDevices, pptDevices: [{ id: 1, domain: 0, oldDriver: 'vgapci', deviceID: '1/0/0' }] };
}

function seedState(hostname: string): DemoAdminState {
	const ruleTemplates = createRuleTemplates(hostname);
	const { pciDevices, pptDevices } = createPCI(hostname);
	return {
		downloads: createDownloads(),
		pendingUploads: {},
		cloudInitTemplates: [
			{
				id: 1,
				name: 'FreeBSD web node',
				user: '#cloud-config\nusers:\n  - name: deploy\n    groups: wheel\n    sudo: ALL=(ALL) NOPASSWD:ALL\npackages:\n  - caddy',
				meta: `instance-id: ${hostname}-web-01\nlocal-hostname: web-01`,
				networkConfig: 'version: 2\nethernets:\n  vtnet0:\n    dhcp4: true',
				createdAt,
				updatedAt
			},
			{
				id: 2,
				name: 'CI runner',
				user: '#cloud-config\nusers:\n  - name: runner\nruncmd:\n  - service qemu-guest-agent start',
				meta: `instance-id: ${hostname}-control-plane\nlocal-hostname: control-plane`,
				networkConfig: 'version: 2\nethernets:\n  eth0:\n    dhcp4: true',
				createdAt,
				updatedAt
			}
		],
		certificates: createCertificates(hostname),
		transports: [
			{
				id: 1,
				name: 'Operations ntfy',
				type: 'ntfy',
				enabled: true,
				ntfy: { baseUrl: 'https://ntfy.sh', topic: 'sylve-demo-ops', hasAuthToken: true }
			},
			{
				id: 2,
				name: 'Infrastructure email',
				type: 'smtp',
				enabled: true,
				email: {
					smtpHost: 'smtp.example.net',
					smtpPort: 587,
					smtpUsername: 'sylve',
					smtpFrom: 'sylve@example.net',
					smtpUseTls: true,
					recipients: ['admin@sylve.local'],
					hasPassword: true
				}
			},
			{
				id: 3,
				name: 'On-call Pushover',
				type: 'pushover',
				enabled: true,
				pushover: { hasApiToken: true, hasUserKey: true }
			}
		],
		rules: [
			{
				id: 1,
				kind: 'zfs.pool.health',
				templateKey: ruleTemplates[0].key,
				templateLabel: ruleTemplates[0].label,
				targetKey: 'atlas',
				targetLabel: 'atlas',
				active: true,
				uiEnabled: true,
				ntfyEnabled: true,
				pushoverEnabled: true,
				emailEnabled: true,
				discordEnabled: false,
				config: ruleTemplates[0].defaultConfig ?? '{}'
			},
			{
				id: 2,
				kind: 'backup.job.failed',
				templateKey: ruleTemplates[2].key,
				templateLabel: ruleTemplates[2].label,
				targetKey: hostname,
				targetLabel: hostname,
				active: true,
				uiEnabled: true,
				ntfyEnabled: true,
				pushoverEnabled: false,
				emailEnabled: false,
				discordEnabled: false,
				config: '{}'
			}
		],
		ruleTemplates,
		pciDevices,
		pptDevices,
		tunables: [
			{ name: 'kern.ipc.maxsockbuf', value: '16777216', type: 'int', writable: true },
			{ name: 'net.inet.ip.forwarding', value: '1', type: 'int', writable: true },
			{ name: 'net.inet.tcp.blackhole', value: '2', type: 'int', writable: true },
			{ name: 'net.inet.tcp.sendspace', value: '65536', type: 'int', writable: true },
			{ name: 'net.inet.tcp.recvspace', value: '65536', type: 'int', writable: true },
			{ name: 'vfs.zfs.arc.max', value: '17179869184', type: 'uint64', writable: true },
			{
				name: 'hw.ncpu',
				value: hostname === 'alia' ? '24' : hostname === 'paul' ? '12' : '16',
				type: 'int',
				writable: false
			},
			{ name: 'kern.osrelease', value: '15.0-RELEASE', type: 'string', writable: false }
		]
	};
}

function stateFor(hostname: string): DemoAdminState {
	let state = adminStates.get(hostname);
	if (!state) {
		state = seedState(hostname);
		adminStates.set(hostname, state);
	}
	return state;
}

export function stageDemoDownloaderUpload(hostname: string, name: string, size: number): string {
	const uploadID = `demo-upload-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
	stateFor(hostname).pendingUploads[uploadID] = { name, size };
	return uploadID;
}

function downloadDeleteResult(downloads: Download[]) {
	return {
		deleted: downloads.map(({ id, uuid, name, type }) => ({ id, uuid, name, type })),
		failed: []
	};
}

function certificateFromInput(
	id: number,
	input: CertificateInput,
	previous?: Certificate
): Certificate {
	const now = new Date().toISOString();
	const managed = input.type === 'lets-encrypt' || input.type === 'sylve-managed';
	return {
		id,
		name: input.name,
		type: input.type,
		domain: input.domain,
		dynamicDnsEntryId: input.dynamicDnsEntryId ?? null,
		staging: input.staging,
		fingerprint:
			previous?.fingerprint ??
			`DE:MO:${id.toString(16).padStart(2, '0')}:CE:RT:IF:IC:AT:E0:00:00:00:00:00:00:01`,
		notBefore: previous?.notBefore ?? now,
		notAfter:
			previous?.notAfter ?? new Date(Date.now() + (managed ? 90 : 365) * 86_400_000).toISOString(),
		updatedAt: now,
		active: previous?.active ?? false,
		pending: false,
		ready: true,
		renewable: managed,
		issuanceStatus: 'ready',
		issuanceOperation: '',
		issuanceError: '',
		issuanceRetryAt: null
	};
}

function transportFromInput(
	id: number,
	input: NotificationTransportInput,
	previous?: NotificationConfig['transports'][number]
): NotificationConfig['transports'][number] {
	return {
		id,
		name: input.name,
		type: input.type,
		enabled: input.enabled,
		...(input.type === 'ntfy' && input.ntfy
			? {
					ntfy: {
						baseUrl: input.ntfy.baseUrl,
						topic: input.ntfy.topic,
						hasAuthToken: Boolean(input.ntfy.authToken) || previous?.ntfy?.hasAuthToken === true
					}
				}
			: {}),
		...(input.type === 'pushover' && input.pushover
			? {
					pushover: {
						hasApiToken:
							Boolean(input.pushover.apiToken) || previous?.pushover?.hasApiToken === true,
						hasUserKey: Boolean(input.pushover.userKey) || previous?.pushover?.hasUserKey === true
					}
				}
			: {}),
		...(input.type === 'smtp' && input.email
			? {
					email: {
						...input.email,
						hasPassword: Boolean(input.email.smtpPassword) || previous?.email?.hasPassword === true
					}
				}
			: {}),
		...(input.type === 'discord' && input.discord
			? { discord: { webhookUrl: input.discord.webhookUrl ?? previous?.discord?.webhookUrl ?? '' } }
			: {})
	};
}

function rulesConfig(state: DemoAdminState) {
	return { rules: state.rules, templates: state.ruleTemplates };
}

export function handleDemoAdminRequest<T = unknown>(
	config: DemoAdminRequestConfig,
	parsed: URL,
	path: string,
	method: string,
	hostname: string
): DemoAdminResponse<T> | null {
	const state = stateFor(hostname);
	const body = payload(config);

	if (path === '/utilities/downloads/utype' && method === 'GET') {
		return success(
			state.downloads
				.filter((download) => download.uType !== '')
				.map((download) => ({ uuid: download.uuid, label: download.name, uType: download.uType }))
		) as DemoAdminResponse<T>;
	}
	if (path === '/utilities/downloads' && method === 'GET') {
		return success(state.downloads) as DemoAdminResponse<T>;
	}
	if (path === '/utilities/downloads' && method === 'POST') {
		const id = nextID(state.downloads);
		const url = stringValue(body, 'url');
		const name =
			stringValue(body, 'filename') || decodeURIComponent(url.split('/').pop() || `download-${id}`);
		const now = new Date().toISOString();
		const download: Download = {
			id,
			uuid: `demo-download-${id}-${Date.now().toString(36)}`,
			path: `/var/sylve/downloads/${name}`,
			name,
			type: url.startsWith('/') ? 'path' : url.startsWith('magnet:') ? 'torrent' : 'http',
			url,
			progress: 100,
			size: 384 * 1024 ** 2,
			files: [],
			uType: stringValue(body, 'downloadType', 'uncategorized') as DownloadType,
			status: 'done',
			automaticExtraction: booleanValue(body, 'automaticExtraction'),
			automaticRawConversion: booleanValue(body, 'automaticRawConversion'),
			ignoreTLS: booleanValue(body, 'ignoreTLS'),
			createdAt: now,
			updatedAt: now
		};
		state.downloads.push(download);
		return success(
			{ id, status: 'pending' as const },
			'download_accepted',
			202
		) as DemoAdminResponse<T>;
	}
	let match = path.match(/^\/utilities\/downloader-uploads\/([^/]+)\/complete$/);
	if (match && method === 'POST') {
		const uploadID = decodeURIComponent(match[1]);
		const pending = state.pendingUploads[uploadID];
		if (!pending) return failure('upload_not_found', 'upload_not_found') as DemoAdminResponse<T>;
		const id = nextID(state.downloads);
		const now = new Date().toISOString();
		state.downloads.push({
			id,
			uuid: `demo-uploaded-${id}`,
			path: `/var/sylve/downloads/${pending.name}`,
			name: pending.name,
			type: 'path',
			url: `/tmp/${pending.name}`,
			progress: 100,
			size: pending.size,
			files: [],
			uType: stringValue(body, 'downloadType', 'uncategorized') as DownloadType,
			status: 'done',
			automaticExtraction: booleanValue(body, 'automaticExtraction'),
			automaticRawConversion: booleanValue(body, 'automaticRawConversion'),
			ignoreTLS: false,
			createdAt: now,
			updatedAt: now
		});
		delete state.pendingUploads[uploadID];
		return success(
			{ uploadId: uploadID, downloadId: id, status: 'completed' as const },
			'upload_completed'
		) as DemoAdminResponse<T>;
	}
	match = path.match(/^\/utilities\/downloader-uploads\/([^/]+)$/);
	if (match && method === 'DELETE') {
		const uploadID = decodeURIComponent(match[1]);
		delete state.pendingUploads[uploadID];
		return success(
			{ uploadId: uploadID, status: 'aborted' as const },
			'upload_aborted'
		) as DemoAdminResponse<T>;
	}
	match = path.match(/^\/utilities\/downloads\/(\d+)$/);
	if (match && method === 'PATCH') {
		const download = state.downloads.find((item) => item.id === Number(match?.[1]));
		if (!download)
			return failure('download_not_found', 'download_not_found') as DemoAdminResponse<T>;
		if (typeof body.name === 'string') download.name = body.name;
		if (typeof body.uType === 'string') download.uType = body.uType as DownloadType;
		if (typeof body.automaticExtraction === 'boolean')
			download.automaticExtraction = body.automaticExtraction;
		if (typeof body.automaticRawConversion === 'boolean')
			download.automaticRawConversion = body.automaticRawConversion;
		download.updatedAt = new Date().toISOString();
		return success(download, 'download_updated') as DemoAdminResponse<T>;
	}
	if (match && method === 'DELETE') {
		const index = state.downloads.findIndex((item) => item.id === Number(match?.[1]));
		if (index < 0)
			return failure('download_not_found', 'download_not_found') as DemoAdminResponse<T>;
		const deleted = state.downloads.splice(index, 1);
		return success(downloadDeleteResult(deleted), 'download_deleted') as DemoAdminResponse<T>;
	}
	if (path === '/utilities/downloads/bulk-delete' && method === 'POST') {
		const ids = new Set(Array.isArray(body.ids) ? body.ids.map(Number) : []);
		const deleted = state.downloads.filter((download) => ids.has(download.id));
		state.downloads = state.downloads.filter((download) => !ids.has(download.id));
		return success(downloadDeleteResult(deleted), 'downloads_deleted') as DemoAdminResponse<T>;
	}
	if (path === '/utilities/downloads/signed-url' && method === 'POST') {
		return success({
			url: `data:application/octet-stream;charset=utf-8,${encodeURIComponent(`Sylve demo file: ${stringValue(body, 'name')}`)}`,
			expiresAt: new Date(Date.now() + 5 * 60_000).toISOString()
		}) as DemoAdminResponse<T>;
	}

	if (path === '/utilities/cloud-init/templates' && method === 'GET') {
		return success(state.cloudInitTemplates) as DemoAdminResponse<T>;
	}
	if (path === '/utilities/cloud-init/templates' && method === 'POST') {
		const id = nextID(state.cloudInitTemplates);
		const now = new Date().toISOString();
		const input = body as unknown as CloudInitTemplateInput;
		const template: CloudInitTemplate = { id, ...input, createdAt: now, updatedAt: now };
		state.cloudInitTemplates.push(template);
		return success(template, 'cloud_init_template_created', 201) as DemoAdminResponse<T>;
	}
	match = path.match(/^\/utilities\/cloud-init\/templates\/(\d+)$/);
	if (match && method === 'PUT') {
		const template = state.cloudInitTemplates.find((item) => item.id === Number(match?.[1]));
		if (!template)
			return failure(
				'cloud_init_template_not_found',
				'cloud_init_template_not_found'
			) as DemoAdminResponse<T>;
		Object.assign(template, body, {
			id: template.id,
			createdAt: template.createdAt,
			updatedAt: new Date().toISOString()
		});
		return success(template, 'cloud_init_template_updated') as DemoAdminResponse<T>;
	}
	if (match && method === 'DELETE') {
		const index = state.cloudInitTemplates.findIndex((item) => item.id === Number(match?.[1]));
		if (index < 0)
			return failure(
				'cloud_init_template_not_found',
				'cloud_init_template_not_found'
			) as DemoAdminResponse<T>;
		const [template] = state.cloudInitTemplates.splice(index, 1);
		return success(
			{ id: template.id, name: template.name },
			'cloud_init_template_deleted'
		) as DemoAdminResponse<T>;
	}

	if (path === '/certificates/domain-check' && method === 'GET') {
		const domain = parsed.searchParams.get('domain') ?? '';
		return success({
			domain,
			resolved: ['203.0.113.42'],
			publicAddresses: ['203.0.113.42'],
			matches: true,
			warning: ''
		}) as DemoAdminResponse<T>;
	}
	if (path === '/certificates' && method === 'GET')
		return success(state.certificates) as DemoAdminResponse<T>;
	if (path === '/certificates' && method === 'POST') {
		const certificate = certificateFromInput(
			nextID(state.certificates),
			body as unknown as CertificateInput
		);
		state.certificates.push(certificate);
		return success(certificate, 'certificate_created', 201) as DemoAdminResponse<T>;
	}
	match = path.match(/^\/certificates\/(\d+)$/);
	if (match && method === 'PATCH') {
		const index = state.certificates.findIndex((item) => item.id === Number(match?.[1]));
		if (index < 0)
			return failure('certificate_not_found', 'certificate_not_found') as DemoAdminResponse<T>;
		state.certificates[index] = certificateFromInput(
			state.certificates[index].id,
			body as unknown as CertificateInput,
			state.certificates[index]
		);
		return success(state.certificates[index], 'certificate_updated') as DemoAdminResponse<T>;
	}
	if (match && method === 'DELETE') {
		const index = state.certificates.findIndex((item) => item.id === Number(match?.[1]));
		if (index < 0)
			return failure('certificate_not_found', 'certificate_not_found') as DemoAdminResponse<T>;
		state.certificates.splice(index, 1);
		return mutationSuccess('certificate_deleted') as DemoAdminResponse<T>;
	}
	match = path.match(/^\/certificates\/(\d+)\/activate$/);
	if (match && method === 'POST') {
		const certificate = state.certificates.find((item) => item.id === Number(match?.[1]));
		if (!certificate)
			return failure('certificate_not_found', 'certificate_not_found') as DemoAdminResponse<T>;
		for (const item of state.certificates) item.pending = false;
		certificate.pending = true;
		certificate.updatedAt = new Date().toISOString();
		return success(certificate, 'certificate_activation_scheduled') as DemoAdminResponse<T>;
	}
	if (match && method === 'DELETE') {
		const certificate = state.certificates.find((item) => item.id === Number(match?.[1]));
		if (!certificate)
			return failure('certificate_not_found', 'certificate_not_found') as DemoAdminResponse<T>;
		certificate.pending = false;
		return mutationSuccess('certificate_activation_cancelled') as DemoAdminResponse<T>;
	}
	match = path.match(/^\/certificates\/(\d+)\/(renew|retry)$/);
	if (match && method === 'POST') {
		const certificate = state.certificates.find((item) => item.id === Number(match?.[1]));
		if (!certificate)
			return failure('certificate_not_found', 'certificate_not_found') as DemoAdminResponse<T>;
		certificate.ready = true;
		certificate.issuanceStatus = 'ready';
		certificate.issuanceOperation = '';
		certificate.issuanceError = '';
		certificate.issuanceRetryAt = null;
		certificate.notBefore = new Date().toISOString();
		certificate.notAfter = new Date(Date.now() + 90 * 86_400_000).toISOString();
		certificate.updatedAt = new Date().toISOString();
		return success(
			certificate,
			match?.[2] === 'renew' ? 'certificate_renewed' : 'certificate_issuance_retried'
		) as DemoAdminResponse<T>;
	}
	match = path.match(/^\/certificates\/(\d+)\/archive$/);
	if (match && method === 'GET') {
		const certificate = state.certificates.find((item) => item.id === Number(match?.[1]));
		if (!certificate)
			return failure('certificate_not_found', 'certificate_not_found') as DemoAdminResponse<T>;
		return {
			status: 200,
			data: new Blob([`Sylve demo certificate archive for ${certificate.domain}`], {
				type: 'application/zip'
			}) as T,
			headers: { 'content-type': 'application/zip' },
			ok: true
		};
	}

	if (path === '/notifications/transports' && method === 'GET')
		return success({ transports: state.transports }) as DemoAdminResponse<T>;
	if (path === '/notifications/transports' && method === 'POST') {
		state.transports.push(
			transportFromInput(nextID(state.transports), body as unknown as NotificationTransportInput)
		);
		return success(
			{ transports: state.transports },
			'notification_transport_created',
			201
		) as DemoAdminResponse<T>;
	}
	match = path.match(/^\/notifications\/transports\/(\d+)$/);
	if (match && method === 'PUT') {
		const index = state.transports.findIndex((item) => item.id === Number(match?.[1]));
		if (index < 0)
			return failure(
				'notification_transport_not_found',
				'notification_transport_not_found'
			) as DemoAdminResponse<T>;
		state.transports[index] = transportFromInput(
			state.transports[index].id,
			body as unknown as NotificationTransportInput,
			state.transports[index]
		);
		return success(
			{ transports: state.transports },
			'notification_transport_updated'
		) as DemoAdminResponse<T>;
	}
	if (match && method === 'DELETE') {
		const index = state.transports.findIndex((item) => item.id === Number(match?.[1]));
		if (index < 0)
			return failure(
				'notification_transport_not_found',
				'notification_transport_not_found'
			) as DemoAdminResponse<T>;
		state.transports.splice(index, 1);
		return mutationSuccess('notification_transport_deleted') as DemoAdminResponse<T>;
	}
	match = path.match(/^\/notifications\/transports\/(\d+)\/test$/);
	if (match && method === 'POST') {
		return state.transports.some((item) => item.id === Number(match?.[1]))
			? (mutationSuccess('notification_transport_test_sent') as DemoAdminResponse<T>)
			: (failure(
					'notification_transport_not_found',
					'notification_transport_not_found'
				) as DemoAdminResponse<T>);
	}

	if (path === '/notifications/rules' && method === 'GET')
		return success(rulesConfig(state)) as DemoAdminResponse<T>;
	if (path === '/notifications/rules' && method === 'POST') {
		const template = state.ruleTemplates.find(
			(item) => item.key === stringValue(body, 'templateKey')
		);
		if (!template)
			return failure(
				'notification_rule_template_not_found',
				'notification_rule_template_not_found'
			) as DemoAdminResponse<T>;
		const targetKey = stringValue(body, 'targetKey');
		const target = template.targets.find((item) => item.key === targetKey);
		state.rules.push({
			id: nextID(state.rules),
			kind: template.key,
			templateKey: template.key,
			templateLabel: template.label,
			targetKey,
			targetLabel: target?.label ?? targetKey,
			active: true,
			uiEnabled: booleanValue(body, 'uiEnabled'),
			ntfyEnabled: booleanValue(body, 'ntfyEnabled'),
			pushoverEnabled: booleanValue(body, 'pushoverEnabled'),
			emailEnabled: booleanValue(body, 'emailEnabled'),
			discordEnabled: booleanValue(body, 'discordEnabled'),
			config: template.defaultConfig ?? '{}'
		});
		return success(rulesConfig(state), 'notification_rule_created', 201) as DemoAdminResponse<T>;
	}
	if (path === '/notifications/rules' && method === 'PUT') {
		const updates = Array.isArray(body.rules) ? body.rules : [];
		for (const update of updates) {
			if (typeof update !== 'object' || update === null) continue;
			const data = update as Record<string, unknown>;
			const rule = state.rules.find((item) => item.id === Number(data.id));
			if (!rule) continue;
			for (const key of [
				'uiEnabled',
				'ntfyEnabled',
				'pushoverEnabled',
				'emailEnabled',
				'discordEnabled'
			] as const)
				if (typeof data[key] === 'boolean') rule[key] = data[key];
		}
		return success(rulesConfig(state), 'notification_rules_updated') as DemoAdminResponse<T>;
	}
	match = path.match(/^\/notifications\/rules\/(\d+)$/);
	if (match && method === 'PUT') {
		const rule = state.rules.find((item) => item.id === Number(match?.[1]));
		if (!rule)
			return failure(
				'notification_rule_not_found',
				'notification_rule_not_found'
			) as DemoAdminResponse<T>;
		for (const key of [
			'uiEnabled',
			'ntfyEnabled',
			'pushoverEnabled',
			'emailEnabled',
			'discordEnabled'
		] as const)
			if (typeof body[key] === 'boolean') rule[key] = body[key];
		if (typeof body.config === 'string') rule.config = body.config;
		return success(rulesConfig(state), 'notification_rule_updated') as DemoAdminResponse<T>;
	}
	if (match && method === 'DELETE') {
		const index = state.rules.findIndex((item) => item.id === Number(match?.[1]));
		if (index < 0)
			return failure(
				'notification_rule_not_found',
				'notification_rule_not_found'
			) as DemoAdminResponse<T>;
		state.rules.splice(index, 1);
		return success(rulesConfig(state), 'notification_rule_deleted') as DemoAdminResponse<T>;
	}
	if (path === '/notifications/rules/bulk-delete' && method === 'POST') {
		const ids = new Set(Array.isArray(body.ids) ? body.ids.map(Number) : []);
		state.rules = state.rules.filter((rule) => !ids.has(rule.id));
		return success(rulesConfig(state), 'notification_rules_deleted') as DemoAdminResponse<T>;
	}
	if (path === '/notifications/rules/bulk-update' && method === 'POST') {
		const ids = new Set(Array.isArray(body.ids) ? body.ids.map(Number) : []);
		for (const rule of state.rules.filter((item) => ids.has(item.id))) {
			for (const key of [
				'uiEnabled',
				'ntfyEnabled',
				'pushoverEnabled',
				'emailEnabled',
				'discordEnabled'
			] as const)
				if (typeof body[key] === 'boolean') rule[key] = body[key];
		}
		return success(rulesConfig(state), 'notification_rules_updated') as DemoAdminResponse<T>;
	}
	if (path === '/notifications/rules/test' && method === 'POST')
		return mutationSuccess('notification_rule_test_sent') as DemoAdminResponse<T>;

	if (path === '/system/pci-devices' && method === 'GET')
		return success(state.pciDevices) as DemoAdminResponse<T>;
	if (path === '/system/ppt-devices' && method === 'GET')
		return success(state.pptDevices) as DemoAdminResponse<T>;
	if (
		(path === '/system/ppt-devices' || path === '/system/ppt-devices/import') &&
		method === 'POST'
	) {
		const domain = Math.trunc(numberValue(body, 'domain'));
		const deviceID = stringValue(body, 'deviceID');
		if (!state.pptDevices.some((item) => item.domain === domain && item.deviceID === deviceID)) {
			state.pptDevices.push({ id: nextID(state.pptDevices), domain, deviceID, oldDriver: 'ix' });
		}
		const [bus, device, fn] = deviceID.split('/').map(Number);
		const pci = state.pciDevices.find(
			(item) =>
				item.domain === domain && item.bus === bus && item.device === device && item.function === fn
		);
		if (pci)
			pci.name = `ppt${state.pptDevices.findIndex((item) => item.domain === domain && item.deviceID === deviceID)}`;
		return mutationSuccess(
			path.endsWith('/import') ? 'device_imported' : 'device_added'
		) as DemoAdminResponse<T>;
	}
	if (path === '/system/ppt-devices/prepare' && method === 'POST')
		return success({ rebootRequired: true }, 'device_prepared') as DemoAdminResponse<T>;
	match = path.match(/^\/system\/ppt-devices\/(\d+)$/);
	if (match && method === 'DELETE') {
		const index = state.pptDevices.findIndex((item) => item.id === Number(match?.[1]));
		if (index < 0)
			return failure('ppt_device_not_found', 'ppt_device_not_found') as DemoAdminResponse<T>;
		const [mapping] = state.pptDevices.splice(index, 1);
		const [bus, device, fn] = mapping.deviceID.split('/').map(Number);
		const pci = state.pciDevices.find(
			(item) =>
				item.domain === mapping.domain &&
				item.bus === bus &&
				item.device === device &&
				item.function === fn
		);
		if (pci) pci.name = mapping.oldDriver;
		return success({ rebootRequired: false }, 'device_removed') as DemoAdminResponse<T>;
	}

	if (path === '/system/tunables/remote' && method === 'GET') {
		let rows = [...state.tunables];
		const search = (parsed.searchParams.get('search') ?? '').trim().toLowerCase();
		if (search)
			rows = rows.filter(
				(row) => row.name.toLowerCase().includes(search) || row.value.toLowerCase().includes(search)
			);
		if (parsed.searchParams.get('configuredOnly') === 'true')
			rows = rows.filter(
				(row) =>
					row.writable &&
					['kern.ipc.maxsockbuf', 'net.inet.ip.forwarding', 'net.inet.tcp.blackhole'].includes(
						row.name
					)
			);
		const page = Math.max(1, Number(parsed.searchParams.get('page') || 1));
		const size = Math.max(1, Number(parsed.searchParams.get('size') || 25));
		return success({
			last_page: Math.max(1, Math.ceil(rows.length / size)),
			data: rows.slice((page - 1) * size, page * size)
		}) as DemoAdminResponse<T>;
	}
	if (path === '/system/tunables' && method === 'PUT') {
		const tunable = state.tunables.find((item) => item.name === stringValue(body, 'name'));
		if (!tunable || !tunable.writable)
			return failure('tunable_not_writable', 'tunable_not_writable', 409) as DemoAdminResponse<T>;
		tunable.value = stringValue(body, 'value', tunable.value);
		return mutationSuccess('tunable_updated') as DemoAdminResponse<T>;
	}

	return null;
}
