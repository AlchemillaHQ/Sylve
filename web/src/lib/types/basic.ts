import { z } from 'zod/v4';

export const InitializeSchema = z.array(z.string());

export const BasicSettingsSchema = z.object({
	pools: z.array(z.string()),
	services: z.array(z.string()),
	initialized: z.boolean()
});

export type Initialize = z.infer<typeof InitializeSchema>;
export type BasicSettings = z.infer<typeof BasicSettingsSchema>;
