import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	PoolsDiskUsageSchema,
	PoolStatPointsResponseSchema,
	ZpoolSchema,
	ZPoolStatusPoolSchema,
	type CreateZpool,
	type PoolsDiskUsage,
	type PoolStatPointsResponse,
	type ReplaceDevice,
	type Zpool,
	type ZpoolStatusPool
} from '$lib/types/zfs/pool';
import { apiRequest } from '$lib/utils/http';
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
	} catch (error) {
		return 0;
	}
}

export async function getPoolsDiskUsageFull(): Promise<PoolsDiskUsage> {
	try {
		const response = await apiRequest('/zfs/pools/disks-usage', PoolsDiskUsageSchema, 'GET');
		return response;
	} catch (error) {
		return { total: 0, usage: 0 };
	}
}

export async function createPool(data: CreateZpool) {
	return await apiRequest('/zfs/pools', APIResponseSchema, 'POST', {
		...data
	});
}

export async function replaceDevice(data: ReplaceDevice) {
	return await apiRequest(`/zfs/pools/${data.guid}/replace-device`, APIResponseSchema, 'PATCH', {
		...data
	});
}

export async function deletePool(guid: string) {
	return await apiRequest(`/zfs/pools/${guid}`, APIResponseSchema, 'DELETE');
}

export async function scrubPool(guid: string) {
	return await apiRequest(`/zfs/pools/${guid}/scrub`, APIResponseSchema, 'POST');
}

export async function getPoolStats(
	interval: number,
	limit: number
): Promise<PoolStatPointsResponse> {
	return await apiRequest(
		`/zfs/pool/stats/${interval}/${limit}`,
		PoolStatPointsResponseSchema,
		'GET'
	);
}

export async function editPool(
	name: string,
	properties: Record<string, string>,
	spares: string[] = []
): Promise<APIResponse> {
	return await apiRequest(`/zfs/pools`, APIResponseSchema, 'PATCH', {
		name,
		properties,
		spares
	});
}

export async function detachDevice(guid: string, device: string): Promise<APIResponse> {
	return await apiRequest(`/zfs/pools/${guid}/detach`, APIResponseSchema, 'POST', {
		device
	});
}
