import { z } from 'zod/v4';

export const BackupTargetSchema = z.object({
    id: z.number(),
    name: z.string(),
    sshHost: z.string(),
    sshPort: z.number().default(22),
    sshKeyPath: z.string().optional().default(''),
    backupRoot: z.string(),
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
    mode: z.enum(['dataset', 'jail']),
    sourceDataset: z.string().optional().default(''),
    jailRootDataset: z.string().optional().default(''),
    friendlySrc: z.string().optional().default(''),
    destSuffix: z.string().optional().default(''),
    pruneKeepLast: z.number().int().nonnegative().default(0),
    pruneTarget: z.boolean().default(false),
    stopBeforeBackup: z.boolean().default(false),
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

export const SnapshotInfoSchema = z.object({
    name: z.string(),
    shortName: z.string(),
    creation: z.string(),
    used: z.string(),
    refer: z.string()
});

export type BackupTarget = z.infer<typeof BackupTargetSchema>;
export type BackupJob = z.infer<typeof BackupJobSchema>;
export type BackupEvent = z.infer<typeof BackupEventSchema>;
export type SnapshotInfo = z.infer<typeof SnapshotInfoSchema>;
