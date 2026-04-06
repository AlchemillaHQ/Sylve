import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { NetworkObjectSchema, type NetworkObject } from '$lib/types/network/object';
import { apiRequest } from '$lib/utils/http';
import z from 'zod/v4';

export async function getNetworkObjects(): Promise<NetworkObject[] | APIResponse> {
    return await apiRequest('/network/object', NetworkObjectSchema.array(), 'GET');
}

export async function createNetworkObject(
    name: string,
    type: string,
    values: string[]
): Promise<number | APIResponse> {
    const body = { name, type, values };
    return await apiRequest('/network/object', z.number(), 'POST', body);
}

export async function updateNetworkObject(
    id: number,
    name: string,
    type: string,
    values: string[]
): Promise<APIResponse> {
    const body = {
        name,
        type,
        values
    };

    return await apiRequest(`/network/object/${id}`, APIResponseSchema, 'PUT', body);
}

export async function deleteNetworkObject(id: number): Promise<APIResponse> {
    return await apiRequest(`/network/object/${id}`, APIResponseSchema, 'DELETE');
}

export async function bulkDeleteNetworkObjects(ids: number[]): Promise<APIResponse> {
    return await apiRequest('/network/object/bulk-delete', APIResponseSchema, 'POST', { ids });
}
