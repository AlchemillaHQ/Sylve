/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { api } from '$lib/api/common';
import { APIResponseSchema } from '$lib/types/common';
import type { QueryFunctionContext } from '@sveltestack/svelte-query';
import adze from 'adze';
import type { z } from 'zod';

export async function apiRequest<T extends z.ZodType>(
	endpoint: string,
	schema: T,
	method: 'GET' | 'POST' | 'PUT' | 'DELETE',
	body?: unknown
): Promise<z.infer<T>> {
	try {
		const config = {
			method,
			url: endpoint,
			...(body ? { data: body } : {})
		};

		const response = await api.request(config);
		const apiResponse = APIResponseSchema.safeParse(response.data);

		if (!apiResponse.success) {
			adze.withEmoji.warn('Invalid API response structure:', apiResponse.error);
			return schema.parse({});
		}

		const responseData = apiResponse.data.data;
		const parsedResult = schema.safeParse(responseData);

		if (parsedResult.success) {
			return parsedResult.data;
		}

		if (responseData && typeof responseData === 'object') {
			const nestedResult = schema.safeParse((responseData as any).data);
			if (nestedResult.success) {
				return nestedResult.data;
			}
		}

		adze.withEmoji.warn(
			'Failed to parse API response data:',
			parsedResult.error,
			responseData,
			endpoint
		);
		return schema.parse({});
	} catch (error) {
		adze.withEmoji.error(`Error in ${method} request to ${endpoint}:`, error);
		return schema.parse({});
	}
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
			if (now - entry.timestamp < duration) {
				return entry.data;
			}
		} catch (error) {
			console.error(`Failed to parse cached data for key "${key}"`, error);
		}
	}

	const data = await fetchFunction();
	const entry: CacheEntry<T> = { timestamp: now, data };
	localStorage.setItem(key, JSON.stringify(entry));
	return data;
}
