import { z } from 'zod/v4';

export const MigrateRequestSchema = z.object({
    targetNodeUuid: z.string().min(1)
});

export const ValidateResultSchema = z.object({
    allowed: z.boolean(),
    reasons: z.array(z.string()).catch([]),
    warnings: z.array(z.string()).catch([])
});

export const MigrationTaskResponseSchema = z.object({
    taskId: z.number().int(),
    guestId: z.number().int(),
    outcome: z.string().optional()
});

export type MigrateRequest = z.infer<typeof MigrateRequestSchema>;
export type ValidateResult = z.infer<typeof ValidateResultSchema>;
export type MigrationTaskResponse = z.infer<typeof MigrationTaskResponseSchema>;
