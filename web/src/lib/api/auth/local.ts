import {
	ImportableUnixUserSchema,
	NextUIDResultSchema,
	UserCapabilitiesSchema,
	UserMutationResultSchema,
	UserSchema,
	type ImportableUnixUser,
	type NextUIDResult,
	type SambaAction,
	type User,
	type UserCapabilities,
	type UserMutationResult
} from '$lib/types/auth';
import type { APIResponse } from '$lib/types/common';
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function listUsers(
	source?: User['source'],
	options: NodeAPIRequestOptions = {}
): Promise<User[] | APIResponse> {
	const query = source ? `?source=${encodeURIComponent(source)}` : '';
	return await apiRequest(`/auth/users${query}`, z.array(UserSchema), 'GET', undefined, {
		...options,
		preserveErrors: true
	});
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
	createSamba?: boolean;
	sambaAction?: SambaAction;
}

export async function createUser(
	payload: UserPayload,
	options: NodeAPIRequestOptions = {}
): Promise<UserMutationResult | APIResponse> {
	return await apiRequest('/auth/users', UserMutationResultSchema, 'POST', payload, {
		...options,
		preserveErrors: true
	});
}

export async function deleteUser(
	id: number,
	options: NodeAPIRequestOptions = {}
): Promise<UserMutationResult | APIResponse> {
	return await apiRequest(
		`/auth/users/${encodeURIComponent(String(id))}`,
		UserMutationResultSchema,
		'DELETE',
		undefined,
		{ ...options, preserveErrors: true }
	);
}

export async function editUser(
	id: number,
	payload: UserPayload,
	options: NodeAPIRequestOptions = {}
): Promise<UserMutationResult | APIResponse> {
	return await apiRequest(
		`/auth/users/${encodeURIComponent(String(id))}`,
		UserMutationResultSchema,
		'PUT',
		payload,
		{ ...options, preserveErrors: true }
	);
}

export async function getNextUID(
	options: NodeAPIRequestOptions = {}
): Promise<NextUIDResult | APIResponse> {
	return await apiRequest('/auth/users/uid/next', NextUIDResultSchema, 'GET', undefined, {
		...options,
		preserveErrors: true
	});
}

export async function getUserCapabilities(
	options: NodeAPIRequestOptions = {}
): Promise<UserCapabilities | APIResponse> {
	return await apiRequest('/auth/users/capabilities', UserCapabilitiesSchema, 'GET', undefined, {
		...options,
		preserveErrors: true
	});
}

export interface ImportUserPayload {
	username: string;
	password?: string;
	admin: boolean;
}

export async function importUser(
	payload: ImportUserPayload,
	options: NodeAPIRequestOptions = {}
): Promise<UserMutationResult | APIResponse> {
	return await apiRequest('/auth/users/import', UserMutationResultSchema, 'POST', payload, {
		...options,
		preserveErrors: true
	});
}

export async function createPamUser(
	payload: UserPayload,
	options: NodeAPIRequestOptions = {}
): Promise<UserMutationResult | APIResponse> {
	return await apiRequest('/auth/users/pam', UserMutationResultSchema, 'POST', payload, {
		...options,
		preserveErrors: true
	});
}

export async function listImportableUsers(
	options: NodeAPIRequestOptions = {}
): Promise<ImportableUnixUser[] | APIResponse> {
	return await apiRequest(
		'/auth/users/importable',
		z.array(ImportableUnixUserSchema),
		'GET',
		undefined,
		{
			...options,
			preserveErrors: true
		}
	);
}
