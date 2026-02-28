import { z } from 'zod/v4';

export const ReplicationGuestTypeSchema = z.enum(['vm', 'jail']);
export const ReplicationSourceModeSchema = z.enum(['follow_active', 'pinned_primary']);
export const ReplicationFailbackModeSchema = z.enum(['manual', 'auto']);

export const ReplicationPolicyTargetSchema = z.object({
	id: z.number().int(),
	policyId: z.number().int(),
	nodeId: z.string(),
	weight: z.number().int().default(100),
	createdAt: z.string().optional(),
	updatedAt: z.string().optional()
});

export const ReplicationPolicySchema = z.object({
	id: z.number().int(),
	name: z.string(),
	guestType: ReplicationGuestTypeSchema,
	guestId: z.number().int(),
	sourceNodeId: z.string().optional().default(''),
	activeNodeId: z.string().optional().default(''),
	sourceMode: ReplicationSourceModeSchema.default('follow_active'),
	failbackMode: ReplicationFailbackModeSchema.default('manual'),
	cronExpr: z.string().default(''),
	enabled: z.boolean().default(true),
	lastRunAt: z.string().nullable().optional(),
	nextRunAt: z.string().nullable().optional(),
	lastStatus: z.string().optional().default(''),
	lastError: z.string().optional().default(''),
	targets: z.array(ReplicationPolicyTargetSchema).default([]),
	createdAt: z.string().optional(),
	updatedAt: z.string().optional()
});

export const ReplicationEventSchema = z.object({
	id: z.number().int(),
	policyId: z.number().int().nullable().optional(),
	eventType: z.string(),
	status: z.string(),
	message: z.string().optional().default(''),
	error: z.string().optional().default(''),
	output: z.string().optional().default(''),
	sourceNodeId: z.string().optional().default(''),
	targetNodeId: z.string().optional().default(''),
	guestType: ReplicationGuestTypeSchema.optional(),
	guestId: z.number().int().optional(),
	startedAt: z.string(),
	completedAt: z.string().nullable().optional(),
	createdAt: z.string().optional(),
	updatedAt: z.string().optional()
});

export const ReplicationEventProgressSchema = z.object({
	event: ReplicationEventSchema,
	movedBytes: z.number().nullable().optional(),
	totalBytes: z.number().nullable().optional(),
	progressPercent: z.number().nullable().optional()
});

export type ReplicationGuestType = z.infer<typeof ReplicationGuestTypeSchema>;
export type ReplicationSourceMode = z.infer<typeof ReplicationSourceModeSchema>;
export type ReplicationFailbackMode = z.infer<typeof ReplicationFailbackModeSchema>;
export type ReplicationPolicyTarget = z.infer<typeof ReplicationPolicyTargetSchema>;
export type ReplicationPolicy = z.infer<typeof ReplicationPolicySchema>;
export type ReplicationEvent = z.infer<typeof ReplicationEventSchema>;
export type ReplicationEventProgress = z.infer<typeof ReplicationEventProgressSchema>;
