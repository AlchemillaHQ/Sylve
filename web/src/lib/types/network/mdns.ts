import { z } from 'zod/v4';

export const MdnsSettingsSchema = z.object({
    id: z.number(),
    interfaces: z.string().default(''),
    hostname: z.string().default('')
});

export type MdnsSettings = z.infer<typeof MdnsSettingsSchema>;

export const MdnsRecordSchema = z.object({
    id: z.number(),
    name: z.string(),
    type: z.string(),
    port: z.number(),
    txt: z.record(z.string(), z.string()).nullish().transform((value) => value ?? {}),
    interfaces: z.string().default(''),
    createdAt: z.string(),
    updatedAt: z.string()
});

export type MdnsRecord = z.infer<typeof MdnsRecordSchema>;

export const MdnsRecordWithManagedSchema = MdnsRecordSchema.extend({
    managed: z.boolean(),
    source: z.string()
});

export type MdnsRecordWithManaged = z.infer<typeof MdnsRecordWithManagedSchema>;
