import { z } from 'zod/v4';
import { ZpoolSchema } from './pool';

export const DatasetSchema = z.object({
	name: z.string(),
	origin: z.string(),
	guid: z.string(),
	used: z.number(),
	available: z.number(),
	recordsize: z.number(),
	mountpoint: z.string(),
	compression: z.string(),
	type: z.string(),
	written: z.number(),
	volsize: z.number(),
	volblocksize: z.number(),
	logicalused: z.number(),
	usedbydataset: z.number(),
	quota: z.number(),
	referenced: z.number(),
	mounted: z.boolean(),
	checksum: z.string(),
	dedup: z.string(),
	aclinherit: z.string(),
	aclmode: z.string(),
	acltype: z.string(),
	primarycache: z.string(),
	volmode: z.string(),
	properties: z
		.object({
			canmount: z.string(),
			casesensitivity: z.string(),
			checksum: z.string(),
			compression: z.string(),
			compressratio: z.coerce.number(),
			context: z.string(),
			copies: z.preprocess((val) => {
				const num = Number(val);
				return isNaN(num) ? null : num;
			}, z.number().nullable()),
			createtxg: z.coerce.number(),
			creation: z.coerce.number(),
			dedup: z.string(),
			defcontext: z.string(),
			devices: z.string(),
			dnodesize: z.string(),
			encryption: z.string(),
			exec: z.string(),
			filesystem_count: z.union([z.number(), z.literal('none')]),
			filesystem_limit: z.union([z.number(), z.literal('none')]),
			fscontext: z.string(),
			guid: z.string(),
			jailed: z.string(),
			keyformat: z.string(),
			keylocation: z.string(),
			logbias: z.string(),
			logicalreferenced: z.coerce.number(),
			mlslabel: z.string(),
			mounted: z.string(),
			mountpoint: z.string(),
			nbmand: z.string(),
			normalization: z.string(),
			objsetid: z.coerce.number(),
			overlay: z.string(),
			pbkdf2iters: z.preprocess((val) => {
				const num = Number(val);
				return isNaN(num) ? null : num;
			}, z.number().nullable()),
			prefetch: z.string(),
			primarycache: z.string(),
			quota: z.preprocess((val) => (val === '-' ? null : val), z.coerce.number().nullable()),
			readonly: z.string(),
			refcompressratio: z.coerce.number(),
			referenced: z.coerce.number(),
			refquota: z.preprocess((val) => (val === '-' ? null : val), z.coerce.number().nullable()),
			refreservation: z.coerce.number(),
			relatime: z.string(),
			reservation: z.coerce.number(),
			rootcontext: z.string(),
			secondarycache: z.string(),
			setuid: z.string(),
			sharenfs: z.string(),
			sharesmb: z.string(),
			snapdev: z.string(),
			snapdir: z.string(),
			snapshot_count: z.union([z.number(), z.literal('none')]),
			snapshot_limit: z.union([z.number(), z.literal('none')]),
			special_small_blocks: z.preprocess(
				(val) => (val === '-' ? null : val),
				z.coerce.number().nullable()
			),
			sync: z.string(),
			type: z.string(),
			used: z.coerce.number(),
			usedbychildren: z.coerce.number(),
			usedbyrefreservation: z.coerce.number(),
			usedbysnapshots: z.coerce.number(),
			utf8only: z.string(),
			version: z.preprocess((val) => (val === '-' ? null : val), z.coerce.number().nullable()),
			volmode: z.string(),
			vscan: z.string(),
			written: z.coerce.number(),
			xattr: z.string()
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
	pool: ZpoolSchema,
	filesystems: z.array(DatasetSchema).default([]),
	snapshots: z.array(DatasetSchema).default([]),
	volumes: z.array(DatasetSchema).default([])
});

export type Dataset = z.infer<typeof DatasetSchema>;
export type GroupedByPool = z.infer<typeof GroupedByPoolSchema>;
export type PeriodicSnapshot = z.infer<typeof PeriodicSnapshotSchema>;
