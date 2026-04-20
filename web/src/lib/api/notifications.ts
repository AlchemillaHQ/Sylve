import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
	NotificationConfigSchema,
	NotificationRulesConfigSchema,
	NotificationsCountSchema,
	NotificationsListSchema,
	type NotificationConfig,
	type NotificationRulesConfig,
	type NotificationsCount,
	type NotificationsList,
	type CreateNotificationRuleInput,
	type UpdateNotificationConfigInput,
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

export async function getNotificationTransports(): Promise<NotificationConfig> {
	return await apiRequest('/notifications/transports', NotificationConfigSchema, 'GET');
}

export async function updateNotificationTransports(
	payload: UpdateNotificationConfigInput
): Promise<NotificationConfig> {
	return await apiRequest('/notifications/transports', NotificationConfigSchema, 'PUT', payload);
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
	return await apiRequest(`/notifications/rules/${id}`, NotificationRulesConfigSchema, 'PUT', payload);
}

export async function deleteNotificationRule(id: number): Promise<NotificationRulesConfig> {
	return await apiRequest(`/notifications/rules/${id}`, NotificationRulesConfigSchema, 'DELETE');
}
