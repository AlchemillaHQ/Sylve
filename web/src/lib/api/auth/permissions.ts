import {
	ACLGrantSchema,
	ACLGroupSchema,
	AuthRealmSchema,
	PermissionsSchema,
	RealmDiscoverySchema,
	RoleSchema,
	type ACLGrant,
	type ACLGroup,
	type AuthRealm,
	type Permissions,
	type RealmDiscovery,
	type Role
} from '$lib/types/auth';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function listGrants(): Promise<ACLGrant[] | APIResponse> {
	return await apiRequest('/auth/grants', z.array(ACLGrantSchema), 'GET', undefined, {
		preserveErrors: true
	});
}

export async function setGrant(grant: ACLGrant): Promise<APIResponse> {
	return await apiRequest('/auth/grants', APIResponseSchema, 'POST', grant);
}

export async function deleteGrant(grant: ACLGrant): Promise<APIResponse> {
	return await apiRequest('/auth/grants', APIResponseSchema, 'DELETE', grant);
}

export async function listRoles(): Promise<Role[] | APIResponse> {
	return await apiRequest('/auth/roles', z.array(RoleSchema), 'GET', undefined, {
		preserveErrors: true
	});
}

export async function setRole(name: string, privileges: string[]): Promise<APIResponse> {
	return await apiRequest(`/auth/roles/${encodeURIComponent(name)}`, APIResponseSchema, 'PUT', {
		privileges
	});
}

export async function deleteRole(name: string): Promise<APIResponse> {
	return await apiRequest(`/auth/roles/${encodeURIComponent(name)}`, APIResponseSchema, 'DELETE');
}

export async function listACLGroups(): Promise<ACLGroup[] | APIResponse> {
	return await apiRequest('/auth/acl-groups', z.array(ACLGroupSchema), 'GET', undefined, {
		preserveErrors: true
	});
}

export async function setACLGroup(name: string, members: string[]): Promise<APIResponse> {
	return await apiRequest(
		`/auth/acl-groups/${encodeURIComponent(name)}`,
		APIResponseSchema,
		'PUT',
		{
			members
		}
	);
}

export async function deleteACLGroup(name: string): Promise<APIResponse> {
	return await apiRequest(
		`/auth/acl-groups/${encodeURIComponent(name)}`,
		APIResponseSchema,
		'DELETE'
	);
}

export async function listRealms(): Promise<AuthRealm[] | APIResponse> {
	return await apiRequest('/auth/realms', z.array(AuthRealmSchema), 'GET', undefined, {
		preserveErrors: true
	});
}

// clientSecret: undefined keeps the stored secret, '' clears it.
export async function setRealm(realm: AuthRealm, clientSecret?: string): Promise<APIResponse> {
	return await apiRequest(
		`/auth/realms/${encodeURIComponent(realm.name)}`,
		APIResponseSchema,
		'PUT',
		{
			...realm,
			clientSecret
		}
	);
}

export async function deleteRealm(name: string): Promise<APIResponse> {
	return await apiRequest(`/auth/realms/${encodeURIComponent(name)}`, APIResponseSchema, 'DELETE');
}

export async function discoverRealm(
	issuer: string,
	caCert: string
): Promise<RealmDiscovery | APIResponse> {
	return await apiRequest(
		'/auth/realms/discover',
		RealmDiscoverySchema,
		'POST',
		{ issuer, caCert },
		{ preserveErrors: true }
	);
}

export async function createRealmUser(
	realm: string,
	user: { username: string; fullName: string; email: string; notes: string }
): Promise<APIResponse> {
	return await apiRequest(
		`/auth/realms/${encodeURIComponent(realm)}/users`,
		APIResponseSchema,
		'POST',
		user
	);
}

export async function getPermissions(): Promise<Permissions | APIResponse> {
	return await apiRequest('/auth/permissions', PermissionsSchema, 'GET', undefined, {
		preserveErrors: true
	});
}
