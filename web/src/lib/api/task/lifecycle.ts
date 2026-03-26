import type { APIResponse } from '$lib/types/common';
import {
    type LifecycleTask,
    isLifecycleTaskActive,
    LifecycleTaskSchema
} from '$lib/types/task/lifecycle';
import { apiRequest, isAPIResponse } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function getActiveLifecycleTasks(
    guestType?: 'vm' | 'jail' | 'jail-template' | 'vm-template',
    guestId?: number,
    hostname?: string
): Promise<LifecycleTask[] | APIResponse> {
    const params = new URLSearchParams();
    if (guestType) {
        params.set('guestType', guestType);
    }
    if (guestId && guestId > 0) {
        params.set('guestId', String(guestId));
    }

    const query = params.toString();
    const endpoint = query ? `/tasks/lifecycle/active?${query}` : '/tasks/lifecycle/active';
    const result = await apiRequest(endpoint, z.array(LifecycleTaskSchema), 'GET', undefined, {
        hostname
    });

    return result ?? [];
}

export async function getActiveLifecycleTaskForGuest(
    guestType: 'vm' | 'jail' | 'jail-template' | 'vm-template',
    guestId: number,
    hostname?: string
): Promise<LifecycleTask | null | APIResponse> {
    const result = await apiRequest(
        `/tasks/lifecycle/active/${guestType}/${guestId}`,
        LifecycleTaskSchema.nullable(),
        'GET',
        undefined,
        { hostname }
    );
    const task = result ?? null;

    if (isAPIResponse(task) || !isLifecycleTaskActive(task)) {
        return null;
    }

    if (task.guestType !== guestType || task.guestId !== guestId) {
        return null;
    }

    return task;
}

export async function getRecentLifecycleTasks(
    limit: number = 50,
    guestType?: 'vm' | 'jail' | 'jail-template' | 'vm-template',
    guestId?: number,
    hostname?: string
): Promise<LifecycleTask[] | APIResponse> {
    const params = new URLSearchParams();
    params.set('limit', String(limit));
    if (guestType) {
        params.set('guestType', guestType);
    }
    if (guestId && guestId > 0) {
        params.set('guestId', String(guestId));
    }

    const result = await apiRequest(
        `/tasks/lifecycle/recent?${params.toString()}`,
        z.array(LifecycleTaskSchema),
        'GET',
        undefined,
        { hostname }
    );

    return result ?? [];
}
