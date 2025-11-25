import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { apiRequest } from '$lib/utils/http';

export async function storageDetach(vmId: number, storageId: number): Promise<APIResponse> {
	return await apiRequest(`/vm/storage/detach`, APIResponseSchema, 'POST', {
		vmId,
		storageId
	});
}

export async function storageAttach(
	vmId: number,
	storageType: string,
	dataset: string,
	emulation: string,
	size: number,
	name: string
): Promise<APIResponse> {
	return await apiRequest(`/vm/storage/attach`, APIResponseSchema, 'POST', {
		vmId,
		storageType,
		dataset,
		emulation,
		size,
		name
	});
}

export async function storageImport(
	vmId: number,
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
		vmId,
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
	vmId: number,
	name: string,
	storageType: 'zvol' | 'raw' | 'image',
	size: number,
	emulation: 'ahci-hd' | 'ahci-cd' | 'nvme' | 'virtio-blk',
	pool: string,
	bootOrder: number
) {
	return await apiRequest('/vm/storage/attach', APIResponseSchema, 'POST', {
		vmId,
		name,
		attachType: 'new',
		size,
		emulation,
		storageType,
		pool,
		bootOrder
	});
}

export async function reorderBootOrder(
	vmId: number,
	storages: { id: number; order: number }[]
): Promise<APIResponse> {
	return await apiRequest(`/vm/storage/reorder-boot-order`, APIResponseSchema, 'POST', {
		vmId,
		storages
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
