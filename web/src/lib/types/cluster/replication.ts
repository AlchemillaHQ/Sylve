import { z } from 'zod/v4';

export const ReplicationGuestTypeSchema = z.enum(['vm', 'jail']);
export const ReplicationSourceModeSchema = z.enum(['follow_active', 'pinned_primary']);
export const ReplicationFailbackModeSchema = z.enum(['manual', 'auto']);
export const ReplicationFailoverModeSchema = z.enum(['manual', 'auto_safe', 'auto_force']);

export const ReplicationPolicyTargetSchema = z.object({
	id: z.number().int(),
	policyId: z.number().int(),
	nodeId: z.string(),
	weight: z.number().int().default(100),
	ready: z.boolean().optional().default(false),
	generationId: z.string().optional().default(''),
	ownerEpoch: z.number().int().optional().default(0),
	manifestHash: z.string().optional().default(''),
	requiredDatasetCount: z.number().int().optional().default(0),
	completedDatasetCount: z.number().int().optional().default(0),
	lastVerifiedAt: z.string().nullable().optional(),
	readyUntil: z.string().nullable().optional(),
	lastError: z.string().optional().default(''),
	createdAt: z.string().optional(),
	updatedAt: z.string().optional()
});

export const ReplicationPolicySchema = z.object({
	id: z.number().int(),
	name: z.string(),
	description: z.string().optional().default(''),
	guestType: ReplicationGuestTypeSchema,
	guestId: z.number().int(),
	sourceNodeId: z.string().optional().default(''),
	activeNodeId: z.string().optional().default(''),
	sourceMode: ReplicationSourceModeSchema.default('follow_active'),
	failbackMode: ReplicationFailbackModeSchema.default('manual'),
	failoverMode: ReplicationFailoverModeSchema.default('manual'),
	cronExpr: z.string().default(''),
	enabled: z.boolean().default(true),
	crashRecovery: z.boolean().optional().default(true),
	crashRestartMax: z.number().int().optional().default(3),
	poolHealthCheck: z.boolean().optional().default(true),
	poolCapacityPct: z.number().int().optional().default(90),
	protectionState: z.string().optional().default(''),
	lastRunAt: z.string().nullable().optional(),
	nextRunAt: z.string().nullable().optional(),
	lastStatus: z.string().optional().default(''),
	lastError: z.string().optional().default(''),
	haEligible: z.boolean().optional().default(false),
	haDegraded: z.boolean().optional().default(false),
	haReasons: z.array(z.string()).optional().default([]),
	targets: z.array(ReplicationPolicyTargetSchema).default([]),
	ownerEpoch: z.number().optional().default(1),
	transitionState: z.string().optional().default('none'),
	transitionRunId: z.string().optional().default(''),
	transitionReason: z.string().optional().default(''),
	transitionSourceNodeId: z.string().optional().default(''),
	transitionTargetNodeId: z.string().optional().default(''),
	transitionOwnerEpoch: z.number().optional().default(0),
	transitionRequestedAt: z.string().nullable().optional(),
	transitionDemotedAt: z.string().nullable().optional(),
	transitionCatchupAt: z.string().nullable().optional(),
	transitionPromotedAt: z.string().nullable().optional(),
	transitionCompletedAt: z.string().nullable().optional(),
	transitionError: z.string().optional().default(''),
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
export type ReplicationFailoverMode = z.infer<typeof ReplicationFailoverModeSchema>;
export type ReplicationPolicyTarget = z.infer<typeof ReplicationPolicyTargetSchema>;
export type ReplicationPolicy = z.infer<typeof ReplicationPolicySchema>;
export type ReplicationEvent = z.infer<typeof ReplicationEventSchema>;
export type ReplicationEventProgress = z.infer<typeof ReplicationEventProgressSchema>;
