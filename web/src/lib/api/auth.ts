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
import { useSafeGoto } from '$lib/hooks/navigation.svelte';
import type { JWTClaims } from '$lib/types/auth';
import type { APIResponse } from '$lib/types/common';
import { kvStorage } from '$lib/types/db';
import { handleAPIError } from '$lib/utils/http';
import { buildLoginOptions, isPasskeySupported, serializeCredential } from '$lib/utils/passkeys';
import { sha256 } from '$lib/utils/string';
import { toast } from 'svelte-sonner';

const SYSTEM_HOSTNAME_NOT_CONFIGURED = 'system_hostname_not_configured';
const SYSTEM_HOSTNAME_NOT_CONFIGURED_MESSAGE =
	'System hostname is not configured. Set one and restart Sylve.';

type JSONRecord = Record<string, unknown>;

function asJSONRecord(value: unknown): JSONRecord | null {
	return typeof value === 'object' && value !== null && !Array.isArray(value)
		? (value as JSONRecord)
		: null;
}

async function parseJSONResponse(response: Response): Promise<JSONRecord | null> {
	const contentType = response.headers.get('content-type') || '';
	if (!contentType.includes('application/json') && !contentType.includes('+json')) {
		return null;
	}

	try {
		return asJSONRecord(await response.json());
	} catch (_e: unknown) {
		return null;
	}
}

function readStringField(payload: unknown, field: string): string {
	const record = asJSONRecord(payload);
	if (!record) return '';

	const value = record[field];
	return typeof value === 'string' ? value : '';
}

function readRecordField(payload: unknown, field: string): JSONRecord | null {
	const record = asJSONRecord(payload);
	return record ? asJSONRecord(record[field]) : null;
}

function hasMissingHostname(payload: unknown): boolean {
	return (
		readStringField(payload, 'token').trim() !== '' &&
		readStringField(payload, 'hostname').trim() === ''
	);
}

function hasErrorCode(data: APIResponse, code: string): boolean {
	if (data.message === code) return true;

	const errors = Array.isArray(data.error) ? data.error : [data.error];
	return errors.some((error) => typeof error === 'string' && error.includes(code));
}

function showLoginFailure(
	data: APIResponse,
	path: string,
	httpStatus: number,
	fallback: string
): void {
	handleAPIError(data, {
		method: 'POST',
		path,
		httpStatus
	});

	let message = fallback;
	if (hasErrorCode(data, SYSTEM_HOSTNAME_NOT_CONFIGURED)) {
		message = SYSTEM_HOSTNAME_NOT_CONFIGURED_MESSAGE;
	} else if (hasErrorCode(data, 'only_admin_allowed')) {
		message = 'Only admin users can log in';
	} else if (hasErrorCode(data, 'account_locked')) {
		message = 'This account is locked';
	} else if (hasErrorCode(data, 'password_auth_disabled')) {
		message = 'Password login is disabled for this account';
	} else if (hasErrorCode(data, 'pam_auth_disabled')) {
		message = 'PAM authentication is disabled';
	} else if (hasErrorCode(data, 'user_not_registered_in_sylve')) {
		message = 'This PAM account is not registered in Sylve';
	} else if (hasErrorCode(data, 'too_many_attempts')) {
		message = 'Too many login attempts. Please try again later';
	}

	toast.error(message, {
		position: 'bottom-center'
	});
}

function applySuccessfulLogin(payload: unknown): boolean {
	const hostname = readStringField(payload, 'hostname').trim();
	const token = readStringField(payload, 'token');

	if (!hostname || !token.trim()) {
		return false;
	}

	storage.localHostname = hostname;
	storage.hostname = hostname;
	storage.nodeId = readStringField(payload, 'nodeId');
	storage.token = token;

	return true;
}

async function clearCachedAPIData() {
	try {
		await kvStorage.clear();
	} catch (error) {
		console.warn('Failed to clear cached API data', error);
	}
}

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

		const response = await fetch('/api/auth/login', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json'
			},
			body: JSON.stringify({
				username,
				password,
				authType,
				remember
			})
		});

		const responseData = await parseJSONResponse(response);

		if (response.status === 200 && responseData) {
			if (applySuccessfulLogin(responseData.data)) {
				await clearCachedAPIData();
				return true;
			} else {
				toast.error(
					hasMissingHostname(responseData.data)
						? SYSTEM_HOSTNAME_NOT_CONFIGURED_MESSAGE
						: 'Invalid response received',
					{
						position: 'bottom-center'
					}
				);
			}

			return false;
		}

		const data = (responseData || {}) as APIResponse;
		showLoginFailure(data, '/api/auth/login', response.status, 'Authentication failed');

		return false;
	} catch (error) {
		console.error('Login error:', error);
		toast.error('Fatal error logging in, check logs!', {
			position: 'bottom-center'
		});
		return false;
	}

	return false;
}

export async function getLoginConfig(): Promise<{ pamEnabled: boolean }> {
	try {
		const response = await fetch('/api/auth/login/config', {
			method: 'GET'
		});

		const responseData = await parseJSONResponse(response);
		const config = readRecordField(responseData, 'data');
		if (response.status === 200 && config && typeof config.pamEnabled === 'boolean') {
			return { pamEnabled: config.pamEnabled };
		}
	} catch (error) {
		console.warn('Failed to load login config', error);
	}

	return { pamEnabled: true };
}

export async function loginWithPasskey(remember: boolean): Promise<boolean> {
	try {
		if (!isPasskeySupported()) {
			toast.error('Passkeys require HTTPS and browser WebAuthn support', {
				position: 'bottom-center'
			});
			return false;
		}

		const beginResponse = await fetch('/api/auth/passkeys/login/begin', {
			method: 'POST'
		});

		const beginResponseData = await parseJSONResponse(beginResponse);
		const beginData = readRecordField(beginResponseData, 'data');
		const requestId = readStringField(beginData, 'requestId');
		const publicKeyData = beginData?.publicKey;
		if (beginResponse.status !== 200 || !requestId || !publicKeyData) {
			const data = (beginResponseData || {}) as APIResponse;
			handleAPIError(data, {
				method: 'POST',
				path: '/api/auth/passkeys/login/begin',
				httpStatus: beginResponse.status
			});
			toast.error('Passkey login could not be started', {
				position: 'bottom-center'
			});
			return false;
		}

		const publicKey = buildLoginOptions(publicKeyData);
		const credential = await navigator.credentials.get({ publicKey });
		if (!credential || !(credential instanceof PublicKeyCredential)) {
			toast.error('Passkey authentication failed', {
				position: 'bottom-center'
			});
			return false;
		}

		const finishResponse = await fetch('/api/auth/passkeys/login/finish', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json'
			},
			body: JSON.stringify({
				requestId,
				credential: serializeCredential(credential),
				remember
			})
		});

		const finishResponseData = await parseJSONResponse(finishResponse);
		const finishData = finishResponseData?.data;
		if (finishResponse.status === 200 && finishData) {
			if (applySuccessfulLogin(finishData)) {
				await clearCachedAPIData();
				return true;
			}

			if (hasMissingHostname(finishData)) {
				toast.error(SYSTEM_HOSTNAME_NOT_CONFIGURED_MESSAGE, {
					position: 'bottom-center'
				});
				return false;
			}
		}

		const data = (finishResponseData || {}) as APIResponse;
		showLoginFailure(
			data,
			'/api/auth/passkeys/login/finish',
			finishResponse.status,
			'Passkey authentication failed'
		);
		return false;
	} catch (error) {
		if (error instanceof DOMException && error.name === 'NotAllowedError') {
			toast.error('Passkey request cancelled or timed out', {
				position: 'bottom-center'
			});
			return false;
		}

		console.error('Passkey login error:', error);
		toast.error('Fatal error during passkey login', {
			position: 'bottom-center'
		});
		return false;
	}
}

export function getToken(): string | null {
	if (browser) {
		return storage.token;
	}

	return null;
}

export async function isTokenValid(): Promise<boolean> {
	if (!storage.token) {
		return false;
	}

	try {
		const response = await fetch('/api/health/basic', {
			headers: {
				Authorization: `Bearer ${storage.token}`
			}
		});

		const responseData = await parseJSONResponse(response);

		if (response.status < 400) {
			const hostname = readStringField(responseData, 'hostname');
			const nodeId = readStringField(responseData, 'nodeId');
			if (hostname) {
				storage.localHostname = hostname;
				if (!storage.hostname) {
					storage.hostname = hostname;
				}
			}
			if (nodeId) {
				storage.nodeId = nodeId;
			}
			return true;
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
		void revokeJWT(token);
	}

	storage.token = '';
	storage.localHostname = '';
	storage.hostname = '';
	storage.nodeId = '';
	storage.enabledServices = null;
	storage.enabledServicesByHostname = {};

	if (browser) {
		localStorage.removeItem('token');
		localStorage.removeItem('localHostname');
		localStorage.removeItem('hostname');
		localStorage.removeItem('nodeId');
	}

	await clearCachedAPIData();

	if (message) {
		toast.success(message, {
			position: 'bottom-center'
		});
	}

	useSafeGoto('/', {
		replaceState: true,
		state: {
			loggedOut: true
		}
	});
}

export async function revokeJWT(token = storage.oldToken) {
	if (!token) return;

	try {
		await fetch('/api/auth/logout', {
			method: 'POST',
			headers: {
				Authorization: `Bearer ${token}`
			},
			keepalive: true
		});
	} catch (_e: unknown) {
		console.error('Failed to revoke JWT');
	} finally {
		if (storage.oldToken === token) {
			storage.oldToken = '';
		}
	}
}

export function getJWTClaims(): JWTClaims | null {
	const token = getToken();
	if (token) {
		try {
			return JSON.parse(atob(token.split('.')[1])) as JWTClaims;
		} catch {
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

export async function isInitialized(): Promise<boolean[]> {
	try {
		const response = await fetch('/api/health/basic', {
			headers: {
				Authorization: `Bearer ${storage.token}`
			}
		});

		const responseData = await parseJSONResponse(response);
		const data = readRecordField(responseData, 'data');

		if (response.status === 200 && data) {
			return [data.initialized === true, data.restarted === true];
		}
	} catch (_e: unknown) {
		return [false, false];
	}

	return [false, false];
}
