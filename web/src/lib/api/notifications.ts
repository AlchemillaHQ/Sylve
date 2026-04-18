import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import {
    NotificationConfigSchema,
    NotificationsCountSchema,
    NotificationsListSchema,
    type NotificationConfig,
    type NotificationsCount,
    type NotificationsList,
    type UpdateNotificationConfigInput
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
