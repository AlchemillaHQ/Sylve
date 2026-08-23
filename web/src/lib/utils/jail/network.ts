import type { NetworkObject } from '$lib/types/network/object';
import { escapeHTML } from '$lib/utils/string';

function objectLabel(networkObjects: NetworkObject[], objectId: number): string {
	const object = networkObjects.find((candidate) => candidate.id === objectId);
	const value = object?.entries?.length === 1 ? object.entries[0]?.value : '';
	if (!object || !value) return '-';
	return `${escapeHTML(object.name)} (${escapeHTML(value)})`;
}

export function ipGatewayFormatter(
	networkObjects: NetworkObject[],
	ipId: number,
	ipGwId?: number | null
): string {
	const address = objectLabel(networkObjects, ipId);
	if (!ipGwId) return address;
	return `${address}<br>${objectLabel(networkObjects, ipGwId)}`;
}

export function macFormatter(networkObjects: NetworkObject[], macId: number): string {
	return objectLabel(networkObjects, macId);
}
