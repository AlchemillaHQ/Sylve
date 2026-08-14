import z from 'zod/v4';

export const CloudInitTemplateSchema = z.object({
	id: z.number().int().positive(),
	name: z.string(),
	user: z.string(),
	meta: z.string(),
	networkConfig: z.string(),
	createdAt: z.string(),
	updatedAt: z.string()
});

export const CloudInitTemplateInputSchema = CloudInitTemplateSchema.pick({
	name: true,
	user: true,
	meta: true,
	networkConfig: true
});

export const CloudInitTemplateIdentitySchema = CloudInitTemplateSchema.pick({
	id: true,
	name: true
});

export type CloudInitTemplate = z.infer<typeof CloudInitTemplateSchema>;
export type CloudInitTemplateInput = z.infer<typeof CloudInitTemplateInputSchema>;
export type CloudInitTemplateIdentity = z.infer<typeof CloudInitTemplateIdentitySchema>;
