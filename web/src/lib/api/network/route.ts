import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	StaticRouteSchema,
	StaticRouteSuggestionSchema,
	type StaticRoute,
	type StaticRouteSuggestion
} from '$lib/types/network/route';
import { apiRequest } from '$lib/utils/http';
import z from 'zod/v4';

export async function getStaticRoutes(): Promise<StaticRoute[] | APIResponse> {
	return await apiRequest('/network/route', StaticRouteSchema.array(), 'GET');
}

export async function createStaticRoute(payload: Partial<StaticRoute>): Promise<number | APIResponse> {
	return await apiRequest('/network/route', z.number(), 'POST', payload);
}

export async function updateStaticRoute(id: number, payload: Partial<StaticRoute>): Promise<APIResponse> {
	return await apiRequest(`/network/route/${id}`, APIResponseSchema, 'PUT', payload);
}

export async function deleteStaticRoute(id: number): Promise<APIResponse> {
	return await apiRequest(`/network/route/${id}`, APIResponseSchema, 'DELETE');
}

export async function suggestStaticRoutesFromNATRule(
	id: number
): Promise<StaticRouteSuggestion[] | APIResponse> {
	return await apiRequest(
		`/network/route/suggest-from-nat/${id}`,
		StaticRouteSuggestionSchema.array(),
		'POST'
	);
}
