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
import { connection, reload } from '$lib/stores/api.svelte';

type JSONRecord = Record<string, unknown>;

async function parseJSONResponse(response: Response): Promise<JSONRecord | null> {
	const contentType = response.headers.get('content-type') || '';
	if (!contentType.includes('application/json') && !contentType.includes('+json')) {
		return null;
	}

	try {
		const value: unknown = await response.json();
		return typeof value === 'object' && value !== null && !Array.isArray(value)
			? (value as JSONRecord)
			: null;
	} catch (_e: unknown) {
		return null;
	}
}

type SSETokenResponse = {
	token: string;
	expiresIn: number;
};

type SSETokenFetchResult = {
	token: string | null;
	retry: boolean;
};

function isSSETokenResponse(value: unknown): value is SSETokenResponse {
	if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
	const data = value as JSONRecord;
	return typeof data.token === 'string' && typeof data.expiresIn === 'number';
}

let eventSource: EventSource | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let leftPanelPulseTimer: ReturnType<typeof setTimeout> | null = null;
let connecting = false;
const LEFT_PANEL_PULSE_COALESCE_MS = 250;
const SSE_RECONNECT_INITIAL_MS = 1500;
const SSE_RECONNECT_MAX_MS = 30000;
let reconnectDelayMs = SSE_RECONNECT_INITIAL_MS;

function pulseLeftPanelReload() {
	reload.leftPanel = false;
	queueMicrotask(() => {
		reload.leftPanel = true;
		reload.auditLog = true;
	});
}

function scheduleLeftPanelReload() {
	if (leftPanelPulseTimer) {
		return;
	}

	leftPanelPulseTimer = setTimeout(() => {
		leftPanelPulseTimer = null;
		pulseLeftPanelReload();
	}, LEFT_PANEL_PULSE_COALESCE_MS);
}

function pulseClusterDetailsReload() {
	reload.clusterDetails = false;
	queueMicrotask(() => {
		reload.clusterDetails = true;
	});
}

function pulseNotificationsReload() {
	reload.notifications = false;
	queueMicrotask(() => {
		reload.notifications = true;
	});
}

async function fetchSSEToken(): Promise<SSETokenFetchResult> {
	if (!storage.token) {
		return { token: null, retry: false };
	}

	try {
		const response = await fetch('/api/auth/sse-tokens', {
			method: 'POST',
			headers: {
				Authorization: `Bearer ${storage.token}`
			}
		});

		const responseData = await parseJSONResponse(response);

		if (response.status < 400 && isSSETokenResponse(responseData?.data)) {
			const data = responseData.data;
			if (data.token) {
				return { token: data.token, retry: false };
			}
		}

		if (response.status === 401 || response.status === 403) {
			return { token: null, retry: false };
		}
	} catch (_e: unknown) {
		return { token: null, retry: true };
	}

	return { token: null, retry: true };
}

function cleanupConnection() {
	if (eventSource) {
		eventSource.close();
		eventSource = null;
	}
}

function scheduleReconnect() {
	if (!storage.token) {
		return;
	}

	if (reconnectTimer) {
		clearTimeout(reconnectTimer);
	}

	const delay = reconnectDelayMs;
	reconnectDelayMs = Math.min(reconnectDelayMs * 2, SSE_RECONNECT_MAX_MS);
	reconnectTimer = setTimeout(() => {
		reconnectTimer = null;
		void startSSEEvents();
	}, delay);
}

export async function startSSEEvents() {
	if (connecting || eventSource || !storage.token) {
		return;
	}

	connecting = true;

	const sseTokenResult = await fetchSSEToken();
	if (!sseTokenResult.token) {
		connecting = false;
		connection.sseConnected = false;
		if (sseTokenResult.retry) {
			scheduleReconnect();
		}
		return;
	}

	const url = `/api/events/stream?sse_token=${encodeURIComponent(sseTokenResult.token)}`;
	eventSource = new EventSource(url);

	eventSource.addEventListener('left-panel-refresh', scheduleLeftPanelReload);

	eventSource.addEventListener('reconnect', () => {
		connection.sseConnected = false;
		cleanupConnection();
		scheduleReconnect();
	});

	eventSource.addEventListener('cluster-details-refresh', pulseClusterDetailsReload);
	eventSource.addEventListener('notifications-refresh', pulseNotificationsReload);

	eventSource.onerror = () => {
		connection.sseConnected = false;
		cleanupConnection();
		scheduleReconnect();
	};

	eventSource.onopen = () => {
		connection.sseConnected = true;
		connecting = false;
		reconnectDelayMs = SSE_RECONNECT_INITIAL_MS;
	};

	connecting = false;
}

export function stopSSEEvents() {
	if (reconnectTimer) {
		clearTimeout(reconnectTimer);
		reconnectTimer = null;
	}

	if (leftPanelPulseTimer) {
		clearTimeout(leftPanelPulseTimer);
		leftPanelPulseTimer = null;
	}

	cleanupConnection();
	connecting = false;
	reconnectDelayMs = SSE_RECONNECT_INITIAL_MS;
	connection.sseConnected = null;
}
