/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { DHCPConfig, DHCPRange, DHCPStaticLease, FileLease } from '$lib/types/network/dhcp';
import type {
	FirewallAdvancedSettings,
	FirewallLiveHitEvent,
	FirewallNATRule,
	FirewallTrafficRule,
	RenderedConfig
} from '$lib/types/network/firewall';
import type { Iface } from '$lib/types/network/iface';
import type { MdnsRecordWithManaged, MdnsSettings } from '$lib/types/network/mdns';
import type { NetworkObject } from '$lib/types/network/object';
import type { StaticRoute } from '$lib/types/network/route';
import type { DynamicDNSEntry, DynamicDNSEntryInput } from '$lib/types/services/dynamic-dns';
import type { ManualSwitch, StandardSwitch, SwitchList } from '$lib/types/network/switch';
import type {
	WireGuardClient,
	WireGuardServer,
	WireGuardServerPeer
} from '$lib/types/network/wireguard';

type DemoNetworkRequestConfig = {
	url: string;
	method?: string;
	headers?: Record<string, string>;
	data?: unknown;
};

export type DemoNetworkResponse<T = unknown> = {
	status: number;
	data: T;
	headers: Record<string, string>;
	ok: boolean;
};

type DemoNetworkState = {
	objects: NetworkObject[];
	interfaces: Iface[];
	switches: SwitchList;
	routes: StaticRoute[];
	dhcpConfig: DHCPConfig;
	dhcpRanges: DHCPRange[];
	staticLeases: DHCPStaticLease[];
	dynamicLeases: FileLease[];
	trafficRules: FirewallTrafficRule[];
	natRules: FirewallNATRule[];
	advanced: FirewallAdvancedSettings;
	liveHits: FirewallLiveHitEvent[];
	wireGuardServer: WireGuardServer | null;
	wireGuardClients: WireGuardClient[];
	mdnsSettings: MdnsSettings;
	mdnsRecords: MdnsRecordWithManaged[];
	dynamicDNSEntries: DynamicDNSEntry[];
};

const createdAt = '2026-05-12T09:30:00.000Z';
const updatedAt = '2026-08-14T08:45:00.000Z';
const networkStates = new Map<string, DemoNetworkState>();

function success<T>(
	data: T,
	message = 'demo_fixture_loaded',
	status = 200
): DemoNetworkResponse<{
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

function mutationSuccess(message: string, status = 200): DemoNetworkResponse {
	return success(null, message, status);
}

function failure(message: string, error: string, status = 404): DemoNetworkResponse {
	return {
		status,
		data: { status: 'error', message, error },
		headers: { 'content-type': 'application/json' },
		ok: false
	};
}

function payload(config: DemoNetworkRequestConfig): Record<string, unknown> {
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

function nullableNumber(body: Record<string, unknown>, key: string): number | null {
	if (body[key] === null || body[key] === undefined || body[key] === '') return null;
	const value = Number(body[key]);
	return Number.isFinite(value) && value > 0 ? value : null;
}

function booleanValue(body: Record<string, unknown>, key: string, fallback = false): boolean {
	return typeof body[key] === 'boolean' ? body[key] : fallback;
}

function stringArray(body: Record<string, unknown>, key: string): string[] {
	return Array.isArray(body[key])
		? (body[key] as unknown[]).filter((value): value is string => typeof value === 'string')
		: [];
}

function numberArray(body: Record<string, unknown>, key: string): number[] {
	if (!Array.isArray(body[key])) return [];
	return (body[key] as unknown[])
		.map(Number)
		.filter((value) => Number.isFinite(value) && value > 0);
}

function strictPositiveIntegerArray(
	body: Record<string, unknown>,
	key: string,
	maxItems: number
): number[] | null {
	const value = body[key];
	if (!Array.isArray(value) || value.length < 1 || value.length > maxItems) return null;
	if (value.some((item) => typeof item !== 'number' || !Number.isSafeInteger(item) || item <= 0)) {
		return null;
	}

	const ids = value as number[];
	if (new Set(ids).size !== ids.length) return null;
	return [...ids];
}

function nextID(items: Array<{ id: number }>): number {
	return Math.max(0, ...items.map((item) => item.id)) + 1;
}

function dynamicDNSEntryFromInput(
	id: number,
	input: DynamicDNSEntryInput,
	previous?: DynamicDNSEntry
): DynamicDNSEntry {
	const now = new Date().toISOString();
	const manualAddress = input.sourceSettings.address ?? '';
	const isIPv6 = manualAddress.includes(':');
	const publishIPv4 = input.recordType !== 'AAAA';
	const publishIPv6 = input.recordType !== 'A';
	return {
		id,
		enabled: input.enabled,
		provider: input.provider,
		providerSettings: { ...(input.providerSettings ?? {}) },
		credentialConfigured: Boolean(input.token?.trim()) || previous?.credentialConfigured === true,
		hostname: input.hostname,
		recordType: input.recordType,
		intervalMinutes: input.intervalMinutes,
		sourceType: input.sourceType,
		sourceSettings: { ...input.sourceSettings },
		lastStatus: input.enabled ? previous?.lastStatus || 'pending' : '',
		lastError: '',
		ipv4Status: publishIPv4 && input.enabled ? previous?.ipv4Status || 'pending' : '',
		ipv4Error: '',
		ipv6Status: publishIPv6 && input.enabled ? previous?.ipv6Status || 'pending' : '',
		ipv6Error: '',
		lastIPv4: publishIPv4 ? previous?.lastIPv4 || (!isIPv6 ? manualAddress : '') : '',
		lastIPv6: publishIPv6 ? previous?.lastIPv6 || (isIPv6 ? manualAddress : '') : '',
		lastSyncAt: previous?.lastSyncAt ?? null,
		lastSuccessAt: previous?.lastSuccessAt ?? null,
		consecutiveFailures: 0,
		nextRetryAt: null,
		createdAt: previous?.createdAt ?? now,
		updatedAt: now
	};
}

function firstObjectValue(object: NetworkObject | null | undefined): string {
	return object?.entries?.[0]?.value ?? '';
}

function createObject(
	id: number,
	name: string,
	type: NetworkObject['type'],
	values: string[],
	options: Partial<NetworkObject> = {}
): NetworkObject {
	return {
		id,
		name,
		type,
		description: '',
		autoUpdate: true,
		refreshIntervalSeconds: 300,
		sourceChecksum: '',
		resolutionChecksum: '',
		lastRefreshAt: null,
		lastRefreshError: '',
		createdAt,
		updatedAt,
		isUsed: false,
		isUsedBy: '',
		entries: values.map((value, index) => ({
			id: id * 100 + index + 1,
			objectId: id,
			value,
			createdAt,
			updatedAt
		})),
		resolutions: [],
		...options
	};
}

function makeInterface(name: string, overrides: Partial<Iface> = {}): Iface {
	return {
		name,
		ether: '',
		hwaddr: '',
		flags: { raw: 34899, desc: ['UP', 'BROADCAST', 'RUNNING', 'SIMPLEX', 'MULTICAST'] },
		mtu: 1500,
		metric: 0,
		capabilities: {
			enabled: { raw: 399, desc: ['RXCSUM', 'TXCSUM', 'VLAN_MTU', 'JUMBO_MTU'] },
			supported: {
				raw: 131071,
				desc: ['RXCSUM', 'TXCSUM', 'TSO4', 'TSO6', 'LRO', 'VLAN_HWTAGGING']
			}
		},
		driver: '',
		model: '',
		description: '',
		bridgeId: '',
		stp: null,
		maxaddr: 0,
		timeout: 0,
		ipv4: [],
		ipv6: [],
		media: {
			type: 'Ethernet',
			subtype: '1000baseT',
			options: ['full-duplex'],
			mode: 'autoselect',
			rawCurrent: 0,
			rawActive: 0,
			status: 'active'
		},
		nd6: { raw: 41, desc: ['PERFORMNUD', 'IFDISABLED'] },
		groups: [],
		bridgeMembers: [],
		...overrides
	};
}

function createInterfaces(hostname: string): Iface[] {
	const hostOctet = hostname === 'paul' ? 12 : hostname === 'alia' ? 13 : 11;
	return [
		makeInterface('igb0', {
			ether: `58:9c:fc:10:00:${hostOctet}`,
			hwaddr: `58:9c:fc:10:00:${hostOctet}`,
			driver: 'igb',
			model: 'Intel I210 Gigabit Network Connection',
			description: 'Management uplink',
			ipv4: [{ ip: `10.0.0.${hostOctet}`, netmask: '255.255.255.0', broadcast: '10.0.0.255' }],
			ipv6: [
				{
					ip: `fd42:7379:6c76::${hostOctet}`,
					prefixLength: 64,
					scopeId: 0,
					autoConf: false,
					detached: false,
					deprecated: false,
					lifeTimes: { preferred: 0, valid: 0 }
				}
			]
		}),
		makeInterface('ix0', {
			ether: `3c:fd:fe:20:00:${hostOctet}`,
			hwaddr: `3c:fd:fe:20:00:${hostOctet}`,
			driver: 'ix',
			model: 'Intel X520 10GbE SFP+',
			description: 'Storage fabric',
			media: {
				type: 'Ethernet',
				subtype: '10Gbase-SR',
				options: ['full-duplex'],
				mode: 'autoselect',
				rawCurrent: 0,
				rawActive: 0,
				status: 'active'
			}
		}),
		makeInterface('vm-production', {
			model: 'Bridge',
			description: 'Production guests',
			bridgeId: '00:00:5e:00:53:01',
			groups: ['bridge'],
			ipv4: [{ ip: '10.30.0.1', netmask: '255.255.255.0', broadcast: '10.30.0.255' }],
			ipv6: [
				{
					ip: 'fd42:30::1',
					prefixLength: 64,
					scopeId: 0,
					autoConf: false,
					detached: false,
					deprecated: false,
					lifeTimes: { preferred: 0, valid: 0 }
				}
			],
			media: null,
			bridgeMembers: [
				{
					name: 'igb0',
					flags: { raw: 3, desc: ['LEARNING', 'DISCOVER'] },
					ifmaxaddr: 2000,
					state: 3,
					priority: 128,
					port: 2,
					pathCost: 20000
				}
			]
		}),
		makeInterface('vm-storage', {
			model: 'Bridge',
			description: 'Storage guests',
			bridgeId: '00:00:5e:00:53:02',
			groups: ['bridge'],
			ipv4: [{ ip: '10.20.0.1', netmask: '255.255.255.0', broadcast: '10.20.0.255' }],
			media: null,
			bridgeMembers: []
		}),
		makeInterface('bridge-lab', {
			model: 'Bridge',
			description: 'Isolated lab bridge',
			bridgeId: '00:00:5e:00:53:03',
			groups: ['bridge'],
			ipv4: [{ ip: '10.50.0.1', netmask: '255.255.255.0', broadcast: '10.50.0.255' }],
			media: null,
			bridgeMembers: []
		}),
		makeInterface('bridge-spare', {
			model: 'Bridge',
			description: 'Unassigned demo bridge',
			bridgeId: '00:00:5e:00:53:04',
			groups: ['bridge'],
			media: null,
			bridgeMembers: []
		}),
		makeInterface('wgs0', {
			model: 'WireGuard',
			description: 'WireGuard server',
			groups: ['wg'],
			ipv4: [{ ip: '10.99.0.1', netmask: '255.255.255.0', broadcast: '10.99.0.255' }],
			media: null
		}),
		makeInterface('wgc1', {
			model: 'WireGuard',
			description: 'Amsterdam edge tunnel',
			groups: ['wg'],
			ipv4: [{ ip: '10.88.0.2', netmask: '255.255.255.255', broadcast: '10.88.0.2' }],
			media: null
		}),
		makeInterface('lo0', {
			flags: { raw: 32841, desc: ['UP', 'LOOPBACK', 'RUNNING', 'MULTICAST'] },
			mtu: 16384,
			model: 'Loopback',
			description: 'Loopback interface',
			groups: ['lo'],
			ipv4: [{ ip: '127.0.0.1', netmask: '255.0.0.0', broadcast: '127.255.255.255' }],
			ipv6: [
				{
					ip: '::1',
					prefixLength: 128,
					scopeId: 0,
					autoConf: false,
					detached: false,
					deprecated: false,
					lifeTimes: { preferred: 0, valid: 0 }
				}
			],
			media: null
		})
	];
}

function createState(hostname: string): DemoNetworkState {
	const objects = [
		createObject(1, 'production-net', 'Network', ['10.30.0.0/24'], {
			description: 'Production guest subnet',
			isUsed: true,
			isUsedBy: 'Standard switch, firewall, DHCP'
		}),
		createObject(2, 'production-gateway', 'Host', ['10.30.0.1'], {
			description: 'Gateway for production guests',
			isUsed: true,
			isUsedBy: 'Standard switch'
		}),
		createObject(3, 'web-servers', 'Host', ['10.30.0.21', '10.30.0.22'], {
			description: 'Public web tier',
			isUsed: true,
			isUsedBy: 'Firewall rules'
		}),
		createObject(4, 'trusted-dns', 'Host', ['1.1.1.1', '9.9.9.9'], {
			description: 'Recursive DNS resolvers'
		}),
		createObject(5, 'web-ports', 'Port', ['80', '443'], {
			description: 'HTTP and HTTPS',
			isUsed: true,
			isUsedBy: 'Firewall rules'
		}),
		createObject(6, 'build-runner-mac', 'Mac', ['02:53:59:4c:56:10'], {
			description: 'Build runner NIC',
			isUsed: true,
			isUsedBy: 'DHCP lease'
		}),
		createObject(7, 'build-runner-duid', 'DUID', ['00:04:9d:8a:11:45:aa:91'], {
			description: 'Build runner DHCPv6 identity'
		}),
		createObject(8, 'blocked-regions', 'Country', ['KP', 'RU'], {
			description: 'Regions blocked at the edge'
		}),
		createObject(9, 'build-runner-ip', 'Host', ['10.30.0.120'], {
			description: 'Reserved build runner address',
			isUsed: true,
			isUsedBy: 'DHCP lease'
		}),
		createObject(10, 'storage-net', 'Network', ['10.20.0.0/24'], {
			description: 'Storage replication subnet',
			isUsed: true,
			isUsedBy: 'Standard switch, static route'
		}),
		createObject(11, 'storage-gateway', 'Host', ['10.20.0.1'], {
			description: 'Storage gateway',
			isUsed: true,
			isUsedBy: 'Standard switch'
		}),
		createObject(12, 'sylve-docs', 'FQDN', ['docs.sylve.io'], {
			description: 'Sylve documentation',
			lastRefreshAt: updatedAt,
			resolutions: [
				{
					id: 1201,
					objectId: 12,
					resolvedIp: '203.0.113.42',
					resolvedValue: 'docs.sylve.io',
					createdAt,
					updatedAt
				}
			]
		}),
		createObject(13, 'monitoring-mac', 'Mac', ['02:53:59:4c:56:20'], {
			description: 'Available for a demo static lease'
		}),
		createObject(14, 'monitoring-ip', 'Host', ['10.30.0.121'], {
			description: 'Available for a demo static lease'
		})
	];

	const standard: StandardSwitch[] = [
		{
			id: 1,
			name: 'production',
			bridgeName: 'vm-production',
			mtu: 1500,
			vlan: 30,
			private: false,
			address: '10.30.0.1/24',
			address6: 'fd42:30::1/64',
			addressObj: objects[1],
			address6Obj: null,
			networkObj: objects[0],
			network6Obj: null,
			gatewayAddressObj: objects[1],
			gateway6AddressObj: null,
			networkManual: '',
			network6Manual: 'fd42:30::/64',
			gatewayManual: '',
			gateway6Manual: 'fd42:30::1',
			ports: [{ id: 1, name: 'igb0', switchId: 1 }],
			dhcp: false,
			slaac: true,
			disableIPv6: false,
			defaultRoute: true,
			disableBridgeOffloads: true
		},
		{
			id: 2,
			name: 'storage',
			bridgeName: 'vm-storage',
			mtu: 9000,
			vlan: 20,
			private: true,
			address: '10.20.0.1/24',
			address6: '',
			addressObj: objects[10],
			address6Obj: null,
			networkObj: objects[9],
			network6Obj: null,
			gatewayAddressObj: objects[10],
			gateway6AddressObj: null,
			networkManual: '',
			network6Manual: '',
			gatewayManual: '',
			gateway6Manual: '',
			ports: [{ id: 2, name: 'ix0', switchId: 2 }],
			dhcp: false,
			slaac: false,
			disableIPv6: true,
			defaultRoute: false,
			disableBridgeOffloads: true
		}
	];
	const manual: ManualSwitch[] = [
		{
			id: 10,
			name: 'lab',
			bridge: 'bridge-lab',
			createdAt,
			updatedAt
		}
	];
	const switches = { standard, manual };

	const dhcpRanges: DHCPRange[] = [
		{
			id: 1,
			type: 'ipv4',
			startIp: '10.30.0.100',
			endIp: '10.30.0.199',
			standardSwitchId: 1,
			standardSwitch: standard[0],
			manualSwitchId: null,
			manualSwitch: null,
			expiry: 86400,
			raOnly: false,
			slaac: false,
			createdAt,
			updatedAt
		},
		{
			id: 2,
			type: 'ipv6',
			startIp: 'fd42:30::100',
			endIp: 'fd42:30::1ff',
			standardSwitchId: 1,
			standardSwitch: standard[0],
			manualSwitchId: null,
			manualSwitch: null,
			expiry: 43200,
			raOnly: false,
			slaac: true,
			createdAt,
			updatedAt
		}
	];

	const now = Date.now();
	const wireGuardPeer: WireGuardServerPeer = {
		id: 1,
		name: 'hayzam-laptop',
		enabled: true,
		wireguardServerId: 1,
		privateKey: 'CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC=',
		publicKey: 'DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD=',
		preSharedKey: 'EEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE=',
		clientIPs: ['10.99.0.2/32'],
		routableIPs: ['10.30.0.0/24', '10.20.0.0/24'],
		routeIPs: true,
		persistentKeepalive: true,
		lastHandshake: new Date(now - 35_000).toISOString(),
		rx: 384_123_904,
		tx: 92_774_400,
		createdAt,
		updatedAt
	};

	return {
		objects,
		interfaces: createInterfaces(hostname),
		switches,
		routes: [
			{
				id: 1,
				name: 'backup-site',
				description: 'Route backup replication over the storage fabric',
				enabled: true,
				fib: 0,
				destinationType: 'network',
				destination: '10.40.0.0/16',
				destinationRaw: '10.40.0.0/16',
				destinationObjId: null,
				family: 'inet',
				nextHopMode: 'gateway',
				gateway: '10.20.0.254',
				gatewayRaw: '10.20.0.254',
				gatewayObjId: null,
				gatewayZone: '',
				interface: 'vm-storage',
				createdAt,
				updatedAt
			},
			{
				id: 2,
				name: 'wireguard-clients',
				description: 'Reach remote WireGuard peers',
				enabled: true,
				fib: 0,
				destinationType: 'network',
				destination: '10.99.0.0/24',
				destinationRaw: '10.99.0.0/24',
				destinationObjId: null,
				family: 'inet',
				nextHopMode: 'interface',
				gateway: '',
				gatewayRaw: '',
				gatewayObjId: null,
				gatewayZone: '',
				interface: 'wgs0',
				createdAt,
				updatedAt
			}
		],
		dhcpConfig: {
			id: 1,
			standardSwitches: [standard[0]],
			manualSwitches: [manual[0]],
			dnsServers: ['1.1.1.1', '9.9.9.9'],
			domain: 'lab.sylve.local',
			expandHosts: true,
			createdAt,
			updatedAt
		},
		dhcpRanges,
		staticLeases: [
			{
				id: 1,
				hostname: 'control-plane',
				comments: 'Reserved for the CI runner VM',
				expiry: 0,
				ipObjectId: 9,
				macObjectId: 6,
				duidObjectId: null,
				ipObject: objects[8],
				macObject: objects[5],
				duidObject: null,
				dhcpRangeId: 1,
				dhcpRange: dhcpRanges[0],
				createdAt,
				updatedAt
			}
		],
		dynamicLeases: [
			{
				expiry: Math.floor(now / 1000) + 41_400,
				mac: '52:54:00:7a:11:01',
				ip: '10.30.0.141',
				iaid: '',
				hostname: 'edge-relay',
				clientId: '01:52:54:00:7a:11:01',
				duid: ''
			},
			{
				expiry: Math.floor(now / 1000) + 68_200,
				mac: '52:54:00:7a:11:02',
				ip: '10.30.0.158',
				iaid: '',
				hostname: 'docs-preview',
				clientId: '01:52:54:00:7a:11:02',
				duid: ''
			}
		],
		trafficRules: [
			{
				id: 1,
				name: 'Allow web ingress',
				description: 'Publish the production web tier',
				visible: true,
				enabled: true,
				log: true,
				quick: true,
				priority: 10,
				action: 'pass',
				direction: 'in',
				protocol: 'tcp',
				ingressInterfaces: ['igb0'],
				egressInterfaces: ['vm-production'],
				family: 'inet',
				sourceRaw: 'any',
				sourceObjId: null,
				destRaw: '',
				destObjId: 3,
				srcPortsRaw: '',
				srcPortObjId: null,
				dstPortsRaw: '',
				dstPortObjId: 5,
				createdAt,
				updatedAt
			},
			{
				id: 2,
				name: 'Allow DNS egress',
				description: 'Permit guests to use trusted resolvers',
				visible: true,
				enabled: true,
				log: false,
				quick: true,
				priority: 20,
				action: 'pass',
				direction: 'out',
				protocol: 'tcp_udp',
				ingressInterfaces: ['vm-production'],
				egressInterfaces: ['igb0'],
				family: 'inet',
				sourceRaw: '',
				sourceObjId: 1,
				destRaw: '',
				destObjId: 4,
				srcPortsRaw: '',
				srcPortObjId: null,
				dstPortsRaw: '53',
				dstPortObjId: null,
				createdAt,
				updatedAt
			},
			{
				id: 3,
				name: 'Default deny',
				description: 'Block unmatched inbound traffic',
				visible: true,
				enabled: true,
				log: true,
				quick: false,
				priority: 1000,
				action: 'block',
				direction: 'in',
				protocol: 'any',
				ingressInterfaces: ['igb0'],
				egressInterfaces: [],
				family: 'any',
				sourceRaw: 'any',
				sourceObjId: null,
				destRaw: 'any',
				destObjId: null,
				srcPortsRaw: '',
				srcPortObjId: null,
				dstPortsRaw: '',
				dstPortObjId: null,
				createdAt,
				updatedAt
			}
		],
		natRules: [
			{
				id: 1,
				name: 'Production outbound NAT',
				description: 'Masquerade production guests on the uplink',
				visible: true,
				enabled: true,
				log: false,
				priority: 10,
				natType: 'snat',
				policyRoutingEnabled: false,
				policyRouteGateway: '',
				ingressInterfaces: ['vm-production'],
				egressInterfaces: ['igb0'],
				family: 'inet',
				protocol: 'any',
				sourceRaw: '',
				sourceObjId: 1,
				destRaw: 'any',
				destObjId: null,
				translateMode: 'interface',
				translateToRaw: '',
				translateToObjId: null,
				dnatTargetRaw: '',
				dnatTargetObjId: null,
				dstPortsRaw: '',
				dstPortObjId: null,
				redirectPortsRaw: '',
				redirectPortObjId: null,
				createdAt,
				updatedAt
			},
			{
				id: 2,
				name: 'Publish web tier',
				description: 'Forward HTTPS to the first web node',
				visible: true,
				enabled: true,
				log: true,
				priority: 20,
				natType: 'dnat',
				policyRoutingEnabled: false,
				policyRouteGateway: '',
				ingressInterfaces: ['igb0'],
				egressInterfaces: ['vm-production'],
				family: 'inet',
				protocol: 'tcp',
				sourceRaw: 'any',
				sourceObjId: null,
				destRaw: '10.0.0.11',
				destObjId: null,
				translateMode: 'address',
				translateToRaw: '',
				translateToObjId: null,
				dnatTargetRaw: '10.30.0.21',
				dnatTargetObjId: null,
				dstPortsRaw: '443',
				dstPortObjId: null,
				redirectPortsRaw: '443',
				redirectPortObjId: null,
				createdAt,
				updatedAt
			}
		],
		advanced: {
			id: 1,
			preRules: 'set block-policy drop\nset optimization normal',
			preNatDecl: '# Custom tables may be declared here',
			postNatDecl: '',
			preTrafficAnchor: '# Allow essential ICMP before managed rules',
			postTrafficAnchor: '',
			postRules: '# Demo firewall generated by Sylve',
			createdAt,
			updatedAt
		},
		liveHits: [
			{
				cursor: 101,
				timestamp: new Date(now - 18_000).toISOString(),
				ruleType: 'traffic',
				ruleId: 1,
				ruleName: 'Allow web ingress',
				action: 'pass',
				direction: 'in',
				interface: 'igb0',
				bytes: 1460,
				rawLine: 'pass in quick on igb0 proto tcp to 10.30.0.21 port 443'
			},
			{
				cursor: 102,
				timestamp: new Date(now - 9_000).toISOString(),
				ruleType: 'nat',
				ruleId: 1,
				ruleName: 'Production outbound NAT',
				action: 'nat',
				direction: 'out',
				interface: 'igb0',
				bytes: 892,
				rawLine: 'nat on igb0 from 10.30.0.0/24 to any -> (igb0)'
			},
			{
				cursor: 103,
				timestamp: new Date(now - 3_000).toISOString(),
				ruleType: 'traffic',
				ruleId: 3,
				ruleName: 'Default deny',
				action: 'block',
				direction: 'in',
				interface: 'igb0',
				bytes: 64,
				rawLine: 'block in on igb0 from 198.51.100.33 to 10.0.0.11'
			}
		],
		wireGuardServer: {
			id: 1,
			enabled: true,
			port: 51820,
			addresses: ['10.99.0.1/24', 'fd42:99::1/64'],
			allowWireGuardPort: true,
			masqueradeIPv4Interface: 'igb0',
			masqueradeIPv6Interface: 'igb0',
			privateKey: 'FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF=',
			publicKey: 'GGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG=',
			peers: [wireGuardPeer],
			mtu: 1420,
			metric: 0,
			rx: 948_332_544,
			tx: 516_882_432,
			uptime: 864_240,
			lastHandshake: wireGuardPeer.lastHandshake,
			restartedAt: '2026-08-04T08:30:00.000Z',
			createdAt,
			updatedAt
		},
		wireGuardClients: [
			{
				id: 1,
				enabled: true,
				name: 'Amsterdam edge',
				endpointHost: 'ams.example.net',
				endpointPort: 51820,
				listenPort: 0,
				privateKey: 'HHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHH=',
				publicKey: 'IIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIII=',
				peerPublicKey: 'JJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJ=',
				preSharedKey: 'KKKKKKKKKKKKKKKKKKKKKKKKKKKKKKKKKKKKKKKKKKK=',
				allowedIPs: ['10.88.0.0/24'],
				addresses: ['10.88.0.2/32'],
				routeAllowedIPs: true,
				mtu: 1420,
				metric: 10,
				fib: 0,
				persistentKeepalive: true,
				rx: 184_202_240,
				tx: 72_441_856,
				uptime: 421_200,
				lastHandshake: new Date(now - 42_000).toISOString(),
				restartedAt: '2026-08-09T11:00:00.000Z',
				createdAt,
				updatedAt
			},
			{
				id: 2,
				enabled: false,
				name: 'Disaster recovery',
				endpointHost: 'dr.example.net',
				endpointPort: 51821,
				listenPort: 0,
				privateKey: 'LLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLL=',
				publicKey: 'MMMMMMMMMMMMMMMMMMMMMMMMMMMMMMMMMMMMMMMMMMM=',
				peerPublicKey: 'NNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNN=',
				preSharedKey: '',
				allowedIPs: ['10.120.0.0/16'],
				addresses: ['10.120.0.2/32'],
				routeAllowedIPs: false,
				mtu: 1380,
				metric: 50,
				fib: 1,
				persistentKeepalive: true,
				rx: 0,
				tx: 0,
				uptime: 0,
				lastHandshake: '0001-01-01T00:00:00Z',
				restartedAt: '',
				createdAt,
				updatedAt
			}
		],
		mdnsSettings: { id: 1, interfaces: 'vm-production,bridge-lab', hostname },
		mdnsRecords: [
			{
				id: 1,
				name: 'sylve',
				type: '_https._tcp',
				port: 8181,
				txt: { path: '/', role: 'management' },
				interfaces: 'vm-production',
				createdAt,
				updatedAt,
				managed: false,
				source: 'user'
			},
			{
				id: 2,
				name: 'samba',
				type: '_smb._tcp',
				port: 445,
				txt: {},
				interfaces: 'vm-production',
				createdAt,
				updatedAt,
				managed: true,
				source: 'samba'
			}
		],
		dynamicDNSEntries: [
			{
				id: 1,
				enabled: true,
				provider: 'cloudflare',
				providerSettings: { zone: 'sylve.io' },
				credentialConfigured: true,
				hostname: `${hostname}.demo.sylve.io`,
				recordType: 'BOTH',
				intervalMinutes: 10,
				sourceType: 'interface',
				sourceSettings: { interface: 'igb0' },
				lastStatus: 'success',
				lastError: '',
				ipv4Status: 'success',
				ipv4Error: '',
				ipv6Status: 'success',
				ipv6Error: '',
				lastIPv4: '203.0.113.42',
				lastIPv6: '2001:db8::42',
				lastSyncAt: updatedAt,
				lastSuccessAt: updatedAt,
				consecutiveFailures: 0,
				nextRetryAt: null,
				createdAt,
				updatedAt
			},
			{
				id: 2,
				enabled: true,
				provider: 'sylve',
				providerSettings: {},
				credentialConfigured: true,
				hostname: `console-${hostname}.example.net`,
				recordType: 'A',
				intervalMinutes: 15,
				sourceType: 'stun',
				sourceSettings: { server: 'stun.cloudflare.com:3478' },
				lastStatus: 'success',
				lastError: '',
				ipv4Status: 'success',
				ipv4Error: '',
				ipv6Status: '',
				ipv6Error: '',
				lastIPv4: '198.51.100.24',
				lastIPv6: '',
				lastSyncAt: updatedAt,
				lastSuccessAt: updatedAt,
				consecutiveFailures: 0,
				nextRetryAt: null,
				createdAt,
				updatedAt
			}
		]
	};
}

function stateFor(hostname: string): DemoNetworkState {
	let state = networkStates.get(hostname);
	if (!state) {
		state = createState(hostname);
		networkStates.set(hostname, state);
	}
	return state;
}

function refreshObjectUsage(state: DemoNetworkState) {
	const used = new Map<number, Set<string>>();
	const mark = (id: number | null | undefined, source: string) => {
		if (!id) return;
		if (!used.has(id)) used.set(id, new Set());
		used.get(id)?.add(source);
	};

	for (const sw of state.switches.standard) {
		mark(sw.networkObj?.id, 'Standard switch');
		mark(sw.network6Obj?.id, 'Standard switch');
		mark(sw.gatewayAddressObj?.id, 'Standard switch');
		mark(sw.gateway6AddressObj?.id, 'Standard switch');
	}
	for (const lease of state.staticLeases) {
		mark(lease.ipObjectId, 'DHCP lease');
		mark(lease.macObjectId, 'DHCP lease');
		mark(lease.duidObjectId, 'DHCP lease');
	}
	for (const route of state.routes) {
		mark(route.destinationObjId, 'Static route');
		mark(route.gatewayObjId, 'Static route');
	}
	for (const rule of [...state.trafficRules, ...state.natRules]) {
		mark(rule.sourceObjId, 'Firewall rule');
		mark(rule.destObjId, 'Firewall rule');
	}
	for (const rule of state.trafficRules) {
		mark(rule.srcPortObjId, 'Firewall rule');
		mark(rule.dstPortObjId, 'Firewall rule');
	}
	for (const rule of state.natRules) {
		mark(rule.translateToObjId, 'NAT rule');
		mark(rule.dnatTargetObjId, 'NAT rule');
		mark(rule.dstPortObjId, 'NAT rule');
		mark(rule.redirectPortObjId, 'NAT rule');
	}

	for (const object of state.objects) {
		const sources = [...(used.get(object.id) ?? [])];
		object.isUsed = sources.length > 0;
		object.isUsedBy = sources.join(', ');
	}
}

function updateObjectEntries(object: NetworkObject, values: string[]) {
	object.entries = values.map((value, index) => ({
		id: object.id * 100 + index + 1,
		objectId: object.id,
		value,
		createdAt: object.createdAt,
		updatedAt: new Date().toISOString()
	}));
	object.updatedAt = new Date().toISOString();
}

function buildStandardSwitch(
	state: DemoNetworkState,
	id: number,
	body: Record<string, unknown>,
	existing?: StandardSwitch
): StandardSwitch {
	const network4ID = nullableNumber(body, 'network4');
	const gateway4ID = nullableNumber(body, 'gateway4');
	const network6ID = nullableNumber(body, 'network6');
	const gateway6ID = nullableNumber(body, 'gateway6');
	const objectByID = (objectID: number | null) =>
		objectID ? (state.objects.find((object) => object.id === objectID) ?? null) : null;
	const networkObj = objectByID(network4ID);
	const gatewayObj = objectByID(gateway4ID);
	const network6Obj = objectByID(network6ID);
	const gateway6Obj = objectByID(gateway6ID);
	const networkManual = stringValue(body, 'network4Manual', existing?.networkManual ?? '');
	const gatewayManual = stringValue(body, 'gateway4Manual', existing?.gatewayManual ?? '');
	const network6Manual = stringValue(body, 'network6Manual', existing?.network6Manual ?? '');
	const gateway6Manual = stringValue(body, 'gateway6Manual', existing?.gateway6Manual ?? '');
	const name = stringValue(body, 'name', existing?.name ?? `switch-${id}`).trim();
	const ports = stringArray(body, 'ports');

	return {
		id,
		name,
		bridgeName: existing?.bridgeName ?? `vm-${name.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`,
		mtu: Math.trunc(numberValue(body, 'mtu', existing?.mtu ?? 1500)),
		vlan: Math.trunc(numberValue(body, 'vlan', existing?.vlan ?? 0)),
		private: booleanValue(body, 'private', existing?.private ?? false),
		address: firstObjectValue(gatewayObj) || gatewayManual,
		address6: firstObjectValue(gateway6Obj) || gateway6Manual,
		addressObj: gatewayObj,
		address6Obj: gateway6Obj,
		networkObj,
		network6Obj,
		gatewayAddressObj: gatewayObj,
		gateway6AddressObj: gateway6Obj,
		networkManual,
		network6Manual,
		gatewayManual,
		gateway6Manual,
		ports: ports.map((port, index) => ({ id: id * 100 + index + 1, name: port, switchId: id })),
		dhcp: booleanValue(body, 'dhcp', existing?.dhcp ?? false),
		slaac: booleanValue(body, 'slaac', existing?.slaac ?? false),
		disableIPv6: booleanValue(body, 'disableIPv6', existing?.disableIPv6 ?? false),
		defaultRoute: booleanValue(body, 'defaultRoute', existing?.defaultRoute ?? false),
		disableBridgeOffloads: booleanValue(
			body,
			'disableBridgeOffloads',
			existing?.disableBridgeOffloads ?? true
		)
	};
}

function buildRange(
	state: DemoNetworkState,
	id: number,
	body: Record<string, unknown>,
	existing?: DHCPRange
): DHCPRange {
	const standardSwitchId = nullableNumber(body, 'standardSwitch');
	const manualSwitchId = nullableNumber(body, 'manualSwitch');
	return {
		id,
		type: stringValue(body, 'type', existing?.type ?? 'ipv4') === 'ipv6' ? 'ipv6' : 'ipv4',
		startIp: stringValue(body, 'startIp', existing?.startIp ?? ''),
		endIp: stringValue(body, 'endIp', existing?.endIp ?? ''),
		standardSwitchId,
		standardSwitch: state.switches.standard.find((item) => item.id === standardSwitchId) ?? null,
		manualSwitchId,
		manualSwitch: state.switches.manual.find((item) => item.id === manualSwitchId) ?? null,
		expiry: Math.trunc(numberValue(body, 'expiry', existing?.expiry ?? 86400)),
		raOnly: booleanValue(body, 'raOnly', existing?.raOnly ?? false),
		slaac: booleanValue(body, 'slaac', existing?.slaac ?? false),
		createdAt: existing?.createdAt ?? new Date().toISOString(),
		updatedAt: new Date().toISOString()
	};
}

function buildLease(
	state: DemoNetworkState,
	id: number,
	body: Record<string, unknown>,
	existing?: DHCPStaticLease
): DHCPStaticLease {
	const ipObjectId = nullableNumber(body, 'ipId');
	const macObjectId = nullableNumber(body, 'macId');
	const duidObjectId = nullableNumber(body, 'duidId');
	const dhcpRangeId = Math.trunc(numberValue(body, 'dhcpRangeId', existing?.dhcpRangeId ?? 0));
	return {
		id,
		hostname: stringValue(body, 'hostname', existing?.hostname ?? ''),
		comments: stringValue(body, 'comments', existing?.comments ?? ''),
		expiry: existing?.expiry ?? 0,
		ipObjectId,
		macObjectId,
		duidObjectId,
		ipObject: state.objects.find((item) => item.id === ipObjectId) ?? null,
		macObject: state.objects.find((item) => item.id === macObjectId) ?? null,
		duidObject: state.objects.find((item) => item.id === duidObjectId) ?? null,
		dhcpRangeId,
		dhcpRange: state.dhcpRanges.find((item) => item.id === dhcpRangeId) ?? null,
		createdAt: existing?.createdAt ?? new Date().toISOString(),
		updatedAt: new Date().toISOString()
	};
}

function buildRoute(
	id: number,
	body: Record<string, unknown>,
	existing?: StaticRoute
): StaticRoute {
	return {
		id,
		name: stringValue(body, 'name', existing?.name ?? `route-${id}`),
		description: stringValue(body, 'description', existing?.description ?? ''),
		enabled: booleanValue(body, 'enabled', existing?.enabled ?? true),
		fib: Math.trunc(numberValue(body, 'fib', existing?.fib ?? 0)),
		destinationType:
			stringValue(body, 'destinationType', existing?.destinationType ?? 'network') === 'host'
				? 'host'
				: 'network',
		destination: stringValue(body, 'destination', existing?.destination ?? ''),
		destinationRaw: stringValue(body, 'destinationRaw', existing?.destinationRaw ?? ''),
		destinationObjId: nullableNumber(body, 'destinationObjId'),
		family: stringValue(body, 'family', existing?.family ?? 'inet') === 'inet6' ? 'inet6' : 'inet',
		nextHopMode:
			stringValue(body, 'nextHopMode', existing?.nextHopMode ?? 'gateway') === 'interface'
				? 'interface'
				: 'gateway',
		gateway: stringValue(body, 'gateway', existing?.gateway ?? ''),
		gatewayRaw: stringValue(body, 'gatewayRaw', existing?.gatewayRaw ?? ''),
		gatewayObjId: nullableNumber(body, 'gatewayObjId'),
		gatewayZone: stringValue(body, 'gatewayZone', existing?.gatewayZone ?? ''),
		interface: stringValue(body, 'interface', existing?.interface ?? ''),
		createdAt: existing?.createdAt ?? new Date().toISOString(),
		updatedAt: new Date().toISOString()
	};
}

function buildTrafficRule(
	id: number,
	body: Record<string, unknown>,
	existing?: FirewallTrafficRule
): FirewallTrafficRule {
	const pick = <T extends string>(key: string, allowed: T[], fallback: T): T => {
		const value = stringValue(body, key, fallback);
		return allowed.includes(value as T) ? (value as T) : fallback;
	};
	return {
		id,
		name: stringValue(body, 'name', existing?.name ?? `Traffic rule ${id}`),
		description: stringValue(body, 'description', existing?.description ?? ''),
		visible: true,
		enabled: booleanValue(body, 'enabled', existing?.enabled ?? true),
		log: booleanValue(body, 'log', existing?.log ?? false),
		quick: booleanValue(body, 'quick', existing?.quick ?? false),
		priority: Math.trunc(numberValue(body, 'priority', existing?.priority ?? id * 10)),
		action: pick('action', ['pass', 'block'], existing?.action ?? 'pass'),
		direction: pick('direction', ['in', 'out'], existing?.direction ?? 'in'),
		protocol: pick(
			'protocol',
			['any', 'tcp', 'udp', 'tcp_udp', 'icmp'],
			existing?.protocol ?? 'any'
		),
		ingressInterfaces: stringArray(body, 'ingressInterfaces'),
		egressInterfaces: stringArray(body, 'egressInterfaces'),
		family: pick('family', ['any', 'inet', 'inet6'], existing?.family ?? 'any'),
		sourceRaw: stringValue(body, 'sourceRaw', existing?.sourceRaw ?? ''),
		sourceObjId: nullableNumber(body, 'sourceObjId'),
		destRaw: stringValue(body, 'destRaw', existing?.destRaw ?? ''),
		destObjId: nullableNumber(body, 'destObjId'),
		srcPortsRaw: stringValue(body, 'srcPortsRaw', existing?.srcPortsRaw ?? ''),
		srcPortObjId: nullableNumber(body, 'srcPortObjId'),
		dstPortsRaw: stringValue(body, 'dstPortsRaw', existing?.dstPortsRaw ?? ''),
		dstPortObjId: nullableNumber(body, 'dstPortObjId'),
		createdAt: existing?.createdAt ?? new Date().toISOString(),
		updatedAt: new Date().toISOString()
	};
}

function buildNATRule(
	id: number,
	body: Record<string, unknown>,
	existing?: FirewallNATRule
): FirewallNATRule {
	const pick = <T extends string>(key: string, allowed: T[], fallback: T): T => {
		const value = stringValue(body, key, fallback);
		return allowed.includes(value as T) ? (value as T) : fallback;
	};
	return {
		id,
		name: stringValue(body, 'name', existing?.name ?? `NAT rule ${id}`),
		description: stringValue(body, 'description', existing?.description ?? ''),
		visible: true,
		enabled: booleanValue(body, 'enabled', existing?.enabled ?? true),
		log: booleanValue(body, 'log', existing?.log ?? false),
		priority: Math.trunc(numberValue(body, 'priority', existing?.priority ?? id * 10)),
		natType: pick('natType', ['snat', 'dnat', 'binat'], existing?.natType ?? 'snat'),
		policyRoutingEnabled: booleanValue(
			body,
			'policyRoutingEnabled',
			existing?.policyRoutingEnabled ?? false
		),
		policyRouteGateway: stringValue(body, 'policyRouteGateway', existing?.policyRouteGateway ?? ''),
		ingressInterfaces: stringArray(body, 'ingressInterfaces'),
		egressInterfaces: stringArray(body, 'egressInterfaces'),
		family: pick('family', ['any', 'inet', 'inet6'], existing?.family ?? 'any'),
		protocol: pick('protocol', ['any', 'tcp', 'udp', 'icmp'], existing?.protocol ?? 'any'),
		sourceRaw: stringValue(body, 'sourceRaw', existing?.sourceRaw ?? ''),
		sourceObjId: nullableNumber(body, 'sourceObjId'),
		destRaw: stringValue(body, 'destRaw', existing?.destRaw ?? ''),
		destObjId: nullableNumber(body, 'destObjId'),
		translateMode: pick(
			'translateMode',
			['interface', 'address'],
			existing?.translateMode ?? 'interface'
		),
		translateToRaw: stringValue(body, 'translateToRaw', existing?.translateToRaw ?? ''),
		translateToObjId: nullableNumber(body, 'translateToObjId'),
		dnatTargetRaw: stringValue(body, 'dnatTargetRaw', existing?.dnatTargetRaw ?? ''),
		dnatTargetObjId: nullableNumber(body, 'dnatTargetObjId'),
		dstPortsRaw: stringValue(body, 'dstPortsRaw', existing?.dstPortsRaw ?? ''),
		dstPortObjId: nullableNumber(body, 'dstPortObjId'),
		redirectPortsRaw: stringValue(body, 'redirectPortsRaw', existing?.redirectPortsRaw ?? ''),
		redirectPortObjId: nullableNumber(body, 'redirectPortObjId'),
		createdAt: existing?.createdAt ?? new Date().toISOString(),
		updatedAt: new Date().toISOString()
	};
}

function renderedConfig(state: DemoNetworkState, advanced = state.advanced): RenderedConfig {
	const objectTables = state.objects
		.map((object) => {
			const values = object.entries?.map((entry) => entry.value).join(', ') ?? '';
			return `table <${object.name}> { ${values} }`;
		})
		.join('\n');
	const natRules = state.natRules
		.filter((rule) => rule.enabled)
		.map((rule) => `# ${rule.name}\n${rule.natType} on ${rule.egressInterfaces[0] || 'egress'}`)
		.join('\n\n');
	const trafficRules = state.trafficRules
		.filter((rule) => rule.enabled)
		.map((rule) => {
			const parts: string[] = [rule.action, rule.direction];
			if (rule.log) parts.push('log');
			if (rule.quick) parts.push('quick');
			const iface = rule.direction === 'in' ? rule.ingressInterfaces[0] : rule.egressInterfaces[0];
			if (iface) parts.push('on', iface);
			if (rule.family !== 'any') parts.push(rule.family);
			if (rule.protocol === 'tcp_udp') {
				parts.push('proto', '{ tcp, udp }');
			} else if (rule.protocol !== 'any') {
				parts.push('proto', rule.protocol);
			}
			parts.push(
				'from',
				rule.sourceRaw || (rule.sourceObjId ? `<sylve_obj_${rule.sourceObjId}>` : 'any')
			);
			if (rule.srcPortsRaw) parts.push('port', rule.srcPortsRaw);
			parts.push('to', rule.destRaw || (rule.destObjId ? `<sylve_obj_${rule.destObjId}>` : 'any'));
			if (rule.dstPortsRaw) parts.push('port', rule.dstPortsRaw);
			return parts.join(' ');
		})
		.join('\n');
	return {
		pfConf: [advanced.preRules, 'anchor "sylve/nat"', 'anchor "sylve/traffic"', advanced.postRules]
			.filter(Boolean)
			.join('\n\n'),
		objectTables,
		natRules,
		trafficRules
	};
}

function buildWireGuardClient(
	id: number,
	body: Record<string, unknown>,
	existing?: WireGuardClient
): WireGuardClient {
	const now = new Date().toISOString();
	return {
		id,
		enabled: booleanValue(body, 'enabled', existing?.enabled ?? true),
		name: stringValue(body, 'name', existing?.name ?? `WireGuard client ${id}`),
		endpointHost: stringValue(body, 'endpointHost', existing?.endpointHost ?? ''),
		endpointPort: Math.trunc(numberValue(body, 'endpointPort', existing?.endpointPort ?? 51820)),
		listenPort: Math.trunc(numberValue(body, 'listenPort', existing?.listenPort ?? 0)),
		privateKey: stringValue(
			body,
			'privateKey',
			existing?.privateKey ?? 'OOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOO='
		),
		publicKey: existing?.publicKey ?? 'PPPPPPPPPPPPPPPPPPPPPPPPPPPPPPPPPPPPPPPPPPP=',
		peerPublicKey: stringValue(body, 'peerPublicKey', existing?.peerPublicKey ?? ''),
		preSharedKey: stringValue(body, 'preSharedKey', existing?.preSharedKey ?? ''),
		allowedIPs: stringArray(body, 'allowedIPs'),
		addresses: stringArray(body, 'addresses'),
		routeAllowedIPs: booleanValue(body, 'routeAllowedIPs', existing?.routeAllowedIPs ?? true),
		mtu: Math.trunc(numberValue(body, 'mtu', existing?.mtu ?? 1420)),
		metric: Math.trunc(numberValue(body, 'metric', existing?.metric ?? 0)),
		fib: Math.trunc(numberValue(body, 'fib', existing?.fib ?? 0)),
		persistentKeepalive: booleanValue(
			body,
			'persistentKeepalive',
			existing?.persistentKeepalive ?? true
		),
		rx: existing?.rx ?? 0,
		tx: existing?.tx ?? 0,
		uptime: existing?.uptime ?? 0,
		lastHandshake: existing?.lastHandshake ?? '0001-01-01T00:00:00Z',
		restartedAt: existing?.restartedAt ?? '',
		createdAt: existing?.createdAt ?? now,
		updatedAt: now
	};
}

function buildWireGuardPeer(
	server: WireGuardServer,
	id: number,
	body: Record<string, unknown>,
	existing?: WireGuardServerPeer
): WireGuardServerPeer {
	const now = new Date().toISOString();
	return {
		id,
		name: stringValue(body, 'name', existing?.name ?? `Peer ${id}`),
		enabled: booleanValue(body, 'enabled', existing?.enabled ?? true),
		wireguardServerId: server.id,
		privateKey: stringValue(
			body,
			'privateKey',
			existing?.privateKey ?? 'QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ='
		),
		publicKey: existing?.publicKey ?? 'RRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRR=',
		preSharedKey: stringValue(body, 'preSharedKey', existing?.preSharedKey ?? ''),
		clientIPs: stringArray(body, 'clientIPs'),
		routableIPs: stringArray(body, 'routableIPs'),
		routeIPs: booleanValue(body, 'routeIPs', existing?.routeIPs ?? false),
		persistentKeepalive: booleanValue(
			body,
			'persistentKeepalive',
			existing?.persistentKeepalive ?? true
		),
		lastHandshake: existing?.lastHandshake ?? '0001-01-01T00:00:00Z',
		rx: existing?.rx ?? 0,
		tx: existing?.tx ?? 0,
		createdAt: existing?.createdAt ?? now,
		updatedAt: now
	};
}

function deleteByID<T extends { id: number }>(items: T[], id: number): boolean {
	const index = items.findIndex((item) => item.id === id);
	if (index < 0) return false;
	items.splice(index, 1);
	return true;
}

export function handleDemoNetworkRequest<T = unknown>(
	config: DemoNetworkRequestConfig,
	parsed: URL,
	path: string,
	method: string,
	hostname: string
): DemoNetworkResponse<T> | null {
	const state = stateFor(hostname);
	const body = payload(config);

	if (path === '/network/interface' && method === 'GET') {
		return success(state.interfaces) as DemoNetworkResponse<T>;
	}

	if (path === '/network/object' && method === 'GET') {
		refreshObjectUsage(state);
		return success(state.objects) as DemoNetworkResponse<T>;
	}
	if (path === '/network/object' && method === 'POST') {
		const id = nextID(state.objects);
		const type = stringValue(body, 'type', 'Host') as NetworkObject['type'];
		state.objects.push(
			createObject(
				id,
				stringValue(body, 'name', `object-${id}`),
				type,
				stringArray(body, 'values'),
				{
					createdAt: new Date().toISOString(),
					updatedAt: new Date().toISOString()
				}
			)
		);
		return success(id, 'network_object_created', 201) as DemoNetworkResponse<T>;
	}
	if (path === '/network/object' && method === 'DELETE') {
		const ids = numberArray(body, 'ids');
		for (const id of ids) deleteByID(state.objects, id);
		return mutationSuccess('network_objects_deleted') as DemoNetworkResponse<T>;
	}
	let match = path.match(/^\/network\/object\/(\d+)$/);
	if (match && method === 'PUT') {
		const id = Number(match[1]);
		const object = state.objects.find((item) => item.id === id);
		if (!object)
			return failure(
				'network_object_not_found',
				'network_object_not_found'
			) as DemoNetworkResponse<T>;
		object.name = stringValue(body, 'name', object.name);
		object.type = stringValue(body, 'type', object.type) as NetworkObject['type'];
		updateObjectEntries(object, stringArray(body, 'values'));
		return mutationSuccess('network_object_updated') as DemoNetworkResponse<T>;
	}
	if (match && method === 'DELETE') {
		if (!deleteByID(state.objects, Number(match[1]))) {
			return failure(
				'network_object_not_found',
				'network_object_not_found'
			) as DemoNetworkResponse<T>;
		}
		return mutationSuccess('network_object_deleted') as DemoNetworkResponse<T>;
	}

	if (path === '/network/switch' && method === 'GET') {
		return success(state.switches) as DemoNetworkResponse<T>;
	}
	if (path === '/network/switch/manual' && method === 'POST') {
		const id = nextID(state.switches.manual);
		state.switches.manual.push({
			id,
			name: stringValue(body, 'name', `manual-${id}`),
			bridge: stringValue(body, 'bridge', 'bridge-lab'),
			createdAt: new Date().toISOString(),
			updatedAt: new Date().toISOString()
		});
		return success(id, 'manual_switch_created', 201) as DemoNetworkResponse<T>;
	}
	match = path.match(/^\/network\/switch\/manual\/(\d+)$/);
	if (match && method === 'DELETE') {
		const id = Number(match[1]);
		if (!deleteByID(state.switches.manual, id)) {
			return failure(
				'manual_switch_not_found',
				'manual_switch_not_found'
			) as DemoNetworkResponse<T>;
		}
		state.dhcpConfig.manualSwitches = state.dhcpConfig.manualSwitches.filter(
			(item) => item.id !== id
		);
		for (const range of state.dhcpRanges) {
			if (range.manualSwitchId !== id) continue;
			range.manualSwitchId = null;
			range.manualSwitch = null;
		}
		return mutationSuccess('manual_switch_deleted') as DemoNetworkResponse<T>;
	}
	if (path === '/network/switch/standard' && method === 'POST') {
		const id = nextID(state.switches.standard);
		state.switches.standard.push(buildStandardSwitch(state, id, body));
		return success(id, 'standard_switch_created', 201) as DemoNetworkResponse<T>;
	}
	match = path.match(/^\/network\/switch\/standard\/(\d+)$/);
	if (match && method === 'PUT') {
		const id = Number(match[1]);
		const index = state.switches.standard.findIndex((item) => item.id === id);
		if (index < 0)
			return failure(
				'standard_switch_not_found',
				'standard_switch_not_found'
			) as DemoNetworkResponse<T>;
		const current = state.switches.standard[index];
		const next = buildStandardSwitch(state, current.id, body, current);
		Object.assign(current, next);
		return mutationSuccess('standard_switch_updated') as DemoNetworkResponse<T>;
	}
	if (match && method === 'DELETE') {
		const id = Number(match[1]);
		if (!deleteByID(state.switches.standard, id)) {
			return failure(
				'standard_switch_not_found',
				'standard_switch_not_found'
			) as DemoNetworkResponse<T>;
		}
		state.dhcpConfig.standardSwitches = state.dhcpConfig.standardSwitches.filter(
			(item) => item.id !== id
		);
		for (const range of state.dhcpRanges) {
			if (range.standardSwitchId !== id) continue;
			range.standardSwitchId = null;
			range.standardSwitch = null;
		}
		return mutationSuccess('standard_switch_deleted') as DemoNetworkResponse<T>;
	}

	if (path === '/network/route' && method === 'GET') {
		return success(state.routes) as DemoNetworkResponse<T>;
	}
	if (path === '/network/route' && method === 'POST') {
		const id = nextID(state.routes);
		state.routes.push(buildRoute(id, body));
		return success(id, 'static_route_created', 201) as DemoNetworkResponse<T>;
	}
	match = path.match(/^\/network\/route\/(\d+)$/);
	if (match && method === 'PUT') {
		const id = Number(match[1]);
		const index = state.routes.findIndex((item) => item.id === id);
		if (index < 0)
			return failure('static_route_not_found', 'static_route_not_found') as DemoNetworkResponse<T>;
		state.routes[index] = buildRoute(state.routes[index].id, body, state.routes[index]);
		return mutationSuccess('static_route_updated') as DemoNetworkResponse<T>;
	}
	if (match && method === 'DELETE') {
		if (!deleteByID(state.routes, Number(match[1]))) {
			return failure('static_route_not_found', 'static_route_not_found') as DemoNetworkResponse<T>;
		}
		return mutationSuccess('static_route_deleted') as DemoNetworkResponse<T>;
	}

	if (path === '/network/dhcp/config' && method === 'GET') {
		return success(state.dhcpConfig) as DemoNetworkResponse<T>;
	}
	if (path === '/network/dhcp/config' && method === 'PUT') {
		state.dhcpConfig.standardSwitches = numberArray(body, 'standardSwitches')
			.map((id) => state.switches.standard.find((item) => item.id === id))
			.filter((item): item is StandardSwitch => Boolean(item));
		state.dhcpConfig.manualSwitches = numberArray(body, 'manualSwitches')
			.map((id) => state.switches.manual.find((item) => item.id === id))
			.filter((item): item is ManualSwitch => Boolean(item));
		state.dhcpConfig.dnsServers = stringArray(body, 'dnsServers');
		state.dhcpConfig.domain = stringValue(body, 'domain', state.dhcpConfig.domain);
		state.dhcpConfig.expandHosts = booleanValue(body, 'expandHosts', true);
		state.dhcpConfig.updatedAt = new Date().toISOString();
		return mutationSuccess('dhcp_config_updated') as DemoNetworkResponse<T>;
	}
	if (path === '/network/dhcp/range' && method === 'GET') {
		return success(state.dhcpRanges) as DemoNetworkResponse<T>;
	}
	if (path === '/network/dhcp/range' && method === 'POST') {
		const id = nextID(state.dhcpRanges);
		state.dhcpRanges.push(buildRange(state, id, body));
		return mutationSuccess('dhcp_range_created', 201) as DemoNetworkResponse<T>;
	}
	match = path.match(/^\/network\/dhcp\/range\/(\d+)$/);
	if (match && method === 'PUT') {
		const id = Number(match[1]);
		const index = state.dhcpRanges.findIndex((item) => item.id === id);
		if (index < 0)
			return failure('dhcp_range_not_found', 'dhcp_range_not_found') as DemoNetworkResponse<T>;
		const current = state.dhcpRanges[index];
		const next = buildRange(state, current.id, body, current);
		Object.assign(current, next);
		return mutationSuccess('dhcp_range_updated') as DemoNetworkResponse<T>;
	}
	if (match && method === 'DELETE') {
		const id = Number(match[1]);
		if (!deleteByID(state.dhcpRanges, id)) {
			return failure('dhcp_range_not_found', 'dhcp_range_not_found') as DemoNetworkResponse<T>;
		}
		state.staticLeases = state.staticLeases.filter((lease) => lease.dhcpRangeId !== id);
		return mutationSuccess('dhcp_range_deleted') as DemoNetworkResponse<T>;
	}
	if (path === '/network/dhcp/lease' && method === 'GET') {
		return success({ file: state.dynamicLeases, db: state.staticLeases }) as DemoNetworkResponse<T>;
	}
	if (path === '/network/dhcp/lease' && method === 'POST') {
		const id = nextID(state.staticLeases);
		state.staticLeases.push(buildLease(state, id, body));
		return mutationSuccess('dhcp_lease_created', 201) as DemoNetworkResponse<T>;
	}
	match = path.match(/^\/network\/dhcp\/lease\/(\d+)$/);
	if (match && method === 'PUT') {
		const id = Number(match[1]);
		const index = state.staticLeases.findIndex((item) => item.id === id);
		if (index < 0)
			return failure('dhcp_lease_not_found', 'dhcp_lease_not_found') as DemoNetworkResponse<T>;
		state.staticLeases[index] = buildLease(
			state,
			state.staticLeases[index].id,
			body,
			state.staticLeases[index]
		);
		return mutationSuccess('dhcp_lease_updated') as DemoNetworkResponse<T>;
	}
	if (match && method === 'DELETE') {
		if (!deleteByID(state.staticLeases, Number(match[1]))) {
			return failure('dhcp_lease_not_found', 'dhcp_lease_not_found') as DemoNetworkResponse<T>;
		}
		return mutationSuccess('dhcp_lease_deleted') as DemoNetworkResponse<T>;
	}
	if (path === '/network/dhcp/lease/dynamic' && method === 'DELETE') {
		const identifier = stringValue(body, 'identifier');
		const ip = stringValue(body, 'ip');
		const index = state.dynamicLeases.findIndex(
			(lease) => lease.ip === ip && [lease.mac, lease.clientId, lease.duid].includes(identifier)
		);
		if (index >= 0) state.dynamicLeases.splice(index, 1);
		return mutationSuccess('dynamic_dhcp_lease_deleted') as DemoNetworkResponse<T>;
	}

	if (path === '/network/firewall/traffic' && method === 'GET') {
		return success(state.trafficRules) as DemoNetworkResponse<T>;
	}
	if (path === '/network/firewall/traffic' && method === 'POST') {
		const id = nextID(state.trafficRules);
		state.trafficRules.push(buildTrafficRule(id, body));
		return success(id, 'firewall_traffic_rule_created', 201) as DemoNetworkResponse<T>;
	}
	if (path === '/network/firewall/traffic' && method === 'DELETE') {
		const ids = strictPositiveIntegerArray(body, 'ids', 1024);
		if (!ids) {
			return failure(
				'invalid_request',
				'invalid_firewall_traffic_request',
				400
			) as DemoNetworkResponse<T>;
		}
		const selected = ids.map((id) => state.trafficRules.find((rule) => rule.id === id));
		if (selected.some((rule) => !rule)) {
			return failure(
				'failed_to_delete_firewall_traffic_rules',
				'firewall_traffic_rule_not_found',
				404
			) as DemoNetworkResponse<T>;
		}
		if (selected.some((rule) => rule?.visible === false)) {
			return failure(
				'failed_to_delete_firewall_traffic_rules',
				'hidden_firewall_rule_managed_by_wireguard',
				409
			) as DemoNetworkResponse<T>;
		}
		const selectedIDs = new Set(ids);
		state.trafficRules = state.trafficRules.filter((rule) => !selectedIDs.has(rule.id));
		return mutationSuccess('firewall_traffic_rules_deleted') as DemoNetworkResponse<T>;
	}
	if (path === '/network/firewall/traffic/counters' && method === 'GET') {
		return success(
			state.trafficRules.map((rule, index) => ({
				id: rule.id,
				packets: (index + 1) * 18_421,
				bytes: (index + 1) * 24_881_920,
				updatedAt: new Date().toISOString()
			}))
		) as DemoNetworkResponse<T>;
	}
	if (path === '/network/firewall/traffic/reorder' && method === 'PUT') {
		if (Array.isArray(config.data)) {
			for (const item of config.data as Array<Record<string, unknown>>) {
				const rule = state.trafficRules.find((candidate) => candidate.id === Number(item.id));
				if (rule) rule.priority = Math.trunc(Number(item.priority));
			}
			state.trafficRules.sort((a, b) => a.priority - b.priority);
		}
		return mutationSuccess('firewall_traffic_rules_reordered') as DemoNetworkResponse<T>;
	}
	match = path.match(/^\/network\/firewall\/traffic\/(\d+)$/);
	if (match && method === 'PUT') {
		const id = Number(match[1]);
		const index = state.trafficRules.findIndex((item) => item.id === id);
		if (index < 0)
			return failure(
				'firewall_rule_not_found',
				'firewall_rule_not_found'
			) as DemoNetworkResponse<T>;
		state.trafficRules[index] = buildTrafficRule(
			state.trafficRules[index].id,
			body,
			state.trafficRules[index]
		);
		return mutationSuccess('firewall_traffic_rule_updated') as DemoNetworkResponse<T>;
	}
	if (match && method === 'DELETE') {
		if (!deleteByID(state.trafficRules, Number(match[1]))) {
			return failure(
				'firewall_rule_not_found',
				'firewall_rule_not_found'
			) as DemoNetworkResponse<T>;
		}
		return mutationSuccess('firewall_traffic_rule_deleted') as DemoNetworkResponse<T>;
	}

	if (path === '/network/firewall/nat' && method === 'GET') {
		return success(state.natRules) as DemoNetworkResponse<T>;
	}
	if (path === '/network/firewall/nat' && method === 'POST') {
		const id = nextID(state.natRules);
		state.natRules.push(buildNATRule(id, body));
		return success(id, 'firewall_nat_rule_created', 201) as DemoNetworkResponse<T>;
	}
	if (path === '/network/firewall/nat' && method === 'DELETE') {
		const ids = strictPositiveIntegerArray(body, 'ids', 1024);
		if (!ids) {
			return failure(
				'invalid_request',
				'invalid_firewall_nat_request',
				400
			) as DemoNetworkResponse<T>;
		}
		const selected = ids.map((id) => state.natRules.find((rule) => rule.id === id));
		if (selected.some((rule) => !rule)) {
			return failure(
				'failed_to_delete_firewall_nat_rules',
				'firewall_nat_rule_not_found',
				404
			) as DemoNetworkResponse<T>;
		}
		if (selected.some((rule) => rule?.visible === false)) {
			return failure(
				'failed_to_delete_firewall_nat_rules',
				'hidden_firewall_rule_managed_by_wireguard',
				409
			) as DemoNetworkResponse<T>;
		}
		const selectedIDs = new Set(ids);
		state.natRules = state.natRules.filter((rule) => !selectedIDs.has(rule.id));
		return mutationSuccess('firewall_nat_rules_deleted') as DemoNetworkResponse<T>;
	}
	if (path === '/network/firewall/nat/counters' && method === 'GET') {
		return success(
			state.natRules.map((rule, index) => ({
				id: rule.id,
				packets: (index + 1) * 9_821,
				bytes: (index + 1) * 18_442_240,
				updatedAt: new Date().toISOString()
			}))
		) as DemoNetworkResponse<T>;
	}
	if (path === '/network/firewall/nat/reorder' && method === 'PUT') {
		if (Array.isArray(config.data)) {
			for (const item of config.data as Array<Record<string, unknown>>) {
				const rule = state.natRules.find((candidate) => candidate.id === Number(item.id));
				if (rule) rule.priority = Math.trunc(Number(item.priority));
			}
			state.natRules.sort((a, b) => a.priority - b.priority);
		}
		return mutationSuccess('firewall_nat_rules_reordered') as DemoNetworkResponse<T>;
	}
	match = path.match(/^\/network\/firewall\/nat\/(\d+)\/route-suggestions$/);
	if (match && method === 'GET') {
		const id = Number(match[1]);
		const rule = state.natRules.find((item) => item.id === id);
		if (!rule)
			return failure(
				'firewall_nat_rule_not_found',
				'firewall_nat_rule_not_found'
			) as DemoNetworkResponse<T>;
		return success([
			{
				name: `Route for ${rule.name}`,
				description: 'Suggested from NAT policy routing',
				enabled: true,
				fib: 0,
				destinationType: 'network',
				destination: rule.sourceRaw || '10.30.0.0/24',
				family: rule.family === 'inet6' ? 'inet6' : 'inet',
				nextHopMode: rule.policyRouteGateway ? 'gateway' : 'interface',
				gateway: rule.policyRouteGateway,
				gatewayZone: '',
				interface: rule.egressInterfaces[0] || 'igb0',
				sourceHint: `NAT rule ${rule.id}`
			}
		]) as DemoNetworkResponse<T>;
	}
	match = path.match(/^\/network\/firewall\/nat\/(\d+)$/);
	if (match && method === 'PUT') {
		const id = Number(match[1]);
		const index = state.natRules.findIndex((item) => item.id === id);
		if (index < 0)
			return failure(
				'firewall_nat_rule_not_found',
				'firewall_nat_rule_not_found'
			) as DemoNetworkResponse<T>;
		state.natRules[index] = buildNATRule(state.natRules[index].id, body, state.natRules[index]);
		return mutationSuccess('firewall_nat_rule_updated') as DemoNetworkResponse<T>;
	}
	if (match && method === 'DELETE') {
		if (!deleteByID(state.natRules, Number(match[1]))) {
			return failure(
				'firewall_nat_rule_not_found',
				'firewall_nat_rule_not_found'
			) as DemoNetworkResponse<T>;
		}
		return mutationSuccess('firewall_nat_rule_deleted') as DemoNetworkResponse<T>;
	}

	if (path === '/network/firewall/advanced' && method === 'GET') {
		return success(state.advanced) as DemoNetworkResponse<T>;
	}
	if (path === '/network/firewall/advanced' && method === 'PUT') {
		Object.assign(state.advanced, {
			preRules: stringValue(body, 'preRules'),
			preNatDecl: stringValue(body, 'preNatDecl'),
			postNatDecl: stringValue(body, 'postNatDecl'),
			preTrafficAnchor: stringValue(body, 'preTrafficAnchor'),
			postTrafficAnchor: stringValue(body, 'postTrafficAnchor'),
			postRules: stringValue(body, 'postRules'),
			updatedAt: new Date().toISOString()
		});
		return mutationSuccess('firewall_advanced_settings_updated') as DemoNetworkResponse<T>;
	}
	if (path === '/network/firewall/advanced/preview' && method === 'POST') {
		return success(
			renderedConfig(state, {
				...state.advanced,
				preRules: stringValue(body, 'preRules'),
				preNatDecl: stringValue(body, 'preNatDecl'),
				postNatDecl: stringValue(body, 'postNatDecl'),
				preTrafficAnchor: stringValue(body, 'preTrafficAnchor'),
				postTrafficAnchor: stringValue(body, 'postTrafficAnchor'),
				postRules: stringValue(body, 'postRules')
			})
		) as DemoNetworkResponse<T>;
	}
	if (path === '/network/firewall/advanced/rendered' && method === 'GET') {
		return success(renderedConfig(state)) as DemoNetworkResponse<T>;
	}
	if (path === '/network/firewall/logs/live' && method === 'GET') {
		const cursor = Math.max(0, Number(parsed.searchParams.get('cursor')) || 0);
		const limit = Math.max(1, Math.min(500, Number(parsed.searchParams.get('limit')) || 200));
		const items = state.liveHits.filter((hit) => hit.cursor > cursor).slice(0, limit);
		return success({
			items,
			nextCursor: items.at(-1)?.cursor ?? cursor,
			sourceStatus: 'ok',
			sourceError: '',
			updatedAt: new Date().toISOString()
		}) as DemoNetworkResponse<T>;
	}

	if (path === '/network/wireguard/server' && method === 'GET') {
		return state.wireGuardServer
			? (success(state.wireGuardServer, 'wireguard_server_loaded') as DemoNetworkResponse<T>)
			: (success(null, 'wireguard_server_not_initialized') as DemoNetworkResponse<T>);
	}
	if (path === '/network/wireguard/server' && method === 'POST') {
		const now = new Date().toISOString();
		state.wireGuardServer = {
			id: 1,
			enabled: true,
			port: Math.trunc(numberValue(body, 'port', 51820)),
			addresses: stringArray(body, 'addresses'),
			allowWireGuardPort: booleanValue(body, 'allowWireGuardPort'),
			masqueradeIPv4Interface: stringValue(body, 'masqueradeIPv4Interface'),
			masqueradeIPv6Interface: stringValue(body, 'masqueradeIPv6Interface'),
			privateKey: stringValue(body, 'privateKey', 'FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF='),
			publicKey: 'GGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG=',
			peers: [],
			mtu: Math.trunc(numberValue(body, 'mtu', 1420)),
			metric: 0,
			rx: 0,
			tx: 0,
			uptime: 0,
			lastHandshake: '0001-01-01T00:00:00Z',
			restartedAt: now,
			createdAt: now,
			updatedAt: now
		};
		return mutationSuccess('wireguard_server_initialized', 201) as DemoNetworkResponse<T>;
	}
	if (path === '/network/wireguard/server' && method === 'PUT') {
		const server = state.wireGuardServer;
		if (!server)
			return failure(
				'wireguard_server_not_initialized',
				'wireguard_server_not_initialized'
			) as DemoNetworkResponse<T>;
		server.port = Math.trunc(numberValue(body, 'port', server.port));
		server.addresses = stringArray(body, 'addresses');
		server.mtu = Math.trunc(numberValue(body, 'mtu', server.mtu));
		server.privateKey = stringValue(body, 'privateKey', server.privateKey);
		server.allowWireGuardPort = booleanValue(body, 'allowWireGuardPort', server.allowWireGuardPort);
		server.masqueradeIPv4Interface = stringValue(
			body,
			'masqueradeIPv4Interface',
			server.masqueradeIPv4Interface
		);
		server.masqueradeIPv6Interface = stringValue(
			body,
			'masqueradeIPv6Interface',
			server.masqueradeIPv6Interface
		);
		server.updatedAt = new Date().toISOString();
		return mutationSuccess('wireguard_server_updated') as DemoNetworkResponse<T>;
	}
	if (path === '/network/wireguard/server' && method === 'PATCH') {
		if (!state.wireGuardServer)
			return failure(
				'wireguard_server_not_initialized',
				'wireguard_server_not_initialized'
			) as DemoNetworkResponse<T>;
		state.wireGuardServer.enabled = booleanValue(body, 'enabled', state.wireGuardServer.enabled);
		state.wireGuardServer.updatedAt = new Date().toISOString();
		return mutationSuccess('wireguard_server_state_updated') as DemoNetworkResponse<T>;
	}
	if (path === '/network/wireguard/server' && method === 'DELETE') {
		state.wireGuardServer = null;
		return mutationSuccess('wireguard_server_deinitialized') as DemoNetworkResponse<T>;
	}
	if (path === '/network/wireguard/server/peer' && method === 'POST') {
		const server = state.wireGuardServer;
		if (!server)
			return failure(
				'wireguard_server_not_initialized',
				'wireguard_server_not_initialized'
			) as DemoNetworkResponse<T>;
		server.peers.push(buildWireGuardPeer(server, nextID(server.peers), body));
		return mutationSuccess('wireguard_peer_created', 201) as DemoNetworkResponse<T>;
	}
	match = path.match(/^\/network\/wireguard\/server\/peer\/(\d+)$/);
	if (match && method === 'PUT') {
		const server = state.wireGuardServer;
		const id = Number(match[1]);
		const index = server?.peers.findIndex((item) => item.id === id) ?? -1;
		if (!server || index < 0)
			return failure(
				'wireguard_peer_not_found',
				'wireguard_peer_not_found'
			) as DemoNetworkResponse<T>;
		server.peers[index] = buildWireGuardPeer(
			server,
			server.peers[index].id,
			body,
			server.peers[index]
		);
		return mutationSuccess('wireguard_peer_updated') as DemoNetworkResponse<T>;
	}
	if (match && method === 'PATCH') {
		const id = Number(match[1]);
		const peer = state.wireGuardServer?.peers.find((item) => item.id === id);
		if (!peer)
			return failure(
				'wireguard_peer_not_found',
				'wireguard_peer_not_found'
			) as DemoNetworkResponse<T>;
		peer.enabled = booleanValue(body, 'enabled', peer.enabled);
		peer.updatedAt = new Date().toISOString();
		return mutationSuccess('wireguard_peer_state_updated') as DemoNetworkResponse<T>;
	}
	if (match && method === 'DELETE') {
		if (!state.wireGuardServer || !deleteByID(state.wireGuardServer.peers, Number(match[1]))) {
			return failure(
				'wireguard_peer_not_found',
				'wireguard_peer_not_found'
			) as DemoNetworkResponse<T>;
		}
		return mutationSuccess('wireguard_peer_deleted') as DemoNetworkResponse<T>;
	}

	if (path === '/network/wireguard/clients' && method === 'GET') {
		return success(state.wireGuardClients, 'wireguard_clients_loaded') as DemoNetworkResponse<T>;
	}
	if (path === '/network/wireguard/clients' && method === 'POST') {
		const id = nextID(state.wireGuardClients);
		state.wireGuardClients.push(buildWireGuardClient(id, body));
		return mutationSuccess('wireguard_client_created', 201) as DemoNetworkResponse<T>;
	}
	match = path.match(/^\/network\/wireguard\/clients\/(\d+)$/);
	if (match && method === 'PUT') {
		const id = Number(match[1]);
		const index = state.wireGuardClients.findIndex((item) => item.id === id);
		if (index < 0)
			return failure(
				'wireguard_client_not_found',
				'wireguard_client_not_found'
			) as DemoNetworkResponse<T>;
		state.wireGuardClients[index] = buildWireGuardClient(
			state.wireGuardClients[index].id,
			body,
			state.wireGuardClients[index]
		);
		return mutationSuccess('wireguard_client_updated') as DemoNetworkResponse<T>;
	}
	if (match && method === 'PATCH') {
		const id = Number(match[1]);
		const client = state.wireGuardClients.find((item) => item.id === id);
		if (!client)
			return failure(
				'wireguard_client_not_found',
				'wireguard_client_not_found'
			) as DemoNetworkResponse<T>;
		client.enabled = booleanValue(body, 'enabled', client.enabled);
		client.updatedAt = new Date().toISOString();
		return mutationSuccess('wireguard_client_state_updated') as DemoNetworkResponse<T>;
	}
	if (match && method === 'DELETE') {
		if (!deleteByID(state.wireGuardClients, Number(match[1]))) {
			return failure(
				'wireguard_client_not_found',
				'wireguard_client_not_found'
			) as DemoNetworkResponse<T>;
		}
		return mutationSuccess('wireguard_client_deleted') as DemoNetworkResponse<T>;
	}

	if (path === '/mdns/config' && method === 'GET') {
		return success(state.mdnsSettings) as DemoNetworkResponse<T>;
	}
	if (path === '/mdns/config' && method === 'PUT') {
		state.mdnsSettings.interfaces = stringValue(body, 'interfaces');
		state.mdnsSettings.hostname = stringValue(body, 'hostname', hostname);
		return mutationSuccess('mdns_config_updated') as DemoNetworkResponse<T>;
	}
	if (path === '/mdns/records' && method === 'GET') {
		return success(state.mdnsRecords) as DemoNetworkResponse<T>;
	}
	if (path === '/mdns/records' && method === 'POST') {
		const id = nextID(state.mdnsRecords);
		state.mdnsRecords.push({
			id,
			name: stringValue(body, 'name', `service-${id}`),
			type: stringValue(body, 'type', '_http._tcp'),
			port: Math.trunc(numberValue(body, 'port', 80)),
			txt:
				typeof body.txt === 'object' && body.txt !== null
					? (body.txt as Record<string, string>)
					: {},
			interfaces: stringValue(body, 'interfaces'),
			createdAt: new Date().toISOString(),
			updatedAt: new Date().toISOString(),
			managed: false,
			source: 'user'
		});
		return mutationSuccess('mdns_record_created', 201) as DemoNetworkResponse<T>;
	}
	match = path.match(/^\/mdns\/records\/(\d+)$/);
	if (match && method === 'PUT') {
		const record = state.mdnsRecords.find((item) => item.id === Number(match?.[1]));
		if (!record || record.managed)
			return failure('mdns_record_not_found', 'mdns_record_not_found') as DemoNetworkResponse<T>;
		record.name = stringValue(body, 'name', record.name);
		record.type = stringValue(body, 'type', record.type);
		record.port = Math.trunc(numberValue(body, 'port', record.port));
		record.txt =
			typeof body.txt === 'object' && body.txt !== null
				? (body.txt as Record<string, string>)
				: record.txt;
		record.interfaces = stringValue(body, 'interfaces', record.interfaces);
		record.updatedAt = new Date().toISOString();
		return mutationSuccess('mdns_record_updated') as DemoNetworkResponse<T>;
	}
	if (match && method === 'DELETE') {
		const record = state.mdnsRecords.find((item) => item.id === Number(match?.[1]));
		if (!record || record.managed)
			return failure('mdns_record_not_found', 'mdns_record_not_found') as DemoNetworkResponse<T>;
		deleteByID(state.mdnsRecords, record.id);
		return mutationSuccess('mdns_record_deleted') as DemoNetworkResponse<T>;
	}

	if (path === '/dynamic-dns/entries' && method === 'GET') {
		return success(state.dynamicDNSEntries) as DemoNetworkResponse<T>;
	}
	if (path === '/dynamic-dns/entries' && method === 'POST') {
		const entry = dynamicDNSEntryFromInput(
			nextID(state.dynamicDNSEntries),
			body as unknown as DynamicDNSEntryInput
		);
		state.dynamicDNSEntries.push(entry);
		return success(entry, 'dynamic_dns_entry_created', 201) as DemoNetworkResponse<T>;
	}
	match = path.match(/^\/dynamic-dns\/entries\/(\d+)$/);
	if (match && method === 'PUT') {
		const index = state.dynamicDNSEntries.findIndex((item) => item.id === Number(match?.[1]));
		if (index < 0)
			return failure(
				'dynamic_dns_entry_not_found',
				'dynamic_dns_entry_not_found'
			) as DemoNetworkResponse<T>;
		const entry = dynamicDNSEntryFromInput(
			state.dynamicDNSEntries[index].id,
			body as unknown as DynamicDNSEntryInput,
			state.dynamicDNSEntries[index]
		);
		state.dynamicDNSEntries[index] = entry;
		return success(entry, 'dynamic_dns_entry_updated') as DemoNetworkResponse<T>;
	}
	if (match && method === 'DELETE') {
		const id = Number(match[1]);
		if (!state.dynamicDNSEntries.some((item) => item.id === id))
			return failure(
				'dynamic_dns_entry_not_found',
				'dynamic_dns_entry_not_found'
			) as DemoNetworkResponse<T>;
		deleteByID(state.dynamicDNSEntries, id);
		return mutationSuccess('dynamic_dns_entry_deleted') as DemoNetworkResponse<T>;
	}
	match = path.match(/^\/dynamic-dns\/entries\/(\d+)\/sync$/);
	if (match && method === 'POST') {
		const entry = state.dynamicDNSEntries.find((item) => item.id === Number(match?.[1]));
		if (!entry)
			return failure(
				'dynamic_dns_entry_not_found',
				'dynamic_dns_entry_not_found'
			) as DemoNetworkResponse<T>;
		const now = new Date().toISOString();
		entry.lastStatus = entry.enabled ? 'success' : '';
		entry.lastError = '';
		entry.ipv4Status = entry.enabled && entry.recordType !== 'AAAA' ? 'success' : '';
		entry.ipv6Status = entry.enabled && entry.recordType !== 'A' ? 'success' : '';
		entry.lastIPv4 = entry.recordType !== 'AAAA' ? entry.lastIPv4 || '203.0.113.42' : '';
		entry.lastIPv6 = entry.recordType !== 'A' ? entry.lastIPv6 || '2001:db8::42' : '';
		entry.lastSyncAt = now;
		entry.lastSuccessAt = entry.enabled ? now : entry.lastSuccessAt;
		entry.updatedAt = now;
		entry.consecutiveFailures = 0;
		entry.nextRetryAt = null;
		return success(entry, 'dynamic_dns_entry_synced') as DemoNetworkResponse<T>;
	}

	return null;
}
