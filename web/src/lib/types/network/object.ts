import { z } from 'zod/v4';

export const NetworkObjectType = z.enum(['Host', 'Network', 'Port', 'MAC']);
export const NetworkObjectSchema = z.object({
	id: z.number().int(),
	name: z.string(),
	type: NetworkObjectType,
	comment: z.string().optional().default(''),
	createdAt: z.string(),
	updatedAt: z.string(),
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
		.optional(),
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
		.optional()
});

export type NetworkObject = z.infer<typeof NetworkObjectSchema>;
