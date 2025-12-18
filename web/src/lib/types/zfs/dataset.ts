import { z } from 'zod/v4';
import { ZpoolSchema } from './pool';

const zfsProp = () =>
	z
		.union([z.string(), z.object({ value: z.string() })])
		.transform((v) => (typeof v === 'string' ? v : v.value))
		.optional();

export const GZFSDatasetTypeSchema = z.enum(['FILESYSTEM', 'VOLUME', 'SNAPSHOT']);
export const DatasetSchema = z.object({
	name: z.string(),
	guid: z.string(),
	used: z.number(),
	available: z.number(),
	mountpoint: z.string(),
	type: GZFSDatasetTypeSchema,
	referenced: z.number(),
	properties: z
		.object({
			atime: zfsProp(),
			dedup: zfsProp(),
			volmode: zfsProp(),
			origin: zfsProp(),
			recordsize: zfsProp(),
			compression: zfsProp(),
			volsize: zfsProp(),
			volblocksize: zfsProp(),
			quota: zfsProp(),
			written: zfsProp(),
			logicalused: zfsProp(),
			usedbydataset: zfsProp(),
			mounted: zfsProp(),
			checksum: zfsProp(),
			aclmode: zfsProp(),
			aclinherit: zfsProp(),
			primarycache: zfsProp(),
			compressratio: zfsProp(),
			mountpoint: zfsProp()
		})
		.partial()
		.optional()
});

export const PeriodicSnapshotSchema = z.object({
	id: z.number(),
	guid: z.string(),
	prefix: z.string(),
	recursive: z.boolean(),
	interval: z.number(),
	cronExpr: z.string(),

	keepLast: z.number().optional(),
	maxAgeDays: z.number().optional(),

	keepHourly: z.number().optional(),
	keepDaily: z.number().optional(),
	keepWeekly: z.number().optional(),
	keepMonthly: z.number().optional(),
	keepYearly: z.number().optional(),

	createdAt: z.coerce.date().optional(),
	lastRunAt: z.coerce.date()
});

export const GroupedByPoolSchema = z.object({
	name: z.string(),
	pool: z.union([ZpoolSchema, z.string()]),
	filesystems: z.array(DatasetSchema).default([]),
	snapshots: z.array(DatasetSchema).default([]),
	volumes: z.array(DatasetSchema).default([])
});

export type GZFSDatasetType = z.infer<typeof GZFSDatasetTypeSchema>;
export type Dataset = z.infer<typeof DatasetSchema>;
export type GroupedByPool = z.infer<typeof GroupedByPoolSchema>;
export type PeriodicSnapshot = z.infer<typeof PeriodicSnapshotSchema>;
