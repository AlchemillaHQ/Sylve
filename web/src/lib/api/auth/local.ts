import { UserSchema, type User } from '$lib/types/auth';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function listUsers(): Promise<User[]> {
    return await apiRequest('/auth/users', z.array(UserSchema), 'GET');
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
