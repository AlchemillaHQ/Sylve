import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { apiRequest } from '$lib/utils/http';

export async function storageDetach(rid: number, storageId: number): Promise<APIResponse> {
	return await apiRequest(`/vm/storage/detach`, APIResponseSchema, 'POST', {
		rid,
		storageId
	});
}

export async function storageImport(
	rid: number,
	name: string,
	downloadUUID: string,
	storageType: 'zvol' | 'raw',
	rawPath: string,
	dataset: string,
	emulation: 'ahci-hd' | 'ahci-cd' | 'nvme' | 'virtio-blk',
	pool: string,
	bootOrder: number
) {
	return await apiRequest('/vm/storage/attach', APIResponseSchema, 'POST', {
		rid,
		name,
		downloadUUID,
		attachType: 'import',
		rawPath: storageType === 'zvol' ? '' : rawPath,
		dataset: storageType === 'zvol' ? dataset : '',
		emulation,
		storageType,
		pool,
		bootOrder
	});
}

export async function storageNew(
	rid: number,
	name: string,
	storageType: 'zvol' | 'raw' | 'image',
	size: number,
	emulation: 'ahci-hd' | 'ahci-cd' | 'nvme' | 'virtio-blk',
	pool: string,
	bootOrder: number
) {
	return await apiRequest('/vm/storage/attach', APIResponseSchema, 'POST', {
		rid,
		name,
		attachType: 'new',
		size,
		emulation,
		storageType,
		pool,
		bootOrder
	});
}

export async function storageUpdate(
	id: number,
	name: string,
	size: number,
	emulation: 'ahci-hd' | 'ahci-cd' | 'nvme' | 'virtio-blk',
	bootOrder: number
): Promise<APIResponse> {
	return await apiRequest(`/vm/storage/update`, APIResponseSchema, 'PUT', {
		id,
		name,
		size,
		emulation,
		bootOrder
	});
}
