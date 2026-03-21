import { type LifecycleTask, LifecycleTaskSchema } from '$lib/types/task/lifecycle';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function getActiveLifecycleTasks(
	guestType?: 'vm' | 'jail',
	guestId?: number,
	hostname?: string
): Promise<LifecycleTask[]> {
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
	guestType: 'vm' | 'jail',
	guestId: number,
	hostname?: string
): Promise<LifecycleTask | null> {
	const result = await apiRequest(
		`/tasks/lifecycle/active/${guestType}/${guestId}`,
		LifecycleTaskSchema.nullable(),
		'GET',
		undefined,
		{ hostname }
	);
	return result ?? null;
}

export async function getRecentLifecycleTasks(
	limit: number = 50,
	guestType?: 'vm' | 'jail',
	guestId?: number,
	hostname?: string
): Promise<LifecycleTask[]> {
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
