import { z } from 'zod/v4';

export const NFSConfigSchema = z.object({
	protocol: z.enum(['v3', 'v4', 'v3v4']),
	v4MaxVersion: z.enum(['4.0', '4.1', '4.2']).default('4.2'),
	numericIds: z.boolean().default(false),
	udp: z.boolean().default(false),
	allowNonroot: z.boolean().default(false),
	bindAddresses: z.array(z.string()).default([]),
	mountdPort: z.number().int().default(0),
	minThreads: z.number().int().default(0),
	maxThreads: z.number().int().default(0),
	kerberosPrincipal: z.string().default('')
});
export type NFSConfig = z.infer<typeof NFSConfigSchema>;

export const NFSShareInputSchema = z.object({
	dataset: z.string(),
	description: z.string(),
	enabled: z.boolean(),
	readOnly: z.boolean(),
	everyone: z.boolean(),
	clients: z.array(z.string()),
	maprootUser: z.string().default(''),
	maprootGroup: z.string().default(''),
	mapallUser: z.string().default(''),
	mapallGroup: z.string().default(''),
	allDirs: z.boolean().default(false),
	security: z.array(z.enum(['sys', 'krb5', 'krb5i', 'krb5p'])).default(['sys'])
});
export type NFSShareInput = z.infer<typeof NFSShareInputSchema>;

export const NFSShareSchema = NFSShareInputSchema.extend({
	datasetName: z.string().default(''),
	mountpoint: z.string().default(''),
	id: z.number(),
	createdAt: z.string(),
	updatedAt: z.string()
});
export type NFSShare = z.infer<typeof NFSShareSchema>;

export const NFSDatasetSchema = z.object({
	guid: z.string(),
	name: z.string(),
	mountpoint: z.string(),
	available: z.boolean(),
	reason: z.string()
});
export type NFSDataset = z.infer<typeof NFSDatasetSchema>;

export const NFSStatusSchema = z.object({
	enabled: z.boolean(),
	runtimeAvailable: z.boolean(),
	applyStatus: z.enum(['pending', 'applying', 'starting', 'applied', 'idle', 'stopped']),
	desiredExports: z.number(),
	message: z.string(),
	managed: z.boolean(),
	running: z.boolean().nullable(),
	appliedExports: z.number(),
	policyVerified: z.boolean(),
	lastError: z.string(),
	checkedAt: z.string().nullable().default(null),
	sharesVersion: z.string().default(''),
	configVersion: z.string().default(''),
	filesystems: z
		.array(z.object({ guid: z.string(), name: z.string(), mountpoint: z.string() }))
		.nullable()
		.transform((value) => value ?? [])
		.default([])
});
export type NFSStatus = z.infer<typeof NFSStatusSchema>;

export function nfsShareStatus(share: Pick<NFSShare, 'enabled'>, status: NFSStatus | null) {
	if (!share.enabled) return 'Disabled';
	if (!status) return 'Unknown';
	if (!status.enabled && !status.managed) return 'Inactive';
	if (!status.runtimeAvailable) return 'Unavailable';
	if (status.applyStatus === 'applying' || status.applyStatus === 'starting') return 'Applying';
	if (status.enabled && status.managed && status.running && status.applyStatus === 'applied')
		return 'Applied';
	return 'Pending';
}

export const NFSPreviewSchema = z.object({
	exports: z.string(),
	warnings: z.array(z.string())
});
export type NFSPreview = z.infer<typeof NFSPreviewSchema>;

export const NFSValidationErrorSchema = z.object({ field: z.string(), message: z.string() });

export const NFSMutationSchema = z.object({
	saved: z.boolean(),
	applied: z.boolean(),
	applyStatus: NFSStatusSchema.shape.applyStatus
});
