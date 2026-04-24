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
import { goto } from '$app/navigation';
import { storage } from '$lib';
import type { APIResponse } from '$lib/types/common';
import { toast } from 'svelte-sonner';

export let ENDPOINT: string;
export let API_ENDPOINT: string;

if (browser) {
    ENDPOINT = window.location.origin;
    API_ENDPOINT = `${window.location.origin}/api`;
} else {
    ENDPOINT = '';
    API_ENDPOINT = '';
}

export type APIRequestConfig = {
    url: string;
    method?: string;
    headers?: Record<string, string>;
    data?: unknown;
    body?: BodyInit | null;
    signal?: AbortSignal;
    credentials?: RequestCredentials;
    validateStatus?: (status: number) => boolean;
};

export type APIClientResponse<T = unknown> = {
    status: number;
    data: T;
    headers: Record<string, string>;
    ok: boolean;
};

type APIClientError = Error & {
    response?: APIClientResponse;
    request?: { url: string; method: string };
    status?: number;
    handled?: boolean;
};

const defaultValidateStatus = (status: number): boolean => status >= 200 && status < 300;

function toHeaderRecord(headers: Headers): Record<string, string> {
    const record: Record<string, string> = {};
    headers.forEach((value, key) => {
        record[key] = value;
    });
    return record;
}

function normalizeURL(url: string): string {
    if (url.startsWith('http://') || url.startsWith('https://')) {
        return url;
    }

    if (url.startsWith('/')) {
        return `${API_ENDPOINT}${url}`;
    }

    return `${API_ENDPOINT}/${url}`;
}

function applyRequestDefaults(config: APIRequestConfig): APIRequestConfig {
    const nextConfig: APIRequestConfig = {
        ...config,
        headers: { ...(config.headers || {}) }
    };

    if (!browser) {
        return nextConfig;
    }

    if (storage.token) {
        nextConfig.headers!.Authorization = `Bearer ${storage.token}`;
    }

    if (storage.clusterToken) {
        nextConfig.headers!['X-Cluster-Token'] = `Bearer ${storage.clusterToken}`;
    }

    const routeHost = window.location.pathname.split('/').filter(Boolean)[0] || '';
    const pathBasedHost =
        routeHost !== '' &&
        routeHost !== 'datacenter' &&
        routeHost !== 'login' &&
        routeHost !== 'inactive-node'
            ? routeHost
            : '';
    const fallbackHost = pathBasedHost || storage.localHostname || storage.hostname || '';
    const explicitHost =
        typeof nextConfig.headers!['X-Current-Hostname'] === 'string'
            ? nextConfig.headers!['X-Current-Hostname']
            : '';

    if (explicitHost) {
        nextConfig.headers!['X-Current-Hostname'] = explicitHost;
    } else if (
        (nextConfig.url === '/vm' || nextConfig.url === '/jail') &&
        nextConfig.method?.toLowerCase() === 'post'
    ) {
        try {
            const bodyData = nextConfig.data as { node?: string } | undefined;
            if (bodyData?.node) {
                nextConfig.headers!['X-Current-Hostname'] = `${bodyData.node}`;
            } else if (fallbackHost) {
                nextConfig.headers!['X-Current-Hostname'] = `${fallbackHost}`;
            }
        } catch (e) {
            console.error('Error parsing request data:', e);
            if (fallbackHost) {
                nextConfig.headers!['X-Current-Hostname'] = `${fallbackHost}`;
            }
        }
    } else if (fallbackHost) {
        nextConfig.headers!['X-Current-Hostname'] = `${fallbackHost}`;
    }

    return nextConfig;
}

function isAPIClientError(error: unknown): error is APIClientError {
    return typeof error === 'object' && error !== null && 'request' in error;
}

function isBodyInit(value: unknown): value is BodyInit {
    return (
        typeof value === 'string' ||
        value instanceof Blob ||
        value instanceof FormData ||
        value instanceof URLSearchParams ||
        value instanceof ArrayBuffer ||
        ArrayBuffer.isView(value) ||
        value instanceof ReadableStream
    );
}

async function parseResponseBody(response: Response): Promise<unknown> {
    if (response.status === 204 || response.status === 205) {
        return null;
    }

    const contentType = response.headers.get('content-type') || '';
    if (contentType.includes('application/json') || contentType.includes('+json')) {
        try {
            return await response.json();
        } catch (_e: unknown) {
            return null;
        }
    }

    try {
        return await response.text();
    } catch (_e: unknown) {
        return null;
    }
}

class FetchAPIClient {
    async request<T = unknown>(config: APIRequestConfig): Promise<APIClientResponse<T>> {
        const nextConfig = applyRequestDefaults(config);
        const method = (nextConfig.method || 'GET').toUpperCase();
        const url = normalizeURL(nextConfig.url);
        const headers = { ...(nextConfig.headers || {}) };

        let body: BodyInit | null = nextConfig.body ?? null;
        if (body === null && nextConfig.data !== undefined) {
            if (isBodyInit(nextConfig.data)) {
                body = nextConfig.data;
            } else {
                if (!headers['Content-Type']) {
                    headers['Content-Type'] = 'application/json';
                }
                body = JSON.stringify(nextConfig.data);
            }
        }

        try {
            const response = await fetch(url, {
                method,
                headers,
                body,
                signal: nextConfig.signal,
                credentials: nextConfig.credentials || 'same-origin'
            });

            const data = (await parseResponseBody(response)) as T;
            const normalizedResponse: APIClientResponse<T> = {
                status: response.status,
                data,
                headers: toHeaderRecord(response.headers),
                ok: response.ok
            };

            const validateStatus = nextConfig.validateStatus || defaultValidateStatus;
            if (!validateStatus(response.status)) {
                const error: APIClientError = new Error(
                    `Request failed with status code ${response.status}`
                );
                error.response = normalizedResponse;
                error.request = { url: nextConfig.url, method };
                error.status = response.status;

                if (response.status === 401 && browser) {
                    toast.error('Session expired, please login again', {
                        position: 'bottom-center'
                    });
                    goto('/login');
                }

                handleAxiosError(error);
                error.handled = true;
                throw error;
            }

            return normalizedResponse;
        } catch (error: unknown) {
            if (isAPIClientError(error) && error.handled) {
                throw error;
            }

            const normalizedError: APIClientError =
                isAPIClientError(error) && error.request
                    ? error
                    : Object.assign(new Error('Network error'), {
                        request: { url: nextConfig.url, method }
                    });

            handleAxiosError(normalizedError);
            throw normalizedError;
        }
    }

    get<T = unknown>(url: string, config: Omit<APIRequestConfig, 'url' | 'method'> = {}) {
        return this.request<T>({ ...config, url, method: 'GET' });
    }

    post<T = unknown>(
        url: string,
        data?: unknown,
        config: Omit<APIRequestConfig, 'url' | 'method' | 'data'> = {}
    ) {
        return this.request<T>({ ...config, url, method: 'POST', data });
    }

    put<T = unknown>(
        url: string,
        data?: unknown,
        config: Omit<APIRequestConfig, 'url' | 'method' | 'data'> = {}
    ) {
        return this.request<T>({ ...config, url, method: 'PUT', data });
    }

    patch<T = unknown>(
        url: string,
        data?: unknown,
        config: Omit<APIRequestConfig, 'url' | 'method' | 'data'> = {}
    ) {
        return this.request<T>({ ...config, url, method: 'PATCH', data });
    }

    delete<T = unknown>(url: string, config: Omit<APIRequestConfig, 'url' | 'method'> = {}) {
        return this.request<T>({ ...config, url, method: 'DELETE' });
    }
}

export const api = new FetchAPIClient();

export function handleAxiosError(error: unknown): void {
    if (!browser) return;

    if (!isAPIClientError(error)) {
        toast.error('An unexpected error occurred', {
            position: 'bottom-center'
        });
        console.error('An unexpected error occurred');
        return;
    }

    const axiosError = error as APIClientError;
    if (axiosError.response) {
        const responseData = axiosError.response.data as { message?: string } | undefined;
        const errorMessage =
            responseData?.message || axiosError.message || 'An error occurred';
        console.error(
            JSON.stringify({
                status: axiosError.response.status,
                data: axiosError.response.data,
                message: errorMessage
            })
        );
    } else if (axiosError.request) {
        console.error('No response:', axiosError.request);
    }
}

export function handleAPIResponse(
    response: APIResponse,
    messages: {
        success?: string;
        error?: string;
        info?: string;
        warn?: string;
    }
): void {
    if (response.status === 'error') {
        console.error(response);
        toast.error(messages.error || 'Operation failed', {
            position: 'bottom-center'
        });
    }

    if (response.status === 'success') {
        toast.success(messages.success || 'Operation successful', {
            position: 'bottom-center'
        });
    }
}
