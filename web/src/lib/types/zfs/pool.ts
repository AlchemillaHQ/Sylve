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

export type ZpoolStatusVDEV = {
	name?: string;
	vdev_type?: string;
	guid?: string;
	path?: string | null;
	class?: string;
	state?: string;
	alloc_space?: string | number | null;
	total_space?: string | number | null;
	def_space?: string | number | null;
	rep_dev_size?: string | number | null;
	read_errors?: string | number | null;
	write_errors?: string | number | null;
	checksum_errors?: string | number | null;
	properties?: Record<string, any> | null;
	vdevs?: Record<string, ZpoolStatusVDEV> | null;
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
		fragmentation: z.number().default(0),
		dedup_ratio: z.number().default(1),
		pool_guid: z.string(),
		txg: z.string(),
		spa_version: z.string(),
		zpl_version: z.string(),
		properties: z.record(z.string(), ZpoolPropertySchema),
		vdevs: z.record(z.string(), ZpoolVdevSchema),
		spares: z.record(z.string(), ZpoolVdevSchema).optional().nullable(),
		logs: z.record(z.string(), ZpoolVdevSchema).optional().nullable(),
		l2cache: z.record(z.string(), ZpoolVdevSchema).optional().nullable(),
		special: z.record(z.string(), ZpoolVdevSchema).optional().nullable(),
		dedup: z.record(z.string(), ZpoolVdevSchema).optional().nullable()
	})
	.transform((data) => ({
		...data,
		guid: data.pool_guid,
		dedupRatio: data.dedup_ratio
	}));

export const ZPoolStatusVDEVSchema = z.lazy(() =>
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
		vdevs: z.record(z.string(), ZPoolStatusVDEVSchema).nullable().optional()
	})
) as unknown as z.ZodType<ZpoolStatusVDEV>;

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
		l2cache: z.record(z.string(), ZPoolStatusVDEVSchema).optional().nullable(),
		special: z.record(z.string(), ZPoolStatusVDEVSchema).optional().nullable(),
		dedup: z.record(z.string(), ZPoolStatusVDEVSchema).optional().nullable()
	})
	.loose();

export const ZpoolRaidTypeSchema = z.union([
	z.enum(['mirror', 'raidz', 'raidz2', 'raidz3', 'stripe']),
	z.undefined()
]);

export const VdevTypeSchema = z.enum(['data', 'log', 'cache', 'special', 'dedup']);

export const CreateVdevSchema = z.object({
	name: z.string(),
	devices: z.array(z.string()),
	type: VdevTypeSchema.default('data'),
	raidType: ZpoolRaidTypeSchema
});

export const CreateZpoolSchema = z.object({
	name: z
		.string()
		.min(1, 'Name must be at least 1 character long')
		.max(24, 'Name must be at most 24 characters long')
		.regex(/^[a-zA-Z0-9]+$/, 'Name must be alphanumeric'),
	vdevs: z.array(CreateVdevSchema),
	properties: z.record(z.string(), z.string()).optional(),
	mountpoint: z.string().optional(),
	createForce: z.boolean().default(false),
	spares: z.array(z.string()).optional()
});

export const ReplaceDeviceSchema = z.object({
	guid: z.string(),
	old: z.string(),
	new: z.string()
});

export const PoolStatPointSchema = z.object({
	id: z.number().default(0),
	health: z.string().default('UNKNOWN'),
	worstHealth: z.string().default('UNKNOWN'),
	allocated: z.number(),
	free: z.number(),
	size: z.number(),
	fragmentation: z.number().default(0),
	dedupRatio: z.number(),
	readIOPS: z.number().default(0),
	writeIOPS: z.number().default(0),
	readBytesPerSecond: z.number().default(0),
	writeBytesPerSecond: z.number().default(0),
	readLatencyNanos: z.number().default(0),
	writeLatencyNanos: z.number().default(0),
	maxReadIOPS: z.number().default(0),
	maxWriteIOPS: z.number().default(0),
	maxReadBytesPerSecond: z.number().default(0),
	maxWriteBytesPerSecond: z.number().default(0),
	maxReadLatencyNanos: z.number().default(0),
	maxWriteLatencyNanos: z.number().default(0),
	sampleCount: z.number().default(1),
	intervalSeconds: z.number().default(10),
	time: z.number()
});

export const ZFSDashboardPoolSeriesSchema = z.object({
	guid: z.string(),
	name: z.string(),
	points: z.array(PoolStatPointSchema)
});

export const ZFSDashboardARCPointSchema = z.object({
	id: z.number().default(0),
	time: z.number(),
	size: z.number().default(0),
	targetSize: z.number().default(0),
	minSize: z.number().default(0),
	maxSize: z.number().default(0),
	dataSize: z.number().default(0),
	metadataSize: z.number().default(0),
	otherSize: z.number().default(0),
	headerSize: z.number().default(0),
	compressedSize: z.number().default(0),
	uncompressedSize: z.number().default(0),
	hitRatio: z.number().nullable().default(null),
	demandHitRatio: z.number().nullable().default(null),
	prefetchHitRatio: z.number().nullable().default(null),
	l2HitRatio: z.number().nullable().default(null),
	evictionsPerSecond: z.number().default(0),
	l2ReadBytesPerSecond: z.number().default(0),
	l2WriteBytesPerSecond: z.number().default(0),
	memoryThrottleEvents: z.number().default(0),
	evictNotEnoughEvents: z.number().default(0),
	l2DeviceCount: z.number().default(0),
	l2Size: z.number().default(0),
	l2Allocated: z.number().default(0)
});

export const ZFSDashboardHistorySchema = z.object({
	pools: z.array(ZFSDashboardPoolSeriesSchema),
	arc: z.array(ZFSDashboardARCPointSchema),
	cursors: z.object({
		pool: z.number().default(0),
		arc: z.number().default(0)
	}),
	resolutionSeconds: z.number().default(10),
	generatedAt: z.number(),
	resetRequired: z.boolean().default(false)
});

export const ZFSDashboardIOStatsSchema = z.object({
	sampledAt: z.number().default(0),
	intervalSeconds: z.number().default(10),
	valid: z.boolean().default(false),
	latencyAvailable: z.boolean().default(false),
	readIOPS: z.number().default(0),
	writeIOPS: z.number().default(0),
	readBytesPerSecond: z.number().default(0),
	writeBytesPerSecond: z.number().default(0),
	readLatencyNanos: z.number().nullable().default(null),
	writeLatencyNanos: z.number().nullable().default(null)
});

export const ZFSDashboardPoolErrorsSchema = z.object({
	read: z.number().default(0),
	write: z.number().default(0),
	checksum: z.number().default(0),
	scan: z.number().default(0)
});

export const ZFSDashboardPoolScanSchema = z.object({
	function: z.string(),
	state: z.string(),
	startTime: z.string().default(''),
	endTime: z.string().default(''),
	examined: z.number().default(0),
	toExamine: z.number().default(0),
	issued: z.number().default(0),
	processed: z.number().default(0),
	errors: z.number().default(0),
	progressPercent: z.number().nullable().default(null)
});

export const ZFSDashboardPoolTopologySchema = z.object({
	dataVdevs: z.number().default(0),
	disks: z.number().default(0),
	logs: z.number().default(0),
	cache: z.number().default(0),
	spares: z.number().default(0),
	special: z.number().default(0),
	dedup: z.number().default(0)
});

export const ZFSDashboardPoolSnapshotSchema = z.object({
	guid: z.string(),
	name: z.string(),
	state: z.string(),
	size: z.number(),
	allocated: z.number(),
	free: z.number(),
	fragmentation: z.number().default(0),
	dedupRatio: z.number().default(1),
	statusAvailable: z.boolean().default(false),
	status: z.string().default(''),
	action: z.string().default(''),
	errors: ZFSDashboardPoolErrorsSchema,
	scan: ZFSDashboardPoolScanSchema.nullable().default(null),
	topology: ZFSDashboardPoolTopologySchema,
	io: ZFSDashboardIOStatsSchema
});

export const ZFSDashboardSnapshotSchema = z.object({
	pools: z.array(ZFSDashboardPoolSnapshotSchema),
	arc: ZFSDashboardARCPointSchema.nullable().default(null),
	sampledAt: z.number().default(0),
	generatedAt: z.number(),
	stale: z.boolean().default(false)
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
export type VdevType = z.infer<typeof VdevTypeSchema>;
export type PoolStatPoint = z.infer<typeof PoolStatPointSchema>;
export type ZFSDashboardPoolSeries = z.infer<typeof ZFSDashboardPoolSeriesSchema>;
export type ZFSDashboardARCPoint = z.infer<typeof ZFSDashboardARCPointSchema>;
export type ZFSDashboardHistory = z.infer<typeof ZFSDashboardHistorySchema>;
export type ZFSDashboardIOStats = z.infer<typeof ZFSDashboardIOStatsSchema>;
export type ZFSDashboardPoolScan = z.infer<typeof ZFSDashboardPoolScanSchema>;
export type ZFSDashboardPoolSnapshot = z.infer<typeof ZFSDashboardPoolSnapshotSchema>;
export type ZFSDashboardSnapshot = z.infer<typeof ZFSDashboardSnapshotSchema>;
export type PoolsDiskUsage = z.infer<typeof PoolsDiskUsageSchema>;

export type ScanStatsRaw = Record<string, any>;
export type ScanSentenceResult = {
	title: string;
	text: string | null;
	progressPercent: number | null;
};

export type ZpoolStatusScanStats = z.infer<typeof ZPoolStatusScanStatsSchema>;
