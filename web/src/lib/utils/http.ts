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
};

function getScopedCacheKey(key: string): string {
    if (!browser) {
        return key;
    }

    const routeHost = window.location.pathname.split('/').filter(Boolean)[0] || '';
    if (routeHost && routeHost !== 'datacenter' && routeHost !== 'login' && routeHost !== 'inactive-node') {
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
    function setReloadFlag() {
        if (method !== 'GET' && !options?.skipAuditLog) {
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
            ...(body ? { data: body } : {})
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
                if (apiResponse.data.error && apiResponse.data.error === 'invalid_cluster_token') {
                    storage.clusterToken = '';
                    return apiRequest(endpoint, schema, method, body, options);
                }

                registerErrorContext(apiResponse.data, errorContext);
                stageErrorDetail(apiResponse.data, errorContext);
                setReloadFlag();
                if (options?.raw) return apiResponse.data as z.infer<T>;
                return getDefaultValue(schema, apiResponse.data);
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
            return getDefaultValue(schema, invalidResponse);
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
                console.warn('Zod Validation Error', parsedResult.error, apiResponse.data);
                setReloadFlag();
                return getDefaultValue(schema, apiResponse.data);
            }
        }

        setReloadFlag();
        return getDefaultValue(schema, apiResponse.data);
    } catch (error) {
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
        return getDefaultValue(schema, failedResponse);
    }
}

function getDefaultValue<T extends z.ZodType>(
    schema: T,
    response: APIResponse
): z.infer<T> | APIResponse {
    if (schema instanceof z.ZodArray) {
        return [] as z.infer<T>;
    }

    return response;
}

export async function cachedFetch<T>(
    key: string,
    fetchFunction: () => Promise<T>,
    duration: number,
    onlyCache?: boolean
): Promise<T> {
    const scopedKey = getScopedCacheKey(key);
    const now = Date.now();
    const entry = await kvStorage.getItem<T>(scopedKey);

    if (entry && entry.data !== null) {
        const isFresh = now - entry.timestamp < duration;
        const data = entry.data;

        const looksLikeError =
            typeof data === 'object' &&
            data !== null &&
            'status' in data &&
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
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
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        (data as any).status !== 'error'
    ) {
        await kvStorage.setItem(scopedKey, data);
    }

    return data;
}

export async function getCache<T>(key: string): Promise<T | null> {
    const scopedKey = getScopedCacheKey(key);
    try {
        const entry = await kvStorage.getItem<T>(scopedKey);
        return entry?.data ?? null;
    } catch (error) {
        console.error(`Failed to read cached data for key "${scopedKey}"`, error);
        return null;
    }
}

export async function updateCache<T>(key: string, obj: T): Promise<void> {
    const scopedKey = getScopedCacheKey(key);
    try {
        await kvStorage.setItem(scopedKey, obj);
    } catch (error) {
        console.error(`Failed to update cached data for key "${scopedKey}"`, error);
    }
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function isAPIResponse(obj: any): obj is APIResponse {
    return (
        obj &&
        typeof obj.status === 'string' &&
        (typeof obj.message === 'string' ||
            typeof obj.error === 'string' ||
            Array.isArray(obj.error))
    );
}

export function handleAPIError(result: APIResponse, context?: ErrorRequestContext): void {
    console.error('API Error', result);
    if (context) registerErrorContext(result, context);
    reportAPIError(result);
}
