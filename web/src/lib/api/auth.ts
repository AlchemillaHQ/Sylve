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
import { oldStore, store } from '$lib/stores/auth';
import { hostname, language as langStore, nodeId } from '$lib/stores/basic';
import type { JWTClaims } from '$lib/types/auth';
import type { APIResponse } from '$lib/types/common';
import { handleAPIError } from '$lib/utils/http';
import { sha256 } from '$lib/utils/string';
import adze from 'adze';
import axios, { AxiosError } from 'axios';
// import jwt from 'jsonwebtoken';
import { toast } from 'svelte-sonner';
import { get } from 'svelte/store';

export async function login(
	username: string,
	password: string,
	authType: string,
	remember: boolean,
	language: string
) {
	try {
		if (username === '' || password === '') {
			toast.error('Credentials are required', {
				position: 'bottom-center'
			});

			return;
		}

		if (authType === '') {
			toast.error('Authentication type is required', {
				position: 'bottom-center'
			});

			return;
		}

		const response = await axios.post('/api/auth/login', {
			username,
			password,
			authType,
			remember
		});

		if (response.status === 200 && response.data) {
			if (response.data.data?.hostname && response.data.data?.token) {
				langStore.set(language);
				hostname.set(response.data.data.hostname);
				nodeId.set(response.data.data.nodeId);
				store.set(response.data.data.token);
				return true;
			} else {
				toast.error('Invalid response received', {
					position: 'bottom-center'
				});
			}
		} else {
			return false;
		}
	} catch (error) {
		if (axios.isAxiosError(error)) {
			const axiosError = error as AxiosError;
			const data = axiosError.response?.data as APIResponse;
			handleAPIError(data);
			if (data.error) {
				if (data.error.includes('only_admin_allowed')) {
					toast.error('Only admin users can log in', {
						position: 'bottom-center'
					});
				} else {
					toast.error('Authentication failed', {
						position: 'bottom-center'
					});
				}
			}
		} else {
			toast.error('Fatal error logging in, check logs!', {
				position: 'bottom-center'
			});
		}
		return false;
	}
}

export function getToken(): string | null {
	if (browser) {
		try {
			const parsed = JSON.parse(localStorage.getItem('token') || '');
			return parsed.value;
		} catch (_e: unknown) {
			return null;
		}
	}

	return null;
}

export async function isTokenValid(): Promise<boolean> {
	try {
		const response = await axios.get('/api/health/basic', {
			headers: {
				Authorization: `Bearer ${getToken()}`
			}
		});

		if (response.status < 400) {
			if (response.data?.hostname) {
				hostname.set(response.data.hostname);
				nodeId.set(response.data.data.nodeId);
			}
			return true;
		}
	} catch (_e: unknown) {
		return false;
	}

	return false;
}

export async function logOut() {
	const token = getToken();
	if (token) {
		oldStore.set(token);
	}

	store.set('');
	hostname.set('');

	if (browser) {
		localStorage.removeItem('token');
		localStorage.removeItem('hostname');
	}

	goto('/', {
		replaceState: true,
		state: {
			loggedOut: true
		}
	});
}

export async function revokeJWT() {
	try {
		const oldToken = get(oldStore);
		if (oldToken) {
			await axios.get('/api/auth/logout', {
				headers: {
					Authorization: `Bearer ${oldToken}`
				}
			});

			oldStore.set('');
		}
	} catch (_e: unknown) {
		adze.error('Failed to revoke JWT');
	}
}

export function getJWTClaims(): JWTClaims | null {
	const token = getToken();
	if (token) {
		try {
			return JSON.parse(atob(token.split('.')[1])) as JWTClaims;
		} catch (e) {
			return null;
		}
	}

	return null;
}

export async function getTokenHash(): Promise<string | null> {
	const token = getToken();
	if (!token) {
		return null;
	}

	return await sha256(token);
}
