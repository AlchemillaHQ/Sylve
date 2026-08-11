import { browser } from '$app/environment';
import { storage } from '$lib';
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

export function getFilePondRequestHeaders(selectedHostname?: string): Record<string, string> {
	if (!browser) return {};

	const hostname = getFilePondRequestHostname(selectedHostname);
	const headers: Record<string, string> = {};
	const token = storage.token?.trim();

	if (token) headers.Authorization = `Bearer ${token}`;
	if (hostname) headers['X-Current-Hostname'] = hostname;

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
