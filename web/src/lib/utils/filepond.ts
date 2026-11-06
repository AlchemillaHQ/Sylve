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
import { nodePath } from '$lib/api/common';
import { resolveNodeHostname } from '$lib/utils/enabled-services';

export function getFilePondRequestHostname(selectedHostname?: string): string {
	const explicitHostname = selectedHostname?.trim();
	if (explicitHostname) return explicitHostname;
	if (!browser) return '';

	return (
		resolveNodeHostname(window.location.pathname) ||
		storage.localHostname?.trim() ||
		storage.hostname?.trim() ||
		''
	);
}

export function getFilePondRequestURL(path: string, selectedHostname?: string): string {
	return nodePath(path, getFilePondRequestHostname(selectedHostname));
}

export function getFilePondRequestHeaders(): Record<string, string> {
	if (!browser) return {};

	const headers: Record<string, string> = {};
	const token = storage.token?.trim();
	if (token) headers.Authorization = `Bearer ${token}`;

	return headers;
}

export function parseFilePondUploadID(response: string): string {
	try {
		const parsed = JSON.parse(response) as {
			data?: { uploadId?: unknown };
		};
		return typeof parsed.data?.uploadId === 'string' ? parsed.data.uploadId.trim() : '';
	} catch {
		return '';
	}
}

export function parseFilePondUploadError(response: string): string {
	try {
		const parsed = JSON.parse(response) as { error?: unknown; message?: unknown };
		if (Array.isArray(parsed.error)) return parsed.error.join(', ');
		if (typeof parsed.error === 'string' && parsed.error) return parsed.error;
		if (typeof parsed.message === 'string' && parsed.message) return parsed.message;
	} catch {
		// FilePond can display a non-JSON response directly.
	}
	return response || 'Upload failed';
}
