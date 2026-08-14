/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	NotificationConfigSchema,
	NotificationRulesConfigSchema,
	NotificationsCountSchema,
	NotificationsDismissAllSchema,
	NotificationsListSchema,
	type BulkUpdateRulesInput,
	type NotificationConfig,
	type NotificationRulesConfig,
	type NotificationTransportInput,
	type NotificationsCount,
	type NotificationsDismissAll,
	type NotificationsList,
	type CreateNotificationRuleInput,
	type UpdateNotificationRuleInput,
	type UpdateNotificationRulesInput
} from '$lib/types/notifications';
import { apiRequest } from '$lib/utils/http';

export async function listNotifications(
	scope: 'active' | 'all' = 'active',
	limit = 50,
	offset = 0
): Promise<NotificationsList> {
	const query = new URLSearchParams({
		scope,
		limit: `${limit}`,
		offset: `${offset}`
	});

	return await apiRequest(`/notifications?${query.toString()}`, NotificationsListSchema, 'GET');
}

export async function getNotificationsCount(): Promise<NotificationsCount> {
	return await apiRequest('/notifications/count', NotificationsCountSchema, 'GET');
}

export async function dismissNotification(id: number): Promise<APIResponse> {
	return await apiRequest(`/notifications/${id}/dismiss`, APIResponseSchema, 'POST');
}

export async function dismissAllNotifications(): Promise<NotificationsDismissAll | APIResponse> {
	return await apiRequest('/notifications/dismiss-all', NotificationsDismissAllSchema, 'POST');
}

export async function getNotificationTransports(): Promise<NotificationConfig> {
	return await apiRequest('/notifications/transports', NotificationConfigSchema, 'GET');
}

export async function createNotificationTransport(
	payload: NotificationTransportInput
): Promise<NotificationConfig> {
	return await apiRequest('/notifications/transports', NotificationConfigSchema, 'POST', payload);
}

export async function updateNotificationTransport(
	id: number,
	payload: NotificationTransportInput
): Promise<NotificationConfig> {
	return await apiRequest(
		`/notifications/transports/${id}`,
		NotificationConfigSchema,
		'PUT',
		payload
	);
}

export async function deleteNotificationTransport(id: number): Promise<APIResponse> {
	return await apiRequest(`/notifications/transports/${id}`, APIResponseSchema, 'DELETE');
}

export async function testNotificationTransport(id: number): Promise<APIResponse> {
	return await apiRequest(`/notifications/transports/${id}/test`, APIResponseSchema, 'POST');
}

export async function getNotificationRules(): Promise<NotificationRulesConfig> {
	return await apiRequest('/notifications/rules', NotificationRulesConfigSchema, 'GET');
}

export async function updateNotificationRules(
	payload: UpdateNotificationRulesInput
): Promise<NotificationRulesConfig> {
	return await apiRequest('/notifications/rules', NotificationRulesConfigSchema, 'PUT', payload);
}

export async function createNotificationRule(
	payload: CreateNotificationRuleInput
): Promise<NotificationRulesConfig> {
	return await apiRequest('/notifications/rules', NotificationRulesConfigSchema, 'POST', payload);
}

export async function updateNotificationRule(
	id: number,
	payload: UpdateNotificationRuleInput
): Promise<NotificationRulesConfig> {
	return await apiRequest(
		`/notifications/rules/${id}`,
		NotificationRulesConfigSchema,
		'PUT',
		payload
	);
}

export async function deleteNotificationRule(id: number): Promise<NotificationRulesConfig> {
	return await apiRequest(`/notifications/rules/${id}`, NotificationRulesConfigSchema, 'DELETE');
}

export async function bulkDeleteNotificationRules(ids: number[]): Promise<NotificationRulesConfig> {
	return await apiRequest(
		'/notifications/rules/bulk-delete',
		NotificationRulesConfigSchema,
		'POST',
		{ ids }
	);
}

export async function bulkUpdateNotificationRules(
	payload: BulkUpdateRulesInput
): Promise<NotificationRulesConfig> {
	return await apiRequest(
		'/notifications/rules/bulk-update',
		NotificationRulesConfigSchema,
		'POST',
		payload
	);
}

export async function testNotificationRule(payload: {
	templateKey: string;
	targetKey?: string;
	condition?: string;
	severity?: string;
}): Promise<APIResponse> {
	return await apiRequest('/notifications/rules/test', APIResponseSchema, 'POST', payload);
}
