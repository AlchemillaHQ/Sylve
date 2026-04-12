import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { SambaShareSchema, type SambaShare } from '$lib/types/samba/shares';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function getSambaShares(): Promise<SambaShare[]> {
    return await apiRequest('/samba/shares', z.array(SambaShareSchema), 'GET');
}

export async function createSambaShare(
    name: string,
    dataset: string,
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
    timeMachineMaxSize: number = 0
): Promise<APIResponse> {
    return await apiRequest('/samba/shares', APIResponseSchema, 'POST', {
        name,
        dataset,
        permissions,
        guest,
        createMask,
        directoryMask,
        timeMachine,
        timeMachineMaxSize
    });
}

export async function updateSambaShare(
    id: number,
    name: string,
    dataset: string,
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
    timeMachineMaxSize: number = 0
): Promise<APIResponse> {
    return await apiRequest(`/samba/shares`, APIResponseSchema, 'PUT', {
        id,
        name,
        dataset,
        permissions,
        guest,
        createMask,
        directoryMask,
        timeMachine,
        timeMachineMaxSize
    });
}

export async function deleteSambaShare(id: number): Promise<APIResponse> {
    return await apiRequest(`/samba/shares/${id}`, APIResponseSchema, 'DELETE');
}
