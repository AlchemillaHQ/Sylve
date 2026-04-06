import { z } from 'zod/v4';

const nullableString = z
	.string()
	.nullish()
	.transform((value) => value ?? '');

const nullableStringArray = z
	.array(z.string())
	.nullish()
	.transform((value) => value ?? []);

const natTypeSchema = z
	.string()
	.nullish()
	.transform((value) => {
		const normalized = (value ?? '').trim().toLowerCase();
		if (normalized === 'dnat') return 'dnat' as const;
		if (normalized === 'binat') return 'binat' as const;
		return 'snat' as const;
	});

const translateModeSchema = z
	.string()
	.nullish()
	.transform((value) =>
		(value ?? '').trim().toLowerCase() === 'address' ? ('address' as const) : ('interface' as const)
	);

export const FirewallTrafficRuleSchema = z.object({
	id: z.number().int(),
	name: z.string(),
	description: nullableString,
	visible: z.boolean().nullish().transform((value) => value ?? true),
	enabled: z.boolean().optional().default(true),
	log: z.boolean().nullish().transform((value) => value ?? false),
	quick: z.boolean().nullish().transform((value) => value ?? false),
	priority: z.number().int(),
	action: z.enum(['pass', 'block']),
	direction: z.enum(['in', 'out']),
	protocol: z.enum(['any', 'tcp', 'udp', 'icmp']),
	ingressInterfaces: nullableStringArray,
	egressInterfaces: nullableStringArray,
	family: z.enum(['any', 'inet', 'inet6']).optional().default('any'),
	sourceRaw: nullableString,
	sourceObjId: z.number().int().nullable().optional().default(null),
	destRaw: nullableString,
	destObjId: z.number().int().nullable().optional().default(null),
	srcPortsRaw: nullableString,
	srcPortObjId: z.number().int().nullable().optional().default(null),
	dstPortsRaw: nullableString,
	dstPortObjId: z.number().int().nullable().optional().default(null),
	createdAt: z.string(),
	updatedAt: z.string()
});

export const FirewallNATRuleSchema = z.object({
	id: z.number().int(),
	name: z.string(),
	description: nullableString,
	visible: z.boolean().nullish().transform((value) => value ?? true),
	enabled: z.boolean().optional().default(true),
	log: z.boolean().nullish().transform((value) => value ?? false),
	priority: z.number().int(),
	natType: natTypeSchema,
	policyRoutingEnabled: z.boolean().nullish().transform((value) => value ?? false),
	policyRouteGateway: nullableString,
	ingressInterfaces: nullableStringArray,
	egressInterfaces: nullableStringArray,
	family: z.enum(['any', 'inet', 'inet6']).optional().default('any'),
	protocol: z.enum(['any', 'tcp', 'udp', 'icmp']),
	sourceRaw: nullableString,
	sourceObjId: z.number().int().nullable().optional().default(null),
	destRaw: nullableString,
	destObjId: z.number().int().nullable().optional().default(null),
	translateMode: translateModeSchema,
	translateToRaw: nullableString,
	translateToObjId: z.number().int().nullable().optional().default(null),
	dnatTargetRaw: nullableString,
	dnatTargetObjId: z.number().int().nullable().optional().default(null),
	dstPortsRaw: nullableString,
	dstPortObjId: z.number().int().nullable().optional().default(null),
	redirectPortsRaw: nullableString,
	redirectPortObjId: z.number().int().nullable().optional().default(null),
	createdAt: z.string(),
	updatedAt: z.string()
});

export const FirewallAdvancedSettingsSchema = z.object({
	id: z.number().int(),
	preRules: nullableString,
	postRules: nullableString,
	createdAt: z.string(),
	updatedAt: z.string()
});

export const FirewallTrafficRuleCounterSchema = z.object({
	id: z.number().int(),
	packets: z.number().int().nonnegative(),
	bytes: z.number().int().nonnegative(),
	updatedAt: z.string()
});

export const FirewallNATRuleCounterSchema = z.object({
	id: z.number().int(),
	packets: z.number().int().nonnegative(),
	bytes: z.number().int().nonnegative(),
	updatedAt: z.string()
});

export const FirewallLiveHitEventSchema = z.object({
	cursor: z.number().int().nonnegative(),
	timestamp: z.string(),
	ruleType: z.enum(['traffic', 'nat']),
	ruleId: z.number().int().nonnegative(),
	ruleName: z.string().nullish().transform((value) => value ?? ''),
	action: nullableString,
	direction: nullableString,
	interface: nullableString,
	bytes: z.number().int().nonnegative(),
	rawLine: nullableString
});

export const FirewallLiveHitsResponseSchema = z.object({
	items: FirewallLiveHitEventSchema.array(),
	nextCursor: z.number().int().nonnegative(),
	sourceStatus: z.enum(['ok', 'unavailable']).default('unavailable'),
	sourceError: nullableString,
	updatedAt: z.string()
});

export type FirewallTrafficRule = z.infer<typeof FirewallTrafficRuleSchema>;
export type FirewallNATRule = z.infer<typeof FirewallNATRuleSchema>;
export type FirewallAdvancedSettings = z.infer<typeof FirewallAdvancedSettingsSchema>;
export type FirewallTrafficRuleCounter = z.infer<typeof FirewallTrafficRuleCounterSchema>;
export type FirewallNATRuleCounter = z.infer<typeof FirewallNATRuleCounterSchema>;
export type FirewallLiveHitEvent = z.infer<typeof FirewallLiveHitEventSchema>;
export type FirewallLiveHitsResponse = z.infer<typeof FirewallLiveHitsResponseSchema>;
