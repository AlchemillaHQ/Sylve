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
import { store as token } from '$lib/stores/auth';
import adze from 'adze';
import axios, { AxiosError, type AxiosInstance, type InternalAxiosRequestConfig } from 'axios';
import { toast } from "svelte-sonner";
import { get } from 'svelte/store';

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
            if (get(token)) {
                config.headers['Authorization'] = `Bearer ${get(token)}`;
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
        // adze.withEmoji.error('Status:', axiosError.response.status);
        // adze.withEmoji.error('Data:', axiosError.response.data);
        // adze.withEmoji.error('Error message:', errorMessage);
        // showToast({ text: errorMessage, type: 'error', timeout: 5000 });
    } else if (axiosError.request) {
        // adze.withEmoji.error('No response:', axiosError.request);
        // showToast({
        // 	text: 'No response from server',
        // 	type: 'error',
        // 	timeout: 5000
        // });
    }
}
