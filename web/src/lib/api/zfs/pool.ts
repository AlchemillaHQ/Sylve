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
import {
	apiRequest,
	apiRequestData,
	apiRequestResult,
	type NodeAPIDataRequestOptions
} from '$lib/utils/http';
import { z } from 'zod/v4';

const PoolsResponseSchema = APIResponseSchema.extend({
	data: ZpoolSchema.array().nullable().optional()
});

export type PoolsResponse = z.infer<typeof PoolsResponseSchema>;

export async function getPoolStatus(guid: string): Promise<ZpoolStatusPool> {
	return await apiRequestData(`/zfs/pools/${guid}/status`, ZPoolStatusPoolSchema, 'GET');
}

export async function getPools(all?: boolean, hostname?: string): Promise<Zpool[]> {
	const url = all ? '/zfs/pools?all=true' : '/zfs/pools';
	return await apiRequestData(url, ZpoolSchema.array(), 'GET', undefined, { hostname });
}

export async function getPoolsResult(
	all?: boolean,
	options?: NodeAPIDataRequestOptions
): Promise<Zpool[] | APIResponse> {
	const url = all ? '/zfs/pools?all=true' : '/zfs/pools';
	return await apiRequestResult(url, ZpoolSchema.array(), 'GET', undefined, options);
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

export async function getPoolsDiskUsage(options?: NodeAPIDataRequestOptions): Promise<number> {
	const response = await apiRequestData(
		'/zfs/pools/disks-usage',
		PoolsDiskUsageSchema,
		'GET',
		undefined,
		options
	);
	return response.usage;
}

export async function getPoolsDiskUsageFull(
	options?: NodeAPIDataRequestOptions
): Promise<PoolsDiskUsage> {
	return await apiRequestData(
		'/zfs/pools/disks-usage',
		PoolsDiskUsageSchema,
		'GET',
		undefined,
		options
	);
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
	options?: NodeAPIDataRequestOptions
): Promise<ZFSDashboardHistory> {
	const query = new URLSearchParams({
		rangeSeconds: rangeSeconds.toString(),
		maxPoints: maxPoints.toString()
	});
	if (poolGuid) query.set('poolGuid', poolGuid);
	return await apiRequestData(
		`/zfs/dashboard/history?${query.toString()}`,
		ZFSDashboardHistorySchema,
		'GET',
		undefined,
		options
	);
}

export async function getZFSDashboardSnapshot(
	options?: NodeAPIDataRequestOptions
): Promise<ZFSDashboardSnapshot> {
	return await apiRequestData(
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
	options?: NodeAPIDataRequestOptions
): Promise<ZFSDashboardHistory> {
	const query = new URLSearchParams({
		poolAfter: poolAfter.toString(),
		arcAfter: arcAfter.toString()
	});
	if (poolGuid) query.set('poolGuid', poolGuid);
	return await apiRequestData(
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
