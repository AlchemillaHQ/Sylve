import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { VMStorageSchema, type VMStorage, type VMStorageEmulationType } from '$lib/types/vm/vm';
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';

type StorageAttachBase = {
	name: string;
	bootOrder?: number;
	recordSize?: number;
	volBlockSize?: number;
};

type WritableStorageAttachBase = StorageAttachBase & {
	emulation: Exclude<VMStorageEmulationType, 'virtio-9p' | 'ahci-cd'>;
};

type MediaStorageAttachBase = StorageAttachBase & {
	emulation: Exclude<VMStorageEmulationType, 'virtio-9p'>;
};

export type StorageAttachRequest =
	| (WritableStorageAttachBase & {
			attachType: 'new';
			storageType: 'raw' | 'zvol';
			pool: string;
			size: number;
	  })
	| (StorageAttachBase & {
			attachType: 'new';
			storageType: 'filesystem';
			emulation: 'virtio-9p';
			dataset: string;
			filesystemTarget: string;
			readOnly: boolean;
	  })
	| (WritableStorageAttachBase & {
			attachType: 'import';
			storageType: 'raw';
			pool: string;
			rawPath: string;
	  })
	| (WritableStorageAttachBase & {
			attachType: 'import';
			storageType: 'zvol';
			pool: string;
			dataset: string;
	  })
	| (MediaStorageAttachBase & {
			attachType: 'import';
			storageType: 'image';
			downloadUUID: string;
	  });

export type StorageFromImageRequest = {
	downloadUUID: string;
	name: string;
	pool: string;
	storageType: 'raw' | 'zvol';
	size?: number;
	recordSize?: number;
	volBlockSize?: number;
	emulation: Exclude<VMStorageEmulationType, 'virtio-9p' | 'ahci-cd'>;
	bootOrder?: number;
};

export type StorageUpdateRequest = {
	name?: string;
	size?: number;
	emulation?: VMStorageEmulationType;
	bootOrder?: number;
	enable?: boolean;
	filesystemTarget?: string;
	readOnly?: boolean;
};

export async function storageAttach(
	rid: number,
	request: StorageAttachRequest,
	options?: NodeAPIRequestOptions
): Promise<VMStorage | APIResponse> {
	return await apiRequest(`/vm/${rid}/storage`, VMStorageSchema, 'POST', request, {
		...options,
		preserveErrors: true
	});
}

export async function createStorageFromImage(
	rid: number,
	request: StorageFromImageRequest,
	options?: NodeAPIRequestOptions
): Promise<VMStorage | APIResponse> {
	return await apiRequest(`/vm/${rid}/storage/from-image`, VMStorageSchema, 'POST', request, {
		...options,
		preserveErrors: true
	});
}

export async function storageUpdate(
	rid: number,
	storageId: number,
	request: StorageUpdateRequest,
	options?: NodeAPIRequestOptions
): Promise<VMStorage | APIResponse> {
	return await apiRequest(`/vm/${rid}/storage/${storageId}`, VMStorageSchema, 'PATCH', request, {
		...options,
		preserveErrors: true
	});
}

export type StorageDeleteOptions = NodeAPIRequestOptions & {
	deleteBacking?: boolean;
};

export async function storageDelete(
	rid: number,
	storageId: number,
	options?: StorageDeleteOptions
): Promise<APIResponse> {
	const { deleteBacking = false, ...requestOptions } = options ?? {};
	const query = deleteBacking ? '?deleteBacking=true' : '';

	return await apiRequest(
		`/vm/${rid}/storage/${storageId}${query}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		{
			...requestOptions,
			preserveErrors: true
		}
	);
}
