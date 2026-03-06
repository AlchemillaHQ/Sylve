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
import { reload } from '$lib/stores/api.svelte';
import axios from 'axios';

type SSETokenResponse = {
	token: string;
	expiresIn: number;
};

let eventSource: EventSource | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let leftPanelPulseTimer: ReturnType<typeof setTimeout> | null = null;
let connecting = false;
const LEFT_PANEL_PULSE_COALESCE_MS = 250;

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

async function fetchSSEToken(): Promise<string | null> {
	if (!storage.token) {
		return null;
	}

	try {
		const response = await axios.get('/api/auth/sse-token', {
			headers: {
				Authorization: `Bearer ${storage.token}`
			}
		});

		if (response.status < 400 && response.data?.data) {
			const data = response.data.data as SSETokenResponse;
			if (data.token) {
				return data.token;
			}
		}
	} catch (_e: unknown) {
		return null;
	}

	return null;
}

function cleanupConnection() {
	if (eventSource) {
		eventSource.close();
		eventSource = null;
	}
}

function scheduleReconnect() {
	if (reconnectTimer) {
		clearTimeout(reconnectTimer);
	}

	reconnectTimer = setTimeout(() => {
		void startSSEEvents();
	}, 1500);
}

export async function startSSEEvents() {
	if (connecting || eventSource || !storage.token) {
		return;
	}

	connecting = true;

	const sseToken = await fetchSSEToken();
	if (!sseToken) {
		connecting = false;
		scheduleReconnect();
		return;
	}

	console.debug('Starting SSE connection with token:', sseToken);

	const url = `/api/events/stream?sse_token=${encodeURIComponent(sseToken)}`;
	eventSource = new EventSource(url);

	eventSource.addEventListener('left-panel-refresh', scheduleLeftPanelReload);

	eventSource.addEventListener('reconnect', () => {
		cleanupConnection();
		scheduleReconnect();
	});

	eventSource.addEventListener('cluster-details-refresh', pulseClusterDetailsReload);

	eventSource.onerror = () => {
		cleanupConnection();
		scheduleReconnect();
	};

	eventSource.onopen = () => {
		connecting = false;
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
}
