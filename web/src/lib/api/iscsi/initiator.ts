import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { ISCSIInitiatorSchema, ISCSIStatusSchema, type ISCSIInitiator, type ISCSIStatus } from '$lib/types/iscsi/initiator';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function getInitiators(): Promise<ISCSIInitiator[]> {
    return await apiRequest('/iscsi/initiators', z.array(ISCSIInitiatorSchema), 'GET');
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
    return await apiRequest('/iscsi/initiators', APIResponseSchema, 'POST', {
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
    return await apiRequest('/iscsi/initiators', APIResponseSchema, 'PUT', {
        id,
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
    return await apiRequest(`/iscsi/initiators/${id}`, APIResponseSchema, 'DELETE');
}

export async function getISCSIStatus(): Promise<ISCSIStatus> {
    return await apiRequest('/iscsi/status', ISCSIStatusSchema, 'GET');
}
