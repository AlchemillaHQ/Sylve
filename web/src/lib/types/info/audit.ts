import { z } from 'zod/v4';

export const AuditRecordSchema = z.object({
    id: z.number(),
    userId: z.number().nullable(),
    user: z.string(),
    authType: z.string(),
    node: z.string(),
    started: z.string(),
    ended: z.string(),
    action: z.preprocess(
        (val) => {
            if (typeof val === 'string') {
                try {
                    return JSON.parse(val);
                } catch {
                    return {};
                }
            }
            return val;
        },
        z.object({
            method: z.string(),
            path: z.string(),
            body: z.any().optional(),
            response: z.any().optional()
        })
    ),
    status: z.string(),
    asyncJobId: z.number().nullable().optional(),
    asyncJobType: z.string().optional(),
    error: z.string().optional(),
    createdAt: z.string(),
    updatedAt: z.string()
});

export type AuditRecord = z.infer<typeof AuditRecordSchema>;
