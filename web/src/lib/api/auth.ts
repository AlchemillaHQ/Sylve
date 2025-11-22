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
import type { JWTClaims } from '$lib/types/auth';
import type { APIResponse, Locales } from '$lib/types/common';
import { handleAPIError } from '$lib/utils/http';
import { sha256 } from '$lib/utils/string';
import adze from 'adze';
import axios, { AxiosError } from 'axios';
import { toast } from 'svelte-sonner';

export async function login(
	username: string,
	password: string,
	authType: string,
	remember: boolean
): Promise<boolean> {
	try {
		if (username === '' || password === '') {
			toast.error('Credentials are required', {
				position: 'bottom-center'
			});

			return false;
		}

		if (authType === '') {
			toast.error('Authentication type is required', {
				position: 'bottom-center'
			});

			return false;
		}

		const response = await axios.post('/api/auth/login', {
			username,
			password,
			authType,
			remember
		});

		if (response.status === 200 && response.data) {
			if (response.data.data?.hostname && response.data.data?.token) {
				console.log('Received login data:', response.data.data);

				storage.hostname = response.data.data.hostname;
				storage.nodeId = response.data.data.nodeId || '';
				storage.token = response.data.data.token || '';
				storage.clusterToken = response.data.data.clusterToken || '';

				console.log('Login response:', response.data);
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
		console.error('Login error:', error);
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

	return false;
}

export function getToken(): string | null {
	if (browser) {
		return storage.token;
	}

	return null;
}

export function getClusterToken(): string | null {
	if (browser) {
		return storage.clusterToken;
	}

	return null;
}

export async function isTokenValid(): Promise<boolean> {
	if (!storage.token) {
		return false;
	}

	try {
		const response = await axios.get('/api/health/basic', {
			headers: {
				Authorization: `Bearer ${storage.token}`
			}
		});

		if (response.status < 400) {
			if (response.data?.hostname) {
				storage.hostname = response.data.hostname;
			}
			if (response.data?.nodeId) {
				storage.nodeId = response.data.nodeId;
			}
			return true;
		}
	} catch (_e: unknown) {
		return false;
	}

	return false;
}

export async function isClusterTokenValid(): Promise<boolean> {
	try {
		const clusterToken = storage.clusterToken;
		if (!clusterToken) {
			return true;
		}

		const response = await axios.get('/api/health/basic', {
			headers: {
				Authorization: `Bearer ${clusterToken}`,
				'X-Cluster-Token': `Bearer ${clusterToken}`
			}
		});

		if (response.status < 400) {
			if (response.data?.hostname) {
				// setLocalStorage('hostname', response.data.hostname);
				storage.hostname = response.data.hostname;
			}
			if (response.data?.nodeId) {
				// setLocalStorage('nodeId', response.data.nodeId);
				storage.nodeId = response.data.nodeId;
			}
			return true;
		} else {
			localStorage.removeItem('clusterToken');
		}
	} catch (_e: unknown) {
		return false;
	}

	return false;
}

export async function logOut(message?: string) {
	const token = storage.token;

	if (token) {
		storage.oldToken = token;
	}

	storage.token = '';
	storage.clusterToken = '';
	storage.hostname = '';
	storage.nodeId = '';

	if (browser) {
		localStorage.removeItem('token');
		localStorage.removeItem('hostname');
		localStorage.removeItem('nodeId');
		localStorage.removeItem('clusterToken');
	}

	if (message) {
		toast.success(message, {
			position: 'bottom-center'
		});
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
		const oldtoken = storage.oldToken;
		if (oldtoken) {
			await axios.get('/api/auth/logout', {
				headers: {
					Authorization: `Bearer ${oldtoken}`
				}
			});

			storage.oldToken = '';
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

export async function isInitialized(): Promise<boolean> {
	try {
		const response = await axios.get('/api/health/basic', {
			headers: {
				Authorization: `Bearer ${storage.token}`
			}
		});

		if (response.status === 200 && response.data && response.data.data) {
			return response.data.data.initialized === true;
		}
	} catch (_e: unknown) {
		return false;
	}

	return false;
}
