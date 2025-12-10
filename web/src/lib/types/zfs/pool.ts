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

/* 
type ZPoolVDEV struct {
	Name     string `json:"name"`
	VdevType string `json:"vdev_type"`
	GUID     string `json:"guid"`
	Path     string `json:"path"`
	PhysPath string `json:"phys_path"`
	Class    string `json:"class"`
	State    string `json:"state"`

	Size          uint64  `json:"size"`
	Free          uint64  `json:"free"`
	Alloc         uint64  `json:"allocated"`
	Fragmentation float64 `json:"fragmentation"`

	Properties map[string]ZFSProperty `json:"properties"`
	Vdevs      map[string]*ZPoolVDEV  `json:"vdevs"`
}

type ZPool struct {
	z *zpool `json:"-"`

	Name       string     `json:"name"`
	Type       string     `json:"type"`
	State      ZPoolState `json:"state"`
	PoolGUID   string     `json:"pool_guid"`
	TXG        string     `json:"txg"`
	SPAVersion string     `json:"spa_version"`
	ZPLVersion string     `json:"zpl_version"`

	Size          uint64  `json:"size"`
	Free          uint64  `json:"free"`
	Alloc         uint64  `json:"allocated"`
	Fragmentation float64 `json:"fragmentation"`
	DedupRatio    float64 `json:"dedup_ratio"`

	Properties map[string]ZFSProperty `json:"properties"`
	Vdevs      map[string]*ZPoolVDEV  `json:"vdevs"`
}
*/
export const ZpoolVdevSchema: z.ZodType<any> = z.lazy(() =>
	z.object({
		name: z.string(),
		vdev_type: z.string(), // see next point
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
);

export const ZpoolSchema = z.object({
	name: z.string(),
	type: z.string(),
	state: z.string(),
	pool_guid: z.string(),
	txg: z.string(),
	spa_version: z.string(),
	zpl_version: z.string(),
	properties: z.record(z.string(), ZpoolPropertySchema),
	vdevs: z.record(z.string(), ZpoolVdevSchema)
});

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
export type Zpool = z.infer<typeof ZpoolSchema>;
export type ReplaceDevice = z.infer<typeof ReplaceDeviceSchema>;
export type CreateZpool = z.infer<typeof CreateZpoolSchema>;
export type ZpoolRaidType = z.infer<typeof ZpoolRaidTypeSchema>;
export type PoolStatPointsResponse = z.infer<typeof PoolStatPointsResponseSchema>;
export type PoolsDiskUsage = z.infer<typeof PoolsDiskUsageSchema>;
