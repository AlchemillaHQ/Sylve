import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	SambaPrincipalsSchema,
	SambaShareSchema,
	SambaStatusSchema,
	ShareDatasetSchema,
	type SambaPrincipals,
	type SambaShare,
	type SambaShareInput,
	type SambaStatus,
	type ShareDataset
} from '$lib/types/samba/shares';
import {
	apiRequestData,
	apiRequestResult,
	type NodeAPIDataRequestOptions,
	type NodeAPIRequestOptions
} from '$lib/utils/http';
import { z } from 'zod/v4';

export type SambaRequestOptions = { hostname: string; signal?: AbortSignal };

export async function getSambaSharesResult(
	options: SambaRequestOptions
): Promise<SambaShare[] | APIResponse> {
	return await apiRequestResult(
		'/samba/shares',
		z.array(SambaShareSchema),
		'GET',
		undefined,
		options
	);
}

export async function getShareDatasets(
	options: SambaRequestOptions
): Promise<ShareDataset[] | APIResponse> {
	return await apiRequestResult(
		'/samba/datasets',
		ShareDatasetSchema.array(),
		'GET',
		undefined,
		options
	);
}

export async function getSambaPrincipals(
	options: SambaRequestOptions
): Promise<SambaPrincipals | APIResponse> {
	return await apiRequestResult(
		'/samba/principals',
		SambaPrincipalsSchema,
		'GET',
		undefined,
		options
	);
}

export async function getSambaStatus(
	options: SambaRequestOptions
): Promise<SambaStatus | APIResponse> {
	return await apiRequestResult('/samba/status', SambaStatusSchema, 'GET', undefined, options);
}

export async function saveSambaShare(
	input: SambaShareInput,
	id: number | undefined,
	options: SambaRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		id === undefined ? '/samba/shares' : `/samba/shares/${id}`,
		APIResponseSchema,
		id === undefined ? 'POST' : 'PUT',
		input,
		options
	);
}

export async function setSambaShareEnabled(
	id: number,
	enabled: boolean,
	options: SambaRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/samba/shares/${id}/enabled`,
		APIResponseSchema,
		'PUT',
		{ enabled },
		options
	);
}

export async function cancelSharePermissionJob(
	id: number,
	options: SambaRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/samba/shares/${id}/permissions-job`,
		APIResponseSchema,
		'DELETE',
		undefined,
		options
	);
}

export async function getSambaShares(options?: NodeAPIDataRequestOptions): Promise<SambaShare[]> {
	return await apiRequestData(
		'/samba/shares',
		z.array(SambaShareSchema),
		'GET',
		undefined,
		options
	);
}

export async function createSambaShare(
	name: string,
	dataset: string,
	enabled: boolean,
	permissions: {
		read: { userIds: number[]; groupIds: number[]; directorySids?: string[] };
		write: { userIds: number[]; groupIds: number[]; directorySids?: string[] };
	},
	guest: {
		enabled: boolean;
		writeable: boolean;
	},
	createMask: string = '',
	directoryMask: string = '',
	timeMachine: boolean = false,
	timeMachineMaxSize: number = 0,
	auditEnabled: boolean = false,
	auditRetentionDays: number = 70,
	auditedOperations: string[] = [],
	extraShareConfig: string = '',
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		'/samba/shares',
		APIResponseSchema,
		'POST',
		{
			name,
			dataset,
			enabled,
			permissions,
			guest,
			createMask,
			directoryMask,
			timeMachine,
			timeMachineMaxSize,
			auditEnabled,
			auditRetentionDays,
			auditedOperations,
			extraShareConfig
		},
		options
	);
}

export async function updateSambaShare(
	id: number,
	name: string,
	dataset: string,
	enabled: boolean,
	permissions: {
		read: { userIds: number[]; groupIds: number[]; directorySids?: string[] };
		write: { userIds: number[]; groupIds: number[]; directorySids?: string[] };
	},
	guest: {
		enabled: boolean;
		writeable: boolean;
	},
	createMask: string = '',
	directoryMask: string = '',
	timeMachine: boolean = false,
	timeMachineMaxSize: number = 0,
	auditEnabled: boolean = false,
	auditRetentionDays: number = 70,
	auditedOperations: string[] = [],
	extraShareConfig?: string,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/samba/shares/${id}`,
		APIResponseSchema,
		'PUT',
		{
			name,
			dataset,
			enabled,
			permissions,
			guest,
			createMask,
			directoryMask,
			timeMachine,
			timeMachineMaxSize,
			auditEnabled,
			auditRetentionDays,
			auditedOperations,
			extraShareConfig
		},
		options
	);
}

export async function deleteSambaShare(
	id: number,
	options?: NodeAPIRequestOptions
): Promise<APIResponse> {
	return await apiRequestResult(
		`/samba/shares/${id}`,
		APIResponseSchema,
		'DELETE',
		undefined,
		options
	);
}
