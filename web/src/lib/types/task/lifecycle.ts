import { z } from 'zod/v4';

export const LifecycleTaskSchema = z.object({
	id: z.number().int(),
	guestType: z.enum(['vm', 'jail']),
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
