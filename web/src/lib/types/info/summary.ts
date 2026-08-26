import { z } from 'zod/v4';

const SummaryUsagePointSchema = z.object({
	id: z.number().int().nonnegative().default(0),
	usage: z.number().default(0),
	createdAt: z.string().default('')
});

export const SummaryHistoryNetworkPointSchema = z.object({
	id: z.number().int().nonnegative().default(0),
	receivedBytes: z.number().int().default(0),
	sentBytes: z.number().int().default(0),
	createdAt: z.string().default('')
});

export const SummaryHistoryCursorsSchema = z.object({
	cpu: z.number().int().nonnegative().default(0),
	ram: z.number().int().nonnegative().default(0),
	network: z.number().int().nonnegative().default(0)
});

export const NodeSummaryHistorySchema = z.object({
	cpu: z.array(SummaryUsagePointSchema).default([]),
	ram: z.array(SummaryUsagePointSchema).default([]),
	network: z.array(SummaryHistoryNetworkPointSchema).default([]),
	cursors: SummaryHistoryCursorsSchema
});

export type SummaryHistoryCursors = z.infer<typeof SummaryHistoryCursorsSchema>;
export type SummaryHistoryNetworkPoint = z.infer<typeof SummaryHistoryNetworkPointSchema>;
export type NodeSummaryHistory = z.infer<typeof NodeSummaryHistorySchema>;
