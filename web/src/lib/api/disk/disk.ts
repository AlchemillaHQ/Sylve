import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { DiskSchema, type Disk } from '$lib/types/disk/disk';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function listDisks(): Promise<Disk[]> {
	return await apiRequest('/disk/list', z.array(DiskSchema), 'GET');
}

export async function destroyDisk(disk: string): Promise<APIResponse> {
	return await apiRequest(`/disk/wipe`, APIResponseSchema, 'POST', {
		device: disk
	});
}

export async function destroyPartition(partition: string): Promise<APIResponse> {
	return await apiRequest(`/disk/delete-partition`, APIResponseSchema, 'POST', {
		device: partition
	});
}

export async function initializeGPT(disk: string): Promise<APIResponse> {
	return await apiRequest(`/disk/initialize-gpt`, APIResponseSchema, 'POST', {
		device: disk
	});
}

export async function createPartitions(disk: string, sizes: number[]): Promise<APIResponse> {
	return await apiRequest(`/disk/create-partitions`, APIResponseSchema, 'POST', {
		device: disk,
		sizes
	});
}
