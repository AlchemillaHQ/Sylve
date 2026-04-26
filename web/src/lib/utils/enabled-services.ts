import { browser } from '$app/environment';
import { storage } from '$lib';
import { getBasicSettings } from '$lib/api/system/settings';
import type { AvailableService } from '$lib/types/system/settings';
import { isAPIResponse } from '$lib/utils/http';

const NON_NODE_ROUTE_SEGMENTS = new Set(['', 'datacenter', 'login', 'inactive-node']);

function normalizeHostname(hostname: string | null | undefined): string {
	return hostname?.trim() || '';
}

function normalizeServices(services: AvailableService[] | null | undefined): AvailableService[] {
	return Array.from(new Set(services ?? []));
}

function getActiveNodeHostname(): string | null {
	if (browser) {
		const routeHostname = resolveNodeHostname(window.location.pathname);
		if (routeHostname) {
			return routeHostname;
		}
	}

	return normalizeHostname(storage.localHostname) || normalizeHostname(storage.hostname) || null;
}

export function resolveNodeHostname(pathname: string): string | null {
	const routeHostname = pathname.split('/').filter(Boolean)[0] || '';
	return NON_NODE_ROUTE_SEGMENTS.has(routeHostname) ? null : routeHostname;
}

export function getEnabledServicesForHostname(
	hostname: string | null | undefined
): AvailableService[] {
	const key = normalizeHostname(hostname);

	if (!key) {
		return normalizeServices(storage.enabledServices);
	}

	return normalizeServices(storage.enabledServicesByHostname?.[key]);
}

export function setEnabledServicesForHostname(
	hostname: string | null | undefined,
	services: AvailableService[] | null | undefined
): AvailableService[] {
	const key = normalizeHostname(hostname);
	const normalizedServices = normalizeServices(services);

	if (key) {
		storage.enabledServicesByHostname = {
			...(storage.enabledServicesByHostname ?? {}),
			[key]: normalizedServices
		};
	}

	const activeHostname = getActiveNodeHostname();
	if (!key || !activeHostname || activeHostname === key) {
		storage.enabledServices = normalizedServices;
	}

	return normalizedServices;
}

export function syncActiveEnabledServices(pathname?: string): AvailableService[] {
	const activeHostname = pathname ? resolveNodeHostname(pathname) : getActiveNodeHostname();

	if (!activeHostname) {
		return normalizeServices(storage.enabledServices);
	}

	const normalizedServices = getEnabledServicesForHostname(activeHostname);
	storage.enabledServices = normalizedServices;
	return normalizedServices;
}

export async function loadEnabledServicesForHostname(
	hostname: string | null | undefined
): Promise<AvailableService[]> {
	const key = normalizeHostname(hostname);

	if (!key) {
		return normalizeServices(storage.enabledServices);
	}

	const result = await getBasicSettings(key);
	if (isAPIResponse(result)) {
		return getEnabledServicesForHostname(key);
	}

	return setEnabledServicesForHostname(key, result.services);
}
