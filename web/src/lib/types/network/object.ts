import { z } from 'zod/v4';

export const NetworkObjectType = z.enum(['Host', 'Network', 'Port', 'Mac', 'DUID', 'FQDN']);
export const NetworkObjectSchema = z.object({
    id: z.number().int(),
    name: z.string(),
    type: NetworkObjectType,
    comment: z.string().optional().default(''),
    createdAt: z.string(),
    updatedAt: z.string(),
    isUsed: z.boolean().optional().default(false),
    isUsedBy: z.enum(['', 'dhcp']),
    entries: z
        .array(
            z.object({
                id: z.number().int(),
                objectId: z.number().int(),
                value: z.string(),
                createdAt: z.string(),
                updatedAt: z.string()
            })
        )
        .nullable(),
    resolutions: z
        .array(
            z.object({
                id: z.number().int(),
                objectId: z.number().int(),
                resolvedIp: z.string(),
                createdAt: z.string(),
                updatedAt: z.string()
            })
        )
        .nullable()
});

export type NetworkObject = z.infer<typeof NetworkObjectSchema>;
