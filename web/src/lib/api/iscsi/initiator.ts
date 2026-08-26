import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	ISCSIInitiatorSchema,
	ISCSIStatusSchema,
	type ISCSIInitiator,
	type ISCSIStatus
} from '$lib/types/iscsi/initiator';
import { apiRequestData, apiRequestResult } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function getInitiators(): Promise<ISCSIInitiator[]> {
	return await apiRequestData('/iscsi/initiators', z.array(ISCSIInitiatorSchema), 'GET');
}

export async function createInitiator(
	nickname: string,
	targetAddress: string,
	targetName: string,
	initiatorName: string = '',
	authMethod: string = 'None',
	chapName: string = '',
	chapSecret: string = '',
	tgtChapName: string = '',
	tgtChapSecret: string = ''
): Promise<APIResponse> {
	return await apiRequestResult('/iscsi/initiators', APIResponseSchema, 'POST', {
		nickname,
		targetAddress,
		targetName,
		initiatorName,
		authMethod,
		chapName,
		chapSecret,
		tgtChapName,
		tgtChapSecret
	});
}

export async function updateInitiator(
	id: number,
	nickname: string,
	targetAddress: string,
	targetName: string,
	initiatorName: string = '',
	authMethod: string = 'None',
	chapName: string = '',
	chapSecret: string = '',
	tgtChapName: string = '',
	tgtChapSecret: string = ''
): Promise<APIResponse> {
	return await apiRequestResult(`/iscsi/initiators/${id}`, APIResponseSchema, 'PUT', {
		nickname,
		targetAddress,
		targetName,
		initiatorName,
		authMethod,
		chapName,
		chapSecret,
		tgtChapName,
		tgtChapSecret
	});
}

export async function deleteInitiator(id: number): Promise<APIResponse> {
	return await apiRequestResult(`/iscsi/initiators/${id}`, APIResponseSchema, 'DELETE');
}

export async function getISCSIStatus(): Promise<ISCSIStatus> {
	return await apiRequestData('/iscsi/status', ISCSIStatusSchema, 'GET');
}

export async function connectInitiator(id: number): Promise<APIResponse> {
	return await apiRequestResult(`/iscsi/initiators/${id}/connect`, APIResponseSchema, 'POST');
}
