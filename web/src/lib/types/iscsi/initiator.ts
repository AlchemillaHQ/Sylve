import { z } from 'zod/v4';

export const ISCSIInitiatorSchema = z.object({
    id: z.number(),
    nickname: z.string(),
    targetAddress: z.string(),
    targetName: z.string(),
    initiatorName: z.string().default(''),
    authMethod: z.string().default('None'),
    chapName: z.string().default(''),
    chapSecret: z.string().default(''),
    tgtChapName: z.string().default(''),
    tgtChapSecret: z.string().default(''),
    createdAt: z.string(),
    updatedAt: z.string()
});

export type ISCSIInitiator = z.infer<typeof ISCSIInitiatorSchema>;

export const ISCSIStatusSchema = z.record(z.string(), z.string());
export type ISCSIStatus = z.infer<typeof ISCSIStatusSchema>;
