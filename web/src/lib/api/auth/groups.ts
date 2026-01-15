import { GroupSchema, UserSchema, type Group, type User } from '$lib/types/auth';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function listGroups(): Promise<Group[]> {
    return await apiRequest('/auth/groups', z.array(GroupSchema), 'GET');
}

export async function createGroup(name: string, members: string[]): Promise<APIResponse> {
    const requestBody = {
        name,
        members
    };

    return await apiRequest('/auth/groups', APIResponseSchema, 'POST', requestBody);
}

export async function deleteGroup(id: number): Promise<APIResponse> {
    return await apiRequest(`/auth/groups/${id}`, APIResponseSchema, 'DELETE');
}

export async function addUsersToGroup(usernames: string[], group: string): Promise<APIResponse> {
    const requestBody = {
        usernames: usernames,
        group
    };

    return await apiRequest('/auth/groups/users', APIResponseSchema, 'POST', requestBody);
}

export async function updateGroupMembers(usernames: string[], group: string): Promise<APIResponse> {
    const requestBody = {
        usernames: usernames,
        group
    };
    return await apiRequest('/auth/groups/users', APIResponseSchema, 'PUT', requestBody);
}