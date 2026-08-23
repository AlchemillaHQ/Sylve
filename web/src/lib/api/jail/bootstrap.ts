import type { APIResponse } from '$lib/types/common';
import {
	BootstrapCreateResponseSchema,
	BootstrapDeleteResponseSchema,
	BootstrapEntrySchema,
	type BootstrapCreateResponse,
	type BootstrapDeleteResponse,
	type BootstrapEntry,
	type BootstrapRequest
} from '$lib/types/jail/bootstrap';
import { apiRequest, type NodeAPIRequestOptions } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function getBootstraps(
	pool: string,
	options: NodeAPIRequestOptions = {}
): Promise<BootstrapEntry[] | APIResponse> {
	return await apiRequest(
		`/jail/bootstraps?pool=${encodeURIComponent(pool)}`,
		z.array(BootstrapEntrySchema),
		'GET',
		undefined,
		{ ...options, preserveErrors: true }
	);
}

export async function createBootstrap(
	req: BootstrapRequest,
	options: NodeAPIRequestOptions = {}
): Promise<BootstrapCreateResponse | APIResponse> {
	return await apiRequest('/jail/bootstraps', BootstrapCreateResponseSchema, 'POST', req, options);
}

export async function deleteBootstrap(
	pool: string,
	name: string,
	options: NodeAPIRequestOptions = {}
): Promise<BootstrapDeleteResponse | APIResponse> {
	return await apiRequest(
		`/jail/bootstraps/${encodeURIComponent(name)}?pool=${encodeURIComponent(pool)}`,
		BootstrapDeleteResponseSchema,
		'DELETE',
		undefined,
		options
	);
}
