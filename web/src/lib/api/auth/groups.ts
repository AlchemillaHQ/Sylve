import {
	GroupMutationResultSchema,
	GroupSchema,
	type Group,
	type GroupMutationResult
} from '$lib/types/auth';
import type { APIResponse } from '$lib/types/common';
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function listGroups(
	options: NodeAPIRequestOptions = {}
): Promise<Group[] | APIResponse> {
	return await apiRequest('/auth/groups', z.array(GroupSchema), 'GET', undefined, {
		...options,
		preserveErrors: true
	});
}

export async function createGroup(
	name: string,
	members: string[],
	options: NodeAPIRequestOptions = {}
): Promise<GroupMutationResult | APIResponse> {
	return await apiRequest(
		'/auth/groups',
		GroupMutationResultSchema,
		'POST',
		{ name, members },
		{ ...options, preserveErrors: true }
	);
}

export async function deleteGroup(
	id: number,
	options: NodeAPIRequestOptions = {}
): Promise<GroupMutationResult | APIResponse> {
	return await apiRequest(
		`/auth/groups/${encodeURIComponent(String(id))}`,
		GroupMutationResultSchema,
		'DELETE',
		undefined,
		{ ...options, preserveErrors: true }
	);
}

export async function updateGroupMembers(
	id: number,
	usernames: string[],
	options: NodeAPIRequestOptions = {}
): Promise<GroupMutationResult | APIResponse> {
	return await apiRequest(
		`/auth/groups/${encodeURIComponent(String(id))}/members`,
		GroupMutationResultSchema,
		'PUT',
		{ usernames },
		{ ...options, preserveErrors: true }
	);
}
