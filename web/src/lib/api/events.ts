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
import { logOut } from '$lib/api/auth';
import { connection, reload } from '$lib/stores/api.svelte';
import { toast } from 'svelte-sonner';

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

type SSETokenFetchResult =
	| { status: 'success'; token: string; sessionToken: string }
	| { status: 'retryable'; sessionToken: string }
	| { status: 'unauthorized'; sessionToken: string }
	| { status: 'stopped' };

function isSSETokenResponse(value: unknown): value is SSETokenResponse {
	if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
	const data = value as JSONRecord;
	return typeof data.token === 'string' && typeof data.expiresIn === 'number';
}

let eventSource: EventSource | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let connectionLostTimer: ReturnType<typeof setTimeout> | null = null;
let leftPanelPulseTimer: ReturnType<typeof setTimeout> | null = null;
let connecting = false;
const LEFT_PANEL_PULSE_COALESCE_MS = 250;
const CONNECTION_LOST_GRACE_MS = 3000;
const SSE_RECONNECT_INITIAL_MS = 1500;
const SSE_RECONNECT_MAX_MS = 30000;
let reconnectDelayMs = SSE_RECONNECT_INITIAL_MS;

function pulseLeftPanelReload() {
	reload.leftPanel = false;
	queueMicrotask(() => {
		reload.leftPanel = true;
		reload.datacenterNodesPulse += 1;
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
		reload.datacenterDetailsPulse += 1;
	});
}

function pulseNotificationsReload() {
	reload.notifications = false;
	queueMicrotask(() => {
		reload.notifications = true;
	});
}

async function fetchSSEToken(): Promise<SSETokenFetchResult> {
	const sessionToken = storage.token;
	if (!sessionToken) return { status: 'stopped' };

	try {
		const response = await fetch('/api/auth/sse-tokens', {
			method: 'POST',
			headers: {
				Authorization: `Bearer ${sessionToken}`
			}
		});

		const responseData = await parseJSONResponse(response);

		if (response.status < 400 && isSSETokenResponse(responseData?.data)) {
			const data = responseData.data;
			if (data.token) {
				return { status: 'success', token: data.token, sessionToken };
			}
		}

		if (response.status === 401 || response.status === 403) {
			return { status: 'unauthorized', sessionToken };
		}
	} catch (_e: unknown) {
		return { status: 'retryable', sessionToken };
	}

	return { status: 'retryable', sessionToken };
}

function cleanupConnection() {
	if (eventSource) {
		eventSource.close();
		eventSource = null;
	}
}

function clearConnectionLostTimer() {
	if (!connectionLostTimer) return;
	clearTimeout(connectionLostTimer);
	connectionLostTimer = null;
}

function scheduleConnectionLost() {
	if (connection.sseConnected === false || connectionLostTimer) return;

	connectionLostTimer = setTimeout(() => {
		connectionLostTimer = null;
		connection.sseConnected = false;
	}, CONNECTION_LOST_GRACE_MS);
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
	if (sseTokenResult.status !== 'stopped' && storage.token !== sseTokenResult.sessionToken) {
		connecting = false;
		if (storage.token) void startSSEEvents();
		return;
	}

	if (sseTokenResult.status !== 'success') {
		connecting = false;

		if (sseTokenResult.status === 'unauthorized') {
			clearConnectionLostTimer();
			toast.error('Session expired, please login again', {
				position: 'bottom-center'
			});
			void logOut();
		} else if (sseTokenResult.status === 'retryable') {
			scheduleConnectionLost();
			scheduleReconnect();
		}
		return;
	}

	const url = `/api/events/stream?sse_token=${encodeURIComponent(sseTokenResult.token)}`;
	const source = new EventSource(url);
	eventSource = source;

	source.addEventListener('left-panel-refresh', scheduleLeftPanelReload);

	source.addEventListener('reconnect', () => {
		if (eventSource !== source) return;
		cleanupConnection();
		reconnectDelayMs = SSE_RECONNECT_INITIAL_MS;
		void startSSEEvents();
	});

	source.addEventListener('cluster-details-refresh', pulseClusterDetailsReload);
	source.addEventListener('notifications-refresh', pulseNotificationsReload);

	source.onerror = () => {
		if (eventSource !== source) return;
		cleanupConnection();
		scheduleConnectionLost();
		scheduleReconnect();
	};

	source.onopen = () => {
		if (eventSource !== source) return;
		clearConnectionLostTimer();
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

	clearConnectionLostTimer();
	cleanupConnection();
	connecting = false;
	reconnectDelayMs = SSE_RECONNECT_INITIAL_MS;
	connection.sseConnected = null;
}
