import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { SambaShareSchema, type SambaShare } from '$lib/types/samba/shares';
import { apiRequestData, apiRequestResult } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function getSambaShares(): Promise<SambaShare[]> {
	return await apiRequestData('/samba/shares', z.array(SambaShareSchema), 'GET');
}

export async function createSambaShare(
	name: string,
	dataset: string,
	enabled: boolean,
	permissions: {
		read: { userIds: number[]; groupIds: number[] };
		write: { userIds: number[]; groupIds: number[] };
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
	auditedOperations: string[] = []
): Promise<APIResponse> {
	return await apiRequestResult('/samba/shares', APIResponseSchema, 'POST', {
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
		auditedOperations
	});
}

export async function updateSambaShare(
	id: number,
	name: string,
	dataset: string,
	enabled: boolean,
	permissions: {
		read: { userIds: number[]; groupIds: number[] };
		write: { userIds: number[]; groupIds: number[] };
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
	auditedOperations: string[] = []
): Promise<APIResponse> {
	return await apiRequestResult(`/samba/shares/${id}`, APIResponseSchema, 'PUT', {
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
		auditedOperations
	});
}

export async function setSambaShareEnabled(id: number, enabled: boolean): Promise<APIResponse> {
	return await apiRequestResult(`/samba/shares/${id}/enabled`, APIResponseSchema, 'PUT', {
		enabled
	});
}

export async function deleteSambaShare(id: number): Promise<APIResponse> {
	return await apiRequestResult(`/samba/shares/${id}`, APIResponseSchema, 'DELETE');
}
