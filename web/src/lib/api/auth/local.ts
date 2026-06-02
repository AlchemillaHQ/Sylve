import { UserSchema, type User } from '$lib/types/auth';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function listUsers(source?: string): Promise<User[]> {
    const query = source ? `?source=${source}` : '';
    return await apiRequest(`/auth/users${query}`, z.array(UserSchema), 'GET');
}

export interface UserPayload {
    username: string;
    fullName?: string;
    email?: string;
    password?: string;
    admin: boolean;
    uid?: number;
    shell?: string;
    homeDirectory?: string;
    homeDirPerms?: number;
    sshPublicKey?: string;
    disablePassword?: boolean;
    locked?: boolean;
    doasEnabled?: boolean;
    newPrimaryGroup?: boolean;
    primaryGroupId?: number;
    auxGroupIds?: number[];
}

export async function createUser(payload: UserPayload): Promise<APIResponse> {
    return await apiRequest('/auth/users', APIResponseSchema, 'POST', payload);
}

export async function deleteUser(id: string): Promise<APIResponse> {
    return await apiRequest(`/auth/users/${id}`, APIResponseSchema, 'DELETE');
}

export async function editUser(id: number, payload: UserPayload): Promise<APIResponse> {
    return await apiRequest('/auth/users', APIResponseSchema, 'PUT', { id, ...payload });
}

export async function getNextUID(): Promise<APIResponse<{ nextUID: number }>> {
    return await apiRequest(
        '/auth/users/uid/next',
        APIResponseSchema.extend({ data: z.object({ nextUID: z.number() }).nullable() }),
        'GET'
    );
}

export async function getUserCapabilities(): Promise<
    APIResponse<{ doasAvailable: boolean }>
> {
    return await apiRequest(
        '/auth/users/capabilities',
        APIResponseSchema.extend({
            data: z.object({ doasAvailable: z.boolean() }).nullable()
        }),
        'GET'
    );
}

export interface ImportUserPayload {
    username: string;
    password?: string;
    admin: boolean;
}

export async function importUser(payload: ImportUserPayload): Promise<APIResponse<User>> {
    return await apiRequest(
        '/auth/users/import',
        APIResponseSchema.extend({ data: UserSchema.nullable() }),
        'POST',
        payload
    );
}

export async function listImportableUsers(): Promise<User[]> {
    return await apiRequest('/auth/users/importable', z.array(UserSchema), 'GET');
}
