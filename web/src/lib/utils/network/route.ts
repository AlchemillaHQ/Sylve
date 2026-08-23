import { isValidIPv4, isValidIPv6, isLinkLocalIPv6 } from '$lib/utils/string';

export interface StaticRoutePayload {
	name: string;
	description?: string;
	enabled?: boolean;
	fib: number;
	destinationType: 'host' | 'network' | string;
	destination: string;
	destinationRaw?: string;
	destinationObjId?: number | null;
	family: 'inet' | 'inet6' | string;
	nextHopMode: 'gateway' | 'interface' | string;
	gateway?: string;
	gatewayRaw?: string;
	gatewayObjId?: number | null;
	gatewayZone?: string;
	interface?: string;
}

export interface RouteValidationResult {
	valid: boolean;
	error?: string;
}

export function validateStaticRoutePayload(payload: StaticRoutePayload): RouteValidationResult {
	const name = String(payload.name ?? '').trim();
	if (!name) {
		return { valid: false, error: 'Route name is required' };
	}
	if (name.length > 128) {
		return { valid: false, error: 'Route name must be 128 characters or fewer' };
	}
	if (String(payload.description ?? '').length > 2048) {
		return { valid: false, error: 'Description must be 2048 characters or fewer' };
	}

	const family = String(payload.family ?? '')
		.trim()
		.toLowerCase();
	if (family !== 'inet' && family !== 'inet6') {
		return { valid: false, error: 'Family must be inet or inet6' };
	}

	const destinationType = String(payload.destinationType ?? '')
		.trim()
		.toLowerCase();
	if (destinationType !== 'host' && destinationType !== 'network') {
		return { valid: false, error: 'Destination type must be host or network' };
	}

	const nextHopMode = String(payload.nextHopMode ?? '')
		.trim()
		.toLowerCase();
	if (nextHopMode !== 'gateway' && nextHopMode !== 'interface') {
		return { valid: false, error: 'Next hop mode must be gateway or interface' };
	}

	const fib = Number(payload.fib);
	if (!Number.isFinite(fib) || fib < 0 || !Number.isInteger(fib)) {
		return { valid: false, error: 'FIB must be a non-negative integer' };
	}

	const destination = String(payload.destination ?? '').trim();
	const destinationObjId = Number(payload.destinationObjId ?? 0);
	const hasDestinationObject = Number.isInteger(destinationObjId) && destinationObjId > 0;
	if (!destination && !hasDestinationObject) {
		return { valid: false, error: 'Destination is required' };
	}
	if (destination && hasDestinationObject) {
		return { valid: false, error: 'Choose either a destination address or object' };
	}

	const isDestV4Host = isValidIPv4(destination, false);
	const isDestV6Host = isValidIPv6(destination, false);
	const isDestV4Network = isValidIPv4(destination, true);
	const isDestV6Network = isValidIPv6(destination, true);

	if (!hasDestinationObject && destinationType === 'host') {
		if (destination.includes('/')) {
			return { valid: false, error: 'Host destination cannot include CIDR notation' };
		}
		if (!isDestV4Host && !isDestV6Host) {
			return { valid: false, error: 'Destination must be a valid host IP' };
		}
		if (family === 'inet' && !isDestV4Host) {
			return { valid: false, error: 'Destination must be IPv4 for family inet' };
		}
		if (family === 'inet6' && !isDestV6Host) {
			return { valid: false, error: 'Destination must be IPv6 for family inet6' };
		}
	}

	if (!hasDestinationObject && destinationType === 'network') {
		if (!isDestV4Network && !isDestV6Network) {
			return { valid: false, error: 'Destination must be a valid CIDR network' };
		}
		if (family === 'inet' && !isDestV4Network) {
			return { valid: false, error: 'Destination must be IPv4 CIDR for family inet' };
		}
		if (family === 'inet6' && !isDestV6Network) {
			return { valid: false, error: 'Destination must be IPv6 CIDR for family inet6' };
		}
	}

	const gateway = String(payload.gateway ?? '').trim();
	const gatewayObjId = Number(payload.gatewayObjId ?? 0);
	const hasGatewayObject = Number.isInteger(gatewayObjId) && gatewayObjId > 0;
	const iface = String(payload.interface ?? '').trim();
	const gatewayZone = String(payload.gatewayZone ?? '').trim();
	if (nextHopMode === 'gateway') {
		if (!gateway && !hasGatewayObject) {
			return { valid: false, error: 'Gateway is required for gateway next hop mode' };
		}
		if (gateway && hasGatewayObject) {
			return { valid: false, error: 'Choose either a gateway address or object' };
		}
		if (iface) {
			return { valid: false, error: 'Interface must be empty for gateway next hop mode' };
		}
		if (!hasGatewayObject && gateway.includes('%')) {
			return {
				valid: false,
				error: 'Gateway must not include a zone — use the Scope Interface field'
			};
		}
		const isGwV4 = !hasGatewayObject && isValidIPv4(gateway, false);
		const isGwV6 = !hasGatewayObject && isValidIPv6(gateway, false);
		if (!hasGatewayObject && !isGwV4 && !isGwV6) {
			return { valid: false, error: 'Gateway must be a valid host IP' };
		}
		if (!hasGatewayObject && family === 'inet' && !isGwV4) {
			return { valid: false, error: 'Gateway must be IPv4 for family inet' };
		}
		if (!hasGatewayObject && family === 'inet6' && !isGwV6) {
			return { valid: false, error: 'Gateway must be IPv6 for family inet6' };
		}
		if (!hasGatewayObject && family === 'inet6' && isLinkLocalIPv6(gateway) && !gatewayZone) {
			return { valid: false, error: 'Scope interface is required for link-local IPv6 gateways' };
		}
		if (gatewayZone) {
			if (family !== 'inet6') {
				return { valid: false, error: 'Scope interface is only valid for IPv6 gateways' };
			}
			if (!hasGatewayObject && !isLinkLocalIPv6(gateway)) {
				return {
					valid: false,
					error: 'Scope interface is only valid for link-local (fe80::/10) gateways'
				};
			}
		}
	} else {
		if (hasGatewayObject) {
			return { valid: false, error: 'Gateway object is only valid in gateway mode' };
		}
		if (!iface) {
			return { valid: false, error: 'Interface is required for interface next hop mode' };
		}
		if (gateway) {
			return { valid: false, error: 'Gateway must be empty for interface next hop mode' };
		}
		if (gatewayZone) {
			return { valid: false, error: 'Scope interface must be empty for interface next hop mode' };
		}
	}

	return { valid: true };
}
