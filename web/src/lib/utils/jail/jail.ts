import type { CreateData } from '$lib/types/jail/jail';
import { toast } from 'svelte-sonner';
import { isValidVMName } from '../string';

export function validateMetadata(meta: string): boolean {
	if (meta.length === 0) {
		return true;
	}

	if (meta.length > 2048) {
		return false;
	}

	const lines = meta.split('\n');
	for (const line of lines) {
		const trimmed = line.trim();
		if (trimmed.length === 0) continue;

		const eqCount = (trimmed.match(/=/g) || []).length;
		if (eqCount !== 1) return false;

		const equalIndex = trimmed.indexOf('=');
		if (equalIndex <= 0 || equalIndex === trimmed.length - 1) {
			return false;
		}
	}

	return true;
}

export async function isValidCreateData(modal: CreateData): Promise<boolean> {
	const toastConfig: Record<string, unknown> = {
		duration: 3000,
		position: 'bottom-center'
	};

	if (!isValidVMName(modal.name)) {
		toast.error('Invalid name', toastConfig);
		return false;
	}

	if (modal.id < 1 || modal.id > 9999) {
		toast.error('Invalid ID', toastConfig);
		return false;
	}

	if (modal.description && (modal.description.length < 1 || modal.description.length > 1024)) {
		toast.error('Invalid description', toastConfig);
		return false;
	}

	if (modal.storage.pool.length < 1) {
		toast.error('No ZFS pool selected', toastConfig);
		return false;
	}

	if (modal.storage.base.length < 1) {
		toast.error('No base selected', toastConfig);
		return false;
	}

	if (modal.network.switch.toLowerCase() !== 'none') {
		if (modal.advanced.jailType === 'linux') {
			if (modal.network.ipv4 !== 0 || modal.network.ipv6 !== 0) {
				toast.error('Linux jails cannot have static IPs assigned', toastConfig);
				return false;
			}

			if (modal.network.dhcp === true || modal.network.slaac === true) {
				toast.error('Linux jails cannot use DHCP or SLAAC', toastConfig);
				return false;
			}
		}

		if (modal.network.switch.toLowerCase() === 'inherit') {
			if (modal.network.inheritIPv4 === false && modal.network.inheritIPv6 === false) {
				toast.error('Either IPv4 or IPv6 must be inherited', toastConfig);
				return false;
			}
		}
	}

	if (modal.advanced.metadata.env.length > 2048 || modal.advanced.metadata.meta.length > 2048) {
		toast.error('Metadata too long', toastConfig);
		return false;
	}

	if (
		!validateMetadata(modal.advanced.metadata.env) ||
		!validateMetadata(modal.advanced.metadata.meta)
	) {
		toast.error('Invalid metadata format', toastConfig);
		return false;
	}

	return true;
}

export function generateSimpleLinuxFSTab(ctId: number, pool: string): string {
	const base = `/${pool}/sylve/jails/${ctId}`;

	const entries = [
		{ fs: 'devfs', mp: `${base}/dev`, type: 'devfs', opts: 'rw' },
		{ fs: 'tmpfs', mp: `${base}/dev/shm`, type: 'tmpfs', opts: 'rw,size=1g,mode=1777' },
		{ fs: 'fdescfs', mp: `${base}/dev/fd`, type: 'fdescfs', opts: 'rw,linrdlnk' },
		{ fs: 'linprocfs', mp: `${base}/proc`, type: 'linprocfs', opts: 'rw' },
		{ fs: 'linsysfs', mp: `${base}/sys`, type: 'linsysfs', opts: 'rw' }
	];

	return entries.map((e) => `${e.fs}\t${e.mp}\t${e.type}\t${e.opts}\t0\t0`).join('\n') + '\n';
}
