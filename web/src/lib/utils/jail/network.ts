import type { NetworkObject } from '$lib/types/network/object';
import { escapeHTML } from '$lib/utils/string';

export function objectLabel(object?: NetworkObject | null): string {
	const value = object?.entries?.length === 1 ? object.entries[0]?.value : '';
	if (!object || !value) return '-';
	return `${escapeHTML(object.name)} (${escapeHTML(value)})`;
}

export function ipGatewayFormatter(
	ip?: NetworkObject | null,
	gateway?: NetworkObject | null
): string {
	const address = objectLabel(ip);
	if (!gateway) return address;
	return `${address}<br>${objectLabel(gateway)}`;
}
