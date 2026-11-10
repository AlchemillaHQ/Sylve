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
import type { APIResponse } from '$lib/types/common';
import { toast } from 'svelte-sonner';
import { showErrorToast, stageErrorDetail } from '$lib/stores/error-details.svelte';
import { isDemoMode } from '$lib/demo/runtime';

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
	node?: string;
	method?: string;
	headers?: Record<string, string>;
	data?: unknown;
	body?: BodyInit | null;
	responseType?: 'blob';
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

	const node = nextConfig.node || pageNode();
	if (node && nextConfig.url.startsWith('/') && !isNodeless(nextConfig.url)) {
		nextConfig.url = nodePath(nextConfig.url, node);
	}

	return nextConfig;
}

export function isSessionEnded(detail: unknown): boolean {
	const message = (detail as { message?: unknown } | null)?.message;
	return (
		typeof message === 'string' && (message.startsWith('jwt_') || message === 'no_token_provided')
	);
}

// Never node-prefixed: cluster-wide, or the signed-in node's own session.
const NODELESS_ROUTES = [
	'/auth/login',
	'/auth/passkeys/login',
	'/auth/logout',
	'/auth/sse-tokens',
	'/auth/preferences',
	'/basic',
	'/cluster',
	'/health',
	'/events'
];

function isNodeless(url: string): boolean {
	return NODELESS_ROUTES.some(
		(route) => url === route || url.startsWith(`${route}/`) || url.startsWith(`${route}?`)
	);
}

export function pageNode(): string {
	const segment = window.location.pathname.split('/').filter(Boolean)[0] || '';
	return ['datacenter', 'login', 'inactive-node'].includes(segment) ? '' : segment;
}

export function nodePath(path: string, node?: string | null): string {
	if (!node || /^(\/api)?\/nodes\//.test(path)) return path;
	const prefix = `/nodes/${encodeURIComponent(node)}`;
	return path.startsWith('/api/') ? `/api${prefix}${path.slice(4)}` : `${prefix}${path}`;
}

function isAPIClientError(error: unknown): error is APIClientError {
	return typeof error === 'object' && error !== null && 'request' in error;
}

function isAbortError(error: unknown): boolean {
	return (
		typeof error === 'object' && error !== null && 'name' in error && error.name === 'AbortError'
	);
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

async function parseResponseBody(
	response: Response,
	responseType?: APIRequestConfig['responseType']
): Promise<unknown> {
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

	if (responseType === 'blob') {
		try {
			return await response.blob();
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

		if (isDemoMode) {
			const { handleDemoRequest } = await import('$lib/demo/fixtures');
			return await handleDemoRequest<T>({ ...nextConfig, method });
		}

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

			const data = (await parseResponseBody(response, nextConfig.responseType)) as T;
			const normalizedResponse: APIClientResponse<T> = {
				status: response.status,
				data,
				headers: toHeaderRecord(response.headers),
				ok: response.ok
			};

			const validateStatus = nextConfig.validateStatus || defaultValidateStatus;
			const failed = !validateStatus(response.status);
			// Callers that read error responses themselves still lose a revoked session here.
			if (response.status === 401 && browser && storage.token && (failed || isSessionEnded(data))) {
				toast.error('Session expired, please login again', {
					id: 'session-expired',
					position: 'bottom-center'
				});
				// Loaded lazily: auth.ts reaches back into this module.
				void import('$lib/api/auth').then(({ logOut }) => logOut());
			}

			if (failed) {
				const error: APIClientError = new Error(
					`Request failed with status code ${response.status}`
				);
				error.response = normalizedResponse;
				error.request = { url: nextConfig.url, method };
				error.status = response.status;

				handleAxiosError(error);
				error.handled = true;
				throw error;
			}

			return normalizedResponse;
		} catch (error: unknown) {
			if (isAbortError(error)) {
				throw error;
			}
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
		const errorMessage = responseData?.message || axiosError.message || 'An error occurred';
		if (axiosError.response.status !== 401) {
			stageErrorDetail(axiosError.response.data, {
				method: axiosError.request?.method,
				path: axiosError.request?.url,
				httpStatus: axiosError.response.status
			});
		}
		console.error(
			JSON.stringify({
				status: axiosError.response.status,
				data: axiosError.response.data,
				message: errorMessage
			})
		);
	} else if (axiosError.request) {
		stageErrorDetail(
			{
				status: 'error',
				message: 'No response from server',
				error: axiosError.message
			},
			{
				method: axiosError.request.method,
				path: axiosError.request.url
			}
		);
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
		showErrorToast(messages.error || 'Operation failed', response, {
			position: 'bottom-center'
		});
	}

	if (response.status === 'success') {
		toast.success(messages.success || 'Operation successful', {
			position: 'bottom-center'
		});
	}
}
