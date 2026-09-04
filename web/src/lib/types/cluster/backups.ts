import { z } from 'zod/v4';

export const BackupTargetNodeReadinessSchema = z.object({
	targetId: z.number(),
	nodeId: z.string(),
	validationSucceeded: z.boolean().default(false),
	lastVerifiedAt: z.string().nullable().optional(),
	readyUntil: z.string().nullable().optional(),
	lastError: z.string().optional().default(''),
	revision: z.number().int().nonnegative().default(0),
	ready: z.boolean().default(false),
	currentVoter: z.boolean().default(false),
	expired: z.boolean().default(false),
	configurationCurrent: z.boolean().default(true)
});

export const BackupTargetSchema = z.object({
	id: z.number(),
	name: z.string(),
	sshHost: z.string(),
	sshPort: z.number().default(22),
	sshKeyPath: z.string().optional().default(''),
	backupRoot: z.string(),
	createBackupRoot: z.boolean().default(false),
	description: z.string().optional().default(''),
	enabled: z.boolean().default(true),
	readiness: z.array(BackupTargetNodeReadinessSchema).optional().default([]),
	createdAt: z.string().optional(),
	updatedAt: z.string().optional()
});

export const BackupJobSchema = z.object({
	id: z.number(),
	name: z.string(),
	targetId: z.number(),
	target: BackupTargetSchema.optional(),
	runnerNodeId: z.string().optional().default(''),
	mode: z.enum(['dataset', 'jail', 'vm']),
	sourceDataset: z.string().optional().default(''),
	jailRootDataset: z.string().optional().default(''),
	friendlySrc: z.string().optional().default(''),
	destSuffix: z.string().optional().default(''),
	pruneKeepLast: z.number().int().nonnegative().default(0),
	pruneTarget: z.boolean().default(false),
	stopBeforeBackup: z.boolean().default(false),
	recursive: z.boolean().default(false),
	encrypted: z.boolean().default(false),
	cronExpr: z.string(),
	enabled: z.boolean().default(true),
	lastRunAt: z.string().nullable().optional(),
	nextRunAt: z.string().nullable().optional(),
	lastStatus: z.string().optional().default(''),
	lastError: z.string().optional().default(''),
	createdAt: z.string().optional(),
	updatedAt: z.string().optional()
});

export const BackupEventSchema = z.object({
	id: z.number(),
	jobId: z.number().nullable().optional(),
	sourceDataset: z.string().optional().default(''),
	targetEndpoint: z.string().optional().default(''),
	mode: z.string().optional().default(''),
	status: z.string().optional().default(''),
	error: z.string().optional().default(''),
	output: z.string().optional().default(''),
	startedAt: z.string(),
	completedAt: z.string().nullable().optional()
});

export const BackupEventProgressSchema = z.object({
	event: BackupEventSchema,
	progressDataset: z.string().optional().default(''),
	phase: z.string().optional().default(''),
	movedBytes: z.number().nullable().optional(),
	totalBytes: z.number().nullable().optional(),
	progressPercent: z.number().nullable().optional()
});

export const SnapshotInfoSchema = z.object({
	name: z.string(),
	shortName: z.string(),
	dataset: z.string().optional().default(''),
	encrypted: z.boolean().default(false),
	creation: z.string(),
	used: z.string(),
	refer: z.string(),
	lineage: z.enum(['active', 'rotated', 'preserved', 'other']).optional().default('active'),
	outOfBand: z.boolean().optional().default(false),
	committed: z.boolean().optional().default(false),
	legacy: z.boolean().optional().default(false),
	childCount: z.number().int().nonnegative().default(0)
});

export const SnapshotPageSchema = z.object({
	items: z.array(SnapshotInfoSchema).default([]),
	nextCursor: z.string().optional().default(''),
	hasMore: z.boolean().default(false)
});

export const BackupTargetDatasetInfoSchema = z.object({
	name: z.string(),
	encrypted: z.boolean().default(false),
	suffix: z.string().default(''),
	baseSuffix: z.string().optional().default(''),
	lineage: z.enum(['active', 'rotated', 'preserved', 'other']).optional().default('active'),
	outOfBand: z.boolean().optional().default(false),
	snapshotCount: z.number().int().nonnegative().default(0),
	snapshotCountKnown: z.boolean().optional().default(true),
	kind: z.enum(['dataset', 'jail', 'vm']).default('dataset'),
	jailCtId: z.number().int().nonnegative().optional(),
	vmRid: z.number().int().nonnegative().optional()
});

export const BackupJailMetadataInfoSchema = z.object({
	ctId: z.number().int().nonnegative(),
	name: z.string().default(''),
	basePool: z.string().default('')
});

export const BackupVMMetadataInfoSchema = z.object({
	rid: z.number().int().nonnegative(),
	name: z.string().default(''),
	pools: z.array(z.string()).default([])
});

export type BackupTargetNodeReadiness = z.infer<typeof BackupTargetNodeReadinessSchema>;
export type BackupTarget = z.infer<typeof BackupTargetSchema>;
export type BackupJob = z.infer<typeof BackupJobSchema>;
export type BackupEvent = z.infer<typeof BackupEventSchema>;
export type BackupEventProgress = z.infer<typeof BackupEventProgressSchema>;
export type SnapshotInfo = z.infer<typeof SnapshotInfoSchema>;
export type SnapshotPage = z.infer<typeof SnapshotPageSchema>;
export type BackupTargetDatasetInfo = z.infer<typeof BackupTargetDatasetInfoSchema>;
export type BackupJailMetadataInfo = z.infer<typeof BackupJailMetadataInfoSchema>;
export type BackupVMMetadataInfo = z.infer<typeof BackupVMMetadataInfoSchema>;
export type BackupJobMode = BackupJob['mode'];
export type BackupGuestKind = 'dataset' | 'jail' | 'vm';
export type BackupScopedGuestKind = Exclude<BackupGuestKind, 'dataset'>;
export type BackupSnapshotLineageMarker = 'CURR' | 'OOB' | 'INT';

export interface BackupGuestRef {
	kind: BackupGuestKind;
	id: number;
}

export interface BackupGuestFilter {
	kind: BackupScopedGuestKind;
	id: number;
}

export interface BackupGuestScope extends BackupGuestFilter {
	hostname: string;
}

export interface BackupRestoreGenerationOption {
	value: string;
	label: string;
}

export interface RestoreTargetDatasetGroup {
	baseSuffix: string;
	label: string;
	jobLabel: string;
	representativeDataset: string;
	kind: BackupGuestKind;
	jailCtId: number;
	vmRid: number;
	totalSnapshots: number;
	snapshotCountKnown: boolean;
	encrypted: boolean;
}
