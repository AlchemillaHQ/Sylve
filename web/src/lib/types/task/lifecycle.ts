import { z } from 'zod/v4';

export const LifecycleTaskSchema = z.object({
    id: z.number().int(),
    guestType: z.enum(['vm', 'jail', 'jail-template', 'vm-template']),
    guestId: z.number().int(),
    action: z.string(),
    source: z.string(),
    status: z.enum(['queued', 'running', 'success', 'failed']),
    requestedBy: z.string().nullable().optional(),
    message: z.string().nullable().optional(),
    error: z.string().nullable().optional(),
    overrideRequested: z.boolean().default(false),
    startedAt: z.string().nullable().optional(),
    finishedAt: z.string().nullable().optional(),
    createdAt: z.string(),
    updatedAt: z.string()
});

export type LifecycleTask = z.infer<typeof LifecycleTaskSchema>;

const activeLifecycleTaskStatuses = new Set<LifecycleTask['status']>(['queued', 'running']);

export function isLifecycleTaskActive(task: LifecycleTask | null | undefined): task is LifecycleTask {
    if (!task) {
        return false;
    }

    if (!activeLifecycleTaskStatuses.has(task.status)) {
        return false;
    }

    return task.action.trim().length > 0;
}
