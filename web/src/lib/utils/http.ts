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
import adze from 'adze';
import { z } from 'zod/v4';
import { kvStorage } from '$lib/types/db';

export async function apiRequest<T extends z.ZodType>(
	endpoint: string,
	schema: T,
	method: 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH',
	body?: unknown,
	options?: { raw?: boolean }
): Promise<z.infer<T>> {
	function setReloadFlag() {
		if (method !== 'GET') {
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
			if (apiResponse.data) {
				return getDefaultValue(schema, { status: 'error' });
			}

			return null as z.infer<T>;
		}

		/* Caller asked for a raw response */
		if (options?.raw) {
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

export async function cachedFetch<T>(
	key: string,
	fetchFunction: () => Promise<T>,
	duration: number,
	onlyCache?: boolean
): Promise<T> {
	const now = Date.now();
	const entry = await kvStorage.getItem<T>(key);

	if (entry && entry.data !== null) {
		const isFresh = now - entry.timestamp < duration;
		const data = entry.data;

		const looksLikeError =
			typeof data === 'object' &&
			data !== null &&
			'status' in data &&
			(data as any).status === 'error';

		if (isFresh && !looksLikeError) {
			return data;
		}
	}

	if (onlyCache) {
		return null as T;
	}

	const data = await fetchFunction();

	if (
		!data ||
		typeof data !== 'object' ||
		!('status' in data) ||
		(data as any).status !== 'error'
	) {
		await kvStorage.setItem(key, data);
	}

	return data;
}

export async function getCache<T>(key: string): Promise<T | null> {
	try {
		const entry = await kvStorage.getItem<T>(key);
		return entry?.data ?? null;
	} catch (error) {
		console.error(`Failed to read cached data for key "${key}"`, error);
		return null;
	}
}

export async function updateCache<T>(key: string, obj: T): Promise<void> {
	try {
		await kvStorage.setItem(key, obj);
	} catch (error) {
		console.error(`Failed to update cached data for key "${key}"`, error);
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
