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

export async function getNotificationConfig(): Promise<NotificationConfig> {
    return await apiRequest('/notifications/config', NotificationConfigSchema, 'GET');
}

export async function updateNotificationConfig(
    payload: UpdateNotificationConfigInput
): Promise<NotificationConfig> {
    return await apiRequest('/notifications/config', NotificationConfigSchema, 'PUT', payload);
}
