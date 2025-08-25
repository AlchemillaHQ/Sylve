import { z } from 'zod/v4';

export const DataCenterSchema = z
	.object({
		id: z.number().min(1).optional(),
		raftBootstrap: z.boolean().optional(),
		clusterKey: z.string().min(1).max(100).optional()
	})
	.nullable();

export type DataCenter = z.infer<typeof DataCenterSchema>;
