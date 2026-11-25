import { z } from 'zod/v4';
import { ZpoolSchema } from './pool';

const zfsProp = () =>
	z
		.union([z.string(), z.object({ value: z.string() })])
		.transform((v) => (typeof v === 'string' ? v : v.value))
		.optional();

export const GZFSDatasetTypeSchema = z.enum(['FILESYSTEM', 'VOLUME', 'SNAPSHOT', 'ALL']);
export const DatasetSchema = z.object({
	name: z.string(),
	guid: z.string(),
	used: z.number(),
	pool: z.string(),
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
			mountpoint: zfsProp(),
			encryption: zfsProp(),
			refreservation: zfsProp(),
			readonly: zfsProp()
		})
		.partial()
		.optional()
});

export const DatasetPropertyDetailSchema = z.union([
	z.object({
		value: z.string(),
		source: z.object({ type: z.string(), data: z.string() }).optional()
	}),
	z
		.string()
		.transform((value): { value: string; source?: { type: string; data: string } } => ({ value }))
]);

export const DetailedDatasetSchema = z.preprocess(
	(raw) =>
		raw && typeof raw === 'object' && !('propertyDetails' in raw)
			? { ...raw, propertyDetails: (raw as { properties?: unknown }).properties }
			: raw,
	DatasetSchema.extend({
		propertyDetails: z.record(z.string(), DatasetPropertyDetailSchema).optional()
	})
);

export const SnapshotSummarySchema = z.object({
	name: z.string(),
	dataset: z.string(),
	snapshot: z.string(),
	pool: z.string(),
	guid: z.string(),
	createdAt: z.coerce.date(),
	createTxg: z
		.number()
		.nullish()
		.transform((txg) => txg ?? 0),
	used: z.number(),
	referenced: z.number(),
	clones: z
		.array(z.string())
		.nullish()
		.transform((clones) => clones ?? []),
	holds: z
		.number()
		.nullish()
		.transform((holds) => holds ?? 0)
});

export const PeriodicSnapshotSchema = z.object({
	id: z.number(),
	guid: z.string(),
	prefix: z.string(),
	recursive: z.boolean(),
	interval: z.number(),
	cronExpr: z.string(),
	pool: z.string(),

	keepLast: z.number().optional(),
	maxAgeDays: z.number().optional(),

	keepHourly: z.number().optional(),
	keepDaily: z.number().optional(),
	keepWeekly: z.number().optional(),
	keepMonthly: z.number().optional(),
	keepYearly: z.number().optional(),

	createdAt: z.coerce.date().optional(),
	lastRunAt: z.coerce.date(),
	lastExecutedAt: z.coerce.date().nullable().optional(),
	nextRunAt: z.coerce.date().nullable().optional()
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
export type DetailedDataset = z.infer<typeof DetailedDatasetSchema>;
export type DatasetPropertyDetail = z.infer<typeof DatasetPropertyDetailSchema>;
export type GroupedByPool = z.infer<typeof GroupedByPoolSchema>;
export type PeriodicSnapshot = z.infer<typeof PeriodicSnapshotSchema>;
export type SnapshotSummary = z.infer<typeof SnapshotSummarySchema>;
