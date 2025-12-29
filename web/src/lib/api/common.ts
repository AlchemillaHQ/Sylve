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
import adze from 'adze';
import axios, { AxiosError, type AxiosInstance, type InternalAxiosRequestConfig } from 'axios';
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

export const api: AxiosInstance = axios.create({
	baseURL: API_ENDPOINT
});

api.interceptors.request.use(
	(config: InternalAxiosRequestConfig) => {
		if (browser) {
			if (storage.token) {
				config.headers['Authorization'] = `Bearer ${storage.token}`;
			}

			if (storage.clusterToken) {
				config.headers['X-Cluster-Token'] = `Bearer ${storage.clusterToken}`;
			}

			if (storage.hostname) {
				if ((config.url === '/vm' || config.url === '/jail') && config.method === 'post') {
					let data;

					try {
						data = config.data;
						if (data.node) {
							config.headers['X-Current-Hostname'] = `${storage.hostname}`;
						}
					} catch (e) {
						adze.withEmoji.error('Error parsing request data:', e);
						config.headers['X-Current-Hostname'] = `${storage.hostname}`;
					}
				} else {
					config.headers['X-Current-Hostname'] = `${storage.hostname}`;
				}
			}
		}
		return config;
	},
	(error) => {
		return Promise.reject(error);
	}
);

api.interceptors.response.use(
	(response) => response,
	async (error) => {
		if (error.response?.status === 401 && browser) {
			toast.error('Session expired, please login again', {
				position: 'bottom-center'
			});
			goto('/login');
			return;
		}
		handleAxiosError(error);
		return Promise.reject(error);
	}
);

export function handleAxiosError(error: unknown): void {
	if (!browser) return;

	if (!axios.isAxiosError(error)) {
		toast.error('An unexpected error occurred', {
			position: 'bottom-center'
		});
		adze.withEmoji.error('An unexpected error occurred');
		return;
	}

	const axiosError = error as AxiosError<{ message?: string }>;
	if (axiosError.response) {
		const errorMessage =
			axiosError.response.data?.message || axiosError.message || 'An error occurred';
		adze.withEmoji.error(
			JSON.stringify({
				status: axiosError.response.status,
				data: axiosError.response.data,
				message: errorMessage
			})
		);
	} else if (axiosError.request) {
		adze.withEmoji.error('No response:', axiosError.request);
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
		adze.withEmoji.error(response);
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
