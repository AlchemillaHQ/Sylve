import { z } from 'zod/v4';

export const VMSnapshotSchema = z.object({
	id: z.number().int(),
	vmId: z.number().int(),
	rid: z.number().int(),
	parentSnapshotId: z.number().int().nullable().default(null),
	name: z.string(),
	description: z.string().default(''),
	snapshotName: z.string(),
	rootDatasets: z.array(z.string()).default([]),
	createdAt: z.string(),
	updatedAt: z.string()
});

export type VMSnapshot = z.infer<typeof VMSnapshotSchema>;

export const VMSnapshotRollbackResultSchema = z.object({
	wasRunning: z.boolean(),
	restarted: z.boolean(),
	warnings: z.array(z.string()).default([])
});

export type VMSnapshotRollbackResult = z.infer<typeof VMSnapshotRollbackResultSchema>;
