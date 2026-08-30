/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { browser } from '$app/environment';
import { storage } from '$lib';
import { api } from '$lib/api/common';
import { isDemoMode } from '$lib/demo/runtime';
import { reload } from '$lib/stores/api.svelte';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { z } from 'zod/v4';
import { kvStorage } from '$lib/types/db';
import {
	type ErrorRequestContext,
	registerErrorContext,
	reportAPIError,
	stageErrorDetail
} from '$lib/stores/error-details.svelte';

export type APIRequestOptions = {
	raw?: boolean;
	hostname?: string;
	headers?: Record<string, string>;
	skipAuditLog?: boolean;
	preserveErrors?: boolean;
	signal?: AbortSignal;
};

export type NodeAPIRequestOptions = Pick<
	APIRequestOptions,
	'hostname' | 'signal' | 'preserveErrors' | 'skipAuditLog'
>;

export type APIDataRequestOptions = Omit<APIRequestOptions, 'raw' | 'preserveErrors'>;

export type NodeAPIDataRequestOptions = Pick<APIDataRequestOptions, 'hostname' | 'signal'>;

export class APIRequestError extends Error {
	readonly response: APIResponse;

	constructor(response: APIResponse) {
		super(getAPIErrorText(response));
		this.name = 'APIRequestError';
		this.response = response;
	}
}

export function isRequestCancellation(error: unknown): boolean {
	if (!(error instanceof Error)) return false;
	const code = 'code' in error ? String(error.code || '') : '';
	return error.name === 'AbortError' || error.name === 'CanceledError' || code === 'ERR_CANCELED';
}

function getScopedCacheKey(key: string, hostname?: string): string {
	if (!browser) {
		return hostname ? `node:${hostname}:${key}` : key;
	}

	if (hostname) return `node:${hostname}:${key}`;

	const routeHost = window.location.pathname.split('/').filter(Boolean)[0] || '';
	if (
		routeHost &&
		routeHost !== 'datacenter' &&
		routeHost !== 'login' &&
		routeHost !== 'inactive-node'
	) {
		return `node:${routeHost}:${key}`;
	}

	return key;
}

export async function apiRequest<T extends z.ZodType>(
	endpoint: string,
	schema: T,
	method: 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH',
	body?: unknown,
	options?: APIRequestOptions
): Promise<z.infer<T> | APIResponse> {
	const auditHostname = options?.hostname || null;

	function setReloadFlag() {
		if (method !== 'GET' && !options?.skipAuditLog) {
			reload.auditLogHostname = auditHostname;
			reload.auditLog = true;
		}
	}

	try {
		const config = {
			method,
			url: endpoint,
			headers: {
				...(options?.headers || {}),
				...(options?.hostname ? { 'X-Current-Hostname': options.hostname } : {})
			},
			...(body ? { data: body } : {}),
			...(options?.signal ? { signal: options.signal } : {})
		};

		const response = await api.request({ ...config, validateStatus: () => true });
		const apiResponse = APIResponseSchema.safeParse(response.data);
		const errorContext = {
			method,
			path: endpoint,
			httpStatus: response.status,
			node: options?.hostname || storage.hostname || undefined
		};

		if (apiResponse.data) {
			if (apiResponse.data.status && apiResponse.data.status === 'error') {
				registerErrorContext(apiResponse.data, errorContext);
				stageErrorDetail(apiResponse.data, errorContext);
				setReloadFlag();
				if (options?.raw) return apiResponse.data as z.infer<T>;
				return getDefaultValue(schema, apiResponse.data, options?.preserveErrors);
			}
		}

		/* Couldn't parse response data into APIResponse so we'll just return the data? */
		if (!apiResponse.success) {
			setReloadFlag();
			const invalidResponse: APIResponse = {
				status: 'error',
				message: 'Invalid response format',
				error: 'The server response did not match the expected API format.',
				data: response.data
			};
			registerErrorContext(invalidResponse, errorContext);
			stageErrorDetail(invalidResponse, errorContext);
			return getDefaultValue(schema, invalidResponse, options?.preserveErrors);
		}

		/* Caller asked for a raw response */
		if (options?.raw) {
			setReloadFlag();
			return apiResponse.data as z.infer<T>;
		}

		const parsedResult = schema.safeParse(apiResponse.data.data);
		if (parsedResult.success) {
			setReloadFlag();
			return parsedResult.data;
		}

		// Response schemas describe the complete API envelope rather than its
		// data field. Preserve that existing use case before reporting a data
		// contract failure.
		const parsedEnvelope = schema.safeParse(apiResponse.data);
		if (parsedEnvelope.success) {
			setReloadFlag();
			return parsedEnvelope.data;
		}

		console.warn('Zod Validation Error', parsedResult.error, apiResponse.data);
		setReloadFlag();
		const invalidResponse: APIResponse = {
			status: 'error',
			message: 'Invalid response data',
			error: 'The server response data did not match the expected format.',
			data: apiResponse.data.data
		};
		registerErrorContext(invalidResponse, errorContext);
		stageErrorDetail(invalidResponse, errorContext);
		return getDefaultValue(schema, invalidResponse, options?.preserveErrors);
	} catch (error) {
		if (isRequestCancellation(error)) throw error;
		setReloadFlag();
		console.error('API Request Error', error);
		const failedResponse: APIResponse = {
			status: 'error',
			message: 'Request failed',
			error: error instanceof Error ? error.message : 'Unknown error'
		};
		const errorContext = {
			method,
			path: endpoint,
			node: options?.hostname || storage.hostname || undefined
		};
		registerErrorContext(failedResponse, errorContext);
		stageErrorDetail(failedResponse, errorContext);
		return getDefaultValue(schema, failedResponse, options?.preserveErrors);
	}
}

export async function apiRequestResult<T extends z.ZodType>(
	endpoint: string,
	schema: T,
	method: 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH',
	body?: unknown,
	options?: APIDataRequestOptions
): Promise<z.infer<T> | APIResponse> {
	return await apiRequest(endpoint, schema, method, body, {
		...options,
		preserveErrors: true
	});
}

export async function apiRequestData<T extends z.ZodType>(
	endpoint: string,
	schema: T,
	method: 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH',
	body?: unknown,
	options?: APIDataRequestOptions
): Promise<z.infer<T>> {
	const result = await apiRequestResult(endpoint, schema, method, body, options);

	if (isAPIErrorResponse(result)) {
		throw new APIRequestError(result);
	}

	// apiRequestResult only returns validated schema data or a staged error response.
	return result as z.infer<T>;
}

function getDefaultValue<T extends z.ZodType>(
	schema: T,
	response: APIResponse,
	preserveErrors = false
): z.infer<T> | APIResponse {
	if (!preserveErrors && schema instanceof z.ZodArray) {
		return [] as z.infer<T>;
	}

	return response;
}

export async function cachedFetch<T>(
	key: string,
	fetchFunction: () => Promise<T>,
	duration: number,
	onlyCache?: boolean,
	hostname?: string
): Promise<T> {
	if (isDemoMode) {
		return await fetchFunction();
	}

	const scopedKey = getScopedCacheKey(key, hostname);
	const now = Date.now();
	const entry = await kvStorage.getItem<T>(scopedKey);

	if (entry && entry.data !== null) {
		const isFresh = now - entry.timestamp < duration;
		const data = entry.data;

		const looksLikeError = isAPIErrorResponse(data);

		if (isFresh && !looksLikeError) {
			return data;
		}
	}

	if (onlyCache) {
		return null as T;
	}

	const data = await fetchFunction();

	if (!isAPIErrorResponse(data)) {
		await kvStorage.setItem(scopedKey, data);
	}

	return data;
}

export async function getCache<T>(key: string, hostname?: string): Promise<T | null> {
	const scopedKey = getScopedCacheKey(key, hostname);
	try {
		const entry = await kvStorage.getItem<T>(scopedKey);
		return entry?.data ?? null;
	} catch (error) {
		console.error(`Failed to read cached data for key "${scopedKey}"`, error);
		return null;
	}
}

export async function updateCache<T>(key: string, obj: T, hostname?: string): Promise<void> {
	const scopedKey = getScopedCacheKey(key, hostname);
	try {
		await kvStorage.setItem(scopedKey, obj);
	} catch (error) {
		console.error(`Failed to update cached data for key "${scopedKey}"`, error);
	}
}

export async function removeCache(key: string, hostname?: string): Promise<void> {
	const scopedKey = getScopedCacheKey(key, hostname);
	try {
		await kvStorage.removeItem(scopedKey);
	} catch (error) {
		console.error(`Failed to remove cached data for key "${scopedKey}"`, error);
	}
}

export function isAPIResponse(obj: unknown): obj is APIResponse {
	if (typeof obj !== 'object' || obj === null) return false;

	const response = obj as Record<string, unknown>;
	const hasData = 'data' in response;
	const hasMessage = typeof response.message === 'string';
	const hasError =
		typeof response.error === 'string' ||
		(Array.isArray(response.error) &&
			response.error.every((message) => typeof message === 'string'));

	if (response.status === 'success') return hasData;
	if (response.status === 'error') return hasData || hasMessage || hasError;

	return false;
}

export function isAPIErrorResponse(obj: unknown): obj is APIResponse {
	return isAPIResponse(obj) && obj.status === 'error';
}

export function getAPIErrorMessages(response: Pick<APIResponse, 'error'>): string[] {
	if (Array.isArray(response.error)) {
		return response.error.map((message) => message.trim()).filter(Boolean);
	}

	const message = response.error?.trim();
	return message ? [message] : [];
}

export function getAPIErrorText(
	response: Pick<APIResponse, 'error' | 'message'>,
	fallback = 'The request could not be completed.'
): string {
	const errorText = getAPIErrorMessages(response).join(', ');
	return errorText || response.message?.trim() || fallback;
}

export function handleAPIError(result: APIResponse, context?: ErrorRequestContext): void {
	console.error('API Error', result);
	if (context) registerErrorContext(result, context);
	reportAPIError(result);
}
