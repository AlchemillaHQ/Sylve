import { z } from 'zod/v4';

export const BackupTargetSchema = z.object({
	id: z.number(),
	name: z.string(),
	endpoint: z.string(),
	description: z.string().optional().default(''),
	enabled: z.boolean().default(true),
	createdAt: z.string().optional(),
	updatedAt: z.string().optional()
});

export const BackupJobSchema = z.object({
	id: z.number(),
	name: z.string(),
	targetId: z.number(),
	target: BackupTargetSchema.optional(),
	runnerNodeId: z.string().optional().default(''),
	mode: z.string(),
	sourceDataset: z.string().optional().default(''),
	jailRootDataset: z.string().optional().default(''),
	destinationDataset: z.string(),
	cronExpr: z.string(),
	force: z.boolean().default(false),
	withIntermediates: z.boolean().default(false),
	enabled: z.boolean().default(true),
	lastRunAt: z.string().nullable().optional(),
	nextRunAt: z.string().nullable().optional(),
	lastStatus: z.string().optional().default(''),
	lastError: z.string().optional().default(''),
	createdAt: z.string().optional(),
	updatedAt: z.string().optional()
});

export const BackupDatasetSchema = z.object({
	name: z.string(),
	guid: z.string(),
	type: z.string(),
	creationUnix: z.number(),
	usedBytes: z.number(),
	referencedBytes: z.number(),
	availableBytes: z.number(),
	mountpoint: z.string().optional().default('')
});

export const BackupEventSchema = z.object({
	id: z.number(),
	jobId: z.number().nullable().optional(),
	direction: z.string(),
	remoteAddress: z.string().optional().default(''),
	sourceDataset: z.string().optional().default(''),
	destinationDataset: z.string().optional().default(''),
	baseSnapshot: z.string().optional().default(''),
	targetSnapshot: z.string().optional().default(''),
	mode: z.string().optional().default(''),
	status: z.string().optional().default(''),
	error: z.string().optional().default(''),
	startedAt: z.string(),
	completedAt: z.string().nullable().optional()
});

export const BackupPlanSchema = z.object({
	mode: z.string(),
	baseSnapshot: z.string().optional().default(''),
	targetSnapshot: z.string().optional().default(''),
	sourceDataset: z.string(),
	destinationDataset: z.string(),
	endpoint: z.string(),
	noop: z.boolean().default(false)
});

export type BackupTarget = z.infer<typeof BackupTargetSchema>;
export type BackupJob = z.infer<typeof BackupJobSchema>;
export type BackupDataset = z.infer<typeof BackupDatasetSchema>;
export type BackupEvent = z.infer<typeof BackupEventSchema>;
export type BackupPlan = z.infer<typeof BackupPlanSchema>;
