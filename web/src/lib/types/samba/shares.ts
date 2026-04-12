import { z } from 'zod/v4';
import { GroupSchema, UserSchema } from '../auth';

export const SambaPrincipalUserSchema = UserSchema.pick({
    id: true,
    username: true
});

export const SambaPrincipalGroupSchema = GroupSchema.pick({
    id: true,
    name: true
});

export const SambaPrincipalSetSchema = z.object({
    users: z.array(SambaPrincipalUserSchema).default([]),
    groups: z.array(SambaPrincipalGroupSchema).default([])
});

export const SambaPermissionsSchema = z.object({
    read: SambaPrincipalSetSchema,
    write: SambaPrincipalSetSchema
});

export const SambaGuestSchema = z.object({
    enabled: z.boolean(),
    writeable: z.boolean()
});

export const SambaShareSchema = z.object({
    id: z.number(),
    name: z.string(),
    dataset: z.string(),
    permissions: SambaPermissionsSchema,
    guest: SambaGuestSchema,
    createMask: z.string(),
    directoryMask: z.string(),
    timeMachine: z.boolean().default(false),
    timeMachineMaxSize: z.number().default(0),
    createdAt: z.string(),
    updatedAt: z.string()
});

export type SambaShare = z.infer<typeof SambaShareSchema>;
