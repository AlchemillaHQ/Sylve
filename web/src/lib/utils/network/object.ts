import type { NetworkObject } from '$lib/types/network/object';
import { isValidIPv4, isValidIPv6 } from '../string';

export function generateIPOptions(
	networkObjects: NetworkObject[] | undefined,
	type: string,
	markValue: boolean = false
): { label: string; value: string }[] {
	if (!networkObjects || networkObjects.length === 0) {
		return [];
	}

	const options = [] as { label: string; value: string }[];
	const objects = networkObjects?.filter((obj) => obj.type === 'Host');
	if (!objects || objects.length === 0) {
		return [];
	}

	for (const object of objects) {
		if (object.entries && object.entries.length === 1) {
			for (const entry of object.entries) {
				const validator = type.toLowerCase() == 'ipv4' ? isValidIPv4 : isValidIPv6;
				if (validator(entry.value)) {
					options.push({
						label: `${object.name} (${entry.value})`,
						value: markValue ? `ip-${object.id.toString()}` : object.id.toString()
					});
				}
			}
		}
	}

	return options;
}

export function generateNetworkOptions(
	networkObjects: NetworkObject[] | undefined,
	type: string,
	markValue: boolean = false
): { label: string; value: string }[] {
	if (!networkObjects || networkObjects.length === 0) {
		return [];
	}

	const options = [] as { label: string; value: string }[];
	const objects = networkObjects?.filter((obj) => obj.type === 'Network');
	if (!objects || objects.length === 0) {
		return [];
	}

	for (const object of objects) {
		if (object.entries && object.entries.length > 0) {
			for (const entry of object.entries) {
				if (type.toLowerCase() === 'ipv4' && isValidIPv4(entry.value, true)) {
					options.push({
						label: `${object.name} (${entry.value})`,
						value: markValue ? `ip-${object.id.toString()}` : object.id.toString()
					});
				} else if (type.toLowerCase() === 'ipv6' && isValidIPv6(entry.value, true)) {
					options.push({
						label: `${object.name} (${entry.value})`,
						value: markValue ? `ip-${object.id.toString()}` : object.id.toString()
					});
				}
			}
		}
	}

	return options;
}

export function generateMACOptions(
	networkObjects: NetworkObject[] | undefined,
	markValue: boolean = false
): { label: string; value: string }[] {
	if (!networkObjects || networkObjects.length === 0) {
		return [];
	}

	const options = [] as { label: string; value: string }[];
	const objects = networkObjects?.filter((obj) => obj.type === 'Mac');
	if (!objects || objects.length === 0) {
		return [];
	}

	for (const object of objects) {
		if (object.entries && object.entries.length > 0) {
			for (const entry of object.entries) {
				options.push({
					label: `${object.name} (${entry.value})`,
					value: markValue ? `mac-${object.id.toString()}` : object.id.toString()
				});
			}
		}
	}

	return options;
}

export function generateDUIDOptions(
	networkObjects: NetworkObject[] | undefined,
	markValue: boolean = false
): { label: string; value: string }[] {
	if (!networkObjects || networkObjects.length === 0) {
		return [];
	}
	const options = [] as { label: string; value: string }[];
	const objects = networkObjects?.filter((obj) => obj.type === 'DUID');
	if (!objects || objects.length === 0) {
		return [];
	}

	for (const object of objects) {
		if (object.entries && object.entries.length > 0) {
			for (const entry of object.entries) {
				options.push({
					label: `${object.name} (${entry.value})`,
					value: markValue ? `duid-${object.id.toString()}` : object.id.toString()
				});
			}
		}
	}

	return options;
}
