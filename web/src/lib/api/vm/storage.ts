import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { VMStorageSchema, type VMStorage, type VMStorageEmulationType } from '$lib/types/vm/vm';
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';

type StorageAttachBase = {
	name: string;
	bootOrder?: number;
	recordSize?: number;
	volBlockSize?: number;
};

type BlockStorageAttachBase = StorageAttachBase & {
	emulation: Exclude<VMStorageEmulationType, 'virtio-9p'>;
};

export type StorageAttachRequest =
	| (BlockStorageAttachBase & {
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
	| (BlockStorageAttachBase & {
			attachType: 'import';
			storageType: 'raw';
			pool: string;
			rawPath: string;
	  })
	| (BlockStorageAttachBase & {
			attachType: 'import';
			storageType: 'zvol';
			pool: string;
			dataset: string;
	  })
	| (BlockStorageAttachBase & {
			attachType: 'import';
			storageType: 'image';
			downloadUUID: string;
	  });

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

export async function storageDetach(
	rid: number,
	storageId: number,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequest(
		`/vm/${rid}/storage/${storageId}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		{
			...options,
			preserveErrors: true
		}
	);
}
