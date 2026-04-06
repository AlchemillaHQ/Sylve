import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { ISCSITargetSchema, type ISCSITarget } from '$lib/types/iscsi/target';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function getTargets(): Promise<ISCSITarget[]> {
    return await apiRequest('/iscsi/targets', z.array(ISCSITargetSchema), 'GET');
}

export async function createTarget(
    targetName: string,
    alias: string = '',
    authMethod: string = 'None',
    chapName: string = '',
    chapSecret: string = '',
    mutualChapName: string = '',
    mutualChapSecret: string = ''
): Promise<APIResponse> {
    return await apiRequest('/iscsi/targets', APIResponseSchema, 'POST', {
        targetName,
        alias,
        authMethod,
        chapName,
        chapSecret,
        mutualChapName,
        mutualChapSecret
    });
}

export async function updateTarget(
    id: number,
    targetName: string,
    alias: string = '',
    authMethod: string = 'None',
    chapName: string = '',
    chapSecret: string = '',
    mutualChapName: string = '',
    mutualChapSecret: string = ''
): Promise<APIResponse> {
    return await apiRequest('/iscsi/targets', APIResponseSchema, 'PUT', {
        id,
        targetName,
        alias,
        authMethod,
        chapName,
        chapSecret,
        mutualChapName,
        mutualChapSecret
    });
}

export async function deleteTarget(id: number): Promise<APIResponse> {
    return await apiRequest(`/iscsi/targets/${id}`, APIResponseSchema, 'DELETE');
}

export async function addPortal(
    targetId: number,
    address: string,
    port: number = 3260
): Promise<APIResponse> {
    return await apiRequest(`/iscsi/targets/${targetId}/portals`, APIResponseSchema, 'POST', {
        address,
        port
    });
}

export async function removePortal(portalId: number): Promise<APIResponse> {
    return await apiRequest(`/iscsi/targets/portals/${portalId}`, APIResponseSchema, 'DELETE');
}

export async function addLUN(
    targetId: number,
    lunNumber: number,
    zvol: string
): Promise<APIResponse> {
    return await apiRequest(`/iscsi/targets/${targetId}/luns`, APIResponseSchema, 'POST', {
        lunNumber,
        zvol
    });
}

export async function removeLUN(lunId: number): Promise<APIResponse> {
    return await apiRequest(`/iscsi/targets/luns/${lunId}`, APIResponseSchema, 'DELETE');
}
