import { z } from 'zod/v4';

export const ISCSITargetPortalSchema = z.object({
    id: z.number(),
    targetId: z.number(),
    address: z.string(),
    port: z.number().default(3260),
    createdAt: z.string(),
    updatedAt: z.string()
});

export type ISCSITargetPortal = z.infer<typeof ISCSITargetPortalSchema>;

export const ISCSITargetLUNSchema = z.object({
    id: z.number(),
    targetId: z.number(),
    lunNumber: z.number(),
    zvol: z.string(),
    createdAt: z.string(),
    updatedAt: z.string()
});

export type ISCSITargetLUN = z.infer<typeof ISCSITargetLUNSchema>;

export const ISCSITargetSchema = z.object({
    id: z.number(),
    targetName: z.string(),
    alias: z.string().default(''),
    authMethod: z.string().default('None'),
    chapName: z.string().default(''),
    chapSecret: z.string().default(''),
    mutualChapName: z.string().default(''),
    mutualChapSecret: z.string().default(''),
    portals: z.array(ISCSITargetPortalSchema).default([]),
    luns: z.array(ISCSITargetLUNSchema).default([]),
    createdAt: z.string(),
    updatedAt: z.string()
});

export type ISCSITarget = z.infer<typeof ISCSITargetSchema>;
