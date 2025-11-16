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
	storageType: 'zvol' | 'raw',
	rawPath: string,
	dataset: string,
	emulation: 'ahci-hd' | 'ahci-cd' | 'nvme' | 'virtio-blk',
	pool: string
) {
	return await apiRequest('/vm/storage/attach', APIResponseSchema, 'POST', {
		vmId,
		attachType: 'import',
		rawPath: storageType === 'zvol' ? '' : rawPath,
		dataset: storageType === 'zvol' ? dataset : '',
		emulation,
		storageType,
		pool
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
