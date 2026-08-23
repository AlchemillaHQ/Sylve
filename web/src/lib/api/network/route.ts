import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	StaticRouteSchema,
	StaticRouteSuggestionSchema,
	type StaticRoute,
	type StaticRouteSuggestion,
	type StaticRouteUpsertRequest
} from '$lib/types/network/route';
import { apiRequest } from '$lib/utils/http';
import z from 'zod/v4';

export async function getStaticRoutes(): Promise<StaticRoute[] | APIResponse> {
	return await apiRequest('/network/route', StaticRouteSchema.array(), 'GET', undefined, {
		preserveErrors: true
	});
}

export async function createStaticRoute(
	payload: StaticRouteUpsertRequest
): Promise<number | APIResponse> {
	return await apiRequest('/network/route', z.number().int().positive(), 'POST', payload);
}

export async function updateStaticRoute(
	id: number,
	payload: StaticRouteUpsertRequest
): Promise<APIResponse> {
	return await apiRequest(`/network/route/${id}`, APIResponseSchema, 'PUT', payload);
}

export async function deleteStaticRoute(id: number): Promise<APIResponse> {
	return await apiRequest(`/network/route/${id}`, APIResponseSchema, 'DELETE');
}

export async function suggestStaticRoutesFromNATRule(
	id: number
): Promise<StaticRouteSuggestion[] | APIResponse> {
	return await apiRequest(
		`/network/firewall/nat/${id}/route-suggestions`,
		StaticRouteSuggestionSchema.array(),
		'GET',
		undefined,
		{ preserveErrors: true }
	);
}
