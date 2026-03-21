import { z } from 'zod/v4';

export const JailSnapshotSchema = z.object({
	id: z.number().int(),
	jid: z.number().int(),
	ctId: z.number().int(),
	parentSnapshotId: z.number().int().nullable().default(null),
	name: z.string(),
	description: z.string().default(''),
	snapshotName: z.string(),
	rootDataset: z.string(),
	createdAt: z.string(),
	updatedAt: z.string()
});

export type JailSnapshot = z.infer<typeof JailSnapshotSchema>;
