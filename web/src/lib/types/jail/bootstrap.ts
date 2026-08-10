import { APIResponseSchema } from '$lib/types/common';
import { z } from 'zod/v4';

export const BootstrapStatusSchema = z.enum([
	'',
	'pending',
	'running',
	'completed',
	'failed',
	'orphaned'
]);

export const BootstrapEntrySchema = z.object({
	pool: z.string(),
	name: z.string(),
	label: z.string(),
	dataset: z.string(),
	mountPoint: z.string(),
	major: z.number().int(),
	minor: z.number().int(),
	type: z.string(),
	exists: z.boolean(),
	status: BootstrapStatusSchema,
	phase: z.string(),
	error: z.string()
});

export const BootstrapCreateResultSchema = z.object({
	pool: z.string(),
	name: z.string(),
	status: z.enum(['pending', 'completed']),
	outcome: z.enum(['queued', 'already_completed'])
});

export const BootstrapDeleteResultSchema = z.object({
	pool: z.string(),
	name: z.string(),
	outcome: z.enum(['deleted', 'already_absent']),
	datasetDeleted: z.boolean(),
	recordDeleted: z.boolean()
});

export const BootstrapCreateResponseSchema = APIResponseSchema.extend({
	data: BootstrapCreateResultSchema
});

export const BootstrapDeleteResponseSchema = APIResponseSchema.extend({
	data: BootstrapDeleteResultSchema
});

export type BootstrapEntry = z.infer<typeof BootstrapEntrySchema>;
export type BootstrapCreateResponse = z.infer<typeof BootstrapCreateResponseSchema>;
export type BootstrapDeleteResponse = z.infer<typeof BootstrapDeleteResponseSchema>;

export interface BootstrapRequest {
	pool: string;
	major: number;
	minor: number;
	type: string;
}

export interface SupportedBootstrapVersion {
	major: number;
	minor: number;
}
