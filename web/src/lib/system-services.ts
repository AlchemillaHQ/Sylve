import type { AvailableService } from '$lib/types/system/settings';

interface InitializationServiceDefinition {
	id: AvailableService;
	label: string;
	defaultEnabled: boolean;
}

export const INITIALIZATION_SERVICES = [
	{ id: 'virtualization', label: 'Virtualization', defaultEnabled: true },
	{ id: 'jails', label: 'Jails', defaultEnabled: true },
	{ id: 'samba-server', label: 'Samba Server', defaultEnabled: false },
	{ id: 'dhcp-server', label: 'DHCP Server', defaultEnabled: true },
	{ id: 'wol-server', label: 'WoL Server', defaultEnabled: false },
	{ id: 'firewall', label: 'Firewall', defaultEnabled: false },
	{ id: 'wireguard', label: 'WireGuard', defaultEnabled: false },
	{ id: 'iscsi', label: 'iSCSI', defaultEnabled: false },
	{ id: 'mdns', label: 'mDNS Discovery', defaultEnabled: false }
] as const satisfies readonly InitializationServiceDefinition[];
