import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	PoolsDiskUsageSchema,
	ZFSDashboardHistorySchema,
	ZFSDashboardSnapshotSchema,
	ZpoolSchema,
	ZPoolStatusPoolSchema,
	type CreateZpool,
	type PoolsDiskUsage,
	type ZFSDashboardHistory,
	type ZFSDashboardSnapshot,
	type ReplaceDevice,
	type Zpool,
	type ZpoolStatusPool
} from '$lib/types/zfs/pool';
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';
import { z } from 'zod/v4';

const PoolsResponseSchema = APIResponseSchema.extend({
	data: ZpoolSchema.array().nullable().optional()
});

export type PoolsResponse = z.infer<typeof PoolsResponseSchema>;

export async function getPoolStatus(guid: string): Promise<ZpoolStatusPool> {
	return await apiRequest(`/zfs/pools/${guid}/status`, ZPoolStatusPoolSchema, 'GET');
}

export async function getPools(all?: boolean, hostname?: string): Promise<Zpool[]> {
	const url = all ? '/zfs/pools?all=true' : '/zfs/pools';
	return await apiRequest(url, ZpoolSchema.array(), 'GET', undefined, { hostname });
}

export async function getPoolsResult(
	all?: boolean,
	options?: NodeAPIRequestOptions
): Promise<Zpool[] | APIResponse> {
	const url = all ? '/zfs/pools?all=true' : '/zfs/pools';
	return await apiRequest(url, ZpoolSchema.array(), 'GET', undefined, {
		...options,
		preserveErrors: true
	});
}

export async function getPoolsResponse(all?: boolean, hostname?: string): Promise<PoolsResponse> {
	const url = all ? '/zfs/pools?all=true' : '/zfs/pools';
	const response = await apiRequest(url, PoolsResponseSchema, 'GET', undefined, {
		hostname,
		raw: true
	});
	const parsed = PoolsResponseSchema.safeParse(response);

	if (parsed.success) {
		return parsed.data;
	}

	return {
		status: 'error',
		message: 'Invalid pools response',
		error: 'The server response did not match the expected pools format.'
	};
}

export async function getPoolsDiskUsage(): Promise<number> {
	try {
		const response = await apiRequest('/zfs/pools/disks-usage', PoolsDiskUsageSchema, 'GET');
		return response.usage || 0;
	} catch {
		return 0;
	}
}

export async function getPoolsDiskUsageFull(): Promise<PoolsDiskUsage> {
	try {
		const response = await apiRequest('/zfs/pools/disks-usage', PoolsDiskUsageSchema, 'GET');
		return response;
	} catch {
		return { total: 0, usage: 0 };
	}
}

export async function createPool(data: CreateZpool) {
	return await apiRequest('/zfs/pools', APIResponseSchema, 'POST', {
		...data
	});
}

export async function replaceDevice(data: ReplaceDevice) {
	const { guid, ...request } = data;
	return await apiRequest(`/zfs/pools/${guid}/replace-device`, APIResponseSchema, 'POST', {
		...request
	});
}

export async function deletePool(guid: string) {
	return await apiRequest(`/zfs/pools/${guid}`, APIResponseSchema, 'DELETE');
}

export async function scrubPool(guid: string) {
	return await apiRequest(`/zfs/pools/${guid}/scrub`, APIResponseSchema, 'POST');
}

export async function getZFSDashboardHistory(
	rangeSeconds: number,
	poolGuid = '',
	maxPoints = 900,
	options?: NodeAPIRequestOptions
): Promise<ZFSDashboardHistory> {
	const query = new URLSearchParams({
		rangeSeconds: rangeSeconds.toString(),
		maxPoints: maxPoints.toString()
	});
	if (poolGuid) query.set('poolGuid', poolGuid);
	return await apiRequest(
		`/zfs/dashboard/history?${query.toString()}`,
		ZFSDashboardHistorySchema,
		'GET',
		undefined,
		options
	);
}

export async function getZFSDashboardSnapshot(
	options?: NodeAPIRequestOptions
): Promise<ZFSDashboardSnapshot> {
	return await apiRequest(
		'/zfs/dashboard/snapshot',
		ZFSDashboardSnapshotSchema,
		'GET',
		undefined,
		options
	);
}

export async function getZFSDashboardHistoryDelta(
	poolAfter: number,
	arcAfter: number,
	poolGuid = '',
	options?: NodeAPIRequestOptions
): Promise<ZFSDashboardHistory> {
	const query = new URLSearchParams({
		poolAfter: poolAfter.toString(),
		arcAfter: arcAfter.toString()
	});
	if (poolGuid) query.set('poolGuid', poolGuid);
	return await apiRequest(
		`/zfs/dashboard/history/delta?${query.toString()}`,
		ZFSDashboardHistorySchema,
		'GET',
		undefined,
		options
	);
}

export async function editPool(
	guid: string,
	properties: Record<string, string>,
	spares: string[] = []
): Promise<APIResponse> {
	return await apiRequest(`/zfs/pools/${guid}`, APIResponseSchema, 'PATCH', {
		properties,
		spares
	});
}

export async function detachDevice(guid: string, device: string): Promise<APIResponse> {
	return await apiRequest(`/zfs/pools/${guid}/detach`, APIResponseSchema, 'POST', {
		device
	});
}
