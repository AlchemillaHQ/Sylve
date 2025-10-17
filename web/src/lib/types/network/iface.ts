import { z } from 'zod/v4';

export const FlagsSchema = z.object({
	raw: z.number(),
	desc: z.array(z.string()).nullable().default([])
});

export const IPv4RCSchema = z.object({
	id: z.number(),
	interfaceId: z.number(),
	protocol: z.string(),
	address: z.string().nullable().optional(),
	netmask: z.string().nullable().optional(),
	options: z.string().default(''),
	isAlias: z.boolean().default(false)
});

export const IPv6RCSchema = z.object({
	id: z.number(),
	interfaceId: z.number(),
	protocol: z.string(),
	address: z.string().nullable().optional(),
	options: z.string().default(''),
	prefixLength: z.number().nullable().optional(),
	isAlias: z.boolean().default(false)
});

export const IPv4Schema = z.object({
	ip: z.string(),
	netmask: z.string(),
	broadcast: z.string()
});

export const IPv6Schema = z.object({
	ip: z.string(),
	prefixLength: z.number(),
	scopeId: z.number(),
	autoConf: z.boolean(),
	detached: z.boolean(),
	deprecated: z.boolean(),
	lifeTimes: z.object({
		preferred: z.number(),
		valid: z.number()
	})
});

export const MediaSchema = z.object({
	type: z.string(),
	subtype: z.string(),
	options: z.array(z.string()).nullable().default([]),
	mode: z.string(),
	rawCurrent: z.number(),
	rawActive: z.number(),
	status: z.string()
});

export const ND6Schema = FlagsSchema;

export const BridgeMemberSchema = z.object({
	name: z.string()
});

export const IfaceSchema = z.object({
	name: z.string(),
	ether: z.string(),
    hwaddr: z.string().default(''),
	flags: FlagsSchema,
	mtu: z.number().nullable().optional(),
	metric: z.number().nullable().optional(),
	capabilities: z.object({
		enabled: FlagsSchema,
		supported: FlagsSchema
	}),
	driver: z.string().default(''),
	model: z.string().default(''),
	description: z.string().default(''),
	ipv4: z.array(IPv4Schema).default([]).nullable().optional(),
	ipv6: z.array(IPv6Schema).default([]).nullable().optional(),
	media: MediaSchema.nullable().optional(),
	nd6: ND6Schema.nullable().optional(),
	groups: z.array(z.string()).default([]).nullable().optional(),
	bridgeMembers: z.array(BridgeMemberSchema).default([]).nullable().optional()
});

export type Iface = z.infer<typeof IfaceSchema>;
export type BridgeMember = z.infer<typeof BridgeMemberSchema>;
