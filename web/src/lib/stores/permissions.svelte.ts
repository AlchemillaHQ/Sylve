import { storage } from '$lib';
import { getPermissions } from '$lib/api/auth/permissions';
import { isDemoMode } from '$lib/demo/runtime';
import type { Permissions } from '$lib/types/auth';
import { kvStorage } from '$lib/types/db';
import { isAPIResponse } from '$lib/utils/http';

export const permissions = $state<{ data: Permissions | null }>({ data: null });

let loadedFor = '';
let loading: Promise<void> | null = null;

export function loadPermissions(force = false): Promise<void> {
	const token = storage.token;
	if (isDemoMode) return Promise.resolve();
	if (!token) {
		clearPermissions();
		return Promise.resolve();
	}
	if (!force && loadedFor === token) return Promise.resolve();
	loading ??= getPermissions()
		.then(async (result) => {
			if (token !== storage.token) return;
			const data = isAPIResponse(result) ? null : result;
			if (data) await dropCacheIfChanged(data);
			permissions.data = data;
			loadedFor = token;
		})
		.finally(() => {
			loading = null;
		});
	return loading;
}

const fingerprintKey = 'permissions-fingerprint';

// Pages keep API data for days. Once what the user may see changes (new grants), that data can show
// what they no longer may, or hide what they now may, so it goes.
async function dropCacheIfChanged(data: Permissions) {
	const fingerprint = JSON.stringify(data);
	try {
		if (localStorage.getItem(fingerprintKey) === fingerprint) return;
		localStorage.setItem(fingerprintKey, fingerprint);
	} catch {
		// Without localStorage the cache just goes every time.
	}
	await kvStorage.clear().catch(() => {});
}

export function clearPermissions() {
	permissions.data = null;
	loadedFor = '';
}

function parentPath(path: string): string {
	const index = path.lastIndexOf('/');
	return index <= 0 ? '/' : path.slice(0, index);
}

// A deeper grant replaces what's inherited, so the nearest listed path decides: its own
// privileges for the path itself, or what it passes below for a path under it.
export function can(privilege: string, path: string): boolean {
	const data = permissions.data;
	if (!data) return false;
	if (data.superuser) return true;
	for (let current = path; ; current = parentPath(current)) {
		const entry = data.paths[current];
		if (entry) return (current === path ? entry.privileges : entry.below).includes(privilege);
		if (current === '/') return false;
	}
}

// Grants name nodes by ID; the UI knows them by hostname.
export function nodePath(hostname: string): string {
	return `/nodes/${permissions.data?.nodes[hostname] ?? hostname}`;
}

export type Need = [privilege: string, path: string];

// For buttons: until permissions load (and in the demo) everything is allowed; the server still
// refuses.
export function allowed(privilege: string, path: string): boolean {
	return !permissions.data || can(privilege, path);
}

// root@pam and admin only, whatever the grants. Same loading rule as allowed().
export function isSuperuser(): boolean {
	return !permissions.data || permissions.data.superuser;
}

// Guest pages also show host details; a user who can't read the host gets the guest without them
// instead of a refusal.
export async function canReadHost(hostname: string): Promise<boolean> {
	await loadPermissions();
	return allowed('Sys.Audit', nodePath(hostname));
}

export async function hostDetail<T>(
	hostname: string,
	fetch: () => Promise<T>,
	fallback: T
): Promise<T> {
	return (await canReadHost(hostname)) ? await fetch() : fallback;
}

// Until permissions load (and in the demo) everything shows.
export function visibleItems<T extends { need?: Need; children?: T[] }>(items: T[]): T[] {
	if (!permissions.data) return items;
	return items.flatMap((item) => {
		if (item.need && !can(...item.need)) return [];
		if (!item.children) return [item];
		const children = visibleItems(item.children);
		return children.length ? [{ ...item, children }] : [];
	});
}
