import { z } from 'zod/v4';

export const IODelaySchema = z.object({
	delay: z.number().default(0)
});

export const IODelayHistoricalSchema = z.array(
	z.object({
		id: z.number().default(0),
		delay: z.number().default(0),
		createdAt: z.string().default('')
	})
);

export const RWSchema = z.object({
	read: z.number(),
	write: z.number()
});

export const VdevDeviceSchema = z.object({
	name: z.string(),
	size: z.number(),
	health: z.string()
});

export const ReplacingVdevDeviceSchema = z.object({
	name: z.string(),
	health: z.string(),
	oldDrive: VdevDeviceSchema,
	newDrive: VdevDeviceSchema
});

export const VdevSchema = z.object({
	name: z.string(),
	alloc: z.number(),
	free: z.number(),
	size: z.number(),
	health: z.string(),
	operations: RWSchema,
	bandwidth: RWSchema,
	devices: z.array(VdevDeviceSchema),
	replacingDevices: z.array(ReplacingVdevDeviceSchema).optional()
});

export const ZpoolDeviceSchema: z.ZodType<any> = z.lazy(() =>
	z.object({
		name: z.string(),
		state: z.string(),
		read: z.number(),
		write: z.number(),
		cksum: z.number(),
		note: z.string(),
		children: z.array(ZpoolDeviceSchema).optional().default([])
	})
);

export const ZpoolStatusSchema = z.object({
	name: z.string(),
	state: z.string(),
	status: z.string(),
	action: z.string(),
	scan: z.string(),
	devices: z.array(ZpoolDeviceSchema).optional().default([]),
	errors: z.string()
});

export const ZpoolSpareSchema = z.object({
	name: z.string(),
	size: z.number(),
	health: z.string()
});

export const ZpoolPropertySourceSchema = z.object({
	type: z.string(),
	data: z.string()
});

export const ZpoolPropertySchema = z.object({
	value: z.string(),
	source: ZpoolPropertySourceSchema
});

export type ZpoolVdev = {
	name: string;
	vdev_type: string;
	guid: string;
	path?: string;
	phys_path?: string | null;
	class: string;
	state: string;
	size: number;
	free: number;
	allocated: number;
	fragmentation?: number;
	properties?: Record<string, z.infer<typeof ZpoolPropertySchema>> | null;
	vdevs?: Record<string, ZpoolVdev> | null;
};

export const ZpoolVdevSchema = z.lazy(() =>
	z.object({
		name: z.string(),
		vdev_type: z.string(),
		guid: z.string(),
		path: z.string().optional(),
		phys_path: z.string().optional().nullable(),
		class: z.string(),
		state: z.string(),
		size: z.number(),
		free: z.number(),
		allocated: z.number(),
		fragmentation: z.number().optional(),
		properties: z.record(z.string(), ZpoolPropertySchema).nullable().optional(),
		vdevs: z.record(z.string(), ZpoolVdevSchema).nullable().optional()
	})
) as unknown as z.ZodType<ZpoolVdev>;

export const ZpoolSchema = z
	.object({
		name: z.string(),
		type: z.string(),
		state: z.string(),
		size: z.number(),
		free: z.number(),
		allocated: z.number(),
		pool_guid: z.string(),
		txg: z.string(),
		spa_version: z.string(),
		zpl_version: z.string(),
		properties: z.record(z.string(), ZpoolPropertySchema),
		vdevs: z.record(z.string(), ZpoolVdevSchema),
		spares: z.record(z.string(), ZpoolVdevSchema).optional().nullable(),
		logs: z.record(z.string(), ZpoolVdevSchema).optional().nullable(),
		l2cache: z.record(z.string(), ZpoolVdevSchema).optional().nullable()
	})
	.transform((data) => ({
		...data,
		guid: data.pool_guid
	}));

export const ZPoolStatusVDEVSchema: z.ZodTypeAny = z.lazy(() =>
	z.object({
		name: z.string().optional(),
		vdev_type: z.string().optional(),
		guid: z.string().optional(),
		path: z.string().nullable().optional(),
		class: z.string().optional(),
		state: z.string().optional(),
		alloc_space: z.union([z.string(), z.number()]).nullable().optional(),
		total_space: z.union([z.string(), z.number()]).nullable().optional(),
		def_space: z.union([z.string(), z.number()]).nullable().optional(),
		rep_dev_size: z.union([z.string(), z.number()]).nullable().optional(),
		read_errors: z.union([z.string(), z.number()]).nullable().optional(),
		write_errors: z.union([z.string(), z.number()]).nullable().optional(),
		checksum_errors: z.union([z.string(), z.number()]).nullable().optional(),
		properties: z.record(z.string(), z.any()).nullable().optional(),
		vdevs: z.union([z.record(z.string(), ZPoolStatusVDEVSchema), z.null()]).optional()
	})
);

export const ZPoolStatusScanStatsSchema = z.object({
	function: z.string().optional(),
	state: z.string().optional(),
	start_time: z.union([z.string(), z.number()]).optional(),
	end_time: z.union([z.string(), z.number()]).optional(),
	to_examine: z.union([z.string(), z.number()]).optional(),
	examined: z.union([z.string(), z.number()]).optional(),
	skipped: z.union([z.string(), z.number()]).optional(),
	processed: z.union([z.string(), z.number()]).optional(),
	errors: z.union([z.string(), z.number()]).optional(),
	bytes_per_scan: z.union([z.string(), z.number()]).optional(),
	pass_start: z.union([z.string(), z.number()]).optional(),
	scrub_pause: z.union([z.string(), z.number()]).optional(),
	scrub_spent_paused: z.union([z.string(), z.number()]).optional(),
	issued_bytes_per_scan: z.union([z.string(), z.number()]).optional(),
	issued: z.union([z.string(), z.number()]).optional()
});

export const ZPoolStatusPoolSchema = z
	.object({
		name: z.string(),
		state: z.string(),
		pool_guid: z.string(),
		txg: z.union([z.string(), z.number()]),
		spa_version: z.union([z.string(), z.number()]),
		zpl_version: z.union([z.string(), z.number()]),
		status: z.string().optional(),
		action: z.string().optional(),
		scan_stats: ZPoolStatusScanStatsSchema.optional().nullable(),
		vdevs: z.record(z.string(), ZPoolStatusVDEVSchema),
		logs: z.record(z.string(), ZPoolStatusVDEVSchema).optional().nullable(),
		spares: z.record(z.string(), ZPoolStatusVDEVSchema).optional().nullable(),
		l2cache: z.record(z.string(), ZPoolStatusVDEVSchema).optional().nullable()
	})
	.loose();

export const CreateVdevSchema = z.object({
	name: z.string(),
	devices: z.array(z.string())
});

export const ZpoolRaidTypeSchema = z.union([
	z.enum(['mirror', 'raidz', 'raidz2', 'raidz3', 'stripe']),
	z.undefined()
]);

export const CreateZpoolSchema = z.object({
	name: z
		.string()
		.min(1, 'Name must be at least 1 character long')
		.max(24, 'Name must be at most 24 characters long')
		.regex(/^[a-zA-Z0-9]+$/, 'Name must be alphanumeric'),
	raidType: ZpoolRaidTypeSchema,
	vdevs: z.array(CreateVdevSchema),
	properties: z.record(z.string(), z.string()).optional(),
	createForce: z.boolean().default(false),
	spares: z.array(z.string()).optional()
});

export const ReplaceDeviceSchema = z.object({
	guid: z.string(),
	old: z.string(),
	new: z.string()
});

export const PoolStatPointSchema = z.object({
	allocated: z.number(),
	free: z.number(),
	size: z.number(),
	dedupRatio: z.number(),
	time: z.number()
});

export const PoolStatPointsSchema = z.record(
	z.string(),
	z
		.array(PoolStatPointSchema)
		.refine((obj) => Object.keys(obj).length > 0, { message: 'No Data Found' })
);

export const PoolStatPointsResponseSchema = z.object({
	poolStatPoint: PoolStatPointsSchema,
	intervalMap: z.array(
		z.object({ value: z.number().transform((v) => v.toString()), label: z.string() })
	)
});

export const PoolsDiskUsageSchema = z.object({
	total: z.number().default(0),
	usage: z.number().default(0)
});

export type IODelay = z.infer<typeof IODelaySchema>;
export type IODelayHistorical = z.infer<typeof IODelayHistoricalSchema>;
export type ZpoolStatusPool = z.infer<typeof ZPoolStatusPoolSchema>;
export type Zpool = z.infer<typeof ZpoolSchema>;
export type ReplaceDevice = z.infer<typeof ReplaceDeviceSchema>;
export type CreateZpool = z.infer<typeof CreateZpoolSchema>;
export type ZpoolRaidType = z.infer<typeof ZpoolRaidTypeSchema>;
export type PoolStatPointsResponse = z.infer<typeof PoolStatPointsResponseSchema>;
export type PoolsDiskUsage = z.infer<typeof PoolsDiskUsageSchema>;

export type ScanStatsRaw = Record<string, any>;
export type ScanSentenceResult = {
	title: string;
	text: string | null;
	progressPercent: number | null;
};
