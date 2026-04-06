import { z } from 'zod/v4';

export const NetworkObjectType = z.enum([
    'Host',
    'Network',
    'Port',
    'Country',
    'List',
    'Mac',
    'DUID',
    'FQDN'
]);

export const ListsType = z.enum(['firehol', 'cloudflare', 'abusedb']);
export type ListsType = z.infer<typeof ListsType>;

export const NetworkObjectSchema = z.object({
    id: z.number().int(),
    name: z.string(),
    type: NetworkObjectType,
    comment: z.string().optional().default(''),
    autoUpdate: z.boolean().optional().default(true),
    refreshIntervalSeconds: z.number().int().optional().default(300),
    lastRefreshAt: z.string().nullable().optional().default(null),
    lastRefreshError: z.string().optional().default(''),
    createdAt: z.string(),
    updatedAt: z.string(),
    isUsed: z.boolean().optional().default(false),
    isUsedBy: z.string().optional().default(''),
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
                resolvedValue: z.string().optional().default(''),
                createdAt: z.string(),
                updatedAt: z.string()
            })
        )
        .nullable()
});

export type NetworkObject = z.infer<typeof NetworkObjectSchema>;
