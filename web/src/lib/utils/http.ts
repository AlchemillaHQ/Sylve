/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { storage } from '$lib';
import { api } from '$lib/api/common';
import { reload } from '$lib/stores/api.svelte';
import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import type { QueryFunctionContext } from '@tanstack/svelte-query';
import adze from 'adze';
import { z } from 'zod/v4';

export async function apiRequest<T extends z.ZodType>(
	endpoint: string,
	schema: T,
	method: 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH',
	body?: unknown
): Promise<z.infer<T>> {
	function setReloadFlag() {
		if (method === 'POST' || method === 'PUT' || method === 'DELETE') {
			reload.auditLog = true;
		}
	}

	try {
		const config = {
			method,
			url: endpoint,
			...(body ? { data: body } : {})
		};

		const response = await api.request({ ...config, validateStatus: () => true });
		const apiResponse = APIResponseSchema.safeParse(response.data);

		if (apiResponse.data) {
			if (apiResponse.data.status && apiResponse.data.status === 'error') {
				if (apiResponse.data.error && apiResponse.data.error === 'invalid_cluster_token') {
					storage.clusterToken = '';
					return apiRequest(endpoint, schema, method, body);
				}
			}
		}

		/* Couldn't parse response data into APIResponse so we'll just return the data? */
		if (!apiResponse.success) {
			setReloadFlag();
			return apiResponse.data;
		}

		/* Caller asked for a raw response */
		if (schema._def.description === 'APIResponseSchema') {
			setReloadFlag();
			return apiResponse.data as z.infer<T>;
		}

		if (apiResponse.data.data) {
			const parsedResult = schema.safeParse(apiResponse.data.data);
			if (parsedResult.success) {
				setReloadFlag();
				return parsedResult.data;
			} else {
				adze.withEmoji.warn('Zod Validation Error', parsedResult.error);
				setReloadFlag();
				return getDefaultValue(schema, apiResponse.data);
			}
		}

		setReloadFlag();
		return getDefaultValue(schema, apiResponse.data);
	} catch (error) {
		setReloadFlag();
		adze.withEmoji.error('API Request Error', error);
		return getDefaultValue(schema, { status: 'error' });
	}
}

function getDefaultValue<T extends z.ZodType>(schema: T, response: APIResponse): z.infer<T> {
	if (schema instanceof z.ZodArray) {
		return [] as z.infer<T>;
	}

	if (schema instanceof z.ZodObject) {
		return response as z.infer<T>;
	}

	return undefined as z.infer<T>;
}

type CacheEntry<T> = {
	timestamp: number;
	data: T;
};

export async function cachedFetch<T>(
	key: string,
	fetchFunction: (queryObj?: QueryFunctionContext) => Promise<T>,
	duration: number
): Promise<T> {
	const now = Date.now();
	const storedEntry = localStorage.getItem(key);

	if (storedEntry) {
		try {
			const entry: CacheEntry<T> = JSON.parse(storedEntry);

			const isFresh = now - entry.timestamp < duration;
			const hasData = entry.data !== null && entry.data !== undefined;
			const looksLikeError =
				hasData &&
				typeof entry.data === 'object' &&
				entry.data !== null &&
				'status' in (entry.data as any) &&
				(entry.data as any).status === 'error';

			if (isFresh && hasData && !looksLikeError) {
				return entry.data;
			}
		} catch (error) {
			console.error(`Failed to parse cached data for key "${key}"`, error);
		}
	}

	const data = await fetchFunction();
	const entry: CacheEntry<T> = { timestamp: now, data };

	const looksLikeError =
		data &&
		typeof data === 'object' &&
		'status' in (data as any) &&
		(data as any).status === 'error';

	if (!looksLikeError) {
		localStorage.setItem(key, JSON.stringify(entry));
	}

	return data;
}

export function getCache<T>(key: string): T | null {
	const storedEntry = localStorage.getItem(key);
	if (storedEntry) {
		try {
			const entry: CacheEntry<T> = JSON.parse(storedEntry);
			return entry.data;
		} catch (error) {
			console.error(`Failed to parse cached data for key "${key}"`, error);
		}
	}
	return null;
}

export async function updateCache<T>(key: string, obj: T): Promise<void> {
	const now = Date.now();
	const storedEntry = localStorage.getItem(key);

	if (storedEntry) {
		try {
			const entry: CacheEntry<T> = JSON.parse(storedEntry);
			entry.data = obj;
			entry.timestamp = now;
			localStorage.setItem(key, JSON.stringify(entry));
		} catch (error) {
			console.error(`Failed to parse cached data for key "${key}"`, error);
		}
	}
}

export function isAPIResponse(obj: any): obj is APIResponse {
	return (
		obj &&
		typeof obj.status === 'string' &&
		(typeof obj.message === 'string' || typeof obj.error === 'string')
	);
}

export function handleAPIError(result: APIResponse): void {
	adze.withEmoji.error('API Error', result);
}
