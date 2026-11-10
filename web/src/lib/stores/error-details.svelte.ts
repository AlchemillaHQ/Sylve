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
import { toast, type ExternalToast } from 'svelte-sonner';

export interface ErrorRequestContext {
	method?: string;
	path?: string;
	httpStatus?: number;
	node?: string;
}

export interface ErrorDetail {
	title: string;
	denied: boolean;
	status: string;
	httpStatus?: number;
	method?: string;
	path?: string;
	node?: string;
	message: string;
	errors: string[];
	occurredAt: string;
	rawResponse: string;
}

export const errorDetailState = $state<{ open: boolean; data: ErrorDetail | null }>({
	open: false,
	data: null
});

const requestContext = new WeakMap<object, ErrorRequestContext>();
const baseErrorToast = toast.error;

// Most callers log an API response and then emit a friendlier toast in the same turn.
// Hold the response briefly so that toast can be enriched instead of duplicated.
let pendingDetail: ErrorDetail | null = null;
let pendingTimer: ReturnType<typeof setTimeout> | undefined;
let installed = false;

function asRecord(value: unknown): Record<string, unknown> | null {
	return typeof value === 'object' && value !== null ? (value as Record<string, unknown>) : null;
}

export function formatDetailValue(value: unknown): string {
	if (typeof value === 'string') return value;
	if (value instanceof Error) return value.stack || `${value.name}: ${value.message}`;
	if (value === undefined) return 'undefined';

	const seen = new WeakSet<object>();

	try {
		return (
			JSON.stringify(
				value,
				(_key, nestedValue: unknown) => {
					if (typeof nestedValue === 'bigint') return nestedValue.toString();
					if (typeof nestedValue === 'object' && nestedValue !== null) {
						if (seen.has(nestedValue)) return '[Circular]';
						seen.add(nestedValue);
					}
					return nestedValue;
				},
				2
			) ?? String(value)
		);
	} catch {
		return String(value);
	}
}

function extractErrors(detail: unknown): string[] {
	const record = asRecord(detail);
	if (!record || record.error === undefined || record.error === null) return [];

	if (Array.isArray(record.error)) {
		return record.error.map((entry) =>
			typeof entry === 'string' ? entry : formatDetailValue(entry)
		);
	}

	return [typeof record.error === 'string' ? record.error : formatDetailValue(record.error)];
}

function detailMessage(detail: unknown): string {
	const record = asRecord(detail);
	if (typeof record?.message === 'string' && record.message.trim()) return record.message;
	if (detail instanceof Error && detail.message) return detail.message;

	return extractErrors(detail)[0] || 'The request could not be completed.';
}

// A refusal says which privileges were missing on which path, which beats any caller's title.
function permissionDenial(detail: unknown): string | null {
	const record = asRecord(detail);
	if (record?.error === 'superuser_required') return 'Only root or admin can do this';
	if (record?.error !== 'permission_denied') return null;

	const data = asRecord(record.data);
	const privileges = Array.isArray(data?.privileges) ? data.privileges.join(', ') : '';
	const path = typeof data?.path === 'string' ? data.path : '';
	return privileges && path
		? `Needs ${privileges} on ${path}`
		: "You don't have permission to do this";
}

function buildErrorDetail(
	title: string,
	detail: unknown,
	context: ErrorRequestContext = {}
): ErrorDetail {
	const record = asRecord(detail);
	const denial = permissionDenial(detail);

	return {
		title: denial ?? title,
		denied: denial !== null,
		status: typeof record?.status === 'string' ? record.status : 'error',
		httpStatus: context.httpStatus,
		method: context.method,
		path: context.path,
		node: context.node,
		message: detailMessage(detail),
		errors: extractErrors(detail),
		occurredAt: new Date().toISOString(),
		rawResponse: formatDetailValue(detail)
	};
}

function openErrorDetail(detail: ErrorDetail) {
	errorDetailState.data = detail;
	errorDetailState.open = true;
}

export function closeErrorDetail() {
	errorDetailState.open = false;
}

export function clearErrorDetail() {
	if (!errorDetailState.open) errorDetailState.data = null;
}

function createDetailedErrorToast(
	message: Parameters<typeof toast.error>[0],
	options: Parameters<typeof toast.error>[1],
	detail: ErrorDetail
) {
	const duration = Math.max(options?.duration ?? 0, 8000);
	const toastId = baseErrorToast(message, {
		...options,
		...(detail.denied ? { id: `denied:${detail.title}` } : {}),
		duration,
		action: {
			label: 'Details',
			onClick: () => {
				toast.dismiss(toastId);
				openErrorDetail(detail);
			}
		}
	} as ExternalToast);

	return toastId;
}

function consumePendingDetail(message: Parameters<typeof toast.error>[0]) {
	const detail = pendingDetail;
	if (!detail) return null;

	pendingDetail = null;
	if (pendingTimer) clearTimeout(pendingTimer);
	if (typeof message === 'string' && !detail.denied) detail.title = message;

	return detail;
}

function installDetailedErrorToasts() {
	if (!browser || installed) return;
	installed = true;

	toast.error = ((
		message: Parameters<typeof toast.error>[0],
		options?: Parameters<typeof toast.error>[1]
	) => {
		const detail = consumePendingDetail(message);
		return detail
			? createDetailedErrorToast(detail.denied ? detail.title : message, options, detail)
			: baseErrorToast(message, options);
	}) as typeof toast.error;
}

function contextFor(detail: unknown): ErrorRequestContext {
	const record = asRecord(detail);
	return record ? requestContext.get(record) || {} : {};
}

export function registerErrorContext(detail: unknown, context: ErrorRequestContext) {
	const record = asRecord(detail);
	if (record) requestContext.set(record, context);
}

export function stageErrorDetail(
	detail: unknown,
	context: ErrorRequestContext = contextFor(detail)
) {
	if (!browser) return;

	const title = detailMessage(detail);
	const staged = buildErrorDetail(title, detail, context);
	pendingDetail = staged;

	if (pendingTimer) clearTimeout(pendingTimer);
	pendingTimer = setTimeout(() => {
		if (pendingDetail === staged) pendingDetail = null;
	}, 0);
}

export function reportAPIError(detail: unknown, title?: string) {
	if (!browser) return;

	const resolvedTitle = title || detailMessage(detail);
	const staged = buildErrorDetail(resolvedTitle, detail, contextFor(detail));
	pendingDetail = staged;
	if (pendingTimer) clearTimeout(pendingTimer);

	queueMicrotask(() => {
		if (pendingDetail !== staged) return;
		pendingDetail = null;
		createDetailedErrorToast(staged.title, { position: 'bottom-center' }, staged);
	});
}

export function showErrorToast(
	title: string,
	detail: unknown,
	options: ExternalToast = {},
	context: ErrorRequestContext = contextFor(detail)
) {
	if (!browser) return;
	pendingDetail = null;
	if (pendingTimer) clearTimeout(pendingTimer);
	const errorDetail = buildErrorDetail(title, detail, context);
	return createDetailedErrorToast(title, options, errorDetail);
}

installDetailedErrorToasts();
