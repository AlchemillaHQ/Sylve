import { isValidIPv4, isValidIPv6 } from '$lib/utils/string';

export interface StaticRoutePayload {
	name: string;
	fib: number;
	destinationType: 'host' | 'network' | string;
	destination: string;
	family: 'inet' | 'inet6' | string;
	nextHopMode: 'gateway' | 'interface' | string;
	gateway?: string;
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

	const family = String(payload.family ?? '').trim().toLowerCase();
	if (family !== 'inet' && family !== 'inet6') {
		return { valid: false, error: 'Family must be inet or inet6' };
	}

	const destinationType = String(payload.destinationType ?? '').trim().toLowerCase();
	if (destinationType !== 'host' && destinationType !== 'network') {
		return { valid: false, error: 'Destination type must be host or network' };
	}

	const nextHopMode = String(payload.nextHopMode ?? '').trim().toLowerCase();
	if (nextHopMode !== 'gateway' && nextHopMode !== 'interface') {
		return { valid: false, error: 'Next hop mode must be gateway or interface' };
	}

	const fib = Number(payload.fib);
	if (!Number.isFinite(fib) || fib < 0 || !Number.isInteger(fib)) {
		return { valid: false, error: 'FIB must be a non-negative integer' };
	}

	const destination = String(payload.destination ?? '').trim();
	if (!destination) {
		return { valid: false, error: 'Destination is required' };
	}

	const isDestV4Host = isValidIPv4(destination, false);
	const isDestV6Host = isValidIPv6(destination, false);
	const isDestV4Network = isValidIPv4(destination, true);
	const isDestV6Network = isValidIPv6(destination, true);

	if (destinationType === 'host') {
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

	if (destinationType === 'network') {
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
	const iface = String(payload.interface ?? '').trim();
	if (nextHopMode === 'gateway') {
		if (!gateway) {
			return { valid: false, error: 'Gateway is required for gateway next hop mode' };
		}
		if (iface) {
			return { valid: false, error: 'Interface must be empty for gateway next hop mode' };
		}
		const isGwV4 = isValidIPv4(gateway, false);
		const isGwV6 = isValidIPv6(gateway, false);
		if (!isGwV4 && !isGwV6) {
			return { valid: false, error: 'Gateway must be a valid host IP' };
		}
		if (family === 'inet' && !isGwV4) {
			return { valid: false, error: 'Gateway must be IPv4 for family inet' };
		}
		if (family === 'inet6' && !isGwV6) {
			return { valid: false, error: 'Gateway must be IPv6 for family inet6' };
		}
	} else {
		if (!iface) {
			return { valid: false, error: 'Interface is required for interface next hop mode' };
		}
		if (gateway) {
			return { valid: false, error: 'Gateway must be empty for interface next hop mode' };
		}
	}

	return { valid: true };
}
