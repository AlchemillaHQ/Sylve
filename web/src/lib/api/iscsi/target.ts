import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { ISCSITargetSchema, type ISCSITarget } from '$lib/types/iscsi/target';
import { apiRequestData, apiRequestResult } from '$lib/utils/http';
import { z } from 'zod/v4';

export const TargetSessionsSchema = z.record(z.string(), z.number());
export type TargetSessions = z.infer<typeof TargetSessionsSchema>;

export async function getTargetSessions(): Promise<TargetSessions> {
	return await apiRequestData('/iscsi/target-sessions', TargetSessionsSchema, 'GET');
}

export async function getTargets(): Promise<ISCSITarget[]> {
	return await apiRequestData('/iscsi/targets', z.array(ISCSITargetSchema), 'GET');
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
	return await apiRequestResult('/iscsi/targets', APIResponseSchema, 'POST', {
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
	return await apiRequestResult(`/iscsi/targets/${id}`, APIResponseSchema, 'PUT', {
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
	return await apiRequestResult(`/iscsi/targets/${id}`, APIResponseSchema, 'DELETE');
}

export async function addPortal(
	targetId: number,
	address: string,
	port: number = 3260
): Promise<APIResponse> {
	return await apiRequestResult(`/iscsi/targets/${targetId}/portals`, APIResponseSchema, 'POST', {
		address,
		port
	});
}

export async function removePortal(targetId: number, portalId: number): Promise<APIResponse> {
	return await apiRequestResult(
		`/iscsi/targets/${targetId}/portals/${portalId}`,
		APIResponseSchema,
		'DELETE'
	);
}

export async function addLUN(
	targetId: number,
	lunNumber: number,
	zvol: string
): Promise<APIResponse> {
	return await apiRequestResult(`/iscsi/targets/${targetId}/luns`, APIResponseSchema, 'POST', {
		lunNumber,
		zvol
	});
}

export async function removeLUN(targetId: number, lunId: number): Promise<APIResponse> {
	return await apiRequestResult(
		`/iscsi/targets/${targetId}/luns/${lunId}`,
		APIResponseSchema,
		'DELETE'
	);
}
