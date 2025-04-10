import { z } from 'zod';

export const DatasetSchema = z.object({
	name: z.string(),
	origin: z.string(),
	used: z.number(),
	avail: z.number(),
	mountpoint: z.string(),
	compression: z.string(),
	type: z.string(),
	written: z.number(),
	volsize: z.number(),
	logicalused: z.number(),
	usedbydataset: z.number(),
	quota: z.number(),
	referenced: z.number(),
	properties: z
		.object({
			aclinherit: z.string(),
			aclmode: z.string(),
			acltype: z.string(),
			available: z.coerce.number(),
			canmount: z.string(),
			casesensitivity: z.string(),
			checksum: z.string(),
			compression: z.string(),
			compressratio: z.coerce.number(),
			context: z.string(),
			copies: z.coerce.number(),
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
			logicalused: z.coerce.number(),
			mlslabel: z.string(),
			mounted: z.string(),
			mountpoint: z.string(),
			nbmand: z.string(),
			normalization: z.string(),
			objsetid: z.coerce.number(),
			overlay: z.string(),
			pbkdf2iters: z.coerce.number(),
			prefetch: z.string(),
			primarycache: z.string(),
			quota: z.coerce.number(),
			readonly: z.string(),
			recordsize: z.coerce.number(),
			refcompressratio: z.coerce.number(),
			referenced: z.coerce.number(),
			refquota: z.coerce.number(),
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
			special_small_blocks: z.coerce.number(),
			sync: z.string(),
			type: z.string(),
			used: z.coerce.number(),
			usedbychildren: z.coerce.number(),
			usedbydataset: z.coerce.number(),
			usedbyrefreservation: z.coerce.number(),
			usedbysnapshots: z.coerce.number(),
			utf8only: z.string(),
			version: z.coerce.number(),
			volmode: z.string(),
			vscan: z.string(),
			written: z.coerce.number(),
			xattr: z.string()
		})
		.partial()
});

export const GroupedByPoolSchema = z.object({
	name: z.string(),
	filesystems: z.array(DatasetSchema).default([]),
	volumes: z.array(DatasetSchema).default([])
});

export type Dataset = z.infer<typeof DatasetSchema>;
export type GroupedByPool = z.infer<typeof GroupedByPoolSchema>;
