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

export async function getPoolStatus(guid: string): Promise<ZpoolStatusPool> {
    return await apiRequest(`/zfs/pools/${guid}/status`, ZPoolStatusPoolSchema, 'GET');
}

export async function getPools(all?: boolean): Promise<Zpool[]> {
    const url = all ? '/zfs/pools?all=true' : '/zfs/pools';
    return await apiRequest(url, ZpoolSchema.array(), 'GET');
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
