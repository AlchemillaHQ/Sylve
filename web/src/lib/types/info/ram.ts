import { z } from 'zod/v4';

export const RAMInfoHistoricalSchema = z.array(
	z.object({
		id: z.number().default(0),
		usage: z.number().default(0),
		createdAt: z.string().default('')
	})
);

export const RAMInfoSchema = z.object({
	total: z.number().default(0),
	free: z.number().default(0),
	usedPercent: z.number().default(0)
});

export type RAMInfo = z.infer<typeof RAMInfoSchema>;
export type RAMInfoHistorical = z.infer<typeof RAMInfoHistoricalSchema>;
